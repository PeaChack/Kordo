// Package geo finds where a server is: country, city, coordinates and time
// zone of an IP address. Kordo uses it to make the device look like it is
// where the VPN server is (fake GPS, time zone) and to compare the device's
// own settings (time zone, SIM country, region) with the server's country.
package geo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"
)

// Location of a server. Manual is true when the user set it by hand.
type Location struct {
	IP          string  `json:"ip,omitempty"`
	Country     string  `json:"country,omitempty"`
	CountryCode string  `json:"countryCode,omitempty"` // ISO 3166-1 alpha-2, upper case
	City        string  `json:"city,omitempty"`
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
	TimeZone    string  `json:"timeZone,omitempty"` // IANA, e.g. Europe/Amsterdam
	Manual      bool    `json:"manual,omitempty"`
	Updated     int64   `json:"updated,omitempty"` // unix seconds
}

func (l Location) Valid() bool {
	return !(l.Lat == 0 && l.Lon == 0) && l.Lat >= -90 && l.Lat <= 90 && l.Lon >= -180 && l.Lon <= 180
}

// Doer is the HTTP client used for lookups. On Windows the service passes a
// client that dials through the server's outbound, so the answer is about
// the real exit address.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Providers, tried in order. ip == "" asks about the caller's own address.
var Providers = []Provider{ipwhoIs{}, ipapiCo{}}

type Provider interface {
	Name() string
	URL(ip string) string
	Parse(body []byte) (Location, error)
}

// Lookup asks the providers in turn and returns the first good answer.
func Lookup(ctx context.Context, c Doer, ip string) (Location, error) {
	if c == nil {
		c = &http.Client{Timeout: 15 * time.Second}
	}
	var errs []string
	for _, p := range Providers {
		loc, err := lookup(ctx, c, p, ip)
		if err == nil {
			loc.Updated = time.Now().Unix()
			return loc, nil
		}
		errs = append(errs, p.Name()+": "+err.Error())
	}
	return Location{}, errors.New("geo lookup failed: " + strings.Join(errs, "; "))
}

func lookup(ctx context.Context, c Doer, p Provider, ip string) (Location, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.URL(ip), nil)
	if err != nil {
		return Location{}, err
	}
	req.Header.Set("User-Agent", "Kordo")
	resp, err := c.Do(req)
	if err != nil {
		return Location{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Location{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return Location{}, err
	}
	loc, err := p.Parse(body)
	if err != nil {
		return Location{}, err
	}
	loc.CountryCode = strings.ToUpper(loc.CountryCode)
	if !loc.Valid() || len(loc.CountryCode) != 2 {
		return Location{}, errors.New("incomplete answer")
	}
	return loc, nil
}

type ipwhoIs struct{}

func (ipwhoIs) Name() string { return "ipwho.is" }
func (ipwhoIs) URL(ip string) string {
	return "https://ipwho.is/" + ip
}
func (ipwhoIs) Parse(b []byte) (Location, error) {
	var r struct {
		Success     bool    `json:"success"`
		Message     string  `json:"message"`
		IP          string  `json:"ip"`
		Country     string  `json:"country"`
		CountryCode string  `json:"country_code"`
		City        string  `json:"city"`
		Lat         float64 `json:"latitude"`
		Lon         float64 `json:"longitude"`
		TimeZone    struct {
			ID string `json:"id"`
		} `json:"timezone"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return Location{}, err
	}
	if !r.Success {
		return Location{}, errors.New(r.Message)
	}
	return Location{IP: r.IP, Country: r.Country, CountryCode: r.CountryCode, City: r.City, Lat: r.Lat, Lon: r.Lon, TimeZone: r.TimeZone.ID}, nil
}

type ipapiCo struct{}

func (ipapiCo) Name() string { return "ipapi.co" }
func (ipapiCo) URL(ip string) string {
	if ip == "" {
		return "https://ipapi.co/json/"
	}
	return "https://ipapi.co/" + ip + "/json/"
}
func (ipapiCo) Parse(b []byte) (Location, error) {
	var r struct {
		Error       bool    `json:"error"`
		Reason      string  `json:"reason"`
		IP          string  `json:"ip"`
		Country     string  `json:"country_name"`
		CountryCode string  `json:"country_code"`
		City        string  `json:"city"`
		Lat         float64 `json:"latitude"`
		Lon         float64 `json:"longitude"`
		TimeZone    string  `json:"timezone"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return Location{}, err
	}
	if r.Error {
		return Location{}, errors.New(r.Reason)
	}
	return Location{IP: r.IP, Country: r.Country, CountryCode: r.CountryCode, City: r.City, Lat: r.Lat, Lon: r.Lon, TimeZone: r.TimeZone}, nil
}

// Jitter moves a point to a random place within radiusM metres, so a fake
// position is not the exact city centre every geolocation database returns.
func Jitter(lat, lon, radiusM float64, r *rand.Rand) (float64, float64) {
	if r == nil {
		r = rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
	}
	d := radiusM * math.Sqrt(r.Float64()) // uniform over the disc
	a := r.Float64() * 2 * math.Pi
	const earth = 6371000.0
	dLat := (d * math.Cos(a)) / earth
	dLon := (d * math.Sin(a)) / (earth * math.Cos(lat*math.Pi/180))
	return lat + dLat*180/math.Pi, lon + dLon*180/math.Pi
}

// Distance in metres between two points (haversine).
func Distance(lat1, lon1, lat2, lon2 float64) float64 {
	const earth = 6371000.0
	toRad := math.Pi / 180
	dLat, dLon := (lat2-lat1)*toRad, (lon2-lon1)*toRad
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*toRad)*math.Cos(lat2*toRad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earth * math.Asin(math.Sqrt(h))
}
