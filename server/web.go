package main

import _ "embed"

// The browser client for controlling the PC from a laptop: open
// http://<PC address>:47800 in any browser on the same network.
//
//go:embed web/index.html
var webIndex []byte

//go:embed winres/icon.ico
var webIcon []byte
