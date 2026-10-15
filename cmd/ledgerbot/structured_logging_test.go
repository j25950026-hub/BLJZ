package main

import (
	"bytes"
	"encoding/json"
	"log"
	"strings"
	"testing"
)

func TestSetupStructuredLogging(t *testing.T) {
	originalWriter := log.Writer()
	originalFlags := log.Flags()
	defer func() {
		log.SetOutput(originalWriter)
		log.SetFlags(originalFlags)
	}()

	var sink bytes.Buffer
	log.SetOutput(&sink)
	log.SetFlags(log.LstdFlags)

	setupStructuredLogging(func(key string) string {
		switch key {
		case "STRUCTURED_LOG_SERVICE":
			return "cp521-cmd"
		case "STRUCTURED_LOG_ENV":
			return "test"
		case "STRUCTURED_LOG_TRACE_ID":
			return "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		default:
			return ""
		}
	})
	log.Print("cmd-structured-log-ready")

	lines := strings.Split(strings.TrimSpace(sink.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("结构化日志输出不足: %q", sink.String())
	}

	startup := decodeJSONLogLine(t, lines[0])
	if startup["service"] != "cp521-cmd" {
		t.Fatalf("service 字段错误: %+v", startup)
	}
	if startup["trace_id"] != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatalf("启动日志默认 trace_id 错误: %+v", startup)
	}

	last := decodeJSONLogLine(t, lines[len(lines)-1])
	if last["message"] != "cmd-structured-log-ready" {
		t.Fatalf("业务日志消息错误: %+v", last)
	}
	if last["trace_id"] != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatalf("业务日志 trace_id 错误: %+v", last)
	}
}

func decodeJSONLogLine(t *testing.T, raw string) map[string]string {
	t.Helper()
	payload := make(map[string]string)
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("日志应为 JSON，解析失败: %v line=%q", err, raw)
	}
	return payload
}
