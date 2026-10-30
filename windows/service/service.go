//go:build windows

// Package service is the privileged Kordo service: it owns the store, the
// engine (sing-box as a library), the supervisor, the kill switch, the process
// tracker and the named-pipe RPC endpoint used by the GUI and CLI.
package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"

	"github.com/PeaChack/Kordo/core/configgen"
	"github.com/PeaChack/Kordo/core/model"
	"github.com/PeaChack/Kordo/core/proctree"
	"github.com/PeaChack/Kordo/core/providers"
	_ "github.com/PeaChack/Kordo/core/providers/all"
	"github.com/PeaChack/Kordo/core/rpcapi"
	"github.com/PeaChack/Kordo/core/supervisor"
	"github.com/PeaChack/Kordo/engine"
	"github.com/PeaChack/Kordo/windows/killswitch"
	"github.com/PeaChack/Kordo/windows/netmon"
	"github.com/PeaChack/Kordo/windows/procs"
	"github.com/PeaChack/Kordo/windows/store"
)

var Version = "0.1.0-dev"

const EngineVersion = "sing-box 1.14.4"

type Service struct {
	DataDir  string
	rulesDir string
	logDir   string
	selfExe  string

	log      *slog.Logger
	logFile  *rotatingFile
	store    *store.Store
	sup      *supervisor.Supervisor
	fw       *killswitch.Firewall
	rpc      *rpcapi.Server
	mon      *netmon.Monitor
	matcher  *proctree.Matcher
	resolver *resolver

	clashAddr   string
	clashSecret string

	mu         sync.Mutex
	asks       map[string]pendingAsk
	askedPaths map[string]time.Time
	watchStop  context.CancelFunc
	listener   net.Listener
}

type pendingAsk struct {
	rpcapi.ChildAsk
}

// DefaultDataDir is %ProgramData%\Kordo; KORDO_DATA overrides it (development).
func DefaultDataDir() string {
	if d := os.Getenv("KORDO_DATA"); d != "" {
		return d
	}
	pd := os.Getenv("ProgramData")
	if pd == "" {
		pd = `C:\ProgramData`
	}
	return filepath.Join(pd, "Kordo")
}

// New prepares directories, logging and the store. It does not start anything.
func New(dataDir string, console bool) (*Service, error) {
	s := &Service{
		DataDir:    dataDir,
		rulesDir:   filepath.Join(dataDir, "rules"),
		logDir:     filepath.Join(dataDir, "logs"),
		matcher:    proctree.NewMatcher(),
		asks:       map[string]pendingAsk{},
		askedPaths: map[string]time.Time{},
	}
	if os.Getenv("KORDO_DATA") == "" {
		// production: only SYSTEM and Administrators may read the store
		if err := store.SecureDir(dataDir); err != nil {
			return nil, fmt.Errorf("prepare %s: %w", dataDir, err)
		}
	}
	for _, d := range []string{s.rulesDir, s.logDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	if os.Getenv("KORDO_DATA") == "" {
		_ = store.ReadableDir(s.logDir)
	}
	// A crash of the service (panic, fatal error) is written here; otherwise
	// it would vanish with the service's invisible stderr.
	if cf, err := os.OpenFile(filepath.Join(s.logDir, "crash.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		_ = debug.SetCrashOutput(cf, debug.CrashOptions{})
		cf.Close()
	}
	lf, err := openRotating(filepath.Join(s.logDir, "service.log"), 5<<20)
	if err != nil {
		return nil, err
	}
	s.logFile = lf
	var w = ioWriter(lf, console)
	s.log = slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(s.log)
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	s.selfExe = exe
	if os.Getenv("KORDO_DATA") == "" {
		s.migrateFromPortico()
	}
	st, err := store.Open(filepath.Join(dataDir, "state.json"), store.DPAPI{})
	if err != nil {
		return nil, err
	}
	s.store = st
	var pruned int
	_ = st.Update(func(p *model.Profile, _ *[]model.Server) error {
		pruned = model.PruneSharedLearned(p)
		return nil
	})
	if pruned > 0 {
		s.log.Info("removed auto-learned shared programs from app rules", "count", pruned)
	}
	s.resolver = newResolver(s.log)
	var sec [16]byte
	_, _ = rand.Read(sec[:])
	s.clashSecret = hex.EncodeToString(sec[:])
	return s, nil
}

// Run starts all components and blocks until ctx is done.
func (s *Service) Run(ctx context.Context) error {
	s.log.Info("kordo service starting", "version", Version, "engine", EngineVersion, "data", s.DataDir)

	fw, err := killswitch.Open(s.log)
	if err != nil {
		s.log.Error("kill switch unavailable", "err", err)
	} else {
		s.fw = fw
		defer fw.Close()
	}

	s.sup = supervisor.New(supervisor.Config{FallbackHealthURL: model.FallbackHealthURL}, supervisor.Hooks{
		Build:            s.build,
		Write:            s.write,
		Start:            s.startEngine,
		HealthURL:        s.healthURL,
		NeedsHandshake:   s.needsHandshake,
		KillSwitchActive: s.killSwitchActive,
		OnChange:         s.onStatus,
	}, s.log)
	defer s.sup.Shutdown()

	mon, err := netmon.Start(func(reason string) { s.sup.NetworkChanged(reason) }, s.isTunLUID)
	if err != nil {
		s.log.Warn("network monitor unavailable", "err", err)
	} else {
		s.mon = mon
		defer mon.Close()
	}

	s.rpc = rpcapi.NewServer(s.handle, s.log)
	l, err := winio.ListenPipe(rpcapi.PipeName, &winio.PipeConfig{
		// SYSTEM and Administrators: full; interactive users: read/write.
		SecurityDescriptor: "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GRGW;;;IU)",
		InputBufferSize:    64 << 10,
		OutputBufferSize:   64 << 10,
	})
	if err != nil {
		return fmt.Errorf("listen %s: %w", rpcapi.PipeName, err)
	}
	s.listener = l
	go func() {
		if err := s.rpc.Serve(l, s.acceptClient); err != nil && !errors.Is(err, winio.ErrPipeListenerClosed) {
			s.log.Error("rpc serve", "err", err)
		}
	}()

	prof, _ := s.store.Snapshot()
	if prof.ConnectOnStart || s.wasConnected() {
		if err := s.sup.Connect(); err != nil {
			s.log.Warn("auto-connect failed", "err", err)
		}
	}

	<-ctx.Done()
	s.log.Info("kordo service stopping")
	l.Close()
	s.stopWatcher()
	// Note: kill-switch filters are intentionally left in place.
	return nil
}

// Resume is called on resume from sleep.
func (s *Service) Resume() {
	if s.sup != nil {
		s.sup.NetworkChanged("resume")
	}
}

func (s *Service) acceptClient(c net.Conn) (any, error) {
	type fder interface{ Fd() uintptr }
	f, ok := c.(fder)
	if !ok {
		return nil, nil
	}
	var pid, session uint32
	if err := windows.GetNamedPipeClientProcessId(windows.Handle(f.Fd()), &pid); err != nil {
		return nil, err
	}
	if err := windows.ProcessIdToSessionId(pid, &session); err != nil {
		return nil, err
	}
	path, _ := procs.ImagePath(pid)
	s.log.Info("rpc client connected", "pid", pid, "session", session, "exe", path)
	return session, nil
}

// ---------------------------------------------------------------- supervisor hooks

func (s *Service) env() configgen.Env {
	_, servers := s.store.Snapshot()
	var domains []string
	for _, srv := range servers {
		if !srv.Enabled {
			continue
		}
		if p, err := providers.Get(srv.ProviderID); err == nil {
			if _, ds, err := p.ServerAddresses(srv.Params); err == nil {
				domains = append(domains, ds...)
			}
		}
	}
	return configgen.Env{
		RulesDir:    s.rulesDir,
		LogPath:     filepath.Join(s.logDir, "core.log"),
		HasIPv6:     netmon.HasGlobalIPv6(configgen.TunName),
		Resolved:    s.resolver.ResolveAll(domains),
		ClashAPI:    s.clashAddrFor(),
		ClashSecret: s.clashSecret,
		CacheFile:   filepath.Join(s.DataDir, "cache.db"),
	}
}

func (s *Service) clashAddrFor() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.clashAddr == "" {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err == nil {
			s.clashAddr = l.Addr().String()
			l.Close()
		}
	}
	return s.clashAddr
}

func (s *Service) build() (*configgen.Output, error) {
	prof, servers := s.store.Snapshot()
	return configgen.Generate(prof, servers, s.env())
}

func (s *Service) write(out *configgen.Output, onlyRuleSets bool) error {
	for name, data := range out.RuleSets {
		if err := store.WriteFileAtomic(filepath.Join(s.rulesDir, name), data); err != nil {
			return err
		}
	}
	if onlyRuleSets {
		return nil
	}
	return store.WriteFileAtomic(filepath.Join(s.DataDir, "config.json"), out.Config)
}

func (s *Service) startEngine(out *configgen.Output) (supervisor.Engine, error) {
	// truncate the core log on every start so it does not grow forever
	_ = rotateIfLarge(filepath.Join(s.logDir, "core.log"), 5<<20)
	inst, err := engine.Start(out.Config, engine.Options{ClashAPI: s.clashAddrFor(), ClashSecret: s.clashSecret})
	if err != nil {
		// the port may have been taken: pick a new one next time
		s.mu.Lock()
		s.clashAddr = ""
		s.mu.Unlock()
		return nil, err
	}
	s.setConnectedFlag(true)
	s.applyFirewall(true, out.ServerPrefixes)
	s.startWatcher()
	return inst, nil
}

func (s *Service) healthURL(id string) string {
	if srv, ok := s.store.Server(id); ok {
		return srv.EffectiveHealthURL()
	}
	return model.DefaultHealthURL
}

func (s *Service) needsHandshake(id string) bool {
	srv, ok := s.store.Server(id)
	if !ok {
		return false
	}
	p, err := providers.Get(srv.ProviderID)
	return err == nil && p.Capabilities().NeedsHandshakeCheck
}

func (s *Service) killSwitchActive() bool {
	prof, _ := s.store.Snapshot()
	return prof.KillSwitch == model.KillAll || prof.KillSwitch == model.KillVPNApps
}

func (s *Service) onStatus(st supervisor.Status) {
	prof, _ := s.store.Snapshot()
	if s.rpc != nil {
		s.rpc.Notify(rpcapi.NStateChanged, rpcapi.StateNote{State: string(st.State), KillSwitch: string(prof.KillSwitch), Status: st})
	}
}

func (s *Service) isTunLUID(luid uint64) bool {
	ads, err := netmon.Adapters()
	if err != nil {
		return false
	}
	for _, a := range ads {
		if a.LUID == luid {
			return a.Name == configgen.TunName
		}
	}
	return false
}

// ---------------------------------------------------------------- firewall

// applyFirewall installs filters for the current profile. connected=false
// means the user disconnected: everything is removed.
func (s *Service) applyFirewall(connected bool, _ []netip.Prefix) {
	if s.fw == nil {
		return
	}
	prof, _ := s.store.Snapshot()
	pol := killswitch.Policy{Mode: killswitch.Off}
	if connected {
		pol = killswitch.Policy{
			Mode:        killswitch.Mode(prof.KillSwitch),
			BlockDNS:    true,
			ServiceExe:  s.selfExe,
			AllowLAN:    prof.AllowLAN,
			TunPrefixes: killswitch.TunPrefixes(configgen.TunAddr4, configgen.TunAddr6),
			VPNApps:     vpnAppPaths(prof),
		}
		if pol.Mode == "" {
			pol.Mode = killswitch.Off
		}
	}
	if err := s.fw.Apply(pol); err != nil {
		s.log.Error("firewall apply failed", "err", err)
	}
}

func vpnAppPaths(p model.Profile) []string {
	var out []string
	for _, r := range p.Rules {
		if r.Target.Kind != model.TargetServer {
			continue
		}
		if r.Match.Kind == model.MatchPath {
			out = append(out, r.Match.Value)
		}
		for _, l := range r.Learned {
			if l.Target.Kind == model.TargetServer && l.Match.Kind == model.MatchPath {
				out = append(out, l.Match.Value)
			}
		}
	}
	return out
}

// ---------------------------------------------------------------- connected flag

func (s *Service) flagPath() string { return filepath.Join(s.DataDir, "connected.flag") }

func (s *Service) setConnectedFlag(on bool) {
	if on {
		_ = os.WriteFile(s.flagPath(), []byte("1"), 0o644)
	} else {
		_ = os.Remove(s.flagPath())
	}
}

func (s *Service) wasConnected() bool {
	_, err := os.Stat(s.flagPath())
	return err == nil
}

// ---------------------------------------------------------------- process watcher

func (s *Service) startWatcher() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.watchStop != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.watchStop = cancel
	onStart := procs.Dedup(s.onProcStart)
	if err := procs.WatchETW(ctx, onStart); err != nil {
		s.log.Warn("process starts: ETW unavailable, polling every 2s", "err", err)
		go procs.Watch(ctx, 2*time.Second, onStart)
		return
	}
	s.log.Info("process starts: ETW")
	// safety net in case ETW drops events under load
	go procs.Watch(ctx, 15*time.Second, onStart)
}

func (s *Service) stopWatcher() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.watchStop != nil {
		s.watchStop()
		s.watchStop = nil
	}
}

func (s *Service) onProcStart(ev procs.StartEvent) {
	prof, _ := s.store.Snapshot()
	d := s.matcher.Decide(prof, ev.Parent.Path, ev.Child.Path)
	switch d.Action {
	case proctree.ActLearn:
		s.log.Info("child learned", "parent", ev.Parent.Path, "child", ev.Child.Path, "target", d.Target)
		s.learn(d.RuleID, model.Learned{Match: d.Child, Target: d.Target, Source: "inherit"})
	case proctree.ActAsk:
		key := d.Child.Key()
		s.mu.Lock()
		if t, ok := s.askedPaths[key]; ok && time.Since(t) < 10*time.Minute {
			s.mu.Unlock()
			return
		}
		s.askedPaths[key] = time.Now()
		id := newID()
		ask := rpcapi.ChildAsk{AskID: id, RuleID: d.RuleID, ParentPID: ev.Parent.PID, ParentPath: ev.Parent.Path,
			ChildPID: ev.Child.PID, ChildPath: ev.Child.Path, Child: d.Child, Shared: d.Shared, Time: time.Now()}
		s.asks[id] = pendingAsk{ask}
		s.mu.Unlock()
		s.log.Info("child ask", "parent", ev.Parent.Path, "child", ev.Child.Path)
		s.rpc.Notify(rpcapi.NChildAsk, ask)
	}
}

func (s *Service) learn(ruleID string, l model.Learned) {
	err := s.store.Update(func(p *model.Profile, _ *[]model.Server) error {
		for i := range p.Rules {
			if p.Rules[i].ID == ruleID {
				for _, ex := range p.Rules[i].Learned {
					if ex.Match.Key() == l.Match.Key() {
						return nil
					}
				}
				p.Rules[i].Learned = append(p.Rules[i].Learned, l)
				return nil
			}
		}
		return fmt.Errorf("rule %s not found", ruleID)
	})
	if err != nil {
		s.log.Warn("learn child", "err", err)
		return
	}
	s.afterChange()
}

// afterChange applies a store change to the running engine and tells clients.
func (s *Service) afterChange() {
	if _, err := s.sup.Apply(); err != nil {
		s.log.Warn("apply", "err", err)
	}
	if s.sup.Engine() != nil {
		s.applyFirewall(true, nil)
	}
	s.rpc.Notify(rpcapi.NDataChanged, map[string]any{})
}

func newID() string { return store.NewID() }

// migrateFromPortico copies servers, rules and settings from the data folder
// the app used before it was renamed (Portico → Kordo). It runs once: only
// when Kordo has no state yet. Secrets stay readable because the DPAPI
// entropy kept its original value.
func (s *Service) migrateFromPortico() {
	newState := filepath.Join(s.DataDir, "state.json")
	if _, err := os.Stat(newState); err == nil {
		return
	}
	oldDir := filepath.Join(filepath.Dir(s.DataDir), "Portico")
	data, err := os.ReadFile(filepath.Join(oldDir, "state.json"))
	if err != nil {
		return
	}
	if err := store.WriteFileAtomic(newState, data); err != nil {
		s.log.Warn("migrate settings from Portico", "err", err)
		return
	}
	if _, err := os.Stat(filepath.Join(oldDir, "connected.flag")); err == nil {
		_ = os.WriteFile(s.flagPath(), []byte("1"), 0o644)
	}
	s.log.Info("settings migrated from Portico", "from", oldDir)
}
