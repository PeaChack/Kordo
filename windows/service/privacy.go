//go:build windows

package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows/registry"

	"github.com/PeaChack/Kordo/core/geo"
	"github.com/PeaChack/Kordo/core/rpcapi"
	"github.com/PeaChack/Kordo/windows/privacy"
	"github.com/PeaChack/Kordo/windows/store"
)

// What Kordo changed in the system and must put back on disconnect. It is
// kept in a file so that a crash or a reboot while connected is undone at
// the next start.
type privacyRestore struct {
	LocationSet  bool   `json:"locationSet,omitempty"`
	LocationPrev string `json:"locationPrev,omitempty"` // HKLM "Value" before Kordo, "" = not set
	TimeZone     string `json:"timeZone,omitempty"`     // Windows zone before Kordo
}

type privacyState struct {
	mu      sync.Mutex // serialises apply/restore
	geoMu   sync.Mutex
	geo     map[string]geo.Location
	geoLoad bool
	lastErr string
}

func (s *Service) restorePath() string { return filepath.Join(s.DataDir, "privacy-restore.json") }
func (s *Service) geoPath() string     { return filepath.Join(s.DataDir, "geo.json") }

func (s *Service) loadRestore() privacyRestore {
	var r privacyRestore
	if b, err := os.ReadFile(s.restorePath()); err == nil {
		_ = json.Unmarshal(b, &r)
	}
	return r
}

func (s *Service) saveRestore(r privacyRestore) {
	if r == (privacyRestore{}) {
		_ = os.Remove(s.restorePath())
		return
	}
	b, _ := json.Marshal(r)
	if err := store.WriteFileAtomic(s.restorePath(), b); err != nil {
		s.log.Warn("privacy: save restore file", "err", err)
	}
}

// applyPrivacy turns location off and switches the time zone while
// connected (if enabled) and restores both otherwise.
func (s *Service) applyPrivacy(connected bool) {
	s.priv.mu.Lock()
	defer s.priv.mu.Unlock()
	prof, _ := s.store.Snapshot()
	r := s.loadRestore()
	var errs []string

	// Location: the device-wide switch in Settings → Privacy → Location.
	switch wantGuard := connected && prof.LocationGuard; {
	case wantGuard && !r.LocationSet:
		prev := privacy.Switch(registry.LOCAL_MACHINE, "location")
		if err := privacy.SetDeviceSwitch("location", "Deny"); err != nil {
			errs = append(errs, "location: "+err.Error())
		} else {
			r.LocationSet, r.LocationPrev = true, prev
			s.log.Info("privacy: location turned off while connected", "was", prev)
		}
	case !wantGuard && r.LocationSet:
		if err := privacy.SetDeviceSwitch("location", r.LocationPrev); err != nil {
			errs = append(errs, "location restore: "+err.Error())
		} else {
			s.log.Info("privacy: location restored", "value", r.LocationPrev)
			r.LocationSet, r.LocationPrev = false, ""
		}
	}

	// Time zone of the server.
	wantZone := ""
	if connected && prof.ServerTimeZone {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		id, _ := s.mainServer()
		loc, err := s.serverGeo(ctx, id, false)
		cancel()
		switch {
		case err != nil:
			errs = append(errs, "server location: "+err.Error())
		case loc.TimeZone == "":
			errs = append(errs, "server location has no time zone")
		default:
			if wantZone, err = privacy.WindowsZone(loc.TimeZone); err != nil {
				errs = append(errs, err.Error())
			}
		}
	}
	cur := privacy.TimeZone()
	switch {
	case wantZone != "":
		if r.TimeZone == "" {
			r.TimeZone = cur
		}
		if !strings.EqualFold(cur, wantZone) {
			if err := privacy.SetTimeZone(wantZone); err != nil {
				errs = append(errs, err.Error())
			} else {
				s.log.Info("privacy: time zone of the server", "zone", wantZone, "was", r.TimeZone)
			}
		}
	case r.TimeZone != "" && !(connected && prof.ServerTimeZone):
		if strings.EqualFold(cur, r.TimeZone) {
			r.TimeZone = ""
		} else if err := privacy.SetTimeZone(r.TimeZone); err != nil {
			errs = append(errs, "time zone restore: "+err.Error())
		} else {
			s.log.Info("privacy: time zone restored", "zone", r.TimeZone)
			r.TimeZone = ""
		}
	}

	s.saveRestore(r)
	s.priv.lastErr = strings.Join(errs, "; ")
	if len(errs) > 0 {
		s.log.Warn("privacy", "err", s.priv.lastErr)
	}
}

// mainServer is the server most of the traffic goes through.
func (s *Service) mainServer() (string, string) {
	prof, servers := s.store.Snapshot()
	id := geo.MainServer(prof, servers, "")
	for _, srv := range servers {
		if srv.ID == id {
			return id, srv.Name
		}
	}
	return id, ""
}

func (s *Service) loadGeoLocked() {
	if s.priv.geoLoad {
		return
	}
	s.priv.geoLoad = true
	s.priv.geo = map[string]geo.Location{}
	if b, err := os.ReadFile(s.geoPath()); err == nil {
		_ = json.Unmarshal(b, &s.priv.geo)
	}
}

func (s *Service) saveGeoLocked() {
	b, _ := json.MarshalIndent(s.priv.geo, "", "  ")
	_ = store.WriteFileAtomic(s.geoPath(), b)
}

// serverGeo returns the cached location of a server, looking it up by the
// server's IP when missing (or when refresh is set and it was not set by hand).
func (s *Service) serverGeo(ctx context.Context, id string, refresh bool) (geo.Location, error) {
	if id == "" {
		return geo.Location{}, errors.New("no servers")
	}
	s.priv.geoMu.Lock()
	s.loadGeoLocked()
	cached, ok := s.priv.geo[id]
	s.priv.geoMu.Unlock()
	if ok && (cached.Manual || !refresh) {
		return cached, nil
	}
	srv, found := s.store.Server(id)
	if !found {
		return geo.Location{}, errors.New("server not found")
	}
	host := geo.ServerHost(srv)
	if host == "" {
		return geo.Location{}, errors.New("server address unknown")
	}
	ip, err := geo.ResolveIP(ctx, host)
	if err != nil {
		return geo.Location{}, err
	}
	loc, err := geo.Lookup(ctx, nil, ip)
	if err != nil {
		return geo.Location{}, err
	}
	s.priv.geoMu.Lock()
	s.priv.geo[id] = loc
	s.saveGeoLocked()
	s.priv.geoMu.Unlock()
	return loc, nil
}

func (s *Service) setServerPlace(p rpcapi.PrivacyPlace) error {
	id, _ := s.mainServer()
	if id == "" {
		return errors.New("add a server first")
	}
	s.priv.geoMu.Lock()
	defer s.priv.geoMu.Unlock()
	s.loadGeoLocked()
	if p.Clear {
		delete(s.priv.geo, id)
		s.saveGeoLocked()
		return nil
	}
	loc := geo.Location{Lat: p.Lat, Lon: p.Lon, CountryCode: strings.ToUpper(strings.TrimSpace(p.CountryCode)),
		TimeZone: strings.TrimSpace(p.TimeZone), Manual: true, Updated: time.Now().Unix()}
	if !loc.Valid() {
		return errors.New("coordinates out of range")
	}
	if loc.TimeZone != "" {
		if _, err := privacy.WindowsZone(loc.TimeZone); err != nil {
			return errors.New("unknown time zone " + loc.TimeZone + " (use names like Europe/Amsterdam)")
		}
	}
	if old, ok := s.priv.geo[id]; ok {
		if loc.TimeZone == "" {
			loc.TimeZone = old.TimeZone
		}
		if loc.CountryCode == "" {
			loc.CountryCode = old.CountryCode
		}
		if loc.CountryCode == old.CountryCode {
			loc.Country = old.Country
		}
	}
	s.priv.geo[id] = loc
	s.saveGeoLocked()
	return nil
}

func (s *Service) privacyStatus(ctx context.Context, refresh bool) rpcapi.PrivacyStatus {
	out := rpcapi.PrivacyStatus{
		TimeZone:       privacy.TimeZone(),
		DeviceLocation: privacy.Switch(registry.LOCAL_MACHINE, "location"),
	}
	out.ServerID, out.ServerName = s.mainServer()
	if out.ServerID != "" {
		lctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		loc, err := s.serverGeo(lctx, out.ServerID, refresh)
		cancel()
		if err != nil {
			out.LookupError = err.Error()
		} else {
			out.Server = &loc
			if loc.TimeZone != "" {
				out.ServerZone, _ = privacy.WindowsZone(loc.TimeZone)
			}
		}
	}
	r := s.loadRestore()
	out.LocationGuard = r.LocationSet
	out.ZoneChanged = r.TimeZone != ""
	out.OriginalZone = r.TimeZone
	s.priv.mu.Lock()
	out.Error = s.priv.lastErr
	s.priv.mu.Unlock()
	return out
}

// privacyAfterChange re-applies after settings changed; the time zone
// lookup may take a while, so it runs in the background.
func (s *Service) privacyAfterChange() {
	connected := s.sup != nil && s.sup.Engine() != nil
	go s.applyPrivacy(connected)
}
