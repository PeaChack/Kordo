package providers_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PeaChack/Kordo/core/providers"
	_ "github.com/PeaChack/Kordo/core/providers/all"
)

var update = os.Getenv("UPDATE_GOLDEN") == "1"

// Test fixtures: syntactically valid but fake credentials.
var cases = []struct {
	name, link, provider, wantName string
}{
	{"vless_reality", "vless://bf000d23-0752-40b4-affe-68f7707a9661@vpn.example.com:443?encryption=none&flow=xtls-rprx-vision&security=reality&sni=www.microsoft.com&fp=chrome&pbk=jNXHt1yRo0vDuchQlIP6Z0ZvjT3KtzVI-T4E7RoLJS0&sid=0123abcd&type=tcp#My%20Reality", "vless", "My Reality"},
	{"vless_ws_tls", "vless://bf000d23-0752-40b4-affe-68f7707a9661@203.0.113.7:8443?security=tls&sni=cdn.example.org&type=ws&path=%2Fws&host=cdn.example.org#ws", "vless", "ws"},
	{"vless_grpc", "vless://bf000d23-0752-40b4-affe-68f7707a9661@vpn.example.com:443?security=tls&type=grpc&serviceName=tun", "vless", "vpn.example.com"},
	{"ss_sip002_b64", "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ@198.51.100.4:8388#SS%20AEAD", "shadowsocks", "SS AEAD"},
	{"ss_2022_plain", "ss://2022-blake3-aes-128-gcm:8JCsPssfgS8tiRwiMlhARg%3D%3D@ss.example.net:443#SS2022", "shadowsocks", "SS2022"},
	{"ss_legacy", "ss://YWVzLTEyOC1nY206dGVzdEAxOTIuMC4yLjE6ODA4OA#legacy", "shadowsocks", "legacy"},
	{"wg_conf", `[Interface]
PrivateKey = yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk=
Address = 10.66.0.2/32, fd00::2/128
DNS = 1.1.1.1
MTU = 1420

[Peer]
PublicKey = xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=
PresharedKey = FpCyhws9cxwWoV4xELtfJvjJN+zQVRPISllRWgeopVE=
Endpoint = wg.example.com:51820
AllowedIPs = 0.0.0.0/0, ::/0
PersistentKeepalive = 15
`, "wireguard", "WireGuard wg.example.com"},
}

func TestParseAndGolden(t *testing.T) {
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pid, params, name, err := providers.ParseAny(c.link)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if pid != c.provider {
				t.Fatalf("provider = %s, want %s", pid, c.provider)
			}
			if name != c.wantName {
				t.Fatalf("name = %q, want %q", name, c.wantName)
			}
			p, _ := providers.Get(pid)
			spec, err := p.ToOutbound("srv-test", params)
			if err != nil {
				t.Fatalf("to outbound: %v", err)
			}
			got, _ := json.MarshalIndent(map[string]any{"params": json.RawMessage(params), "kind": spec.Kind, "json": spec.JSON}, "", "  ")
			golden := filepath.Join("..", "..", "testdata", "providers", c.name+".json")
			if update {
				_ = os.MkdirAll(filepath.Dir(golden), 0o755)
				if err := os.WriteFile(golden, append(got, '\n'), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("missing golden (run with UPDATE_GOLDEN=1): %v", err)
			}
			if strings.TrimSpace(string(want)) != strings.TrimSpace(string(got)) {
				t.Fatalf("golden mismatch for %s:\n%s", c.name, got)
			}
		})
	}
}

func TestRejects(t *testing.T) {
	bad := []string{
		"vless://not-a-uuid@h:443?security=none",
		"vless://bf000d23-0752-40b4-affe-68f7707a9661@h:443?security=reality&sni=x", // no pbk
		"vless://bf000d23-0752-40b4-affe-68f7707a9661@h:443?type=xhttp",
		"ss://bad@h:1",
		"ss://2022-blake3-aes-128-gcm:not%20base64@h:443",
		"[Interface]\nPrivateKey = short\nAddress=10.0.0.2/32\n[Peer]\nPublicKey=xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=\nEndpoint=h:1",
		"[Interface]\nPrivateKey = yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk=\nAddress=10.0.0.2/32\nJc=4\n[Peer]\nPublicKey=xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=\nEndpoint=h:1",
		"trojan://x@h:443",
	}
	for _, b := range bad {
		if _, _, _, err := providers.ParseAny(b); err == nil {
			t.Errorf("expected error for %q", b)
		}
	}
}

func TestServerAddresses(t *testing.T) {
	_, params, _, err := providers.ParseAny(cases[1].link)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := providers.Get("vless")
	ips, domains, err := p.ServerAddresses(params)
	if err != nil || len(ips) != 1 || ips[0].String() != "203.0.113.7/32" || len(domains) != 0 {
		t.Fatalf("got %v %v %v", ips, domains, err)
	}
}
