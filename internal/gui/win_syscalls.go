//go:build windows

package gui

import (
	"syscall"
	"unsafe"
)

// ---------------------------------------------------------------------------
// Win32 API 绑定
//
// 全部走 syscall + LazyDLL，避免引入 cgo / 第三方 GUI 依赖，
// 这样 ChainProxy 始终是"一个 exe、零外部依赖"。
// ---------------------------------------------------------------------------

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	comctl32 = syscall.NewLazyDLL("comctl32.dll")
	shcore   = syscall.NewLazyDLL("shcore.dll")
	dwmapi   = syscall.NewLazyDLL("dwmapi.dll")
)

var (
	pRegisterClassExW              = user32.NewProc("RegisterClassExW")
	pUnregisterClassW              = user32.NewProc("UnregisterClassW")
	pCreateWindowExW               = user32.NewProc("CreateWindowExW")
	pDefWindowProcW                = user32.NewProc("DefWindowProcW")
	pDestroyWindow                 = user32.NewProc("DestroyWindow")
	pShowWindow                    = user32.NewProc("ShowWindow")
	pUpdateWindow                  = user32.NewProc("UpdateWindow")
	pGetMessageW                   = user32.NewProc("GetMessageW")
	pTranslateMessage              = user32.NewProc("TranslateMessage")
	pDispatchMessageW              = user32.NewProc("DispatchMessageW")
	pPostQuitMessage               = user32.NewProc("PostQuitMessage")
	pPostMessageW                  = user32.NewProc("PostMessageW")
	pSendMessageW                  = user32.NewProc("SendMessageW")
	pGetDlgItem                    = user32.NewProc("GetDlgItem")
	pEnableWindow                  = user32.NewProc("EnableWindow")
	pSetFocus                      = user32.NewProc("SetFocus")
	pSetForegroundWindow           = user32.NewProc("SetForegroundWindow")
	pMessageBoxW                   = user32.NewProc("MessageBoxW")
	pLoadCursorW                   = user32.NewProc("LoadCursorW")
	pSetTimer                      = user32.NewProc("SetTimer")
	pKillTimer                     = user32.NewProc("KillTimer")
	pInvalidateRect                = user32.NewProc("InvalidateRect")
	pGetClientRect                 = user32.NewProc("GetClientRect")
	pGetDC                         = user32.NewProc("GetDC")
	pReleaseDC                     = user32.NewProc("ReleaseDC")
	pBeginPaint                    = user32.NewProc("BeginPaint")
	pEndPaint                      = user32.NewProc("EndPaint")
	pFillRect                      = user32.NewProc("FillRect")
	pMoveWindow                    = user32.NewProc("MoveWindow")
	pSetWindowPos                  = user32.NewProc("SetWindowPos")
	pGetWindowRect                 = user32.NewProc("GetWindowRect")
	pAdjustWindowRectEx            = user32.NewProc("AdjustWindowRectEx")
	pGetSysColorBrush              = user32.NewProc("GetSysColorBrush")
	pSetWindowTextW                = user32.NewProc("SetWindowTextW")
	pGetWindowTextLengthW          = user32.NewProc("GetWindowTextLengthW")
	pLoadIconW                     = user32.NewProc("LoadIconW")
	pGetWindowLongPtrW             = user32.NewProc("GetWindowLongPtrW")
	pSetWindowLongPtrW             = user32.NewProc("SetWindowLongPtrW")
	pSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	pGetDpiForWindow               = user32.NewProc("GetDpiForWindow")
	pIsDialogMessageW              = user32.NewProc("IsDialogMessageW")
	pGetWindow                     = user32.NewProc("GetWindow")
	pMapVirtualKeyW                = user32.NewProc("MapVirtualKeyW")
	pGetKeyState                   = user32.NewProc("GetKeyState")
	pSetCapture                    = user32.NewProc("SetCapture")
	pReleaseCapture                = user32.NewProc("ReleaseCapture")
	pGetMessagePos                 = user32.NewProc("GetMessagePos")
	pClientToScreen                = user32.NewProc("ClientToScreen")
	pSendNotifyMessageW            = user32.NewProc("SendNotifyMessageW")
	pCharUpperBuffW                = user32.NewProc("CharUpperBuffW")
)

var (
	pCreateFontW    = gdi32.NewProc("CreateFontW")
	pDeleteObject   = gdi32.NewProc("DeleteObject")
	pGetStockObject = gdi32.NewProc("GetStockObject")
	pSetBkColor     = gdi32.NewProc("SetBkColor")
	// 注意：SetTextColor / SetBkMode 在 **gdi32.dll**，不是 user32.dll。
	// 放错 DLL 会在运行时 panic（LazyProc.Call 找不到符号）。
	pSetBkMode             = gdi32.NewProc("SetBkMode")
	pSetTextColor          = gdi32.NewProc("SetTextColor")
	pSelectObject          = gdi32.NewProc("SelectObject")
	pCreateSolidBrush      = gdi32.NewProc("CreateSolidBrush")
	pCreatePatternBrush    = gdi32.NewProc("CreatePatternBrush")
	pGetTextExtentPoint32W = gdi32.NewProc("GetTextExtentPoint32W")
)

var (
	pInitCommonControlsEx = comctl32.NewProc("InitCommonControlsEx")
	pImageList_Create     = comctl32.NewProc("ImageList_Create")
)

// DWM：仅用于标题栏跟随系统深色模式，失败也不影响功能。
var pDwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")

// ---------------------------------------------------------------------------
// 常量
// ---------------------------------------------------------------------------

const (
	// 窗口样式
	wsOverlappedWindow = 0x00CF0000
	wsPopup            = 0x80000000
	wsChild            = 0x40000000
	wsVisible          = 0x10000000
	wsTabStop          = 0x00010000
	wsDisabled         = 0x08000000
	wsBorder           = 0x00800000
	wsVScroll          = 0x00200000
	wsGroup            = 0x00020000
	wsCaption          = 0x00C00000
	wsSysMenu          = 0x00080000
	wsClipChildren     = 0x02000000
	wsThickFrame       = 0x00040000
	wsMaximizeBox      = 0x00010000
	wsMinimizeBox      = 0x00020000

	// 扩展样式
	wsExTopMost       = 0x00000008
	wsExDlgModalFrame = 0x00000001
	wsExClientEdge    = 0x00000200
	wsExToolWindow    = 0x00000080

	// 控件样式
	bsPushButton    = 0x0000
	bsDefPushButton = 0x0001
	bsAutoCheckBox  = 0x0003
	bsGroupBox      = 0x0007
	bsAutoRadioBox  = 0x0009

	esLeft        = 0x0000
	esMultiline   = 0x0004
	esAutoVScroll = 0x0040
	esAutoHScroll = 0x0080
	esPassword    = 0x0020
	esNumber      = 0x2000
	esReadOnly    = 0x0800
	esCenter      = 0x0003

	ssLeft        = 0x0000
	ssCenter      = 0x0001
	ssRight       = 0x0002
	ssEndEllipsis = 0x4000
	ssSunken      = 0x0000
	ssSimple      = 0x0000
	ssIcon        = 0x0003

	cbsDropdownList   = 0x0003
	cbsSimpleDropdown = 0x0001

	lbsDropdownList     = 0x0003
	lbsNoIntegralHeight = 0x0100

	// ListView
	lvsReport           = 0x0001
	lvsSingleSel        = 0x0004
	lvsShowSelAlways    = 0x0008
	lvsExFullRowSelect  = 0x0020
	lvsExGridLines      = 0x0001
	lvsExDoubleClick    = 0x0002
	lvsExHeaderDragDrop = 0x1000

	lvmFirst            = 0x1000
	lvmGetItemCount     = lvmFirst + 4
	lvmDeleteItem       = lvmFirst + 5
	lvmDeleteAllItems   = lvmFirst + 9
	lvmEnsureVisible    = lvmFirst + 19
	lvmSetColumnWidth   = lvmFirst + 30
	lvmSetExtendedStyle = lvmFirst + 54
	// 注意：LVM_INSERTITEM = LVM_FIRST+77，LVM_SETITEMTEXT = LVM_FIRST+109。
	// 两者写反会让"设置文本"变成"插入行"，列表会莫名其妙地疯长。
	lvmInsertItem  = lvmFirst + 77
	lvmSetItemText = lvmFirst + 109
	lvmGetHeader   = lvmFirst + 31
	lvmGetItemRect = lvmFirst + 14
	lvmGetNextItem = lvmFirst + 12

	lvifText     = 0x0001
	lvcFmtLeft   = 0x0000
	lvcFmtCenter = 0x2000

	// 通用控件初始化
	iccListViewClasses = 0x00000001
	iccProgressClass   = 0x00000020

	// 消息
	wmDestroy        = 0x0002
	wmSize           = 0x0005
	wmPaint          = 0x000F
	wmClose          = 0x0010
	wmSetFocus       = 0x0007
	wmCommand        = 0x0111
	wmNotify         = 0x004E
	wmCtlColorStatic = 0x0138
	wmCtlColorEdit   = 0x0133
	wmTimer          = 0x0113
	wmKeyDown        = 0x0100
	wmGetMinMaxInfo  = 0x0024
	wmSetCursor      = 0x0020
	wmMouseMove      = 0x0200

	wmApp = 0x8000
	// wmAppRefresh 由 app 的事件回调投递，用于跨线程刷新界面。
	wmAppRefresh = wmApp + 1
	// wmAppLog 追加一行日志文本。
	wmAppLog = wmApp + 2

	// ListView / NM 通知码（NMHDR.Code 是 uint32，这里用补码表示负值）
	nmCustomDraw   uint32 = 0xFFFFFFF4 // NM_CUSTOMDRAW = -12
	lvnDoubleClick uint32 = 0xFFFFFF9B // LVN_ITEMDOUBLECLICK = -101

	// NM_CUSTOMDRAW 返回值
	cdrfSkipDraw       = 0x00000020
	cdrfNotifyItemDraw = 0x00000020
	cddsPrePaint       = 0x00000000
	cddsItemPrePaint   = 0x00000001
	cddsPostPaint      = 0x00000002

	// 鼠标
	wmRButtonUp    = 0x0205
	wmLButtonDown  = 0x0201
	mfPopup        = 0x0010
	tpmLeftButton  = 0x0001
	tpmRightButton = 0x0002
	tpmReturnCmd   = 0x0100

	// SetWindowPos
	swpNoZOrder   = 0x0004
	swpNoActivate = 0x0010
	swpNoMove     = 0x0002
	swpNoSize     = 0x0001

	// ShowWindow
	swShow     = 5
	swHide     = 0
	swMaximize = 3

	// 光标
	idcArrow       = 32512
	idcHand        = 32649
	idcAppStarting = 32650

	// GetStockObject
	defaultGUIFont        = 17
	defaultGuiFont        = 17
	whiteBrush            = 0
	blackBrush            = 4
	systemColorWindow     = 5
	systemColorBtnFace    = 15
	systemColorWindowText = 8
	systemColorHighlight  = 4

	transparentBrushMode = 1

	// WM_SETCURSOR / IDC
	idcSizeAll = 32646

	dpiUnaware = -1

	dwmwaUseImmersiveDarkMode = 20
)

var (
	idcArrowW = mustUTF16("IDC_ARROW")
	idcAppW   = mustUTF16("IDC_APPSTARTING")
)

// ---------------------------------------------------------------------------
// 结构体
// ---------------------------------------------------------------------------

type POINT struct{ X, Y int32 }

type RECT struct{ Left, Top, Right, Bottom int32 }

type MSG struct {
	Hwnd    uintptr
	Message uint32
	_       uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	_       uint32
	Pt      POINT
}

type PAINTSTRUCT struct {
	HDC         uintptr
	FErase      int32
	_           uint32
	RcPaint     RECT
	FRestore    int32
	FIncUpdate  int32
	RgbReserved [32]byte
}

type WNDCLASSEX struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type NMHDR struct {
	HwndFrom uintptr
	IDFrom   uint32
	Code     uint32
}

type NMCUSTOMDRAW struct {
	Hdr         NMHDR
	DwDrawStage uint32
	Hdc         uintptr
	Rc          RECT
	DwData      uintptr
}

// LVCOLUMNW
type LVCOLUMN struct {
	Mask        uint32
	Fmt         int32
	Cx          int32
	PszText     *uint16
	CCchTextMax int32
	ISubItem    int32
	IImage      int32
	IOrder      int32
}

// LVITEMW
type LVITEM struct {
	Mask        uint32
	IItem       int32
	ISubItem    int32
	State       uint32
	StateMask   uint32
	PszText     *uint16
	CCchTextMax int32
	IImage      int32
	LParam      uintptr
	IIndent     int32
	IGroupId    int32
	CColumns    uint32
}

type MINMAXINFO struct {
	Reserved     POINT
	MaxSize      POINT
	MaxPosition  POINT
	MinTrackSize POINT
	MaxTrackSize POINT
}

// ---------------------------------------------------------------------------
// 工具
// ---------------------------------------------------------------------------

// mustUTF16 把 Go 字符串转成 UTF-16 指针（只用于常量类名/游标名）。
func mustUTF16(s string) *uint16 {
	buf, err := syscall.UTF16FromString(s)
	if err != nil {
		panic(err)
	}
	return &buf[0]
}

// utf16Ptr 把 s 转成 UTF-16（返回指针和长度），用于 SetWindowText 等。
func utf16Ptr(s string) (*uint16, int) {
	u := utf16Slice(s)
	return &u[0], len(u)
}

func utf16Slice(s string) []uint16 {
	r := make([]uint16, 0, len(s)+1)
	for _, c := range utf16Encode(s) {
		r = append(r, uint16(c))
	}
	return append(r, 0)
}

// utf16Encode 返回 UTF-16 码元（不追加终止符）。
func utf16Encode(s string) []uint16 {
	var out []uint16
	for _, r := range s {
		if r <= 0xFFFF {
			out = append(out, uint16(r))
			continue
		}
		r -= 0x10000
		out = append(out, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
	}
	return out
}

// utf16String 把 UTF-16 码元序列转回 Go 字符串（遇 NUL 结束）。
func utf16String(units []uint16) string {
	n := 0
	for n < len(units) && units[n] != 0 {
		n++
	}
	return string(utf16Decode(units[:n]))
}

func utf16Decode(u []uint16) []rune {
	runes := make([]rune, 0, len(u))
	for i := 0; i < len(u); i++ {
		c := uint32(u[i])
		switch {
		case c >= 0xD800 && c < 0xDC00 && i+1 < len(u):
			c2 := uint32(u[i+1])
			if c2 >= 0xDC00 && c2 < 0xE000 {
				runes = append(runes, rune(0x10000+(c-0xD800)<<10+(c2-0xDC00)))
				i++
				continue
			}
			runes = append(runes, utf8RuneError)
		case c >= 0xD800 && c < 0xE000:
			runes = append(runes, utf8RuneError)
		default:
			runes = append(runes, rune(c))
		}
	}
	return runes
}

const utf8RuneError = '�'

func unsafePtr[T any](v *T) unsafe.Pointer { return unsafe.Pointer(v) }

// unsafePtrAt 返回 base+offset 处的裸指针。
func unsafePtrAt(base uintptr, offset uintptr) unsafe.Pointer {
	return unsafe.Pointer(base + offset)
}
