# 命名决策记录（NAMING_DECISION）

> 2026-10-13。项目开发期一直叫 ChainProxy（**临时名**，先有个能编译的东西用着），
> 这一天定名 **PicoHop**。本文留下决策过程、被否掉的候选和当时的重名数据，
> 以后给别的东西起名时可以直接复用第 2、6 节的方法。
>
> 旧名与旧文件名的对应关系见 [`COMMIT_HISTORY.md`](COMMIT_HISTORY.md) 顶部的改名说明。

---

## 1. 结果

| 项 | 值 |
|---|---|
| 名字 | **PicoHop**（界面与文档里的写法）；小写 `picohop` 用于 module、包名、文件名 |
| 释义 | **Next-hop Node in a Pico Container** —— 微型容器里的下一跳节点 |
| Go module | `github.com/murongruolan/picohop` |
| 产物 | `PicoHop.exe`（图形界面）/ `picohop-cli.exe`（命令行） |
| 运行时 | `data\config.json`、`data\picohop.log`；环境变量前缀 `PICOHOP_` |
| 界面 | 窗口标题 `PicoHop · 链式代理`；托盘菜单「退出 PicoHop」 |

---

## 2. 名字要同时满足什么（硬约束）

名字不只是一个标题，它同时是文件名、路径片段和环境变量前缀：

| 位置 | 形态 | 例子 |
|---|---|---|
| 窗口标题 | `<名> · 链式代理` | `PicoHop · 链式代理` |
| 产物文件名 | `<名>.exe` / `<小写>-cli.exe` | `PicoHop.exe` / `picohop-cli.exe` |
| Go module | `github.com/<账号>/<小写>` | `github.com/murongruolan/picohop` |
| 日志文件 | `data\<小写>.log` | `data\picohop.log` |
| 环境变量 | `<大写>_DATA_DIR` 等 | `PICOHOP_DATA_DIR` |
| 前端 | package name、页面 `<title>` | `picohop-frontend` |

由此推出 5 条**不能让步的**：

1. **纯 ASCII 字母**，无空格、无连字符、无点 —— 连字符会让 exe 名、日志名、
   module 路径到处都是横杠，且不好念。
2. **5–10 个字母**。太短容易撞名，太长每天都要打。
3. **要能全大写当环境变量前缀**，所以不能数字开头、不能用怪缩写。
4. **两个 exe 不能只靠大小写区分** —— NTFS 不区分文件名大小写，`X.exe` 与 `x.exe`
   是同一个文件（早先图形版就这样静默覆盖过命令行版），所以命令行版永远是 `<名>-cli.exe`。
5. **在 GitHub 搜索里要搜得到** —— 见第 6 节的自查方法。

---

## 3. 想要的与不要的意象

**想要的**（按优先级）：

1. 链、逐跳、接力 —— 前置 → 出口 → 目标，本质是"在一条已建立的连接里再发起一次连接"。
2. 一个出口一个位子 —— 本项目最独特的形态（一节点一本地端口），
   但**不要把 `port` 这个词写进名字里**，太直白，像工具类库。
3. 分流、各走各的 —— 住宅与普通站点同时在线互不干扰。

**不要的**：

1. 像 Netflix / 流媒体解锁工具（招来错误联想，方向本身也敏感）。
2. 把 `port` 写进名字。
3. 撞已有的响名字：Flux CD（CNCF 的 GitOps 工具）、以及任何 `-Gate` / `-Lane` 大户。
4. 通用的 AI / 创业公司名：`Nex-`、`Nova-`、`Aura-`、`-ly`、`-ify`。
5. 把产品说大的词：Platform、Suite、Enterprise、Accelerator、Turbo。

---

## 4. 被否掉的候选

重名数据为 2026-10-13 用 GitHub `in:name` 查的结果（仓库名里含该词的数量）：

| 候选 | 命中 | 否掉的原因 |
|---|---|---|
| NexFlux | 11 | 读起来像 Netflix（Nex**Flux** ≈ Net**Flix**）；`appuzlotatheog/Nexflux` 占着裸名，还挂着线上站点 |
| NexHop | 14 | 语义其实最好（下一跳），但标准拼写 `nexthop` 有 **88** 个同名，其中一个是 61 star、自称 "hop by hop" 的网络监测工具；别人会打成 `nexthop`，搜索引擎也会纠正过去 |
| Npiko | 0 | 唯一性满分，但 `Np-` 不是英语合法词首、也不是汉语拼音的声母组合，念不出来（只能说"字母 N 加 piko"）；且 `piko` 子串有 **1783** 个同名，头名 5632 star 且仍在活跃更新 |
| FluxGate | **115** | `OpenFluxGate/fluxgate` 连组织名都被占了 |
| FluxLane | 11 | `ri-dev-it/Fluxlane` |
| FluxHarbor | 2 | `shadowdames/FluxHarbor`（TypeScript，GPL-2.0） |
| FluxBay | 1 | 仓库只 1 个，但 `fluxbay` 组织名已被占用 |
| NodeHop | 7 | `ParinTF/NodeHop`（TypeScript，已停更） |
| PicoRelay | 5 | `fcorthay/PicoRelay` 是树莓派 Pico 2 控制的继电器 —— 暴露了 `Pico*` 的硬件联想 |
| PicoNode | 6 | `RaccoonLabHardware/picoNODE`，同上 |
| FluxHop | 0 | 无重名、也念得出，但 `flux`（流）对本项目是空词，只承载"好看" |
| HopNode | 0 | 无重名、也念得出，但丢掉了"微型 / 自包含"那半句语义。**它是 PicoHop 的次选** |

---

## 5. 为什么最后是 PicoHop

- **命中 0**：搜全名第一条就是自己。
- **念得出、拼得出**：三个音节，`Pico` 与 `Hop` 都是常见词。这是 Npiko 最致命的短板
  （`Np-` 无法发音），PicoHop 完全没有这个问题。
- **保住了原案 Npiko 的两个词素**：`pico`（微型、自包含 —— 正对应"单文件、不装外部内核"
  这条真实属性）与 `hop`（下一跳 —— 链式代理的本质），只是把念不出来的开头那个 `N` 挪走。
- **要接受的代价**：`Pico` 的第一联想是树莓派 Pico，国内还多一层 Pico VR 头显。
  整串 0 命中意味着**搜全名不受影响**，但偶尔会有人先想到硬件。
- 那句释义 `Next-hop Node in a Pico Container` 不塞进名字里，而是当**副标题**用：
  名字负责被记住和被搜到，释义负责让第一次看到的人两秒内明白。

---

## 6. 以后复查重名的方法

```
https://api.github.com/search/repositories?q=<name>+in:name&per_page=1
```

看返回 JSON 里的 `total_count`：**0–2 比较理想，两位数基本等于搜不到自己**。
注意这只覆盖 GitHub 仓库名；域名、商标、npm/PyPI 包名不在内，
真要发布前应当另外核一遍。
