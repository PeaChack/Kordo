// Package mobile is Kordo's core for Android, bound with gomobile together
// with sing-box's libbox (see android/). It keeps servers, app rules and
// the profile in a JSON file in the app's private storage and turns them
// into a sing-box config with the same generator as the Windows service.
//
// The API sticks to gomobile-friendly types: strings (JSON for structured
// data), bools, ints and errors.
package mobile

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/PeaChack/Kordo/core/configgen"
	"github.com/PeaChack/Kordo/core/model"
	"github.com/PeaChack/Kordo/core/providers"
	_ "github.com/PeaChack/Kordo/core/providers/all"
)

type state struct {
	Profile model.Profile  `json:"profile"`
	Servers []model.Server `json:"servers"`
}

var (
	mu      sync.Mutex
	dataDir string
	st      state
)

// Init loads the state from dir (the app's files directory). Call it once
// before anything else; calling it again reloads.
func Init(dir string) error {
	mu.Lock()
	defer mu.Unlock()
	if err := os.MkdirAll(filepath.Join(dir, "rules"), 0o700); err != nil {
		return err
	}
	dataDir = dir
	st = state{Profile: model.DefaultProfile()}
	b, err := os.ReadFile(statePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return err
	}
	if st.Profile.Rules == nil {
		st.Profile.Rules = []model.AppRule{}
	}
	return nil
}

func statePath() string { return filepath.Join(dataDir, "state.json") }

func saveLocked() error {
	if dataDir == "" {
		return errors.New("kordo: Init was not called")
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(statePath(), b)
}

func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func newID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// ---------------------------------------------------------------- servers

// ServerInfo is what the server list shows (no secrets).
type serverInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Enabled  bool   `json:"enabled"`
	Address  string `json:"address"`
}

// Servers returns the servers as a JSON array of {id, name, provider, enabled, address}.
func Servers() string {
	mu.Lock()
	defer mu.Unlock()
	out := []serverInfo{}
	for _, s := range st.Servers {
		info := serverInfo{ID: s.ID, Name: s.Name, Provider: s.ProviderID, Enabled: s.Enabled}
		if p, err := providers.Get(s.ProviderID); err == nil {
			if ips, domains, err := p.ServerAddresses(s.Params); err == nil {
				switch {
				case len(domains) > 0:
					info.Address = domains[0]
				case len(ips) > 0:
					info.Address = ips[0].Addr().String()
				}
			}
		}
		out = append(out, info)
	}
	return toJSON(out)
}

// AddServer adds a server from a link (vless://, ss://, wireguard://) or the
// text of a WireGuard .conf file. name may be empty. Returns the new id.
func AddServer(text, name string) (string, error) {
	pid, params, parsedName, err := providers.ParseAny(strings.TrimSpace(text))
	if err != nil {
		return "", err
	}
	srv := model.Server{ID: newID(), ProviderID: pid, Params: params, Name: parsedName, Enabled: true}
	if strings.TrimSpace(name) != "" {
		srv.Name = strings.TrimSpace(name)
	}
	if srv.Name == "" {
		srv.Name = pid
	}
	mu.Lock()
	defer mu.Unlock()
	st.Servers = append(st.Servers, srv)
	return srv.ID, saveLocked()
}

// RemoveServer deletes a server and the app rules that pointed to it.
func RemoveServer(id string) error {
	mu.Lock()
	defer mu.Unlock()
	var kept []model.Server
	for _, s := range st.Servers {
		if s.ID != id {
			kept = append(kept, s)
		}
	}
	st.Servers = kept
	var rules []model.AppRule
	for _, r := range st.Profile.Rules {
		if !(r.Target.IsServer() && r.Target.ServerID == id) {
			rules = append(rules, r)
		}
	}
	if rules == nil {
		rules = []model.AppRule{}
	}
	st.Profile.Rules = rules
	if st.Profile.DefaultTarget.IsServer() && st.Profile.DefaultTarget.ServerID == id {
		st.Profile.DefaultTarget = model.Direct()
	}
	return saveLocked()
}

// RenameServer changes a server's display name.
func RenameServer(id, name string) error {
	mu.Lock()
	defer mu.Unlock()
	for i := range st.Servers {
		if st.Servers[i].ID == id {
			st.Servers[i].Name = strings.TrimSpace(name)
			return saveLocked()
		}
	}
	return errors.New("server not found")
}

// ---------------------------------------------------------------- apps

type appRoute struct {
	Package string `json:"package"`
	Target  string `json:"target"` // "direct", "block" or a server id
}

// Apps returns the app rules as a JSON array of {package, target}.
func Apps() string {
	mu.Lock()
	defer mu.Unlock()
	out := []appRoute{}
	for _, r := range st.Profile.Rules {
		if r.Match.Kind == model.MatchAndroidPackage {
			out = append(out, appRoute{r.Match.Value, targetString(r.Target)})
		}
	}
	return toJSON(out)
}

func targetString(t model.Target) string {
	if t.IsServer() {
		return t.ServerID
	}
	return t.Kind
}

func parseTarget(s string) (model.Target, error) {
	switch s {
	case model.TargetDirect:
		return model.Direct(), nil
	case model.TargetBlock:
		return model.Target{Kind: model.TargetBlock}, nil
	}
	for _, srv := range st.Servers {
		if srv.ID == s {
			return model.ToServer(s), nil
		}
	}
	return model.Target{}, errors.New("unknown server: " + s)
}

// SetAppRoute routes an app (by package name) to target: "direct", "block"
// or a server id. An empty target removes the rule.
func SetAppRoute(pkg, target string) error {
	pkg = strings.TrimSpace(pkg)
	if pkg == "" {
		return errors.New("empty package name")
	}
	mu.Lock()
	defer mu.Unlock()
	idx := -1
	for i, r := range st.Profile.Rules {
		if r.Match.Kind == model.MatchAndroidPackage && r.Match.Value == pkg {
			idx = i
		}
	}
	if target == "" {
		if idx >= 0 {
			st.Profile.Rules = append(st.Profile.Rules[:idx], st.Profile.Rules[idx+1:]...)
		}
		return saveLocked()
	}
	t, err := parseTarget(target)
	if err != nil {
		return err
	}
	if idx >= 0 {
		st.Profile.Rules[idx].Target = t
	} else {
		st.Profile.Rules = append(st.Profile.Rules, model.AppRule{
			ID: newID(), Match: model.Match{Kind: model.MatchAndroidPackage, Value: pkg}, Target: t, Children: model.ChildInherit,
		})
	}
	return saveLocked()
}

// ---------------------------------------------------------------- settings

type settings struct {
	// Mode "only" (only the listed apps use a server) or "all" (everything
	// through DefaultServer except the listed apps).
	Mode          string `json:"mode"`
	DefaultServer string `json:"defaultServer,omitempty"`
	DNS           string `json:"dns"` // via_server | doh_direct | system
	DoH           string `json:"doh,omitempty"`
	AllowLAN      bool   `json:"allowLan"`
	IPv6          string `json:"ipv6"` // auto | block
	MTU           int    `json:"mtu"`
	Stack         string `json:"stack"`
}

// Settings returns the user settings as JSON.
func Settings() string {
	mu.Lock()
	defer mu.Unlock()
	p := st.Profile
	s := settings{Mode: "only", DNS: p.DNS.Mode, DoH: p.DNS.DoH, AllowLAN: p.AllowLAN, IPv6: p.IPv6, MTU: p.EffectiveMTU(), Stack: p.Stack}
	if p.DefaultTarget.IsServer() {
		s.Mode, s.DefaultServer = "all", p.DefaultTarget.ServerID
	}
	if s.DNS == "" {
		s.DNS = "via_server"
	}
	if s.Stack == "" {
		s.Stack = "gvisor"
	}
	return toJSON(s)
}

// SetSettings stores settings given as JSON (the shape Settings returns).
func SetSettings(js string) error {
	var s settings
	if err := json.Unmarshal([]byte(js), &s); err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	p := st.Profile
	switch s.Mode {
	case "all":
		t, err := parseTarget(s.DefaultServer)
		if err != nil || !t.IsServer() {
			return errors.New("choose a server for \"everything through VPN\"")
		}
		p.DefaultTarget = t
	default:
		p.DefaultTarget = model.Direct()
	}
	p.DNS.Mode, p.DNS.DoH = s.DNS, strings.TrimSpace(s.DoH)
	p.AllowLAN, p.IPv6, p.MTU, p.Stack = s.AllowLAN, s.IPv6, s.MTU, s.Stack
	if p.MTU == model.DefaultMTU {
		p.MTU = 0
	}
	if err := p.Validate(st.Servers); err != nil {
		return err
	}
	st.Profile = p
	return saveLocked()
}

// ---------------------------------------------------------------- config

// BuildConfig writes the app rule-sets and returns the sing-box config for
// libbox. hasIPv6 tells whether the current network has global IPv6.
func BuildConfig(hasIPv6 bool) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	if dataDir == "" {
		return "", errors.New("kordo: Init was not called")
	}
	var enabled int
	for _, s := range st.Servers {
		if s.Enabled {
			enabled++
		}
	}
	if enabled == 0 {
		return "", errors.New("add a server first")
	}
	out, err := configgen.Generate(st.Profile, st.Servers, configgen.Env{
		RulesDir: filepath.Join(dataDir, "rules"),
		HasIPv6:  hasIPv6,
		LogLevel: "warn",
	})
	if err != nil {
		return "", err
	}
	for name, data := range out.RuleSets {
		if err := writeAtomic(filepath.Join(dataDir, "rules", name), data); err != nil {
			return "", err
		}
	}
	return string(out.Config), nil
}
