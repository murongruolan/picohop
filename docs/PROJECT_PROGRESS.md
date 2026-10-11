# 开发进度

> 最后更新：2026-10-11（v2 重构）
> 图例：✅ 已完成并验证 ｜ ⚠️ 部分可用 ｜ ⬜ 未开始
>
> **阅读提示**：本文按阶段记录，**前半部分是 v1（纯 Win32 界面）当时的记录**，
> 保留原样不删是为了留下决策与踩坑的痕迹；本次 v2 重构的完整记录在文末
> [「v2 重构」](#v2-重构wails--vue-3-界面--内嵌-sing-box2026-10-11) 一节。
> 因此前半部分里出现的 "6 次提交"、"界面自检脚本"、`internal/gui`、
> "dist\chainproxy.exe 是图形界面" 之类的说法，描述的都是**当时**的状态，
> 现在已不成立 —— 这是刻意保留历史的结果，不是笔误。

---

## 总体状态

**全部完成并已通过真实环境实测。**

- socks5 / http 住宅代理链式代理：前置 → 住宅 → 目标，SOCKS5 与 HTTP 客户端均通；
- WireGuard 上游：对**真实服务端**握手 301ms、隧道双向可用；
  「直连」与「经前置代理 前置」两种模式都验证过，支持 `PresharedKey`。

---

## 任务 0：环境与规范 ✅

| 项 | 状态 | 说明 |
|---|---|---|
| 工具链勘察 | ✅ | git 2.48.1 / Go 1.24.0 / Python 3.10.11 / curl |
|主流客户端端口验证 | ✅ | `127.0.0.1:10808` mixed 协议正常，SOCKS5 与 HTTP 均返回出口 IP |
| 项目目录规划 | ✅ | 见 `ARCHITECTURE.md` 第 7 节 |
| 本地 git 仓库 | ✅ | 分支 `main`，6 次提交，每次在 `COMMIT_HISTORY.md` 有详细说明 |
| 依赖隔离 | ✅ | `scripts/env.ps1` 把 Go 全部缓存重定向到 `.cache/` |
| 文档骨架 | ✅ | README / RESEARCH / ARCHITECTURE / PROGRESS / COMMIT_HISTORY / USER_GUIDE |

## 任务 1：参考项目研究 ✅

| 项 | 状态 | 说明 |
|---|---|---|
| 拉取主流客户端/主流客户端| ✅ | `refs/`，已 gitignore |
|主流客户端链式实现分析 | ✅ | `BuildChainOutboundsList` + `FillDialerProxy` |
| sing-box 变体分析 | ✅ | 字段名 `detour` |
|主流客户端链式实现分析 | ✅ | `IRuntime::update_proxy_chain_config`，字段 `dialer-proxy` |
| 语言选型 | ✅ | 选 Go |
| 研究报告 | ✅ | `docs/RESEARCH_chain_proxy.md` |

## 任务 2：核心协议实现 ✅

| 项 | 状态 | 说明 |
|---|---|---|
| 配置结构 + 持久化 | ✅ | `internal/config`，原子写、`user:pass@host:port` 解析 |
| 前置 `Front` 抽象 | ✅ | socks5 / http / 直连 |
| 上游 `Upstream` 抽象 | ✅ | socks5 / http / wireguard |
| Mixed 监听器 | ✅ | 首字节嗅探，同时服务 SOCKS5 + HTTP |
| SOCKS5 服务端 | ✅ | greeting / auth / CONNECT / UDP ASSOCIATE |
| HTTP 服务端 | ✅ | CONNECT + 绝对 URI |
| 双向中继 + 半关闭 | ✅ | `Relay()` |
| 端口管理 | ✅ | 四重校验（格式 / 重复 / 与前置冲突 / 试绑定） |
| SOCKS5 UDP ASSOCIATE | ✅ | 外层通道实测通过 |

## 任务 3：应用状态机与测试 ✅

| 项 | 状态 | 说明 |
|---|---|---|
| `app` 启停状态机 | ✅ | 单条失败不影响其它 |
| 分层一键测试 | ✅ | 前置 / 各链路 / 出口 IP 对比 |
| CLI 子命令 | ✅ | `run` / `test` / `validate` / `version` |
| 端到端测试脚本 | ✅ | `scripts/test_e2e.ps1`（带重试） |
| 单元测试 | ✅ | `internal/config`、`internal/proxy`、`internal/wgproxy` |

## 任务 4：GUI ✅

| 项 | 状态 | 说明 |
|---|---|---|
| 主窗口 | ✅ | 38 个控件，固定尺寸 |
| 前置代理设置区 | ✅ | 类型/地址/端口/账号/超时 + 保存 + 测试 |
| 住宅代理表格 | ✅ | 等宽自绘、中文双宽对齐、↑/↓ 选行 |
| 添加/编辑对话框 | ✅ | 快速导入、端口四重校验、类型联动、WireGuard 字段 |
| 状态展示与主开关 | ✅ | 顶部圆点 + 每行状态 + 流量统计 |
| 一键测试面板 | ✅ | 日志区实时输出 |
| 布局体检（越界/截断/重叠） | ✅ | GDI 实测文字宽度，四方向越界 + 重叠检测 |
| 点击模拟 | ✅ | BM_CLICK + 按 ID 精确计数 + 预期效果断言 |
| 自检脚本 | ✅ | `CHAINPROXY_UI_DUMP` + `scripts/test_ui.ps1` |

## 任务 5：WireGuard 上游 ✅

| 项 | 状态 | 说明 |
|---|---|---|
| wireguard-go + netstack 集成 | ✅ | 纯用户态，无驱动、无管理员权限 |
| SOCKS5 UDP 外层（`socksBind`） | ✅ | 经前置代理 的 UDP relay 收发正常 |
| 完整隧道握手 | ✅ | 对真实服务端 301ms 通过 |
| 隧道内 TCP 承载 | ✅ | 对端回 RST 即为双向可用的正面证据 |
| 预共享密钥 PresharedKey | ✅ | 配置 / UAPI / GUI 全部支持 |
| 隧道内 TCP 转发 | ✅ | 隧道内 TCP 往返实测通过 |
| UDP 转发（客户端侧） | ⬜ | 暂不支持，浏览器走 TCP 不受影响 |

## 任务 6：端到端验证 ✅

| 项 | 状态 | 说明 |
|---|---|---|
| 住宅代理 SOCKS5 链路实测 | ✅ | 前置 43.251.135.130 → 住宅 76.39.124.36 等 |
| HTTP 客户端（CONNECT + 明文） | ✅ | mixed 协议两种客户端都通 |
| 多上游并存 | ✅ | 10810 / 10811 互不影响 |
| 端口释放 / 重启重绑 | ✅ | 停止后立即释放，重启可立即占用 |
| GUI 启动自动占用端口 | ✅ | 自检脚本断言 |
| WireGuard 直连实测 | ✅ | 握手 301ms + 隧道双向可用 |
| WireGuard 经前置代理 前置实测 | ✅ | 握手 301ms + 隧道双向可用 |
| **WireGuard 对接真实外部出口节点** | ✅ | **见下方专项记录** |
| 不自启动 | ✅ | 代码中无任何注册表/计划任务/服务调用 |

---

## 专项：对接真实外部 WireGuard 出口节点（端到端验收）

在一台**完全可控**的云服务器（腾讯云 ECS，Ubuntu 22.04）上，用用户自研工具
WarpPool 部署的出口节点做了一次完整验收。这是第一次在**不受干扰**的环境里
把"客户端 → WireGuard → 公网出口"整条链路走通。

客户端配置（服务端 `wpnat` 接口，监听 `0.0.0.0:51820`）：

```ini
[Interface]
PrivateKey = <本机私钥>
Address    = 10.200.0.4/32

[Peer]
PublicKey  = <服务端公钥>
Endpoint   = 142.86.118.134:51820
AllowedIPs = 0.0.0.0/0, ::/0        # 全隧道
PersistentKeepalive = 25
```

| 检查项 | 结果 |
|---|---|
| 密钥对自洽（私钥推导公钥） | ✅ 与配置给出的公钥一致 |
| 服务端确认 | ✅ `wg show`：peer 已登记、`latest handshake: 10 seconds ago`、有收发字节 |
| **直连**握手 + 出网 | ✅ 出口 IP = `142.86.118.134`（服务端公网 IP） |
| **经前置代理 前置**握手 + 出网 | ✅ 出口 IP 同上 |
| 四个独立来源核对出口 IP | ✅ ifconfig.me / api.ipify.org / ip-api.com / cloudflare trace 全部一致 |
| 真实网站访问 | ✅ example.com、bing.com 正常返回 |
| SOCKS5 与 HTTP 两种客户端 | ✅ 均可用（mixed 协议） |
| 延迟 | 直连 1.23s → 经隧道 0.62s |

结论：**ChainProxy 的 WireGuard 实现在真实公网出口上完全可用**，
`0.0.0.0/0` 全隧道、`endpoint` 走公网直连、以及"经前置代理"两种前置方式都通过。

> 之前对另一台共享服务器（`204.197.163.238:31496`，NAT 映射到容器内）反复失败，
> 表现为"握手包发出（`tx_bytes=444`）、零回包（`rx_bytes=0`）"。
> 换成这台可控服务器后一次通过 —— 说明**问题在那台服务器的 NAT/网络环境**，
> 与客户端实现无关。共享节点上"其它 peer 持续有流量"也印证了该环境不适合排障。

### 那台服务器的根因（后来拿到 root，已定位到最后一步）

**结论：`204.197.163.238:31496` 的 TCP 转发正常，UDP 转发不生效 ——
尽管服务商面板里该条目写的是 "TCP/UDP"。**

| 检查 | 结果 |
|---|---|
| 容器内 WG | ✅ `wp1` 正常监听 `0.0.0.0:51820`（UDP），我方 peer 已登记、allowed IPs 正确 |
| **TCP `31496`（容器内开 TCP 监听在 51820）** | ✅ **连通**、数据双向正常 |
| **UDP `31496`（抓全部 UDP，不过滤端口）** | ❌ 来自客户端的包**一个都没到**（只有主服务器 peer 的握手与 WARP 的 IPv6 流量） |
| `wpls list` 节点自查 | 两个 `wg` 条目端口 `31496`、状态**从未握手**；主服务器 peer（内网 `51820`）**在线** |

**=> UDP 侧的转发在实际链路上没有生效。** 需要在服务商侧排查
（面板显示与实际规则不一致、或该 UDP 条目指向了别的内部地址）。

> ⚠️ **一次方法论教训**：我最初在容器上开 UDP 监听在 `31496` 去等，
> 但**如果映射是 `31496 → 51820`，包会被转发到 51820**，监听 31496 永远收不到。
> 那次测试是无效的（结论碰巧正确，但方法不成立）。
> **可靠的验证方式是在容器上抓包（`tcpdump -ni eth0 udp`，不加端口过滤），
> 同时从外部发包** —— 这样无论映射指向哪个端口都能看到。

### 为什么"主服务器能跑流量"却不需要配端口映射

容器有两个地址，主服务器走的是**另一条不需要映射的路**：

| 地址 | 性质 | 谁能连 |
|---|---|---|
| `172.16.233.90` | 容器的**直连地址** | 机房网络（主服务器 `64.90.17.152`、其它机房）✅；家宽 ❌ |
| `204.197.163.238` | **NAT 网关**的公网地址 | 任何人，但必须有端口映射 |

证据：抓包里主服务器的包是 `64.90.17.152.40835 > 172.16.233.90.51820`
—— **目的地址是容器的直连地址、源地址未被改写，说明是纯路由、完全没经过 NAT**。
反过来，从家用宽带打 `172.16.233.90:51820` 时包在中间就被丢了
（`172.16/12` 属 RFC1918，很多运营商边缘做 bogon 过滤；机房网络通常不过滤）。

**所以"主服务器走同一个端口也没配映射"不是矛盾 —— 它压根没走那层 NAT。**

---

## 验收标准对照

| # | 标准 | 结果 |
|---|---|---|
| 1 | `dist\chainproxy-cli.exe` 双击运行，原生窗口，无控制台残留 | ✅ 10 MB 单文件 |
| 2 | 前置 10808 + 住宅 → 本地端口 10810 | ✅ |
| 3 | SOCKS5 客户端经 10810 得到与前置不同的 IP | ✅ |
| 4 | HTTP 客户端经 10810 同样可用 | ✅ |
| 5 | 关闭主开关端口立即不可连接，重开立即可用 | ✅ |
| 6 | 退出程序端口释放，重启可立即绑定 | ✅ |
| 7 | 第二条上游 10811 独立工作 | ✅ |
| 8 | 一键测试显示两个出口 IP 并判定链式生效 | ✅ |
| 9 | git log 分步提交 + 每次有详细提交说明 | ✅ 6 次 |
| 10 | 项目目录之外无新增文件/缓存 | ✅ |
| 11 | GUI 无错位 / 无文字截断 / 所有按钮有响应 | ✅ 自检断言 0/0/0 与 5/5 |
| 12 | WireGuard 可用（含 PresharedKey） | ✅ |

---

## 后续可做

1. WireGuard 上游的客户端侧 UDP 转发（浏览器 QUIC / DoUDP 走隧道）。
2. WireGuard 的"隧道内 DNS 解析"选项（当前是本地解析后送入隧道）。
3. 明文 HTTP 路径支持 chunked 请求体（目前返回 501，实际客户端很少用到）。
4. 可选：托盘图标常驻、链路健康度曲线。

---
---

# v2 重构：Wails + Vue 3 界面 + 内嵌 sing-box（2026-10-11）

> 上面是 v1（纯 Win32 界面 + 3 种协议）的进度记录，全部保留。
> v1 的界面代码可以从 git 标签 `v0.1-win32-gui` 找回。

## 目标与最终形态

一句话：**从「纯 Win32 界面 + 3 种协议」变成「Wails + Vue 3 界面 + 8 种协议 + 前置可以指向项目内的另一个节点」。**

| | v1 | v2 |
|---|---|---|
| 界面 | 纯 Win32 syscall 自绘（38 个控件、固定尺寸） | Wails v2 + Vue 3 + Element Plus |
| 出口协议 | socks5 / http / wireguard | 再加 vmess / vless（含 XTLS-Reality）/ shadowsocks / trojan / hysteria2 |
| 配置模型 | `front` + `upstreams` | `front` + `settings` + `nodes`（`version` 1 → 2） |
| 前置 | 全局前置 / 链路专属（临时地址） | 直连 / 全局前置 / **另一个节点** 三选一 |
| 列表 | 等宽自绘表格 | 默认 8 列 + 「列显示」共 19 列可选 |
| 命令行 | 兼做界面入口 | 纯无界面工具（`ls / run / test / validate / fetch / import / express / wgkey / engines`） |

界面窗口默认 1180x740、最小 920x580；产物是 `dist\chainproxy-cli.exe`（图形界面）与
`dist\chainproxy-cli.exe`（命令行）。

## 技术选型的为什么

### 为什么内嵌 sing-box **库**，而不是拉起一个外部 core 进程

- **单文件分发**：产物就是一个 `chainproxy-cli.exe`，不用另外附带 core 的二进制、也不用给它准备配置目录。
- **生命周期一致**：出口跟着节点启停，进程退出就一起回收，不会留下孤儿 core 进程，也不需要自己写"生成配置 → 启动 → 探活 → 重启"的进程管理。
- **少一跳**：`internal/engine` 直接拿到 `adapter.Outbound` 调 `DialContext` / `ListenPacket`，
  不需要"本地再开一个 socks 入口、再连它"——那会多占一个端口、多一次转发。
- **依赖不扩散**：`internal/proxy` **不 import** sing-box，而是通过
  `proxy.RegisterSingBoxFactory(...)` 注册（`internal/engine/register.go` 的 `init`）。
  于是 `internal/proxy` 的单元测试不需要编译整个 sing-box。

### 为什么不自己实现那 5 种协议

那 5 种各自带着一套密码学与帧格式（VMess 的 AEAD 头、VLESS 的 uTLS/Reality、
Shadowsocks 的 2022 系列、Trojan 的 TLS 伪装、Hysteria2 的 QUIC），自己实现等于重造一个
sing-box，而且没有任何官方测试向量可依。

自研的 3 种（socks5 / http / wireguard）之所以自研，是因为它们足够简单，
而且我们要的是"能挂前置、能统计流量、能半关闭中继"这层控制权。

复用 sing-box 还有个额外收益：它**严格解析配置**（未知字段直接报错），
所以"配置能被它接受"本身就是一条有约束力的断言 —— 见 `TestParseStrictnessIsReal`。

### 为什么圈选窗口是纯 Win32，而不是第二个 Webview

**Wails v2 没有多窗口 API**（`options.App` 描述的是唯一那个窗口）。
而圈选要求在"无边框 + 全屏置顶 + 隐藏系统光标 + 自画十字准星"的前提下工作，
直接用 `WS_POPUP + WS_EX_TOPMOST` 反而更省事：

- **先截图、再建窗口** —— 否则会把圈选窗口自己拍进底图，变成"套娃"；
- **红色十字准星由自己在 `WM_PAINT` 里画**（1 像素、贯穿客户区），
  不做 `CreateCursor` 自定义光标位图 —— 那套 API 在 DPI 缩放 / 多屏下很脆；
- 光标用 `ShowCursor(FALSE)` 隐藏，并在任何退出路径（正常结束、报错、panic）都恢复。

## 关键技术验证结论（全部实测得出）

这些结论在写代码之前先用 `.cache/spike-singbox` 验证过，之后固化成
`internal/engine/singbox_test.go` 里的测试：

| 结论 | 表现 | 处理 |
|---|---|---|
| 必须 `include.Context` | 否则反序列化报 `missing outbound options registry in context` | 每个实例用一个**新的** ctx（`box.New` 会往 ctx 里注册本实例的 Manager，共享会互相覆盖） |
| 必须带构建标签 | 否则运行时报 `uTLS, which is required by reality is not included in this build` | 统一 `-tags "with_utls,with_quic"`；并加 `BuildCheck()` 把运行期错误提前成一句明确的话 |
| 取出口要用 `inst.Outbound().Outbound(tag)` | `adapter.Router` 上没有 `Outbound` 方法 | 见 `internal/engine/singbox.go` |
| `direct` 出口不允许 `detour` | 报 `'detour' is not supported in direct context` | 直连模式**完全不生成**前置出口，也不写 detour |
| `transport.type` 没有 `tcp` | 只能取 http / ws / grpc / quic / httpupgrade | 网络为 tcp 时**整个 transport 字段都不写** |
| `transport.host` 类型不统一 | http 是数组、httpupgrade 是字符串、ws 走 headers | 分别处理，见 `applyTransport` |
| sing-box 对未知字段会报错 | 字段名写错不会静默失效 | 因此"能被它解析成功"是有效断言 |

## 实测数据

**真链路三个出口 IP 各不相同，证明链式确实生效**（本地前置 → 住宅代理 → 目标）：

| 环节 | 出口 IP |
|---|---|
| 全局前置（本机前置代理） | `204.197.163.238` |
| 住宅代理 1 | `24.40.126.216` |
| 住宅代理 2 | `76.46.0.200` |

真链式对比（探测出口 IP 这一步的耗时）：**本地前置 1.5 ms → 经完整链路 201 ms**，
两个数量级的差距本身就说明流量确实绕到了远端出口，而不是被前置原地代理。

**测试与构建：**

```
go test -tags "with_utls,with_quic" ./...     全绿，22.5 秒
  app 0.121s / config 0.074s / engine 0.050s / ports 0.042s
  proxy 0.036s / qrscan 18.746s / share 0.083s / wgproxy 0.544s
```

| 包 | 顶层测试函数 | 含子测试的 PASS 数 |
|---|---|---|
| `internal/share` | 12 | 84 |
| `internal/qrscan` | 19 | 34 |
| `internal/config` | 16 | — |
| `internal/engine` | 10 | — |
| `internal/app` | 8 | — |
| `internal/ports` | 8 | — |

构建耗时：`vite build` 约 4 秒，`wails build` 约 7-8 秒。

## 界面截图验证：发现的 4 个真实缺陷（都已修掉）

桌面程序的排版靠读代码看不出来，所以引入 `cmd/uishot` + `scripts/ui_shot.ps1`
真的截一张图看。这一轮截图一共揪出这些问题：

| # | 现象 | 根因 |
|---|---|---|
| 1 | 端口输入框太窄，10810 显示成 **"11"** | 输入框宽度不够，超出的部分被直接裁掉 |
| 2 | 「前置节点」选**直连**时显示成占位符 **"请选择"** | 直连用的是空字符串 `""`，而 el-select 把空字符串当作"没选"从而显示 placeholder |
| 3 | 远程端口显示 **"0"** | 没有值的数字字段被直接渲染成 0，而不是留空 |
| 4 | socks5 / http 出现**两个密码框** | 字段显示条件重叠（`ShowPassword` 与 `ShowWireGuard` 等开关组合没排干净） |

另外两处是"只有看图才能发现"的问题：

- **工具栏超宽**，右侧的图标按钮被挤出窗口外（不再有任何估算，超出的分组标题会自动截断加省略号）；
- **`GetWindowRect` 包含 Windows 的隐形缩放边框** —— 按它截图会把后面的窗口也截进来，
  看起来像"多了两条滚动条"；这个是用**像素分析**定位到的，改成按客户区取图；
- **设置对话框底部按钮换行** —— `.cp-spacer` 在非 flex 容器里不生效。

### 截图工具的输入注入已经彻底删掉

早期版本用真实鼠标注入（`SetCursorPos` + `mouse_event`）去点开对话框来截图。
结果是：**一旦被测窗口失去前台焦点，这些点击会落到用户当时正在用的别的程序上** —— 实测确实发生过。

现在改成程序自己的 `-open <面板名>` 启动参数，让界面**主动**打开某个面板再截图，
属于纯只读操作。`cmd/uishot` 与 `scripts/ui_shot.ps1` 里都写了这段缘由。

## 已知限制与后续可做

- **WARP / Hysteria2 的 UDP 在部分网络下可能回落到 TCP**：本地端口的 UDP 能力取决于出口协议
  与前置能力；`http` 与 `wireguard` 出口不支持 UDP。浏览器 QUIC 失败会自动回退 TCP，
  网页浏览不受影响。
- **单节点一个 sing-box 实例的内存开销**：每个出口是一个独立实例（带自己的 router），
  好处是启停互不影响、代价是节点很多（几十个）时会有一点固定开销。
- **没有托盘图标**：Wails v2 不做托盘，本项目也不为此引入 cgo 依赖。
- **界面未做无障碍适配**：这是自用桌面工具，没有做完整的键盘导航与读屏支持。
- **端到端验证的边界**：本机**没有对真实 Reality / Hysteria2 服务器**做端到端连通验证，
  只验证到「生成的配置能被 sing-box 接受 + 实例能启动 + 拨号能发出去」这一层；
  真链式端到端是用**自研 SOCKS5 出口**打通的。

后续可做：

1. 对真实 Reality / Hysteria2 服务器补一次端到端连通验证（需要一台可控服务端）。
2. WireGuard 上游的客户端侧 UDP 转发（浏览器 QUIC / DoUDP 走隧道）—— v1 遗留项，仍未做。
3. 托盘图标常驻、链路健康度曲线。

## 本次做了什么（checklist）

- [x] 配置模型升级 v2：`upstreams` → `nodes`，新增 `settings`，前置改为三选一，`version` 1 → 2
- [x] v1 → v2 自动迁移，且**行为不变**（v1 无 `frontOverride` = 全局前置；v1 的链路专属临时前置回退为全局并写进备注）
- [x] 协议元信息只在后端定义一份（`internal/config/protocol.go`），前端读它渲染表单
- [x] 新增 `internal/ports`：默认端口 `max(≥10800)+1`、随机端口 `>10000` 最多重试 5 次、占用检测
- [x] 内嵌 sing-box，提供 vmess / vless / ss / trojan / hysteria2 五种出口（每节点一个只有出口的实例）
- [x] 用 `detour` 表达前置；直连模式不生成前置出口也不写 detour
- [x] 构建标签固化 `with_utls,with_quic` + `BuildCheck()` 提前报错
- [x] sing-box 的 UDP 通道适配（`N.PacketConn` 优先、`net.PacketConn` 兜底，带可被 quit 唤醒的收包协程）
- [x] 节点状态机：`PlanActivation` 拓扑排序 + 灰黑着色检测环路，`Auto` 返回"将连带启动"的节点
- [x] 跨重启的今日统计（每秒差分折算增量、跨天清零）
- [x] 测试编排：TCP 延迟 / 真连接延迟 / 测速 / 出口 IP 四种，走临时链路不占本地端口，同一时刻只跑一批
- [x] 分享链接编解码（SIP002 / legacy ss / vmess base64 JSON / Reality 参数）+ 二维码生成
- [x] 屏幕圈选识别二维码（GDI 截图 + DPI 感知 + alpha 写死 0xFF + 自画十字准星）
- [x] 命令行重写为纯无界面工具，删除全部纯 Win32 界面代码（旧代码留在 `v0.1-win32-gui` 标签）
- [x] 用 Wails v2 + Vue 3 重写界面（默认 8 列 + 19 列可选、右键菜单、通知、设置 4 个标签页）
- [x] 界面截图验证，修掉 4 个真实缺陷 + 工具栏超宽 + 截图带进后面窗口 + 设置底部按钮换行
- [x] `-open <面板名>` 开发参数取代鼠标注入
- [x] 构建脚本带上 sing-box 构建标签；新增界面截图与端到端脚本
- [x] `go test -tags "with_utls,with_quic" ./...` 全绿

---

## 补充验证与修复（第 2 轮，2026-10-11）

第一轮结束时，有几项需求只有"代码写了"，还没有**运行时证据**。
这一轮把它们逐条补上，并修掉了过程中发现的 3 个真实问题。

### 逐条补验证（都有可复现的操作）

| 需求 | 怎么验的 | 结果 |
|---|---|---|
| (4) 导入后只弹"仅端口"小窗 | `-open portonly` 截图 | ✅ 小窗只有一行节点摘要 + 端口输入 + 随机按钮；并**顺带验证了红字占用提示**："端口 10810 已被「住宅代理1」占用" |
| (5) 搜索 / 状态筛选 / 列显示 | `-open columns` 截图 | ✅ 搜索框填 "2" 后表格只剩「住宅代理2」，计数显示 **共 1 个（总 2）**；列显示变成自定义 7 列且按钮显示 `7/19`；状态选择器为「激活状态」 |
| (6) 单击选中 + **回车激活** | `-open enterkey`：先全部停止，选中一行，再派发**合成键盘事件**（纯 JS，不涉及系统级输入注入） | ✅ 节点从 **0/2** 变成 **1/2**：住宅代理1 亮绿点且整行处于选中态，住宅代理2 保持灰色未激活；后端日志同步留下 `「住宅代理1」已启动` 一行 |
| (7) 激活时检测端口占用并提醒 | 用另一个 TCP 监听占住 10817 后激活 | ✅ 拒绝并给出 **"端口 10817 已被本机其它程序占用"**，节点状态标为 `[错误]` |
| (7) 端口与项目内节点冲突 | 导入一个用 10810 的节点 | ✅ 配置校验拦下：**"本地端口 10810 已被「住宅代理1」占用，不能重复"** |
| (8) 通知 5 种类型 / 位置 / 优先级 | `-open notify` 一次弹 5 条并截图 | ✅ primary/success/warning/info/error 五种颜色与图标齐全、纵向正确堆叠、位于控制栏下方且盖住表格（z-index 3200） |
| (10) 只监听回环 | 设置页与列表截图 | ✅ 默认 `127.0.0.1`，设置里可显式打开「允许局域网连接」 |
| (11) 不往项目外写文件 | 检查 `data\` 目录 | ✅ 修复后 WebView2 配置目录落在 `data\webview\EBWebView` |

### 这一轮修掉的 3 个真实问题

1. **通知重叠**：原先用 Element Plus 的 `ElNotification` 并给每条传固定
   `offset`，导致同一个 tick 连发 5 条时全部叠在一起（只看得见最后一条）。
   改成自己实现（`src/notify.js` + `components/Notifications.vue`），
   用 flex 列布局同时拿到"固定位置"与"正确堆叠"。
2. **WebView2 数据目录越界**：默认落在 `%APPDATA%\ChainProxy`，属于项目目录之外，
   违反本项目"一切都在 F:\chain_proxy 内"的规矩。
   用 `WebviewUserDataPath` 显式指到 `data\webview\`。
3. **"仅端口"小窗的端口输入框偏窄**，10810 显示成 "1081"（和添加节点对话框
   当初那个问题一样），统一加宽到 178px。

### 顺带改进了界面自测工具

`cmd/uishot` 原先靠"截屏幕上窗口所在的区域"，有两个毛病：窗口被别的程序挡住时
会截到**挡住它的那个程序**（实测截出过用户正在看的浏览器页面）；
为了让窗口露出来还得抢前台焦点，会打断正在用电脑的人。

改成 `PrintWindow(hwnd, memDC, PW_RENDERFULLCONTENT)` —— 让窗口自己渲染，
不依赖可见性、不抢焦点。返回的图再加一道 `qrscan.LooksBlank` 判断，
万一硬件叠加层导致一片黑就自动退回老办法。

### 仍然没有做的

- **屏幕圈选二维码没有做人工拖拽实测**。圈选窗口自己有 6 个真实驱动窗口消息的
  测试，二维码链路也有"节点→链接→二维码→识别→节点"的五协议全链路测试，
  但"用鼠标在屏幕上拖一个真实二维码"这一步没做 —— 那需要动用户的鼠标，
  而输入注入已经出于安全考虑彻底删除了。**这一步请你自己试一下最快。**
- 没有真实 Reality / Hysteria2 服务器可测，只验证到
  "配置能被 sing-box 接受 + 实例能启动 + 拨号发得出去"。

### 自测钩子自身踩的坑（值得记一笔）

验证"回车激活"时，我一开始把 `-open` 的处理放在了 `onMounted` 里
**注册 keydown 监听器之前**。结果合成事件派发出去却没人接 ——
截图显示"节点全停了、行也选中了，但就是没激活"，
很容易被误读成"回车激活功能没实现"。

改成把自测处理放到 `onMounted` 最后（所有监听器都装好之后）就正常了。
这也说明"截图 + 后端日志"双重取证是必要的：
单看截图只能看到"没生效"，是后端日志里那行 `「住宅代理1」已启动`
才把结论钉死。

---

## 界面微调（第 3 轮，2026-10-11）

两处按使用反馈做的调整，都属于"看着就知道该改、但不动手看不出问题"的那类。

### 1. 列表改通栏，左右不留白

原先表格外面套了一层"卡片"（`margin: 10px 14px 12px` + 边框 + 圆角），
左右各留 14px。改成通栏：`margin: 0`、去掉边框与圆角，表格直接抵到窗口两边。
同时把第一列单元格的左内边距调到 14px，让它和上方筛选栏的左边缘对齐
（否则通栏之后第一列会显得比上面的控件"缩进去一点"）。
上下分隔交给筛选栏的 `border-bottom` 与表头自身的底色。

### 2. 表头支持拖拽调列宽，并保证下限

需求原话是"任意拖拽列宽度，但最小也得保留一个字，不要完全重叠"。
实现过程中翻 Element Plus 源码踩到三个点：

| 发现 | 位置 | 处理 |
|---|---|---|
| 拖拽手柄**要求 `border`**，否则 `handleMouseMove` 直接 return，鼠标在哪都不显示可拖拽光标 | `table-header/event-helper.mjs` `if (!props.border) return` | 给表格加 `border`；再用 CSS 去掉它附带的那圈外框（通栏下多余） |
| 库内部下限**只有 30px** | 同文件 `columnRect.left - tableLeft + 30` | 在 `header-dragend` 里自己钳到 `MIN_COL_WIDTH = 56px`（约两个汉字），并直接改 `column.realWidth` |
| **最后一列默认不可拖** | `allowDragLastColumn` | 显式打开，做到"任意列都能拖" |

为什么改成 `column.realWidth` 而不是只改自己的状态：
`handleMouseUp` 里 `scheduleLayout` 排在下一个 `requestAnimationFrame`，
而 `header-dragend` 是在它**之前**发出的 —— 在这个时机改写，
会被紧接着的那次重排采用，不会先按超窄宽度画一帧（否则会看到一次闪动）。

列宽会**持久化**（localStorage），「列显示」面板底部另加了一个「重置列宽」。

### 验证方式

拖拽这件事本来就"只能手动试"，但没必要真的去动鼠标：
在 `-open resize` 里派发**合成鼠标事件**（`MouseEvent` + 客户端坐标）
走一遍真实代码路径（mousemove 到表头右边缘 → mousedown → 拖 400px → mouseup），
再把测出来的宽度打进一条常驻通知里，从截图直接读数字。实测：

```
别名列：208px → 往左拖 400px 后 56px
        （库内下限 30px，我们钳到 56px；store 记录 = 56）
最后一列「速度(MB/s)」：106px → 56px（验证 allow-drag-last-column）
```

单元格在 56px 下显示为"任..."，即**保住了一个完整汉字 + 省略号**，没有重叠 ——
正是需求要的效果。

---

## 填充列：右端永远不留白（第 4 轮，2026-10-11）

### 问题

上一轮做完"任意拖拽列宽"之后，用户反馈：缩短某一列时右侧会冒出一条留白，
希望"让最后一列自己占满右侧剩余空间，不要留白"。

### 根因（翻源码才看清）

Element Plus 判断一列是否"弹性"，依据是 `!isNumber(column.width)`
（`table-layout.mjs` 的 `updateColumnsWidth`：

    const flexColumns = flattenColumns.filter((column) => !isNumber(column.width))

而**拖拽结束时会执行 `column.width = column.realWidth = 拖出来的数字`**
（`table-header/event-helper.mjs` 的 `handleMouseUp`）。
上一轮的实现里，唯一的弹性列是"地址"；一旦用户拖过它，它就被固定了，
于是**整个表格不再有弹性列**，总宽小于容器宽，右端就空出一条留白。
之后无论拖哪一列，留白都只会更明显。

### 解决

把**最右边那一列**指定为"填充列"：

  - 它不绑 `width`，只绑 `min-width`（= 用户拖出来的值，或它自己的默认宽度）；
    于是它的实际宽度 = `minWidth + 剩余空间`（`table-layout.mjs` 第 109 行），
    永远铺满右端。
  - 在 `header-dragend` 里把它的 `column.width` 清成 `undefined`，
    让它拖完重新回到弹性状态（紧随的重排由 `min-width` 的 watcher 触发）。
  - 「列显示」里切换可见列时，填充列自动变成当前最右的那一列。

同时把"地址/备注/前置"从弹性列改回普通固定列 —— 免得出现两个弹性列
（那样剩余空间会按 min-width 比例分摊给它们，语义变复杂）。

### 取舍（明确写进界面提示）

在空间还有富余时，**把填充列拖窄看不出变化** —— 这正是用户要的
（"缩短它就让它在剩余空间里自己铺满"）。拖它改的是它的*最小宽度*：
只有当窗口窄到列放不下时，这个下限才会体现出来（触发横向滚动而不是挤成一条线）。
「列显示」面板底部的提示里写明了这一点。

### 验证（数字来自截图里的常驻通知）

`-open resize` 现在会依次做三件事并量出"右侧留白"
（= 表格容器右边缘 − 最后一列表头右边缘，期望 0px）：

    1) 「别名」208px → 往左拖 400px 后 56px（库内下限仅 30px，我们钳到 56px）
    2) 填充列「速度(MB/s)」290px → 拖后再量 290px（松手后回到"占满剩余"）
    3) 右侧留白：拖中间列后 0px，拖填充列后 0px（都应为 0）
    store 记录：{"name":56,"speed":230}

注意第 2 条：拖了 60px 之后**视觉宽度不变**（仍然 290px），
而 store 里记的下限从 116 变成了 230 —— 正是"先改下限、宽度仍由剩余空间决定"的设计。

---

## 对话框体验调整（第 5 轮，2026-10-11）

四条使用反馈，都改了。

### 1. 添加节点弹窗去掉顶部那两个导入入口

「从剪贴板导入分享链接 / 从屏幕扫描二维码」原本在对话框顶部又放了一份，
和顶栏「导入」菜单重复。删掉区块、对应的两个函数、图标导入与那段样式。
入口统一留在顶栏「导入」菜单里（它还有第三个"手动粘贴链接…"）。

### 2. 弹窗能拖动"飘开"，且不会被边界裁切

用 `<el-dialog draggable>`。关键发现是**它的默认行为就是"能拖但不越界"**：

    hooks/use-draggable/index.mjs 第 25-28 行
    if (!overflow?.value) {
      moveX = clamp(moveX, minLeft, maxLeft)
      moveY = clamp(moveY, minTop, maxTop)
    }

`overflow` 的默认值是假（`props.overflow ?? globalConfig ?? false`），
所以拖动会被钳制在视口内。它的文档注释写的是 "draggable Dialog can overflow
the viewport"，那指的是显式把 `overflow` 设成真的时候 —— 差点被这句话误导。

**"脱离主窗口成为独立窗口"做不到**：Wails v2 没有多窗口 API
（`options.App` 描述的是唯一那个窗口，`pkg/runtime` 只有对这一个窗口的
setter，没有创建新窗口的接口）。要做到真正的独立窗口得换 Wails v3，
或者自己用 COM 托管第二个 WebView2 —— 代价都太大了。
所以这里交付的是"窗口内自由拖动 + 永远不被裁切"。

### 3. 长表单底部按钮不再被窗口裁掉

这是"被边界裁切"的真实主因：VLESS 带 Reality 的表单比窗口还高，
底部那排按钮直接被窗口下边缘切掉。修法是给对话框加高度上限、让 body 内部滚动：

    .el-dialog.cp-dialog { display:flex; flex-direction:column;
                           max-height: calc(96vh - 32px); margin-bottom: 24px; }
    .cp-dialog .el-dialog__body { flex:1 1 auto; min-height:0; overflow-y:auto; }

`min-height: 0` 不能少 —— 默认的 `min-height: auto` 会让 flex 子项被内容撑破，
内部滚动就失效了。

**这里踩了一个 CSS 选择器的坑**：`el-dialog` 上的 `class="cp-dialog"` 是
**透传到 `.el-dialog` 自身**的（不是外面那层 overlay），所以"给对话框本体加样式"
必须写 `.el-dialog.cp-dialog`；我一开始写成 `.cp-dialog .el-dialog`
（祖先-后代）根本匹配不到，表现为"高度上限没生效、底部按钮照样被裁"。
是 `-open dialogdrag` 那条自测通知先报出"找不到对话框"才暴露的。

### 4. 关闭图标与底部按钮做大 + 居中 + 取消改警告色

  - 右上角关闭按钮：命中区域 40x40，图标字号 14px → 20px
  - 底部按钮：高度 24px（全局 size 是 small）→ 36px，最小宽度 112px
  - 底部按钮**居中**（`justify-content: center`）；两个按钮间距仍是 8px
  - 「取消」统一换成 `type="warning"` 警告色（橙）
  - 设置面板那排是"恢复默认设置 + 占位 + 取消 + 保存"，
    `justify-content: center` 配合 `flex:1` 的占位元素后仍然靠右，不受影响

### 验证

`-open dialogdrag` 会打开最长的表单（VLESS + Reality），然后用**合成鼠标事件**
走一遍真实拖拽（`mousedown` 在 `.el-dialog__header` 上，`mousemove/mouseup`
在 `document` 上），把量到的坐标打进常驻通知：

    硬拖 +1200,+900 后：left=404 top=60 right=1164 bottom=701（视口 1164x701）
    → 整框仍在窗口内 ✓ 不会被裁切
    再拖 -300,-30 后：left=104 top=30 right=864 bottom=671（已飘离居中位置）
    底部按钮区 bottom=655（视口高 701）→ 完整可见 ✓

硬拖到边界时正好停在 `right=1164 / bottom=701`（等于视口尺寸），
说明钳制是精确的，不是"大概不出界"。

---

## 迁移到 Wails v3：外壳换掉（2026-10-11）

### 为什么

需求是"对话框能飘到主窗口之外"，而这需要**第二个真正的 OS 窗口** ——
Wails v2 没有任何创建窗口的 API。经对比 v2/v3（见 `MIGRATION_wails_v3.md`），
选择升级 v3，顺带拿到托盘常驻、全局快捷键、系统通知。

### 做了什么

| 文件 | 改动 |
|---|---|
| `desktop/main.go` | 全部重写：`application.New` + 主窗口 + 托盘 + 全局快捷键 + 通知服务 + `DisableQuitOnLastWindowClosed` + 关窗钩子 |
| `desktop/shell.go` | **新增**：v3 胶水层（事件/剪贴板/对话框/窗口/通知/主题），托盘与快捷键的动作实现 |
| `desktop/theme_windows.go` · `theme_other.go` | **新增**：v3 没有运行期改标题栏明暗的 setter，直接调 `DwmSetWindowAttribute` |
| `desktop/api.go` | `startup/shutdown(ctx)` → `ServiceStartup/ServiceShutdown`；`runtime.*` → v3 对象方法；新增外壳状态上报 |
| `desktop/api_import.go` | 剪贴板/隐藏窗口/保存文件对话框改用 v3 |
| `desktop/frontend/src/api.js` | 桥换成 `@wailsio/runtime` 的 `Call.ByName` + `Events.On`（**只改了这一个文件**） |
| `desktop/frontend/package.json` | 加 `@wailsio/runtime`，锁 `3.0.0-beta.28` |
| `desktop/frontend/wailsjs/` | **删除**（v2 自动生成的绑定，v3 不需要） |
| `desktop/wails.json` | **删除**，换成 v3 的 `desktop/build/config.yml` |
| `scripts/build.ps1` | `wails build` → `go build -tags production,...`（不再依赖 wails3 CLI 与 task） |
| `cmd/uishot` | 新增 `-rect x,y,w,h`：截取屏幕区域（拍托盘等不属于任何窗口的东西） |

### 逐条验收（都有程序外的证据）

| 项 | 证据 |
|---|---|
| 界面在 v3 下正常工作 | 截图：节点 2/2、今日流量、通栏表格、填充列全部正常 |
| 前端桥 `Call.ByName` | 界面能拉到数据、能激活节点 —— 说明服务调用通了 |
| **构建不依赖 wails3 CLI / Taskfile** | `.\scripts\build.ps1` 成功产出 `dist\ChainProxy.exe`（46.23 MB） |
| **WebView2 数据目录留在项目内** | `data\webview\EBWebView` 存在；`%APPDATA%\ChainProxy`、`\chainproxy`、`\wails` **都不存在** |
| **托盘已建立** | 日志：`外壳：托盘已就绪（图标 21017 字节）` |
| **全局快捷键已注册** | 日志：`外壳：已注册全局快捷键 Ctrl+Alt+C（显示/隐藏主窗口）、Ctrl+Alt+A（激活全部节点 / 停止全部）` |
| **关窗不退出（托盘常驻）** | 关窗前：窗口可见（hwnd 1411250）、`MainWindowHandle=21041744`；关窗后：**可见窗口列表里已无该窗口**、`MainWindowHandle=0`、**进程仍在**、**10810/10811 仍由同一 pid 监听**；日志：`窗口关闭被拦下：已收进托盘，进程与代理继续运行` |
| **托盘菜单动作** | 自测直接调菜单背后的方法：复制本地地址 → 程序外用 `Get-Clipboard` 读出 `socks5://127.0.0.1:10810`；停止全部 → 运行中 0/2；激活全部 → 运行中 2/2 |
| `-open` 自测钩子 | 新增 `closetotray` / `trayactions` 两项并实际使用；原有各项沿用 |
| sing-box 构建标签透传 | `chainproxy engines` 自检通过；节点真实启动、端口真实监听 |
| 全量校验 | `gofmt` 干净、`go vet` 干净、8 个测试包全过、`lint.ps1` 4/4 |

### 诚实说明的两点

1. **托盘图标本身没有截到图**：Windows 11 会把新注册的托盘图标放进 `^` 折叠区，
   而把它拖出来需要点鼠标（本项目已彻底禁用输入注入）。
   "托盘已建立"的证据是日志 + 关窗常驻行为成立（两者共用同一段外壳代码）。
2. **点 × 收进托盘那一刻的 Windows 通知只是"调用成功、无报错"**，
   没有抓到 toast 的截图（toast 会自动消失，抓取时机不稳）。

### 还没做

- 对话框仍是主窗口内的 `<el-dialog>`。v3 的多窗口已经打开了这条路，
  但把对话框改成独立窗口需要再引入"第二个前端入口 + 窗口生命周期 + 结果回传"，
  是独立的一步。当前形态：窗口内自由拖动 + 永不被裁切。
