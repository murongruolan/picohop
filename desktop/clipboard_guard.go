package main

import (
	"sync"
	"time"
)

// sensitiveClipboardTTL 是敏感内容（分享链接、UUID、私钥）允许留在剪贴板上的最长时间（审查 #25）。
//
// 【为什么是 60 秒】粘贴到别的程序通常几秒内就完成；再往后，用户"复制完就忘了"
// 的概率随时间快速上升。而分享链接 / 凭据不是一段普通文本：任何能读剪贴板的进程
// 都能顺手拿走它，拿走之后它就是一条可以长期使用的连接凭据。
const sensitiveClipboardTTL = 60 * time.Second

// clipboardGuard 负责「敏感内容复制后延迟清理剪贴板」（审查 #25）。
//
// 【为什么这里要开一个"清剪贴板"的例外】项目里其余复制（日志、本地代理地址、
// 端口）一律不清：那些内容用户多半要反复粘贴，清掉只会逼他重来一次。只有
// 分享链接 / 凭据（UUID、WireGuard 私钥）不同 —— 它们是可长期使用的凭据，
// 粘出去之后没有任何理由继续躺在剪贴板里。
//
// 【为什么不是"到点无条件清空"】这 60 秒里用户随时可能已经复制了别的东西
// （例如把链接发出去后又复制了一段文字）。无条件清空会把**他自己的**内容吃掉，
// 那是数据丢失，比"留在剪贴板里"更糟。所以到点先读回来比对：只有剪贴板里
// 还是我们放进去的那一段，才清。
//
// 【已知做不到的】Windows 的剪贴板历史（Win+V）与云同步会在内容变化时自己留存
// 副本，那部分存在于别的进程里，我们清不掉 —— 这个改动只是把"明文停留在系统
// 剪贴板上"的时间窗从"直到下次复制"缩短到 60 秒。
//
// 【为什么 read/write 是注入的函数】这样清理逻辑不必抓着 Wails 对象，
// 能在没有 GUI 的单元测试里跑（见 clipboard_guard_test.go）。
type clipboardGuard struct {
	mu    sync.Mutex
	read  func() (string, bool)
	write func(string) bool
	ttl   time.Duration

	timer *time.Timer
	// ours 是我们最近放进剪贴板的那段敏感内容；空串表示"没有待清理的东西"。
	ours string
	// gen 每次复制自增一次。定时器到点后要拿它确认"这期间没有新的复制"。
	gen uint64
}

func newClipboardGuard(read func() (string, bool), write func(string) bool, ttl time.Duration) *clipboardGuard {
	return &clipboardGuard{read: read, write: write, ttl: ttl}
}

// copySensitive 把敏感内容写进剪贴板，并安排 ttl 之后的清理。
//
// 连续复制多条（先复制 UUID、再复制私钥）时只有一个待清理窗口：每次都把上一次的
// 定时器停掉重新计时，否则先到的那次会把用户后来复制的内容当"我们的"处理。
func (g *clipboardGuard) copySensitive(text string) bool {
	if g == nil || g.write == nil || text == "" {
		return false
	}
	if !g.write(text) {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ours = text
	g.gen++
	if g.timer != nil {
		g.timer.Stop()
	}
	g.timer = time.AfterFunc(g.ttl, g.clearIfStillOurs)
	return true
}

// clearIfStillOurs 是定时器到点后跑的函数：只有当剪贴板里还是我们放进去的那段内容时，才清掉它。
func (g *clipboardGuard) clearIfStillOurs() {
	if g == nil {
		return
	}
	g.mu.Lock()
	want, gen := g.ours, g.gen
	g.mu.Unlock()

	if want == "" {
		return
	}
	// 【不持锁调用剪贴板】读/写剪贴板要走 Wails 的主线程调度，可能阻塞；
	// 持着 g.mu 去等它，会把后续每一次 copySensitive 一起卡住。
	cur, ok := g.read()
	if !ok {
		// 读不到就别乱写：此时写入有可能把用户刚复制的内容顶掉，
		// 而我们连"这是不是我们那段"都无法确认（fail-closed：宁可不清）。
		return
	}
	if cur != want {
		return
	}

	// 【写之前再确认一次】从读到写之间用户可能又复制了一次（哪怕是同一段内容），
	// 那已经属于"新的一次复制"，该由新的定时器负责清理，不能被这一次提前清掉。
	g.mu.Lock()
	stillCurrent := g.gen == gen
	if stillCurrent {
		g.ours = ""
		g.timer = nil
	}
	g.mu.Unlock()
	if !stillCurrent {
		return
	}
	// 注意：这里到 write 之间还有一个极窄的窗口（用户恰好在这一瞬间复制了东西）。
	// 做到彻底无窗口就必须持着锁写剪贴板，那正是上面刻意避开的事；权衡后
	// 选择保留这个可忽略的窗口，而不是把 Wails 主线程调度和本锁缠在一起。
	g.write("")
}

// release 停掉待清理的定时器（程序退出时调用）。
//
// 【退出时只停表、不清剪贴板】ServiceShutdown 阶段 Wails 的主循环正在收尾，
// 此时再走 Clipboard.Text/SetText 要等主线程调度，可能一直等不到而把退出卡住
// （系统代理还没还原完就被卡住，比"剪贴板上多留一段链接"严重得多）。
func (g *clipboardGuard) release() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.timer != nil {
		g.timer.Stop()
		g.timer = nil
	}
	g.ours = ""
}
