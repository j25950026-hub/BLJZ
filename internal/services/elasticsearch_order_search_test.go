package services

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"go-bot/internal/db"
)

type capturedElasticRequest struct {
	method string
	path   string
	auth   string
	body   []byte
}

func TestControlPoint415ElasticFullTextSearch(t *testing.T) {
	t.Run("控制点 4.1.5 功能验证", func(t *testing.T) {
		requests := make([]capturedElasticRequest, 0, 2)
		var handlerErr error
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			payload, err := io.ReadAll(r.Body)
			if err != nil {
				handlerErr = err
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"read body failed"}`))
				return
			}
			requests = append(requests, capturedElasticRequest{
				method: r.Method,
				path:   r.URL.Path,
				auth:   r.Header.Get("Authorization"),
				body:   payload,
			})

			switch {
			case r.Method == http.MethodPut && r.URL.Path == "/ledger_entries_415/_doc/9001":
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"result":"created"}`))
			case r.Method == http.MethodPost && r.URL.Path == "/ledger_entries_415/_search":
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"hits":{"hits":[{"_id":"9001"},{"_id":"oops"},{"_id":"9002"}]}}`))
			default:
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":"unexpected"}`))
			}
		}))
		defer srv.Close()

		searcher, err := NewElasticOrderSearch(ElasticOrderSearchConfig{
			Endpoint: srv.URL,
			Index:    "ledger_entries_415",
			APIKey:   "test-key",
			Timeout:  time.Second,
		})
		if err != nil {
			t.Fatalf("初始化 Elasticsearch 搜索器失败: %v", err)
		}

		err = searcher.IndexLedgerEntry(context.Background(), db.LedgerEntry{
			ID:          9001,
			ChatID:      410500,
			MessageID:   801,
			UserID:      9527,
			Username:    "auditor",
			Nickname:    "风控",
			EntryType:   "deposit",
			Amount:      1234.5,
			Currency:    "CNY",
			BaseAmount:  1234.5,
			FeeRate:     0,
			Rate:        1,
			FinalAmount: 1234.5,
			Note:        "周末急单 1234.50",
			CardNo:      "6222020011223344",
			TrackLink:   "tg://msg/801",
			CreatedAt:   "2026-02-19 12:00:00",
		})
		if err != nil {
			t.Fatalf("写入 Elasticsearch 索引失败: %v", err)
		}

		amount := 1234.5
		ids, err := searcher.SearchLedgerEntryIDs(context.Background(), db.OrderSearchQuery{
			ChatID:  410500,
			Keyword: "急单 1234.5",
			Amount:  &amount,
			StartAt: "2026-02-18 00:00:00",
			Limit:   20,
		})
		if err != nil {
			t.Fatalf("执行 Elasticsearch 查单失败: %v", err)
		}
		if len(ids) != 2 || ids[0] != 9001 || ids[1] != 9002 {
			t.Fatalf("搜索结果主键解析错误: %+v", ids)
		}
		if handlerErr != nil {
			t.Fatalf("测试服务处理请求失败: %v", handlerErr)
		}

		if len(requests) != 2 {
			t.Fatalf("请求次数错误: got=%d want=2", len(requests))
		}
		if requests[0].method != http.MethodPut || requests[0].path != "/ledger_entries_415/_doc/9001" {
			t.Fatalf("索引请求路径错误: %+v", requests[0])
		}
		if requests[1].method != http.MethodPost || requests[1].path != "/ledger_entries_415/_search" {
			t.Fatalf("搜索请求路径错误: %+v", requests[1])
		}
		if requests[0].auth != "ApiKey test-key" || requests[1].auth != "ApiKey test-key" {
			t.Fatalf("API Key 认证头错误: idx=%q search=%q", requests[0].auth, requests[1].auth)
		}

		indexPayload := mustDecodeJSONObject(t, requests[0].body)
		if got := mustJSONFloat(t, indexPayload["chat_id"]); got != 410500 {
			t.Fatalf("索引 chat_id 错误: %v", got)
		}
		if got := mustJSONString(t, indexPayload["note"]); got != "周末急单 1234.50" {
			t.Fatalf("索引 note 错误: %q", got)
		}
		if got := mustJSONString(t, indexPayload["amount_text"]); got != "1234.5" {
			t.Fatalf("索引 amount_text 错误: %q", got)
		}
		if got := mustJSONString(t, indexPayload["final_amount_text"]); got != "1234.5" {
			t.Fatalf("索引 final_amount_text 错误: %q", got)
		}
		if got := mustJSONFloat(t, indexPayload["created_at_unix"]); got <= 0 {
			t.Fatalf("索引 created_at_unix 应为正数: %v", got)
		}

		searchPayload := mustDecodeJSONObject(t, requests[1].body)
		if got := mustJSONFloat(t, searchPayload["size"]); got != 20 {
			t.Fatalf("搜索 size 错误: %v", got)
		}
		if !hasChatIDFilter(t, searchPayload, 410500) {
			t.Fatalf("搜索请求缺少 chat_id filter: %s", string(requests[1].body))
		}
		if !hasStartAtFilter(t, searchPayload) {
			t.Fatalf("搜索请求缺少 created_at_unix 起始过滤: %s", string(requests[1].body))
		}

		wildcardFields := collectWildcardFields(t, searchPayload)
		wantWildcard := []string{"amount_text", "final_amount_text", "note"}
		if !equalStringSet(wildcardFields, wantWildcard) {
			t.Fatalf("wildcard 字段错误: got=%v want=%v", wildcardFields, wantWildcard)
		}

		multiMatchFields := collectMultiMatchFields(t, searchPayload)
		for _, expected := range []string{"note^3", "amount_text", "final_amount_text"} {
			if !containsString(multiMatchFields, expected) {
				t.Fatalf("multi_match 字段缺失: %s in %v", expected, multiMatchFields)
			}
		}

		if !hasAmountRangeFilter(t, searchPayload) {
			t.Fatalf("搜索请求缺少金额范围过滤: %s", string(requests[1].body))
		}
	})
}

func mustDecodeJSONObject(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("解析 JSON 失败: %v raw=%s", err, string(raw))
	}
	return out
}

func mustJSONObject(t *testing.T, value any) map[string]any {
	t.Helper()
	obj, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("期望 JSON object，实际=%T", value)
	}
	return obj
}

func mustJSONArray(t *testing.T, value any) []any {
	t.Helper()
	arr, ok := value.([]any)
	if !ok {
		t.Fatalf("期望 JSON array，实际=%T", value)
	}
	return arr
}

func mustJSONString(t *testing.T, value any) string {
	t.Helper()
	text, ok := value.(string)
	if !ok {
		t.Fatalf("期望 JSON string，实际=%T", value)
	}
	return text
}

func mustJSONFloat(t *testing.T, value any) float64 {
	t.Helper()
	number, ok := value.(float64)
	if !ok {
		t.Fatalf("期望 JSON number，实际=%T", value)
	}
	return number
}

func hasChatIDFilter(t *testing.T, payload map[string]any, chatID int64) bool {
	t.Helper()
	query := mustJSONObject(t, payload["query"])
	boolQuery := mustJSONObject(t, query["bool"])
	filters := mustJSONArray(t, boolQuery["filter"])
	for _, item := range filters {
		obj := mustJSONObject(t, item)
		term, ok := obj["term"].(map[string]any)
		if !ok {
			continue
		}
		if got, ok := term["chat_id"].(float64); ok && int64(got) == chatID {
			return true
		}
	}
	return false
}

func hasStartAtFilter(t *testing.T, payload map[string]any) bool {
	t.Helper()
	query := mustJSONObject(t, payload["query"])
	boolQuery := mustJSONObject(t, query["bool"])
	filters := mustJSONArray(t, boolQuery["filter"])
	for _, item := range filters {
		obj := mustJSONObject(t, item)
		rangeObj, ok := obj["range"].(map[string]any)
		if !ok {
			continue
		}
		createdAt, ok := rangeObj["created_at_unix"].(map[string]any)
		if !ok {
			continue
		}
		if gte, ok := createdAt["gte"].(float64); ok && gte > 0 {
			return true
		}
	}
	return false
}

func collectWildcardFields(t *testing.T, payload map[string]any) []string {
	t.Helper()
	query := mustJSONObject(t, payload["query"])
	boolQuery := mustJSONObject(t, query["bool"])
	mustClauses := mustJSONArray(t, boolQuery["must"])
	fieldSet := map[string]struct{}{}
	for _, clause := range mustClauses {
		clauseMap := mustJSONObject(t, clause)
		boolClause, ok := clauseMap["bool"].(map[string]any)
		if !ok {
			continue
		}
		shouldClauses, ok := boolClause["should"].([]any)
		if !ok {
			continue
		}
		for _, shouldItem := range shouldClauses {
			wildcard, ok := mustJSONObject(t, shouldItem)["wildcard"].(map[string]any)
			if !ok {
				continue
			}
			for field := range wildcard {
				fieldSet[field] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(fieldSet))
	for field := range fieldSet {
		out = append(out, field)
	}
	sort.Strings(out)
	return out
}

func collectMultiMatchFields(t *testing.T, payload map[string]any) []string {
	t.Helper()
	query := mustJSONObject(t, payload["query"])
	boolQuery := mustJSONObject(t, query["bool"])
	mustClauses := mustJSONArray(t, boolQuery["must"])
	for _, clause := range mustClauses {
		clauseMap := mustJSONObject(t, clause)
		boolClause, ok := clauseMap["bool"].(map[string]any)
		if !ok {
			continue
		}
		shouldClauses, ok := boolClause["should"].([]any)
		if !ok {
			continue
		}
		for _, shouldItem := range shouldClauses {
			multiMatch, ok := mustJSONObject(t, shouldItem)["multi_match"].(map[string]any)
			if !ok {
				continue
			}
			fieldsRaw, ok := multiMatch["fields"].([]any)
			if !ok {
				continue
			}
			fields := make([]string, 0, len(fieldsRaw))
			for _, field := range fieldsRaw {
				if text, ok := field.(string); ok && text != "" {
					fields = append(fields, text)
				}
			}
			return fields
		}
	}
	return nil
}

func hasAmountRangeFilter(t *testing.T, payload map[string]any) bool {
	t.Helper()
	query := mustJSONObject(t, payload["query"])
	boolQuery := mustJSONObject(t, query["bool"])
	mustClauses := mustJSONArray(t, boolQuery["must"])
	foundAmount := false
	foundFinalAmount := false
	for _, clause := range mustClauses {
		clauseMap := mustJSONObject(t, clause)
		boolClause, ok := clauseMap["bool"].(map[string]any)
		if !ok {
			continue
		}
		shouldClauses, ok := boolClause["should"].([]any)
		if !ok {
			continue
		}
		for _, shouldItem := range shouldClauses {
			rangeObj, ok := mustJSONObject(t, shouldItem)["range"].(map[string]any)
			if !ok {
				continue
			}
			if _, ok := rangeObj["amount"]; ok {
				foundAmount = true
			}
			if _, ok := rangeObj["final_amount"]; ok {
				foundFinalAmount = true
			}
		}
	}
	return foundAmount && foundFinalAmount
}

func equalStringSet(got []string, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	copiedGot := append([]string(nil), got...)
	copiedWant := append([]string(nil), want...)
	sort.Strings(copiedGot)
	sort.Strings(copiedWant)
	for i := range copiedWant {
		if copiedWant[i] != copiedGot[i] {
			return false
		}
	}
	return true
}

func containsString(list []string, target string) bool {
	for _, item := range list {
		if item == target {
			return true
		}
	}
	return false
}
