//go:build !windows

// Stub implementation so the protocol can be developed and tested off Windows.
package main

import (
	"fmt"
	"image"
	"image/color"
	"sync"
	"time"
)

func platformInit()                 {}
func setConsoleTitle(string)        {}
func keepAwake(bool)                {}
func ensureFirewall() firewallState { return firewallUnknown }

func listMonitors() []Monitor {
	return []Monitor{{W: 1920, H: 1080, Primary: true, Name: "stub"}, {X: 1920, W: 1280, H: 1024, Name: "stub2"}}
}

type stubCapturer struct {
	img  *image.RGBA
	last int64
}

func newCapturer() (capturer, error) { return &stubCapturer{}, nil }

func (c *stubCapturer) capture(m Monitor, dw, dh int, drawCursor bool) (bool, float32, float32, error) {
	if c.img == nil || c.img.Bounds().Dx() != dw || c.img.Bounds().Dy() != dh {
		c.img = image.NewRGBA(image.Rect(0, 0, dw, dh))
		c.last = -1
	}
	t := time.Now().UnixMilli() / 500
	if t == c.last {
		return false, 0.5, 0.5, nil
	}
	c.last = t
	off := int(t*20) % dw
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			c.img.SetRGBA(x, y, color.RGBA{uint8(x * 255 / dw), uint8(y * 255 / dh), uint8((x + off) % 256), 255})
		}
	}
	return true, 0.5, 0.5, nil
}
func (c *stubCapturer) image() *image.RGBA { return c.img }
func (c *stubCapturer) close()             {}

var (
	stubMu       sync.Mutex
	stubX, stubY = 960, 540
)

func virtualScreen() (x, y, w, h int) { return 0, 0, 3200, 1080 }

func clampVirtual(x, y int) (int, int) {
	vx, vy, vw, vh := virtualScreen()
	return min(max(x, vx), vx+vw-1), min(max(y, vy), vy+vh-1)
}

func mouseMoveAbs(x, y int) {
	stubMu.Lock()
	stubX, stubY = clampVirtual(x, y)
	stubMu.Unlock()
	fmt.Printf("INPUT mouseAbs %d %d\n", x, y)
}

func mouseMoveRel(dx, dy int) {
	stubMu.Lock()
	stubX, stubY = clampVirtual(stubX+dx, stubY+dy)
	stubMu.Unlock()
	fmt.Printf("INPUT mouseRel %d %d\n", dx, dy)
}

func readCursor() cursorPos {
	stubMu.Lock()
	defer stubMu.Unlock()
	// the stub pointer changes its image on the right half of the screen
	shape := uintptr(1)
	if stubX > 960 {
		shape = 2
	}
	return cursorPos{X: stubX, Y: stubY, Visible: true, Shape: shape}
}

// cursorImage draws a simple arrow (shape 1) or a text I-beam (shape 2).
func cursorImage(h uintptr) (*image.NRGBA, int, int, error) {
	const w, hh = 32, 32
	black := make([]byte, w*hh*4)
	white := make([]byte, w*hh*4)
	for i := 0; i < w*hh; i++ {
		white[i*4], white[i*4+1], white[i*4+2], white[i*4+3] = 255, 255, 255, 255
	}
	set := func(x, y int, v byte, invert bool) {
		o := (y*w + x) * 4
		if invert {
			black[o], black[o+1], black[o+2] = 255, 255, 255
			white[o], white[o+1], white[o+2] = 0, 0, 0
			return
		}
		black[o], black[o+1], black[o+2] = v, v, v
		white[o], white[o+1], white[o+2] = v, v, v
	}
	if h == 2 {
		for y := 4; y < 26; y++ {
			set(15, y, 0, true)
		}
		for x := 12; x < 19; x++ {
			set(x, 4, 0, true)
			set(x, 25, 0, true)
		}
		return shapeFromBlackWhite(black, white, w, hh), 15, 15, nil
	}
	for y := 0; y < 20; y++ {
		for x := 0; x <= y*2/3 && x < w; x++ {
			v := byte(255)
			if x == 0 || x == y*2/3 || y == 19 {
				v = 0
			}
			set(x, y, v, false)
		}
	}
	return shapeFromBlackWhite(black, white, w, hh), 0, 0, nil
}
func mouseButton(b int, down bool) { fmt.Printf("INPUT button %d %v\n", b, down) }
func mouseWheel(dy, dx int)        { fmt.Printf("INPUT wheel %d %d\n", dy, dx) }
func keyEvent(vk int, down bool)   { fmt.Printf("INPUT key %#x %v\n", vk, down) }
func typeUnicode(s string)         { fmt.Printf("INPUT text %q\n", s) }

func vkForChar(r rune) (int, bool, bool) {
	switch {
	case r >= 'a' && r <= 'z':
		return int(r - 'a' + 0x41), false, true
	case r >= '0' && r <= '9':
		return int(r), false, true
	}
	return 0, false, false
}
