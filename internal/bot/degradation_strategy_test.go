package bot

import (
	"context"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestControlPoint234DegradationStrategy(t *testing.T) {
	t.Run("控制点 2.3.4 功能验证", func(t *testing.T) {
		app := &App{
			webhookUpdates: make(chan tgbotapi.Update, 10),
		}
		for i := 0; i < 8; i++ {
			app.webhookUpdates <- tgbotapi.Update{UpdateID: i + 1}
		}

		if !app.isHighLoad() {
			t.Fatalf("Webhook 队列达到 80%% 时应进入高负载态")
		}
		if !app.shouldDropByDegradation(nil, "汇率对比") {
			t.Fatalf("高负载下应丢弃查询指令")
		}
		if !app.shouldDropByDegradation(nil, "1+2*3") {
			t.Fatalf("高负载下应丢弃娱乐指令")
		}
		if app.shouldDropByDegradation(nil, "+100") {
			t.Fatalf("高负载下不应丢弃记账指令")
		}
		if app.shouldDropByDegradation(nil, "记账撤回") {
			t.Fatalf("高负载下不应丢弃记账撤回指令")
		}
	})
}

func TestDegradationUsesBookkeepingQueueLoad(t *testing.T) {
	queue := newSerializedExecutionQueue(1, 5)
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := queue.Close(closeCtx); err != nil {
			t.Fatalf("关闭队列失败: %v", err)
		}
	}()

	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	if _, err := queue.enqueue(context.Background(), 1001, func(context.Context) error {
		close(started)
		<-release
		return nil
	}); err != nil {
		t.Fatalf("提交阻塞任务失败: %v", err)
	}
	<-started

	for i := 0; i < 4; i++ {
		if _, err := queue.enqueue(context.Background(), 1001, func(context.Context) error { return nil }); err != nil {
			t.Fatalf("填充队列失败: %v", err)
		}
	}

	app := &App{bookkeepingQueue: queue}
	if !app.isHighLoad() {
		t.Fatalf("记账队列达到 80%% 时应进入高负载态")
	}
	if !app.shouldDropByDegradation(nil, "查卡+6222020000000000") {
		t.Fatalf("高负载下应丢弃查询类指令")
	}
	if app.shouldDropByDegradation(nil, "设置费率10") {
		t.Fatalf("业务配置指令不属于查询/娱乐，不应被丢弃")
	}
}

func TestDegradationUsesOutboundQueueLoad(t *testing.T) {
	queue := newPriorityDispatchQueue(5)
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := queue.Close(closeCtx); err != nil {
			t.Fatalf("关闭队列失败: %v", err)
		}
	}()

	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	if _, err := queue.enqueue(context.Background(), outboundPriorityNormalChat, func(context.Context) error {
		close(started)
		<-release
		return nil
	}); err != nil {
		t.Fatalf("提交阻塞任务失败: %v", err)
	}
	<-started

	for i := 0; i < 4; i++ {
		if _, err := queue.enqueue(context.Background(), outboundPriorityBroadcastAd, func(context.Context) error { return nil }); err != nil {
			t.Fatalf("填充队列失败: %v", err)
		}
	}

	app := &App{outboundQueue: queue}
	if !app.isHighLoad() {
		t.Fatalf("消息发送队列达到 80%% 时应进入高负载态")
	}
	if !app.shouldDropByDegradation(nil, "汇率对比") {
		t.Fatalf("高负载下应丢弃查询类指令")
	}
	if app.shouldDropByDegradation(nil, "设置费率10") {
		t.Fatalf("业务配置指令不属于查询/娱乐，不应被丢弃")
	}
}
