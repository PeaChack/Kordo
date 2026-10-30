package engine_test

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/PeaChack/Kordo/core/providers"
	_ "github.com/PeaChack/Kordo/core/providers/all"
	"github.com/PeaChack/Kordo/engine"
)

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// TestProxyEndToEnd runs a real sing-box server inbound locally and sends an
// HTTP request through the outbound produced by each provider, i.e. the exact
// JSON Kordo generates is exercised against a live peer.
func TestProxyEndToEnd(t *testing.T) {
	hits := 0
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer web.Close()

	cases := []struct {
		name    string
		inbound func(port int) map[string]any
		link    func(port int) string
	}{
		{
			name: "shadowsocks-2022",
			inbound: func(port int) map[string]any {
				return map[string]any{"type": "shadowsocks", "listen": "127.0.0.1", "listen_port": port,
					"method": "2022-blake3-aes-128-gcm", "password": "8JCsPssfgS8tiRwiMlhARg=="}
			},
			link: func(port int) string {
				return fmt.Sprintf("ss://2022-blake3-aes-128-gcm:8JCsPssfgS8tiRwiMlhARg%%3D%%3D@127.0.0.1:%d#local", port)
			},
		},
		{
			name: "vless-tcp",
			inbound: func(port int) map[string]any {
				return map[string]any{"type": "vless", "listen": "127.0.0.1", "listen_port": port,
					"users": []any{map[string]any{"uuid": "bf000d23-0752-40b4-affe-68f7707a9661"}}}
			},
			link: func(port int) string {
				return fmt.Sprintf("vless://bf000d23-0752-40b4-affe-68f7707a9661@127.0.0.1:%d?security=none&type=tcp#local", port)
			},
		},
		{
			name: "vless-ws",
			inbound: func(port int) map[string]any {
				return map[string]any{"type": "vless", "listen": "127.0.0.1", "listen_port": port,
					"users":     []any{map[string]any{"uuid": "bf000d23-0752-40b4-affe-68f7707a9661"}},
					"transport": map[string]any{"type": "ws", "path": "/tunnel"}}
			},
			link: func(port int) string {
				return fmt.Sprintf("vless://bf000d23-0752-40b4-affe-68f7707a9661@127.0.0.1:%d?security=none&type=ws&path=%%2Ftunnel#local", port)
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			port := freePort(t)
			serverCfg, _ := json.Marshal(map[string]any{
				"log":       map[string]any{"level": "warn"},
				"inbounds":  []any{c.inbound(port)},
				"outbounds": []any{map[string]any{"type": "direct", "tag": "direct"}},
			})
			srv, err := engine.Start(serverCfg, engine.Options{})
			if err != nil {
				t.Fatalf("server: %v", err)
			}
			defer srv.Close()

			pid, params, _, err := providers.ParseAny(c.link(port))
			if err != nil {
				t.Fatal(err)
			}
			p, _ := providers.Get(pid)
			spec, err := p.ToOutbound("probe", params)
			if err != nil {
				t.Fatal(err)
			}
			clientCfg, _ := json.Marshal(map[string]any{
				"log":       map[string]any{"level": "warn"},
				"dns":       map[string]any{"servers": []any{map[string]any{"type": "local", "tag": "local"}}},
				"outbounds": []any{map[string]any{"type": "direct", "tag": "direct"}, spec.JSON},
				"route":     map[string]any{"default_domain_resolver": "local"},
			})
			cli, err := engine.Start(clientCfg, engine.Options{})
			if err != nil {
				t.Fatalf("client: %v", err)
			}
			defer cli.Close()

			before := hits
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			rtt, err := cli.URLTest(ctx, "probe", web.URL+"/generate_204")
			if err != nil {
				t.Fatalf("urltest through %s: %v", c.name, err)
			}
			if hits == before {
				t.Fatal("request did not reach the web server")
			}
			t.Logf("%s: %v", c.name, rtt)
		})
	}
}

func wgKeys(t *testing.T) (priv, pub string) {
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(k.Bytes()), base64.StdEncoding.EncodeToString(k.PublicKey().Bytes())
}

func TestWireGuardEndToEnd(t *testing.T) {
	// 127.0.0.1 would be the client netstack's own loopback, so serve on the LAN address.
	probe, err := net.Dial("udp", "192.0.2.1:9")
	if err != nil {
		t.Skip("no non-loopback address:", err)
	}
	lanIP := probe.LocalAddr().(*net.UDPAddr).IP.String()
	probe.Close()
	wl, err := net.Listen("tcp", net.JoinHostPort(lanIP, "0"))
	if err != nil {
		t.Skip(err)
	}
	web := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	web.Listener.Close()
	web.Listener = wl
	web.Start()
	defer web.Close()
	sPriv, sPub := wgKeys(t)
	cPriv, cPub := wgKeys(t)
	l, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.LocalAddr().(*net.UDPAddr).Port
	l.Close()
	serverCfg, _ := json.Marshal(map[string]any{
		"log": map[string]any{"level": "warn"},
		"endpoints": []any{map[string]any{"type": "wireguard", "tag": "wg-in", "address": []string{"10.77.0.1/24"},
			"private_key": sPriv, "listen_port": port,
			"peers": []any{map[string]any{"public_key": cPub, "allowed_ips": []string{"10.77.0.2/32"}}}}},
		"outbounds": []any{map[string]any{"type": "direct", "tag": "direct"}},
		"route":     map[string]any{"final": "direct"},
	})
	srv, err := engine.Start(serverCfg, engine.Options{})
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	defer srv.Close()

	conf := fmt.Sprintf("[Interface]\nPrivateKey = %s\nAddress = 10.77.0.2/32\n[Peer]\nPublicKey = %s\nEndpoint = 127.0.0.1:%d\nAllowedIPs = 0.0.0.0/0\n", cPriv, sPub, port)
	pid, params, _, err := providers.ParseAny(conf)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := providers.Get(pid)
	spec, err := p.ToOutbound("probe", params)
	if err != nil || spec.Kind != "endpoint" {
		t.Fatalf("spec %v %v", spec.Kind, err)
	}
	spec.JSON["detour"] = "wg-direct"
	clientCfg, _ := json.Marshal(map[string]any{
		"log": map[string]any{"level": "warn"},
		"dns": map[string]any{"servers": []any{map[string]any{"type": "local", "tag": "local"}}},
		// same shape as configgen output: endpoint detours via a non-empty direct outbound
		"outbounds": []any{map[string]any{"type": "direct", "tag": "direct"},
			map[string]any{"type": "direct", "tag": "wg-direct", "connect_timeout": "10s"}},
		"endpoints": []any{spec.JSON},
		"route":     map[string]any{"default_domain_resolver": "local"},
	})
	cli, err := engine.Start(clientCfg, engine.Options{})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	defer cli.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// the web server listens on 127.0.0.1; through the tunnel the server side dials it directly
	rtt, err := cli.URLTest(ctx, "probe", web.URL+"/generate_204")
	if err != nil {
		t.Fatalf("urltest through wireguard: %v", err)
	}
	t.Logf("wireguard: %v", rtt)
}
