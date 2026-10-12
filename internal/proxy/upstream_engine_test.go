package proxy

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"chainproxy/internal/config"
)

// TestUpstreamHasNoFallback 锁死"没有第二个内核、也不许假装能跑"。
//
// 【为什么这条测试比什么都重要】引擎只有一个（mihomo）。如果工厂失败时被放行成
// "直连"，配了前置的节点就会用本机 IP 出去 —— 完全静默。所以这里断言：
// 工厂报错 → NewUpstream 就把错误交出去（上层的表现是"这个节点启动失败"），
// 而不是退回任何别的实现。
func TestUpstreamHasNoFallback(t *testing.T) {
	origMihomo := mihomoFactory
	defer func() { mihomoFactory = origMihomo }()

	node := config.Node{ID: "n1", Name: "n1", Protocol: config.ProtocolVLESS, Front: "node-b-id"}
	front := config.Front{Type: config.FrontTypeSocks5, Host: "127.0.0.1", Port: 10808, TimeoutMs: 10000}

	// ① 工厂报错（例如 Hysteria2 配了只能走 TCP 的 HTTP 前置）→ 原样把错误交出去。
	mihomoFactory = func(config.Node, config.Front, func(string, ...any)) (Upstream, error) {
		return nil, errors.New("mihomoFrontPlan 拒绝：Hysteria2 需要 UDP 前置")
	}
	up, err := NewUpstream(node, front, nil)
	if err == nil {
		t.Fatalf("内核接不了就必须失败，绝不能放行成直连，实际拿到 %v", up)
	}
	if !strings.Contains(err.Error(), "Hysteria2") {
		t.Fatalf("应当把内核给的原因交出去，实际 %v", err)
	}

	// ② 工厂存在且成功 → 就用它（没有任何"优先/回退"的中间态）。
	mihomoFactory = func(config.Node, config.Front, func(string, ...any)) (Upstream, error) {
		return fakeUpstream{tag: "mihomo"}, nil
	}
	up, err = NewUpstream(node, front, nil)
	if err != nil {
		t.Fatalf("工厂能构造时不该失败：%v", err)
	}
	if got := up.(fakeUpstream).tag; got != "mihomo" {
		t.Fatalf("应当直接用 mihomo 出口，实际 %q", got)
	}

	// ③ 没有注册工厂（例如只编了 proxy 包）→ 明确报"当前构建未包含"，
	//    而不是悄悄退化成直连。
	mihomoFactory = nil
	if up, err := NewUpstream(node, front, nil); err == nil {
		t.Fatalf("没有内核工厂时必须明确报错，实际拿到 %v", up)
	}
}

// TestFrontedNodeKeepsFrontReachingEngine 锁死"前置会被原样交给内核"。
//
// 【为什么值得单测】mihomo 出口要在**构造时**把前置装进自己的拨号器；如果
// NewUpstream 把 frontCfg 吞掉（例如传零值），配了前置的节点就会静默直连 ——
// 这条断言把"前置必须传到工厂"钉在接口层。
func TestFrontedNodeKeepsFrontReachingEngine(t *testing.T) {
	origMihomo := mihomoFactory
	defer func() { mihomoFactory = origMihomo }()

	var got config.Front
	mihomoFactory = func(_ config.Node, f config.Front, _ func(string, ...any)) (Upstream, error) {
		got = f
		return fakeUpstream{tag: "mihomo"}, nil
	}
	want := config.Front{Type: config.FrontTypeSocks5, Host: "127.0.0.1", Port: 10810, TimeoutMs: 10000}
	if _, err := NewUpstream(config.Node{ID: "n1", Name: "n1", Protocol: config.ProtocolVMess}, want, nil); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("前置必须原样交给内核工厂，实际 %+v", got)
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
