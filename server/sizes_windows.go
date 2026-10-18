//go:build windows && amd64

package main

import "unsafe"

// Compile-time checks that the Win32 structs match the x64 C layout.
var (
	_ [40 - unsafe.Sizeof(mouseINPUT{})]byte
	_ [unsafe.Sizeof(mouseINPUT{}) - 40]byte
	_ [40 - unsafe.Sizeof(keybdINPUT{})]byte
	_ [unsafe.Sizeof(keybdINPUT{}) - 40]byte
	_ [24 - unsafe.Sizeof(cursorInfo{})]byte
	_ [unsafe.Sizeof(cursorInfo{}) - 24]byte
	_ [32 - unsafe.Sizeof(iconInfo{})]byte
	_ [unsafe.Sizeof(iconInfo{}) - 32]byte
	_ [104 - unsafe.Sizeof(monitorInfoEx{})]byte
	_ [unsafe.Sizeof(monitorInfoEx{}) - 104]byte
	_ [40 - unsafe.Sizeof(bitmapInfoHeader{})]byte
)

var (
	_ [32 - unsafe.Sizeof(bitmapObj{})]byte
	_ [unsafe.Sizeof(bitmapObj{}) - 32]byte
	_ [80 - unsafe.Sizeof(wndClassEx{})]byte
	_ [unsafe.Sizeof(wndClassEx{}) - 80]byte
	_ [48 - unsafe.Sizeof(winMsg{})]byte
	_ [unsafe.Sizeof(winMsg{}) - 48]byte
	_ [976 - unsafe.Sizeof(notifyIconData{})]byte
	_ [unsafe.Sizeof(notifyIconData{}) - 976]byte
)
