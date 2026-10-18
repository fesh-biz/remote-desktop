package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// transport carries protocol messages over either the raw TCP framing used
// by the Android app or WebSocket binary messages used by the browser client.
type transport interface {
	readMsg() (byte, []byte, error)
	writeMsg(typ byte, parts ...[]byte) error
}

// ---------------------------------------------------------------- raw TCP

type tcpTransport struct {
	c  net.Conn
	br *bufio.Reader

	mu sync.Mutex
	bw *bufio.Writer
}

func newTCPTransport(c net.Conn, br *bufio.Reader) *tcpTransport {
	return &tcpTransport{c: c, br: br, bw: bufio.NewWriterSize(c, 256<<10)}
}

func (t *tcpTransport) readMsg() (byte, []byte, error) { return readMsg(t.br) }

func (t *tcpTransport) writeMsg(typ byte, parts ...[]byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	total := 0
	for _, p := range parts {
		total += len(p)
	}
	var hdr [5]byte
	hdr[0] = typ
	binary.BigEndian.PutUint32(hdr[1:], uint32(total))
	t.c.SetWriteDeadline(time.Now().Add(15 * time.Second))
	if _, err := t.bw.Write(hdr[:]); err != nil {
		return err
	}
	for _, p := range parts {
		if _, err := t.bw.Write(p); err != nil {
			return err
		}
	}
	return t.bw.Flush()
}

func readMsg(r *bufio.Reader) (byte, []byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[1:])
	if n > maxMsgSize {
		return 0, nil, errors.New("message too large")
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, nil, err
	}
	return hdr[0], buf, nil
}

// ---------------------------------------------------------------- WebSocket

// wsTransport: each binary WebSocket message is one protocol message,
// the first byte being its type ('J', 'F', 'A', ...).
type wsTransport struct {
	c  net.Conn
	br *bufio.Reader

	mu sync.Mutex
	bw *bufio.Writer
}

const (
	wsOpCont   = 0x0
	wsOpText   = 0x1
	wsOpBinary = 0x2
	wsOpClose  = 0x8
	wsOpPing   = 0x9
	wsOpPong   = 0xA
)

func newWSTransport(c net.Conn, br *bufio.Reader) *wsTransport {
	return &wsTransport{c: c, br: br, bw: bufio.NewWriterSize(c, 256<<10)}
}

func (t *wsTransport) readFrame() (fin bool, op byte, payload []byte, err error) {
	var h [2]byte
	if _, err = io.ReadFull(t.br, h[:]); err != nil {
		return
	}
	fin = h[0]&0x80 != 0
	op = h[0] & 0x0F
	masked := h[1]&0x80 != 0
	n := uint64(h[1] & 0x7F)
	switch n {
	case 126:
		var b [2]byte
		if _, err = io.ReadFull(t.br, b[:]); err != nil {
			return
		}
		n = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err = io.ReadFull(t.br, b[:]); err != nil {
			return
		}
		n = binary.BigEndian.Uint64(b[:])
	}
	if n > maxMsgSize {
		err = errors.New("websocket frame too large")
		return
	}
	var mask [4]byte
	if masked {
		if _, err = io.ReadFull(t.br, mask[:]); err != nil {
			return
		}
	}
	payload = make([]byte, n)
	if _, err = io.ReadFull(t.br, payload); err != nil {
		return
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i&3]
		}
	}
	return
}

func (t *wsTransport) readMsg() (byte, []byte, error) {
	var msg []byte
	for {
		fin, op, p, err := t.readFrame()
		if err != nil {
			return 0, nil, err
		}
		switch op {
		case wsOpClose:
			t.writeFrame(wsOpClose, p)
			return 0, nil, io.EOF
		case wsOpPing:
			if err := t.writeFrame(wsOpPong, p); err != nil {
				return 0, nil, err
			}
			continue
		case wsOpPong:
			continue
		case wsOpText, wsOpBinary:
			msg = p
		case wsOpCont:
			msg = append(msg, p...)
			if len(msg) > maxMsgSize {
				return 0, nil, errors.New("websocket message too large")
			}
		default:
			return 0, nil, errors.New("bad websocket opcode")
		}
		if fin {
			if len(msg) == 0 {
				continue
			}
			return msg[0], msg[1:], nil
		}
	}
}

func (t *wsTransport) writeFrame(op byte, parts ...[]byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	var hdr [10]byte
	hdr[0] = 0x80 | op
	hl := 2
	switch {
	case n < 126:
		hdr[1] = byte(n)
	case n < 1<<16:
		hdr[1] = 126
		binary.BigEndian.PutUint16(hdr[2:], uint16(n))
		hl = 4
	default:
		hdr[1] = 127
		binary.BigEndian.PutUint64(hdr[2:], uint64(n))
		hl = 10
	}
	t.c.SetWriteDeadline(time.Now().Add(15 * time.Second))
	if _, err := t.bw.Write(hdr[:hl]); err != nil {
		return err
	}
	for _, p := range parts {
		if _, err := t.bw.Write(p); err != nil {
			return err
		}
	}
	return t.bw.Flush()
}

func (t *wsTransport) writeMsg(typ byte, parts ...[]byte) error {
	all := make([][]byte, 0, len(parts)+1)
	all = append(all, []byte{typ})
	all = append(all, parts...)
	return t.writeFrame(wsOpBinary, all...)
}

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

func wsAccept(key string) string {
	h := sha1.Sum([]byte(key + wsGUID))
	return base64.StdEncoding.EncodeToString(h[:])
}

// ---------------------------------------------------------------- HTTP

// serveHTTP answers one HTTP request on a connection that did not start with
// the binary protocol. It returns a WebSocket transport for "/ws" upgrades and
// nil after serving a static file (the connection is then closed).
func serveHTTP(c net.Conn, br *bufio.Reader) transport {
	req, err := http.ReadRequest(br)
	if err != nil {
		return nil
	}
	if req.URL.Path == "/ws" && strings.EqualFold(req.Header.Get("Upgrade"), "websocket") {
		if !sameOrigin(req) {
			writeHTTP(c, 403, "text/plain; charset=utf-8", []byte("forbidden"))
			return nil
		}
		key := req.Header.Get("Sec-WebSocket-Key")
		if key == "" {
			writeHTTP(c, 400, "text/plain; charset=utf-8", []byte("bad request"))
			return nil
		}
		resp := "HTTP/1.1 101 Switching Protocols\r\n" +
			"Upgrade: websocket\r\nConnection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: " + wsAccept(key) + "\r\n\r\n"
		c.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if _, err := c.Write([]byte(resp)); err != nil {
			return nil
		}
		return newWSTransport(c, br)
	}
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		writeHTTP(c, 405, "text/plain; charset=utf-8", []byte("method not allowed"))
		return nil
	}
	switch req.URL.Path {
	case "/", "/index.html":
		writeHTTP(c, 200, "text/html; charset=utf-8", webIndex)
	case "/favicon.ico":
		writeHTTP(c, 200, "image/x-icon", webIcon)
	default:
		writeHTTP(c, 404, "text/plain; charset=utf-8", []byte("not found"))
	}
	return nil
}

// sameOrigin blocks other web sites open in the laptop's browser from
// talking to the server: only our own page may open the WebSocket.
func sameOrigin(req *http.Request) bool {
	o := req.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, req.Host)
}

func writeHTTP(c net.Conn, code int, ctype string, body []byte) {
	hdr := "HTTP/1.1 " + strconv.Itoa(code) + " " + http.StatusText(code) + "\r\n" +
		"Content-Type: " + ctype + "\r\n" +
		"Content-Length: " + strconv.Itoa(len(body)) + "\r\n" +
		"Cache-Control: no-cache\r\n" +
		"X-Content-Type-Options: nosniff\r\n" +
		"Connection: close\r\n\r\n"
	c.SetWriteDeadline(time.Now().Add(15 * time.Second))
	c.Write([]byte(hdr))
	c.Write(body)
}
