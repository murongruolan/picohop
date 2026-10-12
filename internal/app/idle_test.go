package app

import (
	"context"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"chainproxy/internal/config"
	"chainproxy/internal/proxy"
)

// silentSocks5 起一个"握完手就不说话"的最小 SOCKS5 服务端，代表任何长连接上游。
//
// 它只做三件事：完成 SOCKS5 握手、回成功、然后保持静默 —— 这正是"链路空闲"
// 的场景（长轮询 / SSE / 下载中途没有数据），也是缺陷 #2 里"用着用着就断"的现场。
func silentSocks5(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				// 问候：VER NMETHODS METHOD
				if _, err := io.ReadFull(c, make([]byte, 3)); err != nil {
					return
				}
				if _, err := c.Write([]byte{0x05, 0x00}); err != nil {
					return
				}
				// 请求：VER CMD RSV ATYP + 地址 + 端口（地址要读干净，否则污染后续）
				head := make([]byte, 4)
				if _, err := io.ReadFull(c, head); err != nil {
					return
				}
				var aerr error
				switch head[3] {
				case 0x01:
					_, aerr = io.ReadFull(c, make([]byte, 4))
				case 0x03:
					var l [1]byte
					if _, aerr = io.ReadFull(c, l[:]); aerr == nil {
						_, aerr = io.ReadFull(c, make([]byte, int(l[0])))
					}
				case 0x04:
					_, aerr = io.ReadFull(c, make([]byte, 16))
				default:
					return
				}
				if aerr != nil {
					return
				}
				if _, err := io.ReadFull(c, make([]byte, 2)); err != nil {
					return
				}
				// 成功应答：VER REP RSV ATYP BND.ADDR BND.PORT
				if _, err := c.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
					return
				}
				// 之后一直静默：这就是"空闲链路"
				_, _ = io.Copy(io.Discard, c)
			}(conn)
		}
	}()
	return ln.Addr().String()
}

// TestIdleTimeoutSettingTakesEffect 是审查 #2 的端到端回归测试：
// 「网络设置 → 空闲自动断开」设成 1 秒后，一条静默链路必须在 1 秒左右被断开。
//
// 【为什么必须端到端、而且断言"断开得快"】
// 缺陷是"改了设置不生效 + 用着用着就断"。老代码里空闲超时恒为
// node.Timeout() = 15 秒，所以只断言"最终会被断开"是抓不住的 ——
// 等 15 秒它也会断。只有断言"1 秒级就断"，才能把"读到设置"和"写死 15 秒"分开。
func TestIdleTimeoutSettingTakesEffect(t *testing.T) {
	upAddr := silentSocks5(t)
	host, portStr, err := net.SplitHostPort(upAddr)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}

	// 节点：本地监听口 → 直连 → 上面那个静默的 SOCKS5 上游
	base := freeBase(t, 14000, 1)
	a := newTestApp(t, []config.Node{{
		ID: "n1", Name: "空闲断开", Protocol: config.ProtocolSOCKS5,
		ListenPort: base, Server: host, Port: port, Front: config.FrontDirect,
	}}, config.Front{Type: config.FrontTypeDirect})

	// 秒是最小单位，1 秒已是界面能设的最小值
	s := a.Settings()
	s.IdleTimeoutSec = 1
	if err := a.SetSettings(s); err != nil {
		t.Fatal(err)
	}

	if err := a.Activate([]string{"n1"}); err != nil {
		t.Fatalf("激活节点失败：%v", err)
	}
	srv := a.serverOf("n1")
	if srv == nil || !srv.Running() {
		t.Fatal("节点没有起来")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := proxy.DialSOCKS5(ctx, srv.Addr(), "example.com:80", 5*time.Second)
	if err != nil {
		t.Fatalf("连本地监听口失败：%v", err)
	}
	defer conn.Close()

	// 握完手两边都不发数据：应当约 1 秒断开，而不是老代码的 15 秒
	start := time.Now()
	_ = conn.SetReadDeadline(time.Now().Add(8 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("静默链路没有被断开：空闲超时根本没生效")
	}
	elapsed := time.Since(start)
	if elapsed > 5*time.Second {
		t.Fatalf("静默链路 %v 才断开，说明没用上设置里的 1 秒（老缺陷恒为 15 秒）", elapsed)
	}
}
