//go:build windows

// Package privacy reads what Windows tells apps about where the computer
// is (location, Wi-Fi, Bluetooth access and its history, time zone, region)
// and changes the parts Kordo can: the location switch and the time zone.
package privacy

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const consentRoot = `SOFTWARE\Microsoft\Windows\CurrentVersion\CapabilityAccessManager\ConsentStore`

// Capabilities that reveal where the computer is.
var Capabilities = []Capability{
	{"location", "Местоположение", "Координаты от GPS, Wi-Fi и IP через службу определения местоположения Windows."},
	{"wifiData", "Данные Wi-Fi", "Сети вокруг и их адреса (BSSID). По ним место определяется с точностью до дома."},
	{"bluetoothSync", "Bluetooth-устройства", "Устройства и маячки рядом."},
	{"radios", "Радиомодули", "Управление Wi-Fi и Bluetooth."},
}

type Capability struct {
	ID, Title, About string
}

// AppAccess is one app's entry in Windows' consent store.
type AppAccess struct {
	Capability string    `json:"capability"`
	App        string    `json:"app"`      // exe path for desktop apps, package family name for Store apps
	Desktop    bool      `json:"desktop"`  // classic (non-packaged) app
	Allowed    bool      `json:"allowed"`  // "Value" is Allow (desktop apps have none: inherit)
	LastUsed   time.Time `json:"lastUsed"` // zero if never
	InUse      bool      `json:"inUse"`    // started and not stopped
}

// Switch reads the "Value" (Allow/Deny) of a capability; root is HKLM (the
// device switch) or HKCU (the user's switch). "" if not set.
func Switch(root registry.Key, capability string) string {
	k, err := registry.OpenKey(root, consentRoot+`\`+capability, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, _ := k.GetStringValue("Value")
	return v
}

// SetDeviceSwitch writes the device-wide "Value" (needs admin). "" removes it.
func SetDeviceSwitch(capability, value string) error {
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, consentRoot+`\`+capability, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if value == "" {
		err := k.DeleteValue("Value")
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	return k.SetStringValue("Value", value)
}

// UserAccess lists the apps in the current user's consent store for a
// capability (HKCU: run in the user's process, not the service).
func UserAccess(capability string) []AppAccess {
	base := consentRoot + `\` + capability
	var out []AppAccess
	read := func(path, name string, desktop bool) {
		k, err := registry.OpenKey(registry.CURRENT_USER, path+`\`+name, registry.QUERY_VALUE)
		if err != nil {
			return
		}
		defer k.Close()
		a := AppAccess{Capability: capability, App: name, Desktop: desktop}
		if desktop {
			a.App = strings.ReplaceAll(name, "#", `\`)
		}
		v, _, _ := k.GetStringValue("Value")
		a.Allowed = v == "" || strings.EqualFold(v, "Allow")
		start, _, _ := k.GetIntegerValue("LastUsedTimeStart")
		stop, _, _ := k.GetIntegerValue("LastUsedTimeStop")
		if t := filetime(start); !t.IsZero() {
			a.LastUsed = t
		}
		if t := filetime(stop); t.After(a.LastUsed) {
			a.LastUsed = t
		}
		a.InUse = start != 0 && stop < start
		out = append(out, a)
	}
	for _, sub := range subkeys(base) {
		if sub == "NonPackaged" {
			for _, d := range subkeys(base + `\NonPackaged`) {
				read(base+`\NonPackaged`, d, true)
			}
			continue
		}
		read(base, sub, false)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastUsed.After(out[j].LastUsed) })
	return out
}

func subkeys(path string) []string {
	k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil
	}
	defer k.Close()
	names, _ := k.ReadSubKeyNames(-1)
	return names
}

func filetime(v uint64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	ft := windows.Filetime{LowDateTime: uint32(v), HighDateTime: uint32(v >> 32)}
	return time.Unix(0, ft.Nanoseconds())
}

// Region returns the user's "Country or region" (ISO code, e.g. RU) and
// its name in Russian if available.
func Region() string {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Control Panel\International\Geo`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, _ := k.GetStringValue("Name")
	return strings.ToUpper(v)
}

// TimeZone returns the Windows time zone id, e.g. "Russian Standard Time".
func TimeZone() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\TimeZoneInformation`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, _ := k.GetStringValue("TimeZoneKeyName")
	return v
}

var (
	icu                   = windows.NewLazySystemDLL("icu.dll")
	procWindowsTimeZoneID = icu.NewProc("ucal_getWindowsTimeZoneID")
)

// WindowsZone maps an IANA zone (Europe/Amsterdam) to a Windows one
// (W. Europe Standard Time) with the ICU built into Windows 10 1903+.
func WindowsZone(iana string) (string, error) {
	if err := procWindowsTimeZoneID.Find(); err != nil {
		return "", errors.New("ICU is not available (needs Windows 10 1903 or later)")
	}
	in, err := windows.UTF16FromString(iana)
	if err != nil {
		return "", err
	}
	out := make([]uint16, 128)
	var status int32
	n, _, _ := procWindowsTimeZoneID.Call(
		uintptr(unsafe.Pointer(&in[0])), uintptr(len(in)-1),
		uintptr(unsafe.Pointer(&out[0])), uintptr(len(out)), uintptr(unsafe.Pointer(&status)),
	)
	if status > 0 || int32(n) <= 0 {
		return "", fmt.Errorf("no Windows time zone for %s", iana)
	}
	return windows.UTF16ToString(out[:n]), nil
}

// SetTimeZone switches the system time zone (needs admin).
func SetTimeZone(windowsZone string) error {
	tzutil := filepath.Join(os.Getenv("SystemRoot"), "System32", "tzutil.exe")
	cmd := exec.Command(tzutil, "/s", windowsZone)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tzutil: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Browsers lists installed browsers: they determine location on their own
// (from Wi-Fi around), whatever the IP says.
func Browsers() []string {
	pf := os.Getenv("ProgramFiles")
	pf86 := os.Getenv("ProgramFiles(x86)")
	local := os.Getenv("LOCALAPPDATA")
	known := []struct{ name, path string }{
		{"Google Chrome", filepath.Join(pf, `Google\Chrome\Application\chrome.exe`)},
		{"Google Chrome", filepath.Join(local, `Google\Chrome\Application\chrome.exe`)},
		{"Microsoft Edge", filepath.Join(pf86, `Microsoft\Edge\Application\msedge.exe`)},
		{"Mozilla Firefox", filepath.Join(pf, `Mozilla Firefox\firefox.exe`)},
		{"Яндекс Браузер", filepath.Join(local, `Yandex\YandexBrowser\Application\browser.exe`)},
		{"Opera", filepath.Join(local, `Programs\Opera\opera.exe`)},
		{"Brave", filepath.Join(pf, `BraveSoftware\Brave-Browser\Application\brave.exe`)},
		{"Vivaldi", filepath.Join(local, `Vivaldi\Application\vivaldi.exe`)},
	}
	seen := map[string]bool{}
	var out []string
	for _, b := range known {
		if seen[b.name] {
			continue
		}
		if _, err := os.Stat(b.path); err == nil {
			seen[b.name] = true
			out = append(out, b.name)
		}
	}
	return out
}
