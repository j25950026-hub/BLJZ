package services

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-bot/internal/system"
)

type GeoPoint struct {
	Name string
	Lat  float64
	Lon  float64
}

type RouteDistanceResult struct {
	ResolvedFrom   string
	ResolvedTo     string
	DistanceKM     float64
	DurationMin    int
	FallbackUsed   bool
	FallbackReason string
}

type GeoConfig struct {
	GeocodeEndpoint string
	RouteEndpoint   string
	Timeout         time.Duration
	RetryMax        int
	Backoff         time.Duration
	CacheTTL        time.Duration
	CacheMaxEntries int
}

type geoCacheEntry struct {
	point     GeoPoint
	expiresAt time.Time
}

type GeoService struct {
	client          *http.Client
	geocodeEndpoint string
	routeEndpoint   string
	retryMax        int
	backoff         time.Duration
	cacheTTL        time.Duration
	cacheMaxEntries int
	cacheMu         sync.RWMutex
	cache           map[string]geoCacheEntry
	cacheOrder      []string
	now             func() time.Time
	sleep           func(time.Duration)
}

func NewGeoService(timeout time.Duration) *GeoService {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return NewGeoServiceWithConfig(GeoConfig{
		GeocodeEndpoint: "https://nominatim.openstreetmap.org/search",
		RouteEndpoint:   "https://router.project-osrm.org/route/v1/driving",
		Timeout:         timeout,
		RetryMax:        1,
		Backoff:         200 * time.Millisecond,
		CacheTTL:        30 * time.Minute,
		CacheMaxEntries: 1000,
	})
}

func NewGeoServiceWithConfig(cfg GeoConfig) *GeoService {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if strings.TrimSpace(cfg.GeocodeEndpoint) == "" {
		cfg.GeocodeEndpoint = "https://nominatim.openstreetmap.org/search"
	}
	if strings.TrimSpace(cfg.RouteEndpoint) == "" {
		cfg.RouteEndpoint = "https://router.project-osrm.org/route/v1/driving"
	}
	if cfg.RetryMax < 0 {
		cfg.RetryMax = 0
	}
	if cfg.Backoff <= 0 {
		cfg.Backoff = 200 * time.Millisecond
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = 30 * time.Minute
	}
	if cfg.CacheMaxEntries <= 0 {
		cfg.CacheMaxEntries = 1000
	}

	return &GeoService{
		client:          system.WrapHTTPClientWithTracing(&http.Client{Timeout: cfg.Timeout}, "geo-service"),
		geocodeEndpoint: strings.TrimSpace(cfg.GeocodeEndpoint),
		routeEndpoint:   strings.TrimSpace(cfg.RouteEndpoint),
		retryMax:        cfg.RetryMax,
		backoff:         cfg.Backoff,
		cacheTTL:        cfg.CacheTTL,
		cacheMaxEntries: cfg.CacheMaxEntries,
		cache:           make(map[string]geoCacheEntry),
		now:             time.Now,
		sleep:           time.Sleep,
	}
}

func (s *GeoService) Geocode(query string) (*GeoPoint, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, NewServiceError(ErrInvalidInput, "地址为空", false)
	}
	if hit := s.getCachedGeocode(query); hit != nil {
		return hit, nil
	}

	u, err := url.Parse(s.geocodeEndpoint)
	if err != nil {
		return nil, WrapServiceError(ErrUpstream, "地理接口地址非法", false, err)
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("format", "json")
	q.Set("limit", "1")
	u.RawQuery = q.Encode()

	resp, serr := s.doRequestWithRetry(func() (*http.Request, error) {
		req, reqErr := http.NewRequest(http.MethodGet, u.String(), nil)
		if reqErr != nil {
			return nil, reqErr
		}
		req.Header.Set("User-Agent", "ledger-go-bot/1.0")
		return req, nil
	})
	if serr != nil {
		return nil, serr
	}
	defer resp.Body.Close()

	var rows []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		return nil, WrapServiceError(ErrUpstream, "地理接口响应解析失败", false, err)
	}
	if len(rows) == 0 {
		return nil, NewServiceError(ErrNoData, "地址未命中", false)
	}
	lat, _ := toFloat(rows[0]["lat"])
	lon, _ := toFloat(rows[0]["lon"])
	name, _ := rows[0]["display_name"].(string)
	if name == "" {
		name = query
	}
	row := &GeoPoint{Name: name, Lat: lat, Lon: lon}
	s.setCachedGeocode(query, *row)
	return row, nil
}

func (s *GeoService) DistanceKM(fromName, toName string) (float64, error) {
	row, serr := s.RouteDistance(fromName, toName)
	if serr != nil {
		return 0, serr
	}
	if row == nil {
		return 0, NewServiceError(ErrNoData, "距离结果为空", false)
	}
	return row.DistanceKM, nil
}

func (s *GeoService) RouteDistance(source, target string) (*RouteDistanceResult, *ServiceError) {
	source = strings.TrimSpace(source)
	target = strings.TrimSpace(target)
	if source == "" || target == "" {
		return nil, NewServiceError(ErrInvalidInput, "起点或终点为空", false)
	}

	from, err := s.Geocode(source)
	if err != nil {
		return nil, convertToServiceErr(err, ErrUpstream, "起点地理编码失败")
	}
	to, err := s.Geocode(target)
	if err != nil {
		return nil, convertToServiceErr(err, ErrUpstream, "终点地理编码失败")
	}

	distMeters, durationSec, serr := s.fetchRouteDistance(*from, *to)
	if serr != nil {
		diagKM := roundToOneDecimal(haversine(from.Lat, from.Lon, to.Lat, to.Lon))
		return &RouteDistanceResult{
			ResolvedFrom:   from.Name,
			ResolvedTo:     to.Name,
			DistanceKM:     diagKM,
			DurationMin:    0,
			FallbackUsed:   true,
			FallbackReason: string(serr.Code),
		}, serr
	}

	durationMin := int(math.Ceil(durationSec / 60))
	if durationMin < 0 {
		durationMin = 0
	}

	return &RouteDistanceResult{
		ResolvedFrom: from.Name,
		ResolvedTo:   to.Name,
		DistanceKM:   roundToOneDecimal(distMeters / 1000),
		DurationMin:  durationMin,
	}, nil
}

func (s *GeoService) fetchRouteDistance(from, to GeoPoint) (float64, float64, *ServiceError) {
	endpoint := strings.TrimSpace(s.routeEndpoint)
	if endpoint == "" {
		return 0, 0, NewServiceError(ErrUpstream, "路线接口未配置", false)
	}

	resp, serr := s.doRequestWithRetry(func() (*http.Request, error) {
		u, err := s.buildRouteURL(endpoint, from, to)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "ledger-go-bot/1.0")
		return req, nil
	})
	if serr != nil {
		return 0, 0, serr
	}
	defer resp.Body.Close()

	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return 0, 0, WrapServiceError(ErrUpstream, "路线接口响应解析失败", false, err)
	}

	routes, ok := raw["routes"].([]any)
	if !ok {
		return 0, 0, NewServiceError(ErrUpstream, "路线接口返回结构异常", false)
	}
	if len(routes) == 0 {
		return 0, 0, NewServiceError(ErrUpstream, "路线未命中", false)
	}

	first, ok := routes[0].(map[string]any)
	if !ok {
		return 0, 0, NewServiceError(ErrUpstream, "路线对象结构异常", false)
	}
	dist, ok := toFloat(first["distance"])
	if !ok {
		return 0, 0, NewServiceError(ErrUpstream, "路线距离字段缺失", false)
	}
	duration, ok := toFloat(first["duration"])
	if !ok {
		return 0, 0, NewServiceError(ErrUpstream, "路线时长字段缺失", false)
	}
	return dist, duration, nil
}

func (s *GeoService) buildRouteURL(endpoint string, from, to GeoPoint) (string, error) {
	if strings.Contains(endpoint, "/route/v1/") {
		base := strings.TrimRight(endpoint, "/")
		return fmt.Sprintf("%s/%f,%f;%f,%f?overview=false", base, from.Lon, from.Lat, to.Lon, to.Lat), nil
	}

	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("from_lat", strconv.FormatFloat(from.Lat, 'f', -1, 64))
	q.Set("from_lon", strconv.FormatFloat(from.Lon, 'f', -1, 64))
	q.Set("to_lat", strconv.FormatFloat(to.Lat, 'f', -1, 64))
	q.Set("to_lon", strconv.FormatFloat(to.Lon, 'f', -1, 64))
	q.Set("overview", "false")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (s *GeoService) doRequestWithRetry(makeReq func() (*http.Request, error)) (*http.Response, *ServiceError) {
	attempts := s.retryMax + 1
	if attempts < 1 {
		attempts = 1
	}

	var lastErr *ServiceError
	for i := 0; i < attempts; i++ {
		req, err := makeReq()
		if err != nil {
			return nil, WrapServiceError(ErrUpstream, "请求构造失败", false, err)
		}
		resp, err := s.client.Do(req)
		if err != nil {
			serr := classifyHTTPError(err)
			lastErr = serr
			if serr.Retryable && i < attempts-1 {
				s.sleepWithBackoff(i)
				continue
			}
			return nil, serr
		}

		if resp.StatusCode >= 500 {
			_ = resp.Body.Close()
			lastErr = NewServiceError(ErrUpstream, fmt.Sprintf("上游状态异常: %d", resp.StatusCode), true)
			if i < attempts-1 {
				s.sleepWithBackoff(i)
				continue
			}
			return nil, lastErr
		}
		if resp.StatusCode >= 300 {
			_ = resp.Body.Close()
			return nil, NewServiceError(ErrUpstream, fmt.Sprintf("上游状态异常: %d", resp.StatusCode), false)
		}
		return resp, nil
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, NewServiceError(ErrUpstream, "上游请求失败", false)
}

func (s *GeoService) sleepWithBackoff(attempt int) {
	if s.sleep == nil {
		return
	}
	wait := s.backoff
	if wait <= 0 {
		wait = 200 * time.Millisecond
	}
	if attempt > 0 {
		wait = wait * time.Duration(1<<attempt)
	}
	s.sleep(wait)
}

func classifyHTTPError(err error) *ServiceError {
	if err == nil {
		return nil
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded") {
		return WrapServiceError(ErrTimeout, "请求超时", true, err)
	}
	return WrapServiceError(ErrUpstream, "请求失败", true, err)
}

func convertToServiceErr(err error, fallbackCode ServiceErrorCode, msg string) *ServiceError {
	if serr, ok := AsServiceError(err); ok {
		return serr
	}
	return WrapServiceError(fallbackCode, msg, false, err)
}

func (s *GeoService) getCachedGeocode(query string) *GeoPoint {
	key := normalizeGeoCacheKey(query)
	if key == "" || s.cacheTTL <= 0 {
		return nil
	}

	now := time.Now()
	if s.now != nil {
		now = s.now()
	}

	s.cacheMu.RLock()
	entry, ok := s.cache[key]
	s.cacheMu.RUnlock()
	if !ok {
		return nil
	}
	if now.After(entry.expiresAt) {
		s.cacheMu.Lock()
		delete(s.cache, key)
		s.cacheMu.Unlock()
		return nil
	}
	point := entry.point
	return &point
}

func (s *GeoService) setCachedGeocode(query string, point GeoPoint) {
	key := normalizeGeoCacheKey(query)
	if key == "" || s.cacheTTL <= 0 {
		return
	}

	now := time.Now()
	if s.now != nil {
		now = s.now()
	}

	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	if s.cache == nil {
		s.cache = make(map[string]geoCacheEntry)
	}

	if _, exists := s.cache[key]; !exists {
		s.cacheOrder = append(s.cacheOrder, key)
	}
	s.cache[key] = geoCacheEntry{
		point:     point,
		expiresAt: now.Add(s.cacheTTL),
	}

	for len(s.cache) > s.cacheMaxEntries && len(s.cacheOrder) > 0 {
		oldest := s.cacheOrder[0]
		s.cacheOrder = s.cacheOrder[1:]
		delete(s.cache, oldest)
	}
}

func normalizeGeoCacheKey(raw string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(raw))), " ")
}

func roundToOneDecimal(v float64) float64 {
	return math.Round(v*10) / 10
}

func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371.0
	dLat := toRad(lat2 - lat1)
	dLon := toRad(lon2 - lon1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(toRad(lat1))*math.Cos(toRad(lat2))*math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return r * c
}

func toRad(v float64) float64 {
	return v * math.Pi / 180
}
