//go:build windows

// Package killswitch blocks traffic with WFP filters in Kordo's own
// provider and sublayer. Kill-switch filters are persistent: they survive a
// crash of the service and a reboot, and are removed only by an explicit
// Clear. DNS-leak filters (used while connected without a kill switch) are
// not persistent, so a crash never leaves the machine without DNS.
//
// Model: while the engine runs, all routed traffic leaves the machine from the
// engine (inside the service process) or enters the TUN. So it is enough to
// permit the service executable, loopback, the TUN addresses, DHCP and
// (optionally) the LAN, and block the rest.
package killswitch

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"

	"github.com/tailscale/wf"
	"golang.org/x/sys/windows"
)

var (
	providerID = wf.ProviderID(mustGUID("{6c6e6f9a-6b0b-4b8e-9a5e-70f1c0a7e101}"))
	sublayerID = wf.SublayerID(mustGUID("{6c6e6f9a-6b0b-4b8e-9a5e-70f1c0a7e102}"))
)

func mustGUID(s string) windows.GUID {
	g, err := windows.GUIDFromString(s)
	if err != nil {
		panic(err)
	}
	return g
}

type Mode string

const (
	Off     Mode = "off"
	VPNApps Mode = "vpn_apps"
	All     Mode = "all"
)

// Policy is the complete desired filter state.
type Policy struct {
	Mode        Mode
	BlockDNS    bool     // block plain DNS/DoT on physical interfaces (except the engine)
	ServiceExe  string   // our service binary (the engine runs inside it)
	VPNApps     []string // exe paths of apps routed to servers (for vpn_apps)
	AllowLAN    bool
	TunPrefixes []netip.Prefix
}

type Firewall struct {
	mu  sync.Mutex
	s   *wf.Session
	log *slog.Logger
}

func Open(log *slog.Logger) (*Firewall, error) {
	s, err := wf.New(&wf.Options{Name: "Kordo", Description: "Kordo kill switch", Dynamic: false})
	if err != nil {
		return nil, fmt.Errorf("open WFP: %w", err)
	}
	if log == nil {
		log = slog.Default()
	}
	f := &Firewall{s: s, log: log}
	if err := f.ensureObjects(); err != nil {
		s.Close()
		return nil, err
	}
	return f, nil
}

func (f *Firewall) Close() error { return f.s.Close() }

func (f *Firewall) ensureObjects() error {
	provs, err := f.s.Providers()
	if err != nil {
		return err
	}
	found := false
	for _, p := range provs {
		if p.ID == providerID {
			found = true
		}
	}
	if !found {
		if err := f.s.AddProvider(&wf.Provider{ID: providerID, Name: "Kordo", Description: "Kordo VPN client", Persistent: true}); err != nil {
			return fmt.Errorf("add WFP provider: %w", err)
		}
	}
	subs, err := f.s.Sublayers(providerID)
	if err != nil {
		return err
	}
	found = false
	for _, sl := range subs {
		if sl.ID == sublayerID {
			found = true
		}
	}
	if !found {
		if err := f.s.AddSublayer(&wf.Sublayer{ID: sublayerID, Name: "Kordo", Description: "Kordo filters", Persistent: true, Provider: providerID, Weight: 0xFFFF}); err != nil {
			return fmt.Errorf("add WFP sublayer: %w", err)
		}
	}
	return nil
}

func (f *Firewall) ours() ([]*wf.Rule, error) {
	rules, err := f.s.Rules()
	if err != nil {
		return nil, err
	}
	var out []*wf.Rule
	for _, r := range rules {
		if r.Sublayer == sublayerID {
			out = append(out, r)
		}
	}
	return out, nil
}

var lanPrefixes = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"), netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("224.0.0.0/4"), netip.MustParsePrefix("255.255.255.255/32"),
	netip.MustParsePrefix("fe80::/10"), netip.MustParsePrefix("fc00::/7"), netip.MustParsePrefix("ff00::/8"),
}

type layerPair struct {
	layer wf.LayerID
	v6    bool
}

var connectLayers = []layerPair{
	{wf.LayerALEAuthConnectV4, false}, {wf.LayerALEAuthConnectV6, true},
	{wf.LayerALEAuthRecvAcceptV4, false}, {wf.LayerALEAuthRecvAcceptV6, true},
}

func newRuleID() wf.RuleID {
	g, _ := windows.GenerateGUID()
	return wf.RuleID(g)
}

// Apply replaces Kordo's filters with ones derived from p. New filters are
// added before old ones are removed, so there is never an unprotected gap.
func (f *Firewall) Apply(p Policy) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	old, err := f.ours()
	if err != nil {
		return err
	}
	rules, err := build(p)
	if err != nil {
		return err
	}
	var added []wf.RuleID
	for _, r := range rules {
		if err := f.s.AddRule(r); err != nil {
			for _, id := range added {
				_ = f.s.DeleteRule(id)
			}
			return fmt.Errorf("add filter %q: %w", r.Name, err)
		}
		added = append(added, r.ID)
	}
	var errs []error
	for _, r := range old {
		if err := f.s.DeleteRule(r.ID); err != nil {
			errs = append(errs, err)
		}
	}
	f.log.Info("firewall applied", "mode", p.Mode, "blockDNS", p.BlockDNS, "filters", len(rules), "removed", len(old))
	return errors.Join(errs...)
}

// Clear removes every Kordo filter (explicit user action or uninstall).
func (f *Firewall) Clear() error { return f.Apply(Policy{Mode: Off}) }

// Active reports whether persistent kill-switch filters are installed.
func (f *Firewall) Active() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	rules, err := f.ours()
	if err != nil {
		return false
	}
	for _, r := range rules {
		if r.Persistent && r.Action == wf.ActionBlock {
			return true
		}
	}
	return false
}

func build(p Policy) ([]*wf.Rule, error) {
	if p.Mode == Off && !p.BlockDNS {
		return nil, nil
	}
	persistent := p.Mode == All || p.Mode == VPNApps
	var rules []*wf.Rule
	add := func(name string, l layerPair, weight uint64, action wf.Action, conds ...*wf.Match) {
		rules = append(rules, &wf.Rule{
			ID: newRuleID(), Name: "Kordo: " + name, Layer: l.layer, Sublayer: sublayerID,
			Provider: providerID, Weight: weight, Action: action, Conditions: conds, Persistent: persistent,
		})
	}
	var svcApp string
	if p.ServiceExe != "" {
		id, err := wf.AppID(p.ServiceExe)
		if err != nil {
			return nil, fmt.Errorf("app id for service: %w", err)
		}
		svcApp = id
	}
	for _, l := range connectLayers {
		// permits (higher weight wins inside our sublayer)
		add("loopback", l, 1000, wf.ActionPermit, &wf.Match{Field: wf.FieldFlags, Op: wf.MatchTypeFlagsAllSet, Value: wf.ConditionFlagIsLoopback})
		if svcApp != "" {
			add("engine", l, 1000, wf.ActionPermit, &wf.Match{Field: wf.FieldALEAppID, Op: wf.MatchTypeEqual, Value: svcApp})
		}
		for _, tp := range p.TunPrefixes {
			if tp.Addr().Is6() == l.v6 {
				add("tun", l, 1000, wf.ActionPermit, &wf.Match{Field: wf.FieldIPLocalAddress, Op: wf.MatchTypeEqual, Value: tp})
			}
		}
		if p.Mode == All {
			if !l.v6 {
				add("dhcp", l, 900, wf.ActionPermit,
					&wf.Match{Field: wf.FieldIPProtocol, Op: wf.MatchTypeEqual, Value: wf.IPProtoUDP},
					&wf.Match{Field: wf.FieldIPRemotePort, Op: wf.MatchTypeEqual, Value: uint16(67)})
			} else {
				add("dhcpv6", l, 900, wf.ActionPermit,
					&wf.Match{Field: wf.FieldIPProtocol, Op: wf.MatchTypeEqual, Value: wf.IPProtoUDP},
					&wf.Match{Field: wf.FieldIPRemotePort, Op: wf.MatchTypeEqual, Value: uint16(547)})
				add("icmpv6", l, 900, wf.ActionPermit,
					&wf.Match{Field: wf.FieldIPProtocol, Op: wf.MatchTypeEqual, Value: wf.IPProtoICMPV6})
			}
		}
		// DNS leak protection: plain DNS and DoT only via the engine/TUN.
		if p.BlockDNS || p.Mode != Off {
			for _, port := range []uint16{53, 853} {
				add(fmt.Sprintf("dns-%d", port), l, 800, wf.ActionBlock,
					&wf.Match{Field: wf.FieldIPRemotePort, Op: wf.MatchTypeEqual, Value: port})
			}
		}
		if p.AllowLAN && p.Mode != Off {
			for _, lp := range lanPrefixes {
				if lp.Addr().Is6() == l.v6 {
					add("lan", l, 700, wf.ActionPermit, &wf.Match{Field: wf.FieldIPRemoteAddress, Op: wf.MatchTypeEqual, Value: lp})
				}
			}
		}
		switch p.Mode {
		case All:
			add("block-all", l, 10, wf.ActionBlock)
		case VPNApps:
			for _, exe := range p.VPNApps {
				id, err := wf.AppID(exe)
				if err != nil {
					continue // app not installed (any more): nothing to block
				}
				add("block-app", l, 10, wf.ActionBlock, &wf.Match{Field: wf.FieldALEAppID, Op: wf.MatchTypeEqual, Value: id})
			}
		}
	}
	return rules, nil
}

// TunPrefixes returns the prefixes of the TUN addresses (see configgen).
func TunPrefixes(addrs ...string) []netip.Prefix {
	var out []netip.Prefix
	for _, a := range addrs {
		if p, err := netip.ParsePrefix(a); err == nil {
			out = append(out, p.Masked())
		}
	}
	return out
}
