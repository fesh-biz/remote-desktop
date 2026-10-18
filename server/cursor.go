package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"runtime"
	"sync"
	"time"
)

const (
	cursorPoll    = 4 * time.Millisecond
	cursorMinSend = 15 * time.Millisecond // ≈ 60 updates per second at most
)

// cursorPos is the mouse pointer in virtual-screen coordinates (physical pixels).
type cursorPos struct {
	X, Y    int
	Visible bool
	Shape   uintptr // identifies the pointer image; 0 = unknown
}

type cursorMsg struct {
	x, y, w, h int32
	seq        uint32
	flags      uint8
}

func (m cursorMsg) bytes() []byte {
	b := make([]byte, 21)
	binary.BigEndian.PutUint32(b[0:], uint32(m.x))
	binary.BigEndian.PutUint32(b[4:], uint32(m.y))
	binary.BigEndian.PutUint32(b[8:], uint32(m.w))
	binary.BigEndian.PutUint32(b[12:], uint32(m.h))
	binary.BigEndian.PutUint32(b[16:], m.seq)
	b[20] = m.flags
	return b
}

// cursorLoop sends the pointer position and image to a protocol-2 client.
// It is independent of the (much slower) screen frames, so the client can
// draw the pointer at its display rate.
func (ss *Session) cursorLoop() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	t := time.NewTicker(cursorPoll)
	defer t.Stop()
	var (
		last      cursorMsg
		lastSent  time.Time
		lastShape uintptr = ^uintptr(0)
		sentOnce  bool
	)
	for {
		select {
		case <-ss.done:
			return
		case <-t.C:
		}
		mon := pickMonitor(listMonitorsCached(), ss.settings().mon)
		// read the sequence number first: the position read after it already
		// includes every move up to that number
		seq := ss.moveSeq.Load()
		c := readCursor()

		if c.Visible && c.Shape != lastShape {
			if data, hx, hy, ok := cursorShapePNG(c.Shape); ok {
				var hdr [4]byte
				binary.BigEndian.PutUint16(hdr[0:], uint16(hx))
				binary.BigEndian.PutUint16(hdr[2:], uint16(hy))
				if ss.write(msgShape, hdr[:], data) != nil {
					ss.conn.Close()
					return
				}
			}
			lastShape = c.Shape
		}

		m := cursorMsg{x: int32(c.X - mon.X), y: int32(c.Y - mon.Y), w: int32(mon.W), h: int32(mon.H), seq: seq}
		if c.Visible {
			m.flags = 1
		}
		if sentOnce && m == last {
			continue
		}
		if time.Since(lastSent) < cursorMinSend {
			continue // sent on a later tick
		}
		if ss.write(msgCursor, m.bytes()) != nil {
			ss.conn.Close()
			return
		}
		last, lastSent, sentOnce = m, time.Now(), true
	}
}

// ---------------------------------------------------------------- shapes

var (
	shapeMu    sync.Mutex
	shapeCache = map[uintptr]shapeEntry{}
)

type shapeEntry struct {
	png    []byte
	hx, hy int
	ok     bool
}

// cursorShapePNG returns the pointer image as PNG with transparency.
func cursorShapePNG(h uintptr) ([]byte, int, int, bool) {
	shapeMu.Lock()
	defer shapeMu.Unlock()
	if e, ok := shapeCache[h]; ok {
		return e.png, e.hx, e.hy, e.ok
	}
	var e shapeEntry
	img, hx, hy, err := cursorImage(h)
	if err == nil && img != nil {
		var buf bytes.Buffer
		if png.Encode(&buf, img) == nil {
			e = shapeEntry{png: buf.Bytes(), hx: hx, hy: hy, ok: true}
		}
	}
	if len(shapeCache) > 64 {
		shapeCache = map[uintptr]shapeEntry{}
	}
	shapeCache[h] = e
	return e.png, e.hx, e.hy, e.ok
}

// shapeFromBlackWhite rebuilds a transparent image from the pointer drawn on a
// black and on a white background (BGRA rows). Inverting pixels, as in the text
// I-beam, cannot be shown as-is, so they become black with a white outline.
func shapeFromBlackWhite(black, white []byte, w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	inv := make([]bool, w*h)
	for i := 0; i < w*h; i++ {
		o := i * 4
		bb, bg, br := int(black[o]), int(black[o+1]), int(black[o+2])
		wb, wg, wr := int(white[o]), int(white[o+1]), int(white[o+2])
		db, dg, dr := wb-bb, wg-bg, wr-br
		p := img.Pix[o : o+4]
		if db < -8 || dg < -8 || dr < -8 {
			inv[i] = true
			p[0], p[1], p[2], p[3] = 0, 0, 0, 255
			continue
		}
		d := max(db, dg, dr, 0)
		a := 255 - d
		if a <= 0 {
			continue
		}
		un := func(c int) uint8 {
			v := c * 255 / a
			if v > 255 {
				v = 255
			}
			return uint8(v)
		}
		p[0], p[1], p[2], p[3] = un(br), un(bg), un(bb), uint8(a)
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*w + x
			if inv[i] || img.Pix[i*4+3] != 0 {
				continue
			}
			near := false
			for dy := -1; dy <= 1 && !near; dy++ {
				for dx := -1; dx <= 1; dx++ {
					nx, ny := x+dx, y+dy
					if nx >= 0 && ny >= 0 && nx < w && ny < h && inv[ny*w+nx] {
						near = true
						break
					}
				}
			}
			if near {
				p := img.Pix[i*4 : i*4+4]
				p[0], p[1], p[2], p[3] = 255, 255, 255, 255
			}
		}
	}
	return img
}
