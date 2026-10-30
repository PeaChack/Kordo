//go:build windows

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/portico-vpn/portico/core/configgen"
	"github.com/portico-vpn/portico/core/model"
	"github.com/portico-vpn/portico/core/providers"
	"github.com/portico-vpn/portico/core/rpcapi"
	"github.com/portico-vpn/portico/core/supervisor"
	"github.com/portico-vpn/portico/engine"
	"github.com/portico-vpn/portico/windows/killswitch"
	"github.com/portico-vpn/portico/windows/procs"
)

func decode[T any](params json.RawMessage) (T, error) {
	var v T
	if len(params) == 0 {
		return v, nil
	}
	if err := json.Unmarshal(params, &v); err != nil {
		return v, &rpcapi.Error{Code: -32602, Message: "invalid params: " + err.Error()}
	}
	return v, nil
}

func (s *Service) handle(ctx context.Context, c *rpcapi.Conn, method string, params json.RawMessage) (any, error) {
	switch method {
	case rpcapi.MHello:
		return rpcapi.Hello{ProtocolVersion: rpcapi.ProtocolVersion, Version: Version, EngineVersion: EngineVersion}, nil

	case rpcapi.MProvidersList:
		var out []rpcapi.ProviderInfo
		for _, p := range providers.All() {
			out = append(out, rpcapi.ProviderInfo{ID: p.ID(), DisplayName: p.DisplayName(), Schemes: p.URISchemes(), Schema: p.ConfigSchema()})
		}
		return out, nil

	case rpcapi.MServersList:
		_, servers := s.store.Snapshot()
		return servers, nil

	case rpcapi.MServersAdd:
		req, err := decode[rpcapi.ServerAdd](params)
		if err != nil {
			return nil, err
		}
		return s.addServer(req)

	case rpcapi.MServersUpdate:
		srv, err := decode[model.Server](params)
		if err != nil {
			return nil, err
		}
		return s.updateServer(srv)

	case rpcapi.MServersRemove:
		req, err := decode[rpcapi.IDParam](params)
		if err != nil {
			return nil, err
		}
		err = s.store.Update(func(p *model.Profile, servers *[]model.Server) error {
			out := (*servers)[:0]
			for _, srv := range *servers {
				if srv.ID != req.ID {
					out = append(out, srv)
				}
			}
			*servers = out
			// rules pointing to the removed server go direct; drop them entirely
			rules := p.Rules[:0]
			for _, r := range p.Rules {
				if !(r.Target.Kind == model.TargetServer && r.Target.ServerID == req.ID) {
					rules = append(rules, r)
				}
			}
			p.Rules = rules
			if p.DefaultTarget.ServerID == req.ID {
				p.DefaultTarget = model.Direct()
			}
			if p.DNS.ServerID == req.ID {
				p.DNS.ServerID = ""
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		s.afterChange()
		return true, nil

	case rpcapi.MServersTest:
		req, err := decode[rpcapi.IDParam](params)
		if err != nil {
			return nil, err
		}
		return s.testServer(ctx, req.ID)

	case rpcapi.MRulesList:
		prof, _ := s.store.Snapshot()
		return prof.Rules, nil

	case rpcapi.MRulesUpsert:
		r, err := decode[model.AppRule](params)
		if err != nil {
			return nil, err
		}
		return s.upsertRule(r)

	case rpcapi.MRulesRemove:
		req, err := decode[rpcapi.IDParam](params)
		if err != nil {
			return nil, err
		}
		err = s.store.Update(func(p *model.Profile, _ *[]model.Server) error {
			rules := p.Rules[:0]
			for _, r := range p.Rules {
				if r.ID != req.ID {
					rules = append(rules, r)
				}
			}
			p.Rules = rules
			return nil
		})
		if err != nil {
			return nil, err
		}
		s.afterChange()
		return true, nil

	case rpcapi.MProfileGet:
		prof, _ := s.store.Snapshot()
		return prof, nil

	case rpcapi.MProfileSet:
		req, err := decode[rpcapi.ProfileSet](params)
		if err != nil {
			return nil, err
		}
		err = s.store.Update(func(p *model.Profile, _ *[]model.Server) error {
			rules, answers := p.Rules, p.ChildAnswers
			*p = req.Profile
			p.Rules, p.ChildAnswers = rules, answers
			return nil
		})
		if err != nil {
			return nil, err
		}
		s.afterChange()
		return true, nil

	case rpcapi.MAppsRunning:
		return s.runningApps(c)

	case rpcapi.MAppsInstalled:
		var out []rpcapi.InstalledApp
		for _, a := range procs.InstalledApps() {
			m := model.MatchForPath(a.Path)
			ia := rpcapi.InstalledApp{Name: a.Name, Path: a.Path, Publisher: a.Publisher}
			if m.Kind == model.MatchStorePackage {
				ia.Store = m.Value
			}
			out = append(out, ia)
		}
		return out, nil

	case rpcapi.MEngineConnect:
		if err := s.sup.Connect(); err != nil {
			return nil, err
		}
		return s.sup.Status(), nil

	case rpcapi.MEngineDisconn:
		s.sup.Disconnect()
		s.stopWatcher()
		s.setConnectedFlag(false)
		s.applyFirewall(false, nil)
		return s.sup.Status(), nil

	case rpcapi.MEngineStatus:
		return s.statusWithFirewall(), nil

	case rpcapi.MKillSwitchSet:
		req, err := decode[rpcapi.KillSwitchSet](params)
		if err != nil {
			return nil, err
		}
		switch req.Mode {
		case model.KillOff, model.KillVPNApps, model.KillAll:
		default:
			return nil, fmt.Errorf("unknown kill switch mode %q", req.Mode)
		}
		err = s.store.Update(func(p *model.Profile, _ *[]model.Server) error {
			p.KillSwitch = req.Mode
			return nil
		})
		if err != nil {
			return nil, err
		}
		s.applyFirewall(s.sup.Engine() != nil, nil)
		s.rpc.Notify(rpcapi.NDataChanged, map[string]any{})
		return s.statusWithFirewall(), nil

	case rpcapi.MKillSwitchClear:
		if s.fw == nil {
			return nil, errors.New("kill switch is unavailable")
		}
		if err := s.fw.Clear(); err != nil {
			return nil, err
		}
		return true, nil

	case rpcapi.MLogsTail:
		req, err := decode[rpcapi.LogsTail](params)
		if err != nil {
			return nil, err
		}
		if req.Lines <= 0 || req.Lines > 5000 {
			req.Lines = 300
		}
		name := "service.log"
		if req.File == "core" {
			name = "core.log"
		}
		text, err := tail(filepath.Join(s.logDir, name), req.Lines)
		if err != nil {
			return "", nil
		}
		return text, nil

	case rpcapi.MChildrenAnswer:
		req, err := decode[rpcapi.ChildAnswer](params)
		if err != nil {
			return nil, err
		}
		return s.answerChild(req)

	case rpcapi.MActivity:
		return s.activity(ctx)

	case rpcapi.MDiagnostics:
		return s.diagnostics(), nil
	}
	return nil, &rpcapi.Error{Code: -32601, Message: "method not found: " + method}
}

type statusOut struct {
	supervisor.Status
	KillSwitch        model.KillMode    `json:"killSwitch"`
	FirewallActive    bool              `json:"firewallActive"`
	FirewallAvailable bool              `json:"firewallAvailable"`
	PendingAsks       []rpcapi.ChildAsk `json:"pendingAsks"`
}

func (s *Service) statusWithFirewall() statusOut {
	prof, _ := s.store.Snapshot()
	out := statusOut{Status: s.sup.Status(), KillSwitch: prof.KillSwitch, FirewallAvailable: s.fw != nil, PendingAsks: []rpcapi.ChildAsk{}}
	if s.fw != nil {
		out.FirewallActive = s.fw.Active()
	}
	s.mu.Lock()
	for _, a := range s.asks {
		out.PendingAsks = append(out.PendingAsks, a.ChildAsk)
	}
	s.mu.Unlock()
	sort.Slice(out.PendingAsks, func(i, j int) bool { return out.PendingAsks[i].Time.Before(out.PendingAsks[j].Time) })
	return out
}

func (s *Service) addServer(req rpcapi.ServerAdd) (model.Server, error) {
	srv := model.Server{ID: newID(), Enabled: true}
	if strings.TrimSpace(req.Link) != "" {
		pid, params, name, err := providers.ParseAny(req.Link)
		if err != nil {
			return srv, err
		}
		srv.ProviderID, srv.Params, srv.Name = pid, params, name
	} else {
		p, err := providers.Get(req.Provider)
		if err != nil {
			return srv, err
		}
		if err := p.Validate(req.Params); err != nil {
			return srv, err
		}
		srv.ProviderID, srv.Params = req.Provider, req.Params
	}
	if req.Name != "" {
		srv.Name = req.Name
	}
	if srv.Name == "" {
		srv.Name = srv.ProviderID
	}
	err := s.store.Update(func(_ *model.Profile, servers *[]model.Server) error {
		*servers = append(*servers, srv)
		return nil
	})
	if err != nil {
		return srv, err
	}
	s.afterChange()
	return srv, nil
}

func (s *Service) updateServer(in model.Server) (model.Server, error) {
	p, err := providers.Get(in.ProviderID)
	if err != nil {
		return in, err
	}
	if err := p.Validate(in.Params); err != nil {
		return in, err
	}
	err = s.store.Update(func(_ *model.Profile, servers *[]model.Server) error {
		for i := range *servers {
			if (*servers)[i].ID == in.ID {
				(*servers)[i] = in
				return nil
			}
		}
		return fmt.Errorf("server %s not found", in.ID)
	})
	if err != nil {
		return in, err
	}
	s.afterChange()
	return in, nil
}

func (s *Service) upsertRule(r model.AppRule) (model.AppRule, error) {
	r.Match = r.Match.Normalize()
	if r.Match.Kind == model.MatchPath {
		r.Match = model.MatchForPath(r.Match.Value) // store apps -> package match
	}
	if r.Children == "" {
		r.Children = model.ChildInherit
	}
	err := s.store.Update(func(p *model.Profile, _ *[]model.Server) error {
		if r.ID == "" {
			r.ID = newID()
			r.Priority = len(p.Rules)
			p.Rules = append(p.Rules, r)
			return nil
		}
		for i := range p.Rules {
			if p.Rules[i].ID == r.ID {
				if r.Learned == nil {
					r.Learned = p.Rules[i].Learned
				}
				p.Rules[i] = r
				return nil
			}
		}
		return fmt.Errorf("rule %s not found", r.ID)
	})
	if err != nil {
		return r, err
	}
	s.afterChange()
	return r, nil
}

func (s *Service) answerChild(req rpcapi.ChildAnswer) (bool, error) {
	s.mu.Lock()
	ask, ok := s.asks[req.AskID]
	delete(s.asks, req.AskID)
	s.mu.Unlock()
	if !ok {
		return false, errors.New("this question has expired")
	}
	prof, _ := s.store.Snapshot()
	var target model.Target
	switch req.Choice {
	case "parent":
		for _, r := range prof.Rules {
			if r.ID == ask.RuleID {
				target = r.Target
			}
		}
		if target.Kind == "" {
			return false, errors.New("parent rule no longer exists")
		}
		s.learn(ask.RuleID, model.Learned{Match: ask.Child, Target: target, Source: "user"})
	case "direct":
		if !req.Remember {
			return true, nil
		}
		err := s.store.Update(func(p *model.Profile, _ *[]model.Server) error {
			p.ChildAnswers = append(p.ChildAnswers, model.Learned{Match: ask.Child, Target: model.Direct(), Source: "user"})
			return nil
		})
		if err != nil {
			return false, err
		}
		s.afterChange()
	default:
		return false, fmt.Errorf("unknown choice %q", req.Choice)
	}
	return true, nil
}

func (s *Service) runningApps(c *rpcapi.Conn) ([]rpcapi.RunningApp, error) {
	list, err := procs.Snapshot(true)
	if err != nil {
		return nil, err
	}
	session, _ := c.Info.(uint32)
	seen := map[string]bool{}
	var out []rpcapi.RunningApp
	for _, p := range list {
		if p.Path == "" {
			continue
		}
		// show the caller's session plus services that hold network sockets are not useful: only interactive sessions
		if p.Session == 0 || (session != 0 && p.Session != session) {
			continue
		}
		key := strings.ToLower(p.Path)
		if seen[key] {
			continue
		}
		seen[key] = true
		m := model.MatchForPath(p.Path)
		ra := rpcapi.RunningApp{PID: p.PID, PPID: p.PPID, Name: p.Name, Path: p.Path, Session: p.Session, System: model.IsForbidden(m)}
		if m.Kind == model.MatchStorePackage {
			ra.Store = m.Value
		}
		low := strings.ToLower(p.Path)
		if strings.HasPrefix(low, `c:\windows\system32\`) && !ra.System {
			// keep system tools selectable but sort them last
			ra.System = model.IsForbidden(model.Match{Kind: model.MatchName, Value: p.Name})
		}
		out = append(out, ra)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

// ---------------------------------------------------------------- server test

func (s *Service) testServer(ctx context.Context, id string) (rpcapi.TestResult, error) {
	srv, ok := s.store.Server(id)
	res := rpcapi.TestResult{ID: id}
	if !ok {
		return res, fmt.Errorf("server %s not found", id)
	}
	p, err := providers.Get(srv.ProviderID)
	if err != nil {
		return res, err
	}
	// 1) TCP reachability (not meaningful for UDP-only WireGuard)
	host, port := serverHostPort(srv)
	if srv.ProviderID != "wireguard" && host != "" {
		start := time.Now()
		d := net.Dialer{Timeout: 5 * time.Second}
		conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			res.TCPError = err.Error()
		} else {
			res.TCPms = time.Since(start).Milliseconds()
			conn.Close()
		}
	}
	// 2) HTTP 204 through the outbound
	tctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if eng, ok := s.sup.Engine().(*engine.Instance); ok && eng != nil && srv.Enabled {
		res.Via = "running engine"
		rtt, err := eng.URLTest(tctx, configgen.OutboundTag(id), srv.EffectiveHealthURL())
		if err != nil {
			res.HTTPError = err.Error()
		} else {
			res.HTTPms = rtt.Milliseconds()
		}
		return res, nil
	}
	if s.sup.Engine() != nil {
		// TUN is up but this server is not part of it: a temporary engine
		// would route its own probe into the TUN, so only TCP is reported.
		res.Via = "tcp only"
		return res, nil
	}
	res.Via = "temporary engine"
	spec, err := p.ToOutbound("probe", srv.Params)
	if err != nil {
		return res, err
	}
	cfg := map[string]any{
		"log":       map[string]any{"disabled": true},
		"dns":       map[string]any{"servers": []any{map[string]any{"type": "local", "tag": "local"}}},
		"outbounds": []any{map[string]any{"type": "direct", "tag": "direct"}},
		"route":     map[string]any{"default_domain_resolver": "local", "final": "direct"},
	}
	if spec.Kind == "endpoint" {
		cfg["endpoints"] = []any{spec.JSON}
	} else {
		cfg["outbounds"] = append(cfg["outbounds"].([]any), spec.JSON)
	}
	b, _ := json.Marshal(cfg)
	inst, err := engine.Start(b, engine.Options{})
	if err != nil {
		res.HTTPError = err.Error()
		return res, nil
	}
	defer inst.Close()
	rtt, err := inst.URLTest(tctx, "probe", srv.EffectiveHealthURL())
	if err != nil {
		res.HTTPError = err.Error()
	} else {
		res.HTTPms = rtt.Milliseconds()
	}
	return res, nil
}

func serverHostPort(srv model.Server) (string, int) {
	var v struct {
		Server   string `json:"server"`
		Port     int    `json:"port"`
		Endpoint string `json:"endpoint"`
	}
	_ = json.Unmarshal(srv.Params, &v)
	if v.Endpoint != "" {
		h, p, _ := net.SplitHostPort(v.Endpoint)
		n, _ := strconv.Atoi(p)
		return h, n
	}
	return v.Server, v.Port
}

// ---------------------------------------------------------------- activity

func (s *Service) activity(ctx context.Context) ([]rpcapi.ActivityEntry, error) {
	eng, ok := s.sup.Engine().(*engine.Instance)
	if !ok || eng == nil {
		return []rpcapi.ActivityEntry{}, nil
	}
	conns, err := eng.Connections(ctx)
	if err != nil {
		return nil, err
	}
	_, servers := s.store.Snapshot()
	names := map[string]string{}
	for _, srv := range servers {
		names[configgen.OutboundTag(srv.ID)] = srv.Name
	}
	type key struct{ app, target string }
	agg := map[key]*rpcapi.ActivityEntry{}
	for _, c := range conns {
		target := "direct"
		if len(c.Chains) > 0 {
			target = c.Chains[0]
		}
		app := c.Metadata.ProcessPath
		if app == "" {
			app = "(неизвестно)"
		}
		k := key{app, target}
		e := agg[k]
		if e == nil {
			e = &rpcapi.ActivityEntry{App: app, Target: target}
			if n, ok := names[target]; ok {
				e.Target = n
				e.TargetID = strings.TrimPrefix(target, "srv-")
			}
			agg[k] = e
		}
		e.Conns++
		e.Upload += c.Upload
		e.Download += c.Download
		host := c.Metadata.Host
		if host == "" {
			host = c.Metadata.DstIP
		}
		if host != "" && len(e.Hosts) < 8 && !contains(e.Hosts, host) {
			e.Hosts = append(e.Hosts, host)
		}
	}
	out := make([]rpcapi.ActivityEntry, 0, len(agg))
	for _, e := range agg {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Download+out[i].Upload > out[j].Download+out[j].Upload })
	return out, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func (s *Service) diagnostics() map[string]any {
	svcLog, _ := tail(filepath.Join(s.logDir, "service.log"), 500)
	coreLog, _ := tail(filepath.Join(s.logDir, "core.log"), 500)
	prof, servers := s.store.Snapshot()
	var srvSummary []map[string]any
	for _, srv := range servers {
		srvSummary = append(srvSummary, map[string]any{"id": srv.ID, "name": srv.Name, "provider": srv.ProviderID, "enabled": srv.Enabled})
	}
	prof.Rules = nil
	return map[string]any{
		"version": Version, "engine": EngineVersion, "status": s.statusWithFirewall(),
		"profile": prof, "servers": srvSummary, "serviceLog": svcLog, "coreLog": coreLog,
		"firewall": s.fw != nil, "killswitchModes": []killswitch.Mode{killswitch.Off, killswitch.VPNApps, killswitch.All},
	}
}
