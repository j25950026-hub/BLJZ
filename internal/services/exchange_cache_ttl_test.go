package services

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestControlPoint344CacheTTL(t *testing.T) {
	t.Run("控制点 3.4.4 功能验证", func(t *testing.T) {
		now := time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC)
		var (
			mu       sync.Mutex
			callNums int
		)
		client := &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				mu.Lock()
				callNums++
				mu.Unlock()
				body := `{"data":[{"price":"7.20"},{"price":"7.30"},{"price":"7.40"}]}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(body)),
					Header:     make(http.Header),
				}, nil
			}),
		}

		svc := &ExchangeService{
			client:   client,
			now:      func() time.Time { return now },
			cacheTTL: 10 * time.Second,
			cache:    make(map[string]rateCacheEntry),
		}

		first, err := svc.GetRate("okx", "all", "CNY")
		if err != nil {
			t.Fatalf("首次拉取汇率失败: %v", err)
		}
		mu.Lock()
		if callNums != 1 {
			mu.Unlock()
			t.Fatalf("首次请求应触发一次上游调用，实际=%d", callNums)
		}
		mu.Unlock()
		if first == nil || first.Price <= 0 {
			t.Fatalf("首次请求应返回有效汇率: %+v", first)
		}

		now = now.Add(5 * time.Second)
		second, err := svc.GetRate("okx", "all", "CNY")
		if err != nil {
			t.Fatalf("缓存窗口内再次拉取失败: %v", err)
		}
		mu.Lock()
		if callNums != 1 {
			mu.Unlock()
			t.Fatalf("缓存窗口内不应再次命中上游，实际调用=%d", callNums)
		}
		mu.Unlock()
		if second == nil || second.Price != first.Price {
			t.Fatalf("缓存命中返回异常: first=%+v second=%+v", first, second)
		}

		now = now.Add(6 * time.Second)
		third, err := svc.GetRate("okx", "all", "CNY")
		if err != nil {
			t.Fatalf("缓存过期后拉取失败: %v", err)
		}
		mu.Lock()
		if callNums != 2 {
			mu.Unlock()
			t.Fatalf("缓存过期后应重新请求上游，实际调用=%d", callNums)
		}
		mu.Unlock()
		if third == nil || third.Price <= 0 {
			t.Fatalf("缓存过期后应返回有效汇率: %+v", third)
		}
	})
}
