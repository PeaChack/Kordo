//go:build windows

package service

import (
	"context"
	"net/netip"
	"strings"
	"time"

	"github.com/PeaChack/Kordo/core/configgen"
	"github.com/PeaChack/Kordo/engine"
)

// Connections are routed once, when they open. Long-lived ones (HTTP/2,
// QUIC, WebSockets) therefore keep the route they started with after an app
// is moved, and if the engine ever misses a rule-set reload, new ones follow
// the old rules too. The reconciler compares live connections with the
// current rules: misrouted ones are closed so the app reconnects by the new
// rules, and if fresh connections are still misrouted afterwards the engine
// is restarted (it evidently did not pick the rules up).

const (
	healInterval   = time.Minute
	healAfterApply = 2 * time.Second
	healAfterNet   = 10 * time.Second
	restartGap     = 10 * time.Minute
)

func (s *Service) healLoop(ctx context.Context) {
	t := time.NewTicker(healInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.reconcile()
		}
	}
}

// reconcileSoon runs the reconciler after d, coalescing repeated requests.
func (s *Service) reconcileSoon(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.healTimer != nil {
		s.healTimer.Stop()
	}
	s.healTimer = time.AfterFunc(d, s.reconcile)
}

func chainTag(chains []string) string {
	for _, c := range chains {
		if c == configgen.DirectTag || strings.HasPrefix(c, "srv-") {
			return c
		}
	}
	return ""
}

func (s *Service) reconcile() {
	if s.sup == nil {
		return
	}
	eng, ok := s.sup.Engine().(*engine.Instance)
	if !ok || eng == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conns, err := eng.Connections(ctx)
	if err != nil {
		return
	}
	prof, servers := s.store.Snapshot()
	rc := configgen.NewRouteCheck(prof, servers)
	var wrong []engine.Connection
	for _, c := range conns {
		path := c.Metadata.ProcessPath
		actual := chainTag(c.Chains)
		if path == "" || actual == "" || c.Metadata.DstPort == "53" {
			continue
		}
		if prof.AllowLAN {
			if a, err := netip.ParseAddr(c.Metadata.DstIP); err == nil && (a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsMulticast()) {
				continue // LAN goes direct for every app
			}
		}
		if want := rc.Tag(path); want != actual {
			wrong = append(wrong, c)
		}
	}

	now := time.Now()
	s.mu.Lock()
	stale := false
	next := map[string]time.Time{}
	for _, c := range wrong {
		p := c.Metadata.ProcessPath
		if closed, ok := s.misrouted[p]; ok {
			if st, err := time.Parse(time.RFC3339Nano, c.Start); err == nil && st.After(closed) {
				stale = true // opened after we closed the old ones: the engine still uses old rules
			}
		}
		next[p] = now
	}
	s.misrouted = next
	restart := stale && now.Sub(s.lastHeal) > restartGap
	if restart {
		s.lastHeal = now
		s.misrouted = nil
	}
	s.mu.Unlock()

	if len(wrong) == 0 {
		return
	}
	if restart {
		s.log.Warn("new connections ignore the current rules, restarting the engine",
			"app", wrong[0].Metadata.ProcessPath, "via", chainTag(wrong[0].Chains))
		s.sup.Restart("connections do not follow the rules")
		return
	}
	for _, c := range wrong {
		_ = eng.CloseConnection(ctx, c.ID)
	}
	s.log.Info("closed connections that used an outdated route", "count", len(wrong),
		"app", wrong[0].Metadata.ProcessPath, "via", chainTag(wrong[0].Chains), "want", rc.Tag(wrong[0].Metadata.ProcessPath))
}
