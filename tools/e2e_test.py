"""End-to-end test of RemoteDesktop.exe on a real Windows desktop (used in CI).

Usage: python e2e_test.py path\\to\\RemoteDesktop.exe out_dir
"""
import ctypes
import json
import os
import socket
import struct
import subprocess
import sys
import time
import traceback

sys.stdout.reconfigure(encoding="utf-8", errors="replace")
EXE, OUT = os.path.abspath(sys.argv[1]), os.path.abspath(sys.argv[2])
os.makedirs(OUT, exist_ok=True)
PORT, PIN = 47900, "246810"
results = []
sys.excepthook = lambda *a: (open(os.path.join(OUT, "traceback.txt"), "w").write("".join(traceback.format_exception(*a))), traceback.print_exception(*a))


def check(name, ok, detail=""):
    results.append((name, bool(ok), detail))
    print(("PASS " if ok else "FAIL ") + name + (" — " + str(detail) if detail else ""), flush=True)


def send(s, t, payload=b""):
    s.sendall(bytes([ord(t)]) + struct.pack(">I", len(payload)) + payload)


def sendj(s, o):
    send(s, "J", json.dumps(o).encode())


def recv_exact(s, n):
    d = b""
    while len(d) < n:
        c = s.recv(n - len(d))
        if not c:
            raise EOFError
        d += c
    return d


def recv(s):
    h = recv_exact(s, 5)
    n = struct.unpack(">I", h[1:])[0]
    return chr(h[0]), recv_exact(s, n)


class POINT(ctypes.Structure):
    _fields_ = [("x", ctypes.c_long), ("y", ctypes.c_long)]


user32 = ctypes.windll.user32
user32.SetProcessDpiAwarenessContext(ctypes.c_void_p(-4))


def cursor():
    p = POINT()
    user32.GetCursorPos(ctypes.byref(p))
    return p.x, p.y


log = open(os.path.join(OUT, "server-output.txt"), "w", encoding="utf-8")
proc = subprocess.Popen([EXE, "-pin", PIN, "-port", str(PORT), "-name", "CI-Runner"], stdout=log, stderr=subprocess.STDOUT)
time.sleep(2)
check("server starts", proc.poll() is None)

try:
    u = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    u.settimeout(2)
    u.sendto(b"RDESK?", ("127.0.0.1", 47801))
    data, _ = u.recvfrom(512)
    d = json.loads(data)
    check("discovery reply", d.get("name") == "CI-Runner" and d.get("port") == PORT, d)
except Exception as e:
    check("discovery reply", False, e)

s = socket.create_connection(("127.0.0.1", PORT), timeout=10)
sendj(s, {"t": "hello", "pin": "000000", "client": "ci"})
t, d = recv(s)
check("wrong PIN rejected", t == "J" and not json.loads(d)["ok"], d.decode("utf-8"))
s.close()

s = socket.create_connection(("127.0.0.1", PORT), timeout=10)
sendj(s, {"t": "hello", "pin": PIN, "client": "ci"})
t, d = recv(s)
auth = json.loads(d)
check("auth ok", auth.get("ok"), auth)
mons = auth.get("mons", [])
check("monitors listed", len(mons) >= 1, mons)

for label, maxw in (("1280", 1280), ("native", 0)):
    sendj(s, {"t": "cfg", "q": 60, "maxw": maxw, "fps": 30, "mon": 0})
    frames, sizes, first = 0, [], None
    t0 = time.time()
    # move the mouse continuously so frames keep changing
    while time.time() - t0 < 3:
        sendj(s, {"t": "mr", "dx": 3 if frames % 2 == 0 else -3, "dy": 0})
        t, d = recv(s)
        if t == "F":
            frames += 1
            sizes.append(len(d))
            if first is None:
                first = d
            send(s, "A")
    fps = frames / (time.time() - t0)
    check(f"frames {label}", frames >= 5 and first[8:10] == b"\xff\xd8", f"{frames} frames, {fps:.1f} fps, avg {sum(sizes)//len(sizes)//1024} KB")
    with open(os.path.join(OUT, f"frame-{label}.jpg"), "wb") as f:
        f.write(first[8:])

# mouse: absolute
m = mons[0]
sendj(s, {"t": "mm", "x": 0.5, "y": 0.5})
time.sleep(0.3)
x, y = cursor()
check("absolute move to center", abs(x - (m["w"] - 1) / 2) <= 2 and abs(y - (m["h"] - 1) / 2) <= 2, (x, y, m))
sendj(s, {"t": "mr", "dx": 25, "dy": -15})
time.sleep(0.3)
x2, y2 = cursor()
check("relative move", (x2 - x, y2 - y) == (25, -15), (x2 - x, y2 - y))
sendj(s, {"t": "wh", "dy": 120})
sendj(s, {"t": "tap", "vk": 0x10})
sendj(s, {"t": "key", "vk": 0x11, "d": True})
sendj(s, {"t": "ping"})
pong = False
t0 = time.time()
while time.time() - t0 < 3:
    t, d = recv(s)
    if t == "F":
        send(s, "A")
    elif json.loads(d).get("t") == "pong":
        pong = True
        break
check("ping/pong", pong)
s.close()
time.sleep(0.5)
ctrl_down = user32.GetAsyncKeyState(0x11) & 0x8000
check("keys released on disconnect", not ctrl_down)
check("server alive after disconnect", proc.poll() is None)

# ---- protocol 2: pointer sent separately at up to ~60 Hz
s = socket.create_connection(("127.0.0.1", PORT), timeout=10)
sendj(s, {"t": "hello", "pin": PIN, "client": "ci-v2", "ver": 2})
t, d = recv(s)
auth = json.loads(d)
check("v2 auth", auth.get("ok") and auth.get("ver", 0) >= 2 and len(auth.get("vs", [])) == 4, auth)
sendj(s, {"t": "cfg", "q": 50, "maxw": 960, "fps": 30, "mon": 0})
sendj(s, {"t": "mm", "x": 0.3, "y": 0.5, "i": 1})
shapes, curs, frames, seq = [], [], 0, 1
s.settimeout(0.005)
t0 = last = time.time()
buf = b""
while time.time() - t0 < 2.0:
    if time.time() - last >= 0.008:
        seq += 1
        sendj(s, {"t": "mr", "dx": 1 if (seq // 100) % 2 == 0 else -1, "dy": 0, "i": seq})
        last = time.time()
    try:
        chunk = s.recv(1 << 16)
        if not chunk:
            break
        buf += chunk
    except socket.timeout:
        pass
    while len(buf) >= 5:
        n = struct.unpack(">I", buf[1:5])[0]
        if len(buf) < 5 + n:
            break
        typ, payload, buf = chr(buf[0]), buf[5:5 + n], buf[5 + n:]
        if typ == "S":
            shapes.append(payload)
        elif typ == "C":
            curs.append(struct.unpack(">iiiiIB", payload))
        elif typ == "F":
            frames += 1
            send(s, "A")
s.settimeout(10)
rate = len(curs) / 2.0
check("v2 pointer image (PNG)", len(shapes) >= 1 and shapes[0][4:8] == b"\x89PNG", [len(x) for x in shapes])
check("v2 pointer updates >= 40/s", rate >= 40, f"{rate:.1f}/s, {frames} frames")
if shapes:
    with open(os.path.join(OUT, "cursor.png"), "wb") as f:
        f.write(shapes[0][4:])
time.sleep(0.3)
# drain and look at the newest report
s.settimeout(0.5)
try:
    while True:
        t, d = recv(s)
        if t == "C":
            curs.append(struct.unpack(">iiiiIB", d))
        elif t == "F":
            send(s, "A")
except Exception:
    pass
x, y = cursor()
lastc = curs[-1] if curs else None
check("v2 report acknowledges last move", lastc is not None and lastc[4] == seq, (lastc, seq))
check("v2 report matches real pointer", lastc is not None and abs(lastc[0] - (x - mons[0].get("x", 0))) <= 1 and abs(lastc[1] - (y - mons[0].get("y", 0))) <= 1, (lastc, (x, y)))
s.close()

# ---- browser client: page and WebSocket on the same port
import base64
try:
    h = socket.create_connection(("127.0.0.1", PORT), timeout=10)
    h.sendall(b"GET / HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n")
    page = b""
    while True:
        c = h.recv(65536)
        if not c:
            break
        page += c
    h.close()
    check("web page served", page.startswith(b"HTTP/1.1 200") and b"<canvas" in page, page[:80])
except Exception as e:
    check("web page served", False, e)

try:
    w = socket.create_connection(("127.0.0.1", PORT), timeout=10)
    key = base64.b64encode(os.urandom(16)).decode()
    w.sendall(("GET /ws HTTP/1.1\r\nHost: 127.0.0.1:%d\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"
               "Sec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\nOrigin: http://127.0.0.1:%d\r\n\r\n" % (PORT, key, PORT)).encode())
    resp = b""
    while b"\r\n\r\n" not in resp:
        resp += w.recv(1)
    ok101 = resp.startswith(b"HTTP/1.1 101")

    def ws_send(payload):
        mask = os.urandom(4)
        hdr = bytes([0x82])
        n = len(payload)
        hdr += bytes([0x80 | n]) if n < 126 else bytes([0x80 | 126]) + struct.pack(">H", n)
        w.sendall(hdr + mask + bytes(b ^ mask[i % 4] for i, b in enumerate(payload)))

    def ws_recv():
        h2 = recv_exact(w, 2)
        n = h2[1] & 0x7F
        if n == 126:
            n = struct.unpack(">H", recv_exact(w, 2))[0]
        elif n == 127:
            n = struct.unpack(">Q", recv_exact(w, 8))[0]
        return recv_exact(w, n)

    ws_send(b"J" + json.dumps({"t": "hello", "pin": PIN, "client": "ci-browser", "ver": 2}).encode())
    m = ws_recv()
    a = json.loads(m[1:]) if m[:1] == b"J" else {}
    got = set()
    t0 = time.time()
    while time.time() - t0 < 5 and not {"F", "C", "S"} <= got:
        m = ws_recv()
        got.add(chr(m[0]))
        if m[:1] == b"F":
            ws_send(b"A")
    check("websocket session", ok101 and a.get("ok") and {"F", "C", "S"} <= got, (resp[:40], a.get("ok"), sorted(got)))
    ws_send(b"J" + json.dumps({"t": "mm", "x": 0.25, "y": 0.25, "i": 7}).encode())
    time.sleep(0.3)
    x, y = cursor()
    check("websocket mouse move", abs(x - (mons[0]["w"] - 1) * 0.25) <= 2 and abs(y - (mons[0]["h"] - 1) * 0.25) <= 2, (x, y))
    w.close()
except Exception as e:
    check("websocket session", False, repr(e))

# ---- autostart entry and log file
try:
    import winreg
    with winreg.OpenKey(winreg.HKEY_CURRENT_USER, r"Software\Microsoft\Windows\CurrentVersion\Run") as k:
        val, _ = winreg.QueryValueEx(k, "Remote Desktop (fesh-biz)")
    check("autostart registered", EXE.lower() in val.lower() and "-startup" in val, val)
except Exception as e:
    check("autostart registered", False, e)
logp = os.path.join(os.path.dirname(EXE), "remote-desktop.log")
check("log file written", os.path.exists(logp) and "ci-v2" in open(logp, encoding="utf-8", errors="replace").read(), logp)
check("server alive at the end", proc.poll() is None)

proc.terminate()
log.close()
failed = [r for r in results if not r[1]]
with open(os.path.join(OUT, "summary.txt"), "w", encoding="utf-8") as f:
    for n, ok, det in results:
        f.write(("PASS " if ok else "FAIL ") + n + " — " + str(det) + "\n")
sys.exit(1 if failed else 0)
