# 提交记录说明（COMMIT_HISTORY）

> 这个文档的用途：**几个月后回来还能看懂自己每一行代码是怎么来的、为什么这么写。**
>
> 维护约定：
> 1. 每一次 `git commit` 之后，**必须**在本文档顶部的"提交索引"里新增一行；
> 2. 索引行的链接指向下方对应的详细章节；
> 3. 详细章节必须写清楚：**改了什么文件、为什么改、关键设计取舍、验证方式、已知遗留**；
> 4. 重构类提交要写清"把什么搬到了哪里、为什么搬"；
> 5. 修复类提交要写清"什么现象、根因、改法、如何确认不再复现"。

---

## 提交索引

| # | commit | 日期 | 一句话说明 | 详细 |
|---|---|---|---|---|
| 001 | `chore: 初始化项目骨架与参考项目研究` | 2026-10-10 | 建目录、装 git、拉取主流客户端/主流客户端、产出链式代理研究报告与架构设计 | [详情](#001) |
| 002 | `feat(core): 实现链式代理核心引擎并通过端到端实测` | 2026-10-10 | Front/Upstream 双接口 + mixed 监听器 + 端口管理 + CLI，真实住宅代理实测通过 | [详情](#002) |

---

## 001

**commit**: `chore: 初始化项目骨架与参考项目研究`

**目的**：在写第一行代码之前，先把"链式代理到底是怎么实现的"研究清楚，
并把结论固化成文档；同时把项目目录、依赖隔离策略和 git 规范定下来。

### 新增文件

| 文件 | 说明 |
|---|---|
| `.gitignore` | 排除 `.cache/`、`dist/`、`data/`、`refs/local-refs/`、`refs/local-refs/` 等，保证本仓库只提交源码与文档 |
| `scripts/env.ps1` | **依赖隔离核心**：把 `GOPATH` / `GOMODCACHE` / `GOCACHE` / `GOTMPDIR` 全部重定向到 `F:\chain_proxy\.cache\`，并设置 `GOPROXY=goproxy.cn,direct`、`CGO_ENABLED=0`。执行本项目任何构建前先加载它，即可保证**不在 C 盘用户目录或其他位置留下任何缓存** |
| `refs/README.md` | 登记两个参考项目的仓库地址、拉取时间、HEAD commit，并给出重新拉取的命令 |
| `docs/RESEARCH_chain_proxy.md` | **核心研究成果**：主流客户端（`BuildChainOutboundsList` / `sockopt.dialerProxy` / sing-box `detour`）与主流客户端（`IRuntime::update_proxy_chain_config` / mihomo `dialer-proxy`）的实现剖析，以及 Python/C++/Go 三选一的评估 |
| `docs/ARCHITECTURE.md` | 架构设计：接口定义、端口生命周期、WireGuard 用户态方案、目录结构 |
| `README.md` | 项目介绍与快速开始 |
| `docs/PROJECT_PROGRESS.md` | 开发进度跟踪表 |
| `docs/COMMIT_HISTORY.md` | 本文件 |

### 关键结论（写在这里，防止以后忘记为什么这么设计）

1. **链式代理的本质是"套接字逐跳创建"**，不是新协议，也不需要住宅代理配合改造。
   它就是：在一条"已经被前置代理隧道化"的 TCP 流里，再发起一次 SOCKS5 / HTTP CONNECT 握手。
2.主流客户端与主流客户端**做法完全同构**，只是字段名不同
   （Xray `streamSettings.sockopt.dialerProxy` / sing-box `detour` / mihomo `dialer-proxy`），
   语义都是"本跳的 socket 由上一跳代为创建"。
3. 两个项目都**不自己转发**，转发交给 Xray / sing-box / mihomo 内核；
   本项目不同 —— **自己用 Go 写出这一层转发**，从而做到单文件、无内核、无需重启即可改配置。
4. **选 Go 而不是 Python / C++**：单文件静态 exe 零依赖、goroutine 天然匹配
   "一客户端连接 = 一条三级链"、依赖可完全隔离在项目目录内、WireGuard 纯用户态实现可直接静态编译。
5. **每个上游绑定一个独立本地端口**，而不是像主流客户端那样用路由 tag —— 对普通用户来说
   "浏览器填哪个端口"比"选哪个 tag"直观得多。

### 参考项目版本

| 项目 | HEAD commit |
|---|---|
|主流客户端| `b501efbc45bca3c465df99af7d4ec092c2f4c40f` |
|主流客户端| `44477d97d4f568e8016d6a2b9a0001d52d8f56a1` |

### 验证方式

- `git init` 后 `git status` 干净；
- `scripts/env.ps1` 执行后 `go env GOMODCACHE` 指向 `F:\chain_proxy\.cache\gomod`；
- 本次提交不含任何编译产物与缓存。

### 已知遗留

- 尚未写任何代码；下一步是核心协议实现（见 `docs/PROJECT_PROGRESS.md` 的任务 2）。

---

## 002

**commit**: `feat(core): 实现链式代理核心引擎并通过端到端实测`

**目的**：把研究报告里的结论真正写成可运行的代码——用两个正交接口
（`Front` 管"怎么到住宅代理的服务器"，`Upstream` 管"怎么抵达最终目标"）
拼出三级套接字链，并用真实住宅代理验证。

### 新增文件

| 文件 | 说明 |
|---|---|
| `internal/config/config.go` | 配置模型（Front / Upstream / WireGuardConfig）、默认值、原子保存、`Validate()` 全量校验、`ParseProxySpec()` 解析 `user:pass@host:port` |
| `internal/config/util.go` | 拆出 `randRead` / `nowNano`，让 `NewID` 无额外依赖 |
| `internal/config/config_test.go` | 单测：代理地址解析、端口重复校验、与前置端口冲突校验 |
| `internal/netutil/netutil.go` | `PortAvailable` 试绑定探测、`FirstFreePort`、`LocalIP` |
| `internal/proxy/socks5.go` | RFC 1928/1929 编解码：地址类型、greeting、用户名密码认证、CONNECT、UDP ASSOCIATE、UDP 封帧、应答码中文翻译 |
| `internal/proxy/socks5_test.go` | 单测：地址编解码往返、客户端报文格式（含 10 字节应答的字节级校验）、UDP 封帧 |
| `internal/proxy/helpers_test.go` | 测试用短别名 |
| `internal/proxy/http.go` | HTTP CONNECT 客户端 + 极简 HTTP 头解析器（供代理服务端转发使用） |
| `internal/proxy/front.go` | `Front` 接口 + socks5 / http / direct 三种实现；`socks5UDPChannel` UDP 通道 |
| `internal/proxy/upstream.go` | `Upstream` 接口 + socks5 / http 实现；`RegisterWireGuardFactory` 为 WG 预留注入点 |
| `internal/proxy/relay.go` | `Relay()` 双向搬运 + TCP 半关闭 + 空闲超时 + 流量统计 |
| `internal/proxy/handler.go` | mixed 监听核心：首字节嗅探分派、SOCKS5 服务端（含 UDP ASSOCIATE）、HTTP 服务端（CONNECT + 明文请求） |
| `internal/proxy/server.go` | `Server`：绑定端口、接受连接、跟踪活动连接、停止时释放端口 |
| `internal/app/app.go` | 应用状态机：配置 CRUD、启停、状态行、错误标记 |
| `internal/app/test.go` | 分层测试编排（前置 / 单条链路） |
| `internal/tester/tester.go` | 出口 IP 与延迟探测，支持 `api.ipify.org` JSON 与纯文本两种响应 |
| `internal/gui/gui.go`、`gui_windows.go`、`gui_other.go` | GUI 包骨架（当前为占位实现，下一次提交替换为真正的窗口） |
| `cmd/chainproxy/main.go` | 入口与 CLI：`gui` / `run` / `test` / `validate` / `version` |
| `scripts/build.ps1` | 构建脚本，自动加载 `env.ps1` |
| `scripts/test_e2e.ps1` | 端到端测试脚本 |

### 关键设计取舍

1. **为什么是 `Front` + `Upstream` 两个接口而不是一个大接口**
   住宅链路的"第一跳"和"第二跳"在协议上完全正交：换前置不影响住宅，换住宅不影响前置。
   分开后 WireGuard 这种"没有第二次握手"的上游也能塞进同一个模型
   （它在 `Start()` 时就把隧道建好，`Dial()` 直接出栈）。
2. **mixed 监听器用首字节嗅探而不是让用户选协议**
   `0x05` 判定为 SOCKS5，其余按 HTTP 处理。主流客户端的 10808 就是这么做的，
   用户因此不需要为同一个端口配置两遍。
3. **明文 HTTP 请求每连接只处理一个**，并对上游声明 `Connection: close`。
   真实流量几乎全是 HTTPS→CONNECT（走全双工隧道），为明文路径做 keep-alive
   状态机会显著增加出错面而不带来实际收益。这是有意的简化，写在这里以免以后"优化"回去。
4. **`Relay` 用 `CloseWrite()` 半关闭**而不是"一边 EOF 就关两边"。
   否则"客户端发完请求、还想继续读响应"这类正常场景会被误杀。
5. **端口校验三重**：格式 → 重复 → 与前置端口冲突 → 试绑定。
   与前置端口相同会造成"自己连自己"的死循环，必须单独拦。
6. **单条链路启动失败不影响其它链路**：每条上游的端口独立绑定，
   失败只把该行标红。这直接对应需求里的"可以添加多个、端口不能重复、互不影响"。

### 本次修掉的一个真实 bug

第一次连真实住宅代理时全部失败，报 `SOCKS5 绑定地址读取失败: 未知地址类型 0x00`。
用 PowerShell 裸 socket 抓主流客户端的应答字节：

```
GREETING REPLY (2 bytes):  05 00
CONNECT  REPLY (10 bytes): 05 00 00 01 00 00 00 00 2a 38
```

根因：`socks5Command` 把应答头读成 **4 字节**（VER REP RSV **ATYP**），
然后又调用 `readSocks5Addr` **再读一次 ATYP**，导致地址整体右移一位，
读到的是 `BND.ADDR` 的第一个字节 `0x00` 当成 ATYP。
正确的分隔是：应答头只有 **3 字节**（VER REP RSV），ATYP 属于 ADDR 的一部分。
服务端侧 `serveSOCKS5` 的请求头有同样的错误，一并修正。
`socks5_test.go` 里的字节级用例就是为防止复发而加的。

### 验证方式

```
$ .\scripts\test_e2e.ps1
[0] 前置出口 IP = 103.115.108.31
[PASS] 端口 10810 已占用 / 端口 10811 已占用
[PASS] SOCKS5 链路出口 IP 不同于前置   (103.115.108.31 → 70.170.56.16)
[PASS] HTTP   链路出口 IP 不同于前置   (103.115.108.31 → 69.119.250.84)
[PASS] 第二条链路出口 IP 不同于前置   (103.115.108.31 → 107.212.213.11)
[PASS] 明文 HTTP 请求经链路可达       (65.189.6.232)
[PASS] 端口 10810 / 10811 已释放
[PASS] 重启后端口可重新占用，链路仍可用
=== 全部通过 ===
```

`go test ./...` 与 `go vet ./...` 均通过。

### 已知遗留

- GUI 目前是占位实现（`runNative` 未注册），双击 exe 会退回命令行模式 → 下一次提交。
- WireGuard 上游未实现，`RegisterWireGuardFactory` 还没有注册者 → 再下一次提交。
- 明文 HTTP 路径不支持 chunked 请求体（返回 501）——目前没有客户端会这么用。
- 住宅代理是动态 IP，测试中每次请求出口都不同，属预期现象。