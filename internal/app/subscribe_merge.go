package app

import (
	"time"

	"github.com/murongruolan/picohop/internal/config"
)

// MergeStats 是一次订阅合并的统计。
type MergeStats struct {
	// Added 是机场新增、我们第一次见到的节点数。
	Added int `json:"added"`
	// Updated 是已存在、本次刷新了远端字段的节点数。
	Updated int `json:"updated"`
	// Restored 是上次被标记"已下架"、这次又回来了的节点数（含在 Updated 里）。
	Restored int `json:"restored"`
	// Missing 是本次机场不再提供、被标记为"已下架"的节点数。
	Missing int `json:"missing"`
	// Conflicts 是与**已有手动节点或另一条订阅**撞了同一个远端入口、因而被跳过的节点数。
	//
	// 【为什么跳过而不是"接管"】那可能是用户手工加的节点（也许还配着前置与端口），
	// 悄悄把它变成订阅节点等于抹掉用户的归属设置。宁可跳过并如实告诉用户。
	Conflicts int `json:"conflicts"`
}

// mergeSubNodes 把一次订阅解析结果增量合并进配置。
//
// 【为什么不做两家客户端那种"整组删了重建"】v2ray 系客户端就是那么干的：节点主键每次
// 重新生成，于是用户改过的节点名、自定义排序、测速结果全丢，只能靠多级回退把"当前选中
// 节点"和流量统计捞回来。我们按 config.Node.IdentityKey（协议 + 服务器 + 端口 + 凭据摘要）
// 判定身份，于是能保住用户的一切本机状态。
//
// 三条规则：
//  1. 同一个远端入口已存在 → **只更新远端字段**，本机字段一律保留；
//  2. 机场新增 → 以"未分配端口 + 未激活"加入（见 config.Node.PortPending）；
//  3. 机场删掉了 → 只打 SubMissing 标记，**不自动删**（用户可能给它配过前置/端口）。
func mergeSubNodes(cfg *config.Config, subID string, incoming []config.Node) MergeStats {
	var st MergeStats

	byKey := map[string]int{} // 本订阅已有节点：IdentityKey → 下标
	foreign := map[string]bool{}
	for i := range cfg.Nodes {
		if cfg.Nodes[i].SubID == subID {
			byKey[cfg.Nodes[i].IdentityKey()] = i
		} else {
			foreign[cfg.Nodes[i].IdentityKey()] = true
		}
	}

	present := map[int]bool{} // 本次出现过的已有节点下标（其余的要标下架）
	for order, in := range incoming {
		in.SubOrder = order
		key := in.IdentityKey()
		if idx, ok := byKey[key]; ok {
			wasMissing := cfg.Nodes[idx].SubMissing
			cfg.Nodes[idx] = mergeRemote(cfg.Nodes[idx], in)
			present[idx] = true
			if wasMissing {
				st.Restored++
			}
			st.Updated++
			continue
		}
		if foreign[key] {
			st.Conflicts++
			continue
		}
		n := in
		n.SubID = subID
		n.SubOrder = order
		n.NameFromSub = in.Name
		// 默认不占端口、不激活：一次导入可能上千个节点，全占端口/全起来等于把本机打爆。
		n.ListenPort = 0
		n.Active = false
		n.SubMissing = false
		n.CreatedAt = time.Now()
		cfg.NormalizeNode(&n)
		cfg.Nodes = append(cfg.Nodes, n)
		byKey[key] = len(cfg.Nodes) - 1
		present[len(cfg.Nodes)-1] = true
		st.Added++
	}

	// 本次没出现的本订阅节点 → 标记"机场已下架"（不删）
	for i := range cfg.Nodes {
		if cfg.Nodes[i].SubID != subID || present[i] {
			continue
		}
		if !cfg.Nodes[i].SubMissing {
			cfg.Nodes[i].SubMissing = true
			st.Missing++
		}
	}
	return st
}

// mergeRemote 用订阅里的新数据刷新"远端字段"，保留所有"本机状态"。
func mergeRemote(old, in config.Node) config.Node {
	out := old

	// 名字：用户改过就保留用户的。
	// 判据是"当前名字 ≠ 上次导入时机场给的名字" —— 所以我们每次都会把机场这次给的名字
	// 记进 NameFromSub，用户改名之后就再也不会被订阅覆盖回去。
	customized := old.Name != "" && old.NameFromSub != "" && old.Name != old.NameFromSub
	if !customized || in.Name == "" {
		out.Name = in.Name
	}
	out.NameFromSub = in.Name

	// ---- 远端字段：整体换成机场这次给的 ----
	out.Protocol = in.Protocol
	out.Server = in.Server
	out.Port = in.Port
	out.Username = in.Username
	out.Password = in.Password
	out.UUID = in.UUID
	out.AlterID = in.AlterID
	out.Security = in.Security
	out.Flow = in.Flow
	out.Method = in.Method
	out.UpMbps = in.UpMbps
	out.DownMbps = in.DownMbps
	out.Obfs = in.Obfs
	out.ObfsPassword = in.ObfsPassword
	out.PinSHA256 = in.PinSHA256
	out.Ports = in.Ports
	out.Transport = in.Transport
	out.TLS = in.TLS
	out.WireGuard = in.WireGuard

	// ---- 订阅归属 ----
	out.SubMissing = false // 回来了就清掉"已下架"
	out.SubOrder = in.SubOrder

	// 【刻意不动】ListenHost / ListenPort / ListenUsername / ListenPassword（本地端口鉴权）
	// / Front（前置）/ Active / Remark / 今日统计 / 测试缓存 / CreatedAt ——
	// 这些是用户与本机的状态，订阅更新不该覆盖它们。
	return out
}

// pendingSubNodeIDs 返回某个订阅里"还没分配端口"的节点 ID（测试与界面用）。
func pendingSubNodeIDs(cfg *config.Config, subID string) []string {
	var out []string
	for _, n := range cfg.Nodes {
		if n.SubID == subID && n.PortPending() {
			out = append(out, n.ID)
		}
	}
	return out
}
