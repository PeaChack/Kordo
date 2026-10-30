// Package configgen turns a Kordo profile into a sing-box configuration plus
// a set of local rule-set files. The output is deterministic so it can be
// golden-tested and compared: if only rule-set files differ between two
// generations, the running engine picks them up without a restart (sing-box
// reloads local rule-sets on file change since 1.10.0).
package configgen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strings"

	"github.com/PeaChack/Kordo/core/model"
	"github.com/PeaChack/Kordo/core/providers"
)

const (
	TunTag      = "tun-in"
	DirectTag   = "direct"
	WGDirectTag = "wg-direct"
	TunName     = "Kordo"
	TunAddr4    = "172.19.0.1/30"
	TunAddr6    = "fdfe:dcba:9876::1/126"
	SentinelExe = "__kordo_sentinel__.exe"
	DefaultDoH  = "1.1.1.1"
)

// Tiers order rule-sets by specificity: explicit path/package rules win over
// explicit name rules, which win over auto-learned children.
var tiers = []string{"t0", "t1", "t2"}

// Env carries machine-specific facts. Everything that is not deterministic
// (DNS lookups, interface state) is resolved by the caller and passed here.
type Env struct {
	RulesDir    string                  // absolute directory where rule-set files are written
	LogPath     string                  // sing-box log file; empty = stderr
	LogLevel    string                  // default "info"
	HasIPv6     bool                    // the uplink has a global IPv6 address
	Resolved    map[string][]netip.Addr // server domain -> addresses (to exclude from TUN)
	ClashAPI    string                  // "127.0.0.1:port"; empty = disabled
	ClashSecret string
	CacheFile   string // optional sing-box cache file
}

type Output struct {
	Config   []byte            // config.json
	RuleSets map[string][]byte // file name (relative to Env.RulesDir) -> content
	// OutboundTags maps server ID -> sing-box tag (for health checks).
	OutboundTags map[string]string
	// ServerPrefixes are the remote server addresses (for the kill switch).
	ServerPrefixes []netip.Prefix
}

func OutboundTag(serverID string) string { return "srv-" + serverID }

func bucketOf(t model.Target) string {
	switch t.Kind {
	case model.TargetServer:
		return t.ServerID
	case model.TargetBlock:
		return "block"
	}
	return "direct"
}

func ruleSetTag(bucket, tier string) string  { return "apps-" + bucket + "-" + tier }
func ruleSetFile(bucket, tier string) string { return ruleSetTag(bucket, tier) + ".json" }

// Generate builds the configuration. Disabled servers are left out; rules
// pointing at them fall back to the kill-switch semantics: with KillSwitch
// != off their traffic is blocked, otherwise it goes direct.
func Generate(p model.Profile, servers []model.Server, env Env) (*Output, error) {
	if err := p.Validate(servers); err != nil {
		return nil, err
	}
	if env.RulesDir == "" {
		return nil, fmt.Errorf("configgen: RulesDir is required")
	}
	active := make([]model.Server, 0, len(servers))
	activeByID := map[string]bool{}
	for _, s := range servers {
		if s.Enabled {
			active = append(active, s)
			activeByID[s.ID] = true
		}
	}
	sort.Slice(active, func(i, j int) bool { return active[i].ID < active[j].ID })

	out := &Output{RuleSets: map[string][]byte{}, OutboundTags: map[string]string{}}

	// resolveTarget maps targets on disabled servers to a safe fallback.
	resolveTarget := func(t model.Target) model.Target {
		if t.IsServer() && !activeByID[t.ServerID] {
			if p.KillSwitch == model.KillOff || p.KillSwitch == "" {
				return model.Direct()
			}
			return model.Target{Kind: model.TargetBlock}
		}
		return t
	}

	// --- outbounds / endpoints
	outbounds := []any{map[string]any{"type": "direct", "tag": DirectTag}}
	var endpoints []any
	var excludes []netip.Prefix
	for _, s := range active {
		prov, err := providers.Get(s.ProviderID)
		if err != nil {
			return nil, fmt.Errorf("server %s: %w", s.Name, err)
		}
		tag := OutboundTag(s.ID)
		spec, err := prov.ToOutbound(tag, s.Params)
		if err != nil {
			return nil, fmt.Errorf("server %s: %w", s.Name, err)
		}
		if spec.Kind == "endpoint" {
			// Without a detour, sing-box gives WireGuard a dual-stack bind that
			// is pinned to the default interface; on adapters without IPv6 the
			// IPv6 half fails and the whole bind dies after the TUN comes up.
			// A detour switches it to a client bind that dials the peer over udp4.
			spec.JSON["detour"] = WGDirectTag
			endpoints = append(endpoints, spec.JSON)
		} else {
			outbounds = append(outbounds, spec.JSON)
		}
		out.OutboundTags[s.ID] = tag
		ips, domains, err := prov.ServerAddresses(s.Params)
		if err != nil {
			return nil, fmt.Errorf("server %s: %w", s.Name, err)
		}
		excludes = append(excludes, ips...)
		for _, d := range domains {
			for _, a := range env.Resolved[d] {
				excludes = append(excludes, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
			}
		}
	}
	excludes = dedupPrefixes(excludes)
	out.ServerPrefixes = excludes

	// --- rule-sets: bucket x tier -> process matchers
	type bucketItems struct{ regex, pkgs []string }
	items := map[string]*bucketItems{}
	add := func(bucket, tier string, m model.Match) {
		k := bucket + "/" + tier
		if items[k] == nil {
			items[k] = &bucketItems{}
		}
		b := items[k]
		switch m.Kind {
		case model.MatchAndroidPackage:
			b.pkgs = append(b.pkgs, m.Value)
		default:
			if re := MatchRegex(m); re != "" {
				b.regex = append(b.regex, re)
			}
		}
	}
	for _, r := range p.Rules {
		m := r.Match.Normalize()
		tier := "t0"
		if m.Kind == model.MatchName {
			tier = "t1"
		}
		add(bucketOf(resolveTarget(r.Target)), tier, m)
		for _, l := range r.Learned {
			add(bucketOf(resolveTarget(l.Target)), "t2", l.Match.Normalize())
		}
	}
	for _, l := range p.ChildAnswers {
		add(bucketOf(resolveTarget(l.Target)), "t2", l.Match.Normalize())
	}

	buckets := []string{"block", "direct"}
	for _, s := range active {
		buckets = append(buckets, s.ID)
	}
	var ruleSetDefs []any
	var routeRules []any
	// DNS follows the app's route (see the DNS section): rule-set tag and
	// whether the bucket goes through a server, in routing order.
	type dnsRoute struct {
		tag    string
		viaVPN bool
	}
	var dnsRoutes []dnsRoute
	routeRules = append(routeRules,
		// TCP is not sniffed: sniffing waits for the client's first bytes
		// (TLS ClientHello) before dialing, adding a full round trip to every
		// new connection. Routing is by process, so domains are not needed;
		// UDP sniffing is free (the first packet is already here).
		map[string]any{"network": "udp", "action": "sniff"},
		map[string]any{"protocol": "dns", "action": "hijack-dns"},
		map[string]any{"port": 53, "action": "hijack-dns"},
		// Workaround for sing-box #4341: keep a process rule present from the
		// start so that process rules added to rule-sets later are honoured.
		map[string]any{"process_name": []string{SentinelExe}, "action": "reject"},
	)
	if !env.HasIPv6 || p.IPv6 == "block" {
		routeRules = append(routeRules, map[string]any{"ip_version": 6, "action": "reject"})
	}
	if p.AllowLAN {
		routeRules = append(routeRules, map[string]any{"ip_is_private": true, "outbound": DirectTag})
	}
	for _, tier := range tiers {
		for _, b := range buckets {
			bi := items[b+"/"+tier]
			var rules []any
			if bi != nil {
				if len(bi.regex) > 0 {
					rules = append(rules, map[string]any{"process_path_regex": sortedUnique(bi.regex)})
				}
				if len(bi.pkgs) > 0 {
					rules = append(rules, map[string]any{"package_name": sortedUnique(bi.pkgs)})
				}
			}
			if rules == nil {
				rules = []any{}
			}
			content, err := marshal(map[string]any{"version": 3, "rules": rules})
			if err != nil {
				return nil, err
			}
			file := ruleSetFile(b, tier)
			out.RuleSets[file] = content
			tag := ruleSetTag(b, tier)
			ruleSetDefs = append(ruleSetDefs, map[string]any{
				"type":   "local",
				"tag":    tag,
				"format": "source",
				"path":   joinDir(env.RulesDir, file),
			})
			rr := map[string]any{"rule_set": []string{tag}}
			switch b {
			case "block":
				rr["action"] = "reject"
			case "direct":
				rr["outbound"] = DirectTag
				dnsRoutes = append(dnsRoutes, dnsRoute{tag, false})
			default:
				rr["outbound"] = OutboundTag(b)
				dnsRoutes = append(dnsRoutes, dnsRoute{tag, true})
			}
			routeRules = append(routeRules, rr)
		}
	}

	final := DirectTag
	def := resolveTarget(p.DefaultTarget)
	switch def.Kind {
	case model.TargetServer:
		final = OutboundTag(def.ServerID)
	case model.TargetBlock:
		routeRules = append(routeRules, map[string]any{"network": []string{"tcp", "udp"}, "action": "reject"})
	}

	// --- DNS
	doh := p.DNS.DoH
	if doh == "" {
		doh = DefaultDoH
	}
	remote := map[string]any{"tag": "dns-remote"}
	switch p.DNS.Mode {
	case "system":
		remote["type"] = "local"
	case "doh_direct":
		remote["type"] = "https"
		remote["server"] = doh
	default: // via_server
		sid := p.DNS.ServerID
		if sid == "" || !activeByID[sid] {
			sid = ""
			if def.IsServer() {
				sid = def.ServerID
			} else if len(active) > 0 {
				sid = active[0].ID
			}
		}
		remote["type"] = "https"
		remote["server"] = doh
		if sid != "" {
			remote["detour"] = OutboundTag(sid)
		}
	}
	dnsServers := []any{remote}
	dnsFinal := "dns-remote"
	var dnsRules []any
	if p.DNS.Mode == "" || p.DNS.Mode == "via_server" {
		// Apps that go direct resolve through DoH directly (still encrypted):
		// through the VPN server a CDN would pick nodes near the server, and
		// the direct connection would then go to the far side of the world.
		dnsServers = append(dnsServers, map[string]any{"type": "https", "tag": "dns-direct", "server": doh})
		toDirect := !def.IsServer()
		if toDirect {
			dnsFinal = "dns-direct"
		}
		// Same order as the route rules (first match wins), so an app matched
		// by several rule-sets resolves the way its traffic is routed.
		// Trailing rules that only repeat the final server are dropped.
		last := -1
		for i, r := range dnsRoutes {
			if r.viaVPN == toDirect {
				last = i
			}
		}
		for _, r := range dnsRoutes[:last+1] {
			srv := "dns-direct"
			if r.viaVPN {
				srv = "dns-remote"
			}
			dnsRules = append(dnsRules, map[string]any{"rule_set": []string{r.tag}, "server": srv})
		}
	}
	dnsServers = append(dnsServers, map[string]any{"type": "local", "tag": "dns-local"})
	dns := map[string]any{
		"servers": dnsServers,
		"final":   dnsFinal,
		// lets the activity view show host names without sniffing
		"reverse_mapping": true,
	}
	if len(dnsRules) > 0 {
		dns["rules"] = dnsRules
	}
	if !env.HasIPv6 || p.IPv6 == "block" {
		dns["strategy"] = "ipv4_only"
	}

	// --- TUN
	addrs := []string{TunAddr4}
	if env.HasIPv6 || p.IPv6 == "block" {
		// with IPv6 on the uplink we must capture it to reject it, otherwise it leaks.
		addrs = append(addrs, TunAddr6)
	}
	stack := p.Stack
	if stack == "" {
		// gvisor keeps queues short under load: with "mixed" the Windows TCP
		// stack buffered so much that latency rose from ~6 to ~130 ms while a
		// download saturated the link, for direct apps too.
		stack = "gvisor"
	}
	tun := map[string]any{
		"type":           "tun",
		"tag":            TunTag,
		"interface_name": TunName,
		"address":        addrs,
		"mtu":            p.EffectiveMTU(),
		"auto_route":     true,
		"strict_route":   p.StrictRoute,
		"stack":          stack,
	}
	var routeExclude []string
	for _, pfx := range excludes {
		routeExclude = append(routeExclude, pfx.String())
	}
	if len(routeExclude) > 0 {
		tun["route_exclude_address"] = routeExclude
	}

	route := map[string]any{
		"rules":                   routeRules,
		"rule_set":                ruleSetDefs,
		"final":                   final,
		"auto_detect_interface":   true,
		"find_process":            true,
		"default_domain_resolver": "dns-local",
	}

	// Per-connection "info" logging costs real throughput with busy apps
	// (torrents), so the engine only logs warnings unless asked otherwise.
	level := orDefault(env.LogLevel, "warn")
	if p.VerboseLog {
		level = "info"
	}
	logOpts := map[string]any{"level": level, "timestamp": true}
	if env.LogPath != "" {
		logOpts["output"] = env.LogPath
	}
	cfg := map[string]any{
		"log":       logOpts,
		"dns":       dns,
		"inbounds":  []any{tun},
		"outbounds": outbounds,
		"route":     route,
	}
	if len(endpoints) > 0 {
		cfg["endpoints"] = endpoints
		// sing-box rejects a detour to an "empty" direct outbound, hence connect_timeout.
		cfg["outbounds"] = append(outbounds, map[string]any{"type": "direct", "tag": WGDirectTag, "connect_timeout": "10s"})
	}
	exp := map[string]any{}
	if env.ClashAPI != "" {
		exp["clash_api"] = map[string]any{"external_controller": env.ClashAPI, "secret": env.ClashSecret}
	}
	if env.CacheFile != "" {
		exp["cache_file"] = map[string]any{"enabled": true, "path": env.CacheFile}
	}
	if len(exp) > 0 {
		cfg["experimental"] = exp
	}
	b, err := marshal(cfg)
	if err != nil {
		return nil, err
	}
	out.Config = b
	return out, nil
}

// MatchRegex returns a case-insensitive process_path_regex for a match.
// sing-box compares process_path and process_name case-sensitively, while
// Windows paths are case-insensitive, so regexes are used for everything.
func MatchRegex(m model.Match) string {
	m = m.Normalize()
	switch m.Kind {
	case model.MatchPath:
		return "(?i)^" + regexp.QuoteMeta(m.Value) + "$"
	case model.MatchName:
		return `(?i)(^|[\\/])` + regexp.QuoteMeta(m.Value) + "$"
	case model.MatchStorePackage:
		return `(?i)\\WindowsApps\\` + regexp.QuoteMeta(m.Value) + `_[^\\]*\\`
	}
	return ""
}

func sortedUnique(in []string) []string {
	m := map[string]bool{}
	var out []string
	for _, s := range in {
		if !m[s] {
			m[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func dedupPrefixes(in []netip.Prefix) []netip.Prefix {
	m := map[netip.Prefix]bool{}
	var out []netip.Prefix
	for _, p := range in {
		if !m[p] {
			m[p] = true
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// OnlyRuleSetsChanged reports whether two generations differ only in rule-set
// contents, i.e. the change can be applied by rewriting files without restart.
func OnlyRuleSetsChanged(prev, next *Output) bool {
	if prev == nil || next == nil {
		return false
	}
	if !bytes.Equal(prev.Config, next.Config) || len(prev.RuleSets) != len(next.RuleSets) {
		return false
	}
	for k := range next.RuleSets {
		if _, ok := prev.RuleSets[k]; !ok {
			return false
		}
	}
	return true
}

// Describe is a short human-readable summary used in logs.
func Describe(o *Output) string {
	var names []string
	for k, v := range o.RuleSets {
		if !strings.Contains(string(v), `"rules": []`) {
			names = append(names, k)
		}
	}
	sort.Strings(names)
	return fmt.Sprintf("%d bytes config, non-empty rule-sets: %v", len(o.Config), names)
}

// joinDir joins a file name to dir using dir's own separator, so the output
// is the same on every OS the config is generated on: Windows paths keep "\",
// Unix paths (Android) keep "/".
func joinDir(dir, file string) string {
	if strings.Contains(dir, `\`) {
		return strings.TrimRight(dir, `\`) + `\` + file
	}
	return strings.TrimRight(dir, "/") + "/" + file
}
