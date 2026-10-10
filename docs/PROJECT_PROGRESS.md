# 开发进度

> 最后更新：2026-10-10
> 图例：✅ 已完成 ｜ 🔄 进行中 ｜ ⬜ 未开始 ｜ ⚠️ 有已知问题

---

## 总体状态

**阶段**：核心实现中

---

## 任务清单

### 任务 0：环境与规范 ✅

| 项 | 状态 | 说明 |
|---|---|---|
| 工具链勘察 | ✅ | git 2.48.1 / Go 1.24.0 / Python 3.10.11 / curl 可用 |
|主流客户端端口验证 | ✅ | `127.0.0.1:10808` mixed 协议正常，SOCKS5 与 HTTP 两种方式均返回出口 IP `103.115.108.31` |
| 项目目录规划 | ✅ | 见 `docs/ARCHITECTURE.md` 第 7 节 |
| 本地 git 仓库 | ✅ | `F:\chain_proxy\.git`，分支 `main`，身份 `chainproxy-dev` |
| 依赖隔离 | ✅ | `scripts/env.ps1` 把 Go 全部缓存重定向到 `.cache/` |
| 文档骨架 | ✅ | README / RESEARCH / ARCHITECTURE / PROGRESS / COMMIT_HISTORY |

### 任务 1：参考项目研究 ✅

| 项 | 状态 | 说明 |
|---|---|---|
| 拉取主流客户端| ✅ | `refs/local-refs`，HEAD `b501efb` |
| 拉取主流客户端| ✅ | `refs/local-refs`，HEAD `44477d9` |
|主流客户端链式实现分析 | ✅ | `主流客户端OutboundService.BuildChainOutboundsList` + `FillDialerProxy` |
| sing-box 变体分析 | ✅ | `SingboxOutboundService.BuildChainOutboundsList`，字段名 `detour` |
|主流客户端链式实现分析 | ✅ | `config/runtime.rs::update_proxy_chain_config`，字段 `dialer-proxy` |
| 语言选型 | ✅ | 选 Go，理由见研究报告 4.3 |
| 研究报告 | ✅ | `docs/RESEARCH_chain_proxy.md` |

### 任务 2：核心协议实现 ⬜

| 项 | 状态 | 说明 |
|---|---|---|
| 配置结构 + 持久化 | ⬜ | `internal/config` |
| 前置代理 `Front` 抽象 | ⬜ | socks5 / http / direct 三种实现 |
| 住宅上游 `Upstream` 抽象 | ⬜ | socks5 / http 两种实现 |
| Mixed 监听器 | ⬜ | 首字节嗅探，同时服务 SOCKS5 + HTTP |
| SOCKS5 服务端 | ⬜ | greeting / auth / CONNECT / UDP ASSOCIATE |
| HTTP 服务端 | ⬜ | CONNECT + 绝对 URI + keep-alive |
| 双向中继 + 半关闭 | ⬜ | `Relay()` |
| 端口管理 | ⬜ | 重复校验 / 占用探测 / 启动占用 / 停止释放 |
| 单实例锁 | ⬜ | PID 文件 |

### 任务 3：应用状态机与测试 ⬜

| 项 | 状态 | 说明 |
|---|---|---|
| `app` 启停状态机 | ⬜ | |
| 分层一键测试 | ⬜ | 前置出口 IP / 链路出口 IP / 延迟 |
| CLI 子命令 | ⬜ | `run` / `test` / `validate` |

### 任务 4：GUI ⬜

| 项 | 状态 | 说明 |
|---|---|---|
| 主窗口 | ⬜ | Win32 原生 |
| 前置代理设置区 | ⬜ | |
| 住宅代理列表 | ⬜ | 增删改、启停、排序 |
| 添加/编辑对话框 | ⬜ | 含 WireGuard 字段 |
| 状态展示与主开关 | ⬜ | |
| 一键测试面板 | ⬜ | |

### 任务 5：WireGuard 上游 ⬜

| 项 | 状态 | 说明 |
|---|---|---|
| wireguard-go + netstack | ⬜ | |
| SOCKS5 UDP ASSOCIATE 外层 | ⬜ | 自研 Bind |
| 配置项与 GUI 字段 | ⬜ | |

### 任务 6：端到端验证 ⬜

| 项 | 状态 | 说明 |
|---|---|---|
| 住宅代理 SOCKS5 链路实测 | ⬜ | 目标：`fuckoai_2026-zone-custom-region-US:HVu78BgQcny7@hk.rrp.bestgo.work:10000` |
| 住宅代理 HTTP 链路实测 | ⬜ | |
| 端口释放验证 | ⬜ | |
| 多上游并存验证 | ⬜ | |
| 不自启动验证 | ⬜ | 确认无注册表 Run 键 |

---

## 验收标准（"睡醒希望看到成品"的判定）

1. `dist\chainproxy.exe` 双击可运行，打开原生窗口，无控制台残留；
2. 前置 `127.0.0.1:10808`、住宅 `hk.rrp.bestgo.work:10000` → 本地端口 `10810`；
3. `curl --socks5-hostname 127.0.0.1:10810 https://api.ipify.org` 返回**与前置不同的 IP**；
4. `curl -x http://127.0.0.1:10810 https://api.ipify.org` 同样可用（mixed 协议）；
5. 关闭主开关 → `10810` 立即不可连接；重新开启 → 立即可连接；
6. 退出程序 → `10810` 端口释放，再启动可立即绑定；
7. 添加第二条上游用 `10811`，两条互不影响；
8. GUI 中一键测试能同时显示前置出口 IP 与链路出口 IP，并明确提示"链式代理生效"；
9. `git log` 有完整的分步提交，且每次提交都在 `docs/COMMIT_HISTORY.md` 中有详细说明；
10. 项目目录之外无任何新增文件/缓存。

---

## 风险与对策

| 风险 | 影响 | 对策 |
|---|---|---|
| 住宅代理是动态 IP，部分请求会失败 | 测试偶发失败 | 报告中明确区分"链路错"与"上游偶发失败"；测试内置重试 |
| 住宅代理本身支持 SOCKS5，但不支持"被嵌套" | 链路不通 | 部分厂商的 SOCKS5 只允许 TCP 透传，理论上兼容；实测为准 |
|主流客户端socks 入站未开启 UDP | WireGuard 模式不可用 | WireGuard 明确报错提示"前置代理需支持 UDP" |
| gvisor netstack 体积使 exe 变大 | 分发 | wireguard-go/netstack 为纯 Go，静态编译，无外部 DLL |
| Win32 GUI 代码量大 | 进度 | 先保证核心 + CLI 可用，GUI 独立成一次提交 |