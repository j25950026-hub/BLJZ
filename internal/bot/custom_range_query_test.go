package bot

import (
	"testing"
	"time"
)

func TestParseOrderQueryDateRange(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)

	t.Run("解析开始-结束日期并保留关键词金额", func(t *testing.T) {
		rangeQuery, remain, err := parseOrderQueryDateRange("2026-02-18-2026-02-19+急单+88.8", loc)
		if err != nil {
			t.Fatalf("解析时间范围失败: %v", err)
		}
		if rangeQuery == nil {
			t.Fatalf("应识别出时间范围")
		}
		if rangeQuery.StartAt != "2026-02-17 16:00:00" || rangeQuery.EndAt != "2026-02-19 16:00:00" {
			t.Fatalf("时间范围转换错误: %+v", rangeQuery)
		}
		if remain != "急单+88.8" {
			t.Fatalf("时间范围后缀解析错误: %q", remain)
		}

		keyword, amount := parseOrderQueryPayload(remain)
		if keyword != "急单" {
			t.Fatalf("关键词解析错误: %q", keyword)
		}
		if amount == nil || *amount != 88.8 {
			t.Fatalf("金额解析错误: %v", amount)
		}
	})

	t.Run("结束日期早于开始日期返回错误", func(t *testing.T) {
		_, _, err := parseOrderQueryDateRange("2026-02-20-2026-02-19", loc)
		if err == nil {
			t.Fatalf("结束日期早于开始日期应报错")
		}
	})

	t.Run("无时间范围保持原始行为", func(t *testing.T) {
		rangeQuery, remain, err := parseOrderQueryDateRange("urgent-order+100", loc)
		if err != nil {
			t.Fatalf("无范围文本不应报错: %v", err)
		}
		if rangeQuery != nil {
			t.Fatalf("无范围文本不应返回范围: %+v", rangeQuery)
		}
		if remain != "urgent-order+100" {
			t.Fatalf("无范围文本应原样返回: %q", remain)
		}
	})
}
