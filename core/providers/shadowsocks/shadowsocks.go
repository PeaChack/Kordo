// Package shadowsocks implements the Shadowsocks provider (SIP002 links, AEAD and SS2022).
package shadowsocks

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/kordo-vpn/kordo/core/providers"
)

type Params struct {
	Server     string `json:"server"`
	Port       int    `json:"port"`
	Method     string `json:"method"`
	Password   string `json:"password"`
	Plugin     string `json:"plugin,omitempty"`     // "obfs-local" | "v2ray-plugin"
	PluginOpts string `json:"pluginOpts,omitempty"` // e.g. "obfs=http;obfs-host=example.com"
	UoT        bool   `json:"udpOverTcp,omitempty"`
}

var methods = map[string]bool{
	"2022-blake3-aes-128-gcm": true, "2022-blake3-aes-256-gcm": true, "2022-blake3-chacha20-poly1305": true,
	"aes-128-gcm": true, "aes-192-gcm": true, "aes-256-gcm": true,
	"chacha20-ietf-poly1305": true, "xchacha20-ietf-poly1305": true, "none": true,
}

type provider struct{}

func init() { providers.Register(provider{}) }

func (provider) ID() string                   { return "shadowsocks" }
func (provider) DisplayName() string          { return "Shadowsocks (AEAD / 2022)" }
func (provider) URISchemes() []string         { return []string{"ss"} }
func (provider) Capabilities() providers.Caps { return providers.Caps{UDP: true} }

func b64(s string) (string, error) {
	s = strings.TrimRight(strings.TrimSpace(s), "=")
	for _, enc := range []*base64.Encoding{base64.RawURLEncoding, base64.RawStdEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return string(b), nil
		}
	}
	return "", errors.New("invalid base64")
}

func (pr provider) ParseURI(uri string) (json.RawMessage, string, error) {
	uri = strings.TrimSpace(uri)
	if !strings.HasPrefix(strings.ToLower(uri), "ss://") {
		return nil, "", errors.New("not an ss:// link")
	}
	body := uri[5:]
	name := ""
	if i := strings.IndexByte(body, '#'); i >= 0 {
		name, _ = url.PathUnescape(body[i+1:])
		body = body[:i]
	}
	// Legacy form: ss://base64(method:password@host:port)
	if !strings.Contains(body, "@") {
		dec, err := b64(strings.SplitN(body, "?", 2)[0])
		if err != nil {
			return nil, "", fmt.Errorf("parse ss link: %w", err)
		}
		body = dec
		at := strings.LastIndexByte(body, '@')
		if at < 0 {
			return nil, "", errors.New("parse ss link: missing @")
		}
		body = url.PathEscape(body[:at]) + "@" + body[at+1:]
	}
	u, err := url.Parse("ss://" + body)
	if err != nil {
		return nil, "", fmt.Errorf("parse ss link: %w", err)
	}
	var method, password string
	if pw, ok := u.User.Password(); ok {
		method, password = u.User.Username(), pw
	} else {
		dec, err := b64(u.User.Username())
		if err != nil {
			return nil, "", fmt.Errorf("parse ss userinfo: %w", err)
		}
		m, pw, ok := strings.Cut(dec, ":")
		if !ok {
			return nil, "", errors.New("parse ss userinfo: expected method:password")
		}
		method, password = m, pw
	}
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		return nil, "", fmt.Errorf("parse ss host: %w", err)
	}
	port, _ := strconv.Atoi(portStr)
	p := Params{Server: host, Port: port, Method: strings.ToLower(method), Password: password}
	if pl := u.Query().Get("plugin"); pl != "" {
		parts := strings.SplitN(pl, ";", 2)
		p.Plugin = parts[0]
		if p.Plugin == "simple-obfs" {
			p.Plugin = "obfs-local"
		}
		if len(parts) == 2 {
			p.PluginOpts = parts[1]
		}
	}
	if name == "" {
		name = host
	}
	raw := providers.Encode(p)
	if err := pr.Validate(raw); err != nil {
		return nil, "", err
	}
	return raw, name, nil
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
	if !methods[p.Method] {
		return fmt.Errorf("unsupported method %q", p.Method)
	}
	if p.Password == "" && p.Method != "none" {
		return errors.New("password is required")
	}
	if strings.HasPrefix(p.Method, "2022-") {
		for _, part := range strings.Split(p.Password, ":") {
			if _, err := base64.StdEncoding.DecodeString(part); err != nil {
				return errors.New("SS2022 password must be base64 key(s)")
			}
		}
	}
	switch p.Plugin {
	case "", "obfs-local", "v2ray-plugin":
	default:
		return fmt.Errorf("unsupported plugin %q", p.Plugin)
	}
	return nil
}

func (pr provider) ToOutbound(tag string, raw json.RawMessage) (providers.OutboundSpec, error) {
	if err := pr.Validate(raw); err != nil {
		return providers.OutboundSpec{}, err
	}
	p, _ := providers.Decode[Params](raw)
	out := map[string]any{
		"type":        "shadowsocks",
		"tag":         tag,
		"server":      p.Server,
		"server_port": p.Port,
		"method":      p.Method,
		"password":    p.Password,
	}
	if p.Plugin != "" {
		out["plugin"] = p.Plugin
		out["plugin_opts"] = p.PluginOpts
	}
	if p.UoT {
		out["udp_over_tcp"] = map[string]any{"enabled": true, "version": 2}
	}
	return providers.OutboundSpec{Kind: "outbound", JSON: out}, nil
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
  "required": ["server", "port", "method", "password"],
  "properties": {
    "server": {"type": "string", "title": "Сервер"},
    "port": {"type": "integer", "title": "Порт", "minimum": 1, "maximum": 65535, "default": 8388},
    "method": {"type": "string", "title": "Шифр", "enum": ["2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305", "aes-128-gcm", "aes-256-gcm", "chacha20-ietf-poly1305", "xchacha20-ietf-poly1305"], "default": "2022-blake3-aes-128-gcm"},
    "password": {"type": "string", "title": "Пароль / ключ", "format": "password"},
    "plugin": {"type": "string", "title": "Плагин", "enum": ["", "obfs-local", "v2ray-plugin"]},
    "pluginOpts": {"type": "string", "title": "Опции плагина"},
    "udpOverTcp": {"type": "boolean", "title": "UDP over TCP"}
  }
}`)
}
