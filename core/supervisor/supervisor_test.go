package supervisor

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kordo-vpn/kordo/core/configgen"
)

type fakeEngine struct {
	mu         sync.Mutex
	fail       map[string]bool
	closedTags []string
	resets     atomic.Int32
	closes     atomic.Int32
	closed     atomic.Bool
}

func (f *fakeEngine) URLTest(ctx context.Context, tag, link string) (time.Duration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail[tag] {
		return 0, errors.New("timeout")
	}
	return 42 * time.Millisecond, nil
}
func (f *fakeEngine) ResetNetwork() { f.resets.Add(1) }
func (f *fakeEngine) CloseConnections(ctx context.Context, tag string) error {
	f.mu.Lock()
	f.closedTags = append(f.closedTags, tag)
	f.mu.Unlock()
	f.closes.Add(1)
	return nil
}
func (f *fakeEngine) Close() error { f.closed.Store(true); return nil }

type harness struct {
	s        *Supervisor
	engines  []*fakeEngine
	starts   atomic.Int32
	writes   atomic.Int32
	ruleOnly atomic.Int32
	startErr atomic.Bool
	ruleVer  atomic.Int32
	cfgVer   atomic.Int32
	failTag  map[string]bool
	mu       sync.Mutex
}

func newHarness(t *testing.T) *harness {
	h := &harness{failTag: map[string]bool{}}
	h.s = New(Config{HealthInterval: time.Hour, NetDebounce: 10 * time.Millisecond, BackoffMin: 20 * time.Millisecond, BackoffMax: 50 * time.Millisecond}, Hooks{
		Build: func() (*configgen.Output, error) {
			return &configgen.Output{
				Config:       []byte{byte(h.cfgVer.Load())},
				RuleSets:     map[string][]byte{"a.json": {byte(h.ruleVer.Load())}},
				OutboundTags: map[string]string{"s1": "srv-s1", "s2": "srv-s2"},
			}, nil
		},
		Write: func(out *configgen.Output, onlyRuleSets bool) error {
			h.writes.Add(1)
			if onlyRuleSets {
				h.ruleOnly.Add(1)
			}
			return nil
		},
		Start: func(out *configgen.Output) (Engine, error) {
			h.starts.Add(1)
			if h.startErr.Load() {
				return nil, errors.New("tun busy")
			}
			h.mu.Lock()
			defer h.mu.Unlock()
			e := &fakeEngine{fail: h.failTag}
			h.engines = append(h.engines, e)
			return e, nil
		},
		HealthURL:      func(string) string { return "http://x" },
		NeedsHandshake: func(string) bool { return false },
	}, nil)
	t.Cleanup(h.s.Shutdown)
	return h
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func TestConnectAndHealthy(t *testing.T) {
	h := newHarness(t)
	if err := h.s.Connect(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "connected", func() bool { return h.s.Status().State == StateConnected })
	st := h.s.Status()
	if len(st.Servers) != 2 || !st.Servers[0].Healthy || st.Servers[0].RTTms != 42 {
		t.Fatalf("bad status %+v", st)
	}
}

func TestRuleOnlyChangeDoesNotRestart(t *testing.T) {
	h := newHarness(t)
	_ = h.s.Connect()
	h.ruleVer.Store(1)
	restarted, err := h.s.Apply()
	if err != nil || restarted {
		t.Fatalf("restarted=%v err=%v", restarted, err)
	}
	if h.starts.Load() != 1 || h.ruleOnly.Load() != 1 {
		t.Fatalf("starts=%d ruleOnly=%d", h.starts.Load(), h.ruleOnly.Load())
	}
	h.cfgVer.Store(1)
	restarted, err = h.s.Apply()
	if err != nil || !restarted || h.starts.Load() != 2 {
		t.Fatalf("expected restart: restarted=%v starts=%d err=%v", restarted, h.starts.Load(), err)
	}
}

func TestHealthFailuresCloseOnlyThatServer(t *testing.T) {
	h := newHarness(t)
	_ = h.s.Connect()
	waitFor(t, "connected", func() bool { return h.s.Status().State == StateConnected })
	h.mu.Lock()
	h.failTag["srv-s2"] = true
	h.mu.Unlock()
	h.s.CheckNow()
	if st := h.s.Status().State; st != StateConnected {
		t.Fatalf("one failed probe must not degrade the state, got %s", st)
	}
	h.s.CheckNow()
	if st := h.s.Status().State; st != StateDegraded {
		t.Fatalf("after 2 fails: %s", st)
	}
	waitFor(t, "connections closed", func() bool { return h.engines[0].closes.Load() == 1 })
	h.engines[0].mu.Lock()
	tags := append([]string(nil), h.engines[0].closedTags...)
	h.engines[0].mu.Unlock()
	if len(tags) != 1 || tags[0] != "srv-s2" {
		t.Fatalf("closed %v, want only srv-s2", tags)
	}
	for i := 0; i < 5; i++ {
		h.s.CheckNow()
	}
	if n := h.starts.Load(); n != 1 {
		t.Fatalf("engine restarted %d times; a failing server must not restart it", n-1)
	}
	if h.engines[0].closed.Load() {
		t.Fatal("engine must stay up")
	}
	h.mu.Lock()
	h.failTag["srv-s2"] = false
	h.mu.Unlock()
	h.s.CheckNow()
	if st := h.s.Status().State; st != StateConnected {
		t.Fatalf("after recovery: %s", st)
	}
}

func TestStartFailureBacksOff(t *testing.T) {
	h := newHarness(t)
	h.startErr.Store(true)
	_ = h.s.Connect()
	if st := h.s.Status(); st.State != StateReconnecting || st.NextTry == nil {
		t.Fatalf("status %+v", st)
	}
	waitFor(t, "retries", func() bool { return h.starts.Load() >= 3 })
	h.startErr.Store(false)
	waitFor(t, "connected", func() bool { return h.s.Status().State == StateConnected })
}

func TestNetworkChangeResets(t *testing.T) {
	h := newHarness(t)
	_ = h.s.Connect()
	h.s.NetworkChanged("test")
	h.s.NetworkChanged("test")
	waitFor(t, "reset", func() bool { return h.engines[0].resets.Load() == 1 })
	time.Sleep(30 * time.Millisecond)
	if n := h.engines[0].resets.Load(); n != 1 {
		t.Fatalf("debounce failed: %d resets", n)
	}
}

func TestDisconnect(t *testing.T) {
	h := newHarness(t)
	_ = h.s.Connect()
	h.s.Disconnect()
	if h.s.Status().State != StateOff || !h.engines[0].closed.Load() {
		t.Fatal("not stopped")
	}
}
