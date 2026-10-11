# ChainProxy 链式代理

> 让浏览器**按需**走住宅 IP，而不用常开住宅代理。
>
> ChainProxy 把「前置代理」和「出口节点」拆成两个可独立选择的环节，
> 给每个节点开出一个**独立的本地端口**。浏览器或软件只要指定其中某个端口，
> 流量就是 `本地 →（前置）→ 出口节点 → 目标网站`。

---

## 为什么需要它

日常上网不需要住宅 IP，住宅 IP 又慢又贵；只有少数对 IP 敏感的网站才必须用。
但主流客户端/主流客户端一类客户端一次只能用一个出站，做不到"平时走机房 IP、
需要时才切住宅"。

而国内网络环境下，住宅 IP 往往**不能直连**，必须先经过一个非大陆 IP 的前置节点 ——
这就是"链式代理 / 前置代理"。

ChainProxy 做的事：**把前置和出口拆开，各自绑定一个本地端口**，让你按需选用。
不用的端口不占流量；不开 ChainProxy 就完全不影响正常上网。

---

## 两分钟上手

1. 启动你的前置代理（通常是主流客户端，监听 `127.0.0.1:10808`）。
2. 运行 `dist\chainproxy-cli.exe`。
3. 点右上角 **添加节点**：
   - 「本地代理端口」默认已经算好了（从 10810 起排），一般不用改；
   - 「前置节点」选 **全局前置代理**（走第 1 步那个主流客户端），不选就是直连；
   - 填服务器地址、端口、账号密码；
   - 点「添加」。
4. 在列表里**单击选中该节点，按回车激活**（或右键 →「激活节点」）。
5. 浏览器 / 软件里把代理设为 `127.0.0.1:<该节点的本地端口>`。

想确认链路真的生效：右键节点 →「检测所选出口 IP」，
如果显示的出口 IP 和前置的出口 IP **不一样**，就说明流量确实穿过了这个出口。

完整说明见 [`docs/USER_GUIDE.md`](docs/USER_GUIDE.md)。

---

## 功能

### 八种出口协议

| 协议 | 实现方式 | 本地端口支持 UDP |
|---|---|---|
| SOCKS5 | 自研（第二次握手套在前置隧道里） | ✅ |
| HTTP | 自研（CONNECT 隧道） | ❌（HTTP 代理本身不支持 UDP） |
| WireGuard | 自研（纯用户态，见下） | ❌ |
| VMess | 内嵌 sing-box | ✅ |
| VLESS（含 XTLS-Reality） | 内嵌 sing-box | ✅ |
| Shadowsocks | 内嵌 sing-box | ✅ |
| Trojan | 内嵌 sing-box | ✅ |
| Hysteria2 | 内嵌 sing-box（QUIC） | ✅ |

### 前置可以指向项目内的另一个节点

每个节点的「前置节点」有三种选法：

- **直连** —— 不经过任何前置；
- **全局前置代理** —— 走设置里那一个（典型用法是本机前置代理）；
- **项目内的另一个节点** —— 用那个节点的本地端口当 SOCKS5 前置，
  于是形成 `节点 A → 节点 B` 的链路。

选第三种时激活外层节点会**自动先把前置节点拉起来**（按依赖顺序），
并且会检测环路。删除被依赖的节点时，引用它的节点会自动回退成直连并给出提示。

### 界面

- **列表默认 8 列**：类型、别名、本地端口、地址、远程端口、传输协议、延迟、速度。
  其余列（今日统计、最近使用、创建时间、备注、出口 IP、前置、TLS、活动连接、累计连接）
  通过「列显示」按需打开，勾选结果会记住。
- **别名模糊搜索**（失焦或回车执行）+ **状态筛选**（全部 / 激活 / 未激活）。
- **单击选中、Ctrl 加选、Shift 范围选；回车激活；双击编辑。**
- **「添加 / 编辑节点」开在独立的置顶窗口里**，可以拖到主窗口之外；关窗收进托盘、快捷键 `Ctrl+Alt+C` 显隐窗口（详见[使用手册](docs/USER_GUIDE.md)）。
- **右键菜单**：激活/停止、编辑、克隆此项、复制本地代理地址、测试 TCP 延迟、
  测试真连接延迟、测试速度、检测出口 IP、停止当前测试、分享、清零今日统计、
  移除所选、移除测试无效项。
- **通知**：primary / success / warning / info / error 五种类型，
  出现在右上角控制栏下方，支持深色主题。
- **今日统计**：双向流量（上传+下载），最小单位 KB，自动换算到 MB/GB/TB/PB。

### 导入 / 分享

- **从剪贴板导入**：取剪贴板最新一条，自动识别 `vless://` `vmess://` `ss://`
  `trojan://` `hysteria2://`（含 `hy2://`）五类分享链接，支持一次多条。
- **从屏幕扫描二维码**：点一下 → 主界面暂时隐藏 → 屏幕上出现遮罩、鼠标变成**红色十字准星**
  → 按住左键拖拽圈选二维码 → 松开后识别 → 解析成节点。
- **识别后不会直接入库**：先弹一个**只问本地端口**的小窗口，
  默认值、随机（随机到空闲端口才覆盖）、占用检测都在那一步完成。
- **分享**：上方正方形二维码、中间协议文本、下方「下载图片 / 复制文本」。

### 端口规则

- 新增节点默认端口 = `本项目内所有 10800+ 端口的最大值 + 1`，从 10810 起排。
- **随机**按钮：从大于 10000 的合法端口里随机，**抽到之后先检测占用**，
  被占用就重新抽，最多 5 次；仍失败会明确提示「多次随机仍未随机到空闲端口」。
- 手工输入端口**失焦即检测**，被占用时红字提示（并说明是被本项目哪个节点占的）。
- 激活时再检测一次，被占用会拒绝激活并说明原因。

### 其它

- **本地端口协议是 mixed** —— 同一个端口同时接受 SOCKS5 与 HTTP 客户端。
- **默认只监听 `127.0.0.1`**，不会触发 Windows 防火墙提示；
  需要局域网共享时在设置里打开「允许局域网连接」（此时建议给本地端口加鉴权）。
- **不注册开机自启动** —— 不写注册表、不建计划任务、不装服务。
- **纯用户态 WireGuard**：不需要装驱动、不需要管理员权限、不改系统路由表；
  外层 UDP 可以经前置代理的 SOCKS5 UDP ASSOCIATE 送出（即"WG 跑在前置里面"）。

---

## 端口生命周期

| 时机 | 行为 |
|---|---|
| 程序启动 | 按配置自动激活上次处于激活状态的节点（可在设置里关闭） |
| 节点未激活 | 不占用端口 |
| 点「全部停止」/ 退出程序 | 立即释放全部端口并断开所有进行中的连接 |
| 端口被别的程序占用 | 该节点标记为错误并说明是哪个端口，其它节点照常工作 |
| 程序崩溃 | 监听端口由操作系统自动回收 |

---

## 构建

```powershell
.\scripts\build.ps1                 # 图形界面 + 命令行，产物在 dist\
.\scripts\build.ps1 -Cli            # 只构建命令行版（快）
.\scripts\build.ps1 -SkipFrontend   # 前端没改时跳过 npm 构建
```

产物：

- `dist\ChainProxy.exe` —— 图形界面（Wails v3 + Vue 3），**双击运行**
- `dist\chainproxy-cli.exe` —— 命令行（排障与自动化测试）

> 两者文件名**不能**只靠大小写区分：NTFS 不区分文件名大小写，
> `ChainProxy.exe` 与 `chainproxy.exe` 是同一个文件 —— 早先就因为这样，
> 图形界面版把命令行版静默覆盖了（表现是"想用命令行却弹出界面"）。

图形界面版的构建**不使用 `wails3 build`**：那条链是 Taskfile 驱动的，
需要额外装 `task`，而它本质就是"前端 `npm run build` → `go build -tags production,...`"。
`scripts/build.ps1` 直接调这条 `go build`，于是不依赖 wails3 CLI 与 task。
注意其中的 **`production` 标签不能漏**（v3 用它区分 production/dev 两套实现文件）。

除此之外构建**必须**带 `-tags "with_utls,with_quic"`（脚本里已写死）。
缺了它编译照样通过，但运行时 Reality / Hysteria2 会失败 —— 所以命令行里
专门有一个子命令帮你确认：

```powershell
chainproxy engines      # 检查内嵌引擎的构建标签是否齐全
```

所有 Go 模块缓存 / 编译缓存 / 前端依赖都重定向在 `F:\chain_proxy\.cache\` 与
`desktop\frontend\node_modules\`，**不会往项目目录之外写任何东西**。

---

## 命令行（排障与自动化）

```powershell
chainproxy                     # 等价于 ls
chainproxy ls                  # 列出节点与运行状态
chainproxy run                 # 启动节点并前台运行（Ctrl+C 退出）
chainproxy run -seconds 30     # 跑 30 秒后自动退出（脚本里用）
chainproxy test -kind real     # 测试：tcp / real / speed / exit
chainproxy fetch               # 探测前置与各节点的出口 IP（判断链路是否生效）
chainproxy validate            # 只校验配置，不启动任何服务
chainproxy import "<链接>"     # 从分享链接导入节点
chainproxy express <节点>      # 导出节点的分享链接
chainproxy engines             # 检查内嵌引擎的构建标签
chainproxy wgkey               # 生成 WireGuard 密钥对
```

常用参数：`-config <路径>`、`-node <ID或别名>`。

---

## 测试

```powershell
.\scripts\lint.ps1          # gofmt + go vet + 配置校验 + 引擎自检
go test -tags "with_utls,with_quic" ./...
.\scripts\test_e2e.ps1      # 端到端：经本地端口用 SOCKS5/HTTP 取出口 IP 并对比前置
.\scripts\test_wg.ps1       # WireGuard 出口专项验证
.\scripts\ui_shot.ps1       # 界面截图（开发自测用）
```

---

## 文档

| 文档 | 内容 |
|---|---|
| [`docs/USER_GUIDE.md`](docs/USER_GUIDE.md) | **使用手册与故障排查（先看这个）** |
| [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) | 架构设计、模块边界、关键取舍 |
| [`docs/RESEARCH_chain_proxy.md`](docs/RESEARCH_chain_proxy.md) |主流客户端/主流客户端链式代理实现分析 + 语言选型 |
| [`docs/PROJECT_PROGRESS.md`](docs/PROJECT_PROGRESS.md) | 开发进度与验收状态 |
| [`docs/COMMIT_HISTORY.md`](docs/COMMIT_HISTORY.md) | **每次提交的详细说明（逐条展开，含所有踩过的坑）** |
| [`docs/DEPLOY_reality_xhttp.md`](docs/DEPLOY_reality_xhttp.md) | 在一台 NAT 服务器上部署 VLESS+Reality 的完整过程 |
| [`docs/MIGRATION_wails_v3.md`](docs/MIGRATION_wails_v3.md) | **迁移到 Wails v3 的技术验证与方案**（已实测：裸 go build、托盘、多窗口、通知） |
| [`refs/README.md`](refs/README.md) | 参考项目拉取记录 |

---

## 安全提醒

- 分享链接里包含节点的**全部连接凭据**，只分享给自己信任的人。
- 本项目的配置文件 `data\config.json` 明文保存账号密码与密钥。
  它已被 `.gitignore` 排除，请勿把它提交到任何仓库或发给别人。
