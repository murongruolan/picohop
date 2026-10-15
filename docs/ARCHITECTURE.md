# PicoHop 架构设计

本文记录 v2（Wails v3 + Vue 3 + 内嵌 mihomo）的模块划分、数据流与关键取舍。
v1（纯 Win32 界面）的架构说明已随那次重构一起过时，
对应的代码可以在 git 的 `v0.1-win32-gui` 标签处找回。

> 代理内核在 v2 期间从内嵌 sing-box 换成了内嵌 mihomo，sing-box 与它的回退**已完整摘除**
> （只剩 mihomo 一个内核）。换装过程、源码级依据与**实测打包体积**见
> [`MIHOMO_MIGRATION.md`](MIHOMO_MIGRATION.md)；本文只描述**当前**架构。

---

## 1. 一句话架构

```
浏览器 / 任意软件
      │  SOCKS5 或 HTTP（mixed）
      ▼
┌──────────────────────────────────────────────┐
│ internal/proxy  每个节点一个 mixed 监听端口    │
│  · SOCKS5 服务端 / HTTP 服务端（含 CONNECT）   │
│  · 双向中继 + 流量统计 + 空闲超时              │
└───────────────┬──────────────────────────────┘
                │ 按节点配置选择出口实现
      ┌─────────┴──────────┐
      ▼                    ▼
┌──────────────┐   ┌───────────────────────────┐
│ 自研出口      │   │ internal/engine           │
│ socks5/http  │   │  每个节点一个"只有出口     │
│ wireguard    │   │  没有入口"的 mihomo 出站   │
└──────┬───────┘   └──────────┬────────────────┘
       │                      │
       └──────────┬───────────┘
                  ▼
        internal/proxy.Front
        （第一跳：直连 / 全局前置 / 另一个节点）
                  ▼
               目标网站
```

---

## 2. 目录与职责

| 目录 | 职责 | 为什么单独拆出来 |
|---|---|---|
| `internal/config` | 配置模型、默认值、校验、v1→v2 迁移 | 协议元信息（哪些协议、需要哪些字段）只在这里定义一份，界面读它来渲染表单 |
| `internal/ports` | 端口分配、随机、占用检测 | 界面上的三条端口规则集中在一处，可单测 |
| `internal/proxy` | mixed 入口、前置代理、自研出口、中继与统计 | 数据面核心，不依赖任何界面代码 |
| `internal/engine` | 内嵌 mihomo 的五种协议出口（唯一内核） | 把 mihomo 这个重依赖关在一个包里；`internal/proxy` 通过**注册函数**拿到它，不需要 import |
| `internal/wgproxy` | 纯用户态 WireGuard 出口 | 同上，也是注册进去的 |
| `internal/app` | 应用状态机：节点生命周期、激活链、今日统计、测试编排 | 界面与命令行共用的唯一业务入口 |
| `internal/tester` | TCP 延迟 / 真连接延迟 / 测速 / 出口 IP | 纯函数式的测试能力，与状态机解耦 |
| `internal/share` | 分享链接编解码 + 二维码生成 | 参考主流客户端的格式细节，和主流客户端互通 |
| `internal/qrscan` | GDI 截图 + 屏幕圈选 + 二维码识别 | 纯 syscall 的 Win32 代码集中隔离 |
| `desktop/` | Wails v3 应用（前端 Vue 3 + 绑定层 + 托盘/快捷键/通知） | 界面层不碰 proxy/engine，只通过 API 与 `internal/app` 交互 |
| `cmd/picohop` | 无界面命令行 | 排障与自动化测试；也是"界面出问题时还能用"的兜底 |
| `cmd/uishot` | 开发期界面截图工具 | 桌面程序的界面排版必须靠截图验证 |

### 依赖方向

```
desktop ──► internal/app ──► internal/proxy ──► internal/config
cmd/picohop ─┘                 ▲
                                  │ 注册（init 时）
                    internal/engine、internal/wgproxy
```

`internal/proxy` **不 import** `internal/engine`，而是暴露一个注册点：

```go
proxy.RegisterMihomoFactory(func(config.Node, config.Front, func(string, ...any)) (proxy.Upstream, error))
```

主程序 `import _ "github.com/murongruolan/picohop/internal/engine"` 即可获得那五种协议。
这样做的实际收益：`internal/proxy` 的单元测试不需要编译整个 mihomo。
（这个注册点也是**唯一**的内核入口：没有第二个内核、也没有回退 —— 工厂接不了某个节点时
直接把错误交出去，绝不退化成直连。）

---

## 3. 链式代理怎么表达

### 3.1 「前置」从全局变成了三选一

v1 的前置是"全局一个 + 每个上游可以覆盖"。v2 改成每个节点三选一：

```go
const (
    FrontDirect = ""        // 直连
    FrontGlobal = "global"  // 用设置里的全局前置
    // 其余取值 = 项目内另一个节点的 ID
)
```

`internal/app.resolveFront()` 把三种取值翻译成 `config.Front{Type, Host, Port, ...}`：

| 节点配置 | 解析结果 |
|---|---|
| `Front = ""` | `{Type: direct}` |
| `Front = "global"` | 设置里的那一个 |
| `Front = "<节点ID>"` | `{Type: socks5, Host: 127.0.0.1, Port: <那个节点的本地端口>}` |

**为什么"以另一个节点为前置"能成立**：每个节点本来就在本地开着一个
mixed 端口，它天然就是一个可用的 SOCKS5 代理。所以不需要任何额外机制，
只要把前置指向 `127.0.0.1:<端口>` 即可 —— 这也解释了为什么前置永远走回环，
即使那个节点监听着 `0.0.0.0`。

### 3.2 数据面里的两条实现路径

**自研出口（socks5 / http / wireguard）** 是"套娃"式的：

```
conn := front.DialTCP(出口服务器地址)   // 已经在前置隧道内部
握手(conn, 目标)                        // 第二次握手
```

**mihomo 出口（vmess / vless / ss / trojan / hysteria2）** 走的是另一条路：把前置做成
**出站自己的拨号器**，装在 mihomo 的 `BasicOption.DialerForAPI` 上：

```go
opt.BasicOption.DialerForAPI = &mihomoDialer{front: proxy.NewFront(frontCfg)}
// 出站连它的服务器时，那一跳因此经前置出去（TCP 与 UDP 两条路都覆盖）
```

为什么不用 mihomo 的 `dialer-proxy`：它要一个全局 hub + 出站注册表，与本项目
"每节点一个独立出站、可同时激活多个"的用法冲突。`DialerForAPI` 的优先级高于其它所有
拨号配置，且不需要 hub。细节、覆盖范围（哪些协议走 `DialContext`、哪些走 `ListenPacket`）
与验证方式见 `internal/engine/mihomo_dialer.go` 与
[`MIHOMO_MIGRATION.md`](MIHOMO_MIGRATION.md) 第 ⑰ 轮。

每个节点对应**一个独立出站实例**，只取它来拨号，启停互不影响 —— 这正是"每个节点独立
本地端口"所要求的语义。

---

## 4. 关键技术结论（都是实测出来的）

这些结论在写代码之前用一次性探针验证过，写成测试固化在
`internal/engine/mihomo_test.go` / `mihomo_front_test.go` 里：

| 结论 | 表现 | 处理 |
|---|---|---|
| mihomo 的 REALITY 必须显式给 `client-fingerprint` | 否则报 `REALITY is based on uTLS, please set a client-fingerprint` | 映射时就拦住并提示（sing-box 会自动推断，mihomo 不会） |
| 各协议"连服务器"那一跳都走 `BasicOption.DialerForAPI` | 前置不装进去就**静默从本机 IP 出去** | 非直连时必须装 `mihomoDialer`（见 `mihomo_dialer.go`） |
| UDP 有两条不同的路 | ss 原生 UDP 与 hysteria2 的 QUIC 走 `ListenPacket`；vless/vmess/trojan 的 UDP 走 `DialContext("tcp")` | 两个方法都实现；前置做不了 UDP 时明确报错，不回落直连 |
| mihomo 不需要任何构建标签 | — | 与 sing-box 不同，`with_utls` / `with_quic` 都已删除 |

---

## 5. 端口管理

`internal/ports.Allocator` 是一个纯结构体（不依赖 app），界面的三条规则都在它里面：

- `Next()`：`max(本项目内所有 ≥10800 的端口) + 1`，下端不低于 10810，
  再顺延到真正空闲的端口；
- `RandomFree()`：在 `[10001, 49151]` 内随机，**抽到先检测占用**，
  占用就重抽，最多 5 次，仍失败返回 `ErrNoFreePort`（界面据此显示固定文案）；
- `Check()`：先查本项目占用表（能说出"被哪个节点占了"），
  再真的 `net.Listen` 一次看系统层面是否被别的程序占用。

两个刻意的细节：

1. **上界取 49151 而不是 65535**：Windows 默认动态端口范围是 49152-65535，
   系统自己会从那一段发临时端口，随机到那里等于跟操作系统抢端口。
2. **占用检测用"真的 bind 一次"而不是 netstat**：
   Windows 上 Go 不会给监听套接字设置 `SO_REUSEADDR`，所以 bind 失败就是权威答案，
   不需要解析 netstat 输出，也不受权限影响。

---

## 6. 应用状态机（internal/app）

### 6.1 激活链

```
PlanActivation(["C"])  ──►  Order = [A, B, C], Auto = [A, B]
        （A 是 B 的前置，B 是 C 的前置）
```

- 用 DFS 做拓扑排序，**前置在前**，保证被依赖的先起来；
- 同时用"灰/黑"着色检测环路（配置校验里还有一道，双重保险）；
- 被连带启动的节点通过 `Auto` 返回，界面据此提示"将连带激活前置节点"；
- `Deactivate` 反过来：**会连带停止依赖它的节点**，
  否则那些节点会指向一个已经没人监听的端口，表现为"静默不通"。

### 6.2 今日统计为什么要"折算增量"

每个节点服务内部的计数（`proxy.Stats`）是**本次运行累计**，
而服务对象在"停止再启动"后会被重建、计数归零。
所以 `statsLoop()` 每秒做一次差分：把 `当前值 - 上次基线` 累加进
`Node.UpBytes/DownBytes`，并在跨天时清零。这样"今日统计"才能跨重启累加。

### 6.3 测试

四种测试都走"临时建一条链路"的路子（`openTestChain`），**不占用本地端口**，
所以没激活也能测。同一时刻只允许一批测试在跑（避免互相抢带宽导致数据失真）。
结果写回 `Node.LatencyMs / RealMs / SpeedMBps` 并广播事件，界面顺带刷新。

---

## 7. 界面层（desktop）

桌面外壳用的是 **Wails v3（beta.28）**。v2 → v3 的完整对照与验证过程见
[`MIGRATION_wails_v3.md`](MIGRATION_wails_v3.md)，这里只写结论性的东西。

### 7.1 v3 的外壳长什么样

- **构建不走 `wails3 build`**：那条链是 Taskfile 驱动的（需要额外装 `task`），
  而它本质上就是"前端 `npm run build` → `go build -tags production,...`"。
  `scripts/build.ps1` 直接调这条 `go build`，于是**不依赖 wails3 CLI 与 task**，
  构建过程可见可控。**`production` 标签不能漏**（v3 用它区分 production/dev 两套实现文件）。
- **前端桥换成 `@wailsio/runtime`**：调用是 `Call.ByName('main.API.方法名', ...args)`，
  不再有 v2 那种 `window.go.main.API.*` 全局对象。因为**所有调用都收敛在
  `src/api.js` 的一个 `call()`** 里，这次换桥只改了这一个文件；
  事件同理，`Events.On` 的回调收到的是 `{name, data, sender}`，在 `onEvent` 里统一解包。
  判定"是否在 Wails 里"用的是宿主通道 `window.chrome.webview.postMessage`，
  **不能用 `window._wails`** —— 后者被 runtime 包在导入时就赋成了 `{}`，永远为真。
- **窗口是一等对象**：`application.New()` → `app.Window.NewWithOptions(...)` → `app.Run()`。
  主窗口、托盘、快捷键在 `Run()` 之前建好；后端加载走服务的 `ServiceStartup/ServiceShutdown`
  钩子（v2 是 `OnStartup/OnShutdown` + 一个 ctx 字段）。
- **关窗不退出**：`WindowsOptions.DisableQuitOnLastWindowClosed` + 主窗口的
  `RegisterHook(events.Common.WindowClosing, ...)` 里 `e.Cancel()` 后 `Hide()`。
  真正的退出走托盘菜单或界面上那个退出按钮。
- **托盘与全局快捷键**：
  `app.SystemTray.New()` 配 `SetIcon/SetTooltip/SetMenu/OnClick/Run`，
  菜单项直接调 API 方法（所以不依赖界面是否打开）；
  快捷键 `Ctrl+Alt+C` 显隐主窗口、`Ctrl+Alt+A` 全开/全停，注册失败只提示不阻断启动。
  托盘图标用 `//go:embed build/windows/icon.ico` 嵌进二进制 ——
  它是"关窗后仍然活着"的那部分，不该依赖磁盘上的文件。
- **系统通知**：`pkg/services/notifications` 作为服务注册，
  "已收进托盘""快捷键被占用"这类场景下发真正的 Windows 通知。
- **前端只通过 `desktop/api*.go` 暴露的方法访问后端**，
  拿到的都是"已经算好、已经格式化"的数据（协议显示名、今日流量的可读文本、
  前置的描述），协议细节不出后端。
- **事件**：后端通过 `app.Subscribe` 广播 `log / nodes / stats / notify / testDone`，
  绑定层转成 Wails 事件推给前端。有一条硬约定写在了 `internal/app` 的注释里：
  订阅回调必须快速返回，且**不允许再同步调用 App 的方法** ——
  因为事件可能在持有 App 内部锁时发出。
- **"外壳初始化结果"必须落日志**：托盘、快捷键都是在 `app.Run()` 之前建立的，
  那时后端还没加载、界面也没起来，而 GUI 版用 `-H windowsgui` 构建、**没有控制台** ——
  不记下来就等于凭空消失。所以 `api` 里存下托盘/快捷键的建立结果与"后端就绪前的告警"，
  等 `ServiceStartup` 起来后写进 `data\picohop.log` 并把告警补投成界面通知。
- **刷新策略**：stats 事件每秒一次，前端做了 700ms 去抖合并，
  不会因为事件风暴把 IPC 打满。
- **主题**：以配置文件里的 `settings.theme` 为准（localStorage 只是缓存）。
  标题栏明暗在创建窗口时用 `WindowsWindow{Theme: ...}` 指定；
  **运行期切换 v3 没有 setter**（v2 有 `WindowSetDarkTheme`），
  所以 `SetWindowTheme` 直接调 Win32 的 `DwmSetWindowAttribute`
  （`theme_windows.go`，只匹配本进程窗口，不致误改别的程序）。
- **通知是自己实现的**（`src/notify.js` + `components/Notifications.vue`），
  没有用 Element Plus 的 `ElNotification`。原因：它的垂直位置由**每条通知自己的
  `offset`** 决定，而同一个 tick 里连发多条时它们互相看不到对方，
  于是拿到同一个 offset 直接叠在一起 —— 实测一次弹 5 条只看得见最后一条。
  自己用 flex 列布局就同时拿到了"固定在控制栏下方"和"多条正确堆叠"。
- **WebView2 的浏览器配置目录被显式指到 `data\webview\`**。
  默认它在 `%APPDATA%\<程序名>`，那是项目目录之外；本项目的规矩是
  "一切都在 <仓库根> 内"，所以用 `WebviewUserDataPath` 改掉。
  顺带的好处：清数据只要删 `data\`，不会在用户漫游配置里留残留。
- **列表是通栏的**（左右不留白），表头支持拖拽调列宽。这里有两个必须知道的坑：
  1. Element Plus 的列宽拖拽手柄**要求 `<el-table>` 上有 `border`**
     （`table-header/event-helper.mjs` 里 `if (!props.border) return`），
     而 `border` 同时会画一圈外框 —— 通栏下外框多余，用 CSS 把它去掉。
  2. 库内部的拖拽下限**只有 30px**（`columnRect.left - tableLeft + 30`），
     而单元格左右各 10px 内边距，30px 只剩 10px 文字区，一个汉字会被切一半、
     看起来就像两列叠在一起。所以在 `header-dragend` 里自己钳到 `MIN_COL_WIDTH`
     （56px，约两个汉字宽度），并直接改 `column.realWidth` ——
     此刻库的 `scheduleLayout` 还排在下一个 rAF，改写会被紧接着的重排采用，
     不会先按超窄宽度闪一帧。
  3. 最后一列默认**不允许**拖拽（`allowDragLastColumn`），要显式打开才能"任意列都能拖"。
  4. **最右边那一列是"填充列"**：它不吃 `width`，只吃 `min-width`，
     于是实际宽度 = `minWidth + 剩余空间`，永远铺满右端。
     为什么需要它：Element Plus 判断一列是否弹性，依据是
     `!isNumber(column.width)`（`table-layout.mjs` 的 `updateColumnsWidth`），
     而**拖拽会把内部 `column.width` 写成一个具体数字** ——
     所以只要用户拖过几列，就不再有弹性列，表格总宽小于容器宽，
     右端立刻空出一条留白。填充列保证任何一列被拖窄省出来的空间都有归属。
     实现上还要在 `header-dragend` 里把填充列的 `column.width` 清成 `undefined`，
     它才会在拖拽之后重新回到弹性状态（`min-width` 的 watcher 会触发重排）。
     代价：在空间还有富余时，把填充列拖窄看不出变化 —— 这是刻意的，
     拖它实际改的是它的最小宽度（窗口变窄时才有区别）。
- **对话框可以拖动"飘开"，但只能在主窗口内部**。用 `<el-dialog draggable>` 实现 ——
  它的 `use-draggable`（`hooks/use-draggable/index.mjs` 第 25-28 行）在
  `overflow` 为假时会 `clamp(moveX, minLeft, maxLeft)`，把对话框限制在视口内；
  而 `overflow` 的默认值就是假，所以**默认就是"能拖但不会被边界裁切"**。
  注意它的文档注释写的是 "draggable Dialog can overflow the viewport"，
  那是显式打开 `overflow` 时的行为，默认并非如此。
  **对话框现在有两种形态**：
  - **浮动窗口**：`添加 / 编辑节点` 开在**独立的、无边框、置顶的 OS 窗口**里 ——
    可以拖到主窗口之外、永远压在主窗口上面（这正是从 v2 升到 v3 的原因）。
    机制见下面 7.2。
  - **窗口内浮层**：其余对话框（导入 / 分享 / 设置 / 仅端口小窗）仍是主窗口里的
    `<el-dialog>`，可拖动但只能在窗口内。
- **对话框本体的样式必须写 `.el-dialog.cp-dialog`**，不能写 `.cp-dialog .el-dialog`。
  `el-dialog` 上的 `class` 会**透传到 `.el-dialog` 自身**（不是外面那层 overlay），
  所以祖先-后代选择器匹配不到。踩过一次：给对话框加高度上限的规则没生效，
  表现是"VLESS 那种长表单底部按钮仍被窗口裁掉"。
- **长表单靠高度上限 + 内部滚动解决**：`.el-dialog.cp-dialog` 设
  `max-height: calc(96vh - 32px)` 并成为 flex 列，`.el-dialog__body` 设
  `flex:1; min-height:0; overflow-y:auto`（少了 `min-height:0` 会被内容撑破），
  这样标题栏和底部按钮永远可见。另配 `top="4vh"` + `margin-bottom:24px`
  保证 `4vh + 上限 + 24px ≤ 100vh`。

### 7.2 圈选窗口为什么是纯 Win32（而不是 Wails 的第二个窗口）

v3 已经能开多个窗口了，但圈选那一刻需要的是"**全屏置顶、无边框、自定义十字准星**"
这一种很特殊的东西：`WS_POPUP + WS_EX_TOPMOST`，先截图再建窗口（避免把自己拍进去），
准星由自己画（`ShowCursor(FALSE)` + 在 `WM_PAINT` 里画十字线，
比构造自定义光标位图可靠得多）。用 Win32 直接做比"再起一个 WebView 再去藏边框和光标"
更短、更可控。**"能不能多窗口"与"这件事该不该用 WebView 做"是两个问题。**

### 7.3 浮动对话框（独立窗口）

`添加 / 编辑节点` 开在一个**无边框 + 置顶**的独立窗口里（`desktop/float.go`），
所以它能被拖到主窗口之外、压在主窗口上面 —— 这是 DOM 浮层做不到的事
（DOM 元素只能画在所属窗口的客户区里，怎么移动都会被窗口边界裁掉）。

**实现方式：复用同一份前端，而不是再做一个入口。**
浮动窗口加载的是同一个 SPA，只是带 `?float=node`：

    Go: OpenFloatDialog(kind, payload, devOpen)  → 建窗口 → 阻塞等 <-result
    前端 main.js: 见到 ?float= 就加 body.cp-float-window 并把 kind 传给 App
    App.vue: enterFloatMode() 只做两件事 —— 打开那个对话框、
             把 ui.onNodeDialogClosed 接上（结束时 SubmitFloatDialog 回传）
    CSS: body.cp-float-window 藏掉主界面、让对话框铺满窗口、
         并在标题栏上设 --wails-draggable: drag（拖动 = 移动窗口）

好处是对话框的校验、端口检测、保存逻辑**一行都不用重写**。
代价是"关掉时要回传结果"这件事得显式接上（就是那个 onNodeDialogClosed）。

**层级：它是主窗口的「附属窗口」，不是置顶窗口。**
`AlwaysOnTop` 会让它压过**所有**程序，用户明确说"太高了，只比主窗口高一级就行"。
所以改成 Win32 的 owned window：`SetWindowLongPtrW(hwnd, GWLP_HWNDPARENT, ownerHwnd)`
（v3 没有"设置所有者"的 API，`findOwnWindow` 自己按标题 + 本进程 id 找句柄）。
效果是：永远在主窗口之上、不会被主窗口盖住，但别的程序能盖住它；
主窗口最小化/关闭时它跟着走 —— 全部符合直觉。
验证方式是客观指标而不是肉眼：`uishot -list` 会打印每个窗口是否带 `WS_EX_TOPMOST`
以及所有者句柄。修好后浮动窗口不带 TOP、`owner` 正好等于主窗口句柄。

**紧凑标题栏。** 窗口没有系统标题栏，对话框的标题栏就兼任拖动把手。
一开始直接沿用了 Element Plus 的对话框头部（16px+12px 内边距 + 大字号），
用户形容为"一个大额头"。现在在浮动模式下把它压成 34px：
标题在左、关闭按钮在右（`position: static` + `margin-left: auto`），带一条下边框，
和主窗口的标题栏同一个观感。

**关闭时不要淡出动画。** 窗口马上就要销毁，动画只会留下一帧
"内容已经清空、只剩一个空架子"的画面（用户报过"内容先消失、过一秒才整个消失"）。
浮动模式下把 `dialog-fade-*` 的 animation/transition 全部关掉。
`OpenFloatDialog` 是**阻塞**的：主窗口的前端一个 `await` 就拿到用户在浮动窗口里的
操作结果。等待方一定被唤醒，因为三条路都会结束会话：
提交（`SubmitFloatDialog`）、取消、以及用户直接关窗（`WindowClosing` 钩子按取消处理，
而且**必须先 `e.Cancel()`**，否则窗口被销毁、等待方就永远醒不来了）。

**四个踩过的坑（都留了注释）：**

1. **不要从主窗口的前端去查浮动窗口的几何信息**。
   v3 的 `Window.Get(name)` 是相对**当前窗口**的，从主窗口调它会拿到主窗口自己的
   位置与尺寸（我第一次的"越界测试"就是被这个骗了，报出 1180x740 这种明显不对的数字）。
   窗口名册在 Go 侧，所以 `FloatWindowBounds` / `MainWindowBounds` / `MoveFloatWindow`
   都由 Go 回答。
2. **JS 的 `switch` 里重复的 case 名只有第一个会命中**，后一个是死代码、而且**不报错**。
   我给"主窗口发起测试"和"浮动窗口自行结束"两处用了同名 `floatsubmit`，
   结果主窗口那段永远不执行，表现是"跑完什么也没发生"。
3. **自测用例出错必须看得见**：`onMounted` 里 `await` 一个失败的自测会静默 reject，
   表现成"界面起来了但什么也没发生"。所以自测外面套了 try/catch，把错误弹成通知。
4. **两套拖动机制绝不能同时开**（修的是用户实报的 bug）。
   窗口内浮层靠 Element Plus 的 `draggable`（用 `transform` 移动对话框卡片），
   浮动窗口靠 v3 的 `--wails-draggable: drag`（移动整个 OS 窗口）。
   浮动窗口里如果两套都开着，按住标题栏会**同时**发生两件事：卡片被 transform 拖走、
   窗口也在移动。现象是"卡片跟着鼠标跑、松开还黏在鼠标上，而且窗口内的页面上下滚"
   —— 用户就是这么报的，一眼看不出是两套机制打架。
   所以 `NodeDialog` 写的是 `:draggable="!ui.floatMode"`；
   另外标题栏加了 `user-select: none` / `-webkit-user-drag: none`，
   免得浏览器再掺一脚原生文字选中/元素拖拽。
   回归测试：`-open floatdragtest` 在浮动窗口里合成一次标题栏拖动，
   断言 `.el-dialog` 的 inline transform **仍为空**；
   而窗口内的 `-open dialogdrag` 断言它**会**移动（保证没把原有功能改坏）。

### 7.4 界面自测怎么截图（怎么不打扰用户）

`cmd/uishot` 前期用"截屏幕上窗口所在的那块区域"，这有两个问题：

1. 窗口被别的程序挡住时，截到的是**挡住它的那个程序**（实测截出过
   用户正在看的浏览器页面）；
2. 为了让窗口露出来就得 `SetForegroundWindow` 抢前台焦点 ——
   那会打断正在用电脑的人。

现在改成 `PrintWindow(hwnd, memDC, PW_RENDERFULLCONTENT)`：
让**窗口自己**把画面渲染进我们提供的 DC，因此既不依赖它是否可见、
也不需要抢焦点。万一某个窗口因为硬件叠加层返回一片黑，
`qrscan.LooksBlank` 会识别出来并退回"截屏幕区域"的老办法（那时才抢焦点）。

---

## 8. 数据与配置

- 位置：`<有 go.mod 的目录>\data\config.json`（可用 `PICOHOP_DATA_DIR` 覆盖）。
  **向上找 go.mod** 这一条很关键：`go run` 的可执行文件在 Go 构建缓存里，
  按"exe 同级目录"算会得到一个缓存目录里的 `data\`，
  于是命令行读到的和界面读到的不是同一份配置（实测踩过）。
- 明文 JSON，方便手工查看与备份；已被 `.gitignore` 排除。
- 统计改动每 30 秒落盘一次，退出时强制落盘。
- **版本迁移**：`Load()` 先探测 `version`，小于 2 就先迁移再反序列化。
  迁移的第一原则是**行为不变**：v1 里"没有 `frontOverride`"意味着走全局前置，
  所以迁移后显式写成 `Front = global` —— 否则用户的住宅代理会突然变成直连。

---

## 9. 已知限制

- **UDP**：本地端口的 UDP 能力取决于出口协议与前置能力。
  `http` 出口与 `wireguard` 出口不支持 UDP；**HTTP 前置也不支持 UDP**
  （所以 Hysteria2 这种纯 QUIC 协议不能用 HTTP 前置，配了会在构造期被拒）；
  Reality / Hysteria2 等在部分网络下也可能被运营商 QoS，浏览器会回落到 TCP。
  通道实现是**每个目标一个常驻读协程 + 一条 fan-in 队列**（`internal/engine/mihomo_udp.go`），
  空闲 60 秒的目标会被回收；**不再是"单协程轮询所有目标"**（那个实现有实测代价：
  延迟 ∝ 目标序号、前面的目标会饿死后面的，详见 COMMIT_HISTORY 034）。
  ⚠️ 另外注意：`SupportUDP()` 只看协议自己的 `udp` 开关、**不看传输能不能承载 UDP**，
  所以它不是"这条节点一定能跑 UDP"的保证 —— 以实测为准（本机实测 vless+xhttp 可跑）。
- **单实例内存**：每个 mihomo 出口是一个独立出站实例，节点很多时（几十个）会有一点固定开销。
- **托盘图标在 Windows 11 上默认落在折叠区**：新注册的托盘图标会被系统放进
  `^` 折叠区，需要用户自己拖出来固定。功能不受影响，但"怎么没看到图标"多半是这个原因。
- **无障碍未适配**：界面是自用的桌面工具，没有做完整的键盘导航与读屏支持。
- **端到端验证的边界**：这条到 036 轮（2026-10-16，用两台按小时计费的临时真实服务器）
  **已经跑通整个矩阵**：vless 的 tcp / ws / grpc / h2 / httpupgrade、vmess 的 ws / grpc、
  hysteria2 / shadowsocks / trojan —— 每一种都验了 **TCP 出口 IP** 与 **UDP（DNS 往返）**；
  链式（前置 → 出口两台）也在**网络层**确认过：出口机上看到的对端是前置机的 IP，
  而不是本机（`27.129.153.239` vs `119.45.225.253`）。
  hysteria2 另验了自签证书的两条认证路（`allowInsecure` 与 `pinSHA256`，后者用错 pin
  必须失败：`certificate fingerprints do not match`）与**端口跳跃**
  （同一条会话打过 11 个不同目的端口）。
  服务端配方、DNAT、客户端链接、矩阵跑法与踩过的坑见
  [`TEST_SERVER_RECIPE.md`](TEST_SERVER_RECIPE.md)。
  仍未覆盖：多目标并发压力、长稳内存；`hysteria2(QUIC)` 的 UDP 已覆盖。
  更早只验到"出站能构造、拨号发得出去"那一层，见
  [`MIHOMO_MIGRATION.md`](MIHOMO_MIGRATION.md) 第 ⑰ 轮。
