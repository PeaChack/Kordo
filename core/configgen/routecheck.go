package configgen

import (
	"regexp"
	"sort"
	"sync"

	"github.com/PeaChack/Kordo/core/model"
)

// RouteCheck answers "where should a connection of this process go" with the
// same precedence the generated configuration gives the engine: tiers t0
// (path/package), t1 (name), t2 (learned children and answers); within a
// tier block, then direct, then servers; finally the default target.
// The service compares it with where live connections actually went.
type RouteCheck struct {
	steps []routeStep
	final string
}

type routeStep struct {
	re  *regexp.Regexp
	tag string
}

// Tags returned by RouteCheck besides server outbound tags.
const BlockTag = "block"

var reCache sync.Map // expr -> *regexp.Regexp

func compile(expr string) *regexp.Regexp {
	if v, ok := reCache.Load(expr); ok {
		return v.(*regexp.Regexp)
	}
	re := regexp.MustCompile(expr)
	reCache.Store(expr, re)
	return re
}

func NewRouteCheck(p model.Profile, servers []model.Server) *RouteCheck {
	active := map[string]bool{}
	for _, s := range servers {
		if s.Enabled {
			active[s.ID] = true
		}
	}
	resolve := func(t model.Target) string {
		if t.IsServer() && !active[t.ServerID] {
			if p.KillSwitch == model.KillOff || p.KillSwitch == "" {
				return DirectTag
			}
			return BlockTag
		}
		switch t.Kind {
		case model.TargetServer:
			return OutboundTag(t.ServerID)
		case model.TargetBlock:
			return BlockTag
		}
		return DirectTag
	}
	type item struct {
		tier int
		tag  string
		expr string
	}
	var items []item
	add := func(tier int, t model.Target, m model.Match) {
		if expr := MatchRegex(m); expr != "" {
			items = append(items, item{tier, resolve(t), expr})
		}
	}
	for _, r := range p.Rules {
		tier := 0
		if r.Match.Kind == model.MatchName {
			tier = 1
		}
		add(tier, r.Target, r.Match)
		for _, l := range r.Learned {
			add(2, l.Target, l.Match)
		}
	}
	for _, a := range p.ChildAnswers {
		add(2, a.Target, a.Match)
	}
	rank := func(tag string) int {
		switch tag {
		case BlockTag:
			return 0
		case DirectTag:
			return 1
		}
		return 2
	}
	rc := &RouteCheck{final: resolve(p.DefaultTarget)}
	// same order as the generated route rules: tier, then block, direct and
	// servers sorted by id (outbound tags share the "srv-" prefix)
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.tier != b.tier {
			return a.tier < b.tier
		}
		if rank(a.tag) != rank(b.tag) {
			return rank(a.tag) < rank(b.tag)
		}
		return a.tag < b.tag
	})
	for _, it := range items {
		rc.steps = append(rc.steps, routeStep{compile(it.expr), it.tag})
	}
	return rc
}

// Tag returns DirectTag, BlockTag or a server outbound tag for a process path.
func (rc *RouteCheck) Tag(processPath string) string {
	for _, s := range rc.steps {
		if s.re.MatchString(processPath) {
			return s.tag
		}
	}
	return rc.final
}
