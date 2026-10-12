package engine

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"chainproxy/internal/config"
)

// gateOutbound 是一个"拨号会卡在 DialContext 里"的假出口。
//
// 它存在的唯一目的是把"这个出口正在被使用"这个瞬间拉长到可以稳定断言，
// 从而在**没有真实节点、也没有 -race**（本项目按 CGO_ENABLED=0 构建，
// -race 需要 cgo）的前提下，检验 Upstream 的启停与拨号之间的互斥关系。
type gateOutbound struct {
	entered chan struct{} // 进入 DialContext 时投一个信号（带缓冲，可被多次使用）
	release chan struct{} // 测试放行这次拨号
}

func (g *gateOutbound) Type() string           { return "gate" }
func (g *gateOutbound) Tag() string            { return tagNode }
func (g *gateOutbound) Network() []string      { return []string{N.NetworkTCP} }
func (g *gateOutbound) Dependencies() []string { return nil }

func (g *gateOutbound) DialContext(ctx context.Context, _ string, _ M.Socksaddr) (net.Conn, error) {
	select {
	case g.entered <- struct{}{}:
	default:
	}
	select {
	case <-g.release:
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (g *gateOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, fmt.Errorf("测试用假出口不支持 UDP")
}

// gateUpstream 造一个内部状态已就绪（ob 指向假出口）的 Upstream。
//
// 用假出口而不是真起一个 sing-box 实例：这里要断言的是**锁的时序**，
// 不是内核行为；真的拉实例只会引入网络与 goroutine 的噪声。
func gateUpstream(gate *gateOutbound) *Upstream {
	return &Upstream{
		node: config.Node{Name: "n", Protocol: config.ProtocolVLESS},
		logf: func(string, ...any) {},
		ob:   gate,
	}
}

// TestStopWaitsForInFlightDial 锁死「出口的取用与失效在同一把锁下」（审查 #26）。
//
// 缺陷原型：outbound() 在锁内只把 u.ob 指针取出来，**解锁之后**才拿它去 DialContext；
// 而 Stop 会把 u.inst/u.ob 清空并 Close 掉整个 sing-box 实例 —— 两者之间没有任何同步。
// 于是"节点停止/重启时正好有一次拨号在途"（很常见：socksConnect 在自己的
// goroutine 里拨号，而 stopNode/restartNode 随时可能被点）就会在**正在被拆掉的
// 实例**上拨号（use-after-close）：轻则报一个与真实原因无关的偶发失败，
// 重则在带内部状态的出口（hysteria2/QUIC、多路复用的 ws/grpc）上踩到已释放资源。
//
// 这个测试用假出口把 DialContext 卡住，精确制造那个瞬间：
//   - 旧实现：Stop 立刻返回（拨号还在跑）→ 本测试失败；
//   - 修好后：Stop 必须等这次拨号收尾（写锁等读锁），
//     并且 Stop 之后再拨只会得到明确报错，而不是拿到一个已作废的出口。
func TestStopWaitsForInFlightDial(t *testing.T) {
	gate := &gateOutbound{entered: make(chan struct{}, 2), release: make(chan struct{})}
	up := gateUpstream(gate)

	dialDone := make(chan error, 1)
	go func() {
		_, err := up.Dial(context.Background(), nil, "example.com:443")
		dialDone <- err
	}()
	<-gate.entered // 确认拨号已经拿到出口、正在使用它

	stopDone := make(chan error, 1)
	go func() { stopDone <- up.Stop() }()

	// 拨号还没结束，Stop 就不许返回：一旦返回，这次拨号用的出口（连同它所属的实例）
	// 就已经在被清掉/关掉了。
	select {
	case <-stopDone:
		close(gate.release)
		<-dialDone
		t.Fatal("Stop 在拨号进行中就返回了：出口会在使用中被关掉（审查 #26 的 use-after-close 窗口）")
	case <-time.After(300 * time.Millisecond):
	}

	close(gate.release)
	if err := <-dialDone; err != nil {
		t.Fatalf("被 Stop 等的这次拨号不该失败：%v", err)
	}
	select {
	case <-stopDone:
	case <-time.After(3 * time.Second):
		t.Fatal("拨号结束后 Stop 仍未返回（写锁没被释放？）")
	}

	// 失效之后必须明确报错，而不是把一个已经作废的出口再交出去
	if _, err := up.Dial(context.Background(), nil, "example.com:443"); err == nil {
		t.Error("Stop 之后 Dial 必须失败（出口已失效）")
	}
	if _, err := up.DialUDP(context.Background(), nil); err == nil {
		t.Error("Stop 之后 DialUDP 必须失败（出口已失效）")
	}
}

// TestConcurrentDialsAreNotSerialized 锁死「取用侧用读锁，而不是普通互斥锁」（审查 #26）。
//
// 修 use-after-close 最省事的写法是"整个拨号期间持普通 Mutex"，但那样同一节点上的
// 所有连接会互相排队：一次拨号要等到 DialTimeout，后面所有请求都得等它 ——
// 对一个代理来说这是不可接受的行为退化。所以取用侧必须是读锁：
// 多个拨号并行，只有 Stop 需要独占。
func TestConcurrentDialsAreNotSerialized(t *testing.T) {
	gate := &gateOutbound{entered: make(chan struct{}, 2), release: make(chan struct{})}
	up := gateUpstream(gate)

	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, err := up.Dial(context.Background(), nil, "example.com:443")
			done <- err
		}()
	}

	// 两个拨号必须能同时进入 DialContext；持普通 Mutex 的话第二个进不来。
	for i := 0; i < 2; i++ {
		select {
		case <-gate.entered:
		case <-time.After(2 * time.Second):
			close(gate.release)
			t.Fatal("第二个拨号进不来：取用侧把并发拨号串行化了（应当用读锁）")
		}
	}
	close(gate.release)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatalf("并发拨号不该失败：%v", err)
		}
	}
}
