//go:build windows

package service

import (
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/miekg/dns"

	"github.com/kordo-vpn/kordo/windows/netmon"
)

// resolver looks up server domains to exclude them from the TUN. It speaks
// DNS directly from the service process (permitted by the kill switch), so it
// works even while the system resolver is blocked. Results are cached and the
// last good answer is reused if a lookup fails.
type resolver struct {
	log   *slog.Logger
	mu    sync.Mutex
	cache map[string][]netip.Addr
}

func newResolver(log *slog.Logger) *resolver {
	return &resolver{log: log, cache: map[string][]netip.Addr{}}
}

var fallbackDNS = []string{"1.1.1.1", "8.8.8.8", "77.88.8.8"}

func systemDNSServers() []string {
	// Gateways often run DNS forwarders; we query well-known public resolvers
	// as well. Adapter DNS lists are not exposed by x/sys, gateways are.
	var out []string
	if ads, err := netmon.Adapters(); err == nil {
		for _, a := range ads {
			for _, g := range a.Gateways {
				if g.Is4() {
					out = append(out, g.String())
				}
			}
		}
	}
	return append(out, fallbackDNS...)
}

func (r *resolver) ResolveAll(domains []string) map[string][]netip.Addr {
	out := map[string][]netip.Addr{}
	if len(domains) == 0 {
		return out
	}
	servers := systemDNSServers()
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, d := range domains {
		wg.Add(1)
		go func(d string) {
			defer wg.Done()
			addrs := r.lookup(d, servers)
			r.mu.Lock()
			if len(addrs) > 0 {
				r.cache[d] = addrs
			} else {
				addrs = r.cache[d]
			}
			r.mu.Unlock()
			mu.Lock()
			out[d] = addrs
			mu.Unlock()
		}(d)
	}
	wg.Wait()
	return out
}

func (r *resolver) lookup(domain string, servers []string) []netip.Addr {
	c := &dns.Client{Timeout: 2 * time.Second}
	for _, srv := range servers {
		var res []netip.Addr
		for _, qt := range []uint16{dns.TypeA, dns.TypeAAAA} {
			m := new(dns.Msg)
			m.SetQuestion(dns.Fqdn(domain), qt)
			resp, _, err := c.Exchange(m, net.JoinHostPort(srv, "53"))
			if err != nil || resp == nil || resp.Rcode != dns.RcodeSuccess {
				continue
			}
			for _, rr := range resp.Answer {
				switch v := rr.(type) {
				case *dns.A:
					if a, ok := netip.AddrFromSlice(v.A.To4()); ok {
						res = append(res, a)
					}
				case *dns.AAAA:
					if a, ok := netip.AddrFromSlice(v.AAAA); ok {
						res = append(res, a)
					}
				}
			}
		}
		if len(res) > 0 {
			return res
		}
	}
	r.log.Warn("could not resolve server domain", "domain", domain)
	return nil
}
