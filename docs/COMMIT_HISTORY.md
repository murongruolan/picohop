# 提交记录说明（COMMIT_HISTORY）

> **改名说明（2026-10-13）**：本文档成文时项目叫 ChainProxy。2026-10-13 项目改名为
> **PicoHop**（Go module 路径 `github.com/murongruolan/picohop`）。
> **本文正文一律保留旧名不改写** —— 这份文档的价值就在于它是当时真实状态的记录，
> 把名字替换掉等于伪造历史。因此本文里出现的 `ChainProxy` / `chainproxy` /
> `CHAINPROXY_*` / `dist\chainproxy-cli.exe` 等，指的都是今天的 PicoHop，
> 只是当时的名字与文件名。
>
> **分支改名（2026-10-13）**：迁移期用的分支 `wails-v3` 已去掉 —— `main` 当时停在迁移前的
> 状态（落后 112 个提交），本轮把 `main` 快进到当前状态后删掉了 `wails-v3`。
> 因此本文里出现的 `wails-v3` 指的就是今天的分支 `main`，正文不改写。

> 这个文档的用途：**几个月后回来还能看懂自己每一行代码是怎么来的、为什么这么写。**
>
> 维护约定：
> 1. 每一次 `git commit` 之后，**必须**在本文档顶部的"提交索引"里新增一行；
> 2. 索引行的链接指向下方对应的详细章节；
> 3. 详细章节必须写清楚：**改了什么文件、为什么改、关键设计取舍、验证方式、已知遗留**；
> 4. 重构类提交要写清"把什么搬到了哪里、为什么搬"；
> 5. 修复类提交要写清"什么现象、根因、改法、如何确认不再复现"。
> 6. 提交前跑 `.\scripts\check_history.ps1`：它拿最近几个提交和下面的索引比对，缺登记直接报错（028 起）。

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
| 006a | `fix(wireguard): 修复 Stop 死锁（点停止会卡死）+ 补充局域网/出口的判定说明` | 2026-10-10 | `9769d09` —— v1 阶段的历史提交，详见文末[合并说明](#006a-u) | [详情](#006a-u) |
| 006b | `feat(wireguard): 内置密钥生成与「本端公钥」实时显示` | 2026-10-10 | `dd7242d` —— v1 阶段的历史提交，详见文末[合并说明](#006a-u) | [详情](#006a-u) |
| 006c | `fix(gui): 修复「生成密钥 / 复制公钥」点了没反应（对话框窗口类重复注册）` | 2026-10-10 | `4934bbf` —— v1 阶段的历史提交，详见文末[合并说明](#006a-u) | [详情](#006a-u) |
| 006d | `fix(gui): 修复切换类型后输入框不跟随显隐（通知码被一刀切过滤）` | 2026-10-10 | `65d7fff` —— v1 阶段的历史提交，详见文末[合并说明](#006a-u) | [详情](#006a-u) |
| 006e | `fix(wireguard): 修复 bind.Close 无法唤醒收包协程导致的死锁 + 支持链路专属前置` | 2026-10-10 | `deed598` —— v1 阶段的历史提交，详见文末[合并说明](#006a-u) | [详情](#006a-u) |
| 006f | `feat(wireguard): 全隧道 AllowedIPs 校验 + 一键出口验证脚本 + 修正测试 URL` | 2026-10-10 | `3eaf3ea` —— v1 阶段的历史提交，详见文末[合并说明](#006a-u) | [详情](#006a-u) |
| 006g | `feat(scripts): test_wg.ps1 增加前置 UDP 能力预检 + 私钥完整性校验` | 2026-10-10 | `94f74f7` —— v1 阶段的历史提交，详见文末[合并说明](#006a-u) | [详情](#006a-u) |
| 006h | `fix(wireguard): 修正"前置->WG 不通"的错误结论（实为测试自身缺 keepalive）` | 2026-10-10 | `1a2fe40` —— v1 阶段的历史提交，详见文末[合并说明](#006a-u) | [详情](#006a-u) |
| 006i | `fix(scripts): PowerShell 脚本改为带 UTF-8 BOM，兼容 Windows PowerShell 5.1` | 2026-10-10 | `61de5fd` —— v1 阶段的历史提交，详见文末[合并说明](#006a-u) | [详情](#006a-u) |
| 006j | `chore(cleanup): 清除代码里残留的真实 WireGuard 密钥，重置本地配置` | 2026-10-11 | `5d1ed05` —— v1 阶段的历史提交，详见文末[合并说明](#006a-u) | [详情](#006a-u) |
| 006k | `fix(gui): 加宽 WireGuard 密钥框并给每个密钥加一键复制` | 2026-10-11 | `424109d` —— v1 阶段的历史提交，详见文末[合并说明](#006a-u) | [详情](#006a-u) |
| 006l | `fix(gui): 【根因】对话框里填的参数从来没被保存过（返回值被丢弃）` | 2026-10-11 | `574535f` —— v1 阶段的历史提交，详见文末[合并说明](#006a-u) | [详情](#006a-u) |
| 006m | `fix(wireguard): 去掉收包时的源地址过滤（会静默丢包的隐患）+ 新增通道探测脚本` | 2026-10-11 | `d5b4f16` —— v1 阶段的历史提交，详见文末[合并说明](#006a-u) | [详情](#006a-u) |
| 006n | `chore(scripts): 新增云服务器 WireGuard 搭建脚本（用于在可控服务端上定位问题）` | 2026-10-11 | `47ae714` —— v1 阶段的历史提交，详见文末[合并说明](#006a-u) | [详情](#006a-u) |
| 006o | `docs: 记录对外部真实 WireGuard 出口节点的端到端验收结果` | 2026-10-11 | `fe20bdf` —— v1 阶段的历史提交，详见文末[合并说明](#006a-u) | [详情](#006a-u) |
| 006p | `docs: 定位到之前那台服务器 WG 连不通的根因（端口映射协议选成了 TCP）` | 2026-10-11 | `6e7eec5` —— v1 阶段的历史提交，详见文末[合并说明](#006a-u) | [详情](#006a-u) |
| 006q | `docs: 更正那台服务器的结论（面板写 TCP/UDP，但 UDP 实际不生效）` | 2026-10-11 | `3757600` —— v1 阶段的历史提交，详见文末[合并说明](#006a-u) | [详情](#006a-u) |
| 006r | `docs: 记录在 NAT 服务器上部署 VLESS+Reality+xHTTP 的完整过程与验证结果` | 2026-10-11 | `d9802b3` —— v1 阶段的历史提交，详见文末[合并说明](#006a-u) | [详情](#006a-u) |
| 006s | `feat(reality): 增加 WARP 出口入站，实现 direct + WARP 双出口` | 2026-10-11 | `da1f990` —— v1 阶段的历史提交，详见文末[合并说明](#006a-u) | [详情](#006a-u) |
| 006t | `chore(reality): WARP 入站改为独立端口 51822（公网 30023），与 WireGuard 端口解耦` | 2026-10-11 | `baa8609` —— v1 阶段的历史提交，详见文末[合并说明](#006a-u) | [详情](#006a-u) |
| 006u | `chore(build): 允许 Go 自动切换工具链，并把工具链下载也约束在项目目录内` | 2026-10-11 | `529c6e9` —— v1 阶段的历史提交，详见文末[合并说明](#006a-u) | [详情](#006a-u) |
| 007 | `refactor(config): 配置模型升级为 v2（Node + Settings + 前置节点引用）` | 2026-10-11 | `upstreams`→`nodes`、新增 `settings`、前置改三选一、协议元信息一处定义、新增 `internal/ports`；**这是多提交协同重构的第一步，在 011 之前整棵树编译不过**，属于有意为之 | [详情](#007) |
| 008 | `feat(engine): 内嵌 sing-box 提供 VMess/VLESS/SS/Trojan/Hysteria2 出口` | 2026-10-11 | 每节点一个"只有出口没有入口"的 sing-box 实例、`detour` 表达前置、构建标签 `with_utls,with_quic`、UDP 通道两种形态适配 | [详情](#008) |
| 009 | `feat(app): 节点状态机（激活链、今日统计、测试编排）` | 2026-10-11 | `PlanActivation` 拓扑排序 + 环路检测（顺手修掉 `Auto` 漏算首节点的 bug）、今日统计增量折算、测试并发控制 | [详情](#009) |
| 010 | `feat(import): 分享链接编解码 + 二维码 + 屏幕圈选识别` | 2026-10-11 | 新增 `internal/share` 与 `internal/qrscan`：SIP002/legacy ss、vmess base64 JSON、Reality 参数；GDI 截图 + DPI 感知 + 自画十字准星 | [详情](#010) |
| 011 | `refactor(cli): cmd/chainproxy 改为无界面命令行，移除纯 Win32 界面` | 2026-10-11 | 删除 `internal/gui` 全部 18 个文件与 3 个只服务旧界面的脚本，重写命令行；旧界面代码保留在 tag `v0.1-win32-gui` | [详情](#011) |
| 012 | `feat(desktop): 用 Wails + Vue 3 重写界面` | 2026-10-11 | 默认 8 列 + 19 列可选、右键菜单 13 项、5 种通知、设置 4 个标签页、添加节点对话框把端口排在协议参数之前；新增 `cmd/uishot` 截图工具 | [详情](#012) |
| 013 | `chore(scripts): 构建脚本带上 sing-box 构建标签，新增界面截图与端到端脚本` | 2026-10-11 | `build.ps1` 固化 `-tags with_utls,with_quic`；`lint.ps1` 加"引擎自检"；新增 `ui_shot.ps1`；`test_e2e.ps1` 跟上新配置模型 | [详情](#013) |
| 014 | `docs: 更新项目介绍、架构、使用说明、开发进度与提交说明` | 2026-10-11 | README / ARCHITECTURE / USER_GUIDE 全面改写为 v2；PROJECT_PROGRESS 追加 v2 专节；本文件追加 007-014 | [详情](#014) |
| 015 | `fix(desktop): 通知重叠 / WebView2 数据目录越界 / 端口框宽度 + 补充运行时验证` | 2026-10-11 | 自己实现通知（ElNotification 同一 tick 连发会重叠）、WebView2 配置目录改到 data\webview、uishot 改用 PrintWindow 不抢焦点；补验需求 (4)(5)(7)(8)(10)(11) | [详情](#015) |
| 016 | `feat(desktop): 列表改通栏 + 表头可拖拽调列宽（带最小宽度保护）` | 2026-10-11 | 列表左右不留白；列宽拖拽需要 border、库内下限仅 30px 会切字故自行钳到 56px、最后一列默认不可拖需 allow-drag-last-column；列宽持久化 + 重置入口 | [详情](#016) |
| 017 | `fix(desktop): 最右一列作为填充列，右端永远不留白` | 2026-10-11 | 根因是"拖拽会把内部 column.width 写成数字→不再有弹性列"（table-layout.mjs 用 !isNumber(column.width) 判定弹性）；改为让最右列只吃 min-width 并在 dragend 清空其 width；地址/备注/前置改回固定列；验证右侧留白 0px | [详情](#017) |
| 018 | `test: 端口相关测试不再依赖机器的端口占用状态` | 2026-10-11 | 写死 10810/10815 导致"开发机上开着 ChainProxy 时测试变红"，极易被误判成回归；改为动态找连续空闲端口作为基准，其余断言改为相对基准 | [详情](#018) |
| 019 | `feat(desktop): 对话框可拖动不裁切 + 长表单内部滚动 + 按钮做大居中 + 取消改警告色` | 2026-10-11 | 去掉弹窗顶部的导入区块；el-dialog draggable 默认即"可拖但被钳制在视口内"；长表单靠 max-height + body 滚动；修 `.cp-dialog .el-dialog` 选择器不匹配的坑；关闭图标 20px、按钮 36px 居中、取消橙色 | [详情](#019) |
| 020 | `docs(migration): Wails v3 迁移技术验证（试点实测通过，GO）` | 2026-10-11 | 在 .cache 建试点工程真机验证：裸 `go build -tags production` 可构建、第二窗口(无边框+置顶)可用、托盘/全局快捷键/系统通知均通过、`Call.ByName` 免绑定生成器、WebviewUserDataPath 可留项目内；wails3 CLI 装入项目内；v2 打 tag v2.0-wails2 可回退 | [详情](#020) |
| 021 | `feat(desktop)!: 桌面外壳从 Wails v2 迁移到 v3（托盘常驻/多窗口/全局快捷键/系统通知）` | 2026-10-11 | main.go 全重写 + 新增 shell.go/theme_*.go；API 改 v3 服务钩子；前端桥换 @wailsio/runtime（只改 api.js）；删 wailsjs 与 wails.json；构建改裸 go build（不依赖 wails3 CLI/task）；实测托盘/快捷键/关窗常驻/数据目录全部通过 | [详情](#021) |
| 022 | `feat(desktop): 添加/编辑节点改为独立置顶浮动窗口（可拖到主窗口之外）` | 2026-10-11 | 新增 float.go：OpenFloatDialog 建无边框置顶窗口并阻塞等结果；复用同一份前端（?float=kind + body class），无需第二个入口；结果经 ui.onNodeDialogClosed → SubmitFloatDialog 回传；实测超出主窗口右 270px/下 94px 且不被裁剪 | [详情](#022) |
| 023 | `feat(config): 全局快捷键可自定义 + 设置新增「快捷键」页` | 2026-10-11 | Settings 增 ShortcutToggleWindow/ShortcutToggleAll（空=默认、none=关闭）；默认改 Ctrl+Alt+Shift+A 以避开微信截图的 Ctrl+Alt+A；ApplyShortcuts 先注销再注册并逐条报结果；设置页新增录制框，只按一个键会被拒绝 | [详情](#023) |
| 023a | `(合并) 二维码圈选覆盖层重做` | 2026-10-11 | 6 条：`7bc1ce6`…`ae44aa3` —— 见文末[合并说明](#023a-f) | [详情](#023a-f) |
| 023b | `(合并) 底部面板：从设计契约到七个视图` | 2026-10-12 | 13 条：`14b2623`…`fed15c2` —— 见文末[合并说明](#023a-f) | [详情](#023a-f) |
| 023c | `(合并) sing-box → mihomo 整体换装` | 2026-10-12 ~ 2026-10-13 | 30 条：`332ebe4`…`011a472` —— 见文末[合并说明](#023a-f) | [详情](#023a-f) |
| 023d | `(合并) 24 项审计修复` | 2026-10-12 | 27 条：`c376194`…`601e618` —— 见文末[合并说明](#023a-f) | [详情](#023a-f) |
| 023e | `(合并) 桌面外壳与设置面板` | 2026-10-11 ~ 2026-10-13 | 13 条：`fad295d`…`ce3db4d` —— 见文末[合并说明](#023a-f) | [详情](#023a-f) |
| 023f | `(合并) 脱敏、文档与仓库卫生` | 2026-10-11 ~ 2026-10-13 | 10 条：`45e1718`…`041eca6` —— 见文末[合并说明](#023a-f) | [详情](#023a-f) |
| 024 | `refactor: 项目改名为 PicoHop（Go module 改为 github.com/murongruolan/picohop）` | 2026-10-13 | 三类大小写敏感替换共 423 处 / 102 文件；module 与 98 处 import 同步改名；历史文档只加横幅、不改写正文；门禁全绿，产物体积与改名前一致 | [详情](#024) |
| 025 | `fix(test): 真连接延迟改为取样多次取最小值（与主流客户端同口径）` | 2026-10-13 | 同一节点主流客户端100-200ms 而我们 700+ms 的根因：它循环两次取最小值且**复用连接**，我们是单次冷连接；新增 `realTestAttempts`、默认地址改 `google/generate_204`、冷连接样本进日志 | [详情](#025) |
| 026 | `docs: 增加 GPL-3.0 许可证与依赖许可说明` | 2026-10-13 | 仓库准备公开：没有许可证默认是"保留所有权利"，而本项目静态链接了 GPL-3.0 的 mihomo，属于不合规分发；LICENSE 逐字节取自 mihomo 模块缓存（sha256 一致），README 说明为什么只能是 GPL-3.0 及代价 | [详情](#026) |
| 027 | `test,docs: 去掉对仓库目录名的耦合（测试断言改为校验 module 路径）` | 2026-10-13 | 改目录真正的技术障碍：测试断言写死目录名 + 21 处 `F:\chain_proxy` 绝对路径；改完只剩"关程序 + Rename-Item"两步 | [详情](#027) |
| 028 | `docs(history): 补齐 023 之后 99 个提交的索引与合并说明 + 追加漂移检查脚本` | 2026-10-13 | 索引停在 023 之后积了 99 条（我早期误报为 90）；按主题归并成 023a–023f 六行，hash 集合与 git log 逐条比对通过；新增 `scripts/check_history.ps1` 防再漂移 | [详情](#028) |
| 029 | `chore(git): 分支收归 main（去掉迁移期的 wails-v3 名字）` | 2026-10-13 | 改名时才发现 `main` 已存在且停在迁移前（落后 112 个提交、0 个独有提交）；不是改名而是"快进 + 去掉旧名"，先 `branch -f` 再 `switch` 以免工作树退回旧状态；回退点仍是两个 tag | [详情](#029) |

> **索引缺口（023a–023f，028 已补齐）**：023 到 024 之间原本积了 **99 个提交没有登记**
> （早期误记为 90 —— 那次用日期过滤数的，漏掉了 023 当天之后的 10 条）。现在按主题合并成
> 六行，逐条 hash 与说明在[文末那一节](#023a-f)。索引里不再留 99 行流水账，理由也写在那里。

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
[0] 前置出口 IP = 203.0.113.20
[PASS] 端口 10810 已占用 / 端口 10811 已占用
[PASS] SOCKS5 链路出口 IP 不同于前置   (203.0.113.20 → 203.0.113.21)
[PASS] HTTP   链路出口 IP 不同于前置   (203.0.113.20 → 203.0.113.22)
[PASS] 第二条链路出口 IP 不同于前置   (203.0.113.20 → 203.0.113.23)
[PASS] 明文 HTTP 请求经链路可达       (203.0.113.24)
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
[PASS] 经 10810 的出口 IP 与前置不同  (前置 203.0.113.20 → 住宅 203.0.113.25)
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

**现象**（用户实测）：双击 `dist\chainproxy-cli.exe` 后，**点任何功能都没有任何反应**；
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
[PASS] 经 10810 的出口 IP 与前置不同 (前置 203.0.113.26 → 住宅 203.0.113.27)
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

---
---

## 007

**commit**: `refactor(config): 配置模型升级为 v2（Node + Settings + 前置节点引用）`
**日期**: 2026-10-11
**范围**: `internal/config`、`internal/ports`

### ⚠️ 先读这一条：这是多提交协同重构的第一步

这次重构（**007 → 014**）拆成 8 个提交，其中 **007 到 011 是"跨提交才成立"的**：

> **在 011 落地之前，整棵代码树是编译不过的。**

这是**有意为之**，不是失误：

- 改动互相咬合（配置模型 → 引擎 → 状态机 → 界面），硬拆成"每一步都编译得过"就必须
  写一堆只为过渡而存在的兼容层，反而更难读、更容易掩盖真正的设计；
- 中间态不对外发布，只存在于本地 git 历史里；
- 每一步都能独立读懂"它负责哪一层"，这比"每一步都能 build"更重要。

所以做 `git bisect` 或 checkout 到 007-010 之间时看到编译失败，**那是预期行为**。

### 改了什么

| 文件 | 说明 |
|---|---|
| `internal/config/model.go` | **新增**（301 行）：`Front` / `TLSOptions` / `TransportOptions` / `WireGuardConfig` / `Node` |
| `internal/config/protocol.go` | **新增**（168 行）：8 种协议的元信息 + `ProtocolInfo` 表单开关 + 各下拉框取值 |
| `internal/config/config.go` | 重写：`upstreams` → `nodes`，新增 `Settings`，`CurrentVersion = 2`，v1→v2 迁移 |
| `internal/config/util.go` | 微调（+2 行） |
| `internal/ports/ports.go` | **新增**（242 行）：端口分配、随机、占用检测 |
| `internal/config/config_test.go` | 16 个测试 |
| `internal/ports/ports_test.go` | 8 个测试 |

### 为什么这么改

**1）`upstreams` → `nodes`。** v1 的 `upstreams` 只装"住宅代理"，语义上就是"上游服务器"；
v2 要装的是 8 种协议的任意出口，而且**它自己也能当别人的前置**。
叫 `Node`（节点）才配得上这个语义。

**2）前置从"全局 / 链路专属"改成三选一。** v1 的问题是链路专属前置只能填一个**临时地址**，
于是"用项目里的另一个节点当前置"这件事无法表达。v2 定义成：

```go
const (
    FrontDirect = ""        // 直连
    FrontGlobal = "global"  // 用设置里的全局前置
    // 其余取值 = 本项目内另一个节点的 ID，用它的本地端口（mixed）当前置
)
```

`FrontDirect` 刻意取**空字符串**：它是 `Node` 结构体的零值，于是"没选前置"天然等于"直连"，
不需要任何额外初始化。

这个设计能成立的原因在 `ARCHITECTURE.md` 里写清楚了：**每个节点本来就在本地开着
一个 mixed 端口**，所以"以另一个节点为前置"只要把前置指向 `127.0.0.1:<它的端口>` 即可 ——
这也是为什么前置永远走回环。

**3）协议元信息只在后端定义一份。** `Protocols()` 返回每个协议的
`Label / DefaultPort / ShowUUID / ShowAlterID / ShowSecurity / ShowFlow / ShowMethod /
ShowPassword / ShowUsername / ShowTransport / ShowTLS / ShowReality / ShowHysteria2 /
ShowWireGuard / TLSDefault / TLSRequired / SupportsUDP`。
前端**读它来决定表单显示哪些字段**，不在前端再写一遍。少一处重复就少一处漂移。

**4）新增 `Settings`，但刻意精简。** 只保留我们**切实会用到**的项：
监听地址/允许局域网、起始端口、空闲超时、启动时自动激活上次激活的节点、
测试相关（TCP 超时 / 并发数 / 真连接地址 / 测速地址 / 测速时长）、界面相关（主题 / 通知时长 / 日志行数）。
不照搬主流客户端/主流客户端臃肿的设置面板。

`AutoActivateOnLaunch` 的注释里特意写明：**这与"注册开机自启动"完全无关，本程序任何情况下都不注册自启动。**

**5）新增 `internal/ports`，把界面上的三条端口规则收进一处：**

- `Next()`：`max(本项目内所有 ≥10800 的端口) + 1`，下端不低于 10810，再顺延到真正空闲的端口。
  为什么要设 `BaselineFloor = 10800`：低于它的端口（比如 1080、8080）不参与取最大值，
  否则一个 `8080` 会把起始值拉低。
- `RandomFree()`：在 `[10001, 49151]` 内随机，**抽到先检测占用**，占用就重抽，最多 5 次，
  失败返回 `ErrNoFreePort`。上界取 49151 而不是 65535，是因为 Windows 默认动态端口范围是
  49152-65535，系统自己会从那一段发临时端口，随机到那段等于在跟操作系统抢端口。
- `Check()`：先查本项目占用表（能说出"被哪个节点占了"），再真的 `net.Listen` 一次
  看系统层面是否被别的程序占用。用"真的 bind 一次"而不是解析 netstat，是因为
  Windows 上 Go 不会给监听套接字设 `SO_REUSEADDR`，bind 失败就是权威答案。

**6）v1 → v2 迁移的第一原则是"行为不变"。** `Load()` 先探测 `version`，小于 2 就先迁移：

- v1 里**没有** `frontOverride` 意味着"走全局前置"，所以迁移后**显式写成 `Front = global`**
  —— 否则用户的住宅代理会突然变成直连，这是最危险的静默行为变化；
- v1 里 `frontOverride.Type == "direct"` 才迁移成直连；
- v1 允许为单条链路指定一个**任意临时** socks5/http 前置，而 v2 的前置只能指向"项目内的节点"，
  无法表达任意地址，于是**退回全局前置**（语义最接近），并把原前置的描述追加进**备注**，
  让用户看得见这次迁移做了什么。

### 怎么验证的

```bash
go test -tags "with_utls,with_quic" ./internal/config/... ./internal/ports/...
```

- `TestMigrateV1ToV2` —— 用真实的 v1 配置（两条住宅 socks5，本地端口 10810/10811）跑迁移，
  断言迁移后两条都变成 `Front = global`（即行为不变）、协议/端口/密钥全部保留；
- `TestValidateRejectsSelfFront` / `TestValidateDetectsFrontCycle` / `TestValidateMissingFrontNode`
  —— 前置指向自己、形成环路、指向不存在的节点，三种都要被校验拦下；
- `TestDataDirIsRepoRoot` / `TestFindRepoRoot` —— 见 012 里那次数
  据目录解析缺陷的修复；
- `TestProtocolMetadata` —— 8 种协议的元信息自洽（该显示 UUID 的必须显示、trojan/hysteria2 必须强制 TLS 等）；
- `TestNextFollowsMaxPlusOne` / `TestRandomRetryCountIsFive` / `TestCheckDetectsProjectAndSystem`
  —— 端口三条规则各自的边界。

### 遗留

- **本提交之后整棵树编译不过**（调用方还是旧的 `cfg.Upstreams`），到 011 才恢复。
- 每节点独立超时还没有（`Node.Timeout()` 目前统一返回 15000，保留方法是为将来）。

---

## 008

**commit**: `feat(engine): 内嵌 sing-box 提供 VMess/VLESS/SS/Trojan/Hysteria2 出口`
**日期**: 2026-10-11
**范围**: `internal/engine`（新增）、`internal/proxy/upstream.go`、`internal/wgproxy`、`go.mod`/`go.sum`、`scripts/build.ps1`

### 改了什么

| 文件 | 说明 |
|---|---|
| `internal/engine/singbox.go` | **新增**（693 行）：配置生成 + 实例生命周期 + 拨号 |
| `internal/engine/register.go` | **新增**：`init` 里把构造器注册给 `proxy` |
| `internal/engine/tags_ok.go` / `tags_missing.go` | **新增**：靠构建标签决定 `buildTagsOK` 与 `missingBuildTags()` |
| `internal/engine/singbox_test.go` | **新增**：10 个测试 |
| `internal/proxy/upstream.go` | 改：出口从"只有 3 种"扩展成"注册表 + 5 种走 sing-box" |
| `internal/proxy/server.go` | 改：随接口调整 |
| `internal/wgproxy/*` | 改：接口签名跟上（38 行级别的调整） |
| `go.mod` / `go.sum` | 引入 `sagernet/sing-box`、`sagernet/sing` |
| `scripts/build.ps1` | 加构建标签 |

### 为什么这么改

**1）每节点一个"只有出口没有入口"的 sing-box 实例。**
不是一个大实例装所有节点。代价是多几份 router，好处是**启停互不影响** ——
这正是"每个节点独立本地端口"所要求的语义。实例里只有 `outbounds`，
没有 `inbounds`（入口由我们自研的 mixed 监听器负责）。

**2）去掉 `inbounds` 还有个直接收益：不需要"本地再开一个 socks 入口再连它"。**
`Upstream.Dial` 直接拿 `adapter.Outbound` 调 `DialContext`，`DialUDP` 直接调 `ListenPacket`
—— 少一个端口、少一次转发。

**3）用 `detour` 表达前置。** 生成两份 outbound：

```
outbounds[0] = 前置出口，           tag = cp-front
outbounds[1] = 本节点出口，detour = cp-front, tag = cp-node
```

`detour` 就是 sing-box 里的链式代理（等价于 Xray 的 `dialerProxy`、mihomo 的 `dialer-proxy`）。
**当节点选择"直连"时，完全不生成 `cp-front`、也不写 `detour`** —— 因为
sing-box 明确不允许 `direct` 出口带 detour（会报 `'detour' is not supported in direct context`）。

**4）注册而不是直接依赖。** `internal/proxy` **不 import** sing-box：

```go
// internal/engine/register.go
func init() { proxy.RegisterSingBoxFactory(New) }
```

和 `wgproxy` 用同一套套路：`proxy` 定义接口，实现方在 `init` 里注册。
这样 `internal/proxy` 的单测不需要编译整个 sing-box，依赖也更干净。

**5）构建标签。** sing-box 把 Reality(uTLS) 与 Hysteria2(QUIC) 放在构建标签后面，
**漏掉标签的后果是编译一切正常、但运行时才报**
`uTLS, which is required by reality is not included in this build`。
这种报错很难联想到编译参数，所以额外加了 `BuildCheck()`：用 `tags_ok.go` / `tags_missing.go`
两个互斥的构建约束把"缺标签"变成编译期的一个常量，运行前就能给出一句明确的话：

```
请用 -tags "with_utls,with_quic" 重新编译（scripts/build.ps1 已内置）
```

**6）UDP 通道适配有两种形态。** sing-box 返回的 `net.PacketConn` 可能是
它自己的 `N.PacketConn`（有 `ReadPacket/WritePacket`），也可能只实现了
`ReadFrom/WriteTo`。所以优先断言 `N.PacketConn`，不行再退到 `nPacketConn` 兜底接口。

另一个关键点：**收包必须开后台协程**。因为 `sing` 的 `ReadPacket` 与
`net.PacketConn.ReadFrom` 都是阻塞且不可取消的，而我们的 `ReceiveFromUDP` 需要能被
`quit` **立刻叫醒**（WireGuard 的 Bind 关闭流程强依赖这一点，否则会死锁）。
队列长度取 128，满了**丢最旧的一条**而不是阻塞 —— UDP 是实时流量，宁可丢旧包也不要堆积。

### 怎么验证的

```bash
go test -tags "with_utls,with_quic" ./internal/engine/...   # 10 个测试
go test -tags "with_utls,with_quic" ./...                   # 全绿
```

- `TestBuildConfigVLESSReality` —— Reality 的字段名/取值（`pbk`/`sid`/`spx` 对应的服务端字段）；
- `TestBuildConfigDirectFrontHasNoDetour` —— 直连时**必须没有** `cp-front` 也没有 `detour`
  （这条是把上面那个报错固化成断言）；
- `TestTransportMapping` —— tcp 时**整个 transport 字段都不写**；ws/http/grpc/httpupgrade 各写各的；
- `TestParseStrictnessIsReal` —— **这条很关键**：它故意塞一个未知字段进去，
  断言 sing-box **真的会报错**。有了它，"我们的配置能被 sing-box 解析成功"这个断言才有约束力
  —— 否则一个静默忽略未知字段的库会让所有配置测试都变成空转；
- `TestHysteria2Options` / `TestVMessOptions` / `TestTrojanOptions` / `TestHTTPFrontUsesHTTPOutbound`
  / `TestNewRejectsUnsupportedProtocol` / `TestBuildCheck`。

**真链式仍然成立**（用自研 socks5 出口复验，因为 sing-box 那 5 种当时没有可控服务端）：

| 环节 | 出口 IP |
|---|---|
| 全局前置（本机前置代理） | `203.0.113.10` |
| 住宅代理 1 | `203.0.113.11` |
| 住宅代理 2 | `203.0.113.12` |

三个 IP 互不相同，说明链式确实生效；探测出口 IP 这一步的耗时对比是
**本地前置 1.5 ms → 经完整链路 201 ms**，两个数量级的差距也说明流量确实绕到了远端。

### 遗留

- **本机没有对真实 Reality / Hysteria2 服务器做端到端连通验证**，
  只验证到「配置能被 sing-box 接受 + 实例能启动 + 拨号能发出去」这一层。
  要做真正的端到端需要一台可控服务端，后续补。
- 单个 sing-box 实例带自己的 router，节点很多时会有固定内存开销。
- `go.mod` 一行涨了 205 行依赖，`go.sum` 涨了 631 行 —— 这是内嵌 sing-box 的必然代价。

---

## 009

**commit**: `feat(app): 节点状态机（激活链、今日统计、测试编排）`
**日期**: 2026-10-11
**范围**: `internal/app`、`internal/tester`

### 改了什么

| 文件 | 说明 |
|---|---|
| `internal/app/nodes.go` | **新增**（645 行）：节点 CRUD、前置解析、`PlanActivation`、激活/停止 |
| `internal/app/stats.go` | **新增**（379 行）：今日统计 |
| `internal/app/ports.go` | **新增**（73 行）：把 `internal/ports` 接到状态机上 |
| `internal/app/app.go` | 重写（674 行改动）：只剩事件、订阅与生命周期骨架 |
| `internal/app/test.go` | 改（341 行）：测试编排 |
| `internal/tester/tester.go` | 改（267 行） |
| `internal/app/nodes_test.go` | **新增**：8 个测试 |

### 为什么这么改

**1）`PlanActivation` 用 DFS 做拓扑排序 + 灰黑着色检测环路。**

```
PlanActivation(["C"])  ──►  Order = [A, B, C], Auto = [A, B]
        （A 是 B 的前置，B 是 C 的前置）
```

"前置在前"保证被依赖的先起来。环路用"灰/黑"两色检测（配置校验里还有一道，双重保险）。
`Deactivate` 反过来：**会连带停止依赖它的节点** —— 否则那些节点会指向一个已经没人监听的
端口，表现为"静默不通"，这种故障最难查。

**2）`Auto` 列表的缺陷（测试抓到的）。**
早先的实现是在递归过程中就地记录 `auto`，写成这样：

```go
if fid := n.FrontNodeID(); fid != "" {
    if !explicit[id] { auto = append(auto, id) }   // ← 错：写在 if fid != "" 里面
    ...
}
```

后果是：**链路最前面那个节点（自己没有前置的）永远不会被计入"将连带启动"的列表** ——
而它恰恰是必须最先启动的那个。用户看到的提示会少一个节点，与实际的 `Order` 不一致。

修法不是"把 if 挪出来"，而是**改成从最终的启动顺序里筛**：

```go
// auto 从最终的启动顺序里筛出来，而不是在递归途中记录 ——
// 这样它和 Order 的顺序一致（都是"实际启动顺序"），界面可以直接照着念
// "将按 A → B → C 的顺序启动"。
for _, id := range order {
    if !explicit[id] { auto = append(auto, id) }
}
```

顺序一致这一点很重要：`Auto` 会被界面直接念给用户听。

**3）今日统计必须"折算增量"。** 每个节点服务内部的计数（`proxy.Stats`）是
**本次运行累计**，而服务对象在"停止再启动"后会被重建、计数归零。
所以 `statsLoop()` 每秒做一次差分：把 `当前值 - 上次基线` 累加进 `Node.UpBytes/DownBytes`，
并在跨天时清零。**不这么做，"今日统计"每次重启都会归零。**

**4）测试编排。** 四种测试（TCP 延迟 / 真连接延迟 / 测速 / 出口 IP）都走
"临时建一条链路"（`openTestChain`），**不占用本地端口**，所以节点没激活也能测。
同一时刻只允许一批测试在跑 —— 否则互相抢带宽会让数据失真。结果写回
`Node.LatencyMs / RealMs / SpeedMBps` 并广播事件。

**5）一条硬约定写进了注释：** 订阅回调必须快速返回，且**不允许再同步调用 App 的方法**
—— 因为事件可能在持有 App 内部锁时发出。

### 怎么验证的

```bash
go test -tags "with_utls,with_quic" ./internal/app/...   # 8 个测试
```

- `TestPlanActivationOrdersFrontFirst` —— 前置一定排在依赖它的节点之前；
- `TestPlanActivationDetectsCycle` —— A→B→A 必须报错而不是死循环；
- `TestDeleteNodesFixesDanglingFront` —— 删掉被当作前置的节点后，
  依赖它的节点前置要被修正，不能留下悬空引用；
- `TestResolveFront` —— 三种前置取值各自解析出正确的 `config.Front`；
- `TestPortAllocationFollowsRules` / `TestNodeCRUD` / `TestAutoActivateDisabled`
  / `TestSaveAndReloadConfig`。

真链路复验（v2 状态机下重新跑一遍两跳链路）：

```
真链路对比（探测出口 IP 耗时）:
  本地前置         1.5 ms   -> 203.0.113.10
  经完整链式       201 ms   -> 203.0.113.11 / 203.0.113.12
```

### 遗留

- 今日统计每 30 秒落盘一次、退出时强制落盘，所以**进程被强杀会丢最多 30 秒的统计**。
- 测试并发数是全局设置，没有做"按节点"的细粒度控制。

---

## 010

**commit**: `feat(import): 分享链接编解码 + 二维码生成/识别 + 屏幕圈选`
**日期**: 2026-10-11
**范围**: `internal/share`、`internal/qrscan`（均为新增）

### 改了什么

| 文件 | 说明 |
|---|---|
| `internal/share/link.go` | **新增**（1086 行）：链接编解码 |
| `internal/share/qr.go` | **新增**（76 行）：二维码 PNG / DataURL |
| `internal/share/link_test.go` / `scan_roundtrip_test.go` | **新增**：12 个测试函数 |
| `internal/qrscan/screen_windows.go` | **新增**（272 行）：GDI 截图 |
| `internal/qrscan/overlay_windows.go` | **新增**（1142 行）：圈选窗口 |
| `internal/qrscan/decode.go` | **新增**（243 行）：二维码识别 |
| `internal/qrscan/screen_other.go` / `overlay_other.go` | **新增**：非 Windows 的空实现 |
| `internal/qrscan/*_test.go` | **新增**：19 个测试函数 |

### 为什么这么改（share）

**1）为什么单独成包。** 分享链接是一份**对外契约**，要和主流客户端/ sing-box /主流客户端互通。
格式细节多到离谱：**base64 的四种变体**（标准/URL 安全 × 有无 padding）、参数名拼写、
vmess JSON 里野生的大小写与"字符串还是数字"。混进配置包只会让一处改动崩掉一片。
所以这里**只做纯函数**：不读文件、不碰本地端口、不打日志，可以被单独测到底。

**2）编解码只覆盖"描述远端服务器"的字段。** `ListenHost / ListenPort / Front / Active /
CreatedAt / StatsDay` 属于**本机状态，导入时必须由调用方自己决定**
（尤其是本地端口要弹窗让用户设）。从别人的链接里把这些带进来只会覆盖用户的本地设置。

覆盖的格式：

- `vless://` / `trojan://` / `vmess://` / `ss://`（SIP002 与 legacy 两种）/ `hysteria2://`（别名 `hy2://`）
- base64 解码做了**宽松处理**：四种编码变体依次尝试（`主流客户端` 用标准表+padding，
  但实践里四种组合都出现过）；
- `decodeBase64Body` 先按原样解，失败且内容里有 `%` 时再做一次百分号解码 ——
  因为有些客户端会把整段 base64 再 URL 编码一次（`=` 变 `%3D`）；
  **但不能无脑先做 `QueryUnescape`**，因为 base64 自身含 `+`，那个会被变成空格；
- 解析顺序上，**先去引号再去 BOM/空白** —— 反过来的话 `" ss://..."` 前面的空格会挡住引号处理；
- 承认一个现实：**密码里带 `@` 的 trojan 链接**会被对端拆成两个 `@`，所以要按最后一个 `@` 切。

### 为什么这么改（qrscan）

**1）GDI 截图 + DPI 感知。** `ensureDPIAware()` 尝试把进程设成 per-monitor DPI aware v2
（`DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2` 是个句柄值 `-4`），且用 `sync.Once` 只设一次
—— 因为这个包可能被命令行/测试单独调用，那时进程默认是 DPI unaware 的。

**2）alpha 一律写死 `0xFF`。** GDI 不保证填这个字节，**实测不少驱动给的是 0**。
原样抄过去会得到一张"全透明黑图"，而 `gozxing` 取色时按 alpha 预乘处理，于是什么都识别不出来。
这是个只有真跑起来才会暴露的坑。

**3）`GetWindowRect` 包含 Windows 的隐形缩放边框。** 按它截图会**把后面的窗口也截进来**
—— 看起来像"多了两条滚动条"。这个缺陷是用**像素分析**定位到的，改成按客户区取图。

**4）圈选窗口：先截图、再建窗口。** 反过来的话，那张"点按钮那一刻的屏幕"就已经
包含圈选窗口自己了，变成套娃。

**5）红色十字准星由自己画，不做自定义光标位图。**
`CreateCursor` 那套 API 在 DPI 缩放 / 多屏下很脆。改成 `ShowCursor(FALSE)` 隐藏系统光标，
然后 `HCursor` 留空、在 `WM_PAINT` 里画 1 像素贯穿客户区的红线。

**6）WM_PAINT 用双缓冲。** 一帧里要"贴整屏原图 + 画边框 + 画准星"三步，
直接画会闪。全部画在内存位图上再一次性 `BitBlt` 过去就干净了。

**7）光标恢复必须走所有退出路径** —— 正常结束、报错、panic 都要 `ShowCursor` 推回 `>= 0`，
否则用户会发现系统光标不见了。

### 怎么验证的

```bash
go test -tags "with_utls,with_quic" -v ./internal/share/... ./internal/qrscan/...
```

| 包 | 顶层测试函数 | 含子测试的 PASS 数 |
|---|---|---|
| `internal/share` | 12 | **84** |
| `internal/qrscan` | 19 | **34** |

- share：`TestDecodeRealSamples`（真实客户端导出的样本）、`TestDecodeTolerance`（缺参数/多余空格/大小写乱写）、
  `TestEncodeDecodeRoundTrip`、`TestDecodeDoesNotSetLocalFields`（**断言导入不会覆盖本地字段**）、
  `TestQRScanToNodeRoundTrip`（分享 → 生成二维码 → 截图 → 识别 → 还原成节点，整条闭环）；
- qrscan：`TestDecodeGeneratedQR` / `TestDecodeResizedQR` / `TestDecodeQRInLargerScene`
  （二维码只占画面一小块也要能识别）、`TestToGrayOpaque`（alpha 那条断言）、
  `TestOverlayReverseDragNormalizesRect`、`TestOverlayCancelByTinyDrag`、`TestOverlayLParamSignExtension`。

> **一个真实修掉的 bug**：反向拖拽圈选时，返回的是 10x10 的碎片，
> 根因是锚点漂移 —— 起止点没有归一化就参与计算。`TestOverlayReverseDragNormalizesRect` 固化了这个行为。

### 遗留

- 二维码识别用的是 `makiuchi-d/gozxing`，对超大截图会先跳过过大的缩放档（`TestBuildAttemptsSkipsHugeScales`），
  代价是极端情况下识别率下降。
- `overlay_other.go` / `screen_other.go` 是非 Windows 的空实现，**圈选与截图目前只在 Windows 上可用**。

---

## 011

**commit**: `refactor(cli): cmd/chainproxy 改为无界面命令行，移除纯 Win32 界面`
**日期**: 2026-10-11
**范围**: `cmd/chainproxy`、`internal/gui`（整体删除）、`scripts`（删 3 个）

### 改了什么

**删除**（18 个文件，约 4900 行）：

```
internal/gui/gui.go            internal/gui/win_api.go       internal/gui/win_syscalls.go
internal/gui/gui_other.go      internal/gui/win_autosize.go  internal/gui/win_table.go
internal/gui/gui_windows.go    internal/gui/win_clicksim.go  internal/gui/win_theme.go
internal/gui/win_controls.go   internal/gui/win_debug.go     internal/gui/win_uitest.go
internal/gui/win_dialog.go     internal/gui/win_layout.go    internal/gui/win_window.go
internal/gui/win_platform.go   internal/gui/win_wndproc.go
```

**同时删除**三个只服务于旧界面的脚本：

```
scripts/dump_list.ps1        （枚举旧界面的控件）
scripts/inspect_window.ps1   （检查旧界面的窗口/控件布局）
scripts/test_ui.ps1          （旧界面的越界/截断/重叠/点击自检）
```

**重写** `cmd/chainproxy/main.go`（544 行改动）为纯无界面工具：

```
ls / list        列出节点
run              启动全部（或指定）节点
test             一键测试
validate         配置校验
fetch            取出口 IP
import           导入分享链接
express          生成分享链接
wgkey            生成 WireGuard 密钥对
engines          检查内嵌引擎的构建标签是否齐全
version / help
```

### 为什么这么删

1. **两套界面不能共存。** 保持 Win32 界面"能用"意味着每次改配置模型都要同步改两处，
   而新界面（012）才是要长期维护的那个。
2. **旧界面代码不丢。** 删除前先打了 tag：

   ```
   git tag v0.1-win32-gui
   ```

   该 tag 指向重构前的最后一个提交，`internal/gui` 的 18 个文件都在里面：
   `git show v0.1-win32-gui:internal/gui/win_dialog.go` 随时能翻出来。
3. **命令行必须留下。** 它是"界面出问题时还能用"的兜底，也是 `lint.ps1` 里
   `validate` / `engines` 两道检查的执行者。所以不是"顺手删掉 gui"，而是
   **把命令行从"兼做界面入口"提升为独立的排障工具**。

### 怎么验证的

```bash
go build -tags "with_utls,with_quic" ./...      # 011 之后整棵树恢复可编译
go run -tags "with_utls,with_quic" ./cmd/chainproxy ls
go run -tags "with_utls,with_quic" ./cmd/chainproxy validate
go run -tags "with_utls,with_quic" ./cmd/chainproxy engines
```

`engines` 会调用 `engine.BuildCheck()`，缺标签时直接给出那句明确的提示。

### 遗留

- 旧界面里的"布局体检 / 点击模拟"那两道自动化防线**随代码一起消失了**，
  取而代之的是 012 的截图验证（人工看 + 像素分析），**自动化程度不如从前**。
  这是这次重构在测试覆盖上唯一明确的退步，记录在此以备后查。

---

## 012

**commit**: `feat(desktop): 用 Wails + Vue 3 重写界面`
**日期**: 2026-10-11
**范围**: `desktop/`（新增）、`cmd/uishot`（新增）、`.gitignore`

### 改了什么

| 部分 | 内容 |
|---|---|
| 后端绑定层 | `desktop/api.go`（556 行）、`desktop/api_import.go`（346 行）、`desktop/main.go`（87 行）、`wails.json` |
| 前端 | `desktop/frontend/src/` 下 6 个组件 + 5 个对话框 + `store.js` / `ui.js` / `actions.js` / `api.js` / `columns.js` / `styles.css` |
| 截图工具 | `cmd/uishot/main.go`（210 行） |
| 图标/清单 | `desktop/build/appicon.png`、`windows/icon.ico`、`info.json`、`wails.exe.manifest` |

### 界面设计与交互（都写在这里，免得以后翻代码）

**表格**

- **默认 8 列**：类型、别名、本地端口、地址、远程端口、传输协议、延迟、速度；
- **「列显示」多选**，共 **19 列**可选，另外 11 列是：今日统计、最近使用、创建时间、备注、
  出口 IP、前置、TLS、活动连接、累计连接（以及真连接、状态）；
- 列显示用 **popover 而不是 multiple select** —— 后者的标签堆在一起很乱；
- **别名列带状态点**（运行/停止/错误）；
- **单击选中 / Ctrl 多选 / Shift 连选，回车激活，双击编辑**；
- **右键菜单 13 项，分四组**：基础 4 项（激活/停止、编辑、克隆、复制本地代理地址）、
  测试 5 项（TCP 延迟、真连接延迟、速度、出口 IP、停止当前测试）、
  分享 2 项（分享、清零今日统计）、危险 2 项（移除所选、移除测试无效项）。

**通知**

- **5 种类型**：`primary / success / warning / info / error`。
  element-plus 自带的 type 没有 `primary`，所以借用 `info` 的图标再用自定义 class 换品牌色；
- **位置刻意放在控制栏下方**（`offset: 64`，顶端控制栏高 52），而不是默认的贴顶 ——
  否则会盖住工具栏按钮；
- 后端事件里的 notify 用 `NUL` 分隔成 `level\0title\0text`，在 `api.js` 一处解析，
  业务侧不用关心编码格式。

**设置面板**

- **4 个标签页**：前置与网络 / 测试 / 运行与界面 / 关于；
- **刻意精简**，只放真正会用到的项；**不注册开机自启**（界面上也没有这个开关）。

**对话框**

- **添加/编辑节点**：布局上有一条刻意的顺序 —— **「本地代理端口」排在协议参数之前**
  （紧跟别名）。理由是绝大多数节点的协议参数一次填好就再也不动了，
  而端口是天天要看的，改端口时不该先滚过一整屏协议配置；
- 协议字段由**后端给的元信息**决定显示哪些，前端不写死；
- **导入后只弹「只有端口」的小窗**：导入进来的链接只描述远端，
  本地端口必须由用户决定，所以单独做一个小对话框只问端口，而不是把整个编辑表单弹出来；
- **分享**：上方正方形二维码、中间协议文本、下方「下载图片 / 复制文本」。

**窗口**

- 默认 1180x740，最小 920x580；
- 主题以配置文件里的 `settings.theme` 为准（`localStorage` 只是缓存），
  切换时同步 Windows 标题栏的明暗；
- stats 事件每秒一次，前端做 700ms 去抖合并，避免事件风暴打满 IPC。

### 截图验证：这一轮实际抓出并修掉的问题

桌面程序的排版读代码是看不出来的，所以新增了 `cmd/uishot` + `scripts/ui_shot.ps1`
真的截图看。这一轮一共揪出：

| # | 现象 | 根因 |
|---|---|---|
| 1 | 端口输入框太窄，`10810` 显示成 **"11"** | 输入框宽度不够，超出部分被直接裁掉 |
| 2 | 「前置节点」选**直连**时显示成占位符 **"请选择"** | 直连用空字符串 `""`，而 el-select 把空字符串当成"没选"，于是显示 placeholder |
| 3 | 远程端口显示 **"0"** | 没有值的数字字段被直接渲染成 0，而不是留空 |
| 4 | socks5 / http 出现**两个密码框** | 字段显示条件重叠，开关组合没排干净 |
| 5 | 工具栏超宽，右侧图标按钮被挤出窗口 | 宽度估算不足；改成超出的分组标题自动截断加省略号 |
| 6 | 截图里"多了两条滚动条" | **`GetWindowRect` 包含 Windows 隐形缩放边框**，按它截图会把后面的窗口也截进来；用**像素分析**定位到，改成按客户区取图 |
| 7 | 设置对话框底部按钮换行 | `.cp-spacer` 在非 flex 容器里不生效 |

### 为什么截图不用"模拟点击"

早期版本用真实鼠标注入（`SetCursorPos` + `mouse_event`）去点开对话框再截图。
**一旦被测窗口失去前台焦点，这些点击会落到用户当时正在用的别的程序上** —— 实测确实发生过。

现在改成 `-open <面板名>` 启动参数（`desktop/main.go` 的 `parseBootFlag("open")`），
让界面**主动**打开某个面板，纯只读、零副作用。`cmd/uishot` 与 `scripts/ui_shot.ps1`
的头部注释里都写了这段缘由，避免以后有人"顺手加回来"。

### 怎么验证的

```bash
cd desktop/frontend && npm run build     # vite build，约 4 秒
wails build -tags with_utls,with_quic -platform windows/amd64   # 约 7-8 秒
```

- 产物 `desktop/build/bin/chainproxy-cli.exe` 被 `scripts/build.ps1` 复制为 `dist\chainproxy-cli.exe`；
- **Vite 每次构建会清空 `dist`**，所以 `desktop/frontend/dist/.keep` 占位文件要在构建后补回来
  —— 否则下次 `go build ./...` / `go test ./...` 会因为 `//go:embed` 找不到文件而失败。
  这一步已经写进 `build.ps1`；
- 截图验证：`.\scripts\ui_shot.ps1`、`.\scripts\ui_shot.ps1 -Open settings`。

### 遗留

- **没有托盘图标**（Wails v2 不做托盘，本项目也不为此引入 cgo 依赖）；
- **无障碍未适配**：没有做完整的键盘导航与读屏支持；
- 界面缩放只验证了 100% 与系统推荐的缩放，**没有在 125%/150% 多屏组合下逐项核对**。

---

## 013

**commit**: `chore(scripts): 构建脚本带上 sing-box 构建标签，新增界面截图与端到端脚本`
**日期**: 2026-10-11
**范围**: `scripts/*`

### 改了什么

| 文件 | 说明 |
|---|---|
| `scripts/build.ps1` | +95 行：固化 `-tags "with_utls,with_quic"`，加 `-Cli` / `-Gui` / `-SkipFrontend` / `-Version` 开关，产物统一到 `dist\` |
| `scripts/lint.ps1` | 改 98 行：从 3 步变 4 步，新增"引擎自检" |
| `scripts/test_e2e.ps1` | 改 217 行：跟上 v2 的配置模型与命令行 |
| `scripts/ui_shot.ps1` | **新增**（77 行）：界面截图 |
| `scripts/env.ps1` | 微调 1 行 |

### 为什么这么改

**1）构建标签写死在脚本里。** 漏带标签的后果是"编译成功、运行时才炸"，
而且报错信息（`uTLS, which is required by reality is not included in this build`）
很难联想到是编译参数。写死在 `build.ps1` 里，人就不需要记住它。

**2）`lint.ps1` 加"引擎自检"这一步。** 现在是四件事：

```
1. gofmt：所有 Go 代码必须格式化过（cmd / internal / desktop）
2. go vet -tags "with_utls,with_quic" -unsafeptr=false ./...
3. 配置校验：go run ./cmd/chainproxy validate
4. 引擎自检：go run ./cmd/chainproxy engines    ← 新增
```

关于 `-unsafeptr=false`：`cmd/uishot` 需要用 syscall 把 Windows 传进来的 `uintptr`
还原成结构体指针（`GetWindowRect` / `GetClientRect` 这类 API 直接填结构体），
这是纯 syscall 直调 Win32 的固有写法，vet 的 unsafeptr 检查**必然误报**，
所以关掉它 —— 但**只关这一项**，其它 vet 检查一律保留。

**3）`ui_shot.ps1` 的意义**：桌面程序的排版必须真的看一眼。
它构建（可用 `-NoBuild` 跳过）、按 `-Open` 打开指定面板、等 `-WaitSeconds` 秒后截图，
结果默认放 `.cache\shots\`。

### 怎么验证的

```powershell
.\scripts\lint.ps1                  # 4 步全过
.\scripts\build.ps1                 # 两个产物都出来
.\scripts\ui_shot.ps1 -NoBuild      # 截图
.\scripts\test_e2e.ps1              # 端到端
```

### 遗留

- `lint.ps1` 只对 `cmd` / `internal` / `desktop` 做 gofmt，脚本本身（`.ps1`）没有格式检查；
- Windows PowerShell 5.1 与 PowerShell 7 行为有差异，脚本按 5.1 兼容写，
  但只在本地这一种环境验证过。

---

## 014

**commit**: `docs: 更新项目介绍、架构、使用说明、开发进度与提交说明`
**日期**: 2026-10-11
**范围**: `README.md`、`docs/ARCHITECTURE.md`、`docs/USER_GUIDE.md`、`docs/PROJECT_PROGRESS.md`、`docs/COMMIT_HISTORY.md`

### 改了什么

- `README.md` —— 全面改写为 v2：8 种协议一览表、界面说明（默认 8 列 / 列显示 / 通知 / 设置 4 页）、
  产物说明、脚本用法；
- `docs/ARCHITECTURE.md` —— 重写为 v2 架构：模块职责与拆分理由、依赖方向、
  注册模式、关键技术结论、端口管理、状态机、界面层、数据与配置、已知限制。
  **明确写明 v1（纯 Win32）的架构说明已过时，对应代码在 `v0.1-win32-gui` 标签处**；
- `docs/USER_GUIDE.md` —— 使用说明跟上新界面；
- `docs/PROJECT_PROGRESS.md` —— **追加** v2 专节（目标与最终形态、技术选型的为什么、
  关键技术验证结论、实测数据、截图验证发现的缺陷、已知限制、checklist）；
- `docs/COMMIT_HISTORY.md` —— 索引补 007-014，并追加 007-014 的详细说明（即本节）。

### 为什么这么改

这一轮改了配置模型、出口实现、界面框架与命令行入口，**旧文档里几乎没有一句话还对**。
文档比代码更容易腐烂，所以宁可一次改到位，也不留下"看起来还有效"的旧描述。

### 怎么验证的

- `ARCHITECTURE.md` 里的每一条技术结论都能在代码里指到出处
  （`include.Context` → `internal/engine/singbox.go` 的 `Start`；
  `transport.type` → `applyTransport`；端口三条规则 → `internal/ports`）；
- `PROJECT_PROGRESS.md` 的测试数字来自实际执行
  `go test -tags "with_utls,with_quic" -v ./internal/share/... ./internal/qrscan/...`
  的 PASS 行统计（share 84、qrscan 34）；
- 提交索引里的每一行都对应 `git log --oneline` 里真实存在的 commit。

### 遗留

- 旧文档里**没有删除** v1 的历史记录（本次采用"追加 + 明确分区"的方式），
  所以 `PROJECT_PROGRESS.md` 里"最后更新：2026-10-10"与页尾的 v2 专节（2026-10-11）
  在时间上不一致 —— 这是刻意保留历史的结果，不是笔误。

---

## 006a-u

**这一节是一次补齐，不是新提交。**

### 为什么会有这个缺口

`COMMIT_HISTORY.md` 原本的索引在 006（`50a8fcf`，2026-10-10）之后直接跳到了
007（2026-10-11 的 v2 重构）。中间其实还有 **21 个真实提交**，它们记录的是
v1 阶段的收尾工作，但当时没有写进索引 —— 也就是说：
文档与 `git log` 之间有 21 条"有提交、没说明"的空白。

本次把索引补齐（编号 006a–006u，刻意不复用 007 之后的号，
免得动到已经写好的 007–014 及其锚点），并在下面给出合并说明。

**为什么不逐条展开**：这 21 条是 v1 收尾期的密集修复，彼此高度相关
（同一个问题往往跨好几个提交才收敛），逐条展开会得到 21 段内容相似、
重复度很高的文字。这里按主题合并，同时保留每条的真实 hash 与提交信息，
需要细节时用 `git show <hash>` 直接看代码。

### 主题一：WireGuard 从"能跑"做到"可靠"（9 条）

`1a2fe40` `94f74f7` `3eaf3ea` `deed598` `d5b4f16` `9769d09` `dd7242d` `61de5fd`（脚本）`5d1ed05`（清理）

这一段是 v1 里最长的一条战线，几个关键结论：

- **`1a2fe40`**：推翻了自己先前的错误结论 —— "前置 → WG 不通"其实是
  **测试脚本自己漏了 `PersistentKeepalive`**，导致 `tx_bytes=0`、设备根本不发握手包。
  这是整个 v1 最值得记的一课：*现象指向网络，根因在测试*。
- **`deed598`**：`bind.Close()` 无法唤醒阻塞在收包上的协程，
  `device.Close()` 因此永远等不到它退出 —— 表现是"点停止卡死"。
  修法是给 `ReceiveFromUDP` 加 `quit` 通道。
- **`9769d09`**：同类死锁的另一处（UAPI 的 `ipcMutex` 被长时间持有）——
  最后改成把 `IpcGet` 放进 goroutine 并加超时。
- **`d5b4f16`**：去掉收包时的源地址过滤。它本意是安全加固，实际是个
  **静默丢包隐患**（服务端换 IP、NAT 重写都会让它误判）。
- **`3eaf3ea` / `94f74f7` / `61de5fd`**：全隧道 AllowedIPs 的校验、
  前置 UDP 能力预检脚本、私钥完整性校验，以及 PowerShell 脚本改成带
  UTF-8 BOM（否则 Windows PowerShell 5.1 按 ANSI 读会把中文读成乱码）。
- **`dd7242d`**：内置 WireGuard 密钥生成与公钥实时显示 ——
  用户不该为了生成一个密钥去装 `wireguard-tools`。
- **`5d1ed05`**：清除代码里残留的**真实** WireGuard 密钥并重置本地配置。
  这是个必须记住的教训：密钥曾经被写进过被 git 跟踪的文件。

### 主题二：Win32 界面的一批根因修复（3 条）

`574535f` `424109d` `4934bbf`，另有 `65d7fff`

- **`574535f`（最重要）**：`editUpstreamDialog` 的返回值只取了 `bool`、
  把填好的配置丢掉了 —— **对话框里填的参数从来没被保存过**。
  这就是当时"配置存不下来""端口 0 非法"的根因。
- **`4934bbf`**：对话框窗口类被重复注册，导致第二次打开起的对话框
  全部路由到已销毁的状态，"生成密钥 / 复制公钥"点了没反应。
- **`65d7fff`**：`if notif != 0 { return }` 一刀切把 `CBN_SELCHANGE` 也吞了，
  于是切换类型后输入框不跟随显隐。
- **`424109d`**：加宽密钥输入框并给每个密钥加一键复制（纯体验修复）。

这三条的共同点：**都是"看起来能用、实际悄悄失效"的问题**，
只有真的手工操作一遍才会发现 —— 这也是后来 v2 坚持给界面做截图自测的原因。

### 主题三：在真实服务器上部署与排障（5 条）

`47ae714` `fe20bdf` `6e7eec5` `3757600` `d9802b3`

- 先在可控的云服务器上搭了一个 WireGuard 服务端，用来把"客户端问题"
  和"服务端问题"分开（`47ae714`），并做了端到端验收（`fe20bdf`）。
- 然后在另一台 NAT 服务器上定位了一次真实故障：
  **面板上写着 TCP/UDP，但 UDP 实际不生效**（`3757600` 先更正结论，
  `6e7eec5` 才定位到根因是**端口映射的协议类型选成了 TCP**）。
- `d9802b3` 把 VLESS+Reality+xHTTP 的完整部署过程与验证结果留成了文档，
  也就是现在的 `docs/DEPLOY_reality_xhttp.md`。
  选 xHTTP 是因为它只要 TCP，不需要域名和证书，抗主动探测也更强。

### 主题四：Reality 双出口（2 条）

`da1f990` `baa8609`

给那台服务器加了 WARP 出口，实现 `direct + WARP` 双出口，
随后又把手写的 WARP 入站从 WireGuard 的端口上拆出来
（`51822`/公网 `30023`）—— 端口耦合会让两个功能互相牵制。

### 主题五：工具链（1 条）

`529c6e9`

允许 Go 自动切换工具链（本机 1.24.0，而 Wails 与 sing-box 都要求 ≥1.25），
同时靠 `GOMODCACHE` 已被重定向这一点，把自动下载的工具链也**约束在项目目录内**。
这条是 v2 重构的前置条件。

### 另外两个已知问题（本次没有处理）

1. **这不影响索引的完整性，但值得记一笔**：`scripts/build.ps1` 早期的
   `-Console` 开关是空操作（if/else 两个分支的 `go build` 命令行完全相同）。
   v2 重写该脚本时已经删掉这个参数，并顺手修掉了一个更严重的问题 ——
   **`dist\ChainProxy.exe` 与 `dist\chainproxy.exe` 在 NTFS 上是同一个文件**
   （文件名不区分大小写），图形界面那一步会把命令行版覆盖掉，
   表现是"想用命令行却弹出界面、且控制台里既不输出也不退出"。
   现在命令行版改名为 `dist\chainproxy-cli.exe`。
2. `docs/PROJECT_PROGRESS.md` 前半部分（v1 段落）里仍有已过时的表述，
   已在文件顶部加了「阅读提示」说明哪些说法只对当时成立，内容本身按
   "保留历史"的原则未作删改。

---

## 015

**`fix(desktop): 通知重叠 / WebView2 数据目录越界 / 端口框宽度 + 补充运行时验证`**

### 背景

第一轮结束时，有 4 项需求只有"代码写了"、没有运行时证据：
导入后的"仅端口"小窗、搜索/状态/列显示、激活时的端口占用提醒、
以及 5 种通知。这一轮逐条补验，并把过程中发现的 3 个问题修掉。

### 修掉的 3 个问题

**1. 通知重叠（功能性缺陷）**

原先用 Element Plus 的 `ElNotification`，并给每条都传固定的 `offset: 64`
（为的是把它固定在控制栏下方）。结果：**同一个 tick 里连发多条时，
它们互相看不到对方**，于是拿到同一个 offset 直接叠在一起 ——
实测一次弹 5 条只看得见最后一条。

这不是"微调参数"能解决的：`offset` 既是"基准位置"又是"堆叠基准"，
两件事耦合在一起。改成自己实现：

- `src/notify.js`：一个 reactive 数组 + `notify(level, title, message, duration)`；
  超过 6 条自动挤掉最旧的；`duration` 传 0 表示不自动关闭（自测要用）。
- `src/components/Notifications.vue`：fixed 定位在 `top: 60px; right: 14px`，
  flex 列布局天然堆叠，z-index 3200（高于 Element Plus 对话框的 2000 档）。
- 5 种类型各有颜色与图标；`primary` 是 Element Plus 没有的类型，
  这里用品牌色正常实现，不再需要"借 info 的图标"。
- `api.js` 只做转发，`notify(...)` 的签名不变，**调用方一行都没改**。

**2. WebView2 数据目录越界（违反项目约定）**

Wails 默认把 WebView2 的浏览器配置目录放在 `%APPDATA%\<程序名>`，
那是项目目录之外。本项目从第一天起的规矩就是"所有缓存/数据都在
F:\chain_proxy 内"（`scripts/env.ps1` 连 Go 的工具链都重定向进来了）。

改用 `windows.Options.WebviewUserDataPath` 指到 `data\webview\`。
验证：运行后出现 `data\webview\EBWebView`，`%APPDATA%` 下不再产生新目录。
顺带好处：清数据只要删 `data\`。

**3. "仅端口"小窗的端口输入框偏窄**

10810 被显示成 "1081"（和当初添加节点对话框那个问题一模一样）。加宽到 178px。

### 界面自测工具的改进：不再抢焦点

`cmd/uishot` 原先截的是"屏幕上窗口所在的那块区域"，有两个毛病：

1. 窗口被别的程序挡住时，截到的是**挡住它的那个程序** ——
   实测截出来的是用户当时正在看的浏览器页面；
2. 为了让窗口露出来就得 `SetForegroundWindow` 抢前台焦点，会打断正在用电脑的人。

改成 `PrintWindow(hwnd, memDC, PW_RENDERFULLCONTENT)`：让窗口自己把画面渲染进
我们给的 DC，不依赖可见性、也不抢焦点。新增的 `qrscan.CaptureWindow`
与 `qrscan.LooksBlank`（判断是不是一片同色，防硬件叠加层返回空白）配套使用，
返回空白时自动退回"截屏幕区域"的老办法。改造后即使浏览器在前台，
截出来的仍然是 ChainProxy 自己的界面。

### 补上的运行时验证

| 需求 | 验证方式 | 结果 |
|---|---|---|
| (4) 仅端口小窗 | `-open portonly` 截图 | 一行节点摘要 + 端口输入 + 随机按钮；顺带看到红字「端口 10810 已被「住宅代理1」占用」 |
| (5) 搜索过滤 | `-open columns`，搜索框填 "2" | 表格只剩「住宅代理2」，计数 **共 1 个（总 2）** |
| (5) 列显示 | 同上 | 变成自定义 7 列，按钮显示 `7/19`（19 是全部可选列数） |
| (5) 状态筛选 | 同上 | 选择器为「激活状态」 |
| (7) 被其它程序占用 | 用 TCP 监听占住 10817 再激活 | 拒绝并报 **"端口 10817 已被本机其它程序占用"**，节点状态标为 `[错误]` |
| (7) 与项目内节点冲突 | 导入一个用 10810 的节点 | 配置校验拦下：**"本地端口 10810 已被「住宅代理1」占用，不能重复"** |
| (8) 通知 | `-open notify` 一次弹 5 条 | 五种颜色/图标齐全、纵向正确堆叠、位于控制栏下方且盖住表格 |
| (10) 只监听回环 | 设置页与列表截图 | 默认 127.0.0.1 |
| (11) 不往项目外写文件 | 检查 `data\` | WebView2 配置目录已落在 `data\webview\EBWebView` |

### 仍然没有做的（诚实记录）

- **屏幕圈选二维码没有人工拖拽实测**。圈选窗口有 6 个真实驱动窗口消息的测试，
  二维码链路有"节点→链接→二维码→识别→节点"的五协议全链路测试，
  但"用鼠标在屏幕上拖一个真实二维码"这一步没做 —— 需要动用户的鼠标，
  而输入注入已经出于安全考虑彻底删除。
- 没有真实 Reality / Hysteria2 服务器可测。
- 界面仍然**没有自动化排版断言**（v1 的 `test_ui.ps1` 随旧界面删除），
  现在靠截图人眼看 + 像素分析。

---

## 016

**`feat(desktop): 列表改通栏 + 表头可拖拽调列宽（带最小宽度保护）`**

### 1. 列表通栏

表格外面原先套了一层卡片（`margin: 10px 14px 12px` + 边框 + 圆角）。
改成 `margin: 0`、去掉边框与圆角，表格直接抵到窗口左右两边；
第一列单元格左内边距调到 14px 与筛选栏对齐。
上下分隔交给筛选栏的 `border-bottom` 和表头底色，不再需要卡片边框。

### 2. 表头拖拽调列宽

需求："任意拖拽列宽度，但最小也得保留一个字，不要完全重叠"。

翻 Element Plus 源码踩到三个点：

  - **拖拽手柄要求 `border`**：`table-header/event-helper.mjs` 的
    `handleMouseMove` 开头就是 `if (!props.border) return`，
    `handleMouseDown` 里也有一处 `props.border` 判断。
    没有 `border` 时鼠标移到表头边缘不会变成 `col-resize`、也拖不动。
    所以给表格加 `border`，再用 CSS 去掉它附带的那圈外框（通栏下多余）。
  - **库内下限只有 30px**：同文件里的 `columnRect.left - tableLeft + 30`。
    单元格左右各有 10px 内边距，30px 只剩 10px 文字区，一个汉字（约 13px）
    会被切一半，两个相邻列看起来就像叠在一起。
    处理：在 `header-dragend` 里钳到 `MIN_COL_WIDTH = 56px`（约两个汉字），
    并**直接改 `column.realWidth`** —— 因为 `handleMouseUp` 里的
    `scheduleLayout` 排在下一个 `requestAnimationFrame`，而 `header-dragend`
    在它之前发出，此刻改写会被紧接着的重排采用，不会先按超窄宽度闪一帧。
  - **最后一列默认不可拖**（`allowDragLastColumn` 为假时跳过最后一列），
    显式打开才能做到"任意列都能拖"。

另外：弹性列（地址/备注/前置，只有 `min-width` 没有 `width`）被拖动之后
就不再给 `min-width` —— 否则它还会继续吸收窗口多出来的空间，
表现为"拖了却弹回去"。

列宽会持久化（localStorage），「列显示」面板底部另加「重置列宽」。

### 验证

拖拽本来"只能手动试"，但没必要真的动鼠标：`-open resize` 里派发
**合成鼠标事件**走真实代码路径，再把测出的宽度打进一条常驻通知，从截图读数字：

    别名列：208px → 往左拖 400px 后 56px
            （库内下限 30px，我们钳到 56px；store 记录 = 56）
    最后一列「速度(MB/s)」：106px → 56px（验证 allow-drag-last-column）

56px 下单元格显示为"任..." —— 保住一个完整汉字 + 省略号，没有重叠。

### 文档

ARCHITECTURE.md 在"界面层"一节补了这三条坑；
PROJECT_PROGRESS.md 追加"界面微调（第 3 轮）"。

---

## 017

**`fix(desktop): 最右一列作为填充列，右端永远不留白`**

### 现象与根因

016 做完"任意拖拽列宽"之后，缩短某一列时右端会冒出一条留白。
翻源码定位到根因：

  - `table-layout.mjs` 的 `updateColumnsWidth` 用
    `flattenColumns.filter((column) => !isNumber(column.width))` 找出**弹性列**，
    弹性列负责吸收"容器宽 − 各列宽之和"的差值。
  - `table-header/event-helper.mjs` 的 `handleMouseUp` 在拖拽结束时执行
    `column.width = column.realWidth = 拖出来的数字`。

两件事合起来：**只要用户拖过那个唯一的弹性列，它就变成固定列，
整个表格不再有弹性列，右端立刻空出一条留白**；之后拖任何列都只会更明显。

### 解决

让**最右边那一列**当"填充列"：

  - 不绑 `width`，只绑 `min-width`（= 用户拖出来的值，或它自己的默认宽度）。
    于是它的实际宽度 = `minWidth + 剩余空间`（`table-layout.mjs` 第 109 行：
    `flexColumns.length === 1` 时 `realWidth = minWidth + totalFlexWidth`），
    永远铺满右端。
  - 在 `header-dragend` 里把它的 `column.width` / `column.realWidth`
    清成 `undefined`，它才会在拖完之后重新回到弹性状态。
    紧随其后的重排不用我们手动排 —— `watcher-helper.mjs` 第 24-32 行对
    `min-width` 的 watcher 会调 `store.scheduleLayout`。
  - `el-table-column` 的宽度是以 `prop` 绑定的响应式值，所以切换「列显示」时
    填充列会自动变成当前最右的那一列。

顺带把"地址/备注/前置"从弹性列改回普通固定列 —— 否则会出现两个弹性列，
剩余空间按 min-width 比例分摊，语义反而变复杂、也不好解释。

### 取舍

在空间还有富余时，**把填充列拖窄看不出变化**。这正是需求要的
（"缩短它就让它在剩余空间里自己铺满"），拖它改的是它的*最小宽度*：
只有窗口窄到放不下时这个下限才体现（触发横向滚动，而不是把列挤成一条线）。
「列显示」面板底部的提示里写明了这一点，免得被当成拖拽失效。

### 验证

`-open resize` 依次做三件事，并量出"右侧留白"
（= 表格容器右边缘 − 最后一列表头右边缘）：

    1) 「别名」208px → 往左拖 400px 后 56px（库内下限仅 30px，我们钳到 56px）
    2) 填充列「速度(MB/s)」290px → 拖 60px 后再量 290px（松手后回到"占满剩余"）
    3) 右侧留白：拖中间列后 0px，拖填充列后 0px（都应为 0）
    store 记录：{"name":56,"speed":230}

第 2 条尤其能说明设计：视觉宽度没变，而 store 里记的下限从 116 变成了 230。

### 文档

ARCHITECTURE.md 的"界面层"补上第 4 条（为什么要填充列、以及清 width 的原因）；
PROJECT_PROGRESS.md 追加"填充列：右端永远不留白（第 4 轮）"。

---

## 018

**`test: 端口相关测试不再依赖机器的端口占用状态`**

### 起因：一次差点被误判的假红灯

提交 017 之前跑 `go test ./...`，`internal/ports` 与 `internal/app` 里三个
端口相关测试红了。当时正在启动着 `dist\ChainProxy.exe` 看界面效果 ——
**它正占着 127.0.0.1:10810 与 10811**，而那几个测试写死了这两个端口，
断言"空占用表时 `Next()` 返回 10810"，于是：

    Next() 发现 10810 被真实占用 → 顺延到 10813 → 断言失败

与代码毫无关系的红灯。更糟的是我在红灯状态下把 017 提交了（这是流程错误，
已在这一条里修正）。

### 修法

新增测试辅助函数 `freeBase(t, from, n)`：从 `from` 开始找"连续 n 个都真正
空闲"的起始端口（真的 `net.Listen` 一次再关掉），拿它当基准；
所有断言改成**相对这个基准**，不再写死具体端口号：

  - `TestNextFollowsMaxPlusOne`：基准 base → 空表时返回 base；
    标记 base 后返回 base+1；`MarkProject(8080)` 不参与取最大值（8080 < 10800）；
    只有低端口时回到 base。
  - `TestNextSkipsBusyPort`：用另一个监听占住 base+1，断言 `Next()` 跳到 base+2。
  - `TestAllocateForRegisters`：起点设为 base，断言两次分配是 base 与 base+1。
  - `TestPortAllocationFollowsRules`：把两个节点的端口放在 base 与 base+5，
    并把 `Settings.PortStart` 一起设成 base，断言 `SuggestPort("") == base+6`、
    `SuggestPort("b") == base+1`，占用检测也全部改用 base。

### 验证

    # 不启动程序
    go test ./internal/ports/ ./internal/app/   → ok / ok
    # 故意让界面程序占着 10810 与 10811 再跑
    go test ./internal/ports/ ./internal/app/   → ok / ok
    # 程序仍在运行时跑全量
    go test ./...                                → 8 个包全 ok

### 教训

测试里凡是**依赖宿主机真实资源**（端口、网络、时钟）的断言，都不该写死具体值 ——
否则"开发机上正跑着被测程序"这种再正常不过的情况会让测试变红，
而红灯一旦不可信，它作为质量门禁的价值就没了。

---

## 019

**`feat(desktop): 对话框可拖动不裁切 + 长表单内部滚动 + 按钮做大居中 + 取消改警告色`**

四条使用反馈。

### 1. 添加节点弹窗去掉顶部那两个导入入口

「从剪贴板导入分享链接 / 从屏幕扫描二维码」在对话框顶部与顶栏「导入」菜单重复。
删掉区块、两个处理函数、图标导入与那段 `cp-import-bar` 样式；
入口统一留在顶栏「导入」菜单（它还有第三个"手动粘贴链接…"）。

### 2. 可拖动"飘开"，且不被边界裁切

`<el-dialog draggable>` 即可，因为它的**默认行为就是"可拖但不越界"**：

    hooks/use-draggable/index.mjs
    if (!overflow?.value) {
      moveX = clamp(moveX, minLeft, maxLeft)
      moveY = clamp(moveY, minTop, maxTop)
    }

`overflow` 默认值为假（use-dialog.mjs: `props.overflow ?? globalConfig ?? false`）。
它的文档注释写 "draggable Dialog can overflow the viewport" 指的是显式打开
`overflow` 的情形 —— 差点被这句话误导成"要自己写钳制"。

**真正的独立窗口做不到**：Wails v2 没有多窗口 API（`options.App` 只描述唯一
那个窗口，`pkg/runtime` 里全是这一个窗口的 setter）。要做到得换 Wails v3 或用
COM 自己托管第二个 WebView2，代价过大。交付的是"窗口内自由拖动 + 不裁切"。

### 3. 长表单底部按钮不再被窗口裁掉

"被边界裁切"的真实主因：VLESS + Reality 的表单比窗口还高，底部按钮被窗口
下边缘切掉。给对话框加高度上限、让 body 内部滚动：

    .el-dialog.cp-dialog { display:flex; flex-direction:column;
                           max-height: calc(96vh - 32px); margin-bottom: 24px; }
    .cp-dialog .el-dialog__body { flex:1 1 auto; min-height:0; overflow-y:auto; }

`min-height: 0` 不能少 —— 默认的 `min-height: auto` 会让 flex 子项被内容撑破，
内部滚动失效。

**踩了一个 CSS 选择器的坑**：`el-dialog` 的 `class="cp-dialog"` 是**透传到
`.el-dialog` 自身**的（不是外层 overlay），所以给对话框本体加样式必须写
`.el-dialog.cp-dialog`。我一开始写成 `.cp-dialog .el-dialog`（要求祖先-后代），
根本匹配不到 → 高度上限没生效、底部按钮照样被裁。
是 `-open dialogdrag` 那条自测通知先报"找不到对话框"才暴露出来。

### 4. 关闭图标与底部按钮做大 + 居中 + 取消改警告色

  - 右上角关闭按钮：命中区域 40x40、图标字号 14px → 20px
  - 底部按钮：高度 24px（全局 size=small）→ 36px，最小宽度 112px
  - 底部按钮居中（`justify-content:center`），两个按钮间距仍为 8px
  - 「取消」统一为 `type="warning"` 警告色
  - 设置面板那排是"恢复默认设置 + flex:1 占位 + 取消 + 保存"，
    居中规则配合占位元素后仍靠右，不受影响

### 验证

`-open dialogdrag` 打开最长的表单（VLESS + Reality），用**合成鼠标事件**走一遍
真实拖拽（`mousedown` 在 `.el-dialog__header`，`mousemove/mouseup` 在 `document`），
把量到的坐标打进常驻通知：

    硬拖 +1200,+900 后：left=404 top=60 right=1164 bottom=701（视口 1164x701）
    → 整框仍在窗口内 ✓ 不会被裁切
    再拖 -300,-30 后：left=104 top=30（已飘离居中位置）
    底部按钮区 bottom=655（视口高 701）→ 完整可见 ✓

硬拖到边界时正好停在 `right=1164 / bottom=701`（等于视口尺寸），
说明钳制是精确的。

### 文档

ARCHITECTURE.md 的"界面层"补三条（drag 的钳制依据、`cp-dialog` 选择器的坑、
长表单的高度上限与 `min-height:0`，以及 Wails v2 单窗口的限制）；
PROJECT_PROGRESS.md 追加"对话框体验调整（第 5 轮）"。

---

## 020

**`docs(migration): Wails v3 迁移技术验证（试点实测通过，GO）`**

新增 `docs/MIGRATION_wails_v3.md`。这一条不含任何产品代码改动，只是把**实测结论**固化下来，
让迁移可以照着做、也便于回退。

### 背景

需求是"对话框能飘到主窗口之外、压在主窗口上面"。这需要**第二个真正的 OS 窗口**，
而 Wails v2 没有任何创建窗口的 API（`pkg/application` 只有
`New/NewWithOptions/Run/Quit/Bind/On/SetApplicationMenu`，全模块搜 `NewWebviewWindow` 无结果）。
用户选择升级到 v3，顺带获得托盘常驻、全局快捷键、系统通知。

### 做了什么（验证，不是猜）

在 `.cache\spike-wails3\spike3`（gitignore 内）用 `wails3 init -t vanilla-js` 生成试点，
写了一个含"主窗口 + 无边框置顶浮动窗口 + 托盘 + 全局快捷键 + 系统通知"的 main.go，
**用裸 `go build` 构建并真实运行**，实测日志：

    [spike] WebView2 数据目录 = …\spike3\data\webview        ← 在项目内
    [spike] 主窗口已创建 id=1
    [spike] 浮动窗口已创建 id=2（frameless + alwaysOnTop）
    [spike] 托盘已 Run
    [spike] 已注册全局快捷键 Ctrl+Alt+F9
    [spike] JS 调用 Ping("hello-from-main")
    [spike] JS 调用 Ping("hello-from-float")               ← Call.ByName 免绑定生成器
    [spike] 启动满 3 秒，尝试发一条系统通知（无报错）

枚举可见窗口确认是两个真正的 OS 窗口：`420x260 spike3·浮动窗口`、`760x480 spike3·主窗口`。

### 关键结论

1. **构建不需要 wails3 CLI、也不需要 Taskfile**。`wails3 build` 是 Taskfile 驱动的，
   而本机没有 `task`；但官方 Windows 任务里真正的命令就是一条
   `go build -tags production,<EXTRA_TAGS> -trimpath -buildvcs=false -ldflags="-w -s -H windowsgui"`。
   **`production` 标签是必需的**（v3 用它区分 production/dev 实现文件）。
   我们已有的 `with_utls,with_quic` 照旧作为 EXTRA_TAGS 传入。
2. **前端可以完全绕开绑定生成器**：`@wailsio/runtime` 导出 `Call.ByName('包.类型.方法', args)`
   与 `Events.On(name, e => e.data)`。我们现有的 `api.js` 本来就把调用收敛在一处，改一处即可。
3. **数据目录可留在项目内**：`application.Options.Windows.WebviewUserDataPath`
   （内部即 `chromium.DataPath`），不设则默认 `%APPDATA%\<exe名>`，违反本项目约束。
4. 托盘 / 全局快捷键 / 系统通知 / `DisableQuitOnLastWindowClosed`（关窗不退出，托盘常驻的前提）
   的 API 用法均已编译运行通过，具体代码见文档。

### 隔离性与可回退

- v2 最终状态已打 tag **`v2.0-wails2`**；迁移在分支 **`wails-v3`** 上进行。
- `wails3` CLI 装在 **`F:\chain_proxy\.cache\bin\wails3.exe`**；v3 模块与依赖在
  `GOMODCACHE`（项目内）解包。验证过 `%APPDATA%\wails` **不存在** —— 项目外零写入。

### 遗留待办（写进文档了）

- 试点有告警 `Error saving icon: … resource with id 3`（未生成 `.syso` 图标资源），
  正式迁移时处理。
- 运行期切换标题栏明暗在 v3 未找到 setter（v2 有 `WindowSetDarkTheme`），待验证。

---

## 021

**`feat(desktop)!: 桌面外壳从 Wails v2 迁移到 v3（托盘常驻/多窗口/全局快捷键/系统通知）`**

> 带 `!`：这是架构级的替换，不是增量功能。v2 的最终状态已打 tag
> **`v2.0-wails2`**，本次在分支 **`wails-v3`** 上进行，随时可回退。

### 为什么

需求是"对话框能飘到主窗口之外"，这需要**第二个真正的 OS 窗口**。
Wails v2 没有任何创建窗口的 API（`pkg/application` 只有
`New/NewWithOptions/Run/Quit/Bind/On/SetApplicationMenu`），而 v3 把窗口做成了一等对象。
经与用户对比 v2/v3 后决定升级，顺带拿到托盘常驻、全局快捷键、系统通知。
**成熟度是这次决策里唯一的减分项**：v3 当时最新是 `v3.0.0-beta.28`（发布 6 天），
所以版本被**锁死**在 beta.28，不跟 `latest`。

### 改动

| 文件 | 改动 |
|---|---|
| `desktop/main.go` | 全部重写：`application.New` + 主窗口 + 托盘 + 全局快捷键 + 通知服务 + `DisableQuitOnLastWindowClosed` + 关窗钩子 |
| `desktop/shell.go` | 新增：v3 胶水层（事件/剪贴板/对话框/窗口/通知），以及托盘与快捷键的动作实现 |
| `desktop/theme_windows.go` · `theme_other.go` | 新增：**v3 没有运行期改标题栏明暗的 setter**，直接调 `DwmSetAttribute`（只匹配本进程窗口） |
| `desktop/api.go` | `startup/shutdown(ctx)` → `ServiceStartup/ServiceShutdown`；去 `ctx` 字段；`runtime.*` → v3 对象方法；新增外壳状态上报 |
| `desktop/api_import.go` | 剪贴板 / 隐藏恢复窗口 / 保存文件对话框改用 v3 |
| `desktop/frontend/src/api.js` | 桥换成 `@wailsio/runtime`（`Call.ByName` + `Events.On`）——**只改了这一个文件** |
| `desktop/frontend/package.json` | 加 `@wailsio/runtime`，锁 `3.0.0-beta.28` |
| `desktop/frontend/wailsjs/` | 删除（v2 自动生成的绑定，v3 不需要） |
| `desktop/wails.json` | 删除，换成 v3 的 `desktop/build/config.yml` |
| `scripts/build.ps1` | `wails build` → `go build -tags production,...` |
| `cmd/uishot` | 新增 `-rect x,y,w,h`：截屏幕区域（拍托盘这种不属于任何窗口的东西） |

### 三个改变了工作量估计的结论

1. **构建不需要 wails3 CLI，也不需要 Taskfile。** `wails3 build` 是 Taskfile 驱动的
   （本机没有 `task`），而官方 Windows 任务里真正的命令就是一条 `go build`，
   外加 **`production` 标签**（v3 用它区分 production/dev 两套实现文件）。
   所以构建脚本改成直接调 `go build`，命令行版走的还是同一条路。
2. **前端可完全绕开绑定生成器。** `@wailsio/runtime` 的 `Call.ByName('包.类型.方法', args)`
   按名字调用服务；因为所有调用早就收敛在 `api.js` 的一个 `call()` 里，换桥只改一处。
   事件也一样，`Events.On` 的回调收到 `{name, data, sender}`，在 `onEvent` 里统一解包，
   下游 handler 签名不变。
   判定"是否在 Wails 里"必须用宿主通道 `window.chrome.webview.postMessage`，
   **不能用 `window._wails`** —— 后者被 runtime 包在导入时就赋成了 `{}`，永远为真，
   那样"后端未连接"的提示就永远不会出现。
3. **数据目录仍能留在项目内**：`application.Options.Windows.WebviewUserDataPath`。

### 验收（每条都有程序外的证据）

| 项 | 证据 |
|---|---|
| 界面在 v3 下正常 | 截图：节点 2/2、今日流量、通栏表格与填充列都正常 |
| WebView2 数据目录 | `data\webview\EBWebView` 存在；`%APPDATA%\ChainProxy`、`\chainproxy`、`\wails` **都不存在** |
| 托盘已建立 | 日志 `外壳：托盘已就绪（图标 21017 字节）` |
| 全局快捷键 | 日志 `外壳：已注册全局快捷键 Ctrl+Alt+C（…）、Ctrl+Alt+A（…）` |
| **关窗不退出** | 关窗前：窗口可见（hwnd 1411250）、`MainWindowHandle=21041744`；关窗后：**可见窗口列表里已无它**、`MainWindowHandle=0`、**进程仍在**、**10810/10811 仍由同一 pid 监听**；日志 `窗口关闭被拦下：已收进托盘，进程与代理继续运行` |
| **托盘菜单动作** | 自测直接调菜单背后的方法：复制本地地址 → 程序外用 `Get-Clipboard` 读到 `socks5://127.0.0.1:10810`；停止全部 → 0/2；激活全部 → 2/2 |
| 全量校验 | `gofmt`/`go vet` 干净、8 个测试包全过、`lint.ps1` 4/4 |

### 一处必须记下来的可观测性设计

托盘与快捷键是在 `app.Run()` **之前**建立的，那时后端还没加载、界面还没起来，
而 GUI 版用 `-H windowsgui` 构建、**没有控制台** —— 这类信息不记下来就等于凭空消失。
所以 `api` 里存下"托盘/快捷键的建立结果"与"后端就绪前的告警"，
等 `ServiceStartup` 起来后写进 `data\chainproxy.log` 并把告警补投成界面通知。
上面那两条日志就是这么来的。

### 诚实说明

- **托盘图标本身没截到图**：Windows 11 把新注册的托盘图标放进 `^` 折叠区，
  拖出来需要点鼠标，而本项目已彻底禁用输入注入。"托盘已建立"的证据是日志
  与"关窗常驻"行为成立（两者共用同一段外壳代码）。
- **收进托盘时的 Windows 通知**只验证到"调用成功、无报错"，没抓到 toast 截图。

### 还没做

对话框仍是主窗口内的 `<el-dialog>`。v3 已经把"独立窗口"这条路打开了，
但改成独立窗口要再引入"第二个前端入口 + 窗口生命周期 + 结果回传"，是独立的一步。

### 文档

README（产物与构建说明，顺带修掉"图形界面写成 chainproxy-cli.exe"的笔误）、
ARCHITECTURE 第 7 节重写为 v3 外壳 + 已知限制更新（改成"托盘图标默认在折叠区"）、
USER_GUIDE 新增"托盘常驻/全局快捷键"（并修掉同一处笔误）、
PROJECT_PROGRESS 追加迁移实录、`MIGRATION_wails_v3.md` 更新为已完成。

---

## 022

**`feat(desktop): 添加/编辑节点改为独立置顶浮动窗口（可拖到主窗口之外）`**

### 这是什么

用户要的效果：对话框**能跑到主窗口之外、永远压在主窗口上面、鼠标拖着满屏跑**。
DOM 浮层做不到 —— 元素只能画在所属窗口的客户区里，到边界就被裁掉。
所以「添加 / 编辑节点」现在开在**独立的、无边框、置顶的 OS 窗口**里。

### 怎么实现的（关键：复用同一份前端）

不做第二个前端入口，浮动窗口加载的还是同一个 SPA，只是带 `?float=node`：

    Go 侧 desktop/float.go
      OpenFloatDialog(kind, payload, devOpen)
        → 建无边框 + AlwaysOnTop 窗口，URL = /?float=<kind>
        → 阻塞在 <-result 上，前端一个 await 就能拿到用户在浮动窗口里的操作结果

    前端
      main.js      见到 ?float= 就加 body.cp-float-window，并把 kind 传给 App
      App.vue      enterFloatMode()：打开那个对话框 + 接上 ui.onNodeDialogClosed
      styles.css   藏掉主界面、让对话框铺满窗口、标题栏设 --wails-draggable: drag

于是对话框的校验、端口检测、保存逻辑**一行都不用重写**；
代价是"关掉时把结果回传"得显式接上（就是那个 onNodeDialogClosed）。

会话一定被结束（等待方不会永久挂着），因为三条路都会走到 finishFloat：
提交、取消、以及用户直接关窗（WindowClosing 钩子按取消处理 ——
**必须先 e.Cancel()**，否则窗口被销毁、等待方就永远醒不来了）。

### 三个踩过的坑（都留了注释与文档）

1. **别从主窗口的前端查另一个窗口的几何信息。**
   v3 的 Window.Get(name) 是相对**当前窗口**的，从主窗口调它会拿到主窗口自己的
   位置与尺寸。我第一次的"越界测试"就是被它骗了，报出 1180x740（主窗口尺寸）——
   如果我当时信了这个数，就会得出"浮动窗口尺寸不对"的错误结论。
   窗口名册在 Go 侧，所以 FloatWindowBounds / MainWindowBounds / MoveFloatWindow
   都由 Go 回答。
2. **JS 的 switch 里重复 case 名只有第一个会命中**，后一个是死代码、而且**不报错**。
   我给"主窗口发起测试"和"浮动窗口自行结束"两处都起了 floatsubmit，
   结果主窗口那段永远不执行，表现是"跑完什么也没发生、也没有报错"。
3. **自测用例出错必须看得见。** onMounted 里 await 一个失败的自测会静默 reject，
   表现成"界面起来了但什么也没发生"。给自测套了 try/catch，把错误弹成通知。

### 验收

    独立窗口（进程外枚举）
      820x680  @(1000,300)  ChainProxy · 添加 / 编辑节点     ← 浮动窗口
     1180x740  @(370,146)   ChainProxy · 链式代理            ← 主窗口

    越界（主窗口右 1550 / 下 886；浮动窗口右 1820 / 下 980）
      右边缘超出主窗口 = true；下边缘超出主窗口 = true
      整框仍在屏幕内 = true（1920x1080）
      → 没被主窗口裁剪，因为它是独立 OS 窗口，不是浮层
      （同一组数字在主窗口的通知里也打印了一份，两处互相印证）

    结果回传（-open floatpassthrough）
      主窗口 await 收到的结果 = {"submitted":false,"via":"float-self-test"}
      浮动窗口是否已关闭 = true
      → 结果回传 + 自动关窗都正常
      （链路：对话框关闭 → ui.onNodeDialogClosed → SubmitFloatDialog → Go 唤醒等待方）

另外给 `uishot -list` 加了窗口位置输出（拿越界证据用的），
并在 `-rect` 之外补了屏幕区域截图能力（上一轮加的，用于拍托盘）。

### 还没做

其余对话框（导入 / 分享 / 设置 / 仅端口小窗）仍是窗口内浮层。
机制是通用的：`OpenFloatDialog` 只认 `floatSizes` 表里的类型，
要推广只需把它们纳入这张表、并在 `enterFloatMode` 里加一个分支。

---

## 023

**`feat(config): 全局快捷键可自定义 + 设置新增「快捷键」页`**

### 起因

用户："设置页面在测试前面增加一个 快捷键 tabs，让用户可以自己在里面设置这两个快捷键的
绑定键，默认就是现在这两组，点击后旁边文字提醒'请点击键盘两个以上组合键进行记录'。"
同时他实测反馈 **`Ctrl+Alt+A` 与微信截图冲突**。

### 配置

`Settings.ShortcutToggleWindow` / `Settings.ShortcutToggleAll`，约定：

  - **空字符串 = 用默认值**（老配置自动补默认值，实测会写回 config.json）
  - **显式写 `"none"` = 关闭这一条**，不再占用任何组合键

不拿空字符串表示"关闭"，否则"没设过"和"主动关掉"就没法区分了。

默认值：`Ctrl+Alt+C` 与 **`Ctrl+Alt+Shift+A`**。后者换掉 `Ctrl+Alt+A` 正是为了避开
微信/QQ 的截图。顺带实测确认：v3 的 `GlobalShortcut.Register` 认三键组合。

### 后端

- `API.ApplyShortcuts()`：按当前设置**先全部注销再注册**，逐条返回
  `ShortcutStatus{Key,Label,Accel,OK,Message}`。
  先注销是必须的 —— 改绑定时旧键要让出来，否则会同时占着两个组合键。
- `SaveSettings` 保存后**自动**调用它，所以"保存了但没生效"不可能发生；
  有失败的条目会弹一次界面提示，并把每一条写进日志。
- 注册失败不返回 error，而是逐条报告：一条冲突不该让另一条也失效。
- `API.ShortcutStatuses()` 只查状态、不重注册 —— 免得每次打开设置面板
  都让快捷键短暂失效。

### 界面

设置新增「快捷键」页（排在"运行与界面"之后、"关于"之前）：两行录制框 +
「清除」+ 每行说明 + 冲突提示。点输入框开始录制，按下组合键即记录；
**只按一个键会被拒绝**，并提示"请点击键盘两个以上组合键进行记录"。

### 验证

`-open keyrecord` 合成按键事件走真实录制逻辑：

    初始显示：Ctrl+Alt+C
    ① 只按 K → 仍是「请点击键盘两个以上组合键进行记录…」
    ② 按 Ctrl+Shift+K → 「Ctrl+Shift+K」
    ③ 点清除 → 「未启用（点击可重新设置）」

启动日志：

    外壳：已注册全局快捷键 Ctrl+Alt+C（显示 / 隐藏主窗口）、
                           Ctrl+Alt+Shift+A（激活全部节点 / 停止全部）

### 一处小坑

设置面板的页签用 `const tab = ref(ui.settingsInitialTab || 'front')` 初始化**不生效** ——
组件在应用启动时就创建了，那时自测入口还没跑。改成在 `watch(visible)` 里每次打开时设置。
（截图里看到仍然是第一页才发现。）

全量校验：gofmt / go vet 干净、8 个测试包全过。

---

---

## 023a-f

**这一节是一次补齐，不是新提交。**

### 为什么会有这个缺口

索引原本停在 023（`d2721c7`，2026-10-11），下一次登记已经是 024（改名）。中间从
10-12 到 10-13 有 **99 个提交**没有写进索引 —— 也就是说，文档与 `git log` 之间有
99 条"有提交、没说明"的空白。

**先说一个我自己的错误**：第一次报这个缺口时我说的是"90 个提交"，那是错的。
当时用 `--since=2026-10-12` 过滤，把 023 当天（10-11）之后的 10 条漏掉了。
正确数字是 `git log --oneline d2721c7..ce3db4d` 的 99 条，本节的 hash 集合与它
**逐条比对通过**（脚本做的，不是肉眼）。

### 为什么按主题合并，而不是逐条展开

照 [006a-u](#006a-u) 的先例。这 99 条比那 21 条更极端：里面有 21 轮迁移的连续记录
（同一条战线）、24 项审计修复（编号本身就是索引）、底部面板从骨架到七个视图的推进。
逐条展开会得到 99 段高度重复的文字，而 `git show <hash>` 永远比转述准确。
所以下面**保留每一条的 hash**，只对真正有决策价值的写段落。

### 主题1：二维码圈选覆盖层重做（6 条）

`7bc1ce6` `c6da477` `a287981` `41ec04a` `51703e1` `ae44aa3`

两个"卡"的根因都不是绘制慢：`41ec04a` 是"先出半条线、卡一会儿才响应"，
`7bc1ce6` 是"点了要等两秒才出遮罩" —— 1.8 / 2 秒都花在**系统设置的广播**上。
选区错位是结构问题，`ae44aa3` 把覆盖层改成"半透明压暗层 + 窗口区域挖洞"才根治。
最终 `c6da477`：压暗 + 选区透亮（手感对齐微信/QQ），2.4s → 75ms。
`51703e1` 的缓冲区诊断日志与 `a287981` 的 gofmt 是过程性提交。

### 主题2：底部面板：从设计契约到七个视图（13 条）

`14b2623` `4cf6035` `1ba39ad` `a8a3e66` `c6b0952` `f8e48bb` `b45c370` `147605b` `27162ce` `7de219f` `d48a2d5` `4faf14c` `fed15c2`

先写契约（`14b2623` 产出 `BOTTOM_PANEL.md`）再动手，然后按骨架 → 流量统计 →
网络日志 → 网站测试 + 网站×节点矩阵 → 连接 → 网络设置推进。
`27162ce` 是"14 处说明小字瘦身"—— **界面上不留无意义小描述这条规矩就是这一轮定的**。
曲线重做过一次（`7de219f` 节点下拉 + 坐标系 + bezier 平滑，`d48a2d5` 悬浮块），
`4faf14c`（收起/展开文字差 1-2px）与 `fed15c2`（端口按数量自适应）是像素级收尾。

### 主题3：sing-box → mihomo 整体换装（30 条）

`332ebe4` `8ecdb28` `c24fca1` `fbe8210` `3845e68` `78c1df5` `217c635` `38ea535` `fd3a888` `5022955` `0feb59e` `c2520af` `bd7368a` `f385c39` `824c124` `b4d2f41` `3fe9374` `82cb5bc` `18142e5` `ac000c0` `b5ec102` `765776c` `907546e` `1cdadf2` `f92c4f0` `148dcd3` `8fd49d9` `4c04c40` `4e6afe4` `011a472`

21 轮全过程另有 [`MIHOMO_MIGRATION.md`](MIHOMO_MIGRATION.md)，这里只留几条最值得记的：

- **先读全签名 + 探针实测，再引依赖**（`8ecdb28` 探针、`c24fca1` 引依赖，净增量 +7.36 MB）；
- **UDP 那一轮被测否掉并回滚**（`0feb59e`）：设计要件被钉死为"按目标建通道"，
  第 11 轮才打通（`c2520af`）—— 记下来是因为"先否掉自己"比先写代码更省时间；
- **第 13–17 轮建立的"换装开关 + 失败回退"是过程性产物**（`bd7368a`/`f385c39`/`824c124`/
  `b4d2f41`/`3fe9374`）：后来 `1cdadf2` 把 sing-box 整个摘除、`f92c4f0` 把开关也删了。
  中间态的东西不必惋惜，但要知道它们存在过，否则读旧文档会以为项目还有两个内核；
- **前置不能静默退化**（`765776c` 堵缺口、`907546e` 真接前置 A′）：配了前置的节点
  必须经前置拨号，做不到就明确报错，绝不悄悄直连；
- 收尾两组证书问题：`4c04c40`（跨协议漏传 SkipCertVerify / ALPN / vmess uTLS 指纹）、
  `8fd49d9`（hysteria2 的证书 sha256 pin）；
- `b5ec102` 让 xhttp 的报错说人话（不再甩 sing-box 那句英文）。

### 主题4：24 项审计修复（27 条）

`c376194` `cd09719` `4ff3fa4` `2849689` `0186b5a` `a8f09ff` `7f20d1f` `d0f160f` `cc36d0a` `20541f1` `8a51a1b` `eb4b507` `90dbd1c` `46c81f5` `b18dfc1` `10ba11c` `84156fa` `e18adc6` `05e9a8d` `d90a8fc` `25b08ba` `3e7d39b` `ff5a3f1` `728e000` `1f7483e` `79b84b2` `601e618`

用户给的 30 项审计清单（#12 / #24 不适用），每条的提交标题本身就是根因，不再转述。
这批的共同点是**都错在"静默"**：静默直连（#10）、静默丢结果（#14）、静默把上次测出来的
数字写成 0（#19）、静默放宽目录权限（#7）、静默回显分享链接原文（#23）。
三条最值得记的：

- `e18adc6`（#20）测速分母不再含建链 / 首字节 —— 那条口径后来又被 025 用同样的思路检查过；
- `84156fa`（#19）测试失败不再清零，界面从此分"没测过 / 有效 / 上次成功值"三态；
- `d0f160f`（#8）「移除测试无效项」不再按 `latencyMs == 0` 删节点（那会把用真连接
  延迟测通过的节点一起删掉），改成"测过且失败"+ 确认弹窗。

### 主题5：桌面外壳与设置面板（13 条）

`fad295d` `df14def` `1e51bf9` `548daba` `2fd069a` `9ac7b20` `3cff3e0` `21dd7ef` `e3e279c` `a614af1` `05ae633` `c0ba223` `ce3db4d`

- `9ac7b20` 浮动对话框**复用窗口**：打开耗时 312ms → 15ms；
- `548daba` / `2fd069a` / `1e51bf9` 是设置面板的位置与拖动一系列细节（垂直居中、
  第一次拖动跳一下、关闭时空白一闪、每次打开回到默认位置）；
- `21dd7ef` / `e3e279c` 上全局节点与系统代理模式（含它的安全机制与状态区显示）；
- `ce3db4d` 修"底部栏偏好被验证脚手架和「恢复默认」擦掉"—— 根因是**开发期用真实配置
  做截图验证**，教训写进了 [`VERIFY_DISCIPLINE.md`](VERIFY_DISCIPLINE.md)。

### 主题6：脱敏、文档与仓库卫生（10 条）

`45e1718` `88471ee` `b1c4ac5` `54577ba` `2941dd7` `5bcc1fc` `dd17fc6` `4d2c3b5` `de3b657` `041eca6`

- 脱敏两轮（`2941dd7` docs、`5bcc1fc` 代码与脚本）：真实节点信息全部换成占位值；
- `88471ee` 把"提交前检查"做成脚本（`scripts/check.ps1`）——由此有了门禁；
- 两次恢复 `dist/.keep`（`b1c4ac5`/`54577ba`）：Vite 每次构建清空 `dist`，而
  `//go:embed all:frontend/dist` 要求目录非空，所以 `check.ps1` 后来自己补回来；
- 构建与仓库卫生收尾：`dd17fc6`（去掉已死的 `with_utls,with_quic`、修 ui_shot 构建段）、
  `041eca6`（build.ps1 注释里的 EXTRA_TAGS 占位）、`4d2c3b5`（.gitignore 补压缩包规则，
  并把 `data/` 与 `cache.db` 的排除理由写清楚）、`de3b657`（README 重写：测试版声明、
  当前事实、已知不足）、`45e1718`（清理改结构后的死代码）。

### 索引里为什么不给这 99 条各留一行

那个索引表是给人"扫一眼知道这个项目做了什么"的。99 行会把它撑成流水账，反而看不清
阶段。所以索引里只留六行（023a–023f），指向本节。

**这一节是 028 补的**：详见 [028](#028)。

## 024

**commit**: `refactor: 项目改名为 PicoHop（Go module 改为 github.com/murongruolan/picohop）`

**目的**：把开发期的临时名 `ChainProxy` 定下来。新名 **PicoHop** —— 释义
*Next-hop Node in a Pico Container*（微型容器里的下一跳节点），命中 GitHub `in:name`
**0 个**同名仓库，念得出也拼得出，并且保住了"微型自包含"与"下一跳"两层语义。
被否掉的候选（NexFlux / NexHop / Npiko / FluxGate / PicoRelay …）与重名数据
见 [`NAMING_DECISION.md`](NAMING_DECISION.md)。

### 改了什么

三类**大小写敏感**的替换，覆盖全部已跟踪文本文件，共 **423 处 / 102 个文件**：

| 旧 | 新 | 指什么 |
|---|---|---|
| `CHAINPROXY` | `PICOHOP` | 环境变量前缀（`PICOHOP_DATA_DIR`、`PICOHOP_WG_*`、`PICOHOP_UDP_*`、`PICOHOP_ROOT` 等） |
| `ChainProxy` | `PicoHop` | 品牌：窗口标题、托盘菜单与提示、通知、`User-Agent`、`Proxy-Agent`、文档正文 |
| `chainproxy` | `picohop` | 小写标识：Go 包名、二进制名（`PicoHop.exe` / `picohop-cli.exe`）、日志名（`picohop.log`） |

此外：

- `cmd/chainproxy/` → `cmd/picohop/`（含 `main_test.go`），用 `git mv` 保留文件历史；
- Go module 由 `chainproxy` 改为 `github.com/murongruolan/picohop`，**98 处 import** 同步改写
  —— 这一步的好处是：漏改的 import 会在**编译期**直接暴露，不需要靠 grep 兜底；
- `desktop/build/config.yml` 的 `companyName` / `productIdentifier` / `copyright` 改为账号名；
- 仓库级 git 提交身份由占位身份 `chainproxy-dev <chainproxy@local>` 改为
  `murongruolan <89505739+murongruolan@users.noreply.github.com>`（只影响后续提交，
  历史提交的作者不动）；
- 起名用的临时文档 `docs/NAMING_BRIEF.md` 转为永久决策记录 `docs/NAMING_DECISION.md`，原文件删除。

### 刻意**不改**的地方

| 位置 | 处理 | 为什么 |
|---|---|---|
| `docs/COMMIT_HISTORY.md` | 正文保留旧名，只在顶部加改名横幅 | 改了等于伪造历史 |
| `docs/bench/AB-singbox-vs-mihomo.md` | 同上 | 归档实测数据，且**没有用新名字重测** |
| `chain_proxy`（下划线） | 不动 | 它指仓库目录 `F:\chain_proxy` 与 `RESEARCH_chain_proxy.md` 的文件名，改目录时一并处理 |
| `refs/local-refs`、`refs/local-refs` | 不动 | 别人的源码 |

### 验证方式

1. `scripts/check.ps1` 全绿（gofmt / vite 前端构建 / `go vet -unsafeptr=false ./...` /
   `go test ./...`），输出里的包路径已是 `github.com/murongruolan/picohop/...`；
2. `go version -m dist\PicoHop.exe`：`mod github.com/murongruolan/picohop`，
   且模块清单里没有 `sagernet/sing-box`；
3. **隔离数据目录**（`PICOHOP_DATA_DIR` 指向 `.cache\cli-smoke`）跑
   `picohop-cli.exe version` 与 `ls`：自报 `PicoHop 1.0.0 (windows/amd64, go1.25.5)`，
   并在该目录里生成了 `picohop.log` —— 证明**新环境变量与新日志名在运行时真的生效**，
   而不是只改了字符串；
4. 真实 `data\config.json` 的 sha256 改名前与改名后都是 `85F201D4D1C2E823…`，**全程未被触碰**；
5. 产物体积 35.87 MB / 24.47 MB，与改名前（35.86 / 24.46）一致 —— 改名没有引入任何膨胀。

### 已知遗留

- `dist\` 里改名前产出的 `ChainProxy.exe` / `chainproxy-cli.exe` 已删除（构建产物，不进仓库）；
- **注册表里有一条旧名字留下的通知标识**：`HKCU\Software\Classes\AppUserModelId\ChainProxy`
  （Wails 的 `application.Options.Name` 会进 Windows 通知的程序名归属）。已核实存在；
  它只影响通知里的程序名显示，不影响功能，新名字第一次发通知时会注册自己的键。
  删不删由用户决定，本轮**没有动它**；
- **仓库目录仍叫 `F:\chain_proxy`**：改目录名会让当前会话的工作目录、
  `internal/config/config_test.go` 里"仓库根必须叫 `chain_proxy`"的断言、
  以及 21 处 `F:\chain_proxy\…` 绝对路径同时失效，所以刻意单独一步做（须重启程序与会话）。

---

## 025

**commit**: `fix(test): 真连接延迟改为取样多次取最小值（与主流客户端同口径）`

**起因**：同一个节点，主流客户端的「测试真连接延迟」是 100-200 ms，我们的是 700+ ms。
用户怀疑两边测法不一样。查下来确实不一样，而且我们的口径天生吃亏。

### 根因（两边源码对照，主流客户端侧为 refs/local-refs）

| 维度 |主流客户端| 我们（改前） |
|---|---|---|
| 样本 | 循环 2 次、**取成功值里的最小者**（`同类实现/Handler/ConnectionHandler.cs:87-95`） | 单次 |
| 连接 | 一个 `HttpClient` 建在循环外 → **第 2 次复用连接** | `DisableKeepAlives: true` → 每次重新建链 |
| 计时终点 | 读到**响应体结束**（`GetAsync` 默认 `ResponseContentRead`） | **响应头到达**就停（比它更严） |
| 地址 | 默认 `https://www.google.com/generate_204`（`同类实现:177`） | 默认 `http://cp.cloudflare.com/generate_204`，用户配置里是 `https://api.ipify.org?format=json` |

**关键是"连接复用"**：主流客户端的"最小值"天生比单次冷连接小几倍 —— 它第 2 次样本在
已经建好的隧道里只跑一个 HTTP 往返。只测一次的我们和它根本不是一个口径。

### 实测量级（同一节点、同一分钟、隔离数据目录）

| 地址 | 最小 | 中位 | 最大 |
|---|---|---|---|
| `https://www.google.com/generate_204` | 1007 | 1914 | 2870 |
| `https://api.ipify.org?format=json` | 1201 | 1758 | 2564 |
| `http://cp.cloudflare.com/generate_204`（无 TLS） | 676 | 992 | 2283 |

同一地址的抖动就有 3 倍，所以**跨时刻比较两个软件的数字没有意义**；同时也说明
单纯"换成主流客户端的地址"解决不了问题（最小仍是 1007 ms）。

### 改了什么

1. `tester.RealPing` 改为**取样多次、取最小值**并返回全部样本；样本间隔 100 ms
   （与主流客户端的 `100ms 间隔` 对齐，且在计时区间之外）；
2. `newHTTPClient` 的 `DisableKeepAlives` 由 `true` 改为 **`false`** —— 让第 2 次起真的
   复用连接。否则"取最小值"退化成"取冷连接"，改了等于没改；
3. 读响应体上限 4096 → 64 KiB：Go 的 Transport 要求 body 读完才把连接放回池子；
4. 新增设置 `realTestAttempts`（默认 2，范围 1-5）：字段 + 默认值 + 归一化 + 校验
   + 设置界面「测试」页的「取样次数」；
5. 默认测试地址统一为 `config.DefaultRealTestURL`。以前 `Default()` 与 `internal/app`
   的兜底各写一个地址 —— **"设置里显示的默认值"和"空值时真正用的地址"不是同一个东西**，
   这个根因一并消除；
6. 日志打出全部样本与冷连接那次：`真连接延迟取样 1090/210 ms（冷连接 1090 ms，取最小 210 ms）`
   —— 冷连接的值不再丢失，链路慢还是建链慢一眼可分。

### 验证

- 新增 3 个用例：复用连接并取最小、取样次数为 1 时量到的是冷连接、全部样本失败才报错。
  第一个用例**断言的是连接数而不是快慢** —— 本地回环上建一条 TCP 只要几十微秒，
  光比时间分辨不出有没有复用；
- **反向验证**：把 `DisableKeepAlives` 改回 `true`，该用例立刻红
  （`建立了 2 条连接，want 1`），改回即绿。这条防线不是摆设；
- 门禁全绿（gofmt / 前端构建 / go vet / go test）；
- 真实 `data/config.json` 的 sha256 全程未变（`85F201D4…`）：改的是代码，用户配置里
  那个 `realTestUrl` 仍是他自己设的 ipify，要不要换由他在界面上决定。

### 已知遗留

- 取样次数只影响「真连接延迟」，TCP 延迟与测速不受影响；
- 冷连接的数值目前只在日志里，界面上没有单独的列（少一个概念，界面更干净）。

---

## 026

**commit**: `docs: 增加 GPL-3.0 许可证与依赖许可说明`

**为什么现在做**：仓库准备公开。没有许可证的文件默认是"保留所有权利"，而本项目
**静态链接了 GPL-3.0 的 mihomo** —— 在这个状态下分发二进制，既没给使用者授权，
也不符合 GPL 对下游的要求。所以这是发布前的合规前提，不是装点。

### 做了什么

- 新增仓库根 `LICENSE`：**GPLv3 全文**（674 行），逐字节取自 mihomo 模块缓存里的
  `LICENSE`（sha256 `3972DC9744F6499F…`，与来源完全一致）。用同一份文本，避免
  "自己从网上另抄一份"引入文本差异；
- README 末节新增「许可证」：写明 GPL-3.0 与 `Copyright (C) 2026 murongruolan`、
  **为什么只能选它**（内嵌 mihomo 是 copyleft）、以及代价（允许他人 fork、改、
  甚至售卖，只要同样开源）；
- 同节列出其余直接依赖的许可：wails v3 / wireguard / gozxing / go-qrcode 都是 MIT，
  `golang.org/x/*` 是 BSD-3-Clause —— 均宽松，不产生额外义务；
- 同节说明"对应源码哪里拿"：依赖版本钉在 `go.mod` / `go.sum`，按构建一节可重建。

### 有意没做的

- **没给源码加许可证头**：此前没有任何文件头，突然给 116 个 `.go` 文件加版权头会淹没
  diff，而 GPL 不要求逐文件声明（LICENSE + README 已足够）。真要做，应当一次性做完
  并单独成一次提交；
- 没有 `THIRD_PARTY_NOTICES`：直接依赖只有 5 个，README 一张表足够；一百多个间接依赖
  全是宽松许可，列出来是噪音。

### 验证

- `LICENSE` 与来源逐字节一致（sha256 `3972DC9744F6499F…`，674 行）；
- 门禁全绿；真实 `data/config.json` 未被触碰。

---

## 027

**commit**: `test,docs: 去掉对仓库目录名的耦合（测试断言改为校验 module 路径）`

**为什么**：换个目录名不该让测试变红，文档里也不该写死某一台机器上的绝对路径。
这正是 024「已知遗留」里那两条的内容 —— **本轮把它们作废**：清掉耦合之后再改目录，
就只剩"关掉程序、`Rename-Item`"两步（见本节末尾）。

### 做了什么

1. `internal/config/config_test.go`：`TestFindRepoRoot` 原本断言
   `filepath.Base(root) != "chain_proxy"` —— 断言的是**目录名**。改为读 `go.mod`、
   断言第一行是 `module github.com/murongruolan/picohop`：前者是本地细节，
   后者才是这个仓库的身份（跟着代码走，不跟着目录走），顺带把重复的 `os.Stat` 并掉；
2. 已跟踪文件里的绝对路径 `F:\chain_proxy` → `<仓库根>`：`desktop/main.go`（注释）、
   `docs/ARCHITECTURE.md`、`docs/PROJECT_PROGRESS.md`、`docs/MIGRATION_wails_v3.md`（3 处）；
3. `scripts/env.ps1` 顶部那行示例命令改成**相对路径** `. .\scripts\env.ps1`
   —— 用占位符对方便复制粘贴没有帮助；
4. `refs/README.md` 两条 `git clone` 的目标目录由 `F:\chain_proxy\refs\...` 改成
   `.\refs\...` —— 同理，`<` `>` 在命令行里会被当成重定向。

### 有意没改的

- `docs/COMMIT_HISTORY.md` 与 `docs/bench/AB-*` 里的旧绝对路径**保留**：历史记录与
  归档实测数据，改了等于伪造（本轮 commit message 里提一句就够）；
- `docs/RESEARCH_chain_proxy.md` 的**文件名**保留：那里的 `chain_proxy` 指"链式代理"
  这个研究主题，不是项目旧名。

### 验证

- `git grep 'F:\chain_proxy'` 剩下的命中**只在** COMMIT_HISTORY 与 bench 归档里；
- 反向验证：把断言里的期望值改成 `module github.com/wrong/wrong`，`TestFindRepoRoot`
  立刻红（说明这条断言真的在执行、真的能失败），改回来即绿；
- 门禁全绿；真实 `data/config.json` 未被触碰。

### 现在要改目录名，只剩这两步

```
1. 关掉 PicoHop（托盘退出），确认没有 go 进程在跑
2. Rename-Item F:\chain_proxy F:\picohop
```

**唯一还挡着的**是文件锁：`data\webview\EBWebView`（应用运行时）与
`.cache` 下正在被写入的文件。`.cache` 里的 Go 缓存记的是绝对路径，
改名后 Go 会自己重建受影响的部分 —— 不会出错，只是第一次编译慢一点。
`scripts/*.ps1` 全部用 `$PSScriptRoot` 推导根目录，本来就与目录名无关。

---

## 028

**commit**: `docs(history): 补齐 023 之后 99 个提交的索引与合并说明 + 追加漂移检查脚本`

**起因**：024 / 026 / 027 三次登记时反复撞到同一件事 —— 索引停在 023，此后积了
**99 个提交没有登记**。而且第一次报的数字（90）是**错的**：用 `--since=2026-10-12`
过滤，漏掉了 023 当天之后的 10 条。数字错了就说明"靠印象数"这件事本身不可靠，
所以本轮除了补内容，还加了机械防线。

### 做了什么

1. 索引补齐 `023a`–`023f` 六行（按主题归并），合并说明插在 023 与 024 之间，
   与 `006a-u` 的位置约定一致；
2. 修正缺口说明里的数字（90 → 99）与状态（"尚未补齐" → "已补齐"）；
3. 维护约定新增第 6 条 + 新增 `scripts/check_history.ps1`：拿最近若干提交的
   subject 与索引比对，缺登记就非 0 退出。

### 为什么按主题合并

见[合并说明开头](#023a-f)。核心是：`git show <hash>` 比转述准确，而 99 段相似文字
会把索引的价值淹掉。

### 验证

- 合并说明里的 hash 集合与 `git log --oneline d2721c7..ce3db4d` 的输出**逐条比对通过**
  （99 条，无遗漏、无重复、无错字）—— 这步是脚本断言，不是肉眼校对；脚本在写文件
  **之前**比对，不一致就整体中止，不会留下半成品；
- `.\scripts\check_history.ps1` 跑通（最近提交都在索引里）；
- 门禁全绿；真实 `data/config.json` 未被触碰。

---

## 029

**commit**: `chore(git): 分支收归 main（去掉迁移期的 wails-v3 名字）`

**起因**：准备公开时候发现当前分支还叫 `wails-v3`（迁移期的名字），而公开仓库的默认
分支惯用 `main`。真去改名时才发现 `main` **早就存在** —— 它停在迁移之前。

### 分支当时的实际关系

| 事实 | 值 |
|---|---|
| `main` 的尖端 | `17a54c1`（2026-10-11，Wails v3 迁移之前） |
| `main` 有而 `wails-v3` 没有的提交 | **0** |
| `wails-v3` 有而 `main` 没有的提交 | **112** |
| `main` 是否为 `wails-v3` 的祖先 | 是（`git merge-base --is-ancestor` 退出码 0） |

所以这不是"改名"，而是**快进 + 去掉旧名字**：

```
git branch -f main wails-v3    # 让 main 前移到当前状态（main 未检出，安全）
git switch main                # 两条分支指向同一提交 → 工作树一个文件都不动
git branch -d wails-v3         # 已完全合并，-d 就够（不需要 -D）
```

**为什么先 `branch -f` 再 `switch`**：若直接 `switch main`，工作树会先退回到迁移前
（sing-box 时代）的文件、再合并回来 —— 中间存在一个"树是旧状态"的窗口，正在运行的
程序或脚本可能读到旧文件。让 `main` 先指到同一个提交，切换就成了空操作。

### 结果

- 现在只有一个分支 **`main`**，指向 `4bcce53`；两个回退点仍是 tag：
  `v2.0-wails2`、`v0.1-win32-gui`（都还在，所以文档里"随时可回退"这句仍然成立）；
- `docs/MIGRATION_wails_v3.md` 顶部的分支说明改为 `main`，并注明迁移当时的名字；
- `COMMIT_HISTORY.md` 顶部横幅补一段"分支改名"说明，**正文里的 `wails-v3` 不改写**
  —— 与项目改名的处理方式一致（历史记录不改写，只加说明）。

### 验证

- `git branch -a` 只剩 `main`；`git status` 干净；`HEAD` 仍是 `4bcce53`（内容零变化）；
- 门禁全绿；真实 `data/config.json` 未被触碰。