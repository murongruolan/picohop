package proxy

import (
	"testing"
	"time"

	"chainproxy/internal/config"
)

// stubSingFactory 把内核工厂换成一个不联网的假出口，用完恢复。
//
// 【为什么要 stub】NewServer 会先构造出口；VLESS 这类协议在只跑 proxy 包的
// 测试里没有注册内核工厂，会直接构造失败 —— 那测的就不是空闲超时了。
func stubSingFactory(t *testing.T) {
	t.Helper()
	origSing, origMode := singFactory, engineMode
	singFactory = func(config.Node, config.Front, func(string, ...any)) (Upstream, error) {
		return fakeUpstream{tag: "fake"}, nil
	}
	engineMode = ""
	t.Cleanup(func() { singFactory, engineMode = origSing, origMode })
}

// TestServerIdleTimeoutComesFromSettings 锁死审查 #2：
// 服务用哪段空闲超时，必须来自调用方传进来的值（= Settings.IdleTimeoutSec），
// 不能再拿 node.Timeout() 顶替。
//
// 【以前为什么是错的】node.Timeout() 是**拨号**超时（固定 15s）。拿它当空闲超时，
// 用户把「空闲自动断开」从 300 改成任何值都不生效，而链路只要静默 15 秒就被掐断。
func TestServerIdleTimeoutComesFromSettings(t *testing.T) {
	stubSingFactory(t)
	node := config.Node{ID: "n1", Name: "n1", Protocol: config.ProtocolVLESS}

	s, err := NewServer(node, config.Front{Type: config.FrontTypeDirect}, 240*time.Second, nil)
	if err != nil {
		t.Fatalf("构造服务失败：%v", err)
	}
	if got := s.handler.Idle; got != 240*time.Second {
		t.Fatalf("空闲超时应为传进来的 240s，实际 %v", got)
	}
	if dial := time.Duration(node.Timeout()) * time.Millisecond; s.handler.Idle == dial {
		t.Fatalf("空闲超时又变成拨号超时（%v）了 —— 用户的设置会再次失效", dial)
	}
}

// TestServerIdleTimeoutFailsClosed 确认 0 / 负数不会被当成"永不超时"。
//
// 【为什么不许透传】Relay 里 idle <= 0 = 不设空闲超时。参数漏传时如果原样透传，
// 就从"15 秒就断"变成"永远不断"，死连接再也不回收 —— 比原缺陷更糟。
func TestServerIdleTimeoutFailsClosed(t *testing.T) {
	def := time.Duration(config.DefaultIdleTimeoutSec) * time.Second

	for _, idle := range []time.Duration{0, -time.Second} {
		stubSingFactory(t)
		node := config.Node{ID: "n1", Name: "n1", Protocol: config.ProtocolVLESS}
		s, err := NewServer(node, config.Front{Type: config.FrontTypeDirect}, idle, nil)
		if err != nil {
			t.Fatalf("构造服务失败：%v", err)
		}
		if got := s.handler.Idle; got != def {
			t.Fatalf("空闲超时 %v 应拉回默认值 %v，实际 %v", idle, def, got)
		}
		if s.handler.Idle <= 0 {
			t.Fatalf("空闲超时不许是非正数（那等于永不超时），实际 %v", s.handler.Idle)
		}
	}

	// 正数必须原样保留
	if got := idleTimeoutOrDefault(90 * time.Second); got != 90*time.Second {
		t.Fatalf("正数应原样保留，实际 %v", got)
	}
}
