# sing-box（现状） vs mihomo A/B 性能对比

节点：`node-vmess-hk`（VMess / tcp / 无 TLS）｜本机：Windows 11 25H2，Ryzen 5 9600X（6C/12T），31.8 GB RAM，Wi-Fi AX210
A = `dist\chainproxy-cli.exe`（内嵌 sing-box v1.14.3，1 个进程 = 1 个内核实例 + 1 个本地 mixed 口 127.0.0.1:10813）
B = `.cache\mihomo.exe`（Mihomo Meta，1 个进程 = 1 个引擎，mixed-port 127.0.0.1:10888）

## 0. 先证明"真的走了这个节点"

| 路径 | 出口 IP | 地区 | ASN |
|---|---|---|---|
| 直连（无代理，基线） | 203.0.113.1 | Hebei / CN | AS4134 CHINANET |
| A 经 socks5h://127.0.0.1:10813 | **203.0.113.13** | Tung Chung, Islands / **HK** | AS42960 VH Global |
| B 经 socks5h://127.0.0.1:10888 | **203.0.113.13** | Tung Chung, Islands / **HK** | AS42960 VH Global |

两边出口 IP 相同、且都是香港 → 两边都确实走了该节点，且可比。

## 1. 对比表（中位数）

| 指标 | sing-box | mihomo | 差异（A 相对 B） |
|---|---|---|---|
| 建连 TTFB 中位数（gstatic/204，池化 n=18） | **514.4 ms** | **500.1 ms** | +14.3 ms（+2.9%） |
| 建连 TTFB 均值 / min / max（ms） | 538.2 / 485.3 / 867.6 | 521.8 / 484.4 / 718.0 | — |
| TTFB 标准差（ms） | 89.9 | 60.2 | 抖动更大 |
| 本地口 TCP connect 中位数 | 0.69 ms | 0.60 ms | — |
| 真连接延迟（api.ipify.org，`test -kind real`，n=5） | **755 ms** | 762 ms（同 URL curl TTFB） | −0.9% |
| 吞吐 30 MB 中位数 | 0.613 MB/s | **0.690 MB/s** | −11.2% |
| 吞吐 5 MB 中位数 | 0.266 MB/s | 0.252 MB/s | +5.6% |
| 吞吐 10 s 定窗口中位数 | 0.174 MB/s | 0.141 MB/s | +23.4% |
| 同轮配对速率比 A/B 中位数（30 MB / 5 MB / 10 s） | 0.958 / 1.196 / 1.306 | — | 方向不一致 |
| 下载期 CPU（占单核） | 1.267% | 0.972% | +0.30 pp |
| 下载期 CPU 秒 / MB | 0.01720 | 0.01584 | +8.6% |
| 空闲 CPU | 0%（低于计时精度） | 0%（同上） | — |
| 空闲 WorkingSet | 27.64 MB | 23.81 MB | +3.8 MB（+16.1%） |
| 空闲 Private | 23.05 MB | 26.39 MB | −3.3 MB（−12.7%） |
| 峰值 WorkingSet（30 MB）中位 / max | 27.24 / 28.25 MB | 24.60 / 24.86 MB | +2.6 MB |
| 峰值 Private（30 MB）中位 / max | 22.30 / 23.43 MB | 26.86 / 27.13 MB | −4.6 MB |
| 峰值 WorkingSet（10 s 窗口）中位 / max | 30.04 / 30.12 MB | 24.92 / 25.00 MB | +5.1 MB |
| 成功率 | 延迟 23/23，吞吐 23/23 | 延迟 23/23，吞吐 24/24 | 内核归因失败 0 |
| 进程数 / 内核实例数 | 1 / 1 | 1 / 1 | — |

### 1.1 延迟原始逐轮（`%{time_starttransfer}`，ms）

30 MB 档（main，6 轮，A/B 顺序逐轮翻转）：

| 轮 | 顺序 | sing-box | mihomo |
|---|---|---|---|
| 1 | A,B | 571.9 | 573.1 |
| 2 | B,A | 497.8 | 539.3 |
| 3 | A,B | 492.7 | 503.2 |
| 4 | B,A | 583.6 | 519.6 |
| 5 | A,B | 537.3 | 488.8 |
| 6 | B,A | 508.2 | 503.4 |
| **中位** | | **522.8** | **511.5** |

5 MB 档（small，12 轮）：sing-box 中位 **514.4**（min 485.3 / max 867.6），mihomo 中位 **493.7**（min 484.4 / max 718.0）。

### 1.2 真连接延迟 vs 用户的 732 ms

| 测量 | 值 |
|---|---|
| 用户应用内测（参考） | 732 ms |
| 本次 `chainproxy-cli test -kind real`（用户原 realTestUrl = api.ipify.org，n=5） | 752 / 755 / 756 / 753 / 768 → **中位 755 ms** |
| 同 URL 经 A 的 curl TTFB（n=5） | 758 / 764 / 750 / 739 / 763 → 中位 758 ms |
| 同 URL 经 B 的 curl TTFB（n=5） | 736 / 774 / 762 / 761 / 741 → 中位 762 ms |
| `test -kind real` 换 gstatic/204（n=5） | 512 / 504 / 582 / 501 / 543 → 中位 512 ms |
| gstatic 经 A 的 curl TTFB（n=5） | 505 / 496 / 496 / 530 / 520 → 中位 505 ms |
| gstatic 经 B 的 curl TTFB（n=5） | 484 / 488 / 502 / 501 / 500 → 中位 500 ms |
| TCP 直连握手（不经代理，n=5） | 57 ms（恒定） |

755 ms vs 用户 732 ms：同一量级（+3.1%）→ 路径正确。同一 URL 下 CLI 真连接 755 ms 与经 A 的 curl TTFB 758 ms 互证。

### 1.3 吞吐原始逐轮

30 MB 档（main）：

| 轮 | sing-box MB/s | mihomo MB/s | 配对比 A/B |
|---|---|---|---|
| 1 | 0.902 | 1.012 | 0.90 |
| 2 | 0.507 | 0.815 | 0.62 |
| 3 | 0.499 | 1.345 | 0.37 |
| 4 | 0.885 | 0.243 | 3.66 |
| 5 | 0.529 | 0.514 | 1.02 |
| 6 | 0.698 | 0.563 | 1.24 |
| **中位** | **0.613** | **0.690** | **0.958** |

5 MB 档（small，12 轮）sing-box 中位 **0.266**、mihomo 中位 **0.252**，配对比中位 **1.196**。
10 s 定窗口（有效 A=5 / B=6）sing-box 中位 **0.174**、mihomo 中位 **0.141**，配对比中位 **1.306**。

参考：同一时段直连（无代理）10 MB 探测 = 0.29 ~ 2.14 MB/s，波动比两边差异还大。

### 1.4 CPU / 内存原始逐轮（30 MB 档）

| 轮 | 引擎 | 墙钟 s | CPU 秒 | 占单核 % | WS 峰值 MB | Private 峰值 MB |
|---|---|---|---|---|---|---|
| 1 | sing-box | 32.2 | 0.4375 | 1.361 | 27.93 | 23.16 |
| 1 | mihomo | 28.8 | 0.2812 | 0.975 | 24.42 | 26.76 |
| 2 | mihomo | 35.5 | 0.3438 | 0.968 | 24.61 | 26.89 |
| 2 | sing-box | 56.3 | 0.5469 | 0.972 | 26.99 | 22.00 |
| 3 | sing-box | 57.6 | 0.8438 | 1.465 | 27.27 | 22.34 |
| 3 | mihomo | 21.6 | 0.4531 | 2.097 | 24.70 | 26.99 |
| 4 | mihomo | 118.1 | 0.6719 | 0.569 | 24.86 | 27.13 |
| 4 | sing-box | 32.6 | 0.4062 | 1.248 | 27.12 | 22.25 |
| 5 | sing-box | 54.7 | 0.7031 | 1.286 | 28.25 | 23.43 |
| 5 | mihomo | 55.9 | 0.7188 | 1.285 | 24.34 | 26.56 |
| 6 | mihomo | 50.9 | 0.4531 | 0.890 | 24.59 | 26.82 |
| 6 | sing-box | 40.9 | 0.3438 | 0.840 | 27.20 | 22.18 |

空闲（20 s 采样）：sing-box WS 27.64→27.71 MB / Private 23.05→23.13 MB / CPU 0%；mihomo WS 23.81 MB / Private 26.39 MB / CPU 0%。

### 1.5 失败样本

| 序列 | 轮 | 引擎 | HTTP | 说明 |
|---|---|---|---|---|
| window | 6 | sing-box | 429 | speed.cloudflare.com 限流 |
| window | 7 | sing-box | 429 | 同上 |
| window | 7 | mihomo | 429 | 同上 |
| window | 8 | mihomo | 429 | 同上 |
| window | 8 | sing-box | 429 | 同上 |

全部失败都是 Cloudflare 返回 429（且同时打中两边），非内核问题。另有一次探测用 `bytes=104857600`（100 MB）被 Cloudflare 直接 403、且直连亦 403 → 该档位不可用，未使用。

## 2. 环境与命令

**环境**
- CPU：AMD Ryzen 5 9600X 6-Core，6 核 / 12 逻辑核，3900 MHz
- 内存：TotalVisibleMemorySize = 32,581,432 KB（31,818 MB ≈ 31.1 GiB），测试开始时可用 ~7.4 GB
- OS：Microsoft Windows 11 专业版，10.0.26200，Build 26200，25H2，64 位
- 网络：Wi-Fi（Intel Wi-Fi 6E AX210 160MHz，链路 520 Mbps），默认接口 `WLAN 2`，网关 192.168.31.1
- A 二进制：`F:\chain_proxy\dist\chainproxy-cli.exe` 42,875,904 B（40.89 MB），Go 1.25.5，内嵌 sing-box v1.14.3（go.mod），构建标签 with_utls / with_quic
- B 二进制：`F:\chain_proxy\.cache\mihomo.exe` 60,488,704 B（57.69 MB），Mihomo Meta，go1.24.0，tags `with_gvisor`，`CGO_ENABLED=0 -trimpath -s -w`

**实际执行的命令（省略节点 uuid 等凭据）**

```powershell
# 准备（只动 .cache 下的副本，data\config.json 未作为输入被改写）
Copy-Item data\config.json .cache\ab\sing-config.json   # 再用 PowerShell 把仅目标节点 active 置真、front 置直连

# B 校验 / 启动
.cache\mihomo.exe -d F:\chain_proxy\.cache\ab\mihomo-home -f F:\chain_proxy\.cache\ab\mihomo.yaml -t
.cache\mihomo.exe -d F:\chain_proxy\.cache\ab\mihomo-home -f F:\chain_proxy\.cache\ab\mihomo.yaml

# A 启动（单节点，无 GUI）
dist\chainproxy-cli.exe -config F:\chain_proxy\.cache\ab\sing-config.json run -node <nodeID>

# 出口证明
curl.exe --noproxy "" -x socks5h://127.0.0.1:10813 -s --max-time 30 https://ipinfo.io/json
curl.exe --noproxy "" -x socks5h://127.0.0.1:10888 -s --max-time 30 https://ipinfo.io/json

# 延迟（协议无关，逐轮交替）
curl.exe -s -o NUL --max-time 60 -w "%{time_connect} %{time_starttransfer} %{time_total} %{http_code}" `
  -x socks5h://127.0.0.1:<port> https://www.gstatic.com/generate_204

# 吞吐 30 MB（A/B 顺序逐轮翻转，共 6 轮 + 1 轮预热）
curl.exe -s -o NUL --max-time 600 -w "%{speed_download}|%{time_total}|%{size_download}|%{http_code}" `
  -x socks5h://127.0.0.1:<port> "https://speed.cloudflare.com/__down?bytes=30000000"

# 我们的真连接延迟（从独立工作目录运行，避免与常驻实例抢 sing-box cache-file）
cd .cache\ab\realwd
F:\chain_proxy\dist\chainproxy-cli.exe -config F:\chain_proxy\.cache\ab\sing-config.json test -node <nodeID> -kind real

# CPU / 内存采样
Get-Process -Id <pid> | Select-Object CPU, WorkingSet64, PrivateMemorySize64   # 下载期间每 300~400 ms 一次

# 收尾
Stop-Process -Id <A pid>,<B pid> -Force
```

采样脚本与全部原始数据保存在 `.cache\ab\`（`results-main.csv` / `results-small.csv` / `results-window.csv` / `measure-*.log` / `realping.log` / `ab-measure.ps1` / `fixed-window.ps1` / `final-numbers.ps1`）。

## 3. 公平性与局限（哪些口径不一致、哪些没测到）

**口径不一致的地方**
1. **A 不是"裸 sing-box"**。A 是 `chainproxy-cli.exe` 整进程：内嵌 1 个只带 outbound 的 sing-box 实例（无 inbound、无 router、无 DNS 模块）+ 我们自己的 mixed 监听口（SOCKS5/HTTP 首字节分派 + 中转 + 统计 + 网络日志）。B 是完整 mihomo 引擎（inbound 监听、规则匹配、DNS 解析器、geodata）。所以这是"我方完整本地路径 vs mihomo 完整本地路径"，不是"sing-box 内核 vs mihomo 内核"的裸内核对比。
2. **B 的 CPU 偏悲观**：mihomo 用了其默认 `find-process-mode: strict`，每条连接会做进程名匹配，sing-box 不做这件事。若要公平比内核效率，B 应设 `find-process-mode: off`，本次未测。
3. **吞吐口径受 WAN 主导**：两边都是单流单连接 curl，链路是"中国→香港节点→Cloudflare"。测得的波动（30 MB 档 0.243~1.345 MB/s，σ 最大 0.398）远大于两边差异；三个序列给出的方向互相矛盾（A 慢 11%／A 快 6%／A 快 23%），配对比中位 0.958 / 1.196 / 1.306。**吞吐一项无法把两个内核区分开**。
4. **无线链路**：全程走 Wi-Fi（AX210），未用有线，抖动包含无线因素。
5. **DNS**：两边都做远端解析（mihomo `dns.enable: false`，TCP 走 remote resolve；我方把域名原样交给 sing-box outbound），口径一致。
6. **CPU 采样分辨率**：Windows `Get-Process` 的 CPU 累计值粒度约 15.6 ms。30 MB 档每轮 CPU 仅 0.28~0.84 s，量化误差可接受；5 MB 档（0.016~0.20 s）误差偏大；10 s 窗口档 B 侧只有 0.0078 s（约 0.5 个 tick），该档 CPU 不可用。
7. **mihomo 配置为最小手写**：仅 `mixed-port / mode: rule / rules: [MATCH,节点] / dns.enable: false / external-controller: ""`，其余走默认值（含默认 geodata 加载）。不是用户实际生产配置；A 侧用的是用户真实节点参数但复制后的配置副本。
8. **未做多实例**：本次是单节点，两边都是"1 进程 = 1 实例"。我们的架构支持一进程多实例，但未测多实例口径。
9. **我方 realping 与 curl 的目标 URL 不同**：`test -kind real` 默认打 `api.ipify.org`，与 curl 的 `gstatic/generate_204` 不是同一 URL，因此单列一行；另有把 realTestUrl 换成 gstatic 的一组，用于与 curl 对齐。

**没测到 / 未覆盖**
- **mihomo 侧没有等价的"真连接延迟"原生测量**：只能用 curl TTFB 近似，没有 mihomo 自带测速器的读数。
- **UDP / gVisor / TUN 完全未测**（本次只测 TCP 代理转发）。
- **多节点、多实例、并发多连接、P2P/视频等场景未测**。
- **长稳（长时运行内存增长、GC、连接泄漏）未测**：只测了空闲与单次下载峰值。
- **握手/协议效率（VMess 加解密、复用、mux）未单独隔离测量**：本次只给出端到端结果。
- **mihomo 端口清单未完整枚举**：只确认 10888（mixed）在监听，`external-controller` 已显式置空。
- **`bytes=104857600` 档位不可用**（Cloudflare 403，直连亦然），故 100 MB 档未测；10 s 窗口档因 Cloudflare 429 只有 A=5 / B=6 个有效样本。
- **1 次 867.6 ms 的 A 侧延迟离群值**（small 档第 3 轮）保留在样本里，未剔除。

**过程中的一次意外（如实记录）**
首次探测 CLI 帮助时执行了 `chainproxy-cli.exe run --help`，该 CLI 把 `--help` 当普通参数、子命令仍按 `run` 执行，于是走了一次 `AutoActivate()` 并 `Save()`，导致 `data\config.json` 在 10:59:25 被重写一次。核对结果：该文件内容语义未变（active 节点仍只有 `住宅代理1`，front 仍为 socks5 127.0.0.1:10808，`realTestUrl` 仍为 api.ipify.org，其余节点仍为未激活），且用户 GUI 进程（PID 46408，10:53:24 启动）全程持有 10810，经 10810 复测 HTTP 200 正常。**无法排除**的是该文件被重新序列化（键顺序/缩进可能变化）。此外该次运行在仓库根目录留下了 `cache.db`，已删除。`data\config.json` 未被纳入 git（`.gitignore` 忽略 `/data/`），无提交产生。

## 4. 结论（只给数据）

- **延迟**：几乎打平。协议无关的 TTFB 中位数 sing-box **514.4 ms** vs mihomo **500.1 ms**（+2.9%）；换成用户原 URL（api.ipify.org）后两边分别是 755 ms 与 762 ms，CLI 真连接 755 ms 对照用户既有 732 ms 同量级。单次最优值两边都是 ~485 ms。
- **吞吐**：**无法区分的量级**。三个序列给的差异方向相反（−11.2% / +5.6% / +23.4%），配对比中位 0.958 / 1.196 / 1.306；同轮内单次极差可达 0.24→1.35 MB/s。WAN（中国→香港→Cloudflare，Wi-Fi）的波动大于被测差异。
- **CPU**：两者都极低（下载期占单核 0.97% vs 1.27%）。换算成与速度无关的 **CPU 秒/MB**，30 MB 档 sing-box **0.01720** vs mihomo **0.01584**，即 A 比 B 高约 **8.6%**；5 MB 档同样方向（+9.1%）。注意 B 侧含默认的逐连接进程匹配开销。
- **内存**：**两个口径结论相反**。WorkingSet 常驻 sing-box **27.6 MB** vs mihomo **23.8 MB**（A 高 16%），峰值 A 高 2.6~5.1 MB；而 Private Bytes 常驻 sing-box **23.05 MB** vs mihomo **26.39 MB**（A 低 12.7%），峰值 A 低约 4.6 MB。
- **成功率**：两边都 100%（延迟 23/23、吞吐 23/23 与 24/24），全部失败样本（5 次 HTTP 429）来自 Cloudflare 限流、同时打中两边，无内核归因失败。
- **结构**：两边都是"1 进程 = 1 内核实例 / 1 引擎"，A 侧进程内含 exactly 1 个 sing-box 实例（日志中 `sing-box started` 仅 1 次）+ 1 个 mixed 监听口。
