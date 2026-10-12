# 引擎迁移契约：sing-box → mihomo（整体换装）

> 决策人：用户。原话："**整体换吧**"（2026-10-12）。
> 依据：A/B 实测（见 `.cache/ab/REPORT.md` 与下方第 1 节）。用户此前的门槛是
> "mihomo 相当或优于 sing-box 就全面换装" —— 数据满足该门槛。
>
> **这份文档是下一轮直接开工的依据。** 每一步都要求"可运行、测试全绿、按惯例提交"，
> 不允许把项目留在编译不过的中间态。

---

## 1. 已核实的事实（换装的前提，全部有据）

### 1.1 A/B 实测结论（sing-box vs mihomo，同一香港 VMess 节点）

| 指标 | sing-box | mihomo | 结论 |
|---|---|---|---|
| 建连 TTFB（池化 n=18） | 514.4 ms | 500.1 ms | 打平（+2.9%） |
| 真连接延迟（api.ipify.org，n=5） | 755 ms | 762 ms | 打平 |
| 吞吐（30M/5M/10s 三组） | — | — | **不可区分**（方向相反，WAN 噪声更大） |
| CPU 秒/MB | 0.01720 | 0.01584 | mihomo 略优，**但我们占了便宜** |
| 内存（WS / Private） | 27.64 / 23.05 MB | 23.81 / 26.39 MB | 两口径方向相反，**无结论** |
| 打包体积（拨号层同口径） | **36.29 MB** | **11.31 MB** | mihomo 小 ~25 MB |
| 协议/传输 | 5 种 + ws/grpc/h2/httpupgrade | 更宽（**xhttp**、tuic、anytls、mieru…） | mihomo 更全 |
| 成功率 | 23/23 | 24/24 | 均 100% |

**可比性已证**：两条路径的出口 IP 均为 `39.109.57.15`（香港 AS42960），与直连基线
`27.186.193.217`（河北 AS4134）不同 → 确实都走了该节点。

**CPU 那条要说清**：mihomo 用默认 `find-process-mode: strict`（逐连接匹配进程名），
sing-box 不做 → **B 被多扣分**，所以"我们更省 CPU"站不住。

### 1.2 mihomo 能不能当"拨号库"用（源码级）

- 模块 `github.com/metacubex/mihomo`，分支 **`Meta`**（注意：`main` 分支是另一个 Python 项目，
  不是内核）。克隆在 `.cache/mihomo-meta`。
- **顶层没有 `internal/`** → 外部模块可 import。
- 出站构造器**导出**：`outbound.NewVless(VlessOption) (*Vless, error)`、`NewVmess`、
  `NewHysteria2` …（`adapter/outbound/*.go`）。
- **出站包不 import `hub`/`tunnel`** → 不被全局单例绑死，**可以每节点建一个独立实例**，
  与我们现在的 sing-box 用法同构 → **"分节点端口 + 多节点同时活跃"照样成立**。
- 拨号接口在 `constant/adapters.go`（**本轮已读全，见下**）：
  ```go
  type ProxyAdapter interface {
      Name() string; Type() AdapterType; Addr() string; SupportUDP() bool
      ProxyInfo() ProxyInfo; MarshalJSON() ([]byte, error)
      DialContext(ctx context.Context, metadata *Metadata) (Conn, error)          // TCP
      ListenPacketContext(ctx context.Context, metadata *Metadata) (PacketConn, error) // UDP
      SupportUOT() ...   // 及其后若干方法
  }
  type Proxy interface { ProxyAdapter; Adapter() ProxyAdapter; ...URLTest/延迟记录... }
  ```
  **关键结论：我们不需要实现这些接口** —— mihomo 的具体类型（`*Vless` 等）已经实现好了，
  我们只**调用** `DialContext` / `ListenPacketContext`。这一步的负担比预想的小。
- **`Metadata` 就是 `constant.Metadata`**（所以接口里不带包名）：字段有
  `NetWork` / `Type` / `Host` / `DstPort` / `SrcIP` / `Process` 等，方法 `RemoteAddress()`；
  枚举用 `C.TCP` / `C.UDP`（源码里有现成用法：`&C.Metadata{NetWork: C.TCP, DstPort: 80}`）。
- **TLS/REALITY 字段位置（已核）**：
  - `adapter/outbound/reality.go`：`type RealityOptions struct { PublicKey; ShortID; SupportX25519MLKEM768 }`
  - `vless.go`：`TLS bool`、`ServerName string`（SNI）；`ClientFingerprint string`（uTLS 指纹）
    在各协议的选项结构里（`anytls.go`/`gost_relay.go` 已见同名字段；vless 的确切位置由编译器确认）。
- **xhttp 确有实现**：`adapter/outbound/vless.go` import `.../transport/xhttp`，
  选项结构 `XHTTPOptions{ Path, Host, Mode, Headers, NoGRPCHeader, XPadding*, Session*, Seq* }`。

### 1.3 关键选项结构（已读到）

```go
// adapter/outbound/vless.go
type VlessOption struct {
	BasicOption                     // TFO / MPTCP / Interface / RoutingMark / DialerForAPI …
	Name, Server string; Port int; UUID string
	Flow string; TLS bool; ALPN []string; UDP, PacketAddr, XUDP bool
	PacketEncoding, Encryption, Network string
	RealityOpts  RealityOptions    // 结构体定义不在本文件，下一轮补读
	HTTPOpts, HTTP2Opts, GrpcOpts, WSOpts, XHTTPOpts XHTTPOptions
	// …另有 ECH / ShadowTLS / Restls / JLS 等
}
type XHTTPOptions struct {
	Path, Host, Mode string
	Headers map[string]string
	NoGRPCHeader bool
	XPadding* / UplinkHTTPMethod / Session* / Seq* …   // 字段名已在源码中确认
}
```

### 1.4 构建与体积

- mihomo 官方参数：`CGO_ENABLED=0 go build -tags with_gvisor -trimpath -ldflags '-s -w -X "...constant.Version=..."'`
  → **66 秒**（含下载 133 个依赖）、**57.69 MB** 全功能 CLI。
- **只引出站层**（`adapter/outbound` + `transport/xhttp`，不带 `with_gvisor`）→ **11.31 MB**；
  二进制里已确认含 `vless/xhttp/shadowsocks/hysteria2/tuic` 代码（不是被链接器裁空的假数）。

---

## 2. 迁移方案（整体换，但每步都可运行）

### 第 0 步：把 mihomo 引成依赖，先不动任何行为
- `go get github.com/metacubex/mihomo@<Meta 分支的 commit>`（记录确切版本）。
- 写一个**只 import、不调用**的最小探针（`.cache/` 下即可）确认：能编译、体积增量、
  不与我们现有依赖冲突（两边都用了 utls/gvisor 的**不同模块路径**，预期不冲突但要实测）。
- **验收**：全量测试仍绿；记录体积增量。

### 第 1 步：新写 `internal/engine/mihomo` 适配层（与现有 Upstream 同形状）
- 目标：实现 `proxy.Upstream` 接口（`Start` / `Stop` / `Dial` / `DialUDP` / `Describe` /
  `SupportsUDP` / `DialTimeout`），内部持有**一个 mihomo 出站实例**。
- 关键：**不经 `hub.Parse`、不建 inbound**，只 `NewXxx(option)` + `DialContext`
  → 与我们绕开 sing-box router 的做法一致（这是"每节点一实例"成立的根本）。
- **验收**：单测覆盖"选项映射"（表驱动：我们的 `config.Node` → mihomo option），
  以及一个真实小节点的手工连通验证。

### 第 2 步：选项/传输映射表（本步最易出错，必须表驱动 + 单测）
| 我们的 `TransportOptions.Network` | mihomo |
|---|---|
| `tcp` | 不设（默认） |
| `ws` | `WSOpts{Path, Headers{Host}}` |
| `grpc` | `GrpcOpts{GrpcServiceName}` |
| `http` / `h2` | `HTTPOpts` / `HTTP2Opts{Path, Host[]}` |
| `httpupgrade` | `Network = "httpupgrade"` |
| **`xhttp`（新增能力）** | `Network = "xhttp"` + `XHTTPOpts{Path, Host, Mode}` |

- TLS/Reality/uTLS 指纹：由 `BasicOption`（指纹）+ TLS 字段 + `RealityOpts{PublicKey, ShortID}`
  映射；**下一轮先补读 `RealityOptions` 与 TLS 相关结构体的确切字段名**。
- **验收**：每个传输至少一条真实节点连通验证（我们现在的 5 种协议 + xhttp）。

### 第 3 步：切默认引擎
- 让节点默认走 mihomo 适配层；sing-box 保留在代码里作**回退开关**（配置项），
  跑稳一两个版本后再摘。
- **验收**：全量测试 + 逐节点真连接延迟 + 吞吐抽测（与换装前基线对比，回落超 10% 要停）。

### 第 4 步：清理与收尾
- 摘掉 sing-box 依赖与 `with_utls,with_quic` 构建标记（改为 mihomo 所需标记）；
- 更新 `docs/`（本文件 + PROJECT_PROGRESS）；重测打包体积并记录实际值
  （上一轮给的是**估计** `GUI ≈ 21~24 MB`，只有真编出来才算数）。

---

## 3. 风险与未测项（不许在报告里省略）

1. **`ProxyAdapter` / `metadata` / `RealityOptions` 的确切签名没读全** —— 第 1、2 步的第一步就是补读，别猜。
2. **UDP 未验**：mihomo 出站支持 UDP，但我们的 `DialUDP` 语义（`proxy.UDPChannel`）要重新对接；
   单测 + 真实 UDP（DNS over UDP / QUIC 应用）验证。
3. **内存的"多节点扩展"没测**：我们是"一进程 N 个内核实例"，A/B 只测了单引擎。
   换装后要专门测 1/2/4/8 个节点的常驻内存曲线。
4. **吞吐差异在这条链路上不可分辨** → 换装**不能**以"更快"为理由；理由是体积/覆盖/xhttp。
5. **许可证**：mihomo 是 **GPL-3.0**（已核）。我们现在内嵌的 sing-box 也是 GPL-3.0
   （本轮未重新核对，换装前补齐证据）。对外分发时 copyleft 义务不变。
6. **xhttp 的可用性只证明到"能构建 + 有实现"**，还没拿真实 xhttp 节点跑通
   （用户那条 `①US1直连出口` 正好可以当第一个验证对象）。

---

## 4. 进度与下一轮开工的第一件事

### 第 ① 步（补读签名）✅ 已完成（第 1 轮）
`ProxyAdapter` 全方法、`constant.Metadata` 字段与枚举、`RealityOptions`、`XHTTPOptions`
都已读实（见 1.2 / 1.3 节）。**副产品**：确认"我们只需调用、不需实现 `ProxyAdapter`"。

**探针已实测通过**（`.cache/mihomo-meta/cmd/xprobe`，放在 mihomo 克隆内、不碰我们的仓库）：
```
✅ 隧道建立（382 ms）-> 204.197.163.238:30022
✅ 响应首行：HTTP/1.1 204 No Content
```
即 **mihomo 出站层能在"不经 hub.Parse、不建 inbound"下独立拨通 xhttp + REALITY 节点并过真实流量**
—— 这是"每节点一实例 + 多端口多活"架构成立的关键证据，也是整件事的起因。

途中有两条对迁移直接有用的发现：
1. **REALITY 必须显式给 `client-fingerprint`**（报错原文：`REALITY is based on uTLS,
   please set a client-fingerprint`）。sing-box 会自动推断，**mihomo 不会** → 映射表必须带上。
2. **配置里远端端口字段是 `port`**（不是 `serverPort`）。写映射代码时字段名一律以
   `internal/config/model.go` 的 tag 为准，否则会得到"看起来像网络问题"的报错。

### 第 ② 步（引依赖 + 体积增量）✅ 已完成（第 2 轮）
- `go get github.com/metacubex/mihomo@88dcbf7f…`（`Meta` 分支 tip）→ 解析为**稳定版本 `v1.19.32`**，
  记在 `go.mod`（当前是 `// indirect`，因为还没有代码 import 它）。
- **无依赖冲突**：`go build ./...` 通过；`go test ./internal/...` 8 个包全 ok。
  途中自动升级了两个间接依赖（`go.yaml.in/yaml/v3`→v3.0.5、`go4.org/netipx`→新版），测试全绿。
- **体积增量（实测，非估计）**：

  | 组合 | 体积 |
  |---|---|
  | 仅 sing-box + 我们 engine | 36.29 MB |
  | 仅 mihomo 出站层 | 11.31 MB |
  | **两者都在（过渡期上限）** | **43.64 MB** |
  | → mihomo 净增量 | **+7.36 MB** |

  比简单相加（47.60 MB）**小 4 MB**（两个引擎共享部分底层代码）→ 过渡期"保留 sing-box 作回退"
  的代价只有 +7.36 MB。
- ⚠️ **此时不要跑 `go mod tidy`**：还没有代码 import mihomo，tidy 会把它从 `go.mod` 里删掉。

### 第 ③④ 步（适配层 + 映射）✅ 主体完成（第 3 轮）

新增 `internal/engine/mihomo.go`（`MihomoUpstream` 实现 `proxy.Upstream`）+ `mihomo_test.go`。

- 只把 mihomo 当**拨号器库**：每节点一个独立出站实例、`DialContext` 直连，
  **不经 `hub.Parse`、不建 inbound** → "每节点一端口 + 多节点同时活跃"照旧成立。
- `Start` 建实例 / `Stop` 释放 / `Dial` 走 `DialContext` / `DialTimeout` 用 `node.Timeout()`。
- **`DialUDP` 目前明确报错**（mihomo 的 UDP 是 `ListenPacketContext`+`PacketConn`，
  包成 `proxy.UDPChannel` 属第 ④ 步剩余项）—— 不假装成功。
- `mihomoSupports` 只对**已写好**的协议返回 true（当前 vless），宁可构造时报错，
  也不等用户激活才发现没接。
- 映射已覆盖 vless 的六种传输（tcp/ws/grpc/http/h2/httpupgrade/xhttp），
  表驱动单测 12 个子测试锁死。四个反直觉点（都有源码/实测依据）：REALITY 必须显式给
  client-fingerprint；`HTTPOptions` 没有 Host 字段（放 Headers）；httpupgrade 复用 `WSOpts`；
  h2 用 `HTTP2Opts`。

**真实节点连通验证（第 4 轮，用我们自己的适配层，不是独立探针）**：
```
节点=①US1直连出口  传输=xhttp  TLS=true  指纹="chrome"  REALITY=true
  [engine] 「①US1直连出口」mihomo 出站已就绪：204.197.163.238:30022（type=Vless）
✅ Start 完成（0 ms）
✅ Dial 建立链路（1422 ms）-> 204.197.163.238:30022
✅ 响应首行：HTTP/1.1 204 No Content
```
即：**`internal/engine.MihomoUpstream` 能起、能拨通 xhttp + REALITY 节点、能过真实流量。**

### 第 ⑤ 轮：其余四协议的选项字段（已读实，供实现直接照抄）

> 本轮**补丁未落盘**：写好的补丁脚本随 `.cache` 被清空而丢失（见文末"意外"）。
> 下面这些字段是从 mihomo 源码逐个读出来的事实，**下次实现直接用，不必重读**。

| 协议 | mihomo 选项结构 | 关键字段（我们配置 → 它） |
|---|---|---|
| VMess | `outbound.VmessOption` | `Name/Server/Port/UUID/AlterID`；**加密方式：我们叫 `Security` → 它叫 `Cipher`**；`TLS/ServerName/UDP/Network`；传输：`WSOpts/GrpcOpts/HTTPOpts/HTTP2Opts/XHTTPOpts` |
| Trojan | `outbound.TrojanOption` | `Name/Server/Port/Password`；**SNI 字段名是 `SNI`**（不是 ServerName）；`ALPN/UDP/Network`；`ClientFingerprint` ✓；传输：仅 `GrpcOpts/WSOpts` |
| Shadowsocks | `outbound.ShadowSocksOption`（注意大小写：**ShadowSocks**） | `Name/Server/Port/Password/Cipher/UDP`；无传输层 |
| Hysteria2 | `outbound.Hysteria2Option` | `Name/Server/Port/Password/SNI/Obfs/ObfsPassword`；**`Up`/`Down` 是带单位的字符串**（如 "100 Mbps"），我们配置里是整数 Mbps → 要格式化；为 0 时不写 |

**构造函数名**（已确认）：`NewVless` / `NewVmess` / `NewTrojan` / **`NewShadowSocks`**（不是 `NewShadowsocks` ——
第 1 轮的探针编译错误 `undefined: outbound.NewShadowsocks` 正好反证了正确拼写）/ `NewHysteria2`。

**实现要点（本轮已想清，直接照做）**：
1. 四个映射函数与 `vlessOption` 同一套路：**纯函数**（可表驱动单测）；
2. 传输支持按各结构体**实际有**的选项字段放行（vmess 全六种；trojan 只 ws/grpc；ss/hysteria2 无传输层），
   其余组合返回明确错误，不要静默忽略；
3. `mihomoSupports` 同步放行这四种协议（当前只放行 vless）。

### 第 ④ 步剩余项（下一轮）
1. 其余协议：`vmess` / `trojan` / `shadowsocks` / `hysteria2` 的映射与构造（形状照 vless）。
2. **UDP**：`ListenPacketContext` → 包成 `proxy.UDPChannel`（`SendToUDP`/`ReceiveFromUDP` + quit 语义）。
3. **逐传输真实验证**：目前只验了 `xhttp`（用户手上唯一可用的 vless 节点）。
   ws/grpc/http/h2/httpupgrade 与其它协议**都还没有真实节点验证** —— 不许当已完成。
   用户可以给一条 vmess（已有：香港-39.109-CN2-VMess）来验 vmess+tcp 那条。




## 5. 已知但**暂不处理**的问题（用户明确指示）

- `chainproxy-cli.exe run --help` 会把 `--help` 当普通参数**真的执行 `run`**（会激活节点并保存配置）。
  用户："cli 也就是你自己能用…除非还要提供 linux 版本" → **暂不修**，等确定要不要出 Linux 版再说。
---

## 6. 意外记录：`.cache` 被清空（第 5 轮）

第 5 轮准备落地"其余四协议映射"时发现：**`.cache` 下我这一会话建的东西全没了** ——
补丁脚本、`.cache/mihomo-meta`（mihomo 源码克隆）、`.cache/shots`（验证截图）、
以及 `gomod/gobuild` 的部分内容。表现：`go build` 重新下载了一批模块（随后成功）。

**影响与处置**：
  - 工作区**干净**、`go build ./...` 与全部测试**仍绿**（`git status` 为空）→ 没有半成品状态；
  - mihomo 依赖仍在 `go.mod`（v1.19.32），模块缓存会自动重下 → **不影响后续构建**；
  - A/B 实测数据的**归档在 `docs/bench/`（已提交）**，不受影响；
  - 源码级事实（选项字段、构造函数名、传输映射）已全部写入本文档 → **不依赖那个克隆存在**；
  - 需要重读 mihomo 源码时（例如接 UDP 要看 `PacketConn` 接口），按第 1 轮的做法重新
    `git clone --branch Meta` 即可（`main` 分支是另一个 Python 项目，别克隆错）。
  - **原因未查明**（可能是外部清理/磁盘空间策略）。不猜测；但它提醒一件事：
    **凡是不想丢的结论，必须落到仓库里（docs/），而不是留在 `.cache`。**
---

## 7. 第 ⑤⑥⑦⑧ 轮进度（逐轮累积，证据在提交信息里）

### 第 6 轮：其余四协议的映射 ✅
`vmess` / `trojan` / `shadowsocks` / `hysteria2` 的映射与单测（共 17 个子测试）；
`mihomoSupports` 放行五种协议。要点已在前一节（第 5 轮）记全，实现按它做即可。

被纠正三次，都是同一类教训：
  1. 编译器：**mihomo 的 `VmessOption` 没有 `XHTTPOpts`**（xhttp 只有 vless 有）
     → vmess+xhttp 在映射**入口与分支两处**明确报错，不静默忽略传输；
  2. 测试：第 3 轮那条守护测试**自己抓到过时断言**（"这四种还没接"），
     已按同一意图改为"已实现必须放行、**未实现必须拒绝**"；
  3. 我自己：用 PowerShell 双引号 here-string 写 Go 代码 → **反引号被当成转义符**，
     Go 的原始字符串被吃掉、文件语法损坏（fail-fast 拦住提交）。
     **写 Go 代码不要用 PowerShell here-string。**

### 第 7 轮：vmess 真实链路验证 ✅（契约第 ④ 步的第 2 条证据）

用**我们自己的** `internal/engine.MihomoUpstream` 拨 `香港-39.109-CN2-VMess`
（`39.109.57.15:11720`，tcp、无 TLS）：

    ✅ Start 完成（0 ms）
    ✅ DialUDP 如实报错：mihomo 适配层：UDP 通道尚未接入（…）
    ✅ Dial 建立链路（54 ms）-> 39.109.57.15:11720
    ✅ 响应首行：HTTP/1.1 204 No Content

**真实链路验证清单（契约第 ④ 步的硬要求，逐条累积）**：
  | # | 协议 + 传输 | 轮次 |
  |---|---|---|
  | 1 | vless + xhttp + REALITY | 第 4 轮 |
  | 2 | vmess + tcp | 第 7 轮 |

**仍未验证**：trojan / shadowsocks / hysteria2（只有单测）；
ws / grpc / http / h2 / httpupgrade 五种传输（无真实节点）；UDP（未接入）。

### 第 8 轮：UDP 的两个接口已读实（**未实现**，见下）

mihomo 侧（`constant.PacketConn`，v1.19.32 模块缓存里的源码）：

    type PacketConn interface {
        N.EnhancePacketConn        // sing 的增强包接口：ReadPacket/WritePacket(*PacketBuffer)
        Connection                 // 普通连接语义（含 net.Conn 那套）
        ResolveUDP(ctx, *Metadata) error
    }

我们侧（`internal/proxy`）：

    type UDPChannel interface {
        SendToUDP(payload []byte, host string, port int) error
        ReceiveFromUDP(quit <-chan struct{}, buf []byte) (n int, srcHost string, srcPort int, err error)
        Close() error
    }

**实现思路（下一轮照做）**：
  1. `DialUDP` → `out.ListenPacketContext(ctx, &C.Metadata{NetWork: C.UDP, Host, DstPort})`，
     先按 mihomo 的用法调 `ResolveUDP(ctx, metadata)`；
  2. `SendToUDP` → `WriteTo(payload, &net.UDPAddr{IP, Port})`（`net.PacketConn` 那套）；
  3. `ReceiveFromUDP(quit, buf)` → **必须可被 quit 叫醒**（本项目对 wireguard 侧有过死锁教训）：
     用 `SetReadDeadline` 小步轮询 + 每轮检查 quit，或起一个读协程 + 通道；
  4. `Close()` → 关底层 + 让阻塞读退出。

**为什么这轮没有直接写**：`EnhancePacketConn`/`PacketBuffer` 的确切用法与 `ResolveUDP` 的语义
还没读全，而且**当前没有可验证的 UDP 目标** —— 半写完的 UDP 是"无法验证的代码"，
比不写更糟（本项目已多次吃过"看起来对、其实没通"的教训）。
**验证方案（下一轮）**：经该 UDP 通道把一条 DNS 查询发到 `8.8.8.8:53`，看是否收到应答
（真实、可判定，且不需要额外节点）。

### 第 ⑨ 轮：UDP 的 API 事实已钉死（比预想简单，下一轮可直接写）

在模块缓存里把链路读通了（`.cache/gomod/...`，与 go.mod 同为 v1.19.32）：

**① `constant.PacketConn` 的真实构成**（`mihomo/common/net/packet/packet.go:13`）：

    type EnhancePacketConn interface {
        net.PacketConn      // ReadFrom / WriteTo / SetReadDeadline / Close …
        WaitReadFrom        // 一个"可等待/可叫醒"的机制
    }

**关键结论：返回的对象本身就是 `net.PacketConn`** —— 所以我们的 `UDPChannel`
可以直接建在它上面，**不需要**去啃 `PacketBuffer` 那套（此前担心会复杂得多）：

  - `SendToUDP(payload, host, port)`
      → `pc.WriteTo(payload, &net.UDPAddr{IP: net.ParseIP(host), Port: port})`
  - `ReceiveFromUDP(quit, buf)` → 两种都能用：
      (a) `select` 等 `pc.WaitReadFrom()` 与 `quit`（更干净）；
      (b) 退而求其次：`SetReadDeadline(now+200ms)` 小步轮询，每轮检查 `quit`。
      **无论哪种都必须能被 quit 叫醒**（wireguard 侧有过"阻塞读叫不醒 → 死锁"的教训）。
  - `Close()` → `pc.Close()`
  - `DialUDP(ctx, front)` → `out.ListenPacketContext(ctx, &C.Metadata{NetWork: C.UDP})`，
      再按 mihomo 用法调 `ResolveUDP(ctx, metadata)`。
      注意我们接口的 `DialUDP` **不带目标**（目标由每个 `SendToUDP` 给），所以 metadata
      用空的即可。
  - **错误要透传**：不支持 UDP 的 mihomo 出站会让 `Base.ListenPacketContext` 返回
      `C.ErrNotSupport`（见 `adapter/outbound/base.go:96`）—— 照原样返回给上层，别包装成
      自己的话术，否则排查时看不出是内核不支持。

**② `PacketBuffer{Buffer *buf.Buffer, Destination M.Socksaddr}`** 也读到了
（`sing@v0.5.8/common/network/packet.go`），以及 sing 的高层读接口
`ReadPacket(buffer *buf.Buffer) (destination M.Socksaddr, err error)`
（`packet_conn.go:35`）—— 如果 (a) 方案里 `WaitReadFrom` 用起来别扭，可改用这条高层接口。

**③ 验证方式不变**：经通道把 DNS 查询发到 `8.8.8.8:53`，看是否收到应答。

### 第 ⑪ 轮：UDP 打通（按目标建通道）

    UDP 通道建立（0 ms）
    已发出 DNS 查询（29 字节）-> 8.8.8.8:53
    收到应答：61 字节  来自 8.8.8.8:53
    DNS 应答 ID=0x1234  应答记录数=2      <- ID 匹配即真实往返

设计：按目标建通道（map["host:port"]C.PacketConn，首次发送时 ListenPacketContext + ResolveUDP）；
DialUDP 用 SupportUDP() 尽早失败；C.ErrNotSupport 原样透传；
ReceiveFromUDP 先用轮询版（升级路径：每 conn 一个读协程 + 共享 chan）。
**未覆盖**：hysteria2(QUIC) 的 UDP、WireGuard 经 mihomo 的 UDP、多目标并发压力。

### 第 ⑫/⑬ 轮：换装开关的机制（第 ⑤ 步前半）

internal/proxy 新增 RegisterMihomoFactory / SetEngineMode / EngineMode；
NewUpstream 在五个 sing-box 协议分支之前插入：mode == "mihomo" 且 mihomo 工厂接得了 -> 用它，
**接不了就回退 sing-box 并写日志**。默认空串（sing-box）-> 行为零变化。
单测 TestEngineSelectionAndFallback 用假工厂锁死三条。

**还没做**：应用侧读 Settings.EngineMode 调 SetEngineMode；第 ⑤ 步后半（把默认翻成 mihomo）；
第 ⑥ 步（清依赖/构建标记 + 重测实际打包体积）。

### 第 ⑭ 轮：应用侧接线（第 ⑤ 步后半的一半）

`Settings.EngineMode`（json: `engineMode`，默认空串）落地；`app.New()` 在读配置之后、
**任何节点被创建之前**调用 `proxy.SetEngineMode(cfg.Settings.EngineMode)`。

读法：在 `data/config.json` 里把 `settings.engineMode` 写成 `"mihomo"` 即启用新内核，
留空或写 `"singbox"` 就是原来的行为。**接不了的节点会自动回退并写日志**。

单测 `TestEngineModeJSON` 锁死：老配置缺这个字段时读出来必须是空串（=sing-box），
序列化名字必须是 `engineMode` —— 默认值一旦"自动打开新内核"，那是灾难性的默认。

**还没做**：界面上的开关（现在要改配置文件）；把默认翻成 mihomo；第 ⑥ 步。

### 第 ⑮ 轮：端到端验开关 —— 通过，并暴露/修掉一个回退盲区

用配置副本（不动用户配置）跑单节点激活：

  A) engineMode=mihomo + 正常 vmess 节点 -> **日志出现**
     「香港-39.109-CN2-VMess」mihomo 出站已就绪：39.109.57.15:11720（type=Vmess）
     → 开关端到端生效（走的是我们的 mihomo 适配层，不是 sing-box）。

  B) engineMode=mihomo + vmess 缺 uuid -> **激活直接失败**，没有回退。
     原因：mihomoFactory 只在**构造时**检查协议，而**映射错误发生在 Start 里** ——
     上层没法回退。这在换装期是真风险：某些 sing-box 能跑、mihomo 映射会拒绝的节点
     会从"可用"变成"不可用"。

**修法（本轮已改）**：`NewMihomo` 在**构造时就把出站建出来**（映射错误因此提前到构造期），
实例存在 `built` 字段里、`Start` 只切换激活态、`Stop` 只清激活态。
于是 `proxy.NewUpstream` 的"构造失败就回退 sing-box"能覆盖映射失败这一整类情况。

**注**：`NewVless/NewVmess/...` 只是构造对象、不建连接，所以构造期建实例的代价可以忽略。

## 8. 第 ⑤ 步完成情况（截至第 18 轮）：开关可用、默认未翻

### 已完成

| 项 | 状态 | 证据 |
|---|---|---|
| 注册 mihomo 工厂 + 模式选择 + **失败回退** | 完成 | `internal/proxy`：`RegisterMihomoFactory` / `SetEngineMode` / `EngineMode`；单测 `TestEngineSelectionAndFallback` |
| 应用侧接线 | 完成 | `Settings.EngineMode`（json `engineMode`，默认空串）+ `app.New()` 里 `proxy.SetEngineMode(...)`；单测 `TestEngineModeJSON`（老配置读出必须是空串） |
| **端到端验证** | 通过 | 配置副本 + `engineMode=mihomo` + vmess 节点 → 日志出现「mihomo 出站已就绪：39.109.57.15:11720（type=Vmess）」 |
| **回退路径验证** | 通过 | mihomo 映射拒绝的节点 → 日志「mihomo 接不了…，回退 sing-box：…」，节点最终 running（由 sing-box 接管） |
| 界面开关 | 完成 | 设置 → 运行与界面 → `代理内核 [sing-box（默认） \| mihomo] (?)`；已截图确认 |

**实现期间修掉的一个真风险**：`mihomoFactory` 原本只在构造时检查协议，而**映射错误发生在 `Start`**，
上层回退覆盖不到 → "sing-box 能跑、mihomo 映射拒绝"的节点会从可用变不可用。
现在 `NewMihomo` **构造时就把出站建出来**，映射错误提前到构造期，回退因此覆盖整类情况。

### 默认仍是 sing-box（**没有翻**）

翻默认的前置条件（第 18 轮判定）：
  1. **更多传输的真实验证**。目前 ④′ 只验了 3 条：vless+xhttp、vmess+tcp、vmess+UDP(DNS)。
     ws / grpc / http / h2 / httpupgrade 与 trojan / shadowsocks / hysteria2 **都没有真实链路验证**；
  2. 用户的决定（下面第 9 节）。

### 第 ⑥ 步：一个必须纠正的认知

我早先给过"换装后 GUI 约 21~24 MB"的**估计**，前提是**彻底摘掉 sing-box**。
但第 ⑤ 步要求**保留 sing-box 作回退**，二者不能同时成立：

  > 只要回退还在，sing-box 就在二进制里，`with_utls,with_quic` 这两个构建标记也必须留着
  > （它们是 sing-box 的），体积就仍是**过渡期的 58.46 MB（实测）**。

所以：
  - 当前设计下的"最终体积" = **58.46 MB**（实测，不是 21~24 MB）；
  - 要 21~24 MB，只有在"**所有传输都验证过 + 明确同意放弃回退**"之后摘掉 sing-box；
  - **"清构建标记"在保留回退时不该做**（标记是 sing-box 需要的）。

## 10. Runbook：如何安全地"翻默认"与"摘回退"

> 这两件事都**需要用户明确同意**，且都必须先补齐验证。下面是可照做的步骤与判定标准，
> 目的是让任何一次操作都能**随时停下而不留半成品**。

### A. 补齐传输验证（翻默认的前提）

目前 ④′ 只有 3 条真实链路：`vless+xhttp`、`vmess+tcp`、`vmess+UDP(DNS)`。
`ws / grpc / http / h2 / httpupgrade` 与 `trojan / shadowsocks / hysteria2` **都没有真实链路验证**。

两条补验证的路子：
  1. **外部节点**（最真实）：拿一条对应传输的节点，按第 4/7/11 轮的做法写临时探针
     （只读 `data/config.json`，用**我们自己的**适配层拨一次并断言响应），验完即删。
  2. **自给自足**（不依赖外部节点，本轮评估过但**未做**）：用 mihomo（或我们已内嵌的 sing-box）
     起一个**本地 inbound**（如 vless + ws / vless + grpc，出站 direct），再让我们的适配层去拨它。
     这条路要写"服务端几十行 + 客户端探针"，属于一次完整的实现回合，**不要在回合末尾开始**。

### B. 翻默认引擎（把 mihomo 变成默认）

前置：A 完成（至少覆盖你实际会用到的那几种传输）。

步骤：
  1. 改 `config.Settings.EngineMode` 的**默认值**（现在是空串 = sing-box）；
     注意 `blankSettings()`（前端）与 `defaultSettings`（Go 侧）两处都要改，否则新老配置表现不一致；
  2. 单测要跟着改：`TestEngineModeJSON` 现在断言"缺字段必须是空串"——翻默认后这条要改成
     断言"缺字段 = mihomo"，同时**新增**一条"显式写 singbox 仍然走 sing-box"的断言；
  3. 保留**回退**：`proxy.NewUpstream` 里 mihomo 构造失败 → sing-box（这条不动）；
  4. 真机验证：用你的两个住宅节点 + 那条 vmess 节点各跑一次"真连接延迟 + 一次实际下载"，
     确认与翻默认前**没有回落**（延迟/成功率同量级）；
  5. 提交并把本轮证据写进本文档。

判定（出现任一条就**回滚默认值**）：
  - 任一节点由"可用"变"不可用"；
  - 真连接延迟中位数比翻之前差 20% 以上；
  - 日志里出现以前没有的 mihomo 报错。

### C. 摘掉 sing-box 回退（拿到 ~21~24 MB 的体积）

只在"**所有你会用到的传输都验证过 + 你明确同意失去回退**"之后做。步骤：
  1. 删 `internal/engine/singbox.go` 与 sing-box 相关依赖、`register.go` 里的 sing 注册；
  2. 删 `with_utls,with_quic` 构建标记（它们是 sing-box 的）—— 脚本里 `scripts/build.ps1`、
     `scripts/check.ps1` 都要改；
  3. `proxy` 里拆掉 singFactory 分支与回退逻辑；
  4. 跑全量门禁 + 真机验证（同 B-4）；
  5. **重新测打包体积**并把实测值写进本文档（此刻才第一次有"最终体积"的真数）。

判定：体积若没有明显下降，说明还有别的依赖在撑着 —— 先查清再继续删，别为了一个数字删出问题。

### D. 每一步的纪律（本项目一贯要求）

  - 每一步都要**可运行**：改完立刻 `scripts/check.ps1`（gofmt / 前端构建 / vet / 全部测试）；
  - **不允许**把项目留在编译不过或半状态；失败就回滚（脚本里已有 `git checkout` 回滚的先例）；
  - 结论与证据写进本文档（**不能只留在 `.cache`** —— 它已被清空两次）；
  - 生成的代码也要当代码审：本迁移期间因"锚点凭记忆写""Go 字符串里嵌半角引号"
    "编造不存在的配置字段"各翻过一次车，都记在第 7 节。

## 9. 需要用户决定的事（第 ⑥ 步的真正前提）

  1. 是否**放弃 sing-box 回退**（决定体积 58.46 MB vs 约 21~24 MB）；
  2. 是否**翻默认引擎**为 mihomo（我建议先补更多传输验证）；
  3. 若要补验证：需要对应传输的节点（例如一条 ws 或 trojan 节点）。
     —— 另一条不依赖外部节点的路子是"用 mihomo 自己起一个本地 ws/gRPC inbound 当被测服务"，
     契约记在这里，下一轮可以试。

### 第 ⑩ 轮：UDP 试做后被实测否掉 —— 设计要件是"按目标建通道"

按第 ⑨ 轮的思路写了 `mihomo_udp.go`（`C.PacketConn` → `proxy.UDPChannel`）并接上 `DialUDP`，
**编译一次通过**，但真实跑 DNS 验证时立刻失败：

    ❌ DialUDP 失败：mihomo UDP 通道建立失败: can't resolve ip: couldn't find ip

**原因（这就是第 ⑨ 轮留的那个疑点，现在被实测钉死）**：
`pc.ResolveUDP(ctx, metadata)` **需要一个具体目标**才能解析出 IP；而我们的接口
`DialUDP(ctx, front)` **不带目标**（目标按每个数据报给：`SendToUDP(payload, host, port)`）。
传空 metadata → mihomo 报 "can't resolve ip"。

**结论：不能"一次建一个通道"，必须"按目标建通道"。** 这不是小修补，是设计要件变了。
（mihomo 自己的 UDP 也是按会话/目标建 PacketConn 的。）

#### 下一轮的正确做法（已定，照着写即可）

`mihomoUDPChannel` 内部维护 **`map[string]C.PacketConn`（key = "host:port"）**：

1. `SendToUDP(payload, host, port)`：
   - 先查 map；没有就 `ListenPacketContext(ctx, meta)` + `pc.ResolveUDP(ctx, meta)`，
     其中 `meta = &C.Metadata{NetWork: C.UDP, Host: host, DstPort: uint16(port)}`
     （能解析出 IP 时一并填 `DstIP`）；
   - 然后 `pc.WriteTo(payload, &net.UDPAddr{...})`；
   - **注意**：WireGuard 场景下目标会变化，所以必须**每个目标一个 conn**，不能复用。
2. `ReceiveFromUDP(quit, buf)`：要能从**所有**已建 conn 收包。两种实现：
   - （简单，先做这个）轮询所有 conn，每个给 100–200ms 读超时，检查 quit 后继续；
     缺点是活跃 conn 会拖慢其它 conn 的读取；
   - （更好，若轮询出问题再改）每个 conn 起一个读协程，把 `(payload, srcAddr)` 投进共享 chan，
     `ReceiveFromUDP` 从 chan 取；`quit` 关闭时所有协程退出。
3. `Close()`：关掉 map 里所有 conn。
4. 失败路径：`ListenPacketContext` 返回 `C.ErrNotSupport` 时**照原样透传**（别包装），
   让人一眼看出是内核不支持。

#### 验证方式（不变，且已备好）

经通道把 DNS 查询发到 `8.8.8.8:53`，看是否收到应答并核对 **DNS ID 是否匹配**（`0x1234`）。
本轮临时程序就是干这个的（`udp_probe_main.go`，已删）；下一轮沿用即可。

#### 本轮处置

**回滚了 UDP 代码**（`mihomo_udp.go` 删除、`DialUDP` 还原为显式报错），
因为一个"已知跑不通的 UDP"不该留在树里；事实与实现规格写进本文件，下一轮据此重做。
（项目始终处于"可运行、测试全绿"的状态。）


