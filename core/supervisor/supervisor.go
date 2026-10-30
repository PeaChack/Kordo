// Package supervisor keeps the engine alive: it applies configuration
// changes (hot-reloading rule-sets when possible), health-checks every active
// server through its own outbound, reacts to network changes and restarts the
// engine with exponential backoff. It is platform independent; the platform
// feeds it network events and provides the engine factory.
package supervisor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sort"
	"sync"
	"time"

	"github.com/portico-vpn/portico/core/configgen"
)

type State string

const (
	StateOff          State = "off"
	StateConnecting   State = "connecting"
	StateConnected    State = "connected"
	StateDegraded     State = "degraded"
	StateReconnecting State = "reconnecting"
	StateBlocked      State = "blocked" // engine down, kill switch holds traffic
)

// Engine is a running instance (engine.Instance in production).
type Engine interface {
	URLTest(ctx context.Context, tag, link string) (time.Duration, error)
	ResetNetwork()
	CloseAllConnections(ctx context.Context) error
	Close() error
}

// Hooks connect the supervisor to the platform.
type Hooks struct {
	// Build regenerates the configuration from the current store state.
	Build func() (*configgen.Output, error)
	// Write persists config + rule-sets atomically. onlyRuleSets=true means
	// the engine is running and only rule-set files must be rewritten.
	Write func(out *configgen.Output, onlyRuleSets bool) error
	// Start launches the engine with the written config.
	Start func(out *configgen.Output) (Engine, error)
	// HealthURL returns the probe URL for a server.
	HealthURL func(serverID string) string
	// NeedsHandshake reports whether a server is stateful (WireGuard).
	NeedsHandshake func(serverID string) bool
	// KillSwitchActive reports whether traffic stays blocked while the engine is down.
	KillSwitchActive func() bool
	// OnChange is invoked (asynchronously) after every status change.
	OnChange func(Status)
}

type ServerHealth struct {
	ID        string    `json:"id"`
	Healthy   bool      `json:"healthy"`
	Checked   bool      `json:"checked"`
	RTTms     int64     `json:"rttMs"`
	Fails     int       `json:"fails"`
	LastError string    `json:"lastError,omitempty"`
	LastCheck time.Time `json:"lastCheck"`
}

type Status struct {
	State    State          `json:"state"`
	Since    time.Time      `json:"since"`
	Error    string         `json:"error,omitempty"`
	Servers  []ServerHealth `json:"servers"`
	Restarts int            `json:"restarts"`
	NextTry  *time.Time     `json:"nextTry,omitempty"`
}

type Config struct {
	HealthInterval time.Duration // default 30s
	HealthTimeout  time.Duration // default 5s
	NetDebounce    time.Duration // default 1.5s
	BackoffMin     time.Duration // default 1s
	BackoffMax     time.Duration // default 60s
	FailsToReset   int           // default 2: close connections, degraded
	FailsToRestart int           // default 3: restart the engine
}

func (c *Config) defaults() {
	if c.HealthInterval == 0 {
		c.HealthInterval = 30 * time.Second
	}
	if c.HealthTimeout == 0 {
		c.HealthTimeout = 5 * time.Second
	}
	if c.NetDebounce == 0 {
		c.NetDebounce = 1500 * time.Millisecond
	}
	if c.BackoffMin == 0 {
		c.BackoffMin = time.Second
	}
	if c.BackoffMax == 0 {
		c.BackoffMax = 60 * time.Second
	}
	if c.FailsToReset == 0 {
		c.FailsToReset = 2
	}
	if c.FailsToRestart == 0 {
		c.FailsToRestart = 3
	}
}

type Supervisor struct {
	cfg   Config
	hooks Hooks
	log   *slog.Logger

	mu       sync.Mutex
	want     bool // user wants to be connected
	eng      Engine
	current  *configgen.Output
	status   Status
	health   map[string]*ServerHealth
	backoff  time.Duration
	retry    *time.Timer
	netTimer *time.Timer
	wake     chan struct{}
	stop     chan struct{}
	stopped  chan struct{}
	gen      int // incremented on each engine (re)start; stale health results are dropped
	notifyCh chan Status
	closed   bool
}

func New(cfg Config, hooks Hooks, log *slog.Logger) *Supervisor {
	cfg.defaults()
	if log == nil {
		log = slog.Default()
	}
	s := &Supervisor{
		cfg: cfg, hooks: hooks, log: log,
		status:   Status{State: StateOff, Since: time.Now()},
		health:   map[string]*ServerHealth{},
		wake:     make(chan struct{}, 1),
		stop:     make(chan struct{}),
		stopped:  make(chan struct{}),
		notifyCh: make(chan Status, 64),
	}
	go s.healthLoop()
	go s.notifyLoop()
	return s
}

func (s *Supervisor) notifyLoop() {
	for st := range s.notifyCh {
		if s.hooks.OnChange != nil {
			s.hooks.OnChange(st)
		}
	}
}

// Shutdown stops the engine and background loops. Kill-switch filters are not
// touched here: they are owned by the platform and removed only explicitly.
func (s *Supervisor) Shutdown() {
	s.mu.Lock()
	s.want = false
	s.stopEngineLocked()
	s.mu.Unlock()
	close(s.stop)
	<-s.stopped
	s.mu.Lock()
	s.closed = true
	if s.netTimer != nil {
		s.netTimer.Stop()
	}
	close(s.notifyCh)
	s.mu.Unlock()
}

func (s *Supervisor) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *Supervisor) snapshotLocked() Status {
	st := s.status
	st.Servers = nil
	for _, h := range s.health {
		st.Servers = append(st.Servers, *h)
	}
	sort.Slice(st.Servers, func(i, j int) bool { return st.Servers[i].ID < st.Servers[j].ID })
	return st
}

func (s *Supervisor) setStateLocked(st State, err error) {
	if s.status.State != st {
		s.status.Since = time.Now()
		s.log.Info("state", "from", s.status.State, "to", st, "err", err)
	}
	s.status.State = st
	s.status.Error = ""
	if err != nil {
		s.status.Error = err.Error()
	}
	if st != StateReconnecting && st != StateBlocked {
		s.status.NextTry = nil
	}
	s.emitLocked()
}

func (s *Supervisor) emitLocked() {
	if s.closed {
		return
	}
	select {
	case s.notifyCh <- s.snapshotLocked():
	default: // drop if the consumer is slow; next change sends a fresh snapshot
	}
}

// Engine returns the running engine (nil when not connected).
func (s *Supervisor) Engine() Engine {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.eng
}

// Connect starts the engine (idempotent).
func (s *Supervisor) Connect() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.want = true
	s.backoff = 0
	if s.eng != nil {
		return nil
	}
	s.setStateLocked(StateConnecting, nil)
	return s.startLocked()
}

func (s *Supervisor) Disconnect() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.want = false
	s.stopEngineLocked()
	s.health = map[string]*ServerHealth{}
	s.setStateLocked(StateOff, nil)
}

func (s *Supervisor) stopEngineLocked() {
	if s.retry != nil {
		s.retry.Stop()
		s.retry = nil
	}
	if s.eng != nil {
		if err := s.eng.Close(); err != nil {
			s.log.Warn("engine close", "err", err)
		}
		s.eng = nil
	}
	s.gen++
}

func (s *Supervisor) startLocked() error {
	out, err := s.hooks.Build()
	if err != nil {
		// configuration errors are not retried: the user must fix them.
		s.want = false
		s.setStateLocked(StateOff, fmt.Errorf("configuration: %w", err))
		return err
	}
	if err := s.hooks.Write(out, false); err != nil {
		s.scheduleRetryLocked(fmt.Errorf("write config: %w", err))
		return err
	}
	eng, err := s.hooks.Start(out)
	if err != nil {
		s.scheduleRetryLocked(err)
		return err
	}
	s.eng = eng
	s.current = out
	s.gen++
	s.health = map[string]*ServerHealth{}
	for id := range out.OutboundTags {
		s.health[id] = &ServerHealth{ID: id}
	}
	s.setStateLocked(StateConnecting, nil)
	s.kick()
	return nil
}

func (s *Supervisor) scheduleRetryLocked(cause error) {
	s.stopEngineLocked()
	if !s.want {
		s.setStateLocked(StateOff, cause)
		return
	}
	if s.backoff == 0 {
		s.backoff = s.cfg.BackoffMin
	} else {
		s.backoff *= 2
		if s.backoff > s.cfg.BackoffMax {
			s.backoff = s.cfg.BackoffMax
		}
	}
	// ±20% jitter
	d := time.Duration(float64(s.backoff) * (0.8 + 0.4*rand.Float64()))
	next := time.Now().Add(d)
	s.status.Restarts++
	st := StateReconnecting
	if s.hooks.KillSwitchActive != nil && s.hooks.KillSwitchActive() {
		st = StateBlocked
	}
	s.setStateLocked(st, cause)
	s.status.NextTry = &next
	s.emitLocked()
	s.log.Warn("engine restart scheduled", "in", d, "cause", cause)
	s.retry = time.AfterFunc(d, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.retry = nil
		if s.want && s.eng == nil {
			_ = s.startLocked()
		}
	})
}

// Restart forces a full engine restart (e.g. after server changes).
func (s *Supervisor) Restart(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.want {
		return
	}
	s.log.Info("engine restart", "reason", reason)
	s.stopEngineLocked()
	s.backoff = 0
	s.setStateLocked(StateConnecting, nil)
	_ = s.startLocked()
}

// Apply regenerates the configuration. Rule-only changes are written to the
// rule-set files and picked up by the running engine; anything else restarts it.
// It returns true if a restart happened.
func (s *Supervisor) Apply() (restarted bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.want || s.eng == nil {
		// not running: still validate so the user gets immediate feedback
		_, err := s.hooks.Build()
		return false, err
	}
	out, err := s.hooks.Build()
	if err != nil {
		return false, err
	}
	if configgen.OnlyRuleSetsChanged(s.current, out) {
		if err := s.hooks.Write(out, true); err != nil {
			return false, err
		}
		s.current = out
		s.log.Info("rule-sets hot-reloaded", "summary", configgen.Describe(out))
		return false, nil
	}
	s.stopEngineLocked()
	s.backoff = 0
	s.setStateLocked(StateConnecting, nil)
	return true, s.startLocked()
}

// NetworkChanged is called by the platform on interface/route changes and
// resume from sleep. Events are debounced.
func (s *Supervisor) NetworkChanged(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.netTimer != nil {
		s.netTimer.Stop()
	}
	s.netTimer = time.AfterFunc(s.cfg.NetDebounce, func() {
		s.mu.Lock()
		eng := s.eng
		want := s.want
		retrying := s.retry != nil
		if want && eng == nil && retrying {
			// network came back while waiting: retry right away
			s.retry.Stop()
			s.retry = nil
			s.backoff = 0
			_ = s.startLocked()
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
		if eng != nil {
			s.log.Info("network changed", "reason", reason)
			eng.ResetNetwork()
			s.kick()
		}
	})
}

func (s *Supervisor) kick() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Supervisor) healthLoop() {
	defer close(s.stopped)
	t := time.NewTicker(s.cfg.HealthInterval)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
		case <-s.wake:
		}
		s.checkOnce()
	}
}

// CheckNow runs a health check synchronously (used by tests and "test server").
func (s *Supervisor) CheckNow() { s.checkOnce() }

func (s *Supervisor) checkOnce() {
	s.mu.Lock()
	eng := s.eng
	gen := s.gen
	out := s.current
	s.mu.Unlock()
	if eng == nil || out == nil {
		return
	}
	type res struct {
		id  string
		rtt time.Duration
		err error
	}
	results := make(chan res, len(out.OutboundTags))
	for id, tag := range out.OutboundTags {
		go func(id, tag string) {
			ctx, cancel := context.WithTimeout(context.Background(), s.cfg.HealthTimeout)
			defer cancel()
			rtt, err := eng.URLTest(ctx, tag, s.hooks.HealthURL(id))
			results <- res{id, rtt, err}
		}(id, tag)
	}
	collected := make([]res, 0, len(out.OutboundTags))
	for range out.OutboundTags {
		collected = append(collected, <-results)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if gen != s.gen || s.eng == nil {
		return // engine was restarted meanwhile
	}
	needReset, needRestart := false, false
	for _, r := range collected {
		h := s.health[r.id]
		if h == nil {
			continue
		}
		h.Checked = true
		h.LastCheck = time.Now()
		if r.err == nil {
			h.Healthy, h.Fails, h.LastError, h.RTTms = true, 0, "", r.rtt.Milliseconds()
			continue
		}
		h.Healthy = false
		h.Fails++
		h.LastError = r.err.Error()
		if h.Fails == s.cfg.FailsToReset {
			needReset = true
		}
		if h.Fails >= s.cfg.FailsToRestart {
			needRestart = true
		}
		if h.Fails >= 1 && s.hooks.NeedsHandshake != nil && s.hooks.NeedsHandshake(r.id) && h.Fails >= s.cfg.FailsToReset {
			needRestart = true // stateful tunnels recover only by re-handshake
		}
	}
	if needRestart {
		for _, h := range s.health {
			h.Fails = 0
		}
		s.scheduleRetryLocked(errors.New("health check failed repeatedly"))
		return
	}
	if needReset {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = eng.CloseAllConnections(ctx)
		}()
	}
	allOK := true
	for _, h := range s.health {
		if !h.Healthy {
			allOK = false
		}
	}
	if allOK {
		s.backoff = 0
		s.setStateLocked(StateConnected, nil)
	} else {
		s.setStateLocked(StateDegraded, nil)
	}
}
