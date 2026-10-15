# PicoHop

> **测试版。** 功能还会改，接口和界面都可能变。

Windows 上的本地代理前端。给每个节点开一个独立的本地端口，可以同时激活多个，
并且支持链式：先过一个前置，再走某个节点出去。

---

## 它是干什么的


同类客户端一次只能用一个出站。

PicoHop 的做法是：一个出口一个本地端口，也可配置进走一个节点。

```
浏览器 → 127.0.0.1:10810 →（前置）→ 节点A → 目标网站
浏览器 → 127.0.0.1:10811 →（前置）→ 节点B → 目标网站
浏览器 → 127.0.0.1:10812 → 直连 → 节点A → 目标网站
浏览器 → 127.0.0.1:10812 → 直连 → 节点B → 目标网站
```

浏览器或软件里指定哪个端口，流量就走哪条链。不用的端口不占流量。
「必须在住宅 IP 上访问的站点」和「普通站点」因此可以各走各的，不用来回改全局代理。

前置有三种选法：直连、全局前置代理、或者项目内的另一个节点。
选第三种就形成 `节点 A → 节点 B` 的链路；激活外层节点时会自动按依赖顺序把前置拉起来，
并且检测环路。删掉被依赖的节点时，引用它的节点会退回直连并提示。

---

## 功能

### 节点

- 一个节点一个本地端口，同时可以激活多个。默认端口从 `10800` 往后排，从 10810 起。
- 本地端口是 **mixed**：同一个口同时收 SOCKS5 和 HTTP 客户端。
- 八种出口：
  - SOCKS5

  - HTTP

  - WireGuard

  - VMess

  - VLESS

  - Shadowsocks

  - Trojan

  - Hysteria2

- 传输：`tcp`、`ws`、`grpc`、`http(h2)`、`httpupgrade`、`xhttp`。
- 导入：剪贴板里的 `vless://` `vmess://` `ss://` `trojan://` `hysteria2://`（含 `hy2://`）
  分享链接，可以一次多条；也可以用鼠标在屏幕上圈选二维码识别。识别完先问你要用哪个本地端口。
- 导出：二维码图片 + 协议文本。
- 列表默认 8 列（类型、别名、本地端口、地址、远程端口、传输、延迟、速度），其余列按需打开。
  单击选中、Ctrl 加选、Shift 范围选、回车激活、双击编辑。

### 测试

- **TCP 延迟**：只测本地到节点服务器的握手。
- **真连接延迟**：**取样多次取最小值**。
- **速度**：走节点本地端口下载，量吞吐。
- **出口 IP**：用来判断链路到底有没有生效。
- **网站 × 节点矩阵**：一组站点乘上所有在跑的节点，看每个站点经哪个节点能通。。

### 底部面板

| 视图 | 里面是什么 |
|---|---|
| 流量统计 | 上传/下载折线图（1 分钟 / 10 分钟 / 1 小时）、活跃连接数、累计量、按节点排行 |
| 网络设置 | 允许局域网、起始端口、空闲自动断开 |
| 日志 | 用户日志 + 网络日志，支持正则过滤 |
| 网站测试 | 站点卡片（默认 Apple / GitHub / Google / YouTube，可加自定义），右边是本机 IP 信息 |
| 连接 | 每条连接的主机、链路、上传、下载、时长，可以一键关闭全部 |

### 外壳

- **系统代理模式**：把 Windows 系统代理指到指定的选定节点上。
  程序被强杀需重新启动来自愈。
- **托盘**：关闭窗口是收进托盘，进程和代理继续跑。
- **全局快捷键**：默认 `Ctrl+Alt+C` 显示 / 隐藏主窗口，可以在设置里改。
- **通知**：五种类型（primary / success / warning / info / error）。

---

## 环境

- Windows 10 / 11 x64。界面是 Wails + Vue，依赖系统里的 **WebView2** 运行时
  （Win11 自带；Win10 可能需要装一次）。
- Go **1.25**（`go.mod` 里写的是 `go 1.25.5`）。
- Node + pnpm/npm，只有构建前端时需要。`scripts/check.ps1` 里调的是 `npm run build`。

## 构建

```powershell
.\scripts\build.ps1                 # 图形界面 + 命令行，产物在 dist\
.\scripts\build.ps1 -Cli            # 只构建命令行版（快）
.\scripts\build.ps1 -Gui            # 只构建图形界面版
.\scripts\build.ps1 -SkipFrontend   # 前端没改时跳过 npm 构建
.\scripts\build.ps1 -Version 1.2.3  # 指定版本号
```

产物：

- `dist\PicoHop.exe` —— 图形界面，双击运行
- `dist\picohop-cli.exe` —— 命令行版

命令行（排障用）：

```powershell
picohop                     # 等价于 ls
picohop ls                  # 列出节点与运行状态
picohop run                 # 启动节点并前台运行（Ctrl+C 退出）
picohop run -seconds 30     # 跑 30 秒后自动退出（脚本里用）
picohop test -kind real     # tcp / real / speed / exit
picohop fetch               # 探测前置与各节点的出口 IP
picohop validate            # 只校验配置，不启动任何服务
picohop import "<链接>"     # 从分享链接导入节点
picohop express <节点>      # 导出节点的分享链接
picohop engines             # 自检内嵌 mihomo 是否注册上了
picohop wgkey               # 生成 WireGuard 密钥对
```

通用参数：`-config <路径>`、`-node <ID或别名>`。

## 测试

```powershell
.\scripts\check.ps1         # 门禁：gofmt + go vet + 前端构建 + go test ./...
.\scripts\lint.ps1          # gofmt + go vet + 配置校验 + 内核自检
.\scripts\test_e2e.ps1      # 端到端：经本地端口用 SOCKS5 / HTTP 取出口 IP，和前置对比
.\scripts\test_wg.ps1       # WireGuard 出口专项
.\scripts\test-sysproxy.ps1 # 系统代理的开关顺序与还原
.\scripts\ui_shot.ps1       # 界面截图（开发自测）
```

`test_e2e.ps1` 直接用 `data\config.json` 里的真实节点，跑之前确认前置是通的。

---

## 配置与凭据

配置文件是 `data\config.json`。
程序启动时会把 `data\` 目录的权限收紧成「当前用户 + Administrators + SYSTEM」

---

## 已知不足

- TUN 模式**没有实现**。设置里没有这个开关，别找了。

---

## 文档

| 文档                                     | 里面是什么         |
| ---------------------------------------- | ------------------ |
| [docs/USER_GUIDE.md](docs/USER_GUIDE.md) | 使用手册与故障排查 |

## 许可证

**GPL-3.0** —— Copyright (C) 2026 murongruolan。
