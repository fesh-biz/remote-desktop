package main

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"
)

const discoveryQuery = "RDESK?"

// runDiscovery answers UDP broadcasts from the app so the PC shows up in its list.
func runDiscovery(name string, port int) {
	for {
		pc, err := net.ListenPacket("udp4", fmt.Sprintf(":%d", discoveryPort))
		if err != nil {
			logf("Автопошук недоступний (UDP %d): %v", discoveryPort, err)
			time.Sleep(10 * time.Second)
			continue
		}
		resp, _ := json.Marshal(map[string]any{"t": "rdesk", "name": name, "port": port, "ver": protocolVersion})
		buf := make([]byte, 512)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				break
			}
			if strings.HasPrefix(string(buf[:n]), discoveryQuery) {
				pc.WriteTo(resp, addr)
			}
		}
		pc.Close()
		time.Sleep(time.Second)
	}
}
