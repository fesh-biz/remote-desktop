//go:build windows

package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

// The tray window and its message loop live on the main OS thread.
func init() { runtime.LockOSThread() }

var (
	shell32  = syscall.NewLazyDLL("shell32.dll")
	advapi32 = syscall.NewLazyDLL("advapi32.dll")

	pRegisterClassExW       = user32.NewProc("RegisterClassExW")
	pCreateWindowExW        = user32.NewProc("CreateWindowExW")
	pDefWindowProcW         = user32.NewProc("DefWindowProcW")
	pGetMessageW            = user32.NewProc("GetMessageW")
	pTranslateMessage       = user32.NewProc("TranslateMessage")
	pDispatchMessageW       = user32.NewProc("DispatchMessageW")
	pPostMessageW           = user32.NewProc("PostMessageW")
	pPostQuitMessage        = user32.NewProc("PostQuitMessage")
	pLoadImageW             = user32.NewProc("LoadImageW")
	pLoadIconW              = user32.NewProc("LoadIconW")
	pCreatePopupMenu        = user32.NewProc("CreatePopupMenu")
	pAppendMenuW            = user32.NewProc("AppendMenuW")
	pTrackPopupMenu         = user32.NewProc("TrackPopupMenu")
	pDestroyMenu            = user32.NewProc("DestroyMenu")
	pSetMenuDefaultItem     = user32.NewProc("SetMenuDefaultItem")
	pSetForegroundWindow    = user32.NewProc("SetForegroundWindow")
	pMessageBoxW            = user32.NewProc("MessageBoxW")
	pRegisterWindowMessageW = user32.NewProc("RegisterWindowMessageW")
	pShellNotifyIconW       = shell32.NewProc("Shell_NotifyIconW")
	pShellExecuteW          = shell32.NewProc("ShellExecuteW")
	pGetModuleHandleW       = kernel32.NewProc("GetModuleHandleW")
	pCreateMutexW           = kernel32.NewProc("CreateMutexW")
	pRegCreateKeyExW        = advapi32.NewProc("RegCreateKeyExW")
	pRegSetValueExW         = advapi32.NewProc("RegSetValueExW")
	pRegDeleteValueW        = advapi32.NewProc("RegDeleteValueW")
	pRegCloseKey            = advapi32.NewProc("RegCloseKey")
)

const (
	wmNull        = 0x0000
	wmDestroy     = 0x0002
	wmLButtonUp   = 0x0202
	wmRButtonUp   = 0x0205
	wmApp         = 0x8000
	wmTray        = wmApp + 1
	wmNotes       = wmApp + 2
	wmRetryIcon   = wmApp + 3
	nimAdd        = 0
	nimModify     = 1
	nimDelete     = 2
	nifMessage    = 0x01
	nifIcon       = 0x02
	nifTip        = 0x04
	nifInfo       = 0x10
	niifInfo      = 0x01
	niifNoSound   = 0x10
	mfString      = 0x0000
	mfGrayed      = 0x0001
	mfChecked     = 0x0008
	mfSeparator   = 0x0800
	tpmRightBtn   = 0x0002
	tpmNoNotify   = 0x0080
	tpmReturnCmd  = 0x0100
	mbOK          = 0x00000000
	mbYesNo       = 0x00000004
	mbIconError   = 0x00000010
	mbIconQuest   = 0x00000020
	mbIconInfo    = 0x00000040
	mbSetFg       = 0x00010000
	mbTopmost     = 0x00040000
	idYes         = 6
	imageIcon     = 1
	smCxSmIcon    = 49
	smCySmIcon    = 50
	idiApp        = 32512
	hkeyCurUser   = 0x80000001
	keySetValue   = 0x0002
	keyQueryValue = 0x0001
	regSZ         = 1
)

const (
	cmdInfo = 1 + iota
	cmdAutostart
	cmdNewPIN
	cmdLog
	cmdExit
)

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type winMsg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	Private uint32
}

type notifyIconData struct {
	Size            uint32
	Wnd             uintptr
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            uintptr
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Version         uint32
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GuidItem        [16]byte
	BalloonIcon     uintptr
}

type trayNote struct{ title, text string }

type tray struct {
	app            *App
	hwnd           uintptr
	icon           uintptr
	taskbarCreated uint32
	added          bool
	retries        int

	mu     sync.Mutex
	notes  []trayNote
	dialog atomic.Bool
}

var (
	theTray   *tray
	wndProcCb = syscall.NewCallback(trayWndProc)
)

func utf16p(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func copyUTF16(dst []uint16, s string) {
	u := utf16.Encode([]rune(s))
	if len(u) > len(dst)-1 {
		u = u[:len(dst)-1]
	}
	copy(dst, u)
	dst[len(u)] = 0
}

func runUI(app *App) {
	t := &tray{app: app}
	theTray = t
	if err := t.create(); err != nil {
		logf("Значок у треї недоступний: %v", err)
		select {}
	}
	app.srv.setOnEvent(t.onClient)
	if !app.startup {
		t.notify("Remote Desktop працює у треї",
			"PIN-код: "+app.config().PIN+"\nКлацніть значок, щоб побачити адресу для телефона й ноутбука.")
	}
	var m winMsg
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	t.removeIcon()
	logf("Сервер зупинено")
}

func (t *tray) create() error {
	inst, _, _ := pGetModuleHandleW.Call(0)
	cls := utf16p("RemoteDesktopTray")
	wc := wndClassEx{WndProc: wndProcCb, Instance: inst, ClassName: cls}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if r, _, err := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return fmt.Errorf("RegisterClassEx: %v", err)
	}
	hwnd, _, err := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(utf16p("Remote Desktop"))),
		0, 0, 0, 0, 0, 0, 0, inst, 0)
	if hwnd == 0 {
		return fmt.Errorf("CreateWindowEx: %v", err)
	}
	t.hwnd = hwnd
	t.icon, _, _ = pLoadImageW.Call(inst, 1, imageIcon, uintptr(getMetric(smCxSmIcon)), uintptr(getMetric(smCySmIcon)), 0)
	if t.icon == 0 {
		t.icon, _, _ = pLoadIconW.Call(0, idiApp)
	}
	t.taskbarCreated = uint32(func() uintptr {
		r, _, _ := pRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(utf16p("TaskbarCreated"))))
		return r
	}())
	t.addIcon()
	return nil
}

func (t *tray) nid() *notifyIconData {
	n := &notifyIconData{Wnd: t.hwnd, ID: 1}
	n.Size = uint32(unsafe.Sizeof(*n))
	return n
}

func (t *tray) tip() string {
	s := "Remote Desktop — PIN " + t.app.config().PIN
	if n := len(t.app.srv.clients()); n > 0 {
		s += fmt.Sprintf("\nПідключено: %d", n)
	}
	return s
}

func (t *tray) addIcon() {
	n := t.nid()
	n.Flags = nifMessage | nifIcon | nifTip
	n.CallbackMessage = wmTray
	n.Icon = t.icon
	copyUTF16(n.Tip[:], t.tip())
	r, _, _ := pShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(n)))
	if r != 0 {
		t.added = true
		t.retries = 0
		return
	}
	// right after sign-in the taskbar may not exist yet
	if t.retries < 60 {
		t.retries++
		hwnd := t.hwnd
		time.AfterFunc(2*time.Second, func() { pPostMessageW.Call(hwnd, wmRetryIcon, 0, 0) })
	}
}

func (t *tray) removeIcon() {
	if t.added {
		pShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(t.nid())))
		t.added = false
	}
}

// notify shows a balloon / toast; safe to call from any goroutine.
func (t *tray) notify(title, text string) {
	t.mu.Lock()
	t.notes = append(t.notes, trayNote{title, text})
	t.mu.Unlock()
	pPostMessageW.Call(t.hwnd, wmNotes, 0, 0)
}

func (t *tray) refresh() { pPostMessageW.Call(t.hwnd, wmNotes, 0, 0) }

func (t *tray) flushNotes() {
	t.mu.Lock()
	notes := t.notes
	t.notes = nil
	t.mu.Unlock()
	if !t.added {
		return
	}
	n := t.nid()
	n.Flags = nifTip
	copyUTF16(n.Tip[:], t.tip())
	if len(notes) > 0 {
		last := notes[len(notes)-1]
		n.Flags |= nifInfo
		n.InfoFlags = niifInfo | niifNoSound
		copyUTF16(n.Info[:], last.text)
		copyUTF16(n.InfoTitle[:], last.title)
	}
	pShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(n)))
}

func (t *tray) onClient(connected bool, client, ip string) {
	if connected {
		t.notify("Підключено", client+" ("+ip+")")
	} else {
		t.refresh()
	}
}

func trayWndProc(hwnd, msg, wp, lp uintptr) uintptr {
	t := theTray
	if t != nil && t.hwnd != 0 {
		switch uint32(msg) {
		case wmTray:
			switch uint32(lp) & 0xFFFF {
			case wmLButtonUp:
				t.showInfo()
			case wmRButtonUp:
				t.showMenu()
			}
			return 0
		case wmNotes:
			t.flushNotes()
			return 0
		case wmRetryIcon:
			if !t.added {
				t.addIcon()
			}
			return 0
		case wmDestroy:
			pPostQuitMessage.Call(0)
			return 0
		}
		if t.taskbarCreated != 0 && uint32(msg) == t.taskbarCreated {
			// Explorer restarted: the icon has to be added again
			t.added = false
			t.retries = 0
			t.addIcon()
			return 0
		}
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, msg, wp, lp)
	return r
}

func appendMenu(m uintptr, flags uint32, id int, text string) {
	var p uintptr
	if text != "" {
		p = uintptr(unsafe.Pointer(utf16p(text)))
	}
	pAppendMenuW.Call(m, uintptr(flags), uintptr(id), p)
}

func (t *tray) showMenu() {
	m, _, _ := pCreatePopupMenu.Call()
	if m == 0 {
		return
	}
	defer pDestroyMenu.Call(m)
	appendMenu(m, mfString|mfGrayed, 0, "Remote Desktop "+version+"  ·  PIN "+t.app.config().PIN)
	appendMenu(m, mfSeparator, 0, "")
	appendMenu(m, mfString, cmdInfo, "Показати PIN-код і адресу")
	pSetMenuDefaultItem.Call(m, cmdInfo, 0)
	clients := t.app.srv.clients()
	for i, c := range clients {
		if i == 4 {
			appendMenu(m, mfString|mfGrayed, 0, fmt.Sprintf("   … ще %d", len(clients)-4))
			break
		}
		appendMenu(m, mfString|mfGrayed, 0, "Підключено: "+c)
	}
	appendMenu(m, mfSeparator, 0, "")
	var auto uint32 = mfString
	if t.app.autostart() {
		auto |= mfChecked
	}
	appendMenu(m, auto, cmdAutostart, "Запускати разом з Windows")
	appendMenu(m, mfString, cmdNewPIN, "Згенерувати новий PIN-код…")
	if t.app.logPath != "" {
		appendMenu(m, mfString, cmdLog, "Відкрити журнал")
	}
	appendMenu(m, mfSeparator, 0, "")
	appendMenu(m, mfString, cmdExit, "Вийти")

	var pt point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	pSetForegroundWindow.Call(t.hwnd)
	cmd, _, _ := pTrackPopupMenu.Call(m, tpmReturnCmd|tpmNoNotify|tpmRightBtn, uintptr(pt.X), uintptr(pt.Y), 0, t.hwnd, 0)
	pPostMessageW.Call(t.hwnd, wmNull, 0, 0)

	switch int(cmd) {
	case cmdInfo:
		t.showInfo()
	case cmdAutostart:
		on := !t.app.autostart()
		if err := t.app.setAutostartPref(on); err != nil {
			go showError("Не вдалося змінити автозапуск: " + err.Error())
		} else if on {
			t.notify("Автозапуск увімкнено", "Remote Desktop запускатиметься разом з Windows.")
		} else {
			t.notify("Автозапуск вимкнено", "Remote Desktop не запускатиметься разом з Windows.")
		}
	case cmdNewPIN:
		t.askNewPIN()
	case cmdLog:
		pShellExecuteW.Call(0, uintptr(unsafe.Pointer(utf16p("open"))), uintptr(unsafe.Pointer(utf16p(t.app.logPath))), 0, 0, 1)
	case cmdExit:
		t.removeIcon()
		pPostQuitMessage.Call(0)
	}
}

// dialogs run on their own thread so the tray keeps responding
func (t *tray) inDialog(f func()) {
	if !t.dialog.CompareAndSwap(false, true) {
		return
	}
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer t.dialog.Store(false)
		f()
	}()
}

func (t *tray) showInfo() {
	t.inDialog(func() {
		messageBox("Remote Desktop", t.app.infoText(), mbOK|mbIconInfo)
	})
}

func (t *tray) askNewPIN() {
	t.inDialog(func() {
		r := messageBox("Remote Desktop", "Згенерувати новий PIN-код?\n\nТелефон і браузер попросять ввести новий код під час наступного підключення. Поточні підключення не розриваються.", mbYesNo|mbIconQuest)
		if r != idYes {
			return
		}
		t.app.newPIN()
		t.refresh()
		messageBox("Remote Desktop", t.app.infoText(), mbOK|mbIconInfo)
	})
}

func messageBox(title, text string, flags uintptr) int {
	r, _, _ := pMessageBoxW.Call(0, uintptr(unsafe.Pointer(utf16p(text))), uintptr(unsafe.Pointer(utf16p(title))), flags|mbSetFg|mbTopmost)
	return int(r)
}

func showMessage(title, text string) { messageBox(title, text, mbOK|mbIconInfo) }

func showError(text string) { messageBox("Remote Desktop", text, mbOK|mbIconError) }

// ---------------------------------------------------------------- single instance

var instanceMutex uintptr

func singleInstance() bool {
	h, _, err := pCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(utf16p("Local\\RemoteDesktop-fesh-biz"))))
	if h == 0 {
		return true
	}
	instanceMutex = h
	if errno, ok := err.(syscall.Errno); ok && errno == 183 { // ERROR_ALREADY_EXISTS
		return false
	}
	return true
}

// ---------------------------------------------------------------- autostart

const (
	runKey   = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValue = "Remote Desktop (fesh-biz)"
)

// setAutostart adds or removes this exe in the user's Run key. Called on every
// launch while enabled, so moving the exe to another folder keeps it working.
func setAutostart(on bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if strings.Contains(strings.ToLower(exe), `\go-build`) {
		return nil // `go run` from a temp folder
	}
	var key uintptr
	if r, _, _ := pRegCreateKeyExW.Call(hkeyCurUser, uintptr(unsafe.Pointer(utf16p(runKey))), 0, 0, 0,
		keySetValue|keyQueryValue, 0, uintptr(unsafe.Pointer(&key)), 0); r != 0 {
		return syscall.Errno(r)
	}
	defer pRegCloseKey.Call(key)
	name := utf16p(runValue)
	if !on {
		r, _, _ := pRegDeleteValueW.Call(key, uintptr(unsafe.Pointer(name)))
		if r != 0 && r != 2 { // 2 = ERROR_FILE_NOT_FOUND: already off
			return syscall.Errno(r)
		}
		return nil
	}
	data := utf16.Encode([]rune(`"` + exe + `" -startup` + "\x00"))
	if r, _, _ := pRegSetValueExW.Call(key, uintptr(unsafe.Pointer(name)), 0, regSZ,
		uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)*2)); r != 0 {
		return syscall.Errno(r)
	}
	return nil
}
