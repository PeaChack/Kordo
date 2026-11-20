package configgen

import (
	"testing"

	"github.com/PeaChack/Kordo/core/model"
)

func TestRouteCheck(t *testing.T) {
	servers := []model.Server{{ID: "a", Enabled: true}, {ID: "b", Enabled: false}}
	p := model.DefaultProfile()
	p.KillSwitch = model.KillVPNApps
	p.Rules = []model.AppRule{
		{ID: "1", Match: model.Match{Kind: model.MatchPath, Value: `C:\Opera\opera.exe`}, Target: model.ToServer("a"),
			Learned: []model.Learned{{Match: model.Match{Kind: model.MatchPath, Value: `C:\Opera\helper.exe`}, Target: model.ToServer("a")}}},
		{ID: "2", Match: model.Match{Kind: model.MatchName, Value: "helper.exe"}, Target: model.Direct()},      // name (t1) beats learned (t2)
		{ID: "3", Match: model.Match{Kind: model.MatchPath, Value: `C:\X\x.exe`}, Target: model.ToServer("b")}, // disabled + kill switch
	}
	rc := NewRouteCheck(p, servers)
	cases := map[string]string{
		`c:\opera\OPERA.exe`:  OutboundTag("a"),
		`C:\Opera\helper.exe`: DirectTag,
		`C:\X\x.exe`:          BlockTag,
		`C:\Other\y.exe`:      DirectTag,
	}
	for path, want := range cases {
		if got := rc.Tag(path); got != want {
			t.Errorf("%s: %s, want %s", path, got, want)
		}
	}
}
