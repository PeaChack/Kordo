package model

import "testing"

func TestFamily(t *testing.T) {
	same := [][2]string{
		{`C:\U\AppData\Local\Programs\Opera\139.0.6053.38\opera_crashreporter.exe`, `C:\U\AppData\Local\Programs\Opera\140.0.6074.17\opera_crashreporter.exe`},
		{`C:\U\AppData\Local\Discord\app-1.0.9001\Discord.exe`, `C:\U\AppData\Local\Discord\app-1.0.9002\discord.exe`},
		{`C:\U\AppData\Local\Temp\.opera\B7019CCB526C\installer.exe`, `C:\U\AppData\Local\Temp\.opera\B6889423BD92\x\installer.exe`},
	}
	for _, c := range same {
		a, b := Match{Kind: MatchPath, Value: c[0]}, Match{Kind: MatchPath, Value: c[1]}
		if a.Family() != b.Family() {
			t.Errorf("%s / %s: %q != %q", c[0], c[1], a.Family(), b.Family())
		}
	}
	diff := [][2]string{
		{`C:\Apps\A\tool.exe`, `C:\Apps\B\tool.exe`},
		{`C:\Apps\A\1.0\tool.exe`, `C:\Apps\A\1.0\other.exe`},
	}
	for _, c := range diff {
		a, b := Match{Kind: MatchPath, Value: c[0]}, Match{Kind: MatchPath, Value: c[1]}
		if a.Family() == b.Family() {
			t.Errorf("%s / %s must differ", c[0], c[1])
		}
	}
}

func TestAppRoot(t *testing.T) {
	cases := map[string]string{
		`C:\Users\me\AppData\Local\Programs\Opera\139.0.1\opera.exe`:     `c:\users\me\appdata\local\programs\opera\`,
		`C:\Program Files\Foo\bin\x64\foo.exe`:                           `c:\program files\foo\`,
		`C:\Program Files\Foo\foo.exe`:                                   `c:\program files\foo\`,
		`C:\Program Files\WindowsApps\Claude_5.1.0.0_x64__abc\app\c.exe`: `c:\program files\windowsapps\claude_5.1.0.0_x64__abc\`,
		`C:\tool.exe`: `c:\`,
		// never climbs into a folder shared by many apps
		`C:\Program Files\1.2\x.exe`: `c:\program files\1.2\`,
	}
	for in, want := range cases {
		if got := AppRoot(in); got != want {
			t.Errorf("AppRoot(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestRetargetChildren(t *testing.T) {
	r := AppRule{Target: Direct(), Learned: []Learned{
		{Target: ToServer("a")}, // followed the app
		{Target: Direct()},      // user sent it direct
		{Target: ToServer("b")}, // user sent it elsewhere
	}}
	RetargetChildren(&r, ToServer("a"))
	if !r.Learned[0].Target.Equal(Direct()) || !r.Learned[1].Target.Equal(Direct()) || !r.Learned[2].Target.Equal(ToServer("b")) {
		t.Fatalf("%+v", r.Learned)
	}
}

func TestRepairChildren(t *testing.T) {
	p := DefaultProfile()
	p.Rules = []AppRule{{ID: "r", Target: Direct(), Learned: []Learned{
		{Match: Match{Kind: MatchPath, Value: `C:\A\1.0\x.exe`}, Target: ToServer("s"), Source: "inherit"}, // stale: the app is direct now
		{Match: Match{Kind: MatchPath, Value: `C:\A\2.0\x.exe`}, Target: ToServer("s"), Source: "inherit"}, // same family, newer
		{Match: Match{Kind: MatchPath, Value: `C:\A\y.exe`}, Target: ToServer("s"), Source: "user"},        // explicit: kept
	}}}
	if n := RepairChildren(&p); n != 3 {
		t.Fatalf("changed %d, want 3 (2 retargeted, 1 duplicate)", n)
	}
	l := p.Rules[0].Learned
	if len(l) != 2 || l[0].Match.Value != `C:\A\2.0\x.exe` || !l[0].Target.Equal(Direct()) || !l[1].Target.Equal(ToServer("s")) {
		t.Fatalf("%+v", l)
	}
}
