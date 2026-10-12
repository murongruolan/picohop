package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"chainproxy/internal/config"
)

// TestEngineSelectionAndFallback 锁死换装开关的三条行为：
//
//	默认走 sing-box；mode=mihomo 走 mihomo；mihomo 接不了就回退 sing-box 并留日志。
//
// 全用假工厂：不需要网络，也不需要真内核。
func TestEngineSelectionAndFallback(t *testing.T) {
	origSing, origMihomo, origMode := singFactory, mihomoFactory, engineMode
	defer func() { singFactory, mihomoFactory, engineMode = origSing, origMihomo, origMode }()

	node := config.Node{ID: "n1", Name: "n1", Protocol: config.ProtocolVLESS}
	mk := func(tag string) func(config.Node, config.Front, func(string, ...any)) (Upstream, error) {
		return func(config.Node, config.Front, func(string, ...any)) (Upstream, error) {
			return fakeUpstream{tag: tag}, nil
		}
	}
	singFactory = mk("sing")
	mihomoFactory = mk("mihomo")

	engineMode = ""
	if up, err := NewUpstream(node, config.Front{}, nil); err != nil || up.(fakeUpstream).tag != "sing" {
		t.Fatalf("默认应当走 sing-box，实际 %v / %v", up, err)
	}

	engineMode = "mihomo"
	if up, err := NewUpstream(node, config.Front{}, nil); err != nil || up.(fakeUpstream).tag != "mihomo" {
		t.Fatalf("mode=mihomo 时应当走 mihomo，实际 %v / %v", up, err)
	}

	// mihomo 接不了这个节点 → 必须回退，且必须留下日志
	mihomoFactory = func(config.Node, config.Front, func(string, ...any)) (Upstream, error) {
		return nil, errors.New("mihomo cannot handle this node")
	}
	logged := false
	up, err := NewUpstream(node, config.Front{}, func(string, ...any) { logged = true })
	if err != nil || up.(fakeUpstream).tag != "sing" {
		t.Fatalf("mihomo 失败时应当回退 sing-box，实际 %v / %v", up, err)
	}
	if !logged {
		t.Error("回退时应当写一行日志说明原因，否则用户不知道为什么没用上新内核")
	}
}

// TestFrontRejectionFallsBack 锁死"mihomo 因为前置缺口被拒 → 回退 sing-box"。
//
// 真实的判据在 internal/engine（mihomoFrontGap，那里不允许 import 本包的反向依赖），
// 本包只保证一件事：mihomo 工厂一旦报错就**必须回退**，绝不能被当成
// "这个节点走直连"放行 —— 那正是"配了前置的节点静默从本机 IP 出去"这条路。
func TestFrontRejectionFallsBack(t *testing.T) {
	origSing, origMihomo, origMode := singFactory, mihomoFactory, engineMode
	defer func() { singFactory, mihomoFactory, engineMode = origSing, origMihomo, origMode }()

	singFactory = func(config.Node, config.Front, func(string, ...any)) (Upstream, error) {
		return fakeUpstream{tag: "sing"}, nil
	}
	mihomoFactory = func(config.Node, config.Front, func(string, ...any)) (Upstream, error) {
		return nil, errors.New("节点「n1」的前置指向另一个节点（node-b-id），而 mihomo 适配层还没接前置")
	}
	engineMode = "mihomo"

	node := config.Node{ID: "n1", Name: "n1", Protocol: config.ProtocolVLESS, Front: "node-b-id"}
	var logs []string
	up, err := NewUpstream(node, config.Front{
		Type: config.FrontTypeSocks5, Host: "127.0.0.1", Port: 10808, TimeoutMs: 10000,
	}, func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	})
	if err != nil {
		t.Fatalf("mihomo 因前置被拒时应当回退 sing-box，实际 %v", err)
	}
	if up.(fakeUpstream).tag != "sing" {
		t.Fatalf("必须回退 sing-box，实际 %v", up)
	}
	if joined := strings.Join(logs, "\n"); !strings.Contains(joined, "前置") {
		t.Errorf("回退日志里要带上被拒的原因，实际 %q", joined)
	}
}

type fakeUpstream struct{ tag string }

func (f fakeUpstream) Start(context.Context, Front) error { return nil }
func (f fakeUpstream) Stop() error                        { return nil }
func (f fakeUpstream) Dial(context.Context, Front, string) (net.Conn, error) {
	return nil, errors.New("not implemented")
}
func (f fakeUpstream) DialUDP(context.Context, Front) (UDPChannel, error) {
	return nil, ErrUDPUnsupported
}
func (f fakeUpstream) SupportsUDP() bool          { return false }
func (f fakeUpstream) Describe() string           { return "fake:" + f.tag }
func (f fakeUpstream) DialTimeout() time.Duration { return time.Second }
