package app

import (
	"net"
	"testing"
	"time"

	"chainproxy/internal/config"
)

// TestStartNodeForcesLoopbackWhenAllowLanOff 是从"到底 bind 了哪块网卡"这一层
// 锁死缺陷 #9（AllowLan 关闭时不强制回环）的回归测试。
//
// 【为什么不能只测 config.ListenHostEffective】只测那个纯函数的话，
// 修好它、却把 startNode 里那句 `n.ListenHost = a.cfg.ListenHostEffective(n)`
// 删掉/改掉，测试照样全绿 —— 而用户真正面对的开放代理又回来了。
// 这里走完整链路：配置文件里写死一个非回环地址 → 激活 → 读服务**实际**
// 绑定的地址（Server.Addr() 取的是 ln.Addr()），必须只落在回环上。
//
// 【老代码在这条链路上会怎么红】ListenHost=192.168.1.10 会被原样交给
// net.Listen：本机没有这块网卡就直接绑不上（激活报错），万一有这块网卡
// 就真的绑上去（断言 Addr() 不是 127.0.0.1 而失败）。两种都不会被放过。
func TestStartNodeForcesLoopbackWhenAllowLanOff(t *testing.T) {
	base := freeBase(t, 15200, 1)
	a := newTestApp(t, []config.Node{{
		ID: "n1", Name: "手写了局域网地址", Protocol: config.ProtocolSOCKS5,
		// 手工编辑 data/config.json、或旧版本残留带进来的非回环地址
		ListenHost: "192.168.1.10",
		ListenPort: base,
		Server:     "127.0.0.1", Port: 1,
		Front: config.FrontDirect,
	}}, config.Front{Type: config.FrontTypeDirect})

	if a.Settings().AllowLan {
		t.Fatal("测试前提不成立：AllowLan 默认必须是关的")
	}

	if err := a.Activate([]string{"n1"}); err != nil {
		t.Fatalf("AllowLan 关着时激活失败（监听地址没被拉回回环，就会去绑不存在的网卡）：%v", err)
	}
	srv := a.serverOf("n1")
	if srv == nil || !srv.Running() {
		t.Fatal("节点没有起来")
	}

	host, _, err := net.SplitHostPort(srv.Addr())
	if err != nil {
		t.Fatalf("解析实际监听地址 %q 失败：%v", srv.Addr(), err)
	}
	if host != "127.0.0.1" {
		t.Fatalf("AllowLan 是关的，实际却绑在 %s 上 —— 代理已经对局域网敞开（缺陷 #9）", srv.Addr())
	}

	// 再确认这个地址真的能连上：证明是"实际监听的地址"，不是配置里那个好看的值。
	conn, err := net.DialTimeout("tcp", srv.Addr(), 3*time.Second)
	if err != nil {
		t.Fatalf("连不上实际监听地址 %s：%v", srv.Addr(), err)
	}
	_ = conn.Close()
}
