// Package vless implements the VLESS provider (TCP / Reality / TLS, ws, grpc, httpupgrade, http).
package vless

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/portico-vpn/portico/core/providers"
)

type Params struct {
	Server         string   `json:"server"`
	Port           int      `json:"port"`
	UUID           string   `json:"uuid"`
	Flow           string   `json:"flow,omitempty"`     // "" | "xtls-rprx-vision"
	Security       string   `json:"security,omitempty"` // "none" | "tls" | "reality"
	SNI            string   `json:"sni,omitempty"`
	Fingerprint    string   `json:"fingerprint,omitempty"` // uTLS fingerprint, e.g. "chrome"
	PublicKey      string   `json:"publicKey,omitempty"`   // reality
	ShortID        string   `json:"shortId,omitempty"`     // reality
	ALPN           []string `json:"alpn,omitempty"`
	Insecure       bool     `json:"insecure,omitempty"`
	Transport      string   `json:"transport,omitempty"` // "tcp" | "ws" | "grpc" | "httpupgrade" | "http"
	Path           string   `json:"path,omitempty"`
	Host           string   `json:"host,omitempty"`
	ServiceName    string   `json:"serviceName,omitempty"`
	PacketEncoding string   `json:"packetEncoding,omitempty"` // "" | "xudp" | "packetaddr"
}

type provider struct{}

func init() { providers.Register(provider{}) }

func (provider) ID() string                   { return "vless" }
func (provider) DisplayName() string          { return "VLESS (Reality / TLS)" }
func (provider) URISchemes() []string         { return []string{"vless"} }
func (provider) Capabilities() providers.Caps { return providers.Caps{UDP: true} }

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var shortIDRe = regexp.MustCompile(`^[0-9a-fA-F]{0,16}$`)

func (provider) ParseURI(uri string) (json.RawMessage, string, error) {
	u, err := url.Parse(strings.TrimSpace(uri))
	if err != nil {
		return nil, "", fmt.Errorf("parse vless link: %w", err)
	}
	if !strings.EqualFold(u.Scheme, "vless") {
		return nil, "", errors.New("not a vless:// link")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		return nil, "", fmt.Errorf("invalid port %q", u.Port())
	}
	q := u.Query()
	p := Params{
		Server:         u.Hostname(),
		Port:           port,
		UUID:           u.User.Username(),
		Flow:           q.Get("flow"),
		Security:       strings.ToLower(q.Get("security")),
		SNI:            firstNonEmpty(q.Get("sni"), q.Get("peer")),
		Fingerprint:    q.Get("fp"),
		PublicKey:      q.Get("pbk"),
		ShortID:        q.Get("sid"),
		Insecure:       q.Get("allowInsecure") == "1" || q.Get("insecure") == "1",
		Transport:      strings.ToLower(q.Get("type")),
		Path:           q.Get("path"),
		Host:           q.Get("host"),
		ServiceName:    q.Get("serviceName"),
		PacketEncoding: q.Get("packetEncoding"),
	}
	if a := q.Get("alpn"); a != "" {
		p.ALPN = strings.Split(a, ",")
	}
	if p.Security == "" {
		p.Security = "none"
	}
	if p.Transport == "" || p.Transport == "raw" {
		p.Transport = "tcp"
	}
	if p.Transport == "tcp" && q.Get("headerType") == "http" {
		p.Transport = "http"
	}
	if p.Transport == "xhttp" || p.Transport == "splithttp" {
		return nil, "", fmt.Errorf("transport %q is not supported by the engine", p.Transport)
	}
	name := u.Fragment
	if name == "" {
		name = p.Server
	}
	raw := providers.Encode(p)
	if err := (provider{}).Validate(raw); err != nil {
		return nil, "", err
	}
	return raw, name, nil
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

func (provider) Validate(raw json.RawMessage) error {
	p, err := providers.Decode[Params](raw)
	if err != nil {
		return err
	}
	if p.Server == "" {
		return errors.New("server is required")
	}
	if err := providers.ValidPort(p.Port); err != nil {
		return err
	}
	if !uuidRe.MatchString(p.UUID) {
		return errors.New("uuid is not a valid UUID")
	}
	switch p.Flow {
	case "", "xtls-rprx-vision":
	default:
		return fmt.Errorf("unsupported flow %q", p.Flow)
	}
	switch p.Security {
	case "", "none", "tls":
	case "reality":
		if p.PublicKey == "" {
			return errors.New("reality requires publicKey (pbk)")
		}
		if !shortIDRe.MatchString(p.ShortID) {
			return errors.New("reality shortId must be up to 16 hex chars")
		}
		if p.SNI == "" {
			return errors.New("reality requires sni")
		}
	default:
		return fmt.Errorf("unsupported security %q", p.Security)
	}
	switch p.Transport {
	case "", "tcp", "ws", "grpc", "httpupgrade", "http":
	default:
		return fmt.Errorf("unsupported transport %q", p.Transport)
	}
	if p.Flow != "" && p.Transport != "" && p.Transport != "tcp" {
		return errors.New("flow xtls-rprx-vision requires tcp transport")
	}
	switch p.PacketEncoding {
	case "", "xudp", "packetaddr", "none":
	default:
		return fmt.Errorf("unsupported packet encoding %q", p.PacketEncoding)
	}
	return nil
}

func (pr provider) ToOutbound(tag string, raw json.RawMessage) (providers.OutboundSpec, error) {
	if err := pr.Validate(raw); err != nil {
		return providers.OutboundSpec{}, err
	}
	p, _ := providers.Decode[Params](raw)
	out := map[string]any{
		"type":        "vless",
		"tag":         tag,
		"server":      p.Server,
		"server_port": p.Port,
		"uuid":        strings.ToLower(p.UUID),
	}
	if p.Flow != "" {
		out["flow"] = p.Flow
	}
	switch p.PacketEncoding {
	case "", "xudp":
		out["packet_encoding"] = "xudp"
	case "packetaddr":
		out["packet_encoding"] = "packetaddr"
	}
	if p.Security == "tls" || p.Security == "reality" {
		tls := map[string]any{"enabled": true}
		sni := p.SNI
		if sni == "" && p.Security == "tls" {
			sni = firstNonEmpty(p.Host, p.Server)
		}
		if sni != "" {
			tls["server_name"] = sni
		}
		if p.Insecure && p.Security == "tls" {
			tls["insecure"] = true
		}
		if len(p.ALPN) > 0 {
			tls["alpn"] = p.ALPN
		}
		fp := p.Fingerprint
		if fp == "" && p.Security == "reality" {
			fp = "chrome" // reality requires uTLS
		}
		if fp != "" {
			tls["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
		}
		if p.Security == "reality" {
			tls["reality"] = map[string]any{"enabled": true, "public_key": p.PublicKey, "short_id": p.ShortID}
		}
		out["tls"] = tls
	}
	switch p.Transport {
	case "ws":
		t := map[string]any{"type": "ws", "path": orSlash(p.Path)}
		if p.Host != "" {
			t["headers"] = map[string]any{"Host": p.Host}
		}
		out["transport"] = t
	case "grpc":
		out["transport"] = map[string]any{"type": "grpc", "service_name": p.ServiceName}
	case "httpupgrade":
		t := map[string]any{"type": "httpupgrade", "path": orSlash(p.Path)}
		if p.Host != "" {
			t["host"] = p.Host
		}
		out["transport"] = t
	case "http":
		t := map[string]any{"type": "http", "path": orSlash(p.Path)}
		if p.Host != "" {
			t["host"] = strings.Split(p.Host, ",")
		}
		out["transport"] = t
	}
	return providers.OutboundSpec{Kind: "outbound", JSON: out}, nil
}

func orSlash(s string) string {
	if s == "" {
		return "/"
	}
	return s
}

func (provider) ServerAddresses(raw json.RawMessage) ([]netip.Prefix, []string, error) {
	p, err := providers.Decode[Params](raw)
	if err != nil {
		return nil, nil, err
	}
	ips, domains := providers.HostAddresses(p.Server)
	return ips, domains, nil
}

func (provider) ConfigSchema() []byte {
	return []byte(`{
  "type": "object",
  "required": ["server", "port", "uuid"],
  "properties": {
    "server": {"type": "string", "title": "Сервер"},
    "port": {"type": "integer", "title": "Порт", "minimum": 1, "maximum": 65535, "default": 443},
    "uuid": {"type": "string", "title": "UUID"},
    "flow": {"type": "string", "title": "Flow", "enum": ["", "xtls-rprx-vision"]},
    "security": {"type": "string", "title": "Безопасность", "enum": ["reality", "tls", "none"], "default": "reality"},
    "sni": {"type": "string", "title": "SNI"},
    "fingerprint": {"type": "string", "title": "uTLS fingerprint", "enum": ["", "chrome", "firefox", "safari", "edge", "ios", "android", "random", "randomized"]},
    "publicKey": {"type": "string", "title": "Reality public key"},
    "shortId": {"type": "string", "title": "Reality short ID"},
    "transport": {"type": "string", "title": "Транспорт", "enum": ["tcp", "ws", "grpc", "httpupgrade", "http"], "default": "tcp"},
    "path": {"type": "string", "title": "Path"},
    "host": {"type": "string", "title": "Host"},
    "serviceName": {"type": "string", "title": "gRPC service name"},
    "insecure": {"type": "boolean", "title": "Не проверять сертификат"}
  }
}`)
}
