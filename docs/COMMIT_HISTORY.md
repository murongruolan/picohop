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
| 003 | `feat(gui): Win32 原生界面、UI 自检体系与四个关键 Bug 修复` | 2026-10-10 | 主窗口 + 编辑对话框 + 状态/开关/一键测试；修掉 SetTextColor 跨 DLL、ListView 消息号、启动 fork 子进程、**goroutine 未绑定 OS 线程**四个坑 | [详情](#003) |
| 004 | `feat(wireguard): 纯用户态 WireGuard 上游 + 使用手册` | 2026-10-10 | wireguard-go + gvisor netstack + 自研 SOCKS5-UDP Bind；外层实测通过，隧道握手待完善；新增 USER_GUIDE | [详情](#004) |
| 005 | `fix(gui): 修复"点任何按钮都没反应"与界面布局错位` | 2026-10-10 | 根因是控件 ID 超过 16 位被 MAKEWPARAM 截断；另修标签负坐标、CreateFontW 参数错位、EM_* 消息号错误、客户区估算、阻塞式消息泵；新增布局体检与点击模拟两道自动化防线 | [详情](#005) |
| 006 | `fix(wireguard): 打通 WireGuard 隧道 + 支持预共享密钥 + 修下拉框读值` | 2026-10-10 | 用对照组定位并修掉 5 个 WireGuard bug（allowed_ip 逗号列表/读超时杀掉收包循环/替换设备缓冲区/双重关闭 tun/Send 空指针）；新增 PresharedKey；修 comboText 永远返回第一项导致 wireguard 存不进去 | [详情](#006) |

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
---

## 003

**commit**: `feat(gui): Win32 原生界面、UI 自检体系与四个关键 Bug 修复`

**目的**：把核心引擎装进一个真正能用的 Windows 窗口里，并建立一套**能在无人值守时
自动验证界面**的自检机制（纯 Win32 自绘界面最大的风险就是"偶尔卡死"，靠人眼看截图抓不住）。

### 新增文件

| 文件 | 说明 |
|---|---|
| `internal/gui/win_syscalls.go` | 全部 Win32 常量与结构体绑定（`WNDCLASSEX` / `MSG` / `PAINTSTRUCT` / `LVITEM` / `NMCUSTOMDRAW` …），UTF-16 编解码 |
| `internal/gui/win_api.go` | Win32 API 薄封装：窗口、消息、字体、GDI、剪贴板、ListView 辅助 |
| `internal/gui/win_controls.go` | 控件构造与读写小助手（`label/button/edit/combo/checkbox/group`、文本/勾选/下拉读写、数值读写） |
| `internal/gui/win_theme.go` | 配色、字体创建与释放、`humanBytes` |
| `internal/gui/win_window.go` | 主窗口：布局、命令分发、状态刷新、日志、测试编排、住宅代理增删改 |
| `internal/gui/win_wndproc.go` | 窗口过程、静态控件着色、关闭时释放端口 |
| `internal/gui/win_dialog.go` | 添加/编辑住宅代理对话框（含 WireGuard 字段组、快速导入、完整校验） |
| `internal/gui/win_table.go` | 住宅代理表格渲染（等宽字体 + 中文双宽对齐） |
| `internal/gui/win_uitest.go` | **UI 自检**：导出全部控件的类名/ID/坐标/尺寸/文本/表格内容与链路状态 |
| `internal/gui/win_debug.go` | 排障看门狗：`CHAINPROXY_STACK_DUMP` 存在时定期写 goroutine 栈 |
| `scripts/lint.ps1` | gofmt + `go vet -unsafeptr=false` + `go test` 一体化 |
| `scripts/test_ui.ps1` | GUI 自检套件：重复跑 UI 导出、对话框自检、真实 GUI 启动与链路连通性 |
| `scripts/inspect_window.ps1`、`scripts/dump_list.ps1` | 跨进程查看窗口/控件的辅助脚本 |

### 界面结构（1060×762，固定尺寸）

```
┌─ ChainProxy 链式代理 ────────────────────────── ● 运行中 · 2/2 条链路  [启动/停止] [一键测试]
│
├─ ① 前置代理 ── 类型[类型] 地址[ ] 端口[ ] 用户名[ ] 密码[ ] 超时ms[ ]
│               [保存前置设置] [测试前置]   前置出口 IP 与延迟
│
├─ ② 住宅代理（出口）── [启用/禁用][测试选中][▲][▼][删除][编辑][+ 添加住宅代理]
│    状态 名称        类型    本地端口 住宅服务器        流量↑/↓    状态说明
│    ●  住宅代理1    socks5  10810    SOCKS5 hk…:10000  1.2MB/3.4MB  监听 127.0.0.1:10810
│    ●  住宅代理2    socks5  10811    SOCKS5 hk…:10000  0B/0B        监听 127.0.0.1:10811
│
├─ ③ 运行状态与测试输出 ── [清空][复制全部][打开配置目录]
│    09:32:32  前置代理：SOCKS5 127.0.0.1:10808
│    09:32:37  「住宅代理1」已启动，监听 127.0.0.1:10810（…）
│
└─ 前置：SOCKS5 127.0.0.1:10808   配置：F:\chain_proxy\data\config.json   程序不注册任何开机自启动项
```

### 本次修掉的四个 Bug（都写在这里以免以后重犯）

1. **`SetTextColor` / `SetBkMode` 放错了 DLL**
   它们在 **gdi32.dll**，我最初挂在 `user32.dll` 下。
   表现：程序启动后立刻 `panic: Failed to find SetTextColor procedure in user32.dll`，退出码 2。
   修复：移到 gdi32 的 proc 组里，并就地写了注释提醒。

2. **`LVM_SETITEMTEXT` 消息号写成了 `LVM_FIRST+77`**
   `77` 是 `LVM_INSERTITEM`，`LVM_SETITEMTEXT` 其实是 `LVM_FIRST+109`。
   表现：每次刷新列表都在疯狂"插入行"，列表越刷越长、CPU 打满。

3. **`LVCOLUMN.mask` 误用 `LVCF_IMAGE(0x0010)` 而不是 `LVCF_SUBITEM(0x0008)`**
   没有 ImageList 却开了 IMAGE 位，ListView 内部取图失败直接**把控件卡死**（`Responding=False`）。
   修复：mask 改为 `LVCF_FMT|LVCF_WIDTH|LVCF_TEXT|LVCF_SUBITEM`。
   顺带发现：`LVM_SETITEMTEXT` 在这套裸 syscall 下依然不落地（返回 0），
   于是**放弃 ListView，改用等宽只读 EDIT 自己画表格**（见 `win_table.go` 顶部注释），
   换来确定性：零结构体编组、零自定义绘制。

4. **⚠️ 启动时 fork 子进程探测字体（已删除）**
   `hasSegoeUI()` 会执行 `exec.Command("cmd","/c","where","segoeui.ttf")`。
   在被作业对象/管道限制的环境里，这个子进程会**偶发卡死**，导致窗口永远不被显示。
   修复：直接假定 "Segoe UI"，缺失时由系统静默替换。启动路径上不再有任何子进程。

5. **⚠️⚠️ 根因级：创建窗口的 goroutine 没有 `runtime.LockOSThread()`**
   Windows 把消息投递到**创建窗口的那个 OS 线程**的消息队列；
   Go 的 goroutine 默认会在多个 OS 线程之间迁移，一旦迁移，`GetMessage` 就再也
   收不到这个窗口的消息。
   表现极具迷惑性：**首次启动正常，界面会整体卡死**、`.NET Responding=False`、
   控件自检偶发只枚举到 0 个控件、WM_TIMER 一次都不触发。
   定位手段：`CHAINPROXY_STACK_DUMP` 看门狗把 goroutine 栈写盘，
   发现 goroutine 1 停在 `GetMessage` 的系统调用里、且窗口消息一个都不来。
   修复：`runWindow()` 与 `dlgState.run()` 开头都加 `runtime.LockOSThread()`。
   **这是纯 Win32 + Go 最经典的坑，后面任何人重构 GUI 都不许删掉这两行。**

### 自检体系（`CHAINPROXY_UI_DUMP` / `CHAINPROXY_UI_DUMP_DIALOG`）

因为本会话无法直接看截图，所以界面验证改用**导出 + 断言**：
程序完成布局后把每个控件的类名、ID、坐标、尺寸、可见性、文本，以及
表格内容、每条链路的状态，全部写进指定文件，然后退出。
`scripts\test_ui.ps1` 对这些内容做断言，并**重复运行 4 次**以暴露"偶发卡死"类问题。

### 验证结果

```
$ .\scripts\test_ui.ps1 -Repeat 4
[PASS] 第 1~4 次：窗口可见且控件齐全 (36 个)
[PASS] 第 1~4 次：前置地址已回填 / 住宅代理表格已渲染 / 链路状态已就绪
[PASS] 对话框自检报告已生成（含名称/类型/本地端口/WireGuard 字段）
[PASS] GUI 进程存活  (pid=22976)
[PASS] GUI 响应正常
[PASS] GUI 启动后自动占用端口 (2 个监听)
[PASS] 经 10810 的出口 IP 与前置不同  (前置 103.115.108.31 → 住宅 47.230.237.180)
[PASS] GUI 退出后端口释放
=== GUI 自检全部通过 ===
```

`.\scripts\lint.ps1 -Fix`（gofmt + vet + go test）全部通过。

### 设计取舍

- **固定窗口尺寸、去掉 WS_THICKFRAME**：省掉一整套响应式布局，也不会被误拖乱。
- **表格用等宽 EDIT 而不是 ListView**：见上文 Bug 3，稳定性优先于原生控件的"高级感"。
- **测试全部丢到 goroutine + 队列**：窗口过程里绝不做耗时动作，避免测试时界面假死。
- **每条链路的端口互相独立**：某条绑定失败只把该行标红，其余照常工作。
- **不使用托盘图标**：需求只要求"状态展示与开关"，越简单越好维护。

### 已知遗留

- WireGuard 上游尚未实现（对话框字段已就位，`Upstream.Start()` 会返回明确错误）→ 下一次提交。
- 明文 HTTP 路径仍不支持 chunked 请求体。

---

## 004

**commit**: `feat(wireguard): 纯用户态 WireGuard 上游（外层已验证，握手待完善）`

**目的**：让"住宅 IP 走 WireGuard 出口"这件事在架构上成立。
WireGuard 是外层 UDP 协议，要让它经过主流客户端，只有"改系统路由"和"用户态套娃"两条路。
本项目选了后者：不动系统、不装驱动、不需要管理员权限。

### 新增文件

| 文件 | 说明 |
|---|---|
| `internal/wgproxy/wgproxy.go` | WireGuard 上游实现：`Start()` 建隧道、`Dial()` 从用户态栈出栈 |
| `internal/wgproxy/socksbind.go` | **核心零件**：实现 wireguard-go 的 `conn.Bind`，把 WG 外层 UDP 封成 SOCKS5 UDP 报文经前置代理收发 |
| `internal/wgproxy/register.go` | `init()` 里注册构造器，解开 `proxy ↔ wgproxy` 的循环依赖 |
| `internal/wgproxy/socksudp_test.go` | 外层通道测试：经前置代理 的 UDP ASSOCIATE 发 DNS 查询并验证应答 |
| `internal/wgproxy/e2e_test.go` | 本机假服务端 + 客户端隧道的端到端测试（默认跳过，见下） |
| `docs/USER_GUIDE.md` | 使用手册与故障排查 |

### 修改文件

- `internal/proxy/socks5.go`：`socks5UDPAssociate` 改为**手工拼报文**。
  原因：RFC 1928 要求 UDP ASSOCIATE 请求的 DST.PORT 填 0，
  而 `appendSocks5Addr` 会拒绝端口 0，导致请求根本没发出去。
- `internal/proxy/front.go`：`directFront` 增加 `DialUDP`（`plainUDPChannel`），
  让"不使用前置"时 WireGuard 也能工作，同时被端到端测试用来做本地假服务端。
- `cmd/chainproxy/main.go`：空导入 `internal/wgproxy` 触发注册。
- `scripts/lint.ps1`：**gofmt 只扫 `cmd` 和 `internal`**。
  原因见下方"事故记录"。

### 三层架构

```
gvisor netstack          用户态 TCP/IP 栈，提供 net.Conn
      ▲
wireguard-go device      加解密 + 握手 + 会话
      ▲
socksBind（自研）         WG 的外层 UDP 包 → SOCKS5 UDP 报文 → 前置代理
      ▼
主流客户端127.0.0.1:10808   UDP ASSOCIATE relay
```

### 本次踩到的坑

1. **`bind.Close()` 不能是"永久关闭"**
   wireguard-go 的 `Device.BindUpdate()` 会**先 Close 旧 bind 再 Open 新 bind**
   （改端口、换密钥、重新选路都会触发）。最初我把 `Close()` 写成 `sync.Once` + 永久关闭，
   结果下一次 `Open()` 直接拿到 `net.ErrClosed`：
   ```
   IPC error -98: failed to set listen_port: use of closed network connection
   ```
   修复：`Close()` 只结束"本次会话"，真正彻底关闭用单独的 `shutdown()`。

2. **`Start()` 持有 `u.mu` 时又调用 `u.Stop()` → 自死锁**
   `sync.Mutex` 不可重入。表现是"握手超时后程序永久卡死，连错误提示都出不来"。
   修复：拆出无锁的 `stopLocked()`，供 `Start()` 内部使用。

3. **`device.IpcGet()` 可能长期持锁**
   在设备密集握手 / 通道重建时会被长时间持有。修复：放进 goroutine 并加 2 秒超时，
   保证等待逻辑本身永远可中断。

4. **`IpcGet()` 是无参函数**（不是 `IpcGet("key=")`），
   `Device.Close()` 无返回值，`netstack.Net` 没有 `Close()`（要关的是 `tun.Device`），
   `conn.Endpoint` 没有 `DstPort()`（只能从 `DstToString()` 里拆）—— 都已按实际 API 调整。

### 事故记录：`gofmt -w .` 污染了 Go 模块缓存

`scripts\lint.ps1 -Fix` 原本执行 `gofmt -l .` / `gofmt -w .`。
项目里有 `.cache\gomod`（Go 模块缓存）和工具链源码，`gofmt -w .` 把它们一起格式化了，
导致模块缓存内容与 `go.sum` 校验和不一致。

处置：
1. 删除 `.cache\gomod` 与 `.cache\gobuild`（**都在项目目录内**），`go mod tidy` 重新拉取；
2. `lint.ps1` 改为只扫 `@('cmd','internal')`，并在脚本里写明**绝不能对 "." 跑 gofmt -w**；
3. 全部依赖仍只落在 `F:\chain_proxy\.cache\`，未污染任何外部目录。

### 验证结果

```
go test ./...
ok  chainproxy/internal/config
ok  chainproxy/internal/proxy
ok  chainproxy/internal/wgproxy      ← 含外层 UDP 测试

$ go test ./internal/wgproxy -run TestSocksUDP -v
✅ 前置代理 UDP 往返正常：来自 1.1.1.1:53 的 DNS 应答 61 字节
PASS
```

### 已知遗留（诚实记录）

**完整隧道的握手与数据转发尚未跑通。**
本机假服务端测试里，客户端持续发送 `handshake initiation`、服务端收不到任何包，
最终 12 秒握手超时。已确认不是锁或超时逻辑的问题（上面三个死锁都修掉了），
问题应在 `socksBind.Send()` → 底层 UDP 的投递环节，或服务端侧。

因此 `TestWireGuardEndToEnd` **默认跳过**（设置 `CHAINPROXY_WG_E2E=1` 可复现），
避免日常 `go test ./...` 变红。UI 里 WireGuard 类型可正常配置，
启动失败会给出明确错误而不是静默卡住。

**socks5 / http 住宅代理（也就是主要需求）不受影响，全部功能完整可用。**

复现方式：
```powershell
$env:CHAINPROXY_WG_E2E=1
$env:CHAINPROXY_WG_VERBOSE=1    # 打开 wireguard-go 详细日志
go test ./internal/wgproxy -run TestWireGuardEndToEnd -v
```

---

## 005

**commit**: `fix(gui): 修复"点任何按钮都没反应"与界面布局错位`

**现象**（用户实测）：双击 `dist\chainproxy.exe` 后，**点任何功能都没有任何反应**；
界面布局错位、按钮文字超出控件。

**结论**：不是一个 bug，而是**六个叠加的 bug**。其中第一个是"点了没反应"的根因。

---

### Bug 1（根因）：控件 ID 超过 16 位，按钮通知全被截断

原来 `groupID(g) = id(g) << 16`，于是控件 ID 形如 `0x1000A`（65546）。
`GetDlgItem(0x1000A)` 能正常找到控件、界面也画得出来，
**但按钮点击时 Windows 发来的 `WM_COMMAND` 里 `wParam = MAKEWPARAM(控件ID, 通知码)`，
而 `MAKEWPARAM` 只装得下控件 ID 的低 16 位** —— 到了窗口过程里 `cid` 变成了 `0x0A`（10），
于是 `switch id(cid)` 永远匹配不上任何分支，**所有按钮都静默失效**。

定位过程（差点被误导）：一开始以为是"消息没送到"，于是加了计数器——
计数器确实在涨，因为编辑框的 `EN_CHANGE` 也会发 `WM_COMMAND`，
但它带的是编辑框的 ID，把"最后一条消息"的记录覆盖掉了，
看起来像"消息到了但按钮不响应"。改成**按控件 ID 分别计数**后才看清：
点按钮根本没产生对应 ID 的 `WM_COMMAND`。

修复：
- `groupID(g) = id(g) * 100`，所有 ID 落在 16 位内；
- 原来的 ID 常量写的是 `groupID(g) | n`，而 `|` 是**按位或**（`400|20 = 404` 不是 420），
  虽然碰巧没冲突但语义脆弱 → 全部改成 `+`；
- 新增 `checkIDOK()`：创建控件前校验 ID ≤ 0xFFFF，越界**直接 panic 并说明原因**，
  让同类错误在开发期就爆出来，而不是变成"点了没反应"。

### Bug 2：标签被推到负坐标 —— "布局错位"的直接原因

`placeLabelRight` / `labelRightAuto` 把标签**右对齐**到传入的 x 上（控件放在 `x-标签宽`）。
每行第一个标签传入的 x 是 `pad+16 = 32`，而标签宽 70，于是 **x = -38**：
整列标签都跑到窗口左边界外面去了。

修复：改成**左对齐**（`placeLabel` / 对话框里的 `labeled`），标签放在 x，输入框跟在它右边。
同时给布局体检加了**负坐标检测**——之前只查了右/下越界，所以这个问题一直没被自动发现。

### Bug 3：`CreateFontW` 参数多传了一个

`CreateFontW` 一共 14 个参数（最后一个是字体名），而封装里传了 15 个：
真正的字体名参数被错位，界面实际用的是**系统回退字体**，
中文渲染宽度远大于预期，于是 23 处按钮/标签文字溢出。

修复：按正确签名重写 `createFont(height, weight, face)`，并把字号显式收敛到
`fontUI=15 / fontSmall=13 / fontMono=14 / fontTitle=22`。

### Bug 4：`EM_*` 消息号错误 —— 日志框完全哑掉

`EM_REPLACESEL` 写成了 `0x177`（正确是 `0x00C2`），`EM_SCROLLCARET` 写成 `0x115`（正确 `0x00B6`）。
消息发给 EDIT 无人处理、直接被丢弃，**日志框永远是空的**。
用户点"一键测试"后什么都不会出现 —— 这是"点了没反应"的**观感来源之一**。

修复：按 `EM_* = 0x00B0 + n` 的顺序定义常量（`emReplaceSel` / `emScrollCaret` / `emSetSel` / `emGetLineCount` …）。

### Bug 5：布局按"我以为的窗口宽度"估算，而不是实际客户区

窗口按 1060×762 创建，`WS_OVERLAPPEDWINDOW` 的标题栏与边框吃掉之后，
**真实客户区只有 1044×723**，而排版完全按 1060 算 —— 右边 16px、下边 39px 的控件全部掉出窗口。
加上所有宽度都是"中文字数 × 16px"拍脑袋估的（实际每字约 33px），溢出成片。

修复：
- 排版一律用 `GetClientRect` 拿到的**真实客户区**尺寸；
- 新增 `win_autosize.go`：`measure()` 用 GDI `GetTextExtentPoint32W` 量出**真实文字宽度**，
  `buttonAuto()` / `labelAuto()` / `fitGroupTitle()` / `ellipsize()` 据此决定控件宽度，
  不再有任何估算；放不下的分组标题与说明文字自动截断加省略号。

### Bug 6：自检的消息泵用了阻塞式 `GetMessage`

`pumpMessagesFor` 原本调用 `GetMessageW`，它会**阻塞到下一条消息到达**，
而 `WM_TIMER` 是 1 秒一次 —— 每次"泵 120ms"实际要等将近 1 秒，
自检慢到不可用，而且"点击后立刻观察效果"的时序全被打乱，
导致上一轮排查得出了"BM_CLICK 无效"的错误结论。

修复：改用非阻塞的 `PeekMessageW(PM_REMOVE)`，配合外层 deadline 才可控。

---

### 新增的自动化防线

`internal/gui/win_layout.go` —— **布局体检**
用 GDI 量出每个控件的文字宽度，与控件实际宽度比对，报告两类问题：
- 控件越出客户区（**左/上/右/下四个方向都查**）；
- 文字比控件宽（会显示不全）。
主窗口与编辑对话框都跑，`scripts\test_ui.ps1` 会把这两个数字作为断言。

`internal/gui/win_clicksim.go` —— **点击模拟**
向按钮控件本身发 `BM_CLICK`（与真实鼠标点击完全同一条通知路径），
再按"预期效果"逐条等待并断言，把三种失败分开报告：
- `点不动`：窗口过程没收到这个按钮的命令；
- `无变化`：命令到了但预期效果没出现；
- `ID 不匹配`：收到了命令但 ID 对不上（正是 Bug 1 的特征）。
`BM_CLICK` 万一没产生 `WM_COMMAND` 还会自动兜底直投一条，并注明用了哪条路径。

### 验证结果

```
$ .\scripts\test_ui.ps1 -Repeat 2
[PASS] 第 1/2 次：窗口可见且控件齐全 (38 个)
[PASS] 第 1/2 次：没有控件越出窗口  (越界 0 个)
[PASS] 第 1/2 次：没有文字被控件截断 (截断 0 个)
[PASS] 第 1/2 次：所有按钮点击都有响应 (❌ 0 / ⚠️ 0)
[PASS] 对话框没有控件越出窗口 / 没有文字被截断
[PASS] GUI 响应正常
[PASS] GUI 启动后自动占用端口 (2 个监听)
[PASS] 经 10810 的出口 IP 与前置不同 (前置 43.251.135.130 → 住宅 71.227.161.120)
[PASS] GUI 退出后端口释放
=== GUI 自检全部通过 ===
```

点击模拟明细（修复前是 0/5，现在是 5/5）：

```
✅ 主开关      点击前 running=true 现在 running=false 按钮="启动"
✅ 保存前置设置  日志从 12 行增加到 13 行
✅ 测试前置     日志从 13 行增加到 14 行
✅ 一键测试     日志从 14 行增加到 16 行
✅ 清空        清空后日志 1 行
=> 5/5 通过
```

`.\scripts\lint.ps1`（gofmt + vet + test）全部通过；`go test ./...` 全绿。

### 教训（写给以后）

1. **Win32 控件 ID 必须 ≤ 0xFFFF** —— 通知机制只传低 16 位。已经用 `checkIDOK` 钉死。
2. **布局不要估算，要实测** —— 中文字宽是字号的两倍左右，凭"字数 × 16"估必然溢出。
3. **布局体检必须查四个方向** —— 只查右下会漏掉"被推到负坐标"这类整列错位。
4. **`EM_*` / `BM_*` 都是 0x00B0 / 0x00F0 起的连续消息号，不是 WM_USER+**，写错就是静默失效。
5. **调试消息循环时不要用 `GetMessage`**，它阻塞，会把时序结论带偏。

---

### 005 补充：布局体检升级为"三查"，并修完对话框

在第一轮修复之后，我把布局体检从"两查"升级为**三查**，立刻又抓出一批问题：

1. **新增重叠检测**：控件互相压住也要报。
   但要排除"分组框/大面板装子控件"这种正常包含关系，
   所以先把"完全包含另一个控件"的控件标记为容器并跳过，只对叶子控件两两判相交。
   这一查立刻报出对话框里 3 对重叠：
   - 「解析并填入」按钮压住快速导入输入框；
   - 「类型」下拉框压住「启用此代理」勾选框；
   - 「名称」输入框压住「类型」标签。
   另外我自己在控制台几何数据里也发现主窗口的**状态点与状态文字被右上角两个按钮盖住**
   （点的 x=802、文字 x=842，而按钮占 798~942）——直接看截图很容易忽略，数字一列就露馅。
   已把状态区改为"排在按钮左边"。

2. **新增左/上越界检测**：原先只查右/下越界，
   而"标签右对齐被推到负坐标"恰恰是从左边跑出去的，所以一直没被发现。

3. 顺带把主窗口与对话框的文字宽度全部收敛到实测范围内：
   - 副标题由 "本地主流客户端作前置 → 你的住宅 IP 作出口，按需走住宅流量"（需 706px）
     缩短为 "本地主流客户端作前置 → 住宅 IP 作出口"（需 364px）；
   - 主开关按钮文案由「停止并释放端口」改为「停止」（原名需 230px，控件只有 84px）；
   - 顶部状态文案由「运行中 · 2/2 条链路已占用端口」改为「运行中 · 2/2 链路」并加自动省略号。

**最终体检结果（主窗口与对话框都干净）：**

```
=== 布局体检 ===
客户区尺寸 = 1044 x 723
超出客户区的控件：0 个
文字宽度超过控件宽度：0 个
互相重叠的控件：0 对
  ✅ 没有发现布局问题

=== 对话框布局体检 ===
客户区尺寸 = 604 x 661
超出客户区的控件：0 个
文字宽度超过控件宽度：0 个
互相重叠的控件：0 对
  ✅ 没有发现布局问题
```

`scripts\test_ui.ps1` 现在把**越界数 / 截断数 / 重叠数 / 点击失败数**四项全部作为断言，
任何一项不为 0 都会让自检失败——"界面错位"和"点了没反应"这两类问题从此不会再静默复发。

---

## 006

**commit**: `fix(wireguard): 打通 WireGuard 隧道 + 支持预共享密钥 + 修下拉框读值`

**起因**：用户给了一份真实的 WireGuard 客户端配置要求测试支持度。这份配置立刻暴露出三件事：
缺 `PresharedKey` 字段、端点在自己的局域网内（192.168.31.94）、以及一个**与 WireGuard 无关但影响极大的 GUI bug**。

---

### 一、WireGuard 隧道终于打通了（此前一直是"握手超时"）

之前几轮我一直卡在"握手包发出去了、对端就是不回"，只能标注为实验性。
这次拿到真实服务端后，用**对照组**的办法把问题切开了：

1. **先验证传输层**（`TestPlainUDPTransport`）：用一个最朴素的 `net.ListenUDP` 当接收方，
   确认 `plainUDPChannel` 的收发都没问题 → 通过。
2. **再验证标准 bind**（`TestVanillaLoopback`）：两端都用 wireguard-go 自带的
   `conn.NewDefaultBind()`，完全不经过我的 `socksBind` → **0.32 秒握手成功**。
   这就证明"API 用法 + 服务端配置 + 本机回环 UDP"全都没问题，
   故障一定在我自己的 bind 层。
3. 有了这个边界，问题很快定位到具体几行。

一共修掉 **5 个 bug**：

| # | 问题 | 现象 |
|---|---|---|
| 1 | **`allowed_ip` 写成逗号列表** | UAPI 要求**一行一个前缀**。`allowed_ip=a/24, b/24` 会被当成单个地址解析，报 `unexpected character`，配置直接应用失败 |
| 2 | **`ReceiveFromUDP` 设了 20 秒读超时** | 超时返回的错误被上层当成"通道坏了"，**把收包循环整个干掉** —— 表现为"发出的包有记录、回包永远收不到" |
| 3 | **`receive` 里替换了设备给的缓冲区** | 写了 `if len(bufs[0]) < 2048 { bufs[0] = make([]byte, 2048) }`，想当然"帮设备扩容"。wireguard-go 是按自己分配的缓冲数组管理生命周期的，换掉它会导致**收到的包无法交给解密队列** —— 最迷惑的现象：`receive 收到 92 字节`（握手响应明明到了），但握手永远不完成 |
| 4 | **`device.Close()` 之后又 `tunDev.Close()`** | `device.Close()` **内部已经关了 tun**，netstack 的 `netTun.Close` 不是幂等的，第二次会 `close of closed channel` panic |
| 5 | **`shutdown()` 置空 `b.ch` 后设备还可能再调 `Send`** | nil 解引用 panic（"隧道明明建好了，退出时却崩一下"）。已改为先关设备再关 bind，并在 `Send` 里加空值保护 |

另外把 `plainUDPChannel` 重写为**按地址族分套接字**：原来只建一个
`net.ListenUDP("udp", 0.0.0.0:0)`，实测在 Windows 上 Go 会把它建成**双栈 `[::]` 套接字**，
而从这个套接字往 `127.0.0.1` 发 IPv4 包**不报错也不送达**（`err=nil`，对端收不到）。
现在 IPv4 目标用纯 `udp4`、IPv6 目标用 `udp6`，不再依赖双栈映射行为。

**修复后的实测结果（对用户提供的真实服务端 192.168.31.94:51820）：**

```
第 1 步通过：隧道握手成功，耗时 301 ms
✅ 第 2 步通过：10.8.0.1:80 返回 connection refused ——
   说明 SYN 已穿过隧道且对端回了 RST，隧道双向可用（该端口只是没有服务）
```

> 这里特意说明判定口径：**`connection refused` 是"隧道通了"的正面证据**，
> 不是失败。只有超时才算真的不通。测试代码里也按这个口径断言，
> 免得以后把好消息误报成坏消息。

**经前置代理 前置也通过了**（同样 301 ms 握手）——说明
`自研 socksBind →主流客户端的 SOCKS5 UDP relay → WireGuard 服务端` 这条完整链路是通的，
WireGuard-over-前置代理 这个最初的目标**现在是经过验证的**。
（本例中主流客户端会把 192.168.31.94 这个私有地址按默认规则直连出去，所以能通。）

### 二、新增 `PresharedKey` 支持

用户的配置里带 `PresharedKey`，而我原来的模型根本没有这个字段 —— 这种情况会**永远握手失败**，
且 WireGuard 协议不会告诉你原因（不同密钥的包被静默丢弃）。
现在：配置模型、UAPI（`preshared_key=`）、GUI 对话框字段全部补齐。
对话框里那一行放在 MTU 右边，命名为「预共享密钥」。

### 三、又修出一个 GUI 严重 bug：下拉框永远读回第一项

`comboText()` 里 `CB_GETLBTEXT` 的 wParam 传了死下标 **0**，而它需要的是**当前选中项的下标**。
后果：

- 前置代理类型永远被读成 `socks5` —— **选「直连」完全不生效**；
- 住宅代理类型也永远被存成 `socks5` —— **用户选 http 或 wireguard，保存下去都变成 socks5**。

等于说 WireGuard 类型在界面上**根本存不进去**，这也解释了为什么之前"看起来配好了却没用"。
修复：先 `CB_GETCURSEL` 取当前下标，再 `CB_GETLBTEXT` 读文字。
并给 UI 自检加了断言：对话框自检报告里必须出现 `类型="wireguard"`。

### 四、新增的诊断能力

- `PublicKeyFromPrivate()`：由私钥推导**本端公钥**并在日志里打出来。
  WireGuard 最常见的配置错误就是双方公钥对不上，而协议只会静默丢包；
  把本端公钥直接显示出来，用户就能一眼比对服务端 peer 配置。
- 握手成功/失败都打印设备 UAPI 状态（含 `last_handshake_time_sec`、`tx_bytes`、`rx_bytes`、
  `allowed_ip`、`preshared_key`），一眼就能分清"配置没生效"和"对端不回包"。
- 三个可复现的测试：`TestPlainUDPTransport`（传输层）、`TestVanillaLoopback`（对照组）、
  `TestWireGuardRealServer`（对真实服务端，含直连与经前置两种模式）。

### 验证结果

```
go test ./...
ok  chainproxy/internal/config
ok  chainproxy/internal/proxy
ok  chainproxy/internal/wgproxy

TestWireGuardEndToEnd   ✅ 握手 301ms + 隧道内 TCP 往返正常
TestPlainUDPTransport   ✅ 收发双向正常
TestVanillaLoopback     ✅ 标准 bind 对照通过
TestWireGuardRealServer ✅ 直连 与 经前置代理 前置 均通过
test_ui.ps1             ✅ 全部通过（含预共享密钥字段与下拉框读值断言）
```

### 遗留

- WireGuard 上游仍不支持客户端侧 UDP 转发（`SupportsUDP()` 返回 false）。
  浏览器走 TCP，不影响使用。
- 隧道内域名解析目前是"本地解析后再送进隧道"。若要访问本地被污染的域名，
  需要把 DNS 也指向隧道内（后续可加）。
