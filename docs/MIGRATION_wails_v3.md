# 迁移到 Wails v3：技术验证与方案

> 状态：**技术验证已完成（GO）**，尚未开始改动 `desktop/`。
> 分支 `wails-v3`；Wails v2 的最终状态已打 tag **`v2.0-wails2`**，随时可回退。
> 本文只记录**已实测验证过**的事实；推测的地方都标了"待验证"。

## 为什么迁

用户要的效果是"对话框能飘到主窗口之外、压在主窗口上面"。
这需要**第二个真正的操作系统窗口**，而 Wails v2 **没有任何创建窗口的 API**
（`pkg/application` 只有 `New/NewWithOptions/Run/Quit/Bind/On/SetApplicationMenu`；
全模块搜 `NewWebviewWindow` 无结果），v2 的 `options.App` 描述的是唯一那个窗口。

顺带一起拿到的 v2 完全做不到的能力（官方文档 <https://v3.wails.io/whats-new>）：

| 能力 | 对本项目的价值 |
|---|---|
| **多窗口** | 对话框独立浮动（本次需求） |
| **系统托盘 + 托盘菜单** | 代理工具天生该常驻托盘：关窗不退出、托盘里直接激活/停止/复制端口 |
| **全局快捷键** | 一键激活/停止 |
| **系统通知** | 最小化时"节点掉线"提醒 |
| 屏幕信息（多显示器） | 替代现在手搓的 `GetSystemMetrics` 虚拟桌面计算 |
| 绑定生成器（静态分析） | 现在 `time.Time` 字段只能靠字符串绕过 |
| Autostart API | **明确不用**（用户不要开机自启） |

## 版本与成熟度

| | v2 | v3 |
|---|---|---|
| 最新 | v2.16.0（2026-09-14） | **v3.0.0-beta.28**（2026-10-05） |
| 定位 | 当前稳定线 | beta（官方："桌面 API 已稳定，v2 仍是稳定版，v2 会继续收到关键修复"） |

本项目锁定 **v3.0.0-beta.28**（不要跟 `latest`，避免某天被无声升级）。

## 已实测验证的事实

验证方式：在 `<仓库根>\.cache\spike-wails3\spike3`（gitignore 内）用
`wails3 init -t vanilla-js` 生成试点工程，写了一个含"主窗口 + 无边框置顶浮动窗口 +
托盘 + 全局快捷键 + 系统通知"的 `main.go`，**用裸 `go build` 构建并真实运行**。

实测日志（节选）：

```
[spike] WebView2 数据目录 = <仓库根>\.cache\spike-wails3\spike3\data\webview
[spike] 主窗口已创建 id=1
[spike] 浮动窗口已创建 id=2（frameless + alwaysOnTop）
[spike] 托盘已 Run
[spike] 已注册全局快捷键 Ctrl+Alt+F9
[spike] JS 调用 Ping("hello-from-main")
[spike] JS 调用 Ping("hello-from-float")
[spike] 启动满 3 秒，尝试发一条系统通知   ← 无报错
[spike] 心跳 #1 主窗口可见=true 浮动窗口可见=true
```

枚举可见窗口得到两个**真正的 OS 窗口**：
`420x260 spike3 · 浮动窗口`、`760x480 spike3 · 主窗口`。
截图里浮动窗口无标题栏（`Frameless` 生效）且显示"服务调用成功：pong: hello-from-float"。

### 1. 构建：不需要 wails3 CLI，也不需要 Taskfile

`wails3 build` 是 Taskfile 驱动的，而**本机没有 `task`**（Taskfile 执行器）。
好消息是**完全不需要**它 —— 官方 Windows 构建任务里真正的命令就是一条 `go build`：

```
GOOS=windows CGO_ENABLED=0 go build \
  -tags production,<EXTRA_TAGS> -trimpath -buildvcs=false \
  -ldflags="-w -s -H windowsgui"
```

- **`production` 标签是必需的**：v3 用它与 `dev` 区分不同实现文件
  （`application_production.go` / `application_debug.go`、`roles_production.go` / `roles_dev.go` 等）。
- `EXTRA_TAGS` 当时是我们已有的 `with_utls,with_quic`。
  > **后续变化（引擎迁移结项后）**：那两个标签是 sing-box 的，已随 sing-box 摘除；
  > 现在图形界面只带 `production` 一个标签。见 `docs/MIHOMO_MIGRATION.md` 第 ⑱ 轮。
- 因此 `scripts/build.ps1` 只需把 `wails build` 换成 `go build`（前端仍用 Vite），
  命令行的构建方式完全不变。

### 2. 前端桥：`Call.ByName` 可以完全绕开绑定生成器

v3 前端用 npm 包 **`@wailsio/runtime`**（3.0.0-beta.28）。它导出：

```js
Call.ByName('main.probeService.Ping', args)   // 按名字调用 Go 服务
Events.On('事件名', e => e.data)              // 接收 Go 事件
Clipboard / Dialogs / Screens / System / Window / WML ...
```

实测两个窗口都调通了：`Call.ByName('main.<Type>.<Method>')`
（名字格式 `<包名>.<类型名>.<方法名>`；试点里是 `main.probeService.Ping`）。

**这意味着不需要 `wails3 generate bindings`，也不需要 `frontend/bindings/`** ——
我们现有的 `desktop/frontend/src/api.js` 本来就把所有调用收敛在一个 `call()` 里，
改一处就够；`store.bindEvents` 里的 `window.runtime.EventsOn` 换成 `Events.On`。

runtime 由 v3 的在 `/wails/runtime...` 路径下提供（源码里 `strings.HasPrefix(path, "/wails/runtime")`）。

### 3. 数据目录可以留在项目内

`application.Options.Windows.WebviewUserDataPath`（应用级，对所有窗口生效），
内部即 `chromium.DataPath`（`webview_window_windows.go`）。
不设的话默认 `%APPDATA%\<exe名>` —— 违反本项目"不往项目外写"的约束，**必须设**。

### 4. 托盘 / 快捷键 / 通知 / 关窗不退出的 API（均已编译运行通过）

```go
// 托盘
tray := app.SystemTray.New()
tray.SetIcon(icoBytes).SetTooltip("…")
menu := application.NewMenu()
menu.Add("显示 / 隐藏主窗口").OnClick(func(ctx *application.Context) { … })
menu.AddSeparator()
tray.SetMenu(menu).OnClick(func() { … }).Run()

// 全局快捷键
app.GlobalShortcut.Register("Ctrl+Alt+F9", func() { … })  // 失败会返回 error

// 系统通知（作为一个 Service 注册）
notif := notifications.New()                    // pkg/services/notifications
application.NewService(notif)
notif.SendNotification(notifications.NotificationOptions{ID: "x", Title: "…", Body: "…"})

// 关窗不退出（托盘常驻的前提）
application.WindowsOptions{DisableQuitOnLastWindowClosed: true}
```

窗口操作（v2 的 `runtime.WindowXxx` 全局函数 → v3 的窗口对象方法）：
`Hide() Show() Focus() Center() SetPosition(x,y) SetSize(w,h) SetMinSize() SetTitle() SetURL()
SetAlwaysOnTop(bool) ExecJS(js) Close() Minimise() Maximise() Restore() IsVisible()
OnWindowEvent(events.Common.WindowClosing, cb)`。

## v2 → v3 API 对照（迁移时照这个改）

| v2（现状） | v3 |
|---|---|
| `wails.Run(&options.App{...})` 一把梭 | `app := application.New(application.Options{...})` → `app.Window.NewWithOptions(...)` → `app.Run()` |
| `Bind: []any{api}` | `Services: []application.Service{application.NewService(api)}` |
| 结构体存 `ctx` + `startup(ctx)` | 服务是普通结构体；启动钩子用 `ServiceStartup`/`OnStartup` 选项 |
| `assetserver.Options{Assets: assets}` | `application.AssetOptions{Handler: application.AssetFileServerFS(assets)}` |
| `//go:embed all:frontend/dist` | 不变 |
| `options.App{Width,Height,MinWidth,MinHeight,Windows{...}}` | `WebviewWindowOptions{Width,Height,MinWidth,MinHeight,Windows: WindowsWindow{...}}` |
| `windows.Options{WebviewUserDataPath}` | `application.WindowsOptions{WebviewUserDataPath}`（应用级） |
| `windows.Options{Theme}`（运行期可切标题栏明暗） | 窗口创建时 `WindowsWindow{Theme: application.Dark}`；**未发现运行期 setter，待验证** |
| `runtime.EventsEmit(ctx, "日志", data)` | `app.Event.Emit("日志", data)` |
| 前端 `window.runtime.EventsOn(...)` | `import { Events } from '@wailsio/runtime'` → `Events.On(name, e => e.data)` |
| 前端 `window.go.main.API.X(...)` | `Call.ByName('main.API.X', ...)` |
| `runtime.ClipboardGetText/SetText(ctx)` | `app.Clipboard.Text() (string,bool)` / `app.Clipboard.SetText(string) bool` |
| `runtime.SaveFileDialog(ctx, opts)` | `app.Dialog.SaveFile()` → `*SaveFileDialogStruct`；消息框 `app.Dialog.Info()/Question()/Warning()/Error()` |
| `runtime.WindowHide/Show/Unminimise(ctx)` | `window.Hide()/Show()/Restore()` |
| `runtime.Quit(ctx)` | `app.Quit()` |

## 迁移计划

1. **外壳重写**（`desktop/main.go`）：`application.New` + 主窗口 + 托盘 + 快捷键 + 通知服务
   + `DisableQuitOnLastWindowClosed` + `WebviewUserDataPath`。
2. **API 层改服务**（`desktop/api.go` / `api_import.go`）：去掉 `ctx` 字段与 `runtime.*` 调用，
   改成 `App` 对象方法；`Bind` → `Services`。
3. **前端桥**（`api.js` / `store.js` / `package.json`）：换 `@wailsio/runtime`。
4. **装 `wails3` CLI 到项目内**：已完成，`<仓库根>\.cache\bin\wails3.exe`（53 MB）。
5. **构建脚本**（`scripts/build.ps1`）：`wails build` → `go build -tags production,...`。
6. **浮动对话框**：`app.Window.NewWithOptions(Frameless + AlwaysOnTop + HiddenOnTaskbar? + URL "/?float=…")`，
   对话框改为在独立窗口里渲染同一份前端（按 URL 参数分流），关窗即返回结果。
7. 复核：`-open` 自测钩子、`.keep` 占位文件、标题栏明暗、托盘图标资源、
   测试与 lint、截图验证、文档与分步提交。

## 已知注意点

- **不要碰 Autostart**：v3 有 `Autostart` 管理器，但用户明确不要开机自启。
- 试点里有一条告警 `Error saving icon: failed to retrieve application icon: cannot load icon
  from resource with id 3` —— 试点没生成 `.syso` 图标资源。正式迁移时要么跑
  `wails3 generate syso`，要么接受该告警（不影响功能）。**待处理。**
- 托盘图标用 `build/windows/icon.ico` 的字节可以正常设置（试点已验证）。
- beta 版本：锁定 `v3.0.0-beta.28`，升级前先跑全量验证。
- **一切依赖与 CLI 都装在项目内**（`GOMODCACHE`/`GOPATH`/`GOBIN` 均在 `.cache` 下），
  项目外不落任何文件。
