package configgen_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kordo-vpn/kordo/core/configgen"
	"github.com/kordo-vpn/kordo/core/configgen/cgtest"
	"github.com/kordo-vpn/kordo/core/model"
)

var update = os.Getenv("UPDATE_GOLDEN") == "1"

func TestGolden(t *testing.T) {
	for name, f := range cgtest.Fixtures(t) {
		t.Run(name, func(t *testing.T) {
			out, err := configgen.Generate(f.Profile, f.Servers, f.Env)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(out.Config), configgen.SentinelExe) {
				t.Fatal("sentinel process rule is missing")
			}
			var cfg map[string]any
			if err := json.Unmarshal(out.Config, &cfg); err != nil {
				t.Fatal(err)
			}
			if cfg["route"].(map[string]any)["find_process"] != true {
				t.Fatal("find_process must be true")
			}
			dir := filepath.Join("..", "..", "testdata", "configgen", name)
			files := map[string][]byte{"config.json": out.Config}
			for k, v := range out.RuleSets {
				files[filepath.Join("rules", k)] = v
			}
			for rel, got := range files {
				p := filepath.Join(dir, rel)
				if update {
					_ = os.MkdirAll(filepath.Dir(p), 0o755)
					if err := os.WriteFile(p, got, 0o644); err != nil {
						t.Fatal(err)
					}
					continue
				}
				want, err := os.ReadFile(p)
				if err != nil {
					t.Fatalf("missing golden %s (run with UPDATE_GOLDEN=1)", p)
				}
				if string(want) != string(got) {
					t.Fatalf("golden mismatch %s:\n%s", p, got)
				}
			}
		})
	}
}

func TestDeterministic(t *testing.T) {
	f := cgtest.Fixtures(t)["only_selected"]
	a, _ := configgen.Generate(f.Profile, f.Servers, f.Env)
	for i := 0; i < 20; i++ {
		b, _ := configgen.Generate(f.Profile, f.Servers, f.Env)
		if string(a.Config) != string(b.Config) {
			t.Fatal("non-deterministic config")
		}
		for k := range a.RuleSets {
			if string(a.RuleSets[k]) != string(b.RuleSets[k]) {
				t.Fatal("non-deterministic rule-set", k)
			}
		}
	}
}

func TestRuleChangeIsHotReloadable(t *testing.T) {
	f := cgtest.Fixtures(t)["only_selected"]
	a, _ := configgen.Generate(f.Profile, f.Servers, f.Env)
	p := f.Profile
	p.Rules = append(append([]model.AppRule{}, p.Rules...), model.AppRule{
		ID: "r9", Match: model.Match{Kind: model.MatchPath, Value: `C:\x\curl.exe`}, Target: model.ToServer("b2"),
	})
	b, err := configgen.Generate(p, f.Servers, f.Env)
	if err != nil {
		t.Fatal(err)
	}
	if !configgen.OnlyRuleSetsChanged(a, b) {
		t.Fatal("adding an app rule must only change rule-sets")
	}
	s := append([]model.Server{}, f.Servers...)
	s[0].Enabled = false
	c, _ := configgen.Generate(f.Profile, s, f.Env)
	if configgen.OnlyRuleSetsChanged(a, c) {
		t.Fatal("disabling a server must change the config")
	}
}

func TestMatchRegex(t *testing.T) {
	cases := []struct {
		m     model.Match
		path  string
		match bool
	}{
		{model.Match{Kind: model.MatchPath, Value: `C:\Program Files\App\app.exe`}, `c:\program files\app\APP.EXE`, true},
		{model.Match{Kind: model.MatchPath, Value: `C:\Program Files\App\app.exe`}, `C:\Program Files\App\app.exe.bak`, false},
		{model.Match{Kind: model.MatchName, Value: "Telegram.exe"}, `D:\tg\telegram.exe`, true},
		{model.Match{Kind: model.MatchName, Value: "Telegram.exe"}, `D:\tg\notTelegram.exe`, false},
		{model.Match{Kind: model.MatchStorePackage, Value: "Microsoft.WindowsTerminal"}, `C:\Program Files\WindowsApps\Microsoft.WindowsTerminal_1.21.2911.0_x64__8wekyb3d8bbwe\WindowsTerminal.exe`, true},
		{model.Match{Kind: model.MatchStorePackage, Value: "Microsoft.WindowsTerminal"}, `C:\Program Files\WindowsApps\Microsoft.WindowsTerminalPreview_1.0_x64__8wekyb3d8bbwe\x.exe`, false},
	}
	for _, c := range cases {
		re := regexp.MustCompile(configgen.MatchRegex(c.m))
		if re.MatchString(c.path) != c.match {
			t.Errorf("%v vs %s: want %v", c.m, c.path, c.match)
		}
	}
	if m := model.MatchForPath(`C:\Program Files\WindowsApps\Microsoft.WindowsTerminal_1.21.2911.0_x64__8wekyb3d8bbwe\WindowsTerminal.exe`); m.Kind != model.MatchStorePackage || m.Value != "Microsoft.WindowsTerminal" {
		t.Errorf("MatchForPath store = %v", m)
	}
}

func TestValidation(t *testing.T) {
	p := model.DefaultProfile()
	p.Rules = []model.AppRule{{ID: "x", Match: model.Match{Kind: model.MatchName, Value: "svchost.exe"}, Target: model.Direct()}}
	if _, err := configgen.Generate(p, nil, configgen.Env{RulesDir: "x"}); err == nil {
		t.Fatal("svchost must be rejected")
	}
	p.Rules = []model.AppRule{{ID: "x", Match: model.Match{Kind: model.MatchName, Value: "a.exe"}, Target: model.ToServer("nope")}}
	if _, err := configgen.Generate(p, nil, configgen.Env{RulesDir: "x"}); err == nil {
		t.Fatal("unknown server must be rejected")
	}
	p.Rules = []model.AppRule{
		{ID: "x", Match: model.Match{Kind: model.MatchName, Value: "a.exe"}, Target: model.Direct()},
		{ID: "y", Match: model.Match{Kind: model.MatchName, Value: "A.EXE"}, Target: model.Direct()},
	}
	if _, err := configgen.Generate(p, nil, configgen.Env{RulesDir: "x"}); err == nil {
		t.Fatal("duplicate app must be rejected")
	}
}
