package app

import (
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/murongruolan/picohop/internal/config"
)

// TestActivateRefusesAnonymousLanProxy 从**用户视角**锁死审查 #22：
// 打开「允许局域网」（监听地址被 ListenHostEffective 换成 0.0.0.0）
// 而节点没配齐本地端口鉴权时，激活必须明确失败并说清怎么改 ——
// 而不是悄悄在 0.0.0.0 上开一个不校验任何凭据的代理。
//
// 【为什么不止测 proxy.NewServer】那一层只证明"拒绝逻辑存在"。真正让用户
// 踩坑的是 app 这条链路：startNode 解析监听地址 → 构造 → 启动。要是有人
// 把 n.ListenHost = a.cfg.ListenHostEffective(n) 挪走、或者把构造失败吞掉，
// 只有走完整链路的用例才会红。
//
// 【允许的那一侧（AllowLan + 凭据配齐）为什么放在 proxy 包测】那需要真的
// 把端口绑到 0.0.0.0，会弹 Windows 防火墙对话框；构造层已经覆盖它了。
func TestActivateRefusesAnonymousLanProxy(t *testing.T) {
	base := freeBase(t, 15400, 1)
	a := newTestApp(t, []config.Node{{
		ID: "n1", Name: "没配鉴权", Protocol: config.ProtocolSOCKS5,
		ListenHost: "127.0.0.1", ListenPort: base,
		Server: "127.0.0.1", Port: 1, Front: config.FrontDirect,
	}}, config.Front{Type: config.FrontTypeDirect})

	// 打开「允许局域网」——此后该节点的实际监听地址就是 0.0.0.0
	s := a.Settings()
	s.AllowLan = true
	if err := a.SetSettings(s); err != nil {
		t.Fatalf("保存设置失败：%v", err)
	}

	err := a.Activate([]string{"n1"})
	if err == nil {
		t.Fatal("「允许局域网」打开 + 空凭据，激活却成功了 —— " +
			"局域网里任何人都能匿名借这台机器上网（审查 #22）")
	}
	if !strings.Contains(err.Error(), "本地端口鉴权") {
		t.Fatalf("报错必须告诉用户去哪儿补鉴权，实际：%v", err)
	}

	if srv := a.serverOf("n1"); srv != nil && srv.Running() {
		t.Fatalf("被拒绝的节点不该留下正在监听的实例：%s", srv.Addr())
	}

	// 端口必须还是空的：证明是"压根没 bind"，不是"绑了又关"
	ln, lerr := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(base)))
	if lerr != nil {
		t.Fatalf("被拒绝后端口 %d 仍被占用（说明监听套接字真的创建过）：%v", base, lerr)
	}
	_ = ln.Close()

	// 界面上那一行要能看到原因：节点列表是用户唯一的反馈面
	views := a.Nodes()
	if len(views) != 1 {
		t.Fatalf("节点视图应当只有 1 行，实际 %d", len(views))
	}
	if views[0].State != StateError || !strings.Contains(views[0].Detail, "本地端口鉴权") {
		t.Fatalf("节点行没把原因显示出来：state=%q detail=%q", views[0].State, views[0].Detail)
	}

	// 反过来：关掉「允许局域网」后，同一个节点（依然没有凭据）必须照常启动 ——
	// 修的是"对局域网开放"，不是把本机用法一起改死（fail-closed 不能变成 fail-all）。
	s.AllowLan = false
	if err := a.SetSettings(s); err != nil {
		t.Fatalf("保存设置失败：%v", err)
	}
	if err := a.Activate([]string{"n1"}); err != nil {
		t.Fatalf("AllowLan 关着时应当照常激活（监听地址是 127.0.0.1）：%v", err)
	}
	srv := a.serverOf("n1")
	if srv == nil || !srv.Running() {
		t.Fatal("AllowLan 关着时节点没有起来")
	}
	host, _, herr := net.SplitHostPort(srv.Addr())
	if herr != nil {
		t.Fatalf("解析实际监听地址 %q 失败：%v", srv.Addr(), herr)
	}
	if host != "127.0.0.1" {
		t.Fatalf("AllowLan 关着时应当只听回环，实际绑在 %s", srv.Addr())
	}
}
