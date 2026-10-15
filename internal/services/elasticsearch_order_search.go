package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go-bot/internal/db"
	"go-bot/internal/system"
)

const (
	defaultElasticOrderIndex   = "ledger_entries"
	defaultElasticOrderTimeout = 3 * time.Second
	ledgerTimeLayout           = "2006-01-02 15:04:05"
)

type ElasticOrderSearchConfig struct {
	Endpoint string
	Index    string
	Username string
	Password string
	APIKey   string
	Timeout  time.Duration
}

type ElasticOrderSearch struct {
	endpoint   string
	index      string
	username   string
	password   string
	authHeader string
	httpClient *http.Client
}

type elasticSearchResponse struct {
	Hits struct {
		Hits []struct {
			ID string `json:"_id"`
		} `json:"hits"`
	} `json:"hits"`
}

func NewElasticOrderSearch(cfg ElasticOrderSearchConfig) (*ElasticOrderSearch, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/")
	if endpoint == "" {
		return nil, errors.New("elasticsearch endpoint 不能为空")
	}
	if _, err := url.ParseRequestURI(endpoint); err != nil {
		return nil, fmt.Errorf("elasticsearch endpoint 非法: %w", err)
	}
	index := strings.TrimSpace(cfg.Index)
	if index == "" {
		index = defaultElasticOrderIndex
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultElasticOrderTimeout
	}
	searcher := &ElasticOrderSearch{
		endpoint: strings.TrimSuffix(endpoint, "/"),
		index:    index,
		username: strings.TrimSpace(cfg.Username),
		password: cfg.Password,
		httpClient: system.WrapHTTPClientWithTracing(
			&http.Client{Timeout: timeout},
			"elasticsearch-order-search",
		),
	}
	if key := strings.TrimSpace(cfg.APIKey); key != "" {
		searcher.authHeader = "ApiKey " + key
	}
	return searcher, nil
}

func (s *ElasticOrderSearch) IndexLedgerEntry(ctx context.Context, entry db.LedgerEntry) error {
	if s == nil {
		return errors.New("elasticsearch searcher 未初始化")
	}
	if entry.ID <= 0 {
		return errors.New("entry id 非法")
	}
	payload := map[string]any{
		"entry_id":          entry.ID,
		"chat_id":           entry.ChatID,
		"message_id":        entry.MessageID,
		"user_id":           entry.UserID,
		"username":          strings.TrimSpace(entry.Username),
		"nickname":          strings.TrimSpace(entry.Nickname),
		"entry_type":        strings.TrimSpace(entry.EntryType),
		"amount":            entry.Amount,
		"currency":          strings.TrimSpace(entry.Currency),
		"base_amount":       entry.BaseAmount,
		"fee_rate":          entry.FeeRate,
		"rate":              entry.Rate,
		"final_amount":      entry.FinalAmount,
		"note":              strings.TrimSpace(entry.Note),
		"card_no":           strings.TrimSpace(entry.CardNo),
		"track_link":        strings.TrimSpace(entry.TrackLink),
		"created_at":        strings.TrimSpace(entry.CreatedAt),
		"created_at_unix":   parseLedgerTimeUnix(entry.CreatedAt),
		"reversed":          entry.Reversed,
		"amount_text":       formatAmountText(entry.Amount),
		"final_amount_text": formatAmountText(entry.FinalAmount),
	}
	if entry.ReversalOf != nil {
		payload["reversal_of"] = *entry.ReversalOf
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/%s/_doc/%d", url.PathEscape(s.index), entry.ID)
	resBody, statusCode, err := s.doJSONRequest(ctx, http.MethodPut, path, body)
	if err != nil {
		return err
	}
	if statusCode >= 200 && statusCode < 300 {
		return nil
	}
	return fmt.Errorf("elasticsearch index 失败: status=%d body=%s", statusCode, truncateHTTPBody(resBody))
}

func (s *ElasticOrderSearch) SearchLedgerEntryIDs(ctx context.Context, query db.OrderSearchQuery) ([]int64, error) {
	if s == nil {
		return nil, errors.New("elasticsearch searcher 未初始化")
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}
	body, err := json.Marshal(buildElasticOrderSearchBody(query, limit))
	if err != nil {
		return nil, err
	}
	resBody, statusCode, err := s.doJSONRequest(ctx, http.MethodPost, fmt.Sprintf("/%s/_search", url.PathEscape(s.index)), body)
	if err != nil {
		return nil, err
	}
	if statusCode < 200 || statusCode >= 300 {
		return nil, fmt.Errorf("elasticsearch search 失败: status=%d body=%s", statusCode, truncateHTTPBody(resBody))
	}
	var response elasticSearchResponse
	if err := json.Unmarshal(resBody, &response); err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(response.Hits.Hits))
	for _, hit := range response.Hits.Hits {
		id, parseErr := strconv.ParseInt(strings.TrimSpace(hit.ID), 10, 64)
		if parseErr != nil || id <= 0 {
			continue
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func buildElasticOrderSearchBody(query db.OrderSearchQuery, limit int) map[string]any {
	filters := make([]any, 0, 2)
	filters = append(filters, map[string]any{"term": map[string]any{"chat_id": query.ChatID}})
	if startUnix := parseLedgerTimeUnix(query.StartAt); startUnix > 0 {
		filters = append(filters, map[string]any{"range": map[string]any{"created_at_unix": map[string]any{"gte": startUnix}}})
	}

	must := make([]any, 0, 2)
	if keyword := strings.TrimSpace(query.Keyword); keyword != "" {
		keywordShould := []any{
			map[string]any{"multi_match": map[string]any{
				"query":     keyword,
				"fields":    []string{"note^3", "nickname^2", "username^2", "card_no", "amount_text", "final_amount_text"},
				"fuzziness": "AUTO",
			}},
		}
		if pattern := buildWildcardPattern(keyword); pattern != "" {
			keywordShould = appendWildcardShouldClauses(keywordShould, pattern, []string{"note", "amount_text", "final_amount_text"})
		}
		must = append(must, map[string]any{
			"bool": map[string]any{
				"should":               keywordShould,
				"minimum_should_match": 1,
			},
		})
	}

	if query.Amount != nil {
		amount := *query.Amount
		amountTolerance := 0.000001
		must = append(must, map[string]any{
			"bool": map[string]any{
				"should": []any{
					map[string]any{"range": map[string]any{"amount": map[string]any{"gte": amount - amountTolerance, "lte": amount + amountTolerance}}},
					map[string]any{"range": map[string]any{"final_amount": map[string]any{"gte": amount - amountTolerance, "lte": amount + amountTolerance}}},
				},
				"minimum_should_match": 1,
			},
		})
	}

	boolQuery := map[string]any{
		"filter": filters,
	}
	if len(must) > 0 {
		boolQuery["must"] = must
	}

	return map[string]any{
		"size":    limit,
		"_source": false,
		"sort": []any{
			map[string]any{"_score": map[string]any{"order": "desc"}},
			map[string]any{"created_at_unix": map[string]any{"order": "desc"}},
		},
		"query": map[string]any{
			"bool": boolQuery,
		},
	}
}

func (s *ElasticOrderSearch) doJSONRequest(ctx context.Context, method string, path string, body []byte) ([]byte, int, error) {
	urlText := s.endpoint + path
	req, err := http.NewRequestWithContext(ctx, method, urlText, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.authHeader != "" {
		req.Header.Set("Authorization", s.authHeader)
	} else if s.username != "" {
		req.SetBasicAuth(s.username, s.password)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return respBody, resp.StatusCode, nil
}

func parseLedgerTimeUnix(text string) int64 {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0
	}
	if parsed, err := time.ParseInLocation(ledgerTimeLayout, trimmed, time.UTC); err == nil {
		return parsed.Unix()
	}
	if parsed, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return parsed.UTC().Unix()
	}
	return 0
}

func formatAmountText(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func buildWildcardPattern(keyword string) string {
	trimmed := strings.TrimSpace(keyword)
	if trimmed == "" {
		return ""
	}
	replacer := strings.NewReplacer(`\\`, `\\\\`, `*`, `\\*`, `?`, `\\?`)
	return "*" + replacer.Replace(trimmed) + "*"
}

func appendWildcardShouldClauses(should []any, pattern string, fields []string) []any {
	if pattern == "" || len(fields) == 0 {
		return should
	}
	for _, field := range fields {
		name := strings.TrimSpace(field)
		if name == "" {
			continue
		}
		should = append(should, map[string]any{
			"wildcard": map[string]any{
				name: map[string]any{
					"value":            pattern,
					"case_insensitive": true,
				},
			},
		})
	}
	return should
}

func truncateHTTPBody(raw []byte) string {
	trimmed := strings.TrimSpace(string(raw))
	if len(trimmed) <= 512 {
		return trimmed
	}
	return trimmed[:512]
}
