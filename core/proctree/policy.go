// Package proctree decides what to do with child processes of routed apps.
// The platform (ETW or snapshot polling on Windows) reports process starts;
// this package maps them onto the child policies of the profile.
package proctree

import (
	"regexp"
	"strings"
	"sync"

	"github.com/portico-vpn/portico/core/configgen"
	"github.com/portico-vpn/portico/core/model"
)

type Action string

const (
	ActNone  Action = "none"
	ActLearn Action = "learn" // add child to rule.Learned
	ActAsk   Action = "ask"   // ask the user
)

type Decision struct {
	Action Action
	RuleID string
	Child  model.Match
	Target model.Target // for learn: where the child goes
	Shared bool         // child is a shared executable (cmd, node, ...)
}

type Matcher struct {
	mu    sync.Mutex
	cache map[string]*regexp.Regexp
}

func NewMatcher() *Matcher { return &Matcher{cache: map[string]*regexp.Regexp{}} }

func (m *Matcher) matches(match model.Match, path string) bool {
	if match.Kind == model.MatchAndroidPackage {
		return false
	}
	expr := configgen.MatchRegex(match)
	if expr == "" {
		return false
	}
	m.mu.Lock()
	re, ok := m.cache[expr]
	if !ok {
		re = regexp.MustCompile(expr)
		m.cache[expr] = re
	}
	m.mu.Unlock()
	return re.MatchString(path)
}

// RuleFor returns the rule that owns a process path, either directly or via a
// learned child, and whether it matched directly.
func (m *Matcher) RuleFor(p model.Profile, path string) (*model.AppRule, bool) {
	for i := range p.Rules {
		if m.matches(p.Rules[i].Match, path) {
			return &p.Rules[i], true
		}
	}
	for i := range p.Rules {
		for _, l := range p.Rules[i].Learned {
			if m.matches(l.Match, path) {
				return &p.Rules[i], false
			}
		}
	}
	return nil, false
}

func (m *Matcher) known(p model.Profile, path string) bool {
	if r, _ := m.RuleFor(p, path); r != nil {
		return true
	}
	for _, a := range p.ChildAnswers {
		if m.matches(a.Match, path) {
			return true
		}
	}
	return false
}

func exe(path string) string {
	path = strings.ReplaceAll(path, "/", `\`)
	if i := strings.LastIndexByte(path, '\\'); i >= 0 {
		path = path[i+1:]
	}
	return strings.ToLower(path)
}

// Decide applies the parent's child policy to a newly started child.
func (m *Matcher) Decide(p model.Profile, parentPath, childPath string) Decision {
	none := Decision{Action: ActNone}
	if parentPath == "" || childPath == "" {
		return none
	}
	rule, _ := m.RuleFor(p, parentPath)
	if rule == nil {
		return none
	}
	if m.known(p, childPath) {
		return none // already routed explicitly or learned before
	}
	child := model.MatchForPath(childPath)
	if model.IsForbidden(child) {
		return none
	}
	shared := model.SharedExes[exe(childPath)]
	d := Decision{RuleID: rule.ID, Child: child, Shared: shared}
	switch rule.Children {
	case model.ChildInherit, "":
		if shared {
			d.Action = ActAsk
			return d
		}
		d.Action, d.Target = ActLearn, rule.Target
	case model.ChildDirect:
		if shared || p.DefaultTarget.Kind == model.TargetDirect {
			return none // unmatched children already go direct
		}
		d.Action, d.Target = ActLearn, model.Direct()
	case model.ChildAsk:
		d.Action = ActAsk
	default:
		return none
	}
	return d
}
