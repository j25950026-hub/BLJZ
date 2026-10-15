package services

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"go-bot/internal/system"
)

type TimeResult struct {
	Region        string
	Timezone      string
	TimezoneLabel string
	TimeText      string
	Weekday       string
}

type geoRow struct {
	Lat         string `json:"lat"`
	Lon         string `json:"lon"`
	DisplayName string `json:"display_name"`
}

type WorldTimeService struct {
	client *http.Client
	tzMap  map[string]string

	cacheMu   sync.RWMutex
	cacheAt   time.Time
	zoneCache []string
}

func NewWorldTimeService() *WorldTimeService {
	aliasMap := buildWorldTimeAliasMap()
	return &WorldTimeService{
		client:    system.WrapHTTPClientWithTracing(&http.Client{Timeout: 10 * time.Second}, "worldtime-service"),
		tzMap:     aliasMap,
		zoneCache: buildWorldTimeLocalZones(aliasMap),
	}
}

func (s *WorldTimeService) Query(region string) (*TimeResult, error) {
	original := strings.TrimSpace(region)
	if original == "" {
		return nil, fmt.Errorf("地区不能为空")
	}

	tzName := s.lookupAlias(original)
	if tzName == "" && validTZ(original) {
		tzName = strings.TrimSpace(original)
	}
	if tzName == "" {
		if byGeo, err := s.lookupByGeo(original); err == nil {
			tzName = byGeo
		}
	}
	if tzName == "" {
		if byList, err := s.lookupByZoneList(original); err == nil {
			tzName = byList
		}
	}
	if tzName == "" {
		return nil, fmt.Errorf("暂不支持该地区")
	}

	loc, err := time.LoadLocation(tzName)
	if err != nil {
		return nil, err
	}
	now := time.Now().In(loc)
	return &TimeResult{
		Region:        original,
		Timezone:      tzName,
		TimezoneLabel: s.displayTimezoneLabel(original, tzName),
		TimeText:      now.Format("2006-01-02 15:04:05"),
		Weekday:       formatChineseWeekday(now.Weekday()),
	}, nil
}

func (s *WorldTimeService) displayTimezoneLabel(region, timezone string) string {
	regionText := strings.TrimSpace(region)
	if label := defaultChineseTimezoneLabel(strings.TrimSpace(timezone)); label != "" {
		return label
	}
	if containsChineseRune(regionText) {
		return ensureTimeSuffix(regionText)
	}
	return strings.TrimSpace(timezone)
}

func ensureTimeSuffix(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || strings.HasSuffix(trimmed, "时间") {
		return trimmed
	}
	return trimmed + "时间"
}

func containsChineseRune(text string) bool {
	for _, r := range text {
		if r >= '\u4e00' && r <= '\u9fff' {
			return true
		}
	}
	return false
}

func defaultChineseTimezoneLabel(timezone string) string {
	switch timezone {
	case "Asia/Shanghai":
		return "中国/上海"
	case "Asia/Hong_Kong":
		return "中国香港/香港"
	case "Asia/Macau":
		return "中国澳门/澳门"
	case "Asia/Taipei":
		return "中国台湾/台北"
	case "Asia/Tokyo":
		return "日本/东京"
	case "Asia/Seoul":
		return "韩国/首尔"
	case "Asia/Singapore":
		return "新加坡/新加坡"
	case "Asia/Bangkok":
		return "泰国/曼谷"
	case "Asia/Ho_Chi_Minh":
		return "越南/胡志明"
	case "Asia/Phnom_Penh":
		return "柬埔寨/金边"
	case "Asia/Kuala_Lumpur":
		return "马来西亚/吉隆坡"
	case "Asia/Jakarta":
		return "印度尼西亚/雅加达"
	case "Asia/Manila":
		return "菲律宾/马尼拉"
	case "Asia/Kolkata":
		return "印度/加尔各答"
	case "Asia/Dubai":
		return "阿联酋/迪拜"
	case "Europe/London":
		return "英国/伦敦"
	case "Europe/Paris":
		return "法国/巴黎"
	case "Europe/Berlin":
		return "德国/柏林"
	case "Europe/Rome":
		return "意大利/罗马"
	case "Europe/Madrid":
		return "西班牙/马德里"
	case "Europe/Moscow":
		return "俄罗斯/莫斯科"
	case "America/New_York":
		return "美国/纽约"
	case "America/Chicago":
		return "美国/芝加哥"
	case "America/Denver":
		return "美国/丹佛"
	case "America/Los_Angeles":
		return "美国/洛杉矶"
	case "America/Anchorage":
		return "美国/安克雷奇"
	case "Pacific/Honolulu":
		return "美国/檀香山"
	case "America/Toronto":
		return "加拿大/多伦多"
	case "America/Vancouver":
		return "加拿大/温哥华"
	case "America/Mexico_City":
		return "墨西哥/墨西哥城"
	case "America/Sao_Paulo":
		return "巴西/圣保罗"
	case "America/Argentina/Buenos_Aires":
		return "阿根廷/布宜诺斯艾利斯"
	case "America/Santiago":
		return "智利/圣地亚哥"
	case "Australia/Sydney":
		return "澳大利亚/悉尼"
	case "Australia/Perth":
		return "澳大利亚/珀斯"
	case "Pacific/Auckland":
		return "新西兰/奥克兰"
	case "UTC", "Etc/UTC", "Etc/GMT":
		return "协调世界时"
	default:
		return ""
	}
}

func formatChineseWeekday(weekday time.Weekday) string {
	switch weekday {
	case time.Sunday:
		return "星期日"
	case time.Monday:
		return "星期一"
	case time.Tuesday:
		return "星期二"
	case time.Wednesday:
		return "星期三"
	case time.Thursday:
		return "星期四"
	case time.Friday:
		return "星期五"
	case time.Saturday:
		return "星期六"
	default:
		return "星期日"
	}
}

func (s *WorldTimeService) lookupAlias(region string) string {
	key := normalizeRegionKey(region)
	if key == "" {
		return ""
	}
	if tz := s.tzMap[key]; tz != "" {
		return tz
	}

	bestKey := ""
	bestTZ := ""
	for k, v := range s.tzMap {
		if strings.Contains(key, k) || strings.Contains(k, key) {
			if len(k) > len(bestKey) {
				bestKey = k
				bestTZ = v
			}
		}
	}
	return bestTZ
}

func validTZ(name string) bool {
	if !strings.Contains(name, "/") {
		return false
	}
	_, err := time.LoadLocation(strings.TrimSpace(name))
	return err == nil
}

func normalizeRegionKey(region string) string {
	key := strings.ToLower(strings.TrimSpace(region))
	replacer := strings.NewReplacer(
		" ", "", "\t", "", "\n", "", "\r", "",
		"-", "", "_", "", ".", "", ",", "",
		"，", "", "。", "", "、", "",
		"'", "", "\"", "", "`", "", "·", "",
		"（", "", "）", "", "(", "", ")", "",
		"[", "", "]", "", "{", "", "}", "",
		"／", "", "/", "", "\\", "",
		"　", "",
	)
	return replacer.Replace(key)
}

func normalizeZoneNeedle(region string) string {
	key := strings.ToLower(strings.TrimSpace(region))
	key = strings.ReplaceAll(key, " ", "_")
	key = strings.ReplaceAll(key, "-", "_")
	key = strings.ReplaceAll(key, "／", "/")
	key = strings.ReplaceAll(key, "\\", "/")
	return key
}

func (s *WorldTimeService) lookupByGeo(region string) (string, error) {
	rows, err := s.geocode(region)
	if err != nil || len(rows) == 0 {
		return "", fmt.Errorf("地理编码失败")
	}
	lat := strings.TrimSpace(rows[0].Lat)
	lon := strings.TrimSpace(rows[0].Lon)
	if lat == "" || lon == "" {
		return "", fmt.Errorf("地理坐标为空")
	}
	endpoint := "https://api.geotimezone.com/public/timezone"
	req, _ := http.NewRequest(http.MethodGet, endpoint, nil)
	q := req.URL.Query()
	q.Set("latitude", lat)
	q.Set("longitude", lon)
	req.URL.RawQuery = q.Encode()
	req.Header.Set("User-Agent", "ledger-go-bot/1.0")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("时区接口异常: %d", resp.StatusCode)
	}
	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return "", err
	}
	for _, key := range []string{"iana_timezone", "timezone", "time_zone", "timeZone"} {
		if tz, ok := raw[key].(string); ok && validTZ(tz) {
			return strings.TrimSpace(tz), nil
		}
	}
	return "", fmt.Errorf("时区字段缺失")
}

func (s *WorldTimeService) geocode(query string) ([]geoRow, error) {
	endpoint := "https://nominatim.openstreetmap.org/search"
	req, _ := http.NewRequest(http.MethodGet, endpoint, nil)
	q := req.URL.Query()
	q.Set("q", query)
	q.Set("format", "json")
	q.Set("limit", "3")
	req.URL.RawQuery = q.Encode()
	req.Header.Set("User-Agent", "ledger-go-bot/1.0")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("地理接口异常: %d", resp.StatusCode)
	}
	var rows []geoRow
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *WorldTimeService) lookupByZoneList(region string) (string, error) {
	zoneList, err := s.getZoneList()
	if err != nil || len(zoneList) == 0 {
		return "", fmt.Errorf("时区列表获取失败")
	}

	needle := normalizeZoneNeedle(region)
	if needle == "" {
		return "", fmt.Errorf("时区关键词为空")
	}
	needleCompact := strings.ReplaceAll(strings.ReplaceAll(needle, "_", ""), "/", "")

	for _, zone := range zoneList {
		zoneLower := strings.ToLower(strings.TrimSpace(zone))
		zoneCompact := strings.ReplaceAll(strings.ReplaceAll(zoneLower, "_", ""), "/", "")

		if zoneLower == needle || strings.Contains(zoneLower, needle) || strings.Contains(zoneCompact, needleCompact) {
			return zone, nil
		}

		parts := strings.Split(zoneLower, "/")
		if len(parts) > 0 {
			last := parts[len(parts)-1]
			lastCompact := strings.ReplaceAll(last, "_", "")
			if strings.Contains(last, needle) || strings.Contains(lastCompact, needleCompact) {
				return zone, nil
			}
		}
	}
	return "", fmt.Errorf("未命中时区列表")
}

func (s *WorldTimeService) getZoneList() ([]string, error) {
	s.cacheMu.RLock()
	if len(s.zoneCache) > 0 && time.Since(s.cacheAt) < 12*time.Hour {
		out := make([]string, len(s.zoneCache))
		copy(out, s.zoneCache)
		s.cacheMu.RUnlock()
		return out, nil
	}
	s.cacheMu.RUnlock()

	fallback := buildWorldTimeLocalZones(s.tzMap)
	if len(fallback) == 0 {
		return nil, fmt.Errorf("本地时区列表为空")
	}

	endpoint := "https://worldtimeapi.org/api/timezone"
	req, _ := http.NewRequest(http.MethodGet, endpoint, nil)
	req.Header.Set("User-Agent", "ledger-go-bot/1.0")

	resp, err := s.client.Do(req)
	if err != nil {
		s.cacheMu.Lock()
		s.zoneCache = fallback
		s.cacheAt = time.Now()
		s.cacheMu.Unlock()
		return fallback, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		s.cacheMu.Lock()
		s.zoneCache = fallback
		s.cacheAt = time.Now()
		s.cacheMu.Unlock()
		return fallback, nil
	}

	var zones []string
	if err := json.NewDecoder(resp.Body).Decode(&zones); err != nil || len(zones) == 0 {
		s.cacheMu.Lock()
		s.zoneCache = fallback
		s.cacheAt = time.Now()
		s.cacheMu.Unlock()
		return fallback, nil
	}

	merged := mergeZoneList(zones, fallback)
	s.cacheMu.Lock()
	s.zoneCache = merged
	s.cacheAt = time.Now()
	s.cacheMu.Unlock()
	return merged, nil
}

func mergeZoneList(primary, secondary []string) []string {
	out := make([]string, 0, len(primary)+len(secondary))
	seen := map[string]struct{}{}
	appendUnique := func(items []string) {
		for _, item := range items {
			v := strings.TrimSpace(item)
			if v == "" || !validTZ(v) {
				continue
			}
			if _, ok := seen[v]; ok {
				continue
			}
			seen[v] = struct{}{}
			out = append(out, v)
		}
	}
	appendUnique(primary)
	appendUnique(secondary)
	return out
}
