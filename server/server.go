package main

import (
	"bufio"
	"bytes"
	"crypto/subtle"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image/jpeg"
	"io"
	"math"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// Wire format (both directions): 1 byte type, 4 bytes big-endian length, payload.
//
//	'J' — JSON control message
//	'F' — frame (server→client): float32 cursorX, float32 cursorY (0..1, -1 = hidden), JPEG
//	'A' — frame acknowledgement (client→server), empty payload
const (
	msgJSON  = 'J'
	msgFrame = 'F'
	msgAck   = 'A'

	maxMsgSize      = 4 << 20
	protocolVersion = 1
	maxInFlight     = 2
)

type Server struct {
	cfg  Config
	name string

	mu       sync.Mutex
	sessions map[*Session]struct{}
	fails    map[string]*failInfo
}

type failInfo struct {
	count int
	until time.Time
}

func newServer(cfg Config, name string) *Server {
	return &Server{cfg: cfg, name: name, sessions: map[*Session]struct{}{}, fails: map[string]*failInfo{}}
}

func (s *Server) serve(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			logf("Помилка прийому з'єднання: %v", err)
			time.Sleep(200 * time.Millisecond)
			continue
		}
		go s.handle(c)
	}
}

func (s *Server) isBlocked(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.fails[ip]
	return f != nil && time.Now().Before(f.until)
}

func (s *Server) registerFail(ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.fails[ip]
	if f == nil {
		f = &failInfo{}
		s.fails[ip] = f
	}
	f.count++
	if f.count >= 5 {
		f.until = time.Now().Add(60 * time.Second)
		f.count = 0
	}
}

func (s *Server) clearFail(ip string) {
	s.mu.Lock()
	delete(s.fails, ip)
	s.mu.Unlock()
}

func (s *Server) add(ss *Session) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[ss] = struct{}{}
	keepAwake(len(s.sessions) > 0)
	return len(s.sessions)
}

func (s *Server) remove(ss *Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, ss)
	keepAwake(len(s.sessions) > 0)
}

type helloMsg struct {
	T      string `json:"t"`
	PIN    string `json:"pin"`
	Client string `json:"client"`
	Ver    int    `json:"ver"`
}

func (s *Server) handle(c net.Conn) {
	defer c.Close()
	if tc, ok := c.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
		tc.SetWriteBuffer(1 << 20)
	}
	ip, _, _ := net.SplitHostPort(c.RemoteAddr().String())

	ss := &Session{
		srv:     s,
		conn:    c,
		bw:      bufio.NewWriterSize(c, 256<<10),
		credits: make(chan struct{}, maxInFlight+1),
		wake:    make(chan struct{}, 1),
		done:    make(chan struct{}),
		input:   newInputState(),
	}
	ss.st = streamSettings{quality: 60, maxW: 1280, fps: 30, mon: 0}

	if s.isBlocked(ip) {
		ss.sendJSON(map[string]any{"t": "auth", "ok": false, "msg": "Забагато невдалих спроб. Зачекайте хвилину."})
		return
	}

	br := bufio.NewReaderSize(c, 64<<10)
	c.SetReadDeadline(time.Now().Add(15 * time.Second))
	typ, data, err := readMsg(br)
	if err != nil || typ != msgJSON {
		return
	}
	var hello helloMsg
	if json.Unmarshal(data, &hello) != nil || hello.T != "hello" {
		return
	}
	if subtle.ConstantTimeCompare([]byte(hello.PIN), []byte(s.cfg.PIN)) != 1 {
		s.registerFail(ip)
		logf("Невірний PIN від %s", ip)
		time.Sleep(700 * time.Millisecond)
		ss.sendJSON(map[string]any{"t": "auth", "ok": false, "msg": "Невірний PIN-код"})
		return
	}
	s.clearFail(ip)

	client := hello.Client
	if client == "" {
		client = "телефон"
	}
	if err := ss.sendJSON(map[string]any{
		"t": "auth", "ok": true, "host": s.name, "ver": protocolVersion, "server": version,
		"mons": monitorsJSON(listMonitors()), "mon": 0,
	}); err != nil {
		return
	}

	n := s.add(ss)
	logf("Підключено: %s (%s). Активних підключень: %d", client, ip, n)
	defer func() {
		s.remove(ss)
		logf("Відключено: %s (%s)", client, ip)
	}()

	for i := 0; i < maxInFlight; i++ {
		ss.credits <- struct{}{}
	}
	go ss.streamLoop()
	defer ss.close()

	for {
		c.SetReadDeadline(time.Now().Add(20 * time.Second))
		typ, data, err := readMsg(br)
		if err != nil {
			return
		}
		switch typ {
		case msgAck:
			select {
			case ss.credits <- struct{}{}:
			default:
			}
		case msgJSON:
			ss.handleJSON(data)
		}
	}
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

type streamSettings struct {
	quality int
	maxW    int
	fps     int
	mon     int
}

type Session struct {
	srv  *Server
	conn net.Conn

	wmu sync.Mutex
	bw  *bufio.Writer

	smu sync.Mutex
	st  streamSettings

	force   atomic.Bool
	credits chan struct{}
	wake    chan struct{}
	done    chan struct{}
	once    sync.Once

	input *inputState
}

func (ss *Session) close() {
	ss.once.Do(func() {
		close(ss.done)
		ss.input.releaseAll()
	})
}

func (ss *Session) write(typ byte, parts ...[]byte) error {
	ss.wmu.Lock()
	defer ss.wmu.Unlock()
	total := 0
	for _, p := range parts {
		total += len(p)
	}
	var hdr [5]byte
	hdr[0] = typ
	binary.BigEndian.PutUint32(hdr[1:], uint32(total))
	ss.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	if _, err := ss.bw.Write(hdr[:]); err != nil {
		return err
	}
	for _, p := range parts {
		if _, err := ss.bw.Write(p); err != nil {
			return err
		}
	}
	return ss.bw.Flush()
}

func (ss *Session) sendJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return ss.write(msgJSON, data)
}

func (ss *Session) settings() streamSettings {
	ss.smu.Lock()
	defer ss.smu.Unlock()
	return ss.st
}

func (ss *Session) poke() {
	select {
	case ss.wake <- struct{}{}:
	default:
	}
}

func monitorsJSON(mons []Monitor) []map[string]any {
	out := make([]map[string]any, 0, len(mons))
	for _, m := range mons {
		out = append(out, map[string]any{"w": m.W, "h": m.H, "p": m.Primary})
	}
	return out
}

func pickMonitor(mons []Monitor, idx int) Monitor {
	if len(mons) == 0 {
		return Monitor{W: 1920, H: 1080, Primary: true}
	}
	if idx < 0 || idx >= len(mons) {
		idx = 0
	}
	return mons[idx]
}

// streamLoop captures the screen and sends JPEG frames, at most maxInFlight
// un-acknowledged frames at a time so latency never piles up on slow Wi-Fi.
func (ss *Session) streamLoop() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer ss.close()

	capt, err := newCapturer()
	if err != nil {
		logf("Помилка захоплення екрана: %v", err)
		ss.sendJSON(map[string]any{"t": "error", "msg": "Не вдалося захопити екран: " + err.Error()})
		ss.conn.Close()
		return
	}
	defer capt.close()

	var (
		buf         bytes.Buffer
		lastCapture time.Time
		lastChange  = time.Now()
		lastMonList time.Time
		mons        []Monitor
		lastMonSig  string
		failCount   int
	)
	ss.force.Store(true)

	for {
		// wait for a free slot
		select {
		case <-ss.done:
			return
		case <-ss.credits:
		case <-time.After(5 * time.Second):
			// lost ack — continue anyway
		}

		for {
			st := ss.settings()
			fps := st.fps
			if fps <= 0 {
				fps = 30
			}
			interval := time.Second / time.Duration(fps)
			forced := ss.force.Load()
			if !forced && time.Since(lastChange) > 1500*time.Millisecond && interval < 100*time.Millisecond {
				interval = 100 * time.Millisecond // idle: poll slower
			}
			if wait := time.Until(lastCapture.Add(interval)); wait > 0 {
				t := time.NewTimer(wait)
				select {
				case <-ss.done:
					t.Stop()
					return
				case <-ss.wake:
					lastChange = time.Now()
				case <-t.C:
				}
				t.Stop()
				// woken early by input: still respect the fps cap
				if wait := time.Until(lastCapture.Add(time.Second / time.Duration(fps))); wait > 0 {
					time.Sleep(wait)
				}
			}
			select {
			case <-ss.done:
				return
			default:
			}

			if time.Since(lastMonList) > time.Second || mons == nil {
				mons = listMonitors()
				lastMonList = time.Now()
				sig := fmt.Sprint(monitorsJSON(mons))
				if lastMonSig != "" && sig != lastMonSig {
					ss.sendJSON(map[string]any{"t": "mons", "mons": monitorsJSON(mons), "mon": st.mon})
					ss.force.Store(true)
				}
				lastMonSig = sig
			}
			m := pickMonitor(mons, st.mon)
			dw, dh := m.W, m.H
			if st.maxW > 0 && dw > st.maxW {
				dw = st.maxW
				dh = int(math.Round(float64(m.H) * float64(st.maxW) / float64(m.W)))
			}
			if dw < 16 || dh < 16 {
				dw, dh = 16, 16
			}

			lastCapture = time.Now()
			changed, cx, cy, err := capt.capture(m, dw, dh)
			if err != nil {
				failCount++
				if failCount == 1 || failCount%50 == 0 {
					logf("Захоплення екрана: %v", err)
				}
				time.Sleep(300 * time.Millisecond)
				continue
			}
			failCount = 0
			forced = ss.force.Load()
			if !changed && !forced {
				continue
			}
			lastChange = time.Now()
			ss.force.Store(false)

			buf.Reset()
			var hdr [8]byte
			binary.BigEndian.PutUint32(hdr[0:], math.Float32bits(cx))
			binary.BigEndian.PutUint32(hdr[4:], math.Float32bits(cy))
			q := st.quality
			if q < 10 || q > 95 {
				q = 60
			}
			if err := jpeg.Encode(&buf, capt.image(), &jpeg.Options{Quality: q}); err != nil {
				logf("JPEG: %v", err)
				continue
			}
			if err := ss.write(msgFrame, hdr[:], buf.Bytes()); err != nil {
				ss.conn.Close()
				return
			}
			break
		}
	}
}

type ctrlMsg struct {
	T    string  `json:"t"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	DX   int     `json:"dx"`
	DY   int     `json:"dy"`
	B    int     `json:"b"`
	D    bool    `json:"d"`
	N    int     `json:"n"`
	VK   int     `json:"vk"`
	VKs  []int   `json:"vks"`
	S    string  `json:"s"`
	Q    int     `json:"q"`
	MaxW *int    `json:"maxw"`
	FPS  int     `json:"fps"`
	Mon  *int    `json:"mon"`
}

func (ss *Session) handleJSON(data []byte) {
	var m ctrlMsg
	if json.Unmarshal(data, &m) != nil {
		return
	}
	switch m.T {
	case "ping":
		ss.sendJSON(map[string]any{"t": "pong"})
		return
	case "cfg":
		ss.smu.Lock()
		if m.Q > 0 {
			ss.st.quality = m.Q
		}
		if m.MaxW != nil {
			ss.st.maxW = *m.MaxW
		}
		if m.FPS > 0 {
			ss.st.fps = m.FPS
		}
		if m.Mon != nil {
			ss.st.mon = *m.Mon
		}
		ss.smu.Unlock()
		ss.force.Store(true)
		ss.poke()
		return
	case "mm": // absolute move, normalized to the streamed monitor
		mon := pickMonitor(listMonitorsCached(), ss.settings().mon)
		x := mon.X + int(math.Round(clamp01(m.X)*float64(mon.W-1)))
		y := mon.Y + int(math.Round(clamp01(m.Y)*float64(mon.H-1)))
		mouseMoveAbs(x, y)
	case "mr":
		mouseMoveRel(m.DX, m.DY)
	case "mb":
		ss.input.button(m.B, m.D)
	case "click":
		n := m.N
		if n < 1 || n > 3 {
			n = 1
		}
		for i := 0; i < n; i++ {
			ss.input.button(m.B, true)
			ss.input.button(m.B, false)
		}
	case "wh":
		mouseWheel(m.DY, m.DX)
	case "key":
		ss.input.key(m.VK, m.D)
	case "tap":
		ss.input.key(m.VK, true)
		ss.input.key(m.VK, false)
	case "combo":
		for _, vk := range m.VKs {
			ss.input.key(vk, true)
		}
		for i := len(m.VKs) - 1; i >= 0; i-- {
			ss.input.key(m.VKs[i], false)
		}
	case "text":
		ss.input.text(m.S)
	default:
		return
	}
	ss.poke()
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

var (
	monCacheMu sync.Mutex
	monCache   []Monitor
	monCacheAt time.Time
)

func listMonitorsCached() []Monitor {
	monCacheMu.Lock()
	defer monCacheMu.Unlock()
	if monCache == nil || time.Since(monCacheAt) > time.Second {
		monCache = listMonitors()
		monCacheAt = time.Now()
	}
	return monCache
}
