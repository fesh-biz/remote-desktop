// Remote Desktop server: streams the PC screen to the Android app (and to a
// browser on a laptop) over the local network and injects mouse / keyboard
// input received from them. On Windows it lives in the notification area.
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
	"sync"
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
	// Autostart: start with Windows (missing = first run, switched on).
	Autostart *bool `json:"autostart,omitempty"`
}

// App is what the tray menu works with.
type App struct {
	srv     *Server
	cfgMu   sync.Mutex
	cfg     Config
	cfgPath string
	logPath string
	name    string
	fw      firewallState
	startup bool // launched by Windows at sign-in
}

func main() {
	platformInit()

	portFlag := flag.Int("port", 0, "TCP порт (за замовчуванням 47800)")
	pinFlag := flag.String("pin", "", "встановити свій PIN-код")
	newPin := flag.Bool("newpin", false, "згенерувати новий PIN-код")
	nameFlag := flag.String("name", "", "ім'я комп'ютера, яке бачить телефон")
	startup := flag.Bool("startup", false, "запуск разом з Windows (без підказки)")
	noAuto := flag.Bool("noautostart", false, "не запускати разом з Windows")
	flag.Parse()

	cfgPath := configPath()
	logPath := openLog(filepath.Dir(cfgPath))
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
	if *noAuto {
		cfg.Autostart = boolPtr(false)
		changed = true
	} else if cfg.Autostart == nil {
		cfg.Autostart = boolPtr(true)
		changed = true
	}
	if changed {
		saveConfig(cfgPath, cfg)
	}

	if !singleInstance() {
		showMessage("Remote Desktop", "Remote Desktop уже працює.\n\nЗначок програми — у треї біля годинника (можливо, під стрілкою «^»).")
		return
	}

	name := cfg.Name
	if name == "" {
		name, _ = os.Hostname()
		if name == "" {
			name = "PC"
		}
	}

	setConsoleTitle("Remote Desktop — сервер")
	if err := setAutostart(*cfg.Autostart); err != nil {
		logf("Автозапуск: %v", err)
	}

	ln, err := net.Listen("tcp4", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		logf("Не вдалося відкрити порт %d: %v", cfg.Port, err)
		showError(fmt.Sprintf("Не вдалося відкрити порт %d.\n%v\n\nМожливо, порт зайнятий іншою програмою.", cfg.Port, err))
		os.Exit(1)
	}

	go runDiscovery(name, cfg.Port)
	fw := ensureFirewall()

	printBanner(name, cfg, cfgPath, fw)

	app := &App{cfg: cfg, cfgPath: cfgPath, logPath: logPath, name: name, fw: fw, startup: *startup}
	app.srv = newServer(cfg, name)
	go app.srv.serve(ln)
	runUI(app) // tray icon on Windows; returns only on exit
	app.srv.shutdown()
}

func boolPtr(b bool) *bool { return &b }

func (a *App) config() Config {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	return a.cfg
}

func (a *App) autostart() bool {
	c := a.config()
	return c.Autostart != nil && *c.Autostart
}

func (a *App) setAutostartPref(on bool) error {
	if err := setAutostart(on); err != nil {
		return err
	}
	a.cfgMu.Lock()
	a.cfg.Autostart = boolPtr(on)
	c := a.cfg
	a.cfgMu.Unlock()
	saveConfig(a.cfgPath, c)
	return nil
}

func (a *App) newPIN() string {
	p := randomPIN()
	a.cfgMu.Lock()
	a.cfg.PIN = p
	c := a.cfg
	a.cfgMu.Unlock()
	saveConfig(a.cfgPath, c)
	a.srv.setPIN(p)
	logf("Згенеровано новий PIN-код")
	return p
}

// lanIPs: just the addresses, most likely first.
func lanIPs() []string {
	var out []string
	for _, l := range localIPv4() {
		out = append(out, strings.Fields(l)[0])
	}
	return out
}

// infoText is shown from the tray: everything needed to connect.
func (a *App) infoText() string {
	c := a.config()
	var b strings.Builder
	fmt.Fprintf(&b, "Комп'ютер:  %s\n", a.name)
	ips := lanIPs()
	if len(ips) == 0 {
		b.WriteString("Адреса:  мережу не знайдено\n")
	} else {
		fmt.Fprintf(&b, "Адреса:  %s\n", strings.Join(ips, ", "))
	}
	fmt.Fprintf(&b, "PIN-код:  %s\n\n", c.PIN)
	b.WriteString("Телефон: відкрийте застосунок Remote Desktop у тій самій Wi-Fi мережі — комп'ютер з'явиться у списку.\n\n")
	b.WriteString("Ноутбук: відкрийте в браузері\n")
	if len(ips) == 0 {
		fmt.Fprintf(&b, "    http://<адреса цього ПК>:%d\n", c.Port)
	}
	for _, ip := range ips {
		fmt.Fprintf(&b, "    http://%s:%d\n", ip, c.Port)
	}
	if cl := a.srv.clients(); len(cl) > 0 {
		b.WriteString("\nПідключено зараз:\n")
		for _, x := range cl {
			b.WriteString("    " + x + "\n")
		}
	}
	if a.fw == firewallNotElevated {
		b.WriteString("\nЯкщо пристрій не бачить ПК — дозвольте доступ у вікні брандмауера або запустіть програму один раз «від імені адміністратора».")
	}
	b.WriteString("\n(Ctrl+C копіює цей текст)")
	return b.String()
}

func printBanner(name string, cfg Config, cfgPath string, fw firewallState) {
	line := strings.Repeat("=", 58)
	outln(line)
	outf("  Remote Desktop — сервер  (версія %s)\n", version)
	outln(line)
	outf("  Комп'ютер : %s\n", name)
	ips := localIPv4()
	if len(ips) == 0 {
		outln("  Адреса    : (мережу не знайдено)")
	}
	for i, ip := range ips {
		label := "  Адреса    :"
		if i > 0 {
			label = "             "
		}
		outf("%s %s\n", label, ip)
	}
	outf("  Порт      : %d\n", cfg.Port)
	outf("  PIN-код   : %s\n", cfg.PIN)
	outln(strings.Repeat("-", 58))
	outln("  Телефон: застосунок Remote Desktop у тій самій Wi-Fi мережі.")
	if len(ips) > 0 {
		outf("  Ноутбук: браузер → http://%s:%d\n", strings.Fields(ips[0])[0], cfg.Port)
	}
	switch fw {
	case firewallAdded:
		outln("  Брандмауер: правило для вхідних підключень додано.")
	case firewallNotElevated:
		outln("  Якщо пристрій не бачить ПК — дозвольте доступ у вікні брандмауера")
		outln("  або запустіть програму один раз «від імені адміністратора».")
	}
	outf("  Налаштування: %s\n", cfgPath)
	outln(line)
}

// ---------------------------------------------------------------- log

var (
	logMu   sync.Mutex
	logFile *os.File
)

// openLog keeps a small log next to the settings: without a console window
// it is the only place to see what happened.
func openLog(dir string) string {
	p := filepath.Join(dir, "remote-desktop.log")
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if fi, err := os.Stat(p); err == nil && fi.Size() > 512<<10 {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(p, flags, 0o644)
	if err != nil {
		return ""
	}
	logFile = f
	return p
}

func outf(format string, a ...any) {
	s := fmt.Sprintf(format, a...)
	logMu.Lock()
	defer logMu.Unlock()
	os.Stdout.WriteString(s)
	if logFile != nil {
		logFile.WriteString(s)
	}
}

func outln(s string) { outf("%s\n", s) }

func logf(format string, a ...any) {
	outf("[%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, a...))
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
