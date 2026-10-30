// Package model holds the platform-independent data model of Kordo:
// servers, per-application rules and the profile that ties them together.
package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Server is a user-owned VPN/proxy server.
type Server struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	ProviderID string          `json:"provider"` // "vless" | "shadowsocks" | "wireguard"
	Params     json.RawMessage `json:"params"`   // validated by the provider's schema
	Enabled    bool            `json:"enabled"`
	HealthURL  string          `json:"healthUrl,omitempty"`
}

const DefaultHealthURL = "http://cp.cloudflare.com/generate_204"

func (s Server) EffectiveHealthURL() string {
	if s.HealthURL != "" {
		return s.HealthURL
	}
	return DefaultHealthURL
}

// Target says where traffic of a matched app goes.
type Target struct {
	Kind     string `json:"kind"` // "server" | "direct" | "block"
	ServerID string `json:"serverId,omitempty"`
}

const (
	TargetServer = "server"
	TargetDirect = "direct"
	TargetBlock  = "block"
)

func Direct() Target                 { return Target{Kind: TargetDirect} }
func ToServer(id string) Target      { return Target{Kind: TargetServer, ServerID: id} }
func (t Target) IsServer() bool      { return t.Kind == TargetServer }
func (t Target) Equal(o Target) bool { return t.Kind == o.Kind && t.ServerID == o.ServerID }

func (t Target) Validate(servers map[string]Server) error {
	switch t.Kind {
	case TargetDirect, TargetBlock:
		return nil
	case TargetServer:
		if _, ok := servers[t.ServerID]; !ok {
			return fmt.Errorf("unknown server %q", t.ServerID)
		}
		return nil
	}
	return fmt.Errorf("unknown target kind %q", t.Kind)
}

// Match identifies an application.
type Match struct {
	Kind  string `json:"kind"`  // "path" | "name" | "store_package" | "android_package"
	Value string `json:"value"` // normalized, see Normalize
}

const (
	MatchPath           = "path"
	MatchName           = "name"
	MatchStorePackage   = "store_package"
	MatchAndroidPackage = "android_package"
)

// Normalize returns a cleaned form of the match. Case is preserved (it is shown
// in the UI); comparisons go through Key, which is case-insensitive because
// Windows paths are.
func (m Match) Normalize() Match {
	v := strings.TrimSpace(strings.Trim(m.Value, `"`))
	switch m.Kind {
	case MatchPath:
		v = strings.ReplaceAll(v, "/", `\`)
		for strings.Contains(v, `\\`) {
			v = strings.ReplaceAll(v, `\\`, `\`)
		}
	}
	return Match{Kind: m.Kind, Value: v}
}

func (m Match) Key() string {
	n := m.Normalize()
	if n.Kind == MatchAndroidPackage {
		return n.Kind + ":" + n.Value
	}
	return n.Kind + ":" + strings.ToLower(n.Value)
}

func (m Match) Validate() error {
	if strings.TrimSpace(m.Value) == "" {
		return errors.New("empty match value")
	}
	switch m.Kind {
	case MatchPath:
		if !strings.Contains(m.Value, `\`) && !strings.Contains(m.Value, "/") {
			return fmt.Errorf("path %q is not absolute", m.Value)
		}
	case MatchName, MatchAndroidPackage:
	case MatchStorePackage:
		if !storePkgRe.MatchString(m.Value) {
			return fmt.Errorf("invalid store package name %q", m.Value)
		}
	default:
		return fmt.Errorf("unknown match kind %q", m.Kind)
	}
	if IsForbidden(m) {
		return fmt.Errorf("%q is a system process and cannot be routed per-app", m.Value)
	}
	return nil
}

var storePkgRe = regexp.MustCompile(`^[A-Za-z0-9.\-]+$`)

var windowsAppsRe = regexp.MustCompile(`(?i)\\WindowsApps\\([A-Za-z0-9.\-]+)_[^\\]*\\`)

// MatchForPath builds the best match for an executable path. Microsoft Store
// apps live under a versioned folder that changes on every update, so they are
// matched by package name instead of by exact path.
func MatchForPath(path string) Match {
	m := Match{Kind: MatchPath, Value: path}.Normalize()
	if sm := windowsAppsRe.FindStringSubmatch(m.Value); sm != nil {
		return Match{Kind: MatchStorePackage, Value: sm[1]}
	}
	return m
}

// ExeName returns the executable file name for path/name matches.
func (m Match) ExeName() string {
	switch m.Kind {
	case MatchPath:
		return strings.ToLower(filepath.Base(strings.ReplaceAll(m.Value, `\`, "/")))
	case MatchName:
		return strings.ToLower(m.Value)
	}
	return ""
}

// forbidden processes cannot be selected: their traffic belongs to the whole system.
var forbiddenNames = map[string]bool{
	"svchost.exe": true, "system": true, "system idle process": true, "lsass.exe": true,
	"services.exe": true, "wininit.exe": true, "csrss.exe": true, "smss.exe": true,
	"kordo-svc.exe": true, "kordo.exe": true, "kordoctl.exe": true,
}

func IsForbidden(m Match) bool {
	n := m.ExeName()
	return n != "" && forbiddenNames[n]
}

// SharedExes are executables used by many unrelated programs. Learning them by
// path would pull every instance in the system into a tunnel, so children with
// these images are never auto-inherited (they fall back to "ask").
var SharedExes = map[string]bool{
	"cmd.exe": true, "powershell.exe": true, "pwsh.exe": true, "conhost.exe": true,
	"node.exe": true, "python.exe": true, "pythonw.exe": true, "java.exe": true,
	"javaw.exe": true, "msedgewebview2.exe": true, "rundll32.exe": true, "dllhost.exe": true,
	"openconsole.exe": true, "windowsterminal.exe": true, "wscript.exe": true, "cscript.exe": true,
	// developer tools started by IDEs, agents and terminals alike
	"git.exe": true, "git-remote-https.exe": true, "git-remote-http.exe": true,
	"bash.exe": true, "sh.exe": true, "ssh.exe": true, "curl.exe": true,
	"wsl.exe": true, "wslhost.exe": true, "npm.exe": true, "npx.exe": true,
}

// PruneSharedLearned drops auto-learned children (Source "inherit") whose
// executable is shared. Such entries could exist from before an exe was added
// to SharedExes; routing them by path would capture every instance in the
// system. Children the user decided on explicitly are kept. It reports how
// many entries were removed.
func PruneSharedLearned(p *Profile) int {
	removed := 0
	for i := range p.Rules {
		kept := p.Rules[i].Learned[:0]
		for _, l := range p.Rules[i].Learned {
			if l.Source == "inherit" && SharedExes[l.Match.ExeName()] {
				removed++
				continue
			}
			kept = append(kept, l)
		}
		p.Rules[i].Learned = kept
	}
	return removed
}

// ChildPolicy decides what happens with processes started by a matched app.
type ChildPolicy string

const (
	ChildInherit ChildPolicy = "inherit"
	ChildDirect  ChildPolicy = "direct"
	ChildAsk     ChildPolicy = "ask"
)

// AppRule routes one application.
type AppRule struct {
	ID       string      `json:"id"`
	Name     string      `json:"name,omitempty"` // display name
	Match    Match       `json:"match"`
	Target   Target      `json:"target"`
	Children ChildPolicy `json:"children"`
	Priority int         `json:"priority"`
	Learned  []Learned   `json:"learned,omitempty"` // auto-added children
}

// Learned is a child executable discovered by the process tracker.
type Learned struct {
	Match  Match  `json:"match"`
	Target Target `json:"target"`           // usually the parent's target; "direct" when the user chose so
	Source string `json:"source"`           // "inherit" | "user"
	RuleID string `json:"ruleId,omitempty"` // for ChildAnswers: the app whose child this is
}

func (r AppRule) Validate(servers map[string]Server) error {
	if r.ID == "" {
		return errors.New("rule has no id")
	}
	if err := r.Match.Validate(); err != nil {
		return err
	}
	if err := r.Target.Validate(servers); err != nil {
		return err
	}
	switch r.Children {
	case ChildInherit, ChildDirect, ChildAsk, "":
	default:
		return fmt.Errorf("unknown children policy %q", r.Children)
	}
	return nil
}

type KillMode string

const (
	KillOff     KillMode = "off"
	KillVPNApps KillMode = "vpn_apps"
	KillAll     KillMode = "all"
)

type DNSMode struct {
	Mode     string `json:"mode"`               // "via_server" | "doh_direct" | "system"
	ServerID string `json:"serverId,omitempty"` // for via_server; empty = first enabled server
	DoH      string `json:"doh,omitempty"`      // DoH server IP, default 1.1.1.1
}

// Profile is everything the user configured, except the servers themselves.
type Profile struct {
	DefaultTarget  Target    `json:"defaultTarget"`
	KillSwitch     KillMode  `json:"killSwitch"`
	DNS            DNSMode   `json:"dns"`
	AllowLAN       bool      `json:"allowLan"`
	IPv6           string    `json:"ipv6"` // "auto" | "block"
	StrictRoute    bool      `json:"strictRoute"`
	ConnectOnStart bool      `json:"connectOnStart"`
	Stack          string    `json:"stack,omitempty"`      // tun stack: "mixed" (default) | "system" | "gvisor"
	VerboseLog     bool      `json:"verboseLog,omitempty"` // engine logs every connection (slow with many connections)
	Rules          []AppRule `json:"rules"`
	ChildAnswers   []Learned `json:"childAnswers,omitempty"` // "always so" answers for shared children
}

func DefaultProfile() Profile {
	return Profile{
		DefaultTarget: Direct(),
		KillSwitch:    KillOff,
		DNS:           DNSMode{Mode: "via_server"},
		AllowLAN:      true,
		IPv6:          "auto",
		StrictRoute:   true,
		Rules:         []AppRule{},
	}
}

func (p Profile) Validate(servers []Server) error {
	byID := map[string]Server{}
	for _, s := range servers {
		byID[s.ID] = s
	}
	if err := p.DefaultTarget.Validate(byID); err != nil {
		return fmt.Errorf("default target: %w", err)
	}
	switch p.KillSwitch {
	case KillOff, KillVPNApps, KillAll, "":
	default:
		return fmt.Errorf("unknown kill switch mode %q", p.KillSwitch)
	}
	seen := map[string]string{}
	for _, r := range p.Rules {
		if err := r.Validate(byID); err != nil {
			return fmt.Errorf("rule %s: %w", r.ID, err)
		}
		k := r.Match.Key()
		if other, ok := seen[k]; ok {
			return fmt.Errorf("rules %s and %s match the same app %s", other, r.ID, r.Match.Value)
		}
		seen[k] = r.ID
	}
	return nil
}
