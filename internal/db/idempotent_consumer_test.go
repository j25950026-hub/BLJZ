package db

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestControlPoint453IdempotentConsumer(t *testing.T) {
	t.Run("控制点 4.5.3 功能验证", func(t *testing.T) {
		database, err := NewDatabase(filepath.Join(t.TempDir(), "idempotent-consumer.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		input := IdempotentMessageInput{
			ChatID:     -100453001,
			Topic:      "ledger.sync",
			MessageKey: "mq-453-001",
			Payload:    `{"event":"ledger.settle","entry_id":45301}`,
		}

		enqueue1, err := database.EnqueueIdempotentMessage(input)
		if err != nil {
			t.Fatalf("首次入队失败: %v", err)
		}
		if enqueue1.Duplicate || enqueue1.MessageID == 0 {
			t.Fatalf("首次入队应成功写入: %+v", enqueue1)
		}

		enqueue2, err := database.EnqueueIdempotentMessage(input)
		if err != nil {
			t.Fatalf("重复入队失败: %v", err)
		}
		if !enqueue2.Duplicate {
			t.Fatalf("重复入队应命中幂等: %+v", enqueue2)
		}
		if enqueue2.MessageID != enqueue1.MessageID {
			t.Fatalf("重复入队应返回同一消息 ID: first=%d duplicate=%d", enqueue1.MessageID, enqueue2.MessageID)
		}

		consumeInput := IdempotentConsumeInput{
			ChatID:   input.ChatID,
			Topic:    input.Topic,
			Consumer: "ledger-worker-A",
			Lease:    500 * time.Millisecond,
		}

		failErr := errors.New("inject consumer failure")
		firstHandleCalls := 0
		firstResult, err := database.ConsumeIdempotentMessage(consumeInput, func(message IdempotentQueuedMessage) error {
			firstHandleCalls++
			if message.MessageKey != input.MessageKey || message.Payload != input.Payload {
				t.Fatalf("首次消费消息内容异常: %+v", message)
			}
			if message.DeliveryCount != 1 {
				t.Fatalf("首次消费投递次数应为 1: %+v", message)
			}
			return failErr
		})
		if err == nil || !strings.Contains(err.Error(), failErr.Error()) {
			t.Fatalf("首次消费应返回处理失败: %v", err)
		}
		if !firstResult.Polled || firstResult.Consumed || firstResult.Duplicate {
			t.Fatalf("首次消费结果异常: %+v", firstResult)
		}
		if firstHandleCalls != 1 {
			t.Fatalf("首次消费处理器调用次数错误: %d", firstHandleCalls)
		}

		var statusAfterFail string
		var deliveryAfterFail int
		var lastErrAfterFail string
		if err := database.raw.QueryRow(
			`SELECT status, delivery_count, last_error FROM idempotent_consumer_messages WHERE id = ?`,
			enqueue1.MessageID,
		).Scan(&statusAfterFail, &deliveryAfterFail, &lastErrAfterFail); err != nil {
			t.Fatalf("查询失败后消息状态失败: %v", err)
		}
		if statusAfterFail != idempotentConsumerStatusPending {
			t.Fatalf("失败后消息应回到 pending: status=%s", statusAfterFail)
		}
		if deliveryAfterFail != 1 {
			t.Fatalf("失败后投递次数应累计为 1: %d", deliveryAfterFail)
		}
		if !strings.Contains(lastErrAfterFail, failErr.Error()) {
			t.Fatalf("失败原因应被记录: %s", lastErrAfterFail)
		}

		secondHandleCalls := 0
		secondResult, err := database.ConsumeIdempotentMessage(consumeInput, func(message IdempotentQueuedMessage) error {
			secondHandleCalls++
			if message.DeliveryCount != 2 {
				t.Fatalf("重试消费投递次数应为 2: %+v", message)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("重试消费应成功: %v", err)
		}
		if !secondResult.Polled || !secondResult.Consumed || secondResult.Duplicate {
			t.Fatalf("重试消费结果异常: %+v", secondResult)
		}
		if secondHandleCalls != 1 {
			t.Fatalf("重试消费处理器调用次数错误: %d", secondHandleCalls)
		}

		if _, err := database.raw.Exec(
			`UPDATE idempotent_consumer_messages SET status = ?, lease_until_unix = 0, updated_at = ? WHERE id = ?`,
			idempotentConsumerStatusPending,
			nowUTCText(),
			enqueue1.MessageID,
		); err != nil {
			t.Fatalf("模拟消息重复投递失败: %v", err)
		}

		duplicateHandleCalls := 0
		duplicateResult, err := database.ConsumeIdempotentMessage(consumeInput, func(message IdempotentQueuedMessage) error {
			duplicateHandleCalls++
			return nil
		})
		if err != nil {
			t.Fatalf("重复投递消费失败: %v", err)
		}
		if !duplicateResult.Polled || !duplicateResult.Consumed || !duplicateResult.Duplicate {
			t.Fatalf("重复投递应命中幂等: %+v", duplicateResult)
		}
		if duplicateHandleCalls != 0 {
			t.Fatalf("重复投递命中幂等时不应再次执行业务处理: %d", duplicateHandleCalls)
		}

		checkpointCount := mustCountRows(
			t,
			database,
			`SELECT COUNT(1) FROM idempotent_consumer_checkpoints WHERE chat_id = ? AND topic = ? AND consumer_name = ? AND message_key = ?`,
			input.ChatID,
			input.Topic,
			consumeInput.Consumer,
			input.MessageKey,
		)
		if checkpointCount != 1 {
			t.Fatalf("幂等检查点数量应为 1: %d", checkpointCount)
		}

		finalStatus := ""
		finalDelivery := 0
		if err := database.raw.QueryRow(
			`SELECT status, delivery_count FROM idempotent_consumer_messages WHERE id = ?`,
			enqueue1.MessageID,
		).Scan(&finalStatus, &finalDelivery); err != nil {
			t.Fatalf("查询最终消息状态失败: %v", err)
		}
		if finalStatus != idempotentConsumerStatusConsumed {
			t.Fatalf("最终消息状态应为 consumed: %s", finalStatus)
		}
		if finalDelivery != 3 {
			t.Fatalf("重复投递后 delivery_count 应累计为 3: %d", finalDelivery)
		}

		emptyResult, err := database.ConsumeIdempotentMessage(consumeInput, func(IdempotentQueuedMessage) error {
			return nil
		})
		if err != nil {
			t.Fatalf("空队列轮询失败: %v", err)
		}
		if emptyResult.Polled || emptyResult.Consumed || emptyResult.Duplicate {
			t.Fatalf("空队列结果异常: %+v", emptyResult)
		}
	})
}
