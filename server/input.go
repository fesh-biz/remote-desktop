package main

import (
	"sync"
	"unicode"
)

// Virtual-key codes used by the server-side logic.
const (
	vkShift    = 0x10
	vkControl  = 0x11
	vkMenu     = 0x12 // Alt
	vkLWin     = 0x5B
	vkRWin     = 0x5C
	vkLShift   = 0xA0
	vkRShift   = 0xA1
	vkLControl = 0xA2
	vkRControl = 0xA3
	vkLMenu    = 0xA4
	vkRMenu    = 0xA5
)

// inputState remembers what a client is holding so everything can be
// released when the phone disconnects mid-drag or with Ctrl pressed.
type inputState struct {
	mu      sync.Mutex
	keys    map[int]bool
	buttons map[int]bool
}

func newInputState() *inputState {
	return &inputState{keys: map[int]bool{}, buttons: map[int]bool{}}
}

func (st *inputState) key(vk int, down bool) {
	if vk <= 0 || vk > 0xFE {
		return
	}
	st.mu.Lock()
	if down {
		st.keys[vk] = true
	} else {
		delete(st.keys, vk)
	}
	st.mu.Unlock()
	keyEvent(vk, down)
}

func (st *inputState) button(b int, down bool) {
	if b < 0 || b > 2 {
		return
	}
	st.mu.Lock()
	if down {
		st.buttons[b] = true
	} else {
		delete(st.buttons, b)
	}
	st.mu.Unlock()
	mouseButton(b, down)
}

func (st *inputState) releaseAll() {
	st.mu.Lock()
	keys := make([]int, 0, len(st.keys))
	for k := range st.keys {
		keys = append(keys, k)
	}
	buttons := make([]int, 0, len(st.buttons))
	for b := range st.buttons {
		buttons = append(buttons, b)
	}
	st.keys = map[int]bool{}
	st.buttons = map[int]bool{}
	st.mu.Unlock()
	for _, k := range keys {
		keyEvent(k, false)
	}
	for _, b := range buttons {
		mouseButton(b, false)
	}
}

func (st *inputState) held(vks ...int) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, v := range vks {
		if st.keys[v] {
			return true
		}
	}
	return false
}

// text types a string. With Ctrl/Alt/Win held the characters are sent as
// real key presses so shortcuts like Ctrl+C work (also from a Cyrillic layout).
func (st *inputState) text(s string) {
	if s == "" {
		return
	}
	if !st.held(vkControl, vkLControl, vkRControl, vkMenu, vkLMenu, vkRMenu, vkLWin, vkRWin) {
		typeUnicode(s)
		return
	}
	shiftHeld := st.held(vkShift, vkLShift, vkRShift)
	for _, r := range s {
		r = latinForKey(r)
		vk, shift, ok := vkForChar(r)
		if !ok {
			continue
		}
		if shift && !shiftHeld {
			keyEvent(vkShift, true)
		}
		keyEvent(vk, true)
		keyEvent(vk, false)
		if shift && !shiftHeld {
			keyEvent(vkShift, false)
		}
	}
}

var cyrToLatin = func() map[rune]rune {
	cyr := []rune("йцукенгшщзхїфівапролджєячсмитьбюъыэё")
	lat := []rune("qwertyuiop[]asdfghjkl;'zxcvbnm,.]s'`")
	m := make(map[rune]rune, len(cyr))
	for i, c := range cyr {
		m[c] = lat[i]
	}
	m['ґ'] = '\\'
	return m
}()

// latinForKey maps a letter from the Ukrainian/Russian layout to the Latin
// letter on the same physical key (Ctrl+С → Ctrl+C).
func latinForKey(r rune) rune {
	lr := unicode.ToLower(r)
	if l, ok := cyrToLatin[lr]; ok {
		return l
	}
	return lr
}
