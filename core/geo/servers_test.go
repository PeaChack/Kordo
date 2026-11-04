package geo

import (
	"testing"

	"github.com/PeaChack/Kordo/core/model"
)

func TestMainServer(t *testing.T) {
	servers := []model.Server{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	p := model.DefaultProfile()
	if got := MainServer(p, servers, ""); got != "a" {
		t.Fatalf("no rules: %q", got)
	}
	p.Rules = []model.AppRule{{Target: model.ToServer("b")}, {Target: model.ToServer("b")}, {Target: model.ToServer("c")}}
	if got := MainServer(p, servers, ""); got != "b" {
		t.Fatalf("most used: %q", got)
	}
	p.DefaultTarget = model.ToServer("c")
	if got := MainServer(p, servers, ""); got != "c" {
		t.Fatalf("everything through: %q", got)
	}
	if got := MainServer(p, servers, "a"); got != "a" {
		t.Fatalf("preferred: %q", got)
	}
	if got := MainServer(p, servers, "gone"); got != "c" {
		t.Fatalf("missing preferred: %q", got)
	}
	if MainServer(p, nil, "") != "c" || MainServer(model.DefaultProfile(), nil, "") != "" {
		t.Fatal("no servers")
	}
}
