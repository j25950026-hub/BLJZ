package bot

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestStateBroadcast_ControlPoint_1_3_5(t *testing.T) {
	t.Run("控制点 1.3.5 功能验证 WebSocket 广播", func(t *testing.T) {
		hub := NewWebSocketStateHub()
		chatEvents, cancelChat := hub.Subscribe(123)
		defer cancelChat()
		allEvents, cancelAll := hub.Subscribe(0)
		defer cancelAll()
		otherEvents, cancelOther := hub.Subscribe(456)
		defer cancelOther()

		event := LedgerReversalStateEvent{
			Event:      "ledger.reversal",
			ChatID:     123,
			MessageID:  9001,
			OriginalID: 101,
			ReversalID: 202,
		}
		if err := hub.BroadcastReversal(context.Background(), event); err != nil {
			t.Fatalf("WebSocket 广播失败: %v", err)
		}

		mustReceiveEvent(t, chatEvents, func(received LedgerReversalStateEvent) {
			if received.ChatID != event.ChatID || received.ReversalID != event.ReversalID {
				t.Fatalf("定向订阅消息异常: %+v", received)
			}
			if received.OccurredAt == "" {
				t.Fatalf("广播事件应自动补齐 occurred_at")
			}
		})
		mustReceiveEvent(t, allEvents, func(received LedgerReversalStateEvent) {
			if received.OriginalID != event.OriginalID {
				t.Fatalf("全量订阅消息异常: %+v", received)
			}
		})
		select {
		case leaked := <-otherEvents:
			t.Fatalf("不相关群组不应收到广播: %+v", leaked)
		case <-time.After(120 * time.Millisecond):
		}
	})

	t.Run("控制点 1.3.5 功能验证 Redis PubSub 广播", func(t *testing.T) {
		mini, err := miniredis.Run()
		if err != nil {
			t.Fatalf("启动 miniredis 失败: %v", err)
		}
		defer mini.Close()

		publisher, err := NewRedisStateBroadcaster(RedisStateBroadcasterConfig{
			Addr:           mini.Addr(),
			ChannelPrefix:  "channel:group:",
			PublishTimeout: time.Second,
		})
		if err != nil {
			t.Fatalf("创建 Redis 广播器失败: %v", err)
		}
		defer publisher.Close()

		client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
		defer client.Close()

		sub := client.Subscribe(context.Background(), "channel:group:123")
		defer sub.Close()
		if _, err := sub.Receive(context.Background()); err != nil {
			t.Fatalf("订阅频道失败: %v", err)
		}

		event := LedgerReversalStateEvent{
			Event:      "ledger.reversal",
			ChatID:     123,
			MessageID:  9002,
			OriginalID: 301,
			ReversalID: 302,
		}
		if err := publisher.BroadcastReversal(context.Background(), event); err != nil {
			t.Fatalf("Redis 广播失败: %v", err)
		}

		recvCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		msg, err := sub.ReceiveMessage(recvCtx)
		if err != nil {
			t.Fatalf("读取 Redis 广播消息失败: %v", err)
		}
		if msg.Channel != "channel:group:123" {
			t.Fatalf("广播频道错误: %s", msg.Channel)
		}

		received := LedgerReversalStateEvent{}
		if err := json.Unmarshal([]byte(msg.Payload), &received); err != nil {
			t.Fatalf("解析广播载荷失败: %v", err)
		}
		if received.OriginalID != event.OriginalID || received.ReversalID != event.ReversalID {
			t.Fatalf("广播载荷数据异常: %+v", received)
		}
		if received.OccurredAt == "" {
			t.Fatalf("Redis 广播应自动补齐 occurred_at")
		}
	})
}

func mustReceiveEvent(t *testing.T, ch <-chan LedgerReversalStateEvent, check func(LedgerReversalStateEvent)) {
	t.Helper()
	select {
	case event := <-ch:
		check(event)
	case <-time.After(time.Second):
		t.Fatalf("等待广播事件超时")
	}
}
