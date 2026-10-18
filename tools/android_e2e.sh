#!/usr/bin/env bash
# Smoke test of the Android app in an emulator against the stub server (server/platform_other.go).
set -u
OUT=test-android
mkdir -p "$OUT"
UI="python3 tools/adb_ui.py"
shot() { adb exec-out screencap -p > "$OUT/$1.png"; }
fail=0
step() { echo "== $*"; }
note() { local m="${*//'%'/'%25'}"; m="${m//$'\n'/'%0A'}"; echo "::warning title=android-e2e::$m"; }
t() { "$@" || { fail=1; note "failed: $*"; }; }

adb logcat -c
step install
t adb install -r dist/RemoteDesktop.apk
adb shell am start -n biz.fesh.remotedesktop/.MainActivity
sleep 8
shot 01-main

step manual connect
$UI id editHost && adb shell input text 10.0.2.2 && sleep 1
t $UI id btnConnect
sleep 3
shot 02-pin-dialog
adb shell input text 123456
adb shell input keyevent KEYCODE_ENTER
sleep 6
shot 03-connected-help
t $UI text "Зрозуміло"
sleep 3
shot 04-remote

step gestures
read W H < <(adb shell wm size | grep -o '[0-9]*x[0-9]*' | tail -1 | tr x ' ')
X1=$((W*3/10)); X2=$((W*7/10)); Y1=$((H*3/10)); Y2=$((H*4/10)); CX=$((W/2)); CY=$((H/3))
note "screen ${W}x${H}"
adb shell input swipe $X1 $Y1 $X2 $Y2 400   # touchpad move
sleep 1
adb shell input tap $CX $CY                 # left click (after double-tap window)
sleep 1
adb shell input tap $CX $CY; adb shell input tap $CX $CY   # double click
sleep 1
adb shell input swipe $CX $CY $CX $CY 900   # long press -> right click
sleep 1

step keyboard
t $UI text "⌨"
sleep 3
adb shell input text hello
adb shell input keyevent KEYCODE_DEL
adb shell input keyevent KEYCODE_ENTER
sleep 2
shot 05-keyboard
adb shell input keyevent KEYCODE_BACK       # hide IME
sleep 2

step toolbar
t $UI text "Esc"
t $UI text "Ctrl"
sleep 1
shot 06-ctrl-sticky
t $UI text "Tab"
t $UI text "⋯"
sleep 2
shot 07-extra-row
t $UI text "F5"
t $UI text "🖱 Тачпад"
sleep 2
adb shell input tap $CX $CY                 # direct mode tap
sleep 1
adb shell input swipe $X1 $Y1 $X2 $Y2 500   # direct mode drag
sleep 1
t $UI text "🖥 1/2"                  # stub has two monitors (scrolled? try landscape later)
sleep 3
shot 08-monitor2

step settings
t $UI text "⚙"
sleep 2
shot 09-settings
t $UI text "Зберегти"
sleep 2

step rotate
adb shell settings put system accelerometer_rotation 0
adb shell settings put system user_rotation 1
sleep 4
shot 10-landscape
adb shell settings put system user_rotation 0
sleep 3

step disconnect
adb shell input keyevent KEYCODE_BACK
sleep 2
shot 11-back-dialog
t $UI text "Відключитися"
sleep 4
shot 12-main-after

adb logcat -d > "$OUT/logcat.txt"
if grep -A 30 "FATAL EXCEPTION" "$OUT/logcat.txt" > "$OUT/crash.txt"; then fail=1; note "CRASH: $(head -c 3000 "$OUT/crash.txt")"; fi
note "files: $(ls "$OUT" | tr '\n' ' ')"
echo "fail=$fail"
exit $fail
