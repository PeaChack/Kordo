// Package cgtest holds configuration fixtures shared by configgen and engine tests.
package cgtest

import (
	"net/netip"
	"testing"

	"github.com/portico-vpn/portico/core/configgen"
	"github.com/portico-vpn/portico/core/model"
	"github.com/portico-vpn/portico/core/providers"
	_ "github.com/portico-vpn/portico/core/providers/all"
)

type Fixture struct {
	Profile model.Profile
	Servers []model.Server
	Env     configgen.Env
}

func mustServer(t testing.TB, id, link string) model.Server {
	t.Helper()
	pid, params, name, err := providers.ParseAny(link)
	if err != nil {
		t.Fatal(err)
	}
	return model.Server{ID: id, Name: name, ProviderID: pid, Params: params, Enabled: true}
}

func Fixtures(t testing.TB) map[string]Fixture {
	vless := mustServer(t, "a1", "vless://bf000d23-0752-40b4-affe-68f7707a9661@vpn.example.com:443?flow=xtls-rprx-vision&security=reality&sni=www.microsoft.com&fp=chrome&pbk=jNXHt1yRo0vDuchQlIP6Z0ZvjT3KtzVI-T4E7RoLJS0&sid=0123abcd#reality")
	ss := mustServer(t, "b2", "ss://2022-blake3-aes-128-gcm:8JCsPssfgS8tiRwiMlhARg%3D%3D@198.51.100.4:443#ss")
	wg := mustServer(t, "c3", "[Interface]\nPrivateKey = yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk=\nAddress = 10.66.0.2/32\n[Peer]\nPublicKey = xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=\nEndpoint = 192.0.2.10:51820\n")
	env := configgen.Env{
		RulesDir: `C:\ProgramData\Portico\rules`,
		Resolved: map[string][]netip.Addr{"vpn.example.com": {netip.MustParseAddr("203.0.113.9")}},
	}
	only := model.DefaultProfile()
	only.Rules = []model.AppRule{
		{ID: "r1", Match: model.Match{Kind: model.MatchPath, Value: `C:\Program Files\Mozilla Firefox\firefox.exe`}, Target: model.ToServer("a1"), Children: model.ChildInherit,
			Learned: []model.Learned{{Match: model.Match{Kind: model.MatchPath, Value: `C:\Program Files\Mozilla Firefox\crashreporter.exe`}, Target: model.ToServer("a1"), Source: "inherit"}}},
		{ID: "r2", Match: model.Match{Kind: model.MatchName, Value: "Telegram.exe"}, Target: model.ToServer("b2"), Children: model.ChildDirect},
		{ID: "r3", Match: model.Match{Kind: model.MatchStorePackage, Value: "Microsoft.WindowsTerminal"}, Target: model.ToServer("c3")},
		{ID: "r4", Match: model.Match{Kind: model.MatchPath, Value: `D:\Games\game.exe`}, Target: model.Target{Kind: model.TargetBlock}},
	}
	inverse := model.DefaultProfile()
	inverse.DefaultTarget = model.ToServer("c3")
	inverse.AllowLAN = false
	inverse.IPv6 = "block"
	inverse.DNS = model.DNSMode{Mode: "doh_direct", DoH: "9.9.9.9"}
	inverse.KillSwitch = model.KillVPNApps
	inverse.Rules = []model.AppRule{
		{ID: "r1", Match: model.Match{Kind: model.MatchPath, Value: `C:\Steam\steam.exe`}, Target: model.Direct()},
		{ID: "r2", Match: model.Match{Kind: model.MatchPath, Value: `C:\Apps\bank.exe`}, Target: model.ToServer("b2")},
	}
	disabled := ss
	disabled.Enabled = false
	envV6 := env
	envV6.HasIPv6 = true
	envV6.ClashAPI = "127.0.0.1:19190"
	envV6.ClashSecret = "test-secret"
	return map[string]Fixture{
		"only_selected": {only, []model.Server{vless, ss, wg}, env},
		"inverse":       {inverse, []model.Server{vless, disabled, wg}, envV6},
		"empty":         {model.DefaultProfile(), nil, env},
	}
}
