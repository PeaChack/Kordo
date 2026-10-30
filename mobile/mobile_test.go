package mobile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PeaChack/Kordo/engine"
)

const reality = "vless://bf000d23-0752-40b4-affe-68f7707a9661@vpn.example.com:443?encryption=none&flow=xtls-rprx-vision&security=reality&sni=www.microsoft.com&fp=chrome&pbk=jNXHt1yRo0vDuchQlIP6Z0ZvjT3KtzVI-T4E7RoLJS0&sid=0123abcd&type=tcp#My%20Reality"

func TestFlow(t *testing.T) {
	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildConfig(false); err == nil {
		t.Fatal("config without servers must fail")
	}
	id, err := AddServer(reality, "")
	if err != nil {
		t.Fatal(err)
	}
	var list []serverInfo
	_ = json.Unmarshal([]byte(Servers()), &list)
	if len(list) != 1 || list[0].Name != "My Reality" || list[0].Address != "vpn.example.com" {
		t.Fatalf("servers: %s", Servers())
	}
	if err := SetAppRoute("org.telegram.messenger", id); err != nil {
		t.Fatal(err)
	}
	if err := SetAppRoute("com.bank.app", "direct"); err != nil {
		t.Fatal(err)
	}
	if err := SetAppRoute("x", "nope"); err == nil {
		t.Fatal("unknown target accepted")
	}

	// persisted
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(Apps(), "org.telegram.messenger") {
		t.Fatalf("apps after reload: %s", Apps())
	}

	cfg, err := BuildConfig(false)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Check([]byte(cfg)); err != nil {
		t.Fatalf("sing-box rejects the config: %v", err)
	}
	rules, _ := os.ReadDir(filepath.Join(dir, "rules"))
	found := false
	for _, f := range rules {
		b, _ := os.ReadFile(filepath.Join(dir, "rules", f.Name()))
		if strings.Contains(string(b), "org.telegram.messenger") && strings.Contains(string(b), "package_name") {
			found = true
		}
	}
	if !found {
		t.Fatal("no rule-set with the app's package_name")
	}

	// everything through the server except the listed apps
	if err := SetSettings(`{"mode":"all","defaultServer":"` + id + `","dns":"via_server","allowLan":true,"ipv6":"auto","mtu":9000,"stack":"gvisor"}`); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(Settings(), `"mode":"all"`) {
		t.Fatalf("settings: %s", Settings())
	}
	if cfg, err = BuildConfig(true); err != nil || engine.Check([]byte(cfg)) != nil {
		t.Fatalf("inverse config: %v", err)
	}

	if err := RemoveServer(id); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(Apps(), id) || strings.Contains(Settings(), `"mode":"all"`) {
		t.Fatalf("rules for a removed server remain: %s %s", Apps(), Settings())
	}
}
