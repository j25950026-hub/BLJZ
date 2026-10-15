package utils

import (
	"strings"
	"testing"
)

func TestControlPoint244InputSanitization(t *testing.T) {
	t.Run("控制点 2.4.4 功能验证", func(t *testing.T) {
		keyword, err := SanitizeAutomationKeyword("到账提醒", false)
		if err != nil || keyword != "到账提醒" {
			t.Fatalf("合法关键词清洗失败: keyword=%q err=%v", keyword, err)
		}

		if _, err := SanitizeAutomationKeyword("到账'; DROP TABLE auto_replies;--", false); err == nil || !strings.Contains(err.Error(), "SQL 注入") {
			t.Fatalf("SQL 注入关键词应被拦截: err=%v", err)
		}

		if _, err := SanitizeAutomationContent(`<script>alert("xss")</script>`); err == nil || !strings.Contains(err.Error(), "XSS") {
			t.Fatalf("XSS 内容应被拦截: err=%v", err)
		}

		content, err := SanitizeAutomationContent("已收到，请稍候处理")
		if err != nil || content != "已收到，请稍候处理" {
			t.Fatalf("合法内容清洗失败: content=%q err=%v", content, err)
		}

		targets, err := SanitizeForwardTargets("-1001001，-1001002,-1001002")
		if err != nil {
			t.Fatalf("合法目标群ID清洗失败: %v", err)
		}
		if targets != "-1001001,-1001002" {
			t.Fatalf("目标群ID去重/归一化失败: %q", targets)
		}
	})

	t.Run("允许空关键词兜底", func(t *testing.T) {
		keyword, err := SanitizeAutomationKeyword("   ", true)
		if err != nil {
			t.Fatalf("空关键词兜底应通过: %v", err)
		}
		if keyword != "" {
			t.Fatalf("空关键词兜底应返回空字符串: %q", keyword)
		}
	})
}
