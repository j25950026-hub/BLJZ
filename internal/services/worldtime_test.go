package services

import (
	"testing"
	"time"
)

func TestWorldTimeAliasCoverage(t *testing.T) {
	svc := NewWorldTimeService()

	if got := len(svc.tzMap); got < 220 {
		t.Fatalf("静态地区映射数量不足，当前=%d，期望>=220", got)
	}

	cases := map[string]string{
		"北京":   "Asia/Shanghai",
		"上海":   "Asia/Shanghai",
		"香港":   "Asia/Hong_Kong",
		"台北":   "Asia/Taipei",
		"东京":   "Asia/Tokyo",
		"首尔":   "Asia/Seoul",
		"迪拜":   "Asia/Dubai",
		"英国":   "Europe/London",
		"德国":   "Europe/Berlin",
		"莫斯科":  "Europe/Moscow",
		"纽约":   "America/New_York",
		"美国东部": "America/New_York",
		"美国中部": "America/Chicago",
		"美国西部": "America/Los_Angeles",
		"巴西":   "America/Sao_Paulo",
		"悉尼":   "Australia/Sydney",
		"新西兰":  "Pacific/Auckland",
		"南非":   "Africa/Johannesburg",
	}
	for input, want := range cases {
		if got := svc.lookupAlias(input); got != want {
			t.Fatalf("同义词映射错误: %q => %q，期望 %q", input, got, want)
		}
	}
}

func TestWorldTimeLocalZoneFallback(t *testing.T) {
	svc := NewWorldTimeService()
	zones := buildWorldTimeLocalZones(svc.tzMap)
	if len(zones) < 80 {
		t.Fatalf("本地时区列表过少: %d", len(zones))
	}

	required := []string{
		"Asia/Shanghai",
		"Asia/Tokyo",
		"Europe/London",
		"America/New_York",
		"Australia/Sydney",
	}
	for _, tz := range required {
		if !containsZone(zones, tz) {
			t.Fatalf("本地时区列表缺少: %s", tz)
		}
	}
}

func TestNormalizeRegionKey(t *testing.T) {
	in := "  New-York (US) "
	out := normalizeRegionKey(in)
	if out != "newyorkus" {
		t.Fatalf("标准化结果异常: %q", out)
	}
}

func TestFormatChineseWeekday(t *testing.T) {
	cases := []struct {
		weekday time.Weekday
		want    string
	}{
		{time.Sunday, "星期日"},
		{time.Monday, "星期一"},
		{time.Tuesday, "星期二"},
		{time.Wednesday, "星期三"},
		{time.Thursday, "星期四"},
		{time.Friday, "星期五"},
		{time.Saturday, "星期六"},
	}
	for _, tc := range cases {
		got := formatChineseWeekday(tc.weekday)
		if got != tc.want {
			t.Fatalf("星期映射异常: weekday=%v want=%q got=%q", tc.weekday, tc.want, got)
		}
	}
}

func containsZone(zones []string, target string) bool {
	for _, item := range zones {
		if item == target {
			return true
		}
	}
	return false
}
