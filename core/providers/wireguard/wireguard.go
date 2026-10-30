// Package wireguard implements the WireGuard provider (.conf import and wireguard:// links).
// In sing-box >= 1.11 WireGuard is an endpoint, not an outbound.
package wireguard

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/portico-vpn/portico/core/providers"
)

type Params struct {
	PrivateKey   string   `json:"privateKey"`
	Address      []string `json:"address"` // interface addresses, CIDR
	MTU          int      `json:"mtu,omitempty"`
	PeerEndpoint string   `json:"endpoint"` // host:port
	PublicKey    string   `json:"publicKey"`
	PresharedKey string   `json:"presharedKey,omitempty"`
	AllowedIPs   []string `json:"allowedIps,omitempty"`
	Keepalive    int      `json:"keepalive,omitempty"`
	Reserved     []int    `json:"reserved,omitempty"`
}

type provider struct{}

func init() { providers.Register(provider{}) }

func (provider) ID() string           { return "wireguard" }
func (provider) DisplayName() string  { return "WireGuard" }
func (provider) URISchemes() []string { return []string{"wgconf", "wireguard", "wg"} }
func (provider) Capabilities() providers.Caps {
	return providers.Caps{UDP: true, NeedsHandshakeCheck: true}
}

func (pr provider) ParseURI(text string) (json.RawMessage, string, error) {
	text = strings.TrimSpace(text)
	var p Params
	var name string
	var err error
	if strings.Contains(text, "[Interface]") {
		p, err = parseConf(text)
	} else {
		p, name, err = parseLink(text)
	}
	if err != nil {
		return nil, "", err
	}
	if name == "" {
		host, _, _ := net.SplitHostPort(p.PeerEndpoint)
		name = "WireGuard " + host
	}
	raw := providers.Encode(p)
	if err := pr.Validate(raw); err != nil {
		return nil, "", err
	}
	return raw, name, nil
}

func splitList(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func parseConf(text string) (Params, error) {
	var p Params
	section := ""
	peers := 0
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if i := strings.IndexAny(line, "#;"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = strings.ToLower(strings.Trim(line, "[]"))
			if section == "peer" {
				peers++
			}
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return p, fmt.Errorf("wireguard conf: bad line %q", line)
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		switch section + "." + k {
		case "interface.privatekey":
			p.PrivateKey = v
		case "interface.address":
			p.Address = append(p.Address, splitList(v)...)
		case "interface.mtu":
			p.MTU, _ = strconv.Atoi(v)
		case "interface.dns", "interface.listenport", "interface.table", "interface.saveconfig",
			"interface.preup", "interface.postup", "interface.predown", "interface.postdown":
			// DNS is handled globally by Portico; scripts are never executed.
		case "interface.jc", "interface.jmin", "interface.jmax", "interface.s1", "interface.s2",
			"interface.h1", "interface.h2", "interface.h3", "interface.h4":
			return p, errors.New("AmneziaWG configs are not supported by the engine")
		case "peer.publickey":
			p.PublicKey = v
		case "peer.presharedkey":
			p.PresharedKey = v
		case "peer.endpoint":
			p.PeerEndpoint = v
		case "peer.allowedips":
			p.AllowedIPs = append(p.AllowedIPs, splitList(v)...)
		case "peer.persistentkeepalive":
			p.Keepalive, _ = strconv.Atoi(v)
		}
	}
	if peers > 1 {
		return p, errors.New("wireguard conf: only one [Peer] is supported")
	}
	return p, nil
}

// wireguard://<private key>@host:port?publickey=..&address=..&presharedkey=..&mtu=..&reserved=1,2,3#name
func parseLink(text string) (Params, string, error) {
	u, err := url.Parse(text)
	if err != nil {
		return Params{}, "", fmt.Errorf("parse wireguard link: %w", err)
	}
	q := u.Query()
	pk, _ := url.PathUnescape(u.User.Username())
	p := Params{
		PrivateKey:   pk,
		PeerEndpoint: u.Host,
		PublicKey:    q.Get("publickey"),
		PresharedKey: q.Get("presharedkey"),
		Address:      splitList(q.Get("address")),
		AllowedIPs:   splitList(q.Get("allowedips")),
	}
	p.MTU, _ = strconv.Atoi(q.Get("mtu"))
	for _, r := range splitList(q.Get("reserved")) {
		n, _ := strconv.Atoi(r)
		p.Reserved = append(p.Reserved, n)
	}
	return p, u.Fragment, nil
}

func validKey(k string) bool {
	b, err := base64.StdEncoding.DecodeString(k)
	return err == nil && len(b) == 32
}

func (provider) Validate(raw json.RawMessage) error {
	p, err := providers.Decode[Params](raw)
	if err != nil {
		return err
	}
	if !validKey(p.PrivateKey) {
		return errors.New("privateKey must be a base64 32-byte key")
	}
	if !validKey(p.PublicKey) {
		return errors.New("publicKey must be a base64 32-byte key")
	}
	if p.PresharedKey != "" && !validKey(p.PresharedKey) {
		return errors.New("presharedKey must be a base64 32-byte key")
	}
	if len(p.Address) == 0 {
		return errors.New("at least one interface address is required")
	}
	for _, a := range p.Address {
		if _, err := parseAddr(a); err != nil {
			return fmt.Errorf("invalid address %q", a)
		}
	}
	for _, a := range p.AllowedIPs {
		if _, err := netip.ParsePrefix(a); err != nil {
			return fmt.Errorf("invalid allowed IP %q", a)
		}
	}
	host, port, err := net.SplitHostPort(p.PeerEndpoint)
	if err != nil || host == "" {
		return fmt.Errorf("invalid endpoint %q", p.PeerEndpoint)
	}
	n, _ := strconv.Atoi(port)
	if err := providers.ValidPort(n); err != nil {
		return err
	}
	if p.MTU != 0 && (p.MTU < 576 || p.MTU > 9000) {
		return fmt.Errorf("invalid MTU %d", p.MTU)
	}
	if len(p.Reserved) != 0 && len(p.Reserved) != 3 {
		return errors.New("reserved must have 3 bytes")
	}
	return nil
}

// parseAddr accepts "10.0.0.2/32" and bare "10.0.0.2".
func parseAddr(s string) (netip.Prefix, error) {
	if pr, err := netip.ParsePrefix(s); err == nil {
		return pr, nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(a, a.BitLen()), nil
}

func (pr provider) ToOutbound(tag string, raw json.RawMessage) (providers.OutboundSpec, error) {
	if err := pr.Validate(raw); err != nil {
		return providers.OutboundSpec{}, err
	}
	p, _ := providers.Decode[Params](raw)
	host, portStr, _ := net.SplitHostPort(p.PeerEndpoint)
	port, _ := strconv.Atoi(portStr)
	addrs := make([]string, 0, len(p.Address))
	for _, a := range p.Address {
		pfx, _ := parseAddr(a)
		addrs = append(addrs, pfx.String())
	}
	allowed := p.AllowedIPs
	if len(allowed) == 0 {
		allowed = []string{"0.0.0.0/0", "::/0"}
	}
	peer := map[string]any{
		"address":     strings.Trim(host, "[]"),
		"port":        port,
		"public_key":  p.PublicKey,
		"allowed_ips": allowed,
	}
	if p.PresharedKey != "" {
		peer["pre_shared_key"] = p.PresharedKey
	}
	keepalive := p.Keepalive
	if keepalive == 0 {
		keepalive = 25 // keeps NAT mappings alive and speeds up recovery after network changes
	}
	peer["persistent_keepalive_interval"] = keepalive
	if len(p.Reserved) == 3 {
		peer["reserved"] = p.Reserved
	}
	mtu := p.MTU
	if mtu == 0 {
		mtu = 1408
	}
	out := map[string]any{
		"type":        "wireguard",
		"tag":         tag,
		"mtu":         mtu,
		"address":     addrs,
		"private_key": p.PrivateKey,
		"peers":       []any{peer},
	}
	return providers.OutboundSpec{Kind: "endpoint", JSON: out}, nil
}

func (provider) ServerAddresses(raw json.RawMessage) ([]netip.Prefix, []string, error) {
	p, err := providers.Decode[Params](raw)
	if err != nil {
		return nil, nil, err
	}
	host, _, err := net.SplitHostPort(p.PeerEndpoint)
	if err != nil {
		return nil, nil, err
	}
	ips, domains := providers.HostAddresses(host)
	return ips, domains, nil
}

func (provider) ConfigSchema() []byte {
	return []byte(`{
  "type": "object",
  "required": ["privateKey", "address", "endpoint", "publicKey"],
  "properties": {
    "privateKey": {"type": "string", "title": "Private key", "format": "password"},
    "address": {"type": "array", "items": {"type": "string"}, "title": "Адреса интерфейса (CIDR)"},
    "endpoint": {"type": "string", "title": "Endpoint (host:port)"},
    "publicKey": {"type": "string", "title": "Public key пира"},
    "presharedKey": {"type": "string", "title": "Preshared key", "format": "password"},
    "allowedIps": {"type": "array", "items": {"type": "string"}, "title": "Allowed IPs"},
    "mtu": {"type": "integer", "title": "MTU", "default": 1408},
    "keepalive": {"type": "integer", "title": "Persistent keepalive, с", "default": 25}
  }
}`)
}
