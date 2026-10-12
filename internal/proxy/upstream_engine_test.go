package proxy

import (
	"context"
	"errors"
	"net"
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
