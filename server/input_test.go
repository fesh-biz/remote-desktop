package main

import "testing"

func TestCyrMap(t *testing.T) {
	if latinForKey('С') != 'c' || latinForKey('в') != 'd' || latinForKey('x') != 'x' {
		t.Fatal("bad map")
	}
}
