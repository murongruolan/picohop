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