package mobile

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/PeaChack/Kordo/core/geo"
)

// Privacy settings: making the phone look like it is where the server is.
type privacy struct {
	MockLocation bool   `json:"mockLocation"`         // feed the server's location to apps (developer options)
	MockServer   string `json:"mockServer,omitempty"` // server id; empty = the server most apps use
	JitterM      int    `json:"jitterM"`              // random offset from the city point, metres
}

func defaultPrivacy() privacy { return privacy{JitterM: 1500} }

// geoLookup is replaced in tests.
var geoLookup = geo.Lookup

// PrivacySettings returns the privacy settings as JSON.
func PrivacySettings() string {
	mu.Lock()
	defer mu.Unlock()
	return toJSON(st.Privacy)
}

// SetPrivacySettings stores privacy settings given as JSON.
func SetPrivacySettings(js string) error {
	var p privacy
	if err := json.Unmarshal([]byte(js), &p); err != nil {
		return err
	}
	if p.JitterM < 0 || p.JitterM > 20000 {
		return errors.New("jitter must be 0..20000 m")
	}
	mu.Lock()
	defer mu.Unlock()
	st.Privacy = p
	return saveLocked()
}

// mockServerLocked picks the server whose location the phone pretends to be at.
func mockServerLocked() string {
	return geo.MainServer(st.Profile, st.Servers, st.Privacy.MockServer)
}

// ServerGeo returns the location of a server as JSON
// {ip, country, countryCode, city, lat, lon, timeZone, manual, updated}.
// It is looked up once (by the server's address) and cached; refresh forces a
// new lookup unless the location was set by hand. id == "" means the server
// used for fake location.
func ServerGeo(id string, refresh bool) (string, error) {
	mu.Lock()
	if id == "" {
		id = mockServerLocked()
	}
	if id == "" {
		mu.Unlock()
		return "", errors.New("add a server first")
	}
	cached, ok := st.Geo[id]
	var host string
	for _, srv := range st.Servers {
		if srv.ID == id {
			host = geo.ServerHost(srv)
		}
	}
	mu.Unlock()
	if ok && (cached.Manual || !refresh) {
		return toJSON(cached), nil
	}
	if host == "" {
		return "", errors.New("server not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ip, err := geo.ResolveIP(ctx, host)
	if err != nil {
		return "", err
	}
	loc, err := geoLookup(ctx, nil, ip)
	if err != nil {
		return "", err
	}
	mu.Lock()
	defer mu.Unlock()
	if st.Geo == nil {
		st.Geo = map[string]geo.Location{}
	}
	st.Geo[id] = loc
	return toJSON(loc), saveLocked()
}

// SetServerGeo sets a server's location by hand (countryCode and timeZone
// may be empty). An empty id means the fake-location server.
func SetServerGeo(id string, lat, lon float64, countryCode, timeZone string) error {
	loc := geo.Location{Lat: lat, Lon: lon, CountryCode: strings.ToUpper(strings.TrimSpace(countryCode)),
		TimeZone: strings.TrimSpace(timeZone), Manual: true, Updated: time.Now().Unix()}
	if !loc.Valid() {
		return errors.New("coordinates out of range")
	}
	mu.Lock()
	defer mu.Unlock()
	if id == "" {
		id = mockServerLocked()
	}
	if id == "" {
		return errors.New("add a server first")
	}
	if st.Geo == nil {
		st.Geo = map[string]geo.Location{}
	}
	if old, ok := st.Geo[id]; ok && loc.TimeZone == "" {
		loc.TimeZone, loc.Country = old.TimeZone, old.Country
		if loc.CountryCode == "" {
			loc.CountryCode = old.CountryCode
		}
	}
	st.Geo[id] = loc
	return saveLocked()
}

// ClearServerGeo forgets a server's location (manual or looked up).
func ClearServerGeo(id string) error {
	mu.Lock()
	defer mu.Unlock()
	if id == "" {
		id = mockServerLocked()
	}
	delete(st.Geo, id)
	return saveLocked()
}

// MockPoint returns the point to report as the phone's location for this
// connection: the fake-location server's place, moved randomly within the
// jitter radius. JSON {lat, lon, countryCode, timeZone, city, server}.
func MockPoint() (string, error) {
	js, err := ServerGeo("", false)
	if err != nil {
		return "", err
	}
	var loc geo.Location
	_ = json.Unmarshal([]byte(js), &loc)
	mu.Lock()
	jit := st.Privacy.JitterM
	server := mockServerLocked()
	mu.Unlock()
	lat, lon := geo.Jitter(loc.Lat, loc.Lon, float64(jit), nil)
	return toJSON(map[string]any{
		"lat": lat, "lon": lon, "countryCode": loc.CountryCode, "timeZone": loc.TimeZone,
		"city": loc.City, "server": server,
	}), nil
}

// CachedServerGeo returns the saved location of a server (same JSON as
// ServerGeo) or "" when it was never looked up. Never uses the network.
func CachedServerGeo(id string) string {
	mu.Lock()
	defer mu.Unlock()
	if id == "" {
		id = mockServerLocked()
	}
	if loc, ok := st.Geo[id]; ok {
		return toJSON(loc)
	}
	return ""
}
