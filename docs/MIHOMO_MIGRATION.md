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
- 拨号接口在 `constant/adapters.go`：`type Proxy interface { ProxyAdapter; ... }`，
  `ProxyAdapter` 提供 `DialContext` 等（**下一轮第一件事：把 `ProxyAdapter` 的完整方法
  和 `metadata` 构造方式读全**——本次没读完，见第 4 节）。
- **xhttp 确有实现**：`adapter/outbound/vless.go` import `.../transport/xhttp`，
  选项结构 `XHTTPOptions`。

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

## 4. 下一轮开工的第一件事（清单）

1. 读全 `constant/adapters.go` 的 `ProxyAdapter` 全部方法（尤其 `DialContext` 签名）；
2. 读 `RealityOptions` 与 TLS/指纹相关结构体（`adapter/outbound/vless.go` 附近）；
3. 读 `metadata` 包的构造方式（找一个现成的 `DialContext` 调用点照抄）；
4. 然后执行第 0 步（引依赖 + 探针）。

## 5. 已知但**暂不处理**的问题（用户明确指示）

- `chainproxy-cli.exe run --help` 会把 `--help` 当普通参数**真的执行 `run`**（会激活节点并保存配置）。
  用户："cli 也就是你自己能用…除非还要提供 linux 版本" → **暂不修**，等确定要不要出 Linux 版再说。
