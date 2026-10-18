package main

import "image"

// Monitor is a display in virtual-screen coordinates (physical pixels).
type Monitor struct {
	X, Y, W, H int
	Primary    bool
	Name       string
}

// capturer grabs one monitor scaled to dw×dh. It reports whether the picture
// changed since the previous call and the cursor position (0..1, or -1).
type capturer interface {
	capture(m Monitor, dw, dh int) (changed bool, cx, cy float32, err error)
	image() *image.RGBA
	close()
}

type firewallState int

const (
	firewallUnknown firewallState = iota
	firewallAdded
	firewallNotElevated
)
