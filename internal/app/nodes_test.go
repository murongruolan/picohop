package app

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"chainproxy/internal/config"
)

// newTestApp 造一个干净的 App 实例。
//
// 把 CHAINPROXY_DATA_DIR 指到 t.TempDir()，避免测试往项目真实的
// data\ 目录里写日志和配置。
func newTestApp(t *testing.T, nodes []config.Node, front config.Front) *App {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CHAINPROXY_DATA_DIR", dir)

	cfg := config.Default()
	cfg.Nodes = nodes
	if front.Type != "" {
		cfg.Front = front
	}
	path := filepath.Join(dir, "config.json")
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	a, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a
}

// TestPlanActivationOrdersFrontFirst 锁死"前置必须先启动"。
//
// 这是「前置节点」功能的全部意义：A 是 B 的前置、B 是 C 的前置，
// 那么启动 C 时必须按 A → B → C 的顺序把三个都拉起来。
func TestPlanActivationOrdersFrontFirst(t *testing.T) {
	a := newTestApp(t, []config.Node{
		{ID: "a", Name: "A", Protocol: config.ProtocolSOCKS5, ListenPort: 10810, Server: "h", Port: 1},
		{ID: "b", Name: "B", Protocol: config.ProtocolSOCKS5, ListenPort: 10811, Server: "h", Port: 1, Front: "a"},
		{ID: "c", Name: "C", Protocol: config.ProtocolSOCKS5, ListenPort: 10812, Server: "h", Port: 1, Front: "b"},
	}, config.Front{})

	plan, err := a.PlanActivation([]string{"c"})
	if err != nil {
		t.Fatalf("规划失败：%v", err)
	}
	want := []string{"a", "b", "c"}
	if len(plan.Order) != len(want) {
		t.Fatalf("顺序长度应为 %d，实际 %v", len(want), plan.Order)
	}
	for i := range want {
		if plan.Order[i] != want[i] {
			t.Fatalf("启动顺序应为 %v，实际 %v", want, plan.Order)
		}
	}
	// a、b 是因为"被依赖"而自动带上的，界面要据此提示用户
	if len(plan.Auto) != 2 || plan.Auto[0] != "a" || plan.Auto[1] != "b" {
		t.Fatalf("自动带上的节点应为 [a b]，实际 %v", plan.Auto)
	}

	// 只启动中间的 B，也应当只带上 A
	plan2, err := a.PlanActivation([]string{"b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan2.Order) != 2 || plan2.Order[0] != "a" || plan2.Order[1] != "b" {
		t.Fatalf("启动 B 的顺序应为 [a b]，实际 %v", plan2.Order)
	}
}

// TestPlanActivationDetectsCycle 锁死环路检测。
//
// 环路会让"自动激活前置"无限递归，必须在规划阶段就拦下来。
func TestPlanActivationDetectsCycle(t *testing.T) {
	a := newTestApp(t, []config.Node{
		{ID: "a", Name: "A", Protocol: config.ProtocolSOCKS5, ListenPort: 10810, Server: "h", Port: 1, Front: "b"},
		{ID: "b", Name: "B", Protocol: config.ProtocolSOCKS5, ListenPort: 10811, Server: "h", Port: 1, Front: "a"},
	}, config.Front{})

	if _, err := a.PlanActivation([]string{"a"}); err == nil {
		t.Fatal("前置成环时应当报错")
	}
	// 配置校验也应当发现它（两道防线）
	issues := a.Validate()
	found := false
	for _, i := range issues {
		if i.Field == "front" {
			found = true
		}
	}
	if !found {
		t.Fatalf("配置校验也应当报出前置环：%v", issues)
	}
}

// TestResolveFront 锁死三种前置来源的解析结果。
func TestResolveFront(t *testing.T) {
	global := config.Front{
		Type: config.FrontTypeSocks5, Host: "127.0.0.1", Port: 10808,
		Username: "gu", Password: "gp", TimeoutMs: 10000,
	}
	a := newTestApp(t, []config.Node{
		{
			ID: "a", Name: "A", Protocol: config.ProtocolSOCKS5, ListenPort: 10810,
			Server: "h", Port: 1, Front: config.FrontDirect,
		},
		{
			ID: "b", Name: "B", Protocol: config.ProtocolSOCKS5, ListenPort: 10811,
			Server: "h", Port: 1, Front: config.FrontGlobal,
		},
		{
			ID: "c", Name: "C", Protocol: config.ProtocolSOCKS5, ListenPort: 10812,
			Server: "h", Port: 1, Front: "a",
			ListenUsername: "lu", ListenPassword: "lp",
		},
		{
			ID: "d", Name: "D", Protocol: config.ProtocolSOCKS5, ListenPort: 10813,
			Server: "h", Port: 1, Front: "nope",
		},
	}, global)

	// 直连
	fc, err := a.resolveFront(config.Node{ID: "a", Front: config.FrontDirect})
	if err != nil {
		t.Fatal(err)
	}
	if fc.Type != config.FrontTypeDirect {
		t.Errorf("直连应当解析成 direct，实际 %q", fc.Type)
	}

	// 全局前置
	fc, err = a.resolveFront(config.Node{ID: "b", Front: config.FrontGlobal})
	if err != nil {
		t.Fatal(err)
	}
	if fc.Host != "127.0.0.1" || fc.Port != 10808 || fc.Username != "gu" {
		t.Errorf("全局前置解析错误：%+v", fc)
	}

	// 以另一个节点为前置：应当变成一个指向那个节点本地端口的 socks5
	fc, err = a.resolveFront(config.Node{ID: "c", Front: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if fc.Type != config.FrontTypeSocks5 || fc.Host != "127.0.0.1" || fc.Port != 10810 {
		t.Errorf("节点前置应解析成 socks5 127.0.0.1:10810，实际 %+v", fc)
	}

	// 前置节点自带本地鉴权时，要把凭据一起带上
	fc, err = a.resolveFront(config.Node{ID: "d", Front: "c"})
	if err != nil {
		t.Fatal(err)
	}
	if fc.Username != "lu" || fc.Password != "lp" {
		t.Errorf("应当带上前置节点的本地鉴权：%+v", fc)
	}

	// 引用了不存在的节点要报错
	if _, err = a.resolveFront(config.Node{ID: "d", Front: "nope"}); err == nil {
		t.Error("前置节点不存在时应当报错")
	}
}

// freeBase 找一个"从它开始连续 n 个端口都真正空闲"的起始端口。
//
// 【为什么必须动态找，不能写死 10810/10815】
// 这几个测试依赖真实的端口占用情况。早先写死了端口，结果开发机上正跑着
// ChainProxy（占着 10810/10811）时测试就红了 —— 与代码无关，却极易被
// 误判成回归。真踩过一次。
func freeBase(t *testing.T, from, n int) int {
	t.Helper()
	for p := from; p < from+3000; p++ {
		ok := true
		for i := 0; i < n; i++ {
			ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p+i)))
			if err != nil {
				ok = false
				break
			}
			_ = ln.Close()
		}
		if ok {
			return p
		}
	}
	t.Fatalf("从 %d 起找不到 %d 个连续空闲端口", from, n)
	return 0
}

// TestPortAllocationFollowsRules 锁死端口分配的三条界面规则。
func TestPortAllocationFollowsRules(t *testing.T) {
	// 找一段空闲端口，避免受"本机正在跑 ChainProxy"之类的干扰
	base := freeBase(t, 12000, 8)

	cfgNodes := []config.Node{
		{ID: "a", Name: "A", Protocol: config.ProtocolSOCKS5, ListenPort: base, Server: "h", Port: 1},
		{ID: "b", Name: "B", Protocol: config.ProtocolSOCKS5, ListenPort: base + 5, Server: "h", Port: 1},
	}
	a := newTestApp(t, cfgNodes, config.Front{Type: config.FrontTypeSocks5, Host: "127.0.0.1", Port: 10808})
	// 起始端口也要跟着挪，否则 SuggestPort 会从一个很远的地方开始扫
	s := a.Settings()
	s.PortStart = base
	if err := a.SetSettings(s); err != nil {
		t.Fatal(err)
	}

	// 取"所有 10800+ 端口最大值 + 1"
	if got := a.SuggestPort(""); got != base+6 {
		t.Errorf("建议端口应为 %d，实际 %d", base+6, got)
	}

	// 排除 B（它占着 base+5）之后，最大值回落到 A 的 base，建议值变成 base+1。
	//
	// 这个语义是对的：SuggestPort 的入参含义是"哪个节点的端口不算冲突"，
	// 而界面只在**新增**节点（excludeID 为空）时才拿它做默认值，
	// 编辑场景用的是节点原本的端口，不会走这里。
	if got := a.SuggestPort("b"); got != base+1 {
		t.Errorf("排除 B 后应为 %d（最大值回落到 A 的 %d），实际 %d", base+1, base, got)
	}

	// 随机端口必须落在合法区间且空闲
	p, err := a.RandomPort("")
	if err != nil {
		t.Fatalf("随机端口失败：%v", err)
	}
	if p <= 10000 || !a.PortStatus(p, "").Free {
		t.Errorf("随机端口不合法：%d", p)
	}

	// 检测：已被本项目占用的端口
	st := a.PortStatus(base, "")
	if st.Free || !st.InProject {
		t.Errorf("%d 应被识别为本项目占用：%+v", base, st)
	}
	// 检测：排除自己之后就变成可用的
	if st := a.PortStatus(base, "a"); !st.Free {
		t.Errorf("排除 A 之后 %d 应当可用：%+v", base, st)
	}
	// 检测：非法端口
	if st := a.PortStatus(80, ""); st.Free {
		t.Errorf("80 端口不应当被认作可用：%+v", st)
	}
}

// TestNodeCRUD 覆盖增删改克隆的基本行为与"保留运行时字段"这条约定。
func TestNodeCRUD(t *testing.T) {
	a := newTestApp(t, nil, config.Front{})

	n, err := a.AddNode(config.Node{
		Name: "第一个", Protocol: config.ProtocolShadowsocks,
		Server: "sg.example.com", Port: 8388, Method: "aes-128-gcm", Password: "pw",
	})
	if err != nil {
		t.Fatal(err)
	}
	if n.ListenPort == 0 || n.ID == "" || n.CreatedAt.IsZero() {
		t.Fatalf("新增节点应当自动补全端口/ID/创建时间：%+v", n)
	}

	// 更新时应当保留统计与创建时间（否则编辑一次就把数据清了）
	a.mu.Lock()
	idx := a.indexOfLocked(n.ID)
	a.cfg.Nodes[idx].UpBytes = 12345
	a.cfg.Nodes[idx].DownBytes = 67890
	a.cfg.Nodes[idx].LastUsed = a.cfg.Nodes[idx].CreatedAt
	created := a.cfg.Nodes[idx].CreatedAt
	a.mu.Unlock()

	upd := n
	upd.Name = "改过名"
	upd.UpBytes = 0
	upd.DownBytes = 0
	out, err := a.UpdateNode(upd)
	if err != nil {
		t.Fatal(err)
	}
	if out.Name != "改过名" {
		t.Errorf("改名没生效：%+v", out)
	}
	if out.UpBytes != 12345 || out.DownBytes != 67890 {
		t.Errorf("更新时应当保留今日统计，实际 up=%d down=%d", out.UpBytes, out.DownBytes)
	}
	if !out.CreatedAt.Equal(created) {
		t.Errorf("更新时应当保留创建时间")
	}

	// 克隆：新 ID、新端口、不激活
	dup, err := a.CloneNode(n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if dup.ID == n.ID {
		t.Error("克隆应当分配新的 ID")
	}
	if dup.ListenPort == n.ListenPort {
		t.Error("克隆应当分配新的端口")
	}
	if dup.Active {
		t.Error("克隆出来的节点不应处于激活状态")
	}
	if dup.UpBytes != 0 || dup.DownBytes != 0 {
		t.Error("克隆出来的节点不应带着原节点的统计")
	}

	// 删除
	if err := a.DeleteNodes([]string{dup.ID}); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.GetNode(dup.ID); ok {
		t.Error("删除后不应该还能查到")
	}
	if a.CountNodes() != 1 {
		t.Errorf("删除后应剩 1 个，实际 %d", a.CountNodes())
	}
}

// TestDeleteNodesFixesDanglingFront 锁死"删掉被依赖的节点后前置回退为直连"。
func TestDeleteNodesFixesDanglingFront(t *testing.T) {
	a := newTestApp(t, []config.Node{
		{ID: "a", Name: "A", Protocol: config.ProtocolSOCKS5, ListenPort: 10810, Server: "h", Port: 1},
		{ID: "b", Name: "B", Protocol: config.ProtocolSOCKS5, ListenPort: 10811, Server: "h", Port: 1, Front: "a"},
	}, config.Front{})

	if err := a.DeleteNodes([]string{"a"}); err != nil {
		t.Fatal(err)
	}
	b, ok := a.GetNode("b")
	if !ok {
		t.Fatal("B 不应该被删掉")
	}
	if b.Front != config.FrontDirect {
		t.Errorf("A 被删后 B 的前置应回退为直连，实际 %q", b.Front)
	}
}

// TestAutoActivateDisabled 确认"启动时自动激活"可以被关掉。
func TestAutoActivateDisabled(t *testing.T) {
	a := newTestApp(t, []config.Node{
		{ID: "a", Name: "A", Protocol: config.ProtocolSOCKS5, ListenPort: 10810, Server: "h", Port: 1, Active: true},
	}, config.Front{})

	s := a.Settings()
	s.AutoActivateOnLaunch = false
	if err := a.SetSettings(s); err != nil {
		t.Fatal(err)
	}
	a.AutoActivate()
	// AutoActivate 内部有 300ms 的延迟，这里只断言"没有立刻启动"
	if a.serverOf("a") != nil {
		t.Error("关掉自动激活后不应启动任何节点")
	}
}

// TestSaveAndReloadConfig 确认配置能落盘并原样读回。
func TestSaveAndReloadConfig(t *testing.T) {
	a := newTestApp(t, nil, config.Front{})
	if _, err := a.AddNode(config.Node{
		Name: "持久化", Protocol: config.ProtocolVLESS, Server: "jp.example.com", Port: 443,
		UUID: "11111111-2222-3333-4444-555555555555",
		TLS:  config.TLSOptions{Enabled: true, ServerName: "jp.example.com", RealityPublicKey: "pbk"},
	}); err != nil {
		t.Fatal(err)
	}
	path := a.ConfigPath()

	again, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()

	nodes := again.NodeConfigs()
	if len(nodes) != 1 {
		t.Fatalf("读回来应当有 1 个节点，实际 %d", len(nodes))
	}
	if nodes[0].TLS.RealityPublicKey != "pbk" || nodes[0].UUID == "" {
		t.Errorf("读回来的字段不完整：%+v", nodes[0])
	}
	// 数据目录应当在临时目录下，不该污染项目目录
	if dir := config.DataDir(); dir != filepath.Dir(path) {
		t.Errorf("DataDir 与配置路径应当一致：%s vs %s", dir, path)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "chainproxy.log")); err != nil {
		t.Logf("日志文件未生成（可接受）：%v", err)
	}
}
