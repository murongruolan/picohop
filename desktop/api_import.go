package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/murongruolan/picohop/internal/config"
	"github.com/murongruolan/picohop/internal/ports"
	"github.com/murongruolan/picohop/internal/qrscan"
	"github.com/murongruolan/picohop/internal/share"
)

// ------------------------------------------------------------------
// 导入
// ------------------------------------------------------------------

// PendingNode 是"已识别但还没入库"的节点。
//
// 为什么要有这一步：不管是从剪贴板还是从二维码识别出来的节点，
// 都还没有本地代理端口 —— 而端口是必须由用户确认的（默认值、随机、
// 占用检测都在那一步）。所以先暂存，等用户在只有端口的小弹窗里定了再入库。
type PendingNode struct {
	Node          config.Node `json:"node"`
	Protocol      string      `json:"protocol"`
	ProtocolLabel string      `json:"protocolLabel"`
	Transport     string      `json:"transport"`
	Server        string      `json:"server"`
	Port          int         `json:"port"`
	// SuggestedPort 是后端算好的默认端口（10800+ 最大值 + 1）
	SuggestedPort int `json:"suggestedPort"`
	// ListenPortFree 说明建议端口当前是否可用
	ListenPortFree bool `json:"listenPortFree"`
}

// ImportResult 是一次导入尝试的结果。
type ImportResult struct {
	OK      bool          `json:"ok"`
	Source  string        `json:"source"` // clipboard / screen / text
	Nodes   []PendingNode `json:"nodes"`
	Message string        `json:"message"`
	Raw     string        `json:"raw"` // 识别到的原文，出错时便于用户排查
}

// ImportFromClipboard 取剪贴板最新一条内容并尝试解析成节点。
func (a *API) ImportFromClipboard() ImportResult {
	if a.app == nil {
		return ImportResult{Message: "应用尚未就绪"}
	}
	text, ok := a.clipboardText()
	if !ok {
		return ImportResult{Source: "clipboard", Message: "读取剪贴板失败：系统剪贴板暂时不可用"}
	}
	if strings.TrimSpace(text) == "" {
		return ImportResult{Source: "clipboard", Message: "剪贴板是空的"}
	}
	res := a.parseNodes(text, "clipboard")
	if !res.OK {
		// 【审查 #23】原来这里把剪贴板原文的前 80 个字节当作通知正文显示，而
		// 剪贴板里通常正是一条带密码 / UUID 的分享链接 —— 导入失败（复制被
		// 截断、前后混了别的话）时，凭据就跟着弹窗出现在屏幕上（截图、录屏
		// 一并泄露）。只报"读到多少字符"，用户仍能判断是不是没复制全。
		// 注意 res.Message 走的是 share 包的错误，那里也已经不回显原文。
		a.app.Notify("warning", "剪贴板里没有可识别的节点",
			fmt.Sprintf("已读取 %d 个字符，没有解析出分享链接", len([]rune(text))))
	}
	return res
}

// ParseShareText 解析用户手输/粘贴的分享文本。
func (a *API) ParseShareText(text string) ImportResult {
	if a.app == nil {
		return ImportResult{Message: "应用尚未就绪"}
	}
	return a.parseNodes(text, "text")
}

// parseNodes 是剪贴板导入与手输导入共用的解析逻辑。
func (a *API) parseNodes(text, source string) ImportResult {
	res := ImportResult{Source: source, Raw: text}
	nodes, err := share.DecodeAny(text)
	if err != nil || len(nodes) == 0 {
		msg := "没有识别到可用的分享链接"
		if err != nil {
			msg = err.Error()
		}
		res.Message = msg
		return res
	}

	exclude := ""
	port := a.app.SuggestPort(exclude)
	for _, n := range nodes {
		// 每个候选都给一个默认端口；多个节点时依次顺延
		//
		// 【审查 #21】SuggestPort 在耗尽时返回 ports.NoPort(0)（它不再返回
		// 没验证过的候选）。这里必须显式判 0：否则下面这个循环会从端口 0
		// 开始一个个往后试 —— 0 永远不 Free，于是白白空扫 6 万多次 bind，
		// 最后还把 65535 当成"建议端口"塞进导入结果。
		// 判 0 之后 SuggestedPort 留 0、ListenPortFree=false，
		// 由 PortOnlyDialog 退回默认起始值让用户自己确认端口。
		p := port
		st := ports.Status{Port: p}
		if p != ports.NoPort {
			st = a.app.PortStatus(p, exclude)
			for !st.Free && p < 65535 {
				p++
				st = a.app.PortStatus(p, exclude)
			}
			port = p + 1
		}

		res.Nodes = append(res.Nodes, PendingNode{
			Node:           n,
			Protocol:       n.Protocol,
			ProtocolLabel:  config.ProtocolLabel(n.Protocol),
			Transport:      n.TransportLabel(),
			Server:         n.Server,
			Port:           n.Port,
			SuggestedPort:  p,
			ListenPortFree: st.Free,
		})
	}
	res.OK = true
	return res
}

// BeginScreenScan 走完整个"屏幕圈选 → 识别二维码 → 解析节点"的流程。
//
// 流程与产品要求一致：
//  1. 暂时隐藏主界面（否则会把自己也拍进去，出现无限套娃）
//  2. 屏幕上出现遮罩，用户按住左键拖拽圈选
//  3. 松开后截取该区域并识别二维码
//  4. 解析成节点并暂存（等用户在小弹窗里确认本地端口）
//
// 这个方法会阻塞到用户完成圈选为止，所以 Wails 会把它放到 goroutine 里跑，
// 前端拿到的是一个 Promise。
func (a *API) BeginScreenScan() ImportResult {
	if a.app == nil {
		return ImportResult{Source: "screen", Message: "应用尚未就绪"}
	}

	// tStart 是整条链路的起点，用来写计时日志。
	// 用户感知的"点了圈选没反应"就是"主窗口消失"到"遮罩出现"之间的空档，
	// 所以每一步都记毫秒数，而不是只说"做完了"。
	// 把 qrscan 的时间线接到应用日志（哪一步慢只能靠测量）
	qrscan.Tracef = func(format string, args ...any) {
		if a.app != nil {
			a.app.Logf(format, args...)
		}
	}

	tStart := time.Now()

	a.mu.Lock()
	if a.scanning {
		a.mu.Unlock()
		return ImportResult{Source: "screen", Message: "已经在圈选中"}
	}
	a.scanning = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.scanning = false
		a.mu.Unlock()
	}()

	// 【让接下来的整屏截图看不见主窗口】
	//
	// 优先用 WDA_EXCLUDEFROMCAPTURE 把主窗口从屏幕捕获里排除：窗口原地不动、
	// 也不用等，用户感觉就是"点了就有反应"。
	// 系统不支持时才退回老办法（隐藏窗口 + 等 280ms 让它真的消失）。
	//
	// 为什么值得做：实测那 280ms 是整条链路里最大的一块（其余合计约 55ms），
	// 而用户原话是"点击扫码的时候得卡一会儿才会弄出来十字准星"。
	// 隐藏主窗口（隐藏而不是"从捕获里排除"：WDA_EXCLUDEFROMCAPTURE 会让被排除的
	// 窗口在 BitBlt 捕获里变成**一块纯黑**，实测遮罩背景上会出现一个黑色方洞 ——
	// 那不是用户想看到的）。无论中途出什么问题都必须恢复出来，
	// 否则用户会面对一个"程序还在跑但窗口没了"的状态。
	// 【把主窗口挪到屏幕外，而不是 Hide()】
	//
	// 试过两种更"正统"的做法，都有坑：
	//   - Hide()：DWM 会做约 200ms 的**淡出动画**，截屏拍到的是动画中间 ——
	//     实测就是一块半透明黑板（窗口标题栏还看得见）。早先那句
	//     `time.Sleep(280ms)` 实际上就是在等这个动画演完。
	//   - WDA_EXCLUDEFROMCAPTURE：0ms，但被排除的窗口在 BitBlt 捕获里会变成
	//     **一块纯黑**，遮罩背景上直接出现一个黑色方洞。
	// 挪窗口没有动画：位置一变、下一帧就是最终状态，既快又干净。
	prevX, prevY, parked := parkMainWindowOffscreen()
	defer func() {
		if parked {
			restoreWindowPos(prevX, prevY)
		} else {
			// 挪不动（极少数情况）才退回隐藏
			a.windowRestore()
		}
		dwmFlush()
		// 圈选结束：把主窗口**临时**提到最前（不做置顶）。
		// 用户原话："松手的时候取消全部遮罩然后将主窗口临时提到所有窗口上面，
		// 就提一下，不固定"。
		raiseMainWindowOnce()
	}()

	// 等合成器把"窗口已挪走"这一帧画完再去截图（挪窗口本身没有动画，
	// 所以这里只需等一帧，实测个位数毫秒）。
	dwmFlush()

	// 圈选这条链路很长（排除/隐藏窗口 → 调 Win32 建全屏遮罩 → 等用户拖 → 截图 → 识别），
	// 而且**用户报过"点完就什么都没了"**。所以每一步都写日志 + 计时：
	// 出问题时能从 data\picohop.log 看出卡在哪一步、慢在哪一步，而不是只能猜。
	if a.app != nil {
		a.app.Logf("屏幕圈选：主窗口已挪出屏幕，准备创建遮罩（%dms）", sinceMs(tStart))
	}

	// 用 Hooked 版本是为了拿到两个事实：遮罩窗口真的建出来了、以及它花了多久。
	// 用户感知的"点了没反应"就是"主窗口没了"到"遮罩出现"之间的这段空档，
	// 所以这里必须把毫秒数记下来。
	readyAt := time.Time{}
	rect, err := qrscan.SelectRegionHooked(func(hwnd uintptr) {
		readyAt = time.Now()
		if a.app != nil {
			a.app.Logf("屏幕圈选：遮罩已显示 hwnd=0x%x，等待选择（建窗耗时 %dms）",
				hwnd, sinceMs(tStart))
		}
	})
	if err != nil {
		if a.app != nil {
			a.app.Logf("屏幕圈选：结束（%v，总耗时 %dms）", err, sinceMs(tStart))
		}
		if err == qrscan.ErrCancelled {
			return ImportResult{Source: "screen", Message: "已取消圈选"}
		}
		a.app.Notify("error", "屏幕圈选失败", err.Error())
		return ImportResult{Source: "screen", Message: err.Error()}
	}
	_ = readyAt
	if a.app != nil {
		a.app.Logf("屏幕圈选：选定区域 %v（总耗时 %dms）", rect, sinceMs(tStart))
	}

	img, err := qrscan.CaptureRect(rect)
	if err != nil {
		a.app.Notify("error", "截取所选区域失败", err.Error())
		return ImportResult{Source: "screen", Message: err.Error()}
	}

	text, err := qrscan.Decode(img)
	if err != nil {
		a.app.Notify("warning", "没有识别到二维码", "请圈选得更贴近二维码一些，或换一张更清晰的截图")
		return ImportResult{Source: "screen", Message: "没有识别到二维码：" + err.Error()}
	}

	// 【凭据不入日志】识别结果就是分享链接（含 uuid/密码），打印它等于把凭据抄进日志。
	// 只记长度用于排查"识别到没识别到/内容多长"，内容一律不落盘。
	a.app.Logf("屏幕圈选识别到二维码内容：%d 字符（内容不入日志）", len(text))
	res := a.parseNodes(text, "screen")
	if !res.OK {
		a.app.Notify("warning", "二维码内容不是可用的节点链接", res.Message)
	}
	return res
}

// Scanning 返回是否正在圈选。
func (a *API) Scanning() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.scanning
}

// ------------------------------------------------------------------
// 分享
// ------------------------------------------------------------------

// ShareResult 是一次分享操作的结果。
type ShareResult struct {
	OK            bool   `json:"ok"`
	Name          string `json:"name"`
	Link          string `json:"link"`
	QRDataURL     string `json:"qrDataUrl"`
	ProtocolLabel string `json:"protocolLabel"`
	Transport     string `json:"transport"`
	Error         string `json:"error"`
}

// ShareNode 生成节点的分享链接与二维码。
func (a *API) ShareNode(id string) ShareResult {
	if a.app == nil {
		return ShareResult{Error: "应用尚未就绪"}
	}
	n, ok := a.app.GetNode(id)
	if !ok {
		return ShareResult{Error: "未找到该节点"}
	}
	link, err := share.Encode(n)
	if err != nil {
		return ShareResult{Name: n.Name, ProtocolLabel: config.ProtocolLabel(n.Protocol), Error: err.Error()}
	}
	dataURL, err := share.QRDataURL(link, 320)
	if err != nil {
		// 链接生成成功但二维码失败时，仍然把链接给用户
		return ShareResult{OK: true, Name: n.Name, Link: link,
			ProtocolLabel: config.ProtocolLabel(n.Protocol), Transport: n.TransportLabel(),
			Error: "二维码生成失败：" + err.Error()}
	}
	return ShareResult{
		OK: true, Name: n.Name, Link: link, QRDataURL: dataURL,
		ProtocolLabel: config.ProtocolLabel(n.Protocol), Transport: n.TransportLabel(),
	}
}

// SaveShareImage 生成二维码 PNG 并弹出"另存为"对话框。
//
// 返回保存到的路径（用户取消时返回空串与 nil）。
func (a *API) SaveShareImage(id string) (string, error) {
	if a.app == nil {
		return "", fmt.Errorf("应用尚未就绪")
	}
	n, ok := a.app.GetNode(id)
	if !ok {
		return "", fmt.Errorf("未找到该节点")
	}
	link, err := share.Encode(n)
	if err != nil {
		return "", err
	}
	png, err := share.QRPNG(link, 512)
	if err != nil {
		return "", fmt.Errorf("生成二维码失败：%w", err)
	}

	path, err := a.wapp.Dialog.SaveFileWithOptions(&application.SaveFileDialogOptions{
		Title:    "保存二维码图片",
		Filename: safeFileName(n.Name) + ".png",
		Filters: []application.FileFilter{
			{DisplayName: "PNG 图片 (*.png)", Pattern: "*.png"},
		},
		Window: a.mainWin,
	}).PromptForSingleSelection()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(path) == "" {
		return "", nil // 用户取消
	}
	if err := os.WriteFile(path, png, 0o644); err != nil {
		return "", fmt.Errorf("写入文件失败：%w", err)
	}
	a.app.Notify("success", "二维码已保存", path)
	return path, nil
}

// CopyText 把文本写进剪贴板。
//
// 【只用于非敏感内容】日志、本地代理地址、端口这类东西用户往往要反复粘贴，
// 所以不安排清理。含凭据的内容（分享链接、UUID、私钥）必须走 CopySensitiveText。
func (a *API) CopyText(text string) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("没有可复制的内容")
	}
	if !a.setClipboard(text) {
		return fmt.Errorf("写入剪贴板失败：系统剪贴板暂时不可用")
	}
	return nil
}

// CopySensitiveText 把含凭据的文本写进剪贴板，并在 sensitiveClipboardTTL 之后自动清理（审查 #25）。
//
// 返回清理前的秒数，供界面如实告诉用户"这条链接会在多少秒后消失"——
// 这个数字只有后端知道，写死在前端迟早会和常量对不上。
//
// 【为什么这类复制要清而其它复制不清】见 clipboard_guard.go 顶部。
func (a *API) CopySensitiveText(text string) (int, error) {
	if strings.TrimSpace(text) == "" {
		return 0, fmt.Errorf("没有可复制的内容")
	}
	g := a.clipGuard
	if g == nil {
		// 兜底：直接 &API{} 构造的测试/异常路径没有 clipGuard。
		// 注意不要写回 a.clipGuard —— 那会变成并发写字段。
		g = newClipboardGuard(a.clipboardText, a.setClipboard, sensitiveClipboardTTL)
	}
	if !g.copySensitive(text) {
		return 0, fmt.Errorf("写入剪贴板失败：系统剪贴板暂时不可用")
	}
	return int(sensitiveClipboardTTL / time.Second), nil
}

// ------------------------------------------------------------------
// 测试
// ------------------------------------------------------------------

// RunTest 对若干节点执行测试。
//
// kind: tcp / real / speed / exit
func (a *API) RunTest(ids []string, kind string) error {
	if a.app == nil {
		return fmt.Errorf("应用尚未就绪")
	}
	if err := a.app.TestNodes(ids, kind); err != nil {
		return err
	}
	labels := map[string]string{"tcp": "TCP 延迟", "real": "真连接延迟", "speed": "速度", "exit": "出口 IP"}
	a.app.Notify("info", "开始测试"+labels[kind], fmt.Sprintf("共 %d 个节点", len(ids)))
	return nil
}

// CancelTest 停止当前测试。
func (a *API) CancelTest() {
	if a.app != nil {
		a.app.CancelTests()
	}
}

// IsTesting 返回是否正在测试。
func (a *API) IsTesting() bool {
	if a.app == nil {
		return false
	}
	return a.app.Testing()
}

// ------------------------------------------------------------------
// 小工具
// ------------------------------------------------------------------

// safeFileName 把节点别名变成一个安全的文件名。
func safeFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "picohop-node"
	}
	repl := func(r rune) rune {
		switch r {
		case '\\', '/', ':', '*', '?', '"', '<', '>', '|':
			return '_'
		}
		return r
	}
	out := strings.Map(repl, name)
	if len(out) > 60 {
		out = out[:60]
	}
	return out
}

func truncateForLog(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// sinceMs 返回从 t 到现在经过的毫秒数。圈选链路用它写计时日志。
func sinceMs(t time.Time) int64 { return time.Since(t).Milliseconds() }
