// Remote Desktop server: streams the PC screen to the Android app over the
// local network and injects mouse / keyboard input received from it.
package main

import (
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var version = "dev"

const (
	defaultPort   = 47800
	discoveryPort = 47801
	configName    = "remote-desktop.json"
)

type Config struct {
	Port int    `json:"port"`
	PIN  string `json:"pin"`
	Name string `json:"name,omitempty"`
}

func main() {
	platformInit()

	portFlag := flag.Int("port", 0, "TCP порт (за замовчуванням 47800)")
	pinFlag := flag.String("pin", "", "встановити свій PIN-код")
	newPin := flag.Bool("newpin", false, "згенерувати новий PIN-код")
	nameFlag := flag.String("name", "", "ім'я комп'ютера, яке бачить телефон")
	flag.Parse()

	cfgPath := configPath()
	cfg := loadConfig(cfgPath)
	changed := false
	if *portFlag > 0 {
		cfg.Port = *portFlag
		changed = true
	}
	if cfg.Port <= 0 || cfg.Port > 65535 {
		cfg.Port = defaultPort
		changed = true
	}
	if *pinFlag != "" {
		cfg.PIN = strings.TrimSpace(*pinFlag)
		changed = true
	}
	if cfg.PIN == "" || *newPin {
		cfg.PIN = randomPIN()
		changed = true
	}
	if *nameFlag != "" {
		cfg.Name = *nameFlag
		changed = true
	}
	if changed {
		saveConfig(cfgPath, cfg)
	}

	name := cfg.Name
	if name == "" {
		name, _ = os.Hostname()
		if name == "" {
			name = "PC"
		}
	}

	setConsoleTitle("Remote Desktop — сервер")

	ln, err := net.Listen("tcp4", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		fmt.Printf("\n  Не вдалося відкрити порт %d: %v\n", cfg.Port, err)
		fmt.Println("  Можливо, сервер уже запущено в іншому вікні.")
		waitEnter()
		os.Exit(1)
	}

	go runDiscovery(name, cfg.Port)
	fw := ensureFirewall()

	printBanner(name, cfg, cfgPath, fw)

	srv := newServer(cfg, name)
	srv.serve(ln)
}

func printBanner(name string, cfg Config, cfgPath string, fw firewallState) {
	line := strings.Repeat("=", 58)
	fmt.Println(line)
	fmt.Printf("  Remote Desktop — сервер  (версія %s)\n", version)
	fmt.Println(line)
	fmt.Printf("  Комп'ютер : %s\n", name)
	ips := localIPv4()
	if len(ips) == 0 {
		fmt.Println("  Адреса    : (мережу не знайдено)")
	}
	for i, ip := range ips {
		label := "  Адреса    :"
		if i > 0 {
			label = "             "
		}
		fmt.Printf("%s %s\n", label, ip)
	}
	fmt.Printf("  Порт      : %d\n", cfg.Port)
	fmt.Printf("  PIN-код   : %s\n", cfg.PIN)
	fmt.Println(strings.Repeat("-", 58))
	fmt.Println("  1. Підключіть телефон до тієї ж Wi-Fi мережі.")
	fmt.Println("  2. Відкрийте застосунок Remote Desktop — ПК з'явиться у списку.")
	fmt.Println("  3. Введіть PIN-код. Закрийте це вікно, щоб зупинити сервер.")
	switch fw {
	case firewallAdded:
		fmt.Println("  Брандмауер: правило для вхідних підключень додано.")
	case firewallNotElevated:
		fmt.Println("  Якщо телефон не бачить ПК — дозвольте доступ у вікні брандмауера")
		fmt.Println("  або запустіть програму один раз «від імені адміністратора».")
	}
	fmt.Printf("  Налаштування: %s\n", cfgPath)
	fmt.Println(line)
}

func logf(format string, a ...any) {
	fmt.Printf("[%s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, a...))
}

func waitEnter() {
	fmt.Println("\n  Натисніть Enter, щоб закрити...")
	var s string
	fmt.Scanln(&s)
}

func configPath() string {
	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exe)
		p := filepath.Join(dir, configName)
		// portable mode: keep settings next to the exe when that folder is writable
		if f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0o644); err == nil {
			f.Close()
			return p
		}
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return configName
	}
	dir := filepath.Join(base, "RemoteDesktop")
	os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, configName)
}

func loadConfig(p string) Config {
	var c Config
	data, err := os.ReadFile(p)
	if err == nil && len(data) > 0 {
		json.Unmarshal(data, &c)
	}
	return c
}

func saveConfig(p string, c Config) {
	data, _ := json.MarshalIndent(c, "", "  ")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		logf("Не вдалося зберегти налаштування: %v", err)
	}
}

func randomPIN() string {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return fmt.Sprintf("%06d", time.Now().UnixNano()%1000000)
	}
	return fmt.Sprintf("%06d", n.Int64())
}

type ifaceIP struct {
	ip    string
	iface string
	score int
}

// localIPv4 lists the LAN addresses a phone could use, most likely first.
func localIPv4() []string {
	var res []ifaceIP
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipn.IP.To4()
			if ip4 == nil || ip4[0] == 169 && ip4[1] == 254 {
				continue
			}
			score := 0
			lname := strings.ToLower(ifc.Name)
			for _, v := range []string{"vethernet", "virtual", "vmware", "vbox", "wsl", "hyper-v", "docker", "loopback", "bluetooth"} {
				if strings.Contains(lname, v) {
					score -= 10
				}
			}
			if ip4[0] == 192 && ip4[1] == 168 {
				score += 5
			} else if ip4[0] == 10 {
				score += 3
			}
			res = append(res, ifaceIP{ip: ip4.String(), iface: ifc.Name, score: score})
		}
	}
	sort.SliceStable(res, func(i, j int) bool { return res[i].score > res[j].score })
	out := make([]string, 0, len(res))
	for _, r := range res {
		out = append(out, fmt.Sprintf("%-15s (%s)", r.ip, r.iface))
	}
	return out
}
