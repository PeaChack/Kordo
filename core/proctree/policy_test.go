package proctree

import (
	"testing"

	"github.com/PeaChack/Kordo/core/model"
)

func TestDecide(t *testing.T) {
	p := model.DefaultProfile()
	p.Rules = []model.AppRule{
		{ID: "launcher", Match: model.Match{Kind: model.MatchPath, Value: `C:\Games\Launcher\launcher.exe`}, Target: model.ToServer("s1"), Children: model.ChildInherit,
			Learned: []model.Learned{{Match: model.Match{Kind: model.MatchPath, Value: `C:\Games\Launcher\updater.exe`}, Target: model.ToServer("s1")}}},
		{ID: "ide", Match: model.Match{Kind: model.MatchName, Value: "code.exe"}, Target: model.ToServer("s1"), Children: model.ChildAsk},
		{ID: "dl", Match: model.Match{Kind: model.MatchName, Value: "dl.exe"}, Target: model.ToServer("s1"), Children: model.ChildDirect},
	}
	m := NewMatcher()
	cases := []struct {
		parent, child string
		want          Action
	}{
		{`c:\games\launcher\LAUNCHER.exe`, `C:\Games\Game\game.exe`, ActLearn},
		{`C:\Games\Launcher\launcher.exe`, `C:\Windows\System32\cmd.exe`, ActAsk},    // shared exe
		{`C:\Games\Launcher\launcher.exe`, `C:\Games\Launcher\updater.exe`, ActNone}, // already learned
		{`C:\Games\Launcher\updater.exe`, `C:\Games\Patch\patch.exe`, ActLearn},      // child of learned child inherits too
		{`C:\Other\x.exe`, `C:\Games\Game\game.exe`, ActNone},                        // parent not routed
		{`C:\VS\Code.exe`, `C:\tools\git.exe`, ActAsk},
		{`C:\dl\dl.exe`, `C:\tools\ffmpeg.exe`, ActNone},                               // direct + default direct
		{`C:\Games\Launcher\launcher.exe`, `C:\Windows\System32\svchost.exe`, ActNone}, // forbidden
	}
	for _, c := range cases {
		d := m.Decide(p, c.parent, c.child)
		if d.Action != c.want {
			t.Errorf("%s -> %s: got %s want %s", c.parent, c.child, d.Action, c.want)
		}
	}
	d := m.Decide(p, `C:\Games\Launcher\launcher.exe`, `C:\Games\Game\game.exe`)
	if !d.Target.Equal(model.ToServer("s1")) || d.Child.Kind != model.MatchPath || d.RuleID != "launcher" {
		t.Fatalf("bad learn decision %+v", d)
	}
	p.DefaultTarget = model.ToServer("s1")
	if d := m.Decide(p, `C:\dl\dl.exe`, `C:\tools\ffmpeg.exe`); d.Action != ActLearn || d.Target.Kind != model.TargetDirect {
		t.Fatalf("direct policy in inverse mode: %+v", d)
	}
}

func TestSharedDevToolsAreAsked(t *testing.T) {
	p := model.DefaultProfile()
	p.Rules = []model.AppRule{{ID: "ide", Match: model.Match{Kind: model.MatchName, Value: "claude.exe"}, Target: model.ToServer("s1"), Children: model.ChildInherit}}
	m := NewMatcher()
	for _, child := range []string{`C:\Program Files\Git\cmd\git.exe`, `C:\Program Files\Git\usr\bin\bash.exe`, `C:\Program Files\Git\mingw64\libexec\git-core\git-remote-https.exe`} {
		if d := m.Decide(p, `C:\x\claude.exe`, child); d.Action != ActAsk || !d.Shared {
			t.Errorf("%s: got %+v, want ask (shared)", child, d)
		}
	}
}

func TestPruneSharedLearned(t *testing.T) {
	p := model.DefaultProfile()
	p.Rules = []model.AppRule{{ID: "r", Match: model.Match{Kind: model.MatchName, Value: "claude.exe"}, Target: model.ToServer("s1"),
		Learned: []model.Learned{
			{Match: model.Match{Kind: model.MatchPath, Value: `C:\Program Files\Git\cmd\git.exe`}, Source: "inherit"},
			{Match: model.Match{Kind: model.MatchPath, Value: `C:\Program Files\Git\bin\bash.exe`}, Source: "user"},
			{Match: model.Match{Kind: model.MatchPath, Value: `C:\Apps\idea64.exe`}, Source: "inherit"},
		}}}
	if n := model.PruneSharedLearned(&p); n != 1 {
		t.Fatalf("removed %d, want 1", n)
	}
	if len(p.Rules[0].Learned) != 2 {
		t.Fatalf("left %v", p.Rules[0].Learned)
	}
}
