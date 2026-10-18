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

proc.terminate()
log.close()
failed = [r for r in results if not r[1]]
with open(os.path.join(OUT, "summary.txt"), "w", encoding="utf-8") as f:
    for n, ok, det in results:
        f.write(("PASS " if ok else "FAIL ") + n + " — " + str(det) + "\n")
sys.exit(1 if failed else 0)
