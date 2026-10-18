"""Tiny helper to drive the app through adb: tap elements by resource id or text."""
import re
import subprocess
import sys
import time
import xml.etree.ElementTree as ET


def adb(*args):
    return subprocess.run(["adb", *args], capture_output=True, text=True, encoding="utf-8", errors="replace").stdout


def dump():
    adb("shell", "uiautomator", "dump", "/sdcard/ui.xml")
    xml = adb("shell", "cat", "/sdcard/ui.xml")
    return ET.fromstring(xml[xml.index("<?xml"):] if "<?xml" in xml else xml)


def find(pred, tries=8):
    for _ in range(tries):
        try:
            for n in dump().iter("node"):
                if pred(n):
                    x1, y1, x2, y2 = map(int, re.findall(r"\d+", n.get("bounds")))
                    return (x1 + x2) // 2, (y1 + y2) // 2
        except Exception as e:
            print("dump failed:", e)
        time.sleep(1)
    return None


def main():
    cmd, arg = sys.argv[1], sys.argv[2]
    if cmd == "id":
        p = find(lambda n: n.get("resource-id", "").endswith(":id/" + arg))
    elif cmd == "text":
        p = find(lambda n: n.get("text", "").casefold() == arg.casefold())
    elif cmd == "textcontains":
        p = find(lambda n: arg.casefold() in n.get("text", "").casefold())
    else:
        raise SystemExit("unknown command")
    if p is None:
        print(f"NOT FOUND {cmd} {arg}")
        sys.exit(1)
    if len(sys.argv) > 3 and sys.argv[3] == "find":
        print(f"FOUND {cmd} {arg} at {p}")
        return
    adb("shell", "input", "tap", str(p[0]), str(p[1]))
    print(f"tapped {cmd} {arg} at {p}")


if __name__ == "__main__":
    main()
