package geo

import (
	"context"
	"errors"
	"net"

	"github.com/PeaChack/Kordo/core/model"
	"github.com/PeaChack/Kordo/core/providers"
)

// MainServer is the server whose place the device should look like it is
// at: preferred if set and present, else the "everything through" server,
// else the one most app rules use, else the first.
func MainServer(p model.Profile, servers []model.Server, preferred string) string {
	for _, s := range servers {
		if preferred != "" && s.ID == preferred {
			return s.ID
		}
	}
	if p.DefaultTarget.IsServer() {
		return p.DefaultTarget.ServerID
	}
	count := map[string]int{}
	for _, r := range p.Rules {
		if r.Target.IsServer() {
			count[r.Target.ServerID]++
		}
	}
	best := ""
	for _, s := range servers {
		if best == "" || count[s.ID] > count[best] {
			best = s.ID
		}
	}
	return best
}

// ServerHost returns the server's address as written in its parameters
// (an IP or a domain), "" if unknown.
func ServerHost(s model.Server) string {
	p, err := providers.Get(s.ProviderID)
	if err != nil {
		return ""
	}
	ips, domains, err := p.ServerAddresses(s.Params)
	if err != nil {
		return ""
	}
	if len(ips) > 0 {
		return ips[0].Addr().String()
	}
	if len(domains) > 0 {
		return domains[0]
	}
	return ""
}

// ResolveIP turns a host into an IP (first address).
func ResolveIP(ctx context.Context, host string) (string, error) {
	if host == "" {
		return "", errors.New("server has no address")
	}
	if net.ParseIP(host) != nil {
		return host, nil
	}
	addrs, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil || len(addrs) == 0 {
		return "", errors.New("cannot resolve " + host)
	}
	return addrs[0].String(), nil
}
