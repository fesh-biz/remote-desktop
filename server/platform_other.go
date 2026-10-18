//go:build !windows

// Stub implementation so the protocol can be developed and tested off Windows.
package main

import (
	"fmt"
	"image"
	"image/color"
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

func (c *stubCapturer) capture(m Monitor, dw, dh int) (bool, float32, float32, error) {
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

func mouseMoveAbs(x, y int)        { fmt.Printf("INPUT mouseAbs %d %d\n", x, y) }
func mouseMoveRel(dx, dy int)      { fmt.Printf("INPUT mouseRel %d %d\n", dx, dy) }
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
