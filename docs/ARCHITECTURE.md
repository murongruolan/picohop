# ChainProxy 架构设计

> 依据：[`RESEARCH_chain_proxy.md`](./RESEARCH_chain_proxy.md) 的研究结论。
> 语言：**Go 1.24**（选型理由见研究报告第 4.3 节）。

---

## 1. 一句话架构

```
浏览器
  │  SOCKS5 或 HTTP(明文代理请求) 或 HTTP CONNECT，指向 127.0.0.1:10810
  ▼
┌──────────────────────────────────────────────────────────────┐
│ ChainProxy 进程                                              │
│                                                              │
│  [MixedListener :10810]                                      │
│        │  解析 SOCKS5 / HTTP，二选一（首字节嗅探）             │
│        ▼                                                     │
│  target = 目标地址（域名或 IP:Port）                          │
│        │                                                     │
│        ├─ Front.Dial(target=住宅代理地址) ─────────────────┐  │
│        │      · socks5 前置：TCP 连前置 → SOCKS5 CONNECT  │  │
│        │      · http  前置：TCP 连前置 → CONNECT          │  │
│        │      · direct 前置：裸 TCP 连                     │  │
│        │                                                  ▼  │
│        │      ┌──────────────────────────────────────┐      │
│        │      │ 住宅代理连接（跑在前置隧道内部）      │      │
│        │      │ socks5 上游：第二次 SOCKS5 握手       │      │
│        │      │ http   上游：CONNECT + Proxy-Auth     │      │
│        │      │ wireguard 上游：gvisor netstack 出栈   │      │
│        │      └──────────────────────────────────────┘      │
│        │ 拨号成功后得到 net.Conn（= fd_residential）        │
│        ▼                                                     │
│  [Upstream.Connect(住宅代理conn, target)]  ← 第三次握手       │
│        ▼                                                     │
│  relay(clientConn ↔ targetConn)  双向搬运 + 半关闭           │
└──────────────────────────────────────────────────────────────┘
```

**关键点**：从 `Front.Dial` 返回的那一刻起，Byte 层面已经处于"前置隧道内部"，
所以后续的 `Upstream.Connect` 只是在这条已经加密/中继过的流上再写一段 SOCKS5/HTTP 报文。
前置代理对这些字节**完全不透明**。

---

## 2. 包划分

| 包 | 路径 | 职责 |
|---|---|---|
| `config` | `internal/config` | 配置结构、默认值、加载/保存、校验（端口冲突等） |
| `proxy` | `internal/proxy` | 协议实现核心：SOCKS5/HTTP 客户端与服务端、双向中继 |
| `netutil` | `internal/netutil` | 端口探测、地址解析、超时工具 |
| `app` | `internal/app` | 应用状态机：启停、状态、统计、测试编排 |
| `wg` | `internal/wg` | WireGuard 上游（wireguard-go + gvisor netstack + SOCKS5 UDP 外层） |
| `gui` | `internal/gui` | Win32 原生窗口（仅 Windows 构建） |
| `tester` | `internal/tester` | 一键测试：探测出口 IP / 延迟 / 各跳连通性 |
| `main` | `cmd/chainproxy` | 入口：CLI 子命令 + GUI 启动 |

---

## 3. 核心接口

### 3.1 前置代理 `proxy.Front`

```go
// Front 负责"经由前置代理把一条 TCP 连接到某个地址"。
type Front interface {
    // DialTCP 通过前置代理连接到 addr（host:port）。
    DialTCP(ctx context.Context, addr string) (net.Conn, error)
    // DialUDP 通过前置代理建立 UDP 通道（仅 socks5 前置支持；WireGuard 上游需要）。
    DialUDP(ctx context.Context, target string) (UDPChannel, error)
    // Describe 返回可读描述，用于 GUI 状态展示。
    Describe() string
    Close() error
}
```

实现：
- `socks5Front` —— RFC 1928/1929，支持用户名密码认证、UDP ASSOCIATE；
- `httpFront` —— RFC 7230 CONNECT + Proxy-Authorization（无 UDP 能力）；
- `directFront` —— 不使用前置，直连（用于对照测试与特殊场景）。

### 3.2 上游（住宅出口）`proxy.Upstream`

```go
// Upstream 负责在一条"已经连通前置"的连接上，建立到最终目标的连接。
type Upstream interface {
    // Connect 在 conn（已由前置隧道打通）之上完成到 addr 的第二次握手。
    // 返回的 net.Conn 已经"就绪"，可以直接读写目标流量。
    Connect(ctx context.Context, conn net.Conn, addr string) (net.Conn, error)
    // Describe 返回可读描述。
    Describe() string
}
```

实现：
- `socks5Upstream` —— 第二次 SOCKS5 握手（可带用户名密码）；
- `httpUpstream` —— 第二次 `CONNECT host:port` + `Proxy-Authorization`；
- `wgUpstream`（`internal/wg`）—— 通过 netstack 直接拨号，不走 SOCKS 握手。

> 这两个接口的**分离**是整个设计的核心：
> `Front` 只关心"怎么到达住宅代理的服务器地址"，
> `Upstream` 只关心"怎么在这条流里抵达最终目标"。
> 二者正交，因此"换前置"和"换住宅"互不影响，也天然支持多层嵌套。

### 3.3 监听器 `proxy.MixedListener`

对外暴露的本地端口，同时接受 **SOCKS5** 与 **HTTP** 两种客户端协议（"mixed / 混合"模式）：

```
新连接 → 读首字节
   0x05           → SOCKS5 服务端流程
   0x50 'P' ...   → "POST/GET/PUT/CONNECT" 开头 → HTTP 服务端流程
```

- **SOCKS5 服务端**：greeting(0x05) → 可选 user/pass auth(0x02) → request(CMD_CONNECT) → 双向中继。
  另外实现 **UDP ASSOCIATE**：客户端从返回的 relay 地址发 UDP，由我们再经住宅 SOCKS5 的 UDP 通道转发（仅 socks5/http 上游；http 上游不支持则明确回 0x07）。
- **HTTP 服务端**：
  - `CONNECT host:port` → 回 `HTTP/1.1 200 Connection established` → 双向中继；
  - 普通绝对 URI 请求（`GET http://host/path`）→ 改写为 origin-form 并加上我们自己的 `Host`，
    经链路转发，保留 keep-alive（同一 host:port 复用上游连接）。

### 3.4 中继 `proxy.Relay`

```go
func Relay(client, remote net.Conn, stats *Stats, idle time.Duration) error
```

- 双向 `io.Copy`；
- 每个方向单独 goroutine；一侧 EOF 时对另一侧 `CloseWrite()`（TCP 半关闭），
  让对方知道"我这边发完了"，同时保留另一方向继续传输；
- 双向都结束后关闭两条连接；
- 统计上/下行字节数；
- `idle` 空闲超时（默认 300s，可配置）。

---

## 4. 端口生命周期管理（用户明确要求）

### 4.1 三重校验（启动前）

1. **格式校验**：端口 ∈ [1, 65535]，非本项目已占用的端口不能等于前置代理端口；
2. **重复校验**：任意两条上游的 `listenPort` 不能相同（含监听地址不同的情形也按端口去重，
   保守起见按端口全局唯一，避免 `0.0.0.0:10810` 与 `127.0.0.1:10810` 之类的语义冲突）；
3. **可用性校验**：`net.Listen("tcp", host:port)` 试绑定。Windows 上 Go 默认**不开**
   `SO_EXCLUSIVEADDRUSE` 之外的复用语义，因此试绑定成功即代表可用。

### 4.2 占用与释放

| 时机 | 行为 |
|---|---|
| 应用启动 | 若 `startOnLaunch` 为真，自动绑定所有 `enabled` 上游的端口并开始服务 |
| 手动开关 | 主开关切换：开 = 绑定并服务；关 = 停止接受新连接 + 关闭所有活动连接 |
| 应用退出 | 关闭全部 listener，**等待所有连接结束后进程退出**（最多 3s） |
| 应用崩溃 | 监听 socket 随进程退出由 OS 自动释放（Windows 上不会有 TIME_WAIT 占用监听端口的问题） |
| 端口被外部占用 | 启动时明确报错，指明**哪个名字的哪个端口被占用**，该条上游标记为 `error` 而不影响其他条 |

### 4.3 单实例保护

- PID 文件 `<data>/chainproxy.pid`，记录 PID 与启动时间；
- 启动时若发现 PID 文件且进程仍存在 → 提示"程序已在运行"并退出；
- 正常退出时删除；发现是陈旧 PID 文件则直接覆盖。

### 4.4 不做自启动

程序**不注册** Windows 启动项、不写 Run 键、不写计划任务、不安装服务。
代码里没有任何自启动相关调用，文档中也显式声明。

---

## 5. WireGuard 上游设计

### 5.1 为什么能做到"WireGuard 跑在前置代理里面"

WireGuard 是**外层 UDP**协议。要让它经过主流客户端，只有两条路：

| 方案 | 说明 | 本项目选择 |
|---|---|---|
| A. 改系统路由 | 起 WireGuard 隧道（需 wintun 驱动 + 管理员权限），再用路由规则把 WG 端点导进主流客户端的 TUN | ❌ 侵入系统、需要驱动和权限、依赖主流客户端开 TUN 模式 |
| **B. 纯用户态套娃** | wireguard-go 在用户态跑整个协议栈，外层 UDP 通过**前置代理的 SOCKS5 UDP ASSOCIATE** 送出，内层用 gvisor netstack 出栈 | ✅ **采用** |

方案 B 的三件套：

```
[ gvisor netstack ]  ← 提供纯用户态 TCP/IP 栈（不需要 TUN 驱动）
        ▲ 读写 IP 包
[  wireguard-go device  ]  ← WireGuard 加解密 + 握手 + 会话
        │  Bind 接口：收发"加密后的 UDP 包"
[ SocksUDPBind（自研）  ]  ← 把 WG 的 UDP 包封成 SOCKS5 UDP 请求头，
        │                     发给前置代理的 UDP relay；收到的包解封后回调设备
[主流客户端:10808 UDP relay ]
```

- **无驱动、无管理员权限、不改动系统路由表、不影响其他程序** —— 和 ChainProxy 其它部分一样的"纯用户态"哲学；
- 需要前置代理支持 **UDP**（主流客户端/Xray 的 socks 入站默认开启 UDP）；
- 若前置是 HTTP 或直连、或前置不支持 UDP，WireGuard 上游会**明确报错**而不是静默失败。

### 5.2 `SocksUDPBind` 要实现的接口

`wireguard-go/device.Bind`：

```go
type Bind interface {
    io.Closer
    SetMark(mark uint32) error
    Send(bufs [][]byte, sizes []int, eps []device.Endpoint) error
    ParseEndpoint(s string) (device.Endpoint, error)
    BatchSize() int
}
```

`Send` 把每个加密包按 RFC 1928 §7 封帧：
`RSV(0x0000) | FRAG(0x00) | ATYP | DST.ADDR | DST.PORT | DATA`，发给 relay；
接收循环解封后调用 `device.RoutineReceiveIncoming()` 交回设备。
需要额外实现 `device.ReceiveFunc` 注册与关闭时的解绑。

### 5.3 DNS 策略差异（重要）

| 上游类型 | 域名解析位置 | 原因 |
|---|---|---|
| socks5 / http | **交给住宅代理解析**（把域名原样发出去） | 住宅代理服务器在境外，本地 DNS 常常污染/不可用 |
| wireguard | **本地解析后再连 IP** | gvisor netstack 内不跑完整 DNS 客户端；且 WG 隧道内已可用公网 DNS，但为简单可靠起见先本地解析 |

> 后续若需要，可为 WG 上游增加"隧道内 DNS"选项（把 DNS 指向隧道内的 `1.1.1.1` 等）。

---

## 6. 状态模型

每个上游有一条实时状态（GUI 中每行显示）：

| 状态 | 含义 | 颜色 |
|---|---|---|
| `stopped` | 已配置但未启用，或主开关关闭 | 灰 |
| `starting` | 正在建隧道 | 黄（闪烁） |
| `running` | 监听中，端口可用 | 绿 |
| `error` | 端口被占用 / 配置非法 / 前置不可达 | 红（鼠标悬停或点击显示原因） |

测试（`tester` 包）分三层，逐层定位问题：

1. **前置测试**：直接经前置访问 `https://api.ipify.org` → 拿到前置出口 IP；
2. **链路测试**：经本项目的 `127.0.0.1:10810` 访问同一地址 → 拿到住宅出口 IP；
3. **对比判定**：若两个 IP 不同 → ✅ 链式代理生效（这正是我们想要的）；
   若相同 → ⚠️ 流量没有走住宅代理（多半是住宅代理不支持 TCP 转发或认证失败）。

---

## 7. 目录与构建

```
F:\chain_proxy\
├── cmd\chainproxy\          # 入口
├── internal\
│   ├── app\                 # 应用状态机
│   ├── config\              # 配置
│   ├── gui\                 # Win32 GUI（build tag: windows）
│   ├── netutil\             # 端口/地址工具
│   ├── proxy\               # 协议核心
│   ├── tester\              # 一键测试
│   └── wg\                  # WireGuard 上游
├── scripts\                 # env.ps1 / build.ps1 / run.ps1 / test_e2e.ps1
├── docs\                    # 全部文档
├── refs\                    # 参考项目（gitignored）
├── dist\                    # 编译产物（gitignored）
└── data\                    # 运行时配置与日志（gitignored）
```

所有 Go 缓存通过 `scripts\env.ps1` 重定向到 `F:\chain_proxy\.cache\`，
**不会在 `C:\Users\...\go` 或任何外部目录写入**。