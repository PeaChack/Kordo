package geo

import (
	"context"
	"io"
	"math/rand/v2"
	"net/http"
	"strings"
	"testing"
)

type fake map[string]struct {
	code int
	body string
}

func (f fake) Do(r *http.Request) (*http.Response, error) {
	a, ok := f[r.URL.Host]
	if !ok {
		return &http.Response{StatusCode: 502, Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	return &http.Response{StatusCode: a.code, Body: io.NopCloser(strings.NewReader(a.body))}, nil
}

const ipwho = `{"ip":"203.0.113.9","success":true,"country":"Netherlands","country_code":"NL","city":"Amsterdam","latitude":52.37,"longitude":4.89,"timezone":{"id":"Europe/Amsterdam"}}`
const ipapi = `{"ip":"203.0.113.9","country_name":"Germany","country_code":"de","city":"Frankfurt","latitude":50.11,"longitude":8.68,"timezone":"Europe/Berlin"}`

func TestLookupFirstProvider(t *testing.T) {
	loc, err := Lookup(context.Background(), fake{"ipwho.is": {200, ipwho}}, "203.0.113.9")
	if err != nil {
		t.Fatal(err)
	}
	if loc.CountryCode != "NL" || loc.City != "Amsterdam" || loc.TimeZone != "Europe/Amsterdam" || loc.Updated == 0 {
		t.Fatalf("%+v", loc)
	}
}

func TestLookupFallsBack(t *testing.T) {
	c := fake{
		"ipwho.is": {200, `{"success":false,"message":"rate limited"}`},
		"ipapi.co": {200, ipapi},
	}
	loc, err := Lookup(context.Background(), c, "")
	if err != nil {
		t.Fatal(err)
	}
	if loc.CountryCode != "DE" || loc.TimeZone != "Europe/Berlin" {
		t.Fatalf("%+v", loc)
	}
}

func TestLookupAllFail(t *testing.T) {
	_, err := Lookup(context.Background(), fake{"ipwho.is": {200, `{"success":true,"latitude":0,"longitude":0}`}}, "x")
	if err == nil || !strings.Contains(err.Error(), "ipapi.co") {
		t.Fatalf("err = %v", err)
	}
}

func TestJitterStaysInRadius(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 1000; i++ {
		lat, lon := Jitter(52.37, 4.89, 2000, r)
		if d := Distance(52.37, 4.89, lat, lon); d > 2001 {
			t.Fatalf("moved %.0f m", d)
		}
	}
	if d := Distance(52.37, 4.89, 48.86, 2.35); d < 420000 || d > 440000 {
		t.Fatalf("Amsterdam-Paris = %.0f m", d)
	}
}
