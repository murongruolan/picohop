# 研究报告：主流客户端/主流客户端的"链式代理"实现逻辑

> 研究目的：搞清楚主流代理客户端是怎么把 `前置代理 → 住宅代理 → 目标站点` 这条链路搭起来的，
> 然后用一种可自己掌控的轻量语言重新实现一遍。
>
> 研究对象、仓库与 commit 见 [`../refs/README.md`](../refs/README.md)。

---

## 0. 结论先行

**"链式代理"（也叫前置代理、Pre-proxy、dialer chain）不是一种新的代理协议，
而是一种"套接字逐跳创建"的转发拓扑。**

标准代理协议（SOCKS5 / HTTP CONNECT）只会告诉你"我帮你连到目标地址"。
链式代理做的事情是：**当我要连第 N 跳服务器时，不要从本地直连，
而是先通过第 N-1 跳已经建好的连接，再从第 N-1 跳内部发起到第 N 跳的连接。**

数学上就是套接字的嵌套：

```
普通代理：  fd_client  ──直接 connect()──>  fd_target
链式代理：  fd_client ──> fd_front ──> fd_residential ──> fd_target
```

对应的报文序列（以最常见的 SOCKS5 为例）：

```
1. 浏览器            ──SOCKS5 greeting──>          PicoHop(10810)         [本地监听]
2. PicoHop(10810) ──TCP connect──>                前置代理(127.0.0.1:10808)  [主流客户端]
3. PicoHop        ──SOCKS5 CONNECT 前置代理──>     前置代理
4. 前置代理          ──在其隧道内──>                   住宅代理(如 hk.xxx.com:10000)
5. PicoHop        ──SOCKS5/auth+CONNECT 住宅代理──> 住宅代理（这就是"第二次握手"）
6. 住宅代理          ──> 目标站点
7. 之后全部是裸 TCP 双向搬运
```

**第 5 步是整件事的核心与全部难点**：在一条"已经被代理过的"TCP 流之上，
再套一层 SOCKS5 / HTTP CONNECT 握手。前置代理只是搬运字节流，它根本不知道
你在里面又开了一条到住宅代理的连接 —— 对它而言这就是普通的隧道内负载。

---

## 1.主流客户端的实现

### 1.1 定位

主流客户端是 **C# WPF 客户端**，自身**不做任何转发**。它的职责是：

1. 让用户选择一串节点，构成"链"；
2. 把这条链翻译成 Xray / sing-box 的 **outbound 配置**；
3. 启动内核（xray.exe / sing-box.exe），由内核完成真正的逐跳拨号。

所以主流客户端的"链式代理实现"= **配置生成器**。

### 1.2 核心代码位置

| 文件 | 作用 |
|---|---|
| `主流客户端/同类实现/Services/CoreConfig/主流客户端/主流客户端OutboundService.cs` → `BuildChainOutboundsList()` | 为 Xray 生成链式 outbound |
| 同上 → `FillDialerProxy()` | 把 dialerProxy 字段写进出站 |
| `主流客户端/同类实现/Services/CoreConfig/Singbox/SingboxOutboundService.cs` → `BuildChainOutboundsList()` | 为 sing-box 生成链式 outbound（字段名不同：`detour`） |
| `主流客户端/同类实现/Models/CoreConfigs/主流客户端Config.cs` | `sockopt.dialerProxy` 字段定义 |
| `主流客户端/同类实现/Resx/ResUI.zh-Hans.resx` | UI 文案："链式代理"、"添加链式代理"、"前置代理配置别名" |

### 1.3 算法（`主流客户端OutboundService.cs:692-797`）

用户选择的节点顺序是 `[A, B, C]`，语义是 **A 最前、C 最后**。

代码先反转：`nodesReverse = [C, B, A]`。
注释写得很清楚：`// Based on actual network flow instead of data packets`
（按真实的数据流向排列，而不是按用户勾选顺序）。

然后从出口往回逐个生成 outbound：

```csharp
for (var i = 0; i < nodesReverse.Count; i++)
{
    var node = nodesReverse[i];                                   // C, B, A
    var currentTag     = i == 0 ? baseTagName                      // 出口拿"主 tag"（proxy）
                                 : $"chain-{baseTagName}-{i}-{node.Remarks}";
    var dialerProxyTag = i != nodesReverse.Count - 1               // 不是最后一跳就挂 dialerProxy
                                 ? $"chain-{baseTagName}-{i+1}-{nodesReverse[i+1].Remarks}"
                                 : null;

    var outbound = BuildProxyOutbound(node);
    outbound.tag = currentTag;
    if (!dialerProxyTag.IsNullOrEmpty())
        FillDialerProxy(outbound, dialerProxyTag);                  // ← 关键一行
    resultOutbounds.Add(outbound);
}

static void FillDialerProxy(Outbounds4Ray outbound, string dialerProxyTag)
{
    outbound.streamSettings ??= new();
    outbound.streamSettings.sockopt ??= new();
    outbound.streamSettings.sockopt.dialerProxy = dialerProxyTag;   // ← 核心
}
```

展开到 `[A, B, C]` 三个节点，生成的 outbound 集合是：

| tag | 协议 | `sockopt.dialerProxy` | 含义 |
|---|---|---|---|
| `proxy` | C（住宅节点） | `chain-proxy-1-B` | 连 C 时，**先通过 B 连** |
| `chain-proxy-1-B` | B | `chain-proxy-2-A` | 连 B 时，**先通过 A 连** |
| `chain-proxy-2-A` | A（前置节点） | *(空)* | 连 A 时**直连** |

内核侧的语义就是：**`dialerProxy = X` 表示"本出站的底层 socket 由出站 X 代为创建"**。
于是运行时的套接字链条自然是 `客户端 → A → B → C → 目标`，出口 tag 叫 `proxy`
是因为路由规则（`outbounds[0].tag == "proxy"`）指向它。

### 1.4 sing-box 版本的差异

`SingboxOutboundService.cs:678-762` 逻辑一模一样，只是字段名从
`streamSettings.sockopt.dialerProxy` 换成了 sing-box 的 **`detour`**：

```csharp
if (!dialerProxyTag.IsNullOrEmpty())
    outbound.detour = dialerProxyTag;      // sing-box 的叫法
```

sing-box 的 `detour` 语义：*"通过哪个出站去拨本出站的服务器"*。
**同一个概念，两个内核两套字段名。**

### 1.5主流客户端在链里处理"代理组"的复杂度

如果链中间某一跳是**代理组**（负载均衡 / 自动选择 / 故障转移），
`BuildChainOutboundsList` 会把该组展开成多个具体 outbound（`BuildGroupProxyOutbounds`），
此时"1 个逻辑节点"变成"N 个物理出站"，链的拓扑不再是简单链表。
代码里有两段专门处理：

- `chainEndNodes = childProfiles.Where(n => n.streamSettings?.sockopt?.dialerProxy is 空)`
  → 把后续节点的 dialerProxy 挂到组内**所有没有上游的末端出站**上（复制分叉）；
- `chainStartNodes.Count > 1` 时**克隆整条已生成的链**（`CloneOutbounds`），
  让每个分支都拥有一份独立的链路，避免多路复用冲突。

> **本项目的取舍**：我们不做代理组。一条链 = 一条直链，逻辑简单、行为可预测。
> 这是刻意的简化，也是"简单易用"这一产品目标的一部分。

### 1.6 其他细节

- `主流客户端ConfigTemplateService.cs:191-205`：有"前置代理配置别名"（`ProxyDetour`），
  即**给全局所有出站统一套一个前置**。这正是本项目"前置代理"设置的对应物 ——
 主流客户端允许你把一整个配置文件的所有出站都指向前置节点。
- `ApplyOutboundFragment()` (`主流客户端OutboundService.cs:805`)：
  分片功能只作用于 `dialerProxy` 为空的出站（即链路最前端），中间跳不分片。
  说明分片/伪装只对第一跳有意义。

---

## 2.主流客户端的实现

### 2.1 定位

主流客户端是 **Tauri（Rust 后端 + React 前端）客户端**，同样**不自己转发**，
转发由 **mihomo（主流客户端.Meta）内核**完成。

但它的链式代理做得比主流客户端**更贴近本项目的需求**：
它把"链"当作**普通代理之间的一种链接关系**（`dialer-proxy` 字段），
而不是"一份需要整体重新生成的配置文件"。这对我们很有参考价值。

### 2.2 核心代码位置

| 文件 | 作用 |
|---|---|
| `src-tauri/src/config/runtime.rs` → `IRuntime::update_proxy_chain_config()` | 核心：把链写回 mihomo 配置 |
| `src/components/proxy/proxy-chain.tsx` | 前端链式代理编辑器（入口/出口标识、拖拽排序） |
| `src/cmd/runtime.rs:57-63` | 校验链是否成环、是否只有一个入口 |
| `src/types/global.d.ts:416` | `'dialer-proxy'?: string` 字段定义 |

### 2.3 核心算法（`src-tauri/src/config/runtime.rs:53-85`）

这个函数写得非常干净，是本项目参考价值最高的一段代码：

```rust
/// Rebuilds `dialer-proxy` links from an ordered proxy chain, or removes them for `None`.
pub fn update_proxy_chain_config(&mut self, proxy_chain_config: Option<Value>) {
    // 1) 先无条件清空所有代理上已有的 dialer-proxy（幂等重建）
    if let Some(Value::Sequence(proxies)) = config.get_mut("proxies") {
        proxies.iter_mut().for_each(|proxy| {
            if let Some(proxy) = proxy.as_mapping_mut()
                && proxy.get("dialer-proxy").is_some()
            { proxy.remove("dialer-proxy"); }
        });
    }

    // 2) 再按顺序链式重建：proxies[i].dialer-proxy = proxies[i-1].name（i != 0）
    if let Some(Value::Sequence(dialer_proxies)) = proxy_chain_config
        && let Some(Value::Sequence(proxies)) = config.get_mut("proxies")
    {
        for (i, dialer_proxy) in dialer_proxies.iter().enumerate() {
            if let Some(Value::Mapping(proxy)) = proxies.iter_mut()
                    .find(|proxy| proxy.get("name") == Some(dialer_proxy))
                && i != 0
                && let Some(dialer_proxy) = dialer_proxies.get(i - 1)
            {
                proxy.insert("dialer-proxy".into(), dialer_proxy.to_owned());
            }
        }
    }
}
```

链 `[A, B, C]`（A 前 C 后）生成的 mihomo 配置：

```yaml
proxies:
  - { name: A, type: socks5, ... }                       # 无 dialer-proxy，直连
  - { name: B, type: socks5, ..., dialer-proxy: A }     # 经 A 连
  - { name: C, type: socks5, ..., dialer-proxy: B }     # 经 B 连
proxy-groups:
  - { name: 链式代理, type: select, proxies: [C] }       # 出口是 C
```

**和主流客户端完全同构**：都是 `第 i 跳的 dialer-proxy = 第 i-1 跳`。
区别只是主流客户端反转数组后从出口往前写，主流客户端正序从前往后写。

### 2.4 校验逻辑

`src-tauri/src/cmd/runtime.rs:57-63` 会沿着 `dialer-proxy` 反向遍历整条链，
检查：

- 是否存在成环（`A → B → A`）；
- 整条链是否**只有一个入口**（没有上游的节点必须唯一）。

```rust
if proxy_map.get("name").map(|x| x.as_str()) == proxy_name && proxy_map.get("dialer-proxy").is_some() {
    ...
    proxy_name = proxy.get("dialer-proxy").map(|x| x.as_str());
}
```

### 2.5 UX 设计（值得抄的部分）

从 `src/locales/zh/proxies.json` 能看出作者对这条功能的取舍与警告：

| 文案 key | 内容 | 我们的应对 |
|---|---|---|
| `warning` / `empty` | "链式代理会显著降低网速。如果您没有明确的业务需求…请保持关闭" | 默认**关闭**，需用户显式开启 |
| `minimumNodes` | "链式代理至少需要 2 个节点" | 前置 + 住宅 = 2 跳，本项目固定 2 跳，不满足则不允许启动 |
| `duplicateNode` | "该节点已在链式代理表中" | 本项目端口不可重复 + 同名代理不可重复 |
| `connectFailed` / `disconnectFailed` | 连接/断开链式代理失败提示 | 启动失败必须**明确报错**，不静默 |
| Changelog: "链式代理增加明显的入口出口与数据流向标识" | UI 上标出**入口 / 出口 / 数据流向** | 本项目 GUI 中明确画出 `浏览器 → 10810 →主流客户端→ 住宅` 的流向 |
| Changelog: "链式代理仅显示 Selector 类型规则组" | 链里只允许 Selector 组 | 我们不做组，直接排除该复杂度 |

### 2.6 mihomo 的另一种"链"：`relay` 组

mihomo 还有 `type: relay` 的代理组，它和 `dialer-proxy` 是**两种不同的链**：

- `dialer-proxy`：**套接字级**串联，内核在建立 socket 时逐跳拨号（本项目要的就是这种）；
- `relay`：**组级**串联，两台真实代理服务器互相转发整条 TCP 连接（`relay` 组把成员当作
  服务端对来用，需要 `Network` 协议的两端都在 mihomo 控制下），普通用户基本用不了。

**本项目采用 `dialer-proxy` 语义**，因为它是"客户端 → 前置 → 住宅 → 目标"唯一可行的模型。

---

## 3. 两个项目的共同点与差异

| 维度 |主流客户端|主流客户端| **PicoHop（本项目）** |
|---|---|---|---|
| 谁做转发 | Xray / sing-box 内核（外部进程） | mihomo 内核（sidecar） | **自己用 Go 实现** |
| 链的表达 | `sockopt.dialerProxy` / `detour` | `dialer-proxy` | **Go 对象图（Upstream 接口）** |
| 链长 | 任意 N 跳 | 任意 N 跳 | 固定 2 跳（前置 + 出口） |
| 出口选择 | 内核路由表 `proxy` tag | 规则组 `select` | **每条链独立绑定一个本地端口** |
| 多条链并存 | 多配置文件 / 多实例 | 多 profile | **一个进程内多个监听端口** |
| 支持代理组 | 是（且很复杂） | 仅 Selector | **否（刻意简化）** |
| 住宅节点类型 | 任意 sing-box/Xray 出站 | 任意 mihomo proxy | **socks5 / http / wireguard** |

**最重要的共同洞察**：

> 三者都不需要"理解"链式代理的报文。
> 链式代理的本质只是 **"在一条已建立的 TCP 连接内部，再发起一次新的连接请求"**。
> 因此它可以完全用标准协议套娃实现，**不需要任何私有协议**。
> 住宅代理不需要知道自己在被代理 —— 它只会看到一条来自主流客户端出口 IP 的普通连接。

---

## 4. 复刻可行性评估（Python 3.10.11 / C++ / Go）

### 4.1 核心工作量

实现链式代理只需要手写这几件事，**没有任何高难度算法**：

1. SOCKS5 服务端（greeting / auth / CONNECT / UDP ASSOCIATE）—— RFC 1928，约 200 行；
2. HTTP 服务端（CONNECT + 普通绝对 URI 请求）—— 约 150 行；
3. SOCKS5 客户端 + HTTP CONNECT 客户端 —— 约 150 行；
4. 三级套接字串联 + 双向 `io.Copy` 中继（含 half-close）—— 约 100 行；
5. 端口生命周期管理（占用检测 / 冲突校验 / 启动占用 / 停止释放 / 崩溃残留清理）—— 约 150 行。

真正的工作量在**工程化**（配置持久化、端口回收、GUI、错误提示），而不是协议本身。

### 4.2 三种语言对比

| 维度 | Python 3.10.11 | C++ | **Go 1.24** |
|---|---|---|---|
| 协议实现 | asyncio 够用，但同步/异步混写易错 | 裸 socket + 线程，代码量大 | `net` + goroutine，**最贴合** |
| 并发模型 | 单线程 asyncio 遇到阻塞 DNS / 慢住宅代理容易卡死 | 线程池，需自己写池和生命周期 | **天然并发，一连接一 goroutine** |
| 分发形态 | 需要目标机装 Python（或 PyInstaller 打包，仍带 ~30MB 运行时） | 需 MSVC/MinGW，编译环境门槛高 | **`go build` 静态单文件 exe，零依赖** |
| GUI | tkinter/PyQt，另需安装 | Win32 裸 API 或 Qt，代码量巨大 | **原生 syscall 调 Win32，同样零依赖** |
| 打包分发 | 用户要装解释器 | 用户要装运行库（/MT 静态可解决） | **一个 exe，双击即用** |
| 跨平台 | 好 | 差 | **好（`GOOS=linux` 同样直接编）** |
| 项目内依赖隔离 | 需 venv（在项目内可做到） | 第三方库需 CMake + 本地缓存 | **`GOMODCACHE/GOCACHE` 可全部重定向到项目目录** |
| 开发速度 | 快 | 慢（模板/内存/异常处理） | **快** |

### 4.3 选型结论

**选择 Go 1.24**，理由按权重排序：

1. **单文件 exe，零运行时依赖** —— 最符合"简单易用"，用户机器上不需要装任何东西；
2. **goroutine 天然适配"一客户端连接 = 一条三级链"** 的模型，代码线性、无锁、无回调地狱；
3. `net.Conn` 接口统一了 SOCKS5 / HTTP / WireGuard 三种上游的抽象（都实现 `Dial`）；
4. 依赖可完全隔离在项目目录（`GOMODCACHE`/`GOCACHE` 环境变量），满足"严禁往别处装"的要求；
5. 官方 `net` 包直接支持 UDP，WireGuard 的 userspace 实现（wireguard-go + gvisor netstack）
   是纯 Go，可静态编译，不需要 wintun 驱动、不需要管理员权限、不改动系统路由表。

**Python 的定位**：作为端到端验证脚本的语言（环境里已有 3.10.11），
以及万一 Go 版出现平台问题时的一条备选实现路径。
**C++ 的定位**：不采用 —— 在这个场景下它既不比 Go 省事，也不比 Go 好装。

---

## 5. PicoHop 的架构决策（研究 → 设计的映射）

| 研究得到的结论 | PicoHop 的落地 |
|---|---|
| 链 = 套接字逐跳创建 | `proxy.Dialer` 逐层包装：`client → FrontDialer → UpstreamDialer → target` |
| `dialer-proxy = 上一跳` | `Upstream` 接口只有一个方法 `Dial(ctx, network, addr) (net.Conn, error)` |
| 链里可以混不同协议 | 所以 `Upstream` 的实现有 `socks5` / `http` / `wireguard` 三种，**可自由组合** |
| 出口 = 路由表里选中的那个 tag | 所以 PicoHop 让**每个上游绑定独立端口**，浏览器选端口 = 选出口，最直观 |
| 链式代理显著降速 | 所以**默认关闭**，界面上显式画出两跳，测试按钮实时反馈延迟与出口 IP |
| 至少 2 跳 | 所以前置代理为空时**不允许启动**，直接报错 |
| 端口必须互斥 | 所以启动前做三重校验：重复检测 / 占用检测 / 与前置端口冲突检测 |
| 用户住宅 IP 是动态的 | 所以每条客户端连接**独立**建立完整链路，不复用连接，出口 IP 每次都可能变 |

---

## 6. 附录：前置代理代理（全局 ProxyDetour）的补充说明

主流客户端的 `主流客户端ConfigTemplateService.cs:191-205` 展示了一种**更接近本项目需求**的用法：

```csharp
if (outbound["streamSettings"]?["sockopt"]?["dialerProxy"].ToString().IsNullOrEmpty() ?? true)
{
    outbound["streamSettings"]["sockopt"]["dialerProxy"] = fullConfigTemplate.ProxyDetour;
}
```

即：**把配置里所有还没有上游的出站，统一挂到同一个"前置代理"节点上**。
效果等价于"所有出站都要先经过我指定的这个前置节点"。

这正是 PicoHop 里"前置代理（全局单例）"这一概念的来源，
只是我们把它做成了**运行期**的（改完立即生效、无需重启内核、可以多条链同时用同一个前置），
而不是主流客户端那种**配置期**的（改完要重写配置并重启内核）。

---

## 7. 参考的 RFC / 规范

| 文档 | 用途 |
|---|---|
| RFC 1928 — SOCKS Protocol Version 5 | SOCKS5 客户端/服务端报文格式 |
| RFC 1929 — Username/Password Authentication for SOCKS5 | SOCKS5 认证子协商（方法 0x02） |
| RFC 7230 §4.3 — HTTP CONNECT | HTTP 代理隧道建立方法 |
| RFC 9110 §9.3.6 / §15 | HTTP 代理绝对 URI 形式（普通转发） |
| WireGuard 协议白皮书 §5 — Transport Data | WireGuard 消息格式（由 wireguard-go 实现，本项目不自己解析） |