package config

import (
	"strings"
	"time"
)

// DefaultSubscriptionUA 是订阅下载默认使用的 User-Agent。
//
// 【为什么不是 "PicoHop/1.0.0"】机场普遍按 UA 决定给你什么格式，甚至决定给你**哪些节点**。
// 用使用者给的真实机场实测过：同一家机场，clash 系 UA 拿到 40 个节点（含 21 个 hysteria2），
// 而 v2ray 系 UA 只给 19 个 vless —— UA 不只是格式开关，而是"给不给你某些协议"的开关。
// 另外，UA 里含 "clash" 还是能拿到 subscription-userinfo（流量/到期）响应头的前提。
//
// 我们内嵌的内核正是 mihomo，用它的标识最贴切（不冒充别的客户端）；设置里可逐条改。
const DefaultSubscriptionUA = "clash.meta/mihomo-1.19.32"

// SubscriptionUserInfo 是机场通过 subscription-userinfo 响应头给的套餐用量。
//
// 【单位】Upload/Download/Total 是**字节**，Expire 是 **Unix 秒**。
// 缺失字段一律为 0 —— 实测确实有机场把 expire 留空（表示长期有效），
// 也有机场用 4102329600（2100 年）表示永久，解析与展示都要容忍这两种写法。
type SubscriptionUserInfo struct {
	Upload   int64 `json:"upload,omitempty"`
	Download int64 `json:"download,omitempty"`
	Total    int64 `json:"total,omitempty"`
	Expire   int64 `json:"expire,omitempty"`
}

// Used 返回已用流量（字节）。
func (u SubscriptionUserInfo) Used() int64 { return u.Upload + u.Download }

// Remaining 返回剩余流量（字节）；Total 未知（0）时返回 0，调用方据此显示"—"。
func (u SubscriptionUserInfo) Remaining() int64 {
	if u.Total <= 0 {
		return 0
	}
	if r := u.Total - u.Used(); r > 0 {
		return r
	}
	return 0
}

// ExpireTime 把 Expire 转成时间；0（或明显不合理的过去值）返回零值，表示"没有到期时间"。
func (u SubscriptionUserInfo) ExpireTime() time.Time {
	if u.Expire <= 0 {
		return time.Time{}
	}
	return time.Unix(u.Expire, 0)
}

// Subscription 是一条机场订阅。
//
// 【URL 是凭据】订阅链接里带着个人 token，等于账号口令。它**不进日志、不进错误信息**，
// 界面里默认打码（见 subscribe.MaskURL）。这个结构体里其它字段都可以见光。
type Subscription struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`

	// UserAgent 空 = 用设置里的默认值（再空则用 DefaultSubscriptionUA）。
	UserAgent string `json:"userAgent,omitempty"`
	// ViaProxy 为真时经本机某个代理端口下载订阅。默认**关**（直连优先，
	// 避免"订阅域名只能在代理下访问、而代理又还没起来"这种死锁体验；
	// 真要经代理，用户显式打开即可）。
	ViaProxy bool `json:"viaProxy,omitempty"`

	// UpdateMinutes 是自动更新间隔（分钟）；0 = 不自动更新。
	UpdateMinutes int `json:"updateMinutes,omitempty"`
	// LastUpdate 是上次**成功**更新的时间（零值 = 从没成功过）。
	LastUpdate time.Time `json:"lastUpdate,omitempty"`
	// LastError 是上次失败的原因（成功一次即清空）。**不含 URL**。
	LastError string `json:"lastError,omitempty"`

	// ---- 以下都是"上次更新的观察结果"，只服务界面显示与排查 ----
	// Format 上次识别到的形态：clash-yaml / links / sip008 / html / unknown。
	Format string `json:"format,omitempty"`
	// Skipped 上次被跳过的**协议**计数（我们暂不支持的协议），键是协议名。
	Skipped map[string]int `json:"skipped,omitempty"`
	// FakeRows 上次被当作"机场信息行"过滤掉的条数（"剩余流量：…""套餐到期：…"之类）。
	FakeRows int `json:"fakeRows,omitempty"`
	// Missing 上次更新时被判为"机场已下架"的节点数。
	Missing int `json:"missing,omitempty"`

	// UserInfo / WebPageURL / UpdateIntervalHours 取自响应头。
	UserInfo            SubscriptionUserInfo `json:"userInfo,omitempty"`
	WebPageURL          string               `json:"webPageUrl,omitempty"`
	UpdateIntervalHours int                  `json:"updateIntervalHours,omitempty"`

	// Sort 是订阅之间的显示顺序；Disabled 为真表示不自动更新（节点仍在）。
	//
	// 【为什么是 Disabled 而不是 Enabled】JSON 里布尔零值就是 false，
	// 用 Enabled 会让"老配置没这个字段"与"用户明确关掉了"混在一起。
	Sort     int  `json:"sort,omitempty"`
	Disabled bool `json:"disabled,omitempty"`
}

// EffectiveUA 返回这条订阅实际使用的 UA：订阅自己的 → 设置里的 → 内置默认值。
func (s Subscription) EffectiveUA(fallback string) string {
	if ua := strings.TrimSpace(s.UserAgent); ua != "" {
		return ua
	}
	if ua := strings.TrimSpace(fallback); ua != "" {
		return ua
	}
	return DefaultSubscriptionUA
}

// FindSubscription 按 ID 找订阅，找不到返回 nil。
func (c *Config) FindSubscription(id string) *Subscription {
	for i := range c.Subscriptions {
		if c.Subscriptions[i].ID == id {
			return &c.Subscriptions[i]
		}
	}
	return nil
}

// NormalizeSubscription 给一条订阅补默认值：ID、名字、排序。
//
// 与 NormalizeNode 同一套路（写入路径显式调用，校验路径不回写）：
//
//	新建订阅   App.ImportSubscription
//	载入配置   config.Load -> normalize 里不逐个处理（订阅比节点简单得多，
//	           ID/Name 缺失只影响显示，不影响能不能跑）
func (c *Config) NormalizeSubscription(s *Subscription) {
	if s == nil {
		return
	}
	if strings.TrimSpace(s.ID) == "" {
		s.ID = NewID()
	}
	if strings.TrimSpace(s.Name) == "" {
		s.Name = "订阅"
	}
	if s.Sort <= 0 {
		max := 0
		for _, e := range c.Subscriptions {
			if e.ID != s.ID && e.Sort > max {
				max = e.Sort
			}
		}
		s.Sort = max + 1
	}
}
