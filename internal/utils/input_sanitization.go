package utils

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	maxAutomationTokenRunes   = 64
	maxAutomationContentRunes = 2000
	maxForwardTargetCount     = 50
)

var dangerousXSSPattern = regexp.MustCompile(`(?i)(<\s*/?\s*(script|iframe|object|embed|svg|math|link|style|base|meta|img)\b|on[a-z]+\s*=|javascript:|vbscript:|data\s*:\s*text/html)`)

func SanitizeAutomationTriggerKey(raw string) (string, error) {
	return sanitizeAutomationToken(raw, "触发词", false)
}

func SanitizeAutomationKeyword(raw string, allowEmpty bool) (string, error) {
	return sanitizeAutomationToken(raw, "关键词", allowEmpty)
}

func sanitizeAutomationToken(raw, fieldName string, allowEmpty bool) (string, error) {
	cleaned := strings.TrimSpace(raw)
	if cleaned == "" {
		if allowEmpty {
			return "", nil
		}
		return "", fmt.Errorf("%s不能为空", fieldName)
	}
	if utf8.RuneCountInString(cleaned) > maxAutomationTokenRunes {
		return "", fmt.Errorf("%s长度不能超过 %d 个字符", fieldName, maxAutomationTokenRunes)
	}
	if idx, ch, bad := FindFirstInvalidSymbol(cleaned); bad {
		return "", fmt.Errorf("%s包含不可识别符号，位置 %d，符号 `%U`（%q）", fieldName, idx, ch, string(ch))
	}
	if strings.ContainsAny(cleaned, "<>") || dangerousXSSPattern.MatchString(cleaned) {
		return "", fmt.Errorf("%s包含疑似 XSS 片段", fieldName)
	}
	if containsSQLMetaFragment(cleaned) {
		return "", fmt.Errorf("%s包含疑似 SQL 注入片段", fieldName)
	}
	return cleaned, nil
}

func SanitizeAutomationContent(raw string) (string, error) {
	cleaned := strings.TrimSpace(raw)
	if cleaned == "" {
		return "", fmt.Errorf("回复内容不能为空")
	}
	if utf8.RuneCountInString(cleaned) > maxAutomationContentRunes {
		return "", fmt.Errorf("回复内容长度不能超过 %d 个字符", maxAutomationContentRunes)
	}
	if idx, ch, bad := FindFirstInvalidSymbol(cleaned); bad {
		return "", fmt.Errorf("回复内容包含不可识别符号，位置 %d，符号 `%U`（%q）", idx, ch, string(ch))
	}
	if dangerousXSSPattern.MatchString(cleaned) {
		return "", fmt.Errorf("回复内容包含疑似 XSS 片段")
	}
	return cleaned, nil
}

func SanitizeForwardTargets(raw string) (string, error) {
	cleaned := strings.TrimSpace(raw)
	if cleaned == "" {
		return "", fmt.Errorf("目标群ID不能为空")
	}
	if idx, ch, bad := FindFirstInvalidSymbol(cleaned); bad {
		return "", fmt.Errorf("目标群ID包含不可识别符号，位置 %d，符号 `%U`（%q）", idx, ch, string(ch))
	}
	if strings.ContainsAny(cleaned, "<>") || containsSQLMetaFragment(cleaned) {
		return "", fmt.Errorf("目标群ID包含疑似 SQL 注入片段")
	}
	cleaned = strings.ReplaceAll(cleaned, "，", ",")
	parts := strings.Split(cleaned, ",")
	seen := make(map[string]struct{}, len(parts))
	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		token := strings.TrimSpace(part)
		if token == "" {
			continue
		}
		if _, err := strconv.ParseInt(token, 10, 64); err != nil {
			return "", fmt.Errorf("目标群ID格式错误：%s", token)
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		normalized = append(normalized, token)
	}
	if len(normalized) == 0 {
		return "", fmt.Errorf("目标群ID不能为空")
	}
	if len(normalized) > maxForwardTargetCount {
		return "", fmt.Errorf("目标群ID数量不能超过 %d", maxForwardTargetCount)
	}
	return strings.Join(normalized, ","), nil
}

func containsSQLMetaFragment(raw string) bool {
	lowered := strings.ToLower(raw)
	for _, fragment := range []string{"--", "/*", "*/", ";", "\x00"} {
		if strings.Contains(lowered, fragment) {
			return true
		}
	}
	return false
}
