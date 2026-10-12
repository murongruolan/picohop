package main

import (
	"sync"
	"testing"
	"time"
)

// fakeClipboard 是最小可用的"剪贴板"替身。
//
// 带锁的原因：清理是定时器协程跑的，测试主协程同时在读它 —— 不带锁在
// `go test` 下就是数据竞争（本项目的 -race 因 CGO_ENABLED=0 用不了，
// 只能靠代码本身别写出竞争）。
type fakeClipboard struct {
	mu     sync.Mutex
	text   string
	ok     bool
	writes []string
}

func newFakeClipboard() *fakeClipboard { return &fakeClipboard{ok: true} }

func (c *fakeClipboard) read() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.text, c.ok
}

func (c *fakeClipboard) write(s string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.text = s
	c.writes = append(c.writes, s)
	return true
}

// set 模拟"用户在这期间自己复制了别的东西"。
func (c *fakeClipboard) set(s string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.text = s
}

func (c *fakeClipboard) get() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.text
}

func (c *fakeClipboard) writeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.writes)
}

// waitFor 轮询等待条件成立。
//
// 【为什么不用"睡够时间再断言"】Windows 的定时器精度很粗（默认 ~15ms），
// 睡固定时长要么白等要么偶发失败；这里给足上限、条件成立就返回。
func waitFor(t *testing.T, limit time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

// TestClipboardGuardClearRules 是审查 #25 的核心回归测试：到点该清的清、
// 不该动的一律不动。
//
// 【为什么必须逐条钉住"不该动"】自动清剪贴板最容易出的事故不是"没清"，
// 而是**把用户自己刚复制的内容吃掉**——那是数据丢失。所以下面每个"不清"
// 的子用例都是防这类回归。
func TestClipboardGuardClearRules(t *testing.T) {
	const secret = "vless://canary-uuid@example.com:443?security=reality#node"

	t.Run("剪贴板里还是我们那段凭据：清掉", func(t *testing.T) {
		cb := newFakeClipboard()
		g := newClipboardGuard(cb.read, cb.write, time.Hour) // 定时器不参与，手动触发
		if !g.copySensitive(secret) {
			t.Fatal("copySensitive 应当成功")
		}
		if got := cb.get(); got != secret {
			t.Fatalf("复制后剪贴板应当是凭据本身，实际 %q", got)
		}
		g.clearIfStillOurs()
		if got := cb.get(); got != "" {
			t.Fatalf("到点后应当被清空，实际残留 %q", got)
		}
	})

	t.Run("用户在这期间复制了别的东西：不许动他的", func(t *testing.T) {
		cb := newFakeClipboard()
		g := newClipboardGuard(cb.read, cb.write, time.Hour)
		g.copySensitive(secret)
		cb.set("用户后来自己复制的一段文字")
		g.clearIfStillOurs()
		if got := cb.get(); got != "用户后来自己复制的一段文字" {
			t.Fatalf("不该动用户自己的剪贴板内容，实际 %q", got)
		}
	})

	t.Run("读不到剪贴板：fail-closed，不写", func(t *testing.T) {
		cb := newFakeClipboard()
		g := newClipboardGuard(cb.read, cb.write, time.Hour)
		g.copySensitive(secret)
		before := cb.writeCount()
		cb.mu.Lock()
		cb.ok = false // 模拟剪贴板被别的程序占着 / 系统暂时读不到
		cb.mu.Unlock()
		g.clearIfStillOurs()
		if cb.writeCount() != before {
			t.Fatal("读不到内容时绝不能盲写剪贴板——可能把用户刚复制的东西顶掉")
		}
	})

	t.Run("空内容不复制也不排清理", func(t *testing.T) {
		cb := newFakeClipboard()
		g := newClipboardGuard(cb.read, cb.write, time.Hour)
		if g.copySensitive("") {
			t.Fatal("空内容不该被当成一次敏感复制")
		}
		if cb.writeCount() != 0 {
			t.Fatalf("空内容不该写剪贴板，实际写了 %d 次", cb.writeCount())
		}
	})
}

// TestClipboardGuardTimerFires 验证"定时器真的接上了"：上面那组用例都是手动
// 触发清理的，如果 AfterFunc 那行写错（比如传了 0 或者忘了挂），它们照样全绿。
func TestClipboardGuardTimerFires(t *testing.T) {
	cb := newFakeClipboard()
	g := newClipboardGuard(cb.read, cb.write, 20*time.Millisecond)
	g.copySensitive("trojan://password@example.com:443")

	if !waitFor(t, 5*time.Second, func() bool { return cb.get() == "" }) {
		t.Fatalf("定时器到点后剪贴板应当被清空，实际仍是 %q", cb.get())
	}
}

// TestClipboardGuardRecopyExtendsDeadline 验证连续复制（先 UUID 再私钥）
// 只有一个待清理窗口，且不会因为先到的那次而清掉后一次的内容。
func TestClipboardGuardRecopyExtendsDeadline(t *testing.T) {
	cb := newFakeClipboard()
	g := newClipboardGuard(cb.read, cb.write, 400*time.Millisecond)

	g.copySensitive("first-secret")
	time.Sleep(250 * time.Millisecond) // 已经过了第一次的 400ms 的一半以上
	g.copySensitive("second-secret")
	time.Sleep(250 * time.Millisecond) // 距第二次复制才 250ms，不该被清

	if got := cb.get(); got != "second-secret" {
		t.Fatalf("第二次复制的内容被提前清掉了，实际 %q", got)
	}
	if !waitFor(t, 5*time.Second, func() bool { return cb.get() == "" }) {
		t.Fatalf("第二次复制安排的那次清理没有发生，实际仍是 %q", cb.get())
	}
}

// TestClipboardGuardConcurrentCopies 是并发冒烟测试：多个协程同时复制时
// 不许 panic / 卡死，且最终留在剪贴板上的只能是某一次复制进去的内容。
func TestClipboardGuardConcurrentCopies(t *testing.T) {
	cb := newFakeClipboard()
	g := newClipboardGuard(cb.read, cb.write, 5*time.Millisecond)

	texts := []string{"a-secret", "b-secret", "c-secret", "d-secret"}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				g.copySensitive(texts[(i+j)%len(texts)])
			}
		}(i)
	}
	wg.Wait()

	got := cb.get()
	known := false
	for _, s := range texts {
		if got == s {
			known = true
			break
		}
	}
	// 允许已经被定时器清空（""），但不允许出现"既不是我们写的、也没被清"的第三种状态。
	if got != "" && !known {
		t.Fatalf("剪贴板里出现了我们没有写过的内容：%q", got)
	}
}

// TestCopySensitiveTextValidatesInput 覆盖 API 包装层不依赖 GUI 就能验的部分：
// 空内容报错、剪贴板不可用时如实报错（绝不假装复制成功）。
func TestCopySensitiveTextValidatesInput(t *testing.T) {
	api := &API{} // wapp 为 nil：与"系统剪贴板不可用"等价

	if _, err := api.CopySensitiveText("   "); err == nil {
		t.Fatal("空内容应当报错")
	}
	if _, err := api.CopySensitiveText("trojan://password@example.com:443"); err == nil {
		t.Fatal("剪贴板不可用时应当报错，而不是假装复制成功")
	}
	// 顺带钉住旧行为没被改坏：普通复制同样在剪贴板不可用时报错。
	if err := api.CopyText("hello"); err == nil {
		t.Fatal("CopyText 在剪贴板不可用时应当报错")
	}
}
