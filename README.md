# PicoHop

> **测试版。** 功能还会改，接口和界面都可能变。别把它当成唯一的翻墙通道 ——
> 它是挂在别的代理后面的，前置挂了它也跟着不通。

Windows 上的本地代理前端。给每个节点开一个独立的本地端口，可以同时激活多个，
并且支持链式：先过一个前置（比如你本机跑的主流客户端），再走某个节点出去。

---

## 它是干什么的

日常上网不需要住宅 IP，住宅 IP 又慢又贵。只有少数对 IP 敏感的站点必须用。
但主流客户端、主流客户端这类客户端一次只能用一个出站。

PicoHop 的做法是：把「前置」和「出口」拆开，一个出口一个本地端口。

```
浏览器 → 127.0.0.1:10810 →（前置主流客户端）→ 住宅节点 → 目标网站
浏览器 → 127.0.0.1:10811 →（前置主流客户端）→ 另一个节点 → 目标网站
浏览器 → 127.0.0.1:10812 → 直连 → 机房节点 → 目标网站
```

浏览器或软件里指定哪个端口，流量就走哪条链。不用的端口不占流量。
「必须在住宅 IP 上访问的站点」和「普通站点」因此可以各走各的，不用来回改全局代理。

前置有三种选法：直连、全局前置代理（设置里那一个）、或者项目内的另一个节点。
选第三种就形成 `节点 A → 节点 B` 的链路；激活外层节点时会自动按依赖顺序把前置拉起来，
并且检测环路。删掉被依赖的节点时，引用它的节点会退回直连并提示。

---

## 内核

出口由**内嵌的 mihomo**（`v1.19.32`）负责。不依赖任何外部内核进程，不用另外装
xray 或 sing-box 的可执行文件。

项目以前内嵌的是 sing-box，已经完整摘除：`go.mod` / `go.sum` 里没有 `sagernet/sing-box`，
也没有回退开关。迁移的实测数据、源码级依据和过程中踩的坑都在
[docs/MIHOMO_MIGRATION.md](docs/MIHOMO_MIGRATION.md) 里，那份文档本身留了"当时是什么状态"的标注。

用到的库：

| 库 | 用途 |
|---|---|
| `github.com/metacubex/mihomo` | 五种协议的出口（每节点一个只有出口、没有入口的实例） |
| `github.com/wailsapp/wails/v3` | 桌面外壳（窗口、托盘、通知、剪贴板） |
| `github.com/makiuchi-d/gozxing` + `github.com/skip2/go-qrcode` | 扫二维码 / 出二维码 |
| `golang.zx2c4.com/wireguard` | 纯用户态 WireGuard 出口，不装驱动、不改系统路由表 |

---

## 功能

### 节点

- 一个节点一个本地端口，同时可以激活多个。默认端口从 `10800` 往后排，从 10810 起。
- 本地端口是 **mixed**：同一个口同时收 SOCKS5 和 HTTP 客户端。
- 八种出口：

  | 协议 | 谁实现 | 本地端口支持 UDP |
  |---|---|---|
  | SOCKS5 | 自研（第二次握手套在前置隧道里） | 是 |
  | HTTP | 自研（CONNECT 隧道） | 否（HTTP 代理本身不支持） |
  | WireGuard | 自研，纯用户态 | 否 |
  | VMess | mihomo | 是 |
  | VLESS（含 XTLS-Reality） | mihomo | 是 |
  | Shadowsocks | mihomo | 是 |
  | Trojan | mihomo | 是 |
  | Hysteria2 | mihomo | 是 |

- 传输：`tcp`、`ws`、`grpc`、`http(h2)`、`httpupgrade`、`xhttp`。
- 导入：剪贴板里的 `vless://` `vmess://` `ss://` `trojan://` `hysteria2://`（含 `hy2://`）
  分享链接，可以一次多条；也可以用鼠标在屏幕上圈选二维码识别。识别完先问你要用哪个本地端口。
- 导出：二维码图片 + 协议文本。
- 列表默认 8 列（类型、别名、本地端口、地址、远程端口、传输、延迟、速度），其余列按需打开。
  单击选中、Ctrl 加选、Shift 范围选、回车激活、双击编辑。

### 测试

- **TCP 延迟**：只测本地到节点服务器的握手。
- **真连接延迟**：走节点本地端口发请求，**取样多次取最小值**（默认 2 次，第 2 次起复用
  连接），与主流客户端同口径；冷连接那一次的值记在日志里。
- **速度**：走节点本地端口下载，量吞吐。
- **出口 IP**：单独查一次，用来判断链路到底有没有生效（出口 IP 和前置出口 IP 不一样才算通）。
- **网站 × 节点矩阵**：一组站点乘上所有在跑的节点，看每个站点经哪个节点能通。
  可以切成"只测当前链路"。

### 底部面板

窗口底部常驻一条 34px 的栏，五个视图：

| 视图 | 里面是什么 |
|---|---|
| 流量统计 | 上传/下载折线图（1 分钟 / 10 分钟 / 1 小时）、活跃连接数、累计量、按节点排行 |
| 网络设置 | 允许局域网、起始端口、空闲自动断开 |
| 日志 | 用户日志 + 网络日志，支持正则过滤 |
| 网站测试 | 站点卡片（默认 Apple / GitHub / Google / YouTube，可加自定义），右边是本机 IP 信息 |
| 连接 | 每条连接的主机、链路、上传、下载、时长，可以一键关闭全部 |

网络日志和连接表的数据源是本程序自己的 handler（`internal/proxy/connlog.go`），
不是去读内核的日志接口。

### 外壳

- **系统代理模式**：把 Windows 系统代理指到指定的那个节点上；退出前会还回去。
  程序被强杀下次启动也会先自愈一次，不会把你整机留在断网状态。
- **托盘**：关闭窗口是收进托盘，进程和代理继续跑；托盘菜单能激活/停止、复制本地地址、退出。
- **全局快捷键**：默认 `Ctrl+Alt+C` 显示 / 隐藏主窗口，可以在设置里改。
- **通知**：右上角，五种类型（primary / success / warning / info / error）。
- 不注册开机自启动。不写启动项、不建计划任务、不装服务。

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

两个文件名不能只靠大小写区分：NTFS 不区分文件名大小写，`PicoHop.exe` 与
`picohop.exe` 是同一个文件。早先就是这样，图形界面版把命令行版静默覆盖了。

图形界面版**不走 `wails3 build`**。那条链是 Taskfile 驱动的，要额外装 `task`，
而它做的事就是「前端 `npm run build` → `go build -tags production`」。
`scripts\build.ps1` 直接调这条 `go build`，所以不依赖 wails3 CLI。`production`
标签不能漏，Wails v3 用它区分 production / dev 两套实现文件。

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

配置文件是 `data\config.json`，**明文的**，里面就是节点的账号密码和密钥。
所以：

- `data\` 整个目录都在 `.gitignore` 里，别往仓库里塞。
- 程序启动时会把 `data\` 目录的权限收紧成「当前用户 + Administrators + SYSTEM」
  三条完全控制，并关掉权限继承（`internal/config/datadir_windows.go`）。
  这不需要管理员权限。但如果 `data\` 落在 FAT/exFAT 分区或网络盘上，那里没有 NTFS ACL，
  收紧会失败 —— 这种情况程序照常启动，但会在 stderr 和日志里明确警告，
  不会静默放过。
- 日志里不打印配置内容和扫码识别到的原文，只记长度。分享链接本身带着全部凭据，
  发给谁就等于把节点给了谁。

## 网络暴露面

本地监听口**默认只绑 `127.0.0.1`**，不会触发 Windows 防火墙提示。

设置里的「允许局域网连接」会把监听地址换成 `0.0.0.0`。开了以后，
**没配本地端口鉴权的节点会被拒绝启动**，报错会告诉你去哪儿补。

所以：

- 除非你确实要让别的设备连进来，否则别开这个开关。
- 真开了，就给每个要用到的节点补上本地端口鉴权的用户名和密码。

---

## 已知不足

这些是现状，不是路线图。

- **部分传输还没在真实节点上验过。** `ws`、`grpc`、`http(h2)`、`httpupgrade`
  这四种只有单测，没有拿真实节点跑通。
  已经用真实节点逐条验过的是：`vless+xhttp+REALITY`、`vmess+tcp`、`vmess+UDP`、
  `hysteria2+证书 pin`、`trojan+tcp+TLS`、`shadowsocks+aes-256-gcm` 六条
  （清单在 [docs/MIHOMO_MIGRATION.md](docs/MIHOMO_MIGRATION.md) 第 7 节）。
- **hysteria2 的端口跳跃（`mport`）没做**，配置里没有对应字段。
- **UDP 是轮询实现。** `internal/engine/mihomo_udp.go` 逐个 conn 给一小段读超时、
  谁有包就返回。节点一多，这条路的开销会体现出来。
- **CLI 把 `-h` / `-help` 当普通参数。** 命令行版没有真正的 `-h`，写
  `picohop -h` 会被当成一个节点名，然后真的去执行 `ls`。已知，没修。
- **只支持 Windows。** `internal/config/datadir_other.go` 之类的非 Windows 分支
  编译得过，但没用过，界面的系统代理、托盘、全局快捷键都是 Windows 实现。
- **每个激活的节点一个 mihomo 实例**，节点多的时候常驻内存会线性上去。
  1 / 2 / 4 / 8 个节点的内存曲线还没测过。
- TUN 模式**没有实现**。设置里没有这个开关，别找了。

---

## 文档

| 文档 | 里面是什么 |
|---|---|
| [docs/USER_GUIDE.md](docs/USER_GUIDE.md) | 使用手册与故障排查，先看这个 |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | 当前架构、模块边界、关键取舍 |
| [docs/BOTTOM_PANEL.md](docs/BOTTOM_PANEL.md) | 底部面板的实现契约 |
| [docs/MIHOMO_MIGRATION.md](docs/MIHOMO_MIGRATION.md) | sing-box → mihomo 的换装全记录 |
| [docs/RESEARCH_chain_proxy.md](docs/RESEARCH_chain_proxy.md) |主流客户端/主流客户端的链式代理实现分析，以及语言选型 |
| [docs/PROJECT_PROGRESS.md](docs/PROJECT_PROGRESS.md) | 开发进度与验收状态 |
| [docs/COMMIT_HISTORY.md](docs/COMMIT_HISTORY.md) | 每次提交的详细说明，含踩过的坑 |
| [docs/DEPLOY_reality_xhttp.md](docs/DEPLOY_reality_xhttp.md) | 在一台 NAT 服务器上部署 VLESS+Reality 的过程 |
| [docs/MIGRATION_wails_v3.md](docs/MIGRATION_wails_v3.md) | 迁移到 Wails v3 的技术验证 |
| [docs/NAMING_DECISION.md](docs/NAMING_DECISION.md) | 项目为什么叫 PicoHop，以及被否掉的候选与重名数据 |
| [refs/README.md](refs/README.md) | 参考项目的来源与 commit 登记 |

---

## 许可证

**GPL-3.0** —— [LICENSE](LICENSE) 是许可证全文。Copyright (C) 2026 murongruolan。

**为什么是 GPL-3.0 而不是 MIT**：出口由**内嵌的 mihomo** 承担
（`github.com/metacubex/mihomo`，GPL-3.0），它是被静态链接进这个二进制的。
把 GPL 代码链进自己的程序再分发，整个作品就受 GPL 约束 —— 所以这不是"挑一个更喜欢的
许可证"，而是唯一的合规选项。想换成宽松许可，就得换掉整个引擎，不现实。

代价说清楚：GPL 允许任何人 fork、修改、**甚至拿去卖**，只要他们也按 GPL 开源。
不希望这样的话，唯一的办法是不公开发布。

其余直接依赖都是宽松许可，不要求你开源任何东西：

| 依赖 | 许可证 |
|---|---|
| `github.com/wailsapp/wails/v3` | MIT |
| `golang.zx2c4.com/wireguard` | MIT |
| `github.com/makiuchi-d/gozxing` | MIT |
| `github.com/skip2/go-qrcode` | MIT |
| `golang.org/x/*` | BSD-3-Clause |

**对应源码哪里拿**：全部依赖的版本都钉在 `go.mod` / `go.sum` 里，按上面
[构建](#构建) 一节跑 `.\scripts\build.ps1`，就能从源码重建出与发布产物一致的二进制。
