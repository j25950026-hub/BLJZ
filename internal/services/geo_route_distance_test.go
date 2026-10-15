package services

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRouteDistanceSuccess(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/geocode", func(w http.ResponseWriter, r *http.Request) {
		query := strings.TrimSpace(r.URL.Query().Get("q"))
		switch query {
		case "北京":
			_, _ = w.Write([]byte(`[{"lat":"39.9042","lon":"116.4074","display_name":"北京"}]`))
		case "上海":
			_, _ = w.Write([]byte(`[{"lat":"31.2304","lon":"121.4737","display_name":"上海"}]`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	})
	mux.HandleFunc("/route", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"routes":[{"distance":1213000.0,"duration":50640.0}]}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	svc := NewGeoServiceWithConfig(GeoConfig{
		GeocodeEndpoint: ts.URL + "/geocode",
		RouteEndpoint:   ts.URL + "/route",
		Timeout:         2 * time.Second,
		RetryMax:        1,
		Backoff:         10 * time.Millisecond,
		CacheTTL:        30 * time.Minute,
		CacheMaxEntries: 1000,
	})

	row, serr := svc.RouteDistance("北京", "上海")
	if serr != nil {
		t.Fatalf("RouteDistance unexpected error: %+v", serr)
	}
	if row == nil {
		t.Fatal("RouteDistance returned nil result")
	}
	if row.ResolvedFrom == "" || row.ResolvedTo == "" {
		t.Fatalf("resolved names must not be empty: %+v", row)
	}
	if row.DistanceKM != 1213.0 {
		t.Fatalf("DistanceKM mismatch: want=1213.0 got=%.1f", row.DistanceKM)
	}
	if row.DurationMin != 844 {
		t.Fatalf("DurationMin mismatch: want=844 got=%d", row.DurationMin)
	}
	if row.FallbackUsed {
		t.Fatalf("FallbackUsed should be false on success: %+v", row)
	}
}

func TestRouteDistanceGeocodeMiss(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer ts.Close()

	svc := NewGeoServiceWithConfig(GeoConfig{
		GeocodeEndpoint: ts.URL,
		RouteEndpoint:   ts.URL + "/route",
		Timeout:         2 * time.Second,
		RetryMax:        1,
		Backoff:         10 * time.Millisecond,
	})

	_, serr := svc.RouteDistance("A", "B")
	if serr == nil || serr.Code != ErrNoData {
		t.Fatalf("RouteDistance geocode miss should return ErrNoData, got=%+v", serr)
	}
}

func TestRouteDistanceRouteFailure(t *testing.T) {
	var routeCalls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/geocode", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"lat":"39.9042","lon":"116.4074","display_name":"北京"}]`))
	})
	mux.HandleFunc("/route", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&routeCalls, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"upstream failed"}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	svc := NewGeoServiceWithConfig(GeoConfig{
		GeocodeEndpoint: ts.URL + "/geocode",
		RouteEndpoint:   ts.URL + "/route",
		Timeout:         2 * time.Second,
		RetryMax:        1,
		Backoff:         10 * time.Millisecond,
	})

	row, serr := svc.RouteDistance("北京", "上海")
	if serr == nil || serr.Code != ErrUpstream {
		t.Fatalf("RouteDistance route failure should return ErrUpstream, got=%+v", serr)
	}
	if row == nil || !row.FallbackUsed {
		t.Fatalf("RouteDistance should provide fallback diagnostics on route failure, got row=%+v", row)
	}
	if got := atomic.LoadInt32(&routeCalls); got != 2 {
		t.Fatalf("Route endpoint should be called 2 times when RetryMax=1, got=%d", got)
	}
}

func TestRouteDistanceTimeoutRetryBackoff(t *testing.T) {
	var routeCalls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/geocode", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"lat":"39.9042","lon":"116.4074","display_name":"北京"}]`))
	})
	mux.HandleFunc("/route", func(w http.ResponseWriter, r *http.Request) {
		call := atomic.AddInt32(&routeCalls, 1)
		if call == 1 {
			time.Sleep(120 * time.Millisecond)
			return
		}
		_, _ = w.Write([]byte(`{"routes":[{"distance":1000.0,"duration":180.0}]}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	svc := NewGeoServiceWithConfig(GeoConfig{
		GeocodeEndpoint: ts.URL + "/geocode",
		RouteEndpoint:   ts.URL + "/route",
		Timeout:         80 * time.Millisecond,
		RetryMax:        1,
		Backoff:         10 * time.Millisecond,
	})

	row, serr := svc.RouteDistance("北京", "上海")
	if serr != nil {
		t.Fatalf("RouteDistance should succeed after timeout retry, got=%+v", serr)
	}
	if row == nil || row.DurationMin != 3 {
		t.Fatalf("unexpected row after timeout retry: %+v", row)
	}
	if got := atomic.LoadInt32(&routeCalls); got != 2 {
		t.Fatalf("route calls mismatch: want=2 got=%d", got)
	}
}
