package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"testing"
)

// clientFrame builds a masked frame the way a browser sends it.
func clientFrame(op byte, fin bool, payload []byte) []byte {
	var b bytes.Buffer
	h0 := op
	if fin {
		h0 |= 0x80
	}
	b.WriteByte(h0)
	n := len(payload)
	switch {
	case n < 126:
		b.WriteByte(0x80 | byte(n))
	case n < 1<<16:
		b.WriteByte(0x80 | 126)
		binary.Write(&b, binary.BigEndian, uint16(n))
	default:
		b.WriteByte(0x80 | 127)
		binary.Write(&b, binary.BigEndian, uint64(n))
	}
	mask := [4]byte{0x12, 0x34, 0x56, 0x78}
	b.Write(mask[:])
	for i, c := range payload {
		b.WriteByte(c ^ mask[i&3])
	}
	return b.Bytes()
}

func TestWebSocketRoundTrip(t *testing.T) {
	srv, cli := net.Pipe()
	defer srv.Close()
	defer cli.Close()
	tr := newWSTransport(srv, bufio.NewReader(srv))

	big := bytes.Repeat([]byte{'x'}, 70000)
	go func() {
		cli.Write(clientFrame(wsOpPing, true, []byte("hi")))
		cli.Write(clientFrame(wsOpBinary, true, append([]byte{'J'}, `{"t":"ping"}`...)))
		cli.Write(clientFrame(wsOpBinary, false, []byte{'A'}))
		cli.Write(clientFrame(wsOpCont, true, nil))
		cli.Write(clientFrame(wsOpBinary, true, append([]byte{'J'}, big...)))
	}()

	// the ping is answered with a pong while reading
	pong := make(chan []byte, 1)
	go func() {
		h := make([]byte, 4)
		io.ReadFull(cli, h)
		pong <- h
		// then drain the server's own frames
		io.Copy(io.Discard, cli)
	}()

	typ, data, err := tr.readMsg()
	if err != nil || typ != 'J' || string(data) != `{"t":"ping"}` {
		t.Fatalf("got %c %q %v", typ, data, err)
	}
	if h := <-pong; h[0] != 0x80|wsOpPong || h[1] != 2 || string(h[2:]) != "hi" {
		t.Fatalf("bad pong %v", h)
	}
	typ, data, err = tr.readMsg()
	if err != nil || typ != 'A' || len(data) != 0 {
		t.Fatalf("fragmented ack: %c %q %v", typ, data, err)
	}
	typ, data, err = tr.readMsg()
	if err != nil || typ != 'J' || !bytes.Equal(data, big) {
		t.Fatalf("big message: %c len %d %v", typ, len(data), err)
	}
}

func TestWebSocketWrite(t *testing.T) {
	srv, cli := net.Pipe()
	defer srv.Close()
	defer cli.Close()
	tr := newWSTransport(srv, bufio.NewReader(srv))
	payload := bytes.Repeat([]byte{7}, 300)
	go tr.writeMsg('F', []byte{1, 2}, payload)
	h := make([]byte, 4)
	io.ReadFull(cli, h)
	if h[0] != 0x82 || h[1] != 126 || binary.BigEndian.Uint16(h[2:]) != 303 {
		t.Fatalf("bad header %v", h)
	}
	body := make([]byte, 303)
	io.ReadFull(cli, body)
	if body[0] != 'F' || body[1] != 1 || body[2] != 2 || body[3] != 7 {
		t.Fatalf("bad body %v", body[:4])
	}
}

func TestWSAccept(t *testing.T) {
	// example from RFC 6455
	if got := wsAccept("dGhlIHNhbXBsZSBub25jZQ=="); got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatal(got)
	}
}

func TestShapeFromBlackWhite(t *testing.T) {
	const w, h = 4, 1
	black := make([]byte, w*h*4)
	white := make([]byte, w*h*4)
	px := func(buf []byte, i int, v byte) { buf[i*4], buf[i*4+1], buf[i*4+2] = v, v, v }
	// 0: transparent, 1: opaque white, 2: half-transparent black, 3: inverting
	px(black, 0, 0)
	px(white, 0, 255)
	px(black, 1, 255)
	px(white, 1, 255)
	px(black, 2, 0)
	px(white, 2, 128)
	px(black, 3, 255)
	px(white, 3, 0)
	img := shapeFromBlackWhite(black, white, w, h)
	a := func(i int) uint8 { return img.Pix[i*4+3] }
	if a(0) != 0 { // not next to the inverting pixel: stays transparent
		t.Fatalf("pixel 0 alpha %d", a(0))
	}
	if a(1) != 255 || img.Pix[4] != 255 {
		t.Fatalf("pixel 1 %v", img.Pix[4:8])
	}
	if a(2) < 120 || a(2) > 135 || img.Pix[8] != 0 {
		t.Fatalf("pixel 2 %v", img.Pix[8:12])
	}
	if a(3) != 255 || img.Pix[12] != 0 {
		t.Fatalf("pixel 3 %v", img.Pix[12:16])
	}
}
