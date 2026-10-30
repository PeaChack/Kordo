// Package providers defines the protocol plug-in interface. Each protocol
// (VLESS, Shadowsocks, WireGuard, ...) lives in its own sub-package and
// registers itself with Register.
package providers

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"sync"
)

// OutboundSpec is a sing-box outbound or endpoint object.
type OutboundSpec struct {
	Kind string         // "outbound" | "endpoint"
	JSON map[string]any // complete sing-box object, including "type" and "tag"
}

type Caps struct {
	UDP                 bool `json:"udp"`
	NeedsHandshakeCheck bool `json:"needsHandshakeCheck"` // WireGuard: stateful, re-handshake after network change
}

// Provider converts user-facing server parameters into sing-box configuration.
type Provider interface {
	ID() string
	DisplayName() string
	// URISchemes lists the URI schemes (without "://") this provider can import.
	URISchemes() []string
	// ConfigSchema is a JSON Schema used by the GUI to render the manual form.
	ConfigSchema() []byte
	ParseURI(uri string) (params json.RawMessage, name string, err error)
	Validate(params json.RawMessage) error
	ToOutbound(tag string, params json.RawMessage) (OutboundSpec, error)
	// ServerAddresses returns literal IPs and domain names of the remote server,
	// used to exclude them from the TUN and to allow them in the kill switch.
	ServerAddresses(params json.RawMessage) (ips []netip.Prefix, domains []string, err error)
	Capabilities() Caps
}

var (
	mu       sync.RWMutex
	registry = map[string]Provider{}
)

func Register(p Provider) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := registry[p.ID()]; dup {
		panic("providers: duplicate provider " + p.ID())
	}
	registry[p.ID()] = p
}

func Get(id string) (Provider, error) {
	mu.RLock()
	defer mu.RUnlock()
	p, ok := registry[id]
	if !ok {
		return nil, fmt.Errorf("unknown provider %q", id)
	}
	return p, nil
}

func All() []Provider {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Provider, 0, len(registry))
	for _, p := range registry {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}

// ParseAny detects the provider by URI scheme (or WireGuard .conf content).
func ParseAny(text string) (providerID string, params json.RawMessage, name string, err error) {
	text = strings.TrimSpace(strings.TrimPrefix(text, string(rune(0xFEFF))))
	scheme := ""
	if i := strings.Index(text, "://"); i > 0 {
		scheme = strings.ToLower(text[:i])
	}
	for _, p := range All() {
		for _, s := range p.URISchemes() {
			if s == scheme || (scheme == "" && s == "wgconf" && strings.Contains(text, "[Interface]")) {
				params, name, err = p.ParseURI(text)
				return p.ID(), params, name, err
			}
		}
	}
	return "", nil, "", fmt.Errorf("unrecognized server link (supported: vless://, ss://, WireGuard .conf)")
}

// HostAddresses classifies a host as literal IP or domain.
func HostAddresses(hosts ...string) (ips []netip.Prefix, domains []string) {
	for _, h := range hosts {
		h = strings.Trim(h, "[]")
		if h == "" {
			continue
		}
		if a, err := netip.ParseAddr(h); err == nil {
			ips = append(ips, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
		} else {
			domains = append(domains, strings.ToLower(h))
		}
	}
	return
}

// Decode unmarshals params strictly into T.
func Decode[T any](params json.RawMessage) (T, error) {
	var v T
	dec := json.NewDecoder(strings.NewReader(string(params)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return v, fmt.Errorf("invalid parameters: %w", err)
	}
	return v, nil
}

func Encode(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func ValidPort(p int) error {
	if p <= 0 || p > 65535 {
		return fmt.Errorf("invalid port %d", p)
	}
	return nil
}
