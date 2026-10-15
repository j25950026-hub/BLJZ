package bot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestControlPoint251PriorityQueue(t *testing.T) {
	t.Run("控制点 2.5.1 功能验证", func(t *testing.T) {
		queue := newPriorityDispatchQueue(16)
		defer closePriorityQueueForTest(t, queue)

		var (
			mu    sync.Mutex
			order []string
		)
		appendOrder := func(label string) {
			mu.Lock()
			order = append(order, label)
			mu.Unlock()
		}

		blockedStarted := make(chan struct{})
		releaseBlocked := make(chan struct{})
		blockedDone, err := queue.enqueue(context.Background(), outboundPriorityBroadcastAd, func(context.Context) error {
			appendOrder("广告-阻塞")
			close(blockedStarted)
			<-releaseBlocked
			return nil
		})
		if err != nil {
			t.Fatalf("提交阻塞广告任务失败: %v", err)
		}
		waitSignal(t, blockedStarted, "阻塞广告任务未启动")

		normalDone, err := queue.enqueue(context.Background(), outboundPriorityNormalChat, func(context.Context) error {
			appendOrder("普通聊天")
			return nil
		})
		if err != nil {
			t.Fatalf("提交普通聊天任务失败: %v", err)
		}
		lowDone, err := queue.enqueue(context.Background(), outboundPriorityBroadcastAd, func(context.Context) error {
			appendOrder("广告-尾部")
			return nil
		})
		if err != nil {
			t.Fatalf("提交尾部广告任务失败: %v", err)
		}
		alertDone, err := queue.enqueue(context.Background(), outboundPriorityErrorAlert, func(context.Context) error {
			appendOrder("错误报警")
			return nil
		})
		if err != nil {
			t.Fatalf("提交错误报警任务失败: %v", err)
		}
		receiptDone, err := queue.enqueue(context.Background(), outboundPriorityBookkeepingReceipt, func(context.Context) error {
			appendOrder("记账回执")
			return nil
		})
		if err != nil {
			t.Fatalf("提交记账回执任务失败: %v", err)
		}

		close(releaseBlocked)
		waitTaskDone(t, blockedDone, "阻塞广告任务")
		waitTaskDone(t, receiptDone, "记账回执任务")
		waitTaskDone(t, alertDone, "错误报警任务")
		waitTaskDone(t, normalDone, "普通聊天任务")
		waitTaskDone(t, lowDone, "尾部广告任务")

		got := append([]string(nil), order...)
		want := []string{"广告-阻塞", "记账回执", "错误报警", "普通聊天", "广告-尾部"}
		if len(got) != len(want) {
			t.Fatalf("任务执行顺序长度异常: got=%v want=%v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("优先级顺序错误: got=%v want=%v", got, want)
			}
		}
	})
}

func TestPriorityQueueRunsMultipleWorkers(t *testing.T) {
	queue := newPriorityDispatchQueueWithWorkers(16, 2)
	defer closePriorityQueueForTest(t, queue)

	started := make(chan string, 2)
	release := make(chan struct{})
	submit := func(label string) <-chan error {
		done, err := queue.enqueue(context.Background(), outboundPriorityNormalChat, func(context.Context) error {
			started <- label
			<-release
			return nil
		})
		if err != nil {
			t.Fatalf("提交任务 %s 失败: %v", label, err)
		}
		return done
	}

	firstDone := submit("first")
	secondDone := submit("second")

	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case label := <-started:
			seen[label] = true
		case <-time.After(250 * time.Millisecond):
			t.Fatalf("两个 worker 应并发启动两个任务，当前只启动: %v", seen)
		}
	}

	close(release)
	waitTaskDone(t, firstDone, "first")
	waitTaskDone(t, secondDone, "second")
}

func TestOutboundDispatchDropAndFallback(t *testing.T) {
	queue := newPriorityDispatchQueue(1)
	defer closePriorityQueueForTest(t, queue)

	started := make(chan struct{})
	release := make(chan struct{})
	runningDone, err := queue.enqueue(context.Background(), outboundPriorityNormalChat, func(context.Context) error {
		close(started)
		<-release
		return nil
	})
	if err != nil {
		t.Fatalf("提交运行中任务失败: %v", err)
	}
	waitSignal(t, started, "运行中任务未启动")

	queuedDone, err := queue.enqueue(context.Background(), outboundPriorityNormalChat, func(context.Context) error {
		return nil
	})
	if err != nil {
		t.Fatalf("提交排队任务失败: %v", err)
	}

	app := &App{outboundQueue: queue}
	droppedExecuted := false
	if err := app.dispatchOutboundTask(context.Background(), outboundPriorityBroadcastAd, func(context.Context) error {
		droppedExecuted = true
		return nil
	}); err != nil {
		t.Fatalf("低优先级拥塞时应允许丢弃，实际错误=%v", err)
	}
	if droppedExecuted {
		t.Fatalf("低优先级任务拥塞时不应回退执行")
	}

	fallbackExecuted := false
	if err := app.dispatchOutboundTask(context.Background(), outboundPriorityErrorAlert, func(context.Context) error {
		fallbackExecuted = true
		return nil
	}); err != nil {
		t.Fatalf("高优先级拥塞时应回退执行，实际错误=%v", err)
	}
	if !fallbackExecuted {
		t.Fatalf("高优先级任务拥塞时应触发回退执行")
	}

	close(release)
	waitTaskDone(t, runningDone, "运行中任务")
	waitTaskDone(t, queuedDone, "排队任务")
}

func TestSendWithPriorityRecordsDroppedWhenBroadcastBackpressureDrops(t *testing.T) {
	queue := newPriorityDispatchQueue(1)
	defer closePriorityQueueForTest(t, queue)

	started := make(chan struct{})
	release := make(chan struct{})
	runningDone, err := queue.enqueue(context.Background(), outboundPriorityNormalChat, func(context.Context) error {
		close(started)
		<-release
		return nil
	})
	if err != nil {
		t.Fatalf("提交运行中任务失败: %v", err)
	}
	waitSignal(t, started, "运行中任务未启动")
	queuedDone, err := queue.enqueue(context.Background(), outboundPriorityNormalChat, func(context.Context) error {
		return nil
	})
	if err != nil {
		t.Fatalf("提交排队任务失败: %v", err)
	}

	metrics := &recordingOutboundMetrics{}
	app := &App{
		api:             &tgbotapi.BotAPI{},
		outboundQueue:   queue,
		outboundMetrics: metrics,
	}
	if err := app.sendWithPriority(outboundPriorityBroadcastAd, tgbotapi.NewMessage(-1008001, "ad")); err != nil {
		t.Fatalf("低优先级群发背压丢弃不应向调用方返回错误: %v", err)
	}

	results := metrics.sendResults()
	fallbacks := metrics.fallbackResults()
	close(release)
	waitTaskDone(t, runningDone, "运行中任务")
	waitTaskDone(t, queuedDone, "排队任务")
	if len(results) != 1 || results[0] != "send:broadcast_ad:dropped" {
		t.Fatalf("群发丢弃应记录 dropped 发送结果，got=%v", results)
	}
	if len(fallbacks) != 1 || fallbacks[0] != "broadcast_ad:dropped_backpressure" {
		t.Fatalf("群发丢弃应记录业务背压降级指标，got=%v", fallbacks)
	}
}

type recordingOutboundMetrics struct {
	mu          sync.Mutex
	sends       []string
	retryAfters []string
	fallbacks   []string
}

func (r *recordingOutboundMetrics) ObserveOutboundQueue(int, int, int)            {}
func (r *recordingOutboundMetrics) ObserveOutboundQueueSubmission(string, string) {}
func (r *recordingOutboundMetrics) ObserveOutboundDispatchFallback(priority, result string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fallbacks = append(r.fallbacks, priority+":"+result)
}
func (r *recordingOutboundMetrics) ObserveOutboundQueueWait(string, time.Duration) {}
func (r *recordingOutboundMetrics) ObserveOutboundTask(string, string, time.Duration) {
}
func (r *recordingOutboundMetrics) ObserveOutboundDeadLetter(string, string) {}
func (r *recordingOutboundMetrics) ObserveLedgerReceiptOutbox(string, time.Duration) {
}
func (r *recordingOutboundMetrics) ObserveOutboundSend(operation, priority, result string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sends = append(r.sends, operation+":"+priority+":"+result)
}
func (r *recordingOutboundMetrics) ObserveTelegramRetryAfter(operation, priority string, duration time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.retryAfters = append(r.retryAfters, operation+":"+priority+":"+duration.String())
}
func (r *recordingOutboundMetrics) sendResults() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.sends...)
}
func (r *recordingOutboundMetrics) retryAfterResults() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.retryAfters...)
}
func (r *recordingOutboundMetrics) fallbackResults() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.fallbacks...)
}

func TestSendWithPriorityRecordsTelegramRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":7}}`))
	}))
	defer server.Close()

	api := &tgbotapi.BotAPI{
		Token:  "TEST",
		Client: server.Client(),
	}
	api.SetAPIEndpoint(server.URL + "/bot%s/%s")

	queue := newPriorityDispatchQueueWithWorkers(4, 1)
	defer closePriorityQueueForTest(t, queue)
	metrics := &recordingOutboundMetrics{}
	app := &App{
		api:             api,
		outboundQueue:   queue,
		outboundMetrics: metrics,
	}

	if err := app.sendWithPriority(outboundPriorityNormalChat, tgbotapi.NewMessage(-1008002, "limited")); err == nil {
		t.Fatalf("Telegram 429 应返回错误")
	}
	if got := metrics.sendResults(); len(got) != 1 || got[0] != "send:normal_chat:rate_limited" {
		t.Fatalf("429 发送结果指标错误: got=%v", got)
	}
	if got := metrics.retryAfterResults(); len(got) != 1 || got[0] != "send:normal_chat:7s" {
		t.Fatalf("429 retry_after 指标错误: got=%v", got)
	}
}

func TestRequestWithPriorityRecordsTelegramRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":9}}`))
	}))
	defer server.Close()

	api := &tgbotapi.BotAPI{
		Token:  "TEST",
		Client: server.Client(),
	}
	api.SetAPIEndpoint(server.URL + "/bot%s/%s")

	queue := newPriorityDispatchQueueWithWorkers(4, 1)
	defer closePriorityQueueForTest(t, queue)
	metrics := &recordingOutboundMetrics{}
	app := &App{
		api:             api,
		outboundQueue:   queue,
		outboundMetrics: metrics,
	}

	if err := app.requestWithPriority(outboundPriorityNormalChat, tgbotapi.NewDeleteMessage(-1008003, 42)); err == nil {
		t.Fatalf("Telegram 429 应返回错误")
	}
	if got := metrics.sendResults(); len(got) != 1 || got[0] != "request:normal_chat:rate_limited" {
		t.Fatalf("429 请求结果指标错误: got=%v", got)
	}
	if got := metrics.retryAfterResults(); len(got) != 1 || got[0] != "request:normal_chat:9s" {
		t.Fatalf("429 retry_after 指标错误: got=%v", got)
	}
}

func TestAdminSendCompositeUsesOutboundQueueMetrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":11}}`))
	}))
	defer server.Close()

	api := &tgbotapi.BotAPI{
		Token:  "TEST",
		Client: server.Client(),
	}
	api.SetAPIEndpoint(server.URL + "/bot%s/%s")

	queue := newPriorityDispatchQueueWithWorkers(4, 1)
	defer closePriorityQueueForTest(t, queue)
	metrics := &recordingOutboundMetrics{}
	app := &App{
		api:             api,
		outboundQueue:   queue,
		outboundMetrics: metrics,
	}

	if err := app.adminSendComposite(-1008004, "text", "", "admin broadcast"); err == nil {
		t.Fatalf("Telegram 429 应返回错误")
	}
	if got := metrics.sendResults(); len(got) != 1 || got[0] != "send:broadcast_ad:rate_limited" {
		t.Fatalf("后台群发应记录统一出站发送指标，got=%v", got)
	}
	if got := metrics.retryAfterResults(); len(got) != 1 || got[0] != "send:broadcast_ad:11s" {
		t.Fatalf("后台群发应记录统一出站 retry_after 指标，got=%v", got)
	}
}

func TestAdminBroadcastDoesNotCountBackpressureDroppedMessages(t *testing.T) {
	app, database, _ := newFourLayerTestApp(t, "admin-broadcast-backpressure.db")
	sourceChatID := int64(-1008101)
	targetChatID := int64(-1008102)
	if _, err := database.EnsureGroup(sourceChatID, "后台群发源群"); err != nil {
		t.Fatalf("初始化源群失败: %v", err)
	}
	if _, err := database.EnsureGroup(targetChatID, "后台群发目标群"); err != nil {
		t.Fatalf("初始化目标群失败: %v", err)
	}
	if err := database.UpdateGroupFields(sourceChatID, map[string]any{"group_tags": "后台群发"}); err != nil {
		t.Fatalf("设置源群标签失败: %v", err)
	}
	if err := database.UpdateGroupFields(targetChatID, map[string]any{"group_tags": "后台群发"}); err != nil {
		t.Fatalf("设置目标群标签失败: %v", err)
	}

	queue := newPriorityDispatchQueue(1)
	defer closePriorityQueueForTest(t, queue)
	started := make(chan struct{})
	release := make(chan struct{})
	runningDone, err := queue.enqueue(context.Background(), outboundPriorityNormalChat, func(context.Context) error {
		close(started)
		<-release
		return nil
	})
	if err != nil {
		t.Fatalf("提交运行中任务失败: %v", err)
	}
	waitSignal(t, started, "运行中任务未启动")
	queuedDone, err := queue.enqueue(context.Background(), outboundPriorityNormalChat, func(context.Context) error {
		return nil
	})
	if err != nil {
		t.Fatalf("提交排队任务失败: %v", err)
	}
	app.outboundQueue = queue
	metrics := &recordingOutboundMetrics{}
	app.outboundMetrics = metrics

	sent, err := app.AdminBroadcast(AdminBroadcastRequest{
		SourceChatID: sourceChatID,
		TargetTags:   []string{"后台群发"},
		MediaType:    "text",
		Content:      "后台通知",
	})
	close(release)
	waitTaskDone(t, runningDone, "运行中任务")
	waitTaskDone(t, queuedDone, "排队任务")
	if err != nil {
		t.Fatalf("后台群发背压丢弃不应中断整个任务: %v", err)
	}
	if sent != 0 {
		t.Fatalf("背压丢弃不应计入已发送数量，got=%d", sent)
	}
	if got := metrics.sendResults(); len(got) != 2 || got[0] != "send:broadcast_ad:dropped" || got[1] != "send:broadcast_ad:dropped" {
		t.Fatalf("应记录两个群发 dropped 发送指标，got=%v", got)
	}
}

func closePriorityQueueForTest(t *testing.T, queue *priorityDispatchQueue) {
	t.Helper()
	closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := queue.Close(closeCtx); err != nil {
		t.Fatalf("关闭优先级队列失败: %v", err)
	}
}
