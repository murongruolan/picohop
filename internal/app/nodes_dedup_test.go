package app

import (
	"testing"

	"chainproxy/internal/config"
)

// 本文件是审查 #29 的回归测试：导入查重 + 显式指定的本地端口不许撞车。
//
// 【缺陷】internal/share/link.go 里 DecodeAny 明确写着"不做去重（是否去重由调用方
// 决定）"，而项目里两条导入路径（界面 PortOnlyDialog、CLI import）最终都落到
// App.AddNode 这个唯一的写入口 —— 它既不查"这条链接是不是已经导入过"，
// 也不查"调用方指定的本地端口是不是已经被占了"。
//
// 于是有两个坏结果：
//  1. 同一条链接导入两次，配置里留下两份 server/port/uuid 完全相同的节点；
//  2. CLI `import -port 10810 <多行链接>` 把 10810 塞给这一批里的每一个节点，
//     一批节点全部监听同一个端口，只有第一个能起来，其余的点启动才报
//     "端口绑定失败"（而坏配置已经落盘）。

// TestAddNodeRejectsDuplicateRemote 锁死"同一个远端不许重复导入"。
func TestAddNodeRejectsDuplicateRemote(t *testing.T) {
	base := freeBase(t, 17000, 4)
	a := newTestApp(t, nil, config.Front{})

	// 明显是假值的 uuid：测试里不能出现真凭据
	const (
		uuidA = "11111111-2222-3333-4444-555555555555"
		uuidB = "99999999-2222-3333-4444-555555555555"
	)

	first, err := a.AddNode(config.Node{
		Name: "日本-01", Protocol: config.ProtocolVLESS,
		Server: "jp.example.com", Port: 443, UUID: uuidA, ListenPort: base,
	})
	if err != nil {
		t.Fatalf("第一次导入就失败了：%v", err)
	}

	// 1) 同一个远端再导入一次（别名不同、服务器大小写不同、本地端口也不同）
	//    必须被拦下 —— 用户"以为没导入成功又点了一次"是这条缺陷最常见的触发方式。
	if _, err := a.AddNode(config.Node{
		Name: "日本-01-副本", Protocol: config.ProtocolVLESS,
		Server: "JP.Example.COM", Port: 443, UUID: uuidA, ListenPort: base + 1,
	}); err == nil {
		t.Fatal("同一个远端（协议/服务器/端口/凭据都相同）重复导入应当被拦下")
	}
	if got := a.CountNodes(); got != 1 {
		t.Fatalf("查重拦下时不该写入节点，实际 %d 个", got)
	}

	// 2) 换了凭据 = 另一个账号，是另一个节点，允许导入
	if _, err := a.AddNode(config.Node{
		Name: "日本-02", Protocol: config.ProtocolVLESS,
		Server: "jp.example.com", Port: 443, UUID: uuidB, ListenPort: base + 1,
	}); err != nil {
		t.Fatalf("不同凭据的远端应当允许导入：%v", err)
	}

	// 3) 同一个远端的第二个本地端口走「克隆」：查重只拦"重复导入"，
	//    绝不能把克隆一起拦死。
	if _, err := a.CloneNode(first.ID); err != nil {
		t.Fatalf("克隆应当仍然允许（它就是同一个远端的第二个本地端口）：%v", err)
	}
	if got := a.CountNodes(); got != 3 {
		t.Fatalf("应当有 3 个节点（两份不同凭据 + 一个克隆），实际 %d", got)
	}
}

// TestAddNodeRejectsExplicitListenPortCollision 锁死"显式指定的本地端口不许撞车"。
//
// 这条正是 CLI 批量导入撞端口的根因：AddNode 只在端口为 0 时才走分配器，
// 端口非 0 就直接 append 落盘，一个检查都没有。
func TestAddNodeRejectsExplicitListenPortCollision(t *testing.T) {
	base := freeBase(t, 17500, 3)
	a := newTestApp(t, []config.Node{{
		ID: "a", Name: "已有节点", Protocol: config.ProtocolSOCKS5,
		ListenHost: "127.0.0.1", ListenPort: base, Server: "h", Port: 1,
		Front: config.FrontDirect,
	}}, config.Front{})

	// 撞上本项目里已有节点的本地端口
	if _, err := a.AddNode(config.Node{
		Name: "新节点", Protocol: config.ProtocolSOCKS5, Server: "h2", Port: 1, ListenPort: base,
	}); err == nil {
		t.Fatal("显式指定的本地端口已被本项目别的节点占用时应当报错")
	}
	if got := a.CountNodes(); got != 1 {
		t.Fatalf("报错时不该写入节点，实际 %d 个", got)
	}

	// 空闲端口照常可以新增
	if _, err := a.AddNode(config.Node{
		Name: "新节点", Protocol: config.ProtocolSOCKS5, Server: "h2", Port: 1, ListenPort: base + 1,
	}); err != nil {
		t.Fatalf("空闲端口应当能正常新增：%v", err)
	}

	// 全局前置代理占着的端口同样不许被节点拿去当本地端口：
	// 本地端口撞上前置端口等于"自己拨自己"（见 App.allocatorLocked）。
	b := newTestApp(t, nil, config.Front{
		Type: config.FrontTypeSocks5, Host: "127.0.0.1", Port: base + 2, TimeoutMs: 10000,
	})
	if _, err := b.AddNode(config.Node{
		Name: "新节点", Protocol: config.ProtocolSOCKS5, Server: "h", Port: 1, ListenPort: base + 2,
	}); err == nil {
		t.Fatal("本地端口撞上全局前置代理端口时应当报错")
	}
}
