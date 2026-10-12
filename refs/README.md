# 参考项目拉取记录（refs）

本目录存放**只读参考**的开源项目源码，用于研究"链式代理 / 前置代理"的实现逻辑。
这些目录体积很大，已在根目录 `.gitignore` 中排除，**不纳入本项目的 git 仓库**。

拉取方式统一为浅克隆（`--depth 1`），只保留最新一次提交。

| 项目 | 仓库地址 | 拉取时间 | HEAD commit | 说明 |
|---|---|---|---|---|
|主流客户端| https://github.com/2dust/主流客户端| 2026-10-10 | `b501efbc45bca3c465df99af7d4ec092c2f4c40f` | C# WPF 客户端 + Xray/sing-box 内核，链式代理实现在 `同类实现/Services/CoreConfig/` |
|主流客户端| https://github.com/主流客户端/主流客户端| 2026-10-09 | `44477d97d4f568e8016d6a2b9a0001d52d8f56a1` | Tauri(Rust+React) 客户端 + mihomo 内核，链式代理实现在 `src-tauri/src/config/runtime.rs` + `src/components/proxy/proxy-chain.tsx` |

## 重新拉取

```powershell
$ErrorActionPreference='Continue'
git clone --depth 1 https://github.com/2dust/主流客户端.git                 F:\chain_proxy\refs\主流客户端
git clone --depth 1 https://github.com/主流客户端/主流客户端.git F:\chain_proxy\refs\主流客户端
```

## 研究结论

详见 [`../docs/RESEARCH_chain_proxy.md`](../docs/RESEARCH_chain_proxy.md)。

一句话总结：两个项目的"链式代理"本质完全一样 —— **dialer chaining（套接字逐跳创建）**，
只不过一个把它表达成 Xray/sing-box 的配置字段（`sockopt.dialerProxy` / `detour`），
另一个表达成 mihomo 的 `dialer-proxy` 字段。它们都**不自己实现转发**，
而是由各自的内核（Xray / sing-box / mihomo）去实现真正的逐跳拨号与中继。

本项目 PicoHop 则是**自己实现这一层转发**：用 Go 直接写出
"客户端 → 前置代理 → 住宅代理 → 目标" 的三级套接字串联与双向搬运。