package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"chainproxy/internal/config"
	"chainproxy/internal/qrscan"
	"chainproxy/internal/share"
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
	text, err := wruntime.ClipboardGetText(a.ctx)
	if err != nil {
		return ImportResult{Source: "clipboard", Message: "读取剪贴板失败：" + err.Error()}
	}
	if strings.TrimSpace(text) == "" {
		return ImportResult{Source: "clipboard", Message: "剪贴板是空的"}
	}
	res := a.parseNodes(text, "clipboard")
	if !res.OK {
		a.app.Notify("warning", "剪贴板里没有可识别的节点", strings.TrimSpace(text[:min(len(text), 80)]))
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
		p := port
		st := a.app.PortStatus(p, exclude)
		for !st.Free && p < 65535 {
			p++
			st = a.app.PortStatus(p, exclude)
		}
		port = p + 1

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

	// 无论中途出什么问题，都必须把主界面恢复出来，
	// 否则用户会面对一个"程序还在跑但窗口没了"的状态。
	wruntime.WindowHide(a.ctx)
	defer func() {
		wruntime.WindowShow(a.ctx)
		wruntime.WindowUnminimise(a.ctx)
	}()

	// 等窗口真的从屏幕上消失再去截图。
	// WindowHide 只是投递消息，立刻截图有可能把还没隐掉的窗口拍进去。
	time.Sleep(280 * time.Millisecond)

	rect, err := qrscan.SelectRegion()
	if err != nil {
		if err == qrscan.ErrCancelled {
			return ImportResult{Source: "screen", Message: "已取消圈选"}
		}
		a.app.Notify("error", "屏幕圈选失败", err.Error())
		return ImportResult{Source: "screen", Message: err.Error()}
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

	a.app.Logf("屏幕圈选识别到二维码内容：%s", truncateForLog(text, 120))
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

	path, err := wruntime.SaveFileDialog(a.ctx, wruntime.SaveDialogOptions{
		Title:           "保存二维码图片",
		DefaultFilename: safeFileName(n.Name) + ".png",
		Filters: []wruntime.FileFilter{
			{DisplayName: "PNG 图片 (*.png)", Pattern: "*.png"},
		},
	})
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
func (a *API) CopyText(text string) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("没有可复制的内容")
	}
	if err := wruntime.ClipboardSetText(a.ctx, text); err != nil {
		return fmt.Errorf("写入剪贴板失败：%w", err)
	}
	return nil
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
		return "chainproxy-node"
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
