//go:build windows

package main

import (
	"bytes"
	"errors"
	"image"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"sync"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	shcore   = syscall.NewLazyDLL("shcore.dll")

	pGetDC                         = user32.NewProc("GetDC")
	pReleaseDC                     = user32.NewProc("ReleaseDC")
	pGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
	pEnumDisplayMonitors           = user32.NewProc("EnumDisplayMonitors")
	pGetMonitorInfoW               = user32.NewProc("GetMonitorInfoW")
	pSendInput                     = user32.NewProc("SendInput")
	pGetCursorPos                  = user32.NewProc("GetCursorPos")
	pGetCursorInfo                 = user32.NewProc("GetCursorInfo")
	pGetIconInfo                   = user32.NewProc("GetIconInfo")
	pDrawIconEx                    = user32.NewProc("DrawIconEx")
	pMapVirtualKeyW                = user32.NewProc("MapVirtualKeyW")
	pVkKeyScanW                    = user32.NewProc("VkKeyScanW")
	pSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	pSetProcessDPIAware            = user32.NewProc("SetProcessDPIAware")
	pSetProcessDpiAwareness        = shcore.NewProc("SetProcessDpiAwareness")

	pCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	pCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	pSelectObject       = gdi32.NewProc("SelectObject")
	pDeleteObject       = gdi32.NewProc("DeleteObject")
	pDeleteDC           = gdi32.NewProc("DeleteDC")
	pBitBlt             = gdi32.NewProc("BitBlt")
	pStretchBlt         = gdi32.NewProc("StretchBlt")
	pSetStretchBltMode  = gdi32.NewProc("SetStretchBltMode")
	pSetBrushOrgEx      = gdi32.NewProc("SetBrushOrgEx")
	pGdiFlush           = gdi32.NewProc("GdiFlush")

	pGetConsoleMode          = kernel32.NewProc("GetConsoleMode")
	pSetConsoleMode          = kernel32.NewProc("SetConsoleMode")
	pSetConsoleTitleW        = kernel32.NewProc("SetConsoleTitleW")
	pSetThreadExecutionState = kernel32.NewProc("SetThreadExecutionState")
)

const (
	smCxScreen        = 0
	smCyScreen        = 1
	smCxCursor        = 13
	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCxVirtualScreen = 78
	smCyVirtualScreen = 79

	srccopy     = 0x00CC0020
	halftone    = 4
	diNormal    = 3
	cursorShown = 1

	inputMouse    = 0
	inputKeyboard = 1

	mouseeventfMove       = 0x0001
	mouseeventfLeftDown   = 0x0002
	mouseeventfLeftUp     = 0x0004
	mouseeventfRightDown  = 0x0008
	mouseeventfRightUp    = 0x0010
	mouseeventfMiddleDown = 0x0020
	mouseeventfMiddleUp   = 0x0040
	mouseeventfWheel      = 0x0800
	mouseeventfHWheel     = 0x1000
	mouseeventfVirtualDsk = 0x4000
	mouseeventfAbsolute   = 0x8000

	keyeventfExtended = 0x0001
	keyeventfKeyUp    = 0x0002
	keyeventfUnicode  = 0x0004

	esContinuous      = 0x80000000
	esSystemRequired  = 0x00000001
	esDisplayRequired = 0x00000002
)

type rect struct{ Left, Top, Right, Bottom int32 }
type point struct{ X, Y int32 }

type monitorInfoEx struct {
	Size    uint32
	Monitor rect
	Work    rect
	Flags   uint32
	Device  [32]uint16
}

type cursorInfo struct {
	Size   uint32
	Flags  uint32
	Cursor uintptr
	Pos    point
}

type iconInfo struct {
	IsIcon   int32
	XHotspot uint32
	YHotspot uint32
	Mask     uintptr
	Color    uintptr
}

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [1]uint32
}

type mouseInput struct {
	Dx, Dy    int32
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type keybdInput struct {
	Vk        uint16
	Scan      uint16
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type mouseINPUT struct {
	Type uint32
	_    uint32
	Mi   mouseInput
}

type keybdINPUT struct {
	Type uint32
	_    uint32
	Ki   keybdInput
	_    [8]byte
}

func platformInit() {
	// Physical pixels everywhere, so capture and clicks match on scaled (125%/150%) displays.
	if pSetProcessDpiAwarenessContext.Find() == nil {
		r, _, _ := pSetProcessDpiAwarenessContext.Call(^uintptr(3)) // PER_MONITOR_AWARE_V2
		if r == 0 {
			pSetProcessDpiAwarenessContext.Call(^uintptr(2)) // PER_MONITOR_AWARE
		}
	} else if pSetProcessDpiAwareness.Find() == nil {
		pSetProcessDpiAwareness.Call(2)
	} else {
		pSetProcessDPIAware.Call()
	}
	// Turn off QuickEdit: a stray click in the console would freeze the server.
	h, err := syscall.GetStdHandle(syscall.STD_INPUT_HANDLE)
	if err == nil {
		var mode uint32
		if r, _, _ := pGetConsoleMode.Call(uintptr(h), uintptr(unsafe.Pointer(&mode))); r != 0 {
			mode &^= 0x0040 // ENABLE_QUICK_EDIT_MODE
			mode |= 0x0080  // ENABLE_EXTENDED_FLAGS
			pSetConsoleMode.Call(uintptr(h), uintptr(mode))
		}
	}
}

func setConsoleTitle(t string) {
	p, err := syscall.UTF16PtrFromString(t)
	if err == nil {
		pSetConsoleTitleW.Call(uintptr(unsafe.Pointer(p)))
	}
}

func getMetric(i int) int {
	r, _, _ := pGetSystemMetrics.Call(uintptr(i))
	return int(int32(r))
}

// ---------------------------------------------------------------- monitors

var (
	enumMu   sync.Mutex
	enumList []Monitor
	enumCb   = syscall.NewCallback(func(hmon, hdc, lprc, data uintptr) uintptr {
		var mi monitorInfoEx
		mi.Size = uint32(unsafe.Sizeof(mi))
		if r, _, _ := pGetMonitorInfoW.Call(hmon, uintptr(unsafe.Pointer(&mi))); r != 0 {
			enumList = append(enumList, Monitor{
				X: int(mi.Monitor.Left), Y: int(mi.Monitor.Top),
				W: int(mi.Monitor.Right - mi.Monitor.Left), H: int(mi.Monitor.Bottom - mi.Monitor.Top),
				Primary: mi.Flags&1 != 0,
				Name:    syscall.UTF16ToString(mi.Device[:]),
			})
		}
		return 1
	})
)

func listMonitors() []Monitor {
	enumMu.Lock()
	defer enumMu.Unlock()
	enumList = nil
	pEnumDisplayMonitors.Call(0, 0, enumCb, 0)
	mons := enumList
	enumList = nil
	if len(mons) == 0 {
		return []Monitor{{X: 0, Y: 0, W: getMetric(smCxScreen), H: getMetric(smCyScreen), Primary: true}}
	}
	// primary first, then left to right
	sort.SliceStable(mons, func(i, j int) bool {
		if mons[i].Primary != mons[j].Primary {
			return mons[i].Primary
		}
		if mons[i].X != mons[j].X {
			return mons[i].X < mons[j].X
		}
		return mons[i].Y < mons[j].Y
	})
	return mons
}

// ---------------------------------------------------------------- capture

type winCapturer struct {
	screenDC uintptr
	memDC    uintptr
	bmp      uintptr
	oldBmp   uintptr
	bits     unsafe.Pointer
	w, h     int

	prev      []byte
	prevValid bool
	img       *image.RGBA
	hotspots  map[uintptr]point
}

func newCapturer() (capturer, error) {
	dc, _, _ := pGetDC.Call(0)
	if dc == 0 {
		return nil, errors.New("GetDC failed")
	}
	mem, _, _ := pCreateCompatibleDC.Call(dc)
	if mem == 0 {
		pReleaseDC.Call(0, dc)
		return nil, errors.New("CreateCompatibleDC failed")
	}
	return &winCapturer{screenDC: dc, memDC: mem, hotspots: map[uintptr]point{}}, nil
}

func (c *winCapturer) ensure(w, h int) error {
	if c.bmp != 0 && c.w == w && c.h == h {
		return nil
	}
	c.freeBitmap()
	bi := bitmapInfo{Header: bitmapInfoHeader{
		Size: uint32(unsafe.Sizeof(bitmapInfoHeader{})), Width: int32(w), Height: -int32(h),
		Planes: 1, BitCount: 32,
	}}
	var bits unsafe.Pointer
	bmp, _, _ := pCreateDIBSection.Call(c.memDC, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bmp == 0 || bits == nil {
		return errors.New("CreateDIBSection failed")
	}
	c.oldBmp, _, _ = pSelectObject.Call(c.memDC, bmp)
	c.bmp, c.bits, c.w, c.h = bmp, bits, w, h
	c.prev = make([]byte, w*h*4)
	c.prevValid = false
	c.img = image.NewRGBA(image.Rect(0, 0, w, h))
	return nil
}

func (c *winCapturer) freeBitmap() {
	if c.bmp != 0 {
		pSelectObject.Call(c.memDC, c.oldBmp)
		pDeleteObject.Call(c.bmp)
		c.bmp = 0
		c.bits = nil
	}
}

func (c *winCapturer) close() {
	c.freeBitmap()
	if c.memDC != 0 {
		pDeleteDC.Call(c.memDC)
		c.memDC = 0
	}
	if c.screenDC != 0 {
		pReleaseDC.Call(0, c.screenDC)
		c.screenDC = 0
	}
}

func (c *winCapturer) image() *image.RGBA { return c.img }

func (c *winCapturer) hotspot(cur uintptr) point {
	if p, ok := c.hotspots[cur]; ok {
		return p
	}
	var ii iconInfo
	p := point{}
	if r, _, _ := pGetIconInfo.Call(cur, uintptr(unsafe.Pointer(&ii))); r != 0 {
		p = point{int32(ii.XHotspot), int32(ii.YHotspot)}
		if ii.Mask != 0 {
			pDeleteObject.Call(ii.Mask)
		}
		if ii.Color != 0 {
			pDeleteObject.Call(ii.Color)
		}
	}
	if len(c.hotspots) > 256 {
		c.hotspots = map[uintptr]point{}
	}
	c.hotspots[cur] = p
	return p
}

func (c *winCapturer) capture(m Monitor, dw, dh int) (bool, float32, float32, error) {
	if err := c.ensure(dw, dh); err != nil {
		return false, -1, -1, err
	}
	var r uintptr
	if dw == m.W && dh == m.H {
		r, _, _ = pBitBlt.Call(c.memDC, 0, 0, uintptr(dw), uintptr(dh), c.screenDC, uintptr(m.X), uintptr(m.Y), srccopy)
	} else {
		pSetStretchBltMode.Call(c.memDC, halftone)
		pSetBrushOrgEx.Call(c.memDC, 0, 0, 0)
		r, _, _ = pStretchBlt.Call(c.memDC, 0, 0, uintptr(dw), uintptr(dh),
			c.screenDC, uintptr(m.X), uintptr(m.Y), uintptr(m.W), uintptr(m.H), srccopy)
	}
	if r == 0 {
		return false, -1, -1, errors.New("екран недоступний (заблоковано або UAC)")
	}

	var cx, cy float32 = -1, -1
	var ci cursorInfo
	ci.Size = uint32(unsafe.Sizeof(ci))
	if ok, _, _ := pGetCursorInfo.Call(uintptr(unsafe.Pointer(&ci))); ok != 0 && ci.Flags&cursorShown != 0 && ci.Cursor != 0 {
		px, py := int(ci.Pos.X)-m.X, int(ci.Pos.Y)-m.Y
		if px >= 0 && py >= 0 && px < m.W && py < m.H {
			cx, cy = float32(px)/float32(m.W), float32(py)/float32(m.H)
			scale := float64(dw) / float64(m.W)
			size := int(float64(getMetric(smCxCursor)) * scale)
			if size < 16 {
				size = 16
			}
			hs := c.hotspot(ci.Cursor)
			base := float64(getMetric(smCxCursor))
			if base <= 0 {
				base = 32
			}
			k := float64(size) / base
			x := int(float64(px)*scale - float64(hs.X)*k)
			y := int(float64(py)*scale - float64(hs.Y)*k)
			pDrawIconEx.Call(c.memDC, uintptr(x), uintptr(y), ci.Cursor, uintptr(size), uintptr(size), 0, 0, diNormal)
		}
	}
	pGdiFlush.Call()

	src := unsafe.Slice((*byte)(c.bits), dw*dh*4)
	changed := !c.prevValid || !bytes.Equal(src, c.prev)
	if changed {
		copy(c.prev, src)
		c.prevValid = true
		dst := c.img.Pix
		for i := 0; i+3 < len(src); i += 4 {
			dst[i] = src[i+2]
			dst[i+1] = src[i+1]
			dst[i+2] = src[i]
			dst[i+3] = 0xFF
		}
	}
	return changed, cx, cy, nil
}

// ---------------------------------------------------------------- input

func sendMouse(dx, dy int32, data uint32, flags uint32) {
	in := mouseINPUT{Type: inputMouse, Mi: mouseInput{Dx: dx, Dy: dy, MouseData: data, Flags: flags}}
	pSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in))
}

func virtualScreen() (x, y, w, h int) {
	return getMetric(smXVirtualScreen), getMetric(smYVirtualScreen), getMetric(smCxVirtualScreen), getMetric(smCyVirtualScreen)
}

func mouseMoveAbs(x, y int) {
	vx, vy, vw, vh := virtualScreen()
	if vw <= 1 || vh <= 1 {
		return
	}
	if x < vx {
		x = vx
	}
	if y < vy {
		y = vy
	}
	if x > vx+vw-1 {
		x = vx + vw - 1
	}
	if y > vy+vh-1 {
		y = vy + vh - 1
	}
	nx := int32(((x-vx)*65535 + (vw-1)/2) / (vw - 1))
	ny := int32(((y-vy)*65535 + (vh-1)/2) / (vh - 1))
	sendMouse(nx, ny, 0, mouseeventfMove|mouseeventfAbsolute|mouseeventfVirtualDsk)
}

func mouseMoveRel(dx, dy int) {
	var p point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	mouseMoveAbs(int(p.X)+dx, int(p.Y)+dy)
}

func mouseButton(b int, down bool) {
	var f uint32
	switch b {
	case 0:
		f = mouseeventfLeftUp
		if down {
			f = mouseeventfLeftDown
		}
	case 1:
		f = mouseeventfRightUp
		if down {
			f = mouseeventfRightDown
		}
	case 2:
		f = mouseeventfMiddleUp
		if down {
			f = mouseeventfMiddleDown
		}
	default:
		return
	}
	sendMouse(0, 0, 0, f)
}

func mouseWheel(dy, dx int) {
	if dy != 0 {
		sendMouse(0, 0, uint32(int32(dy)), mouseeventfWheel)
	}
	if dx != 0 {
		sendMouse(0, 0, uint32(int32(dx)), mouseeventfHWheel)
	}
}

func isExtendedKey(vk int) bool {
	switch vk {
	case 0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27, 0x28, // PgUp PgDn End Home arrows
		0x2C, 0x2D, 0x2E, // PrtSc Ins Del
		0x5B, 0x5C, 0x5D, // Win Apps
		0x6F, 0x90, // Num / , NumLock
		vkRControl, vkRMenu:
		return true
	}
	return vk >= 0xA6 && vk <= 0xB7 // browser / media / volume keys
}

func keyInput(vk int, up bool) keybdINPUT {
	scan, _, _ := pMapVirtualKeyW.Call(uintptr(vk), 0)
	var f uint32
	if isExtendedKey(vk) {
		f |= keyeventfExtended
	}
	if up {
		f |= keyeventfKeyUp
	}
	return keybdINPUT{Type: inputKeyboard, Ki: keybdInput{Vk: uint16(vk), Scan: uint16(scan), Flags: f}}
}

func sendKeys(ins []keybdINPUT) {
	if len(ins) == 0 {
		return
	}
	pSendInput.Call(uintptr(len(ins)), uintptr(unsafe.Pointer(&ins[0])), unsafe.Sizeof(ins[0]))
}

func keyEvent(vk int, down bool) {
	sendKeys([]keybdINPUT{keyInput(vk, !down)})
}

func typeUnicode(s string) {
	var ins []keybdINPUT
	for _, r := range s {
		switch r {
		case '\r':
			continue
		case '\n':
			ins = append(ins, keyInput(0x0D, false), keyInput(0x0D, true))
			continue
		case '\t':
			ins = append(ins, keyInput(0x09, false), keyInput(0x09, true))
			continue
		case '\b':
			ins = append(ins, keyInput(0x08, false), keyInput(0x08, true))
			continue
		}
		for _, u := range utf16.Encode([]rune{r}) {
			ins = append(ins,
				keybdINPUT{Type: inputKeyboard, Ki: keybdInput{Scan: u, Flags: keyeventfUnicode}},
				keybdINPUT{Type: inputKeyboard, Ki: keybdInput{Scan: u, Flags: keyeventfUnicode | keyeventfKeyUp}})
		}
	}
	sendKeys(ins)
}

func vkForChar(r rune) (int, bool, bool) {
	if r > 0xFFFF {
		return 0, false, false
	}
	res, _, _ := pVkKeyScanW.Call(uintptr(r))
	v := int16(res)
	if v == -1 {
		return 0, false, false
	}
	return int(v & 0xFF), (v>>8)&1 != 0, true
}

// ---------------------------------------------------------------- misc

var awakeCh = func() chan bool {
	ch := make(chan bool, 4)
	go func() {
		// SetThreadExecutionState is per thread, so keep one dedicated thread.
		runtime.LockOSThread()
		for on := range ch {
			if on {
				pSetThreadExecutionState.Call(esContinuous | esSystemRequired | esDisplayRequired)
			} else {
				pSetThreadExecutionState.Call(esContinuous)
			}
		}
	}()
	return ch
}()

// keepAwake stops the PC from sleeping while a phone is connected.
func keepAwake(on bool) {
	select {
	case awakeCh <- on:
	default:
	}
}

func isElevated() bool {
	var tok syscall.Token
	p, err := syscall.GetCurrentProcess()
	if err != nil {
		return false
	}
	if syscall.OpenProcessToken(p, syscall.TOKEN_QUERY, &tok) != nil {
		return false
	}
	defer tok.Close()
	var elevation uint32
	var n uint32
	if syscall.GetTokenInformation(tok, 20 /* TokenElevation */, (*byte)(unsafe.Pointer(&elevation)), 4, &n) != nil {
		return false
	}
	return elevation != 0
}

const firewallRule = "Remote Desktop (fesh-biz)"

func runHidden(cmdline string) error {
	cmd := exec.Command("netsh")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: cmdline, HideWindow: true, CreationFlags: 0x08000000}
	return cmd.Run()
}

// ensureFirewall adds an inbound allow rule for this exe when running as admin.
func ensureFirewall() firewallState {
	if !isElevated() {
		return firewallNotElevated
	}
	exe, err := os.Executable()
	if err != nil {
		return firewallUnknown
	}
	runHidden(`netsh advfirewall firewall delete rule name="` + firewallRule + `"`)
	err = runHidden(`netsh advfirewall firewall add rule name="` + firewallRule + `" dir=in action=allow enable=yes profile=any program="` + exe + `"`)
	if err != nil {
		return firewallUnknown
	}
	return firewallAdded
}
