package db

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestLedgerReceiptOutboxLifecycle(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "ledger-receipt-outbox.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	input := LedgerEntryInput{
		ChatID:           8801,
		MessageID:        9901,
		UserID:           501,
		Username:         "outbox",
		Nickname:         "Outbox",
		EntryType:        "deposit",
		Amount:           100,
		Currency:         "CNY",
		BaseAmount:       100,
		FeeRate:          0,
		Rate:             1,
		FinalAmount:      100,
		Note:             "receipt-outbox",
		CardNo:           "6222000011118888",
		TrackMessageLink: "tg://msg/9901",
	}

	result, err := database.ApplyLedgerEntryWithReceiptOutbox(input, func(result LedgerApplyResult) (*LedgerReceiptOutboxInput, error) {
		return &LedgerReceiptOutboxInput{
			ChatID:          input.ChatID,
			SourceMessageID: input.MessageID,
			EntryID:         result.EntryID,
			UserID:          input.UserID,
			CommandKey:      "ledger",
			Payload:         `{"kind":"ledger_receipt"}`,
		}, nil
	})
	if err != nil {
		t.Fatalf("记账并写入 outbox 失败: %v", err)
	}
	if result.EntryID == 0 || result.Duplicate {
		t.Fatalf("记账结果异常: %+v", result)
	}

	item, err := database.ClaimLedgerReceiptOutbox(time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatalf("claim outbox 失败: %v", err)
	}
	if item == nil {
		t.Fatalf("预期存在待发送 outbox")
	}
	if item.ChatID != input.ChatID || item.EntryID != result.EntryID {
		t.Fatalf("claim 出来的 outbox 异常: %+v", item)
	}
	if item.AttemptCount != 1 {
		t.Fatalf("首次 claim 后 attempt_count 应为 1，实际=%d", item.AttemptCount)
	}

	nextAttemptAt := time.Now().UTC().Add(2 * time.Minute)
	if err := database.MarkLedgerReceiptOutboxRetry(item.ChatID, item.ID, "temporary send failure", nextAttemptAt, false); err != nil {
		t.Fatalf("标记 retry 失败: %v", err)
	}

	retryItem, err := database.ClaimLedgerReceiptOutbox(nextAttemptAt.Add(time.Second), time.Minute)
	if err != nil {
		t.Fatalf("二次 claim outbox 失败: %v", err)
	}
	if retryItem == nil {
		t.Fatalf("预期 retry outbox 可再次 claim")
	}
	if retryItem.ID != item.ID {
		t.Fatalf("再次 claim 的 outbox id 异常: want=%d got=%d", item.ID, retryItem.ID)
	}
	if retryItem.AttemptCount != 2 {
		t.Fatalf("二次 claim 后 attempt_count 应为 2，实际=%d", retryItem.AttemptCount)
	}

	if err := database.MarkLedgerReceiptOutboxSent(retryItem.ChatID, retryItem.ID, time.Now().UTC()); err != nil {
		t.Fatalf("标记 sent 失败: %v", err)
	}

	claimedAfterSent, err := database.ClaimLedgerReceiptOutbox(time.Now().UTC().Add(5*time.Minute), time.Minute)
	if err != nil {
		t.Fatalf("sent 后再次 claim 失败: %v", err)
	}
	if claimedAfterSent != nil {
		t.Fatalf("sent 后不应再 claim 到 outbox: %+v", claimedAfterSent)
	}
}

func TestLedgerReceiptOutboxRollbackWithApplyFailure(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "ledger-receipt-outbox-rollback.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	input := LedgerEntryInput{
		ChatID:           8802,
		MessageID:        9902,
		UserID:           502,
		Username:         "outbox-rollback",
		Nickname:         "OutboxRollback",
		EntryType:        "deposit",
		Amount:           66,
		Currency:         "CNY",
		BaseAmount:       66,
		FeeRate:          0,
		Rate:             1,
		FinalAmount:      66,
		Note:             "receipt-outbox-rollback",
		CardNo:           "6222000011119999",
		TrackMessageLink: "tg://msg/9902",
	}

	result, err := database.ApplyLedgerEntryWithReceiptOutbox(input, func(LedgerApplyResult) (*LedgerReceiptOutboxInput, error) {
		return nil, errors.New("inject outbox build failure")
	})
	if err == nil {
		t.Fatalf("outbox 构建失败时应整体回滚")
	}
	if result.EntryID != 0 || result.Duplicate {
		t.Fatalf("失败写入不应返回成功结果: %+v", result)
	}

	ledgerCount := mustCountRows(t, database, `SELECT COUNT(1) FROM ledger_entries WHERE chat_id = ?`, input.ChatID)
	if ledgerCount != 0 {
		t.Fatalf("回滚后 ledger_entries 不应残留: %d", ledgerCount)
	}
	outboxCount := mustCountRows(t, database, `SELECT COUNT(1) FROM ledger_receipt_outbox WHERE chat_id = ?`, input.ChatID)
	if outboxCount != 0 {
		t.Fatalf("回滚后 ledger_receipt_outbox 不应残留: %d", outboxCount)
	}
	idempotencyCount := mustCountRows(t, database, `SELECT COUNT(1) FROM message_idempotency WHERE chat_id = ? AND message_id = ? AND command_key = 'ledger'`, input.ChatID, input.MessageID)
	if idempotencyCount != 0 {
		t.Fatalf("回滚后 message_idempotency 不应残留: %d", idempotencyCount)
	}
}

func TestLedgerReceiptOutboxClaimSkipsChatAlreadyProcessing(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "ledger-receipt-outbox-chat-order.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	now := time.Now().UTC().Add(time.Second)
	sameChatFirst := mustApplyLedgerWithOutboxForTest(t, database, LedgerEntryInput{
		ChatID:      -1007001,
		MessageID:   700101,
		UserID:      7001,
		EntryType:   "deposit",
		Amount:      10,
		Currency:    "CNY",
		BaseAmount:  10,
		Rate:        1,
		FinalAmount: 10,
	})
	sameChatSecond := mustApplyLedgerWithOutboxForTest(t, database, LedgerEntryInput{
		ChatID:      -1007001,
		MessageID:   700102,
		UserID:      7001,
		EntryType:   "deposit",
		Amount:      20,
		Currency:    "CNY",
		BaseAmount:  20,
		Rate:        1,
		FinalAmount: 20,
	})
	otherChat := mustApplyLedgerWithOutboxForTest(t, database, LedgerEntryInput{
		ChatID:      -1007002,
		MessageID:   700201,
		UserID:      7002,
		EntryType:   "deposit",
		Amount:      30,
		Currency:    "CNY",
		BaseAmount:  30,
		Rate:        1,
		FinalAmount: 30,
	})

	first, err := database.ClaimLedgerReceiptOutbox(now, time.Minute)
	if err != nil {
		t.Fatalf("第一次 claim 失败: %v", err)
	}
	if first == nil || first.EntryID != sameChatFirst {
		t.Fatalf("第一次应 claim 同群第一条 outbox，got=%+v want_entry=%d", first, sameChatFirst)
	}

	second, err := database.ClaimLedgerReceiptOutbox(now, time.Minute)
	if err != nil {
		t.Fatalf("第二次 claim 失败: %v", err)
	}
	if second == nil || second.EntryID != otherChat {
		t.Fatalf("同群第一条 processing 时应跳过同群第二条，claim 其他群: got=%+v want_entry=%d same_chat_second=%d", second, otherChat, sameChatSecond)
	}

	if err := database.MarkLedgerReceiptOutboxSent(first.ChatID, first.ID, now.Add(time.Second)); err != nil {
		t.Fatalf("标记第一条 sent 失败: %v", err)
	}
	third, err := database.ClaimLedgerReceiptOutbox(now.Add(2*time.Second), time.Minute)
	if err != nil {
		t.Fatalf("第三次 claim 失败: %v", err)
	}
	if third == nil || third.EntryID != sameChatSecond {
		t.Fatalf("同群第一条完成后应 claim 同群第二条: got=%+v want_entry=%d", third, sameChatSecond)
	}
}

func TestLedgerReceiptOutboxStaleOwnerCannotOverwriteNewClaim(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "ledger-receipt-outbox-owner.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	now := time.Now().UTC().Add(time.Second)
	entryID := mustApplyLedgerWithOutboxForTest(t, database, LedgerEntryInput{
		ChatID:      -1007101,
		MessageID:   710101,
		UserID:      7101,
		EntryType:   "deposit",
		Amount:      10,
		Currency:    "CNY",
		BaseAmount:  10,
		Rate:        1,
		FinalAmount: 10,
	})

	first, err := database.ClaimLedgerReceiptOutbox(now, time.Minute)
	if err != nil {
		t.Fatalf("第一次 claim 失败: %v", err)
	}
	if first == nil || first.EntryID != entryID || first.ProcessingStarted == "" {
		t.Fatalf("第一次 claim 异常: %+v want_entry=%d", first, entryID)
	}

	staleNow := now.Add(2 * time.Minute)
	second, err := database.ClaimLedgerReceiptOutbox(staleNow, time.Minute)
	if err != nil {
		t.Fatalf("stale 后第二次 claim 失败: %v", err)
	}
	if second == nil || second.ID != first.ID || second.ProcessingStarted == first.ProcessingStarted {
		t.Fatalf("第二次 claim 应拿到同一条且 owner token 更新: first=%+v second=%+v", first, second)
	}

	ok, err := database.MarkLedgerReceiptOutboxSentIfProcessing(first.ChatID, first.ID, first.ProcessingStarted, staleNow.Add(time.Second))
	if err != nil {
		t.Fatalf("旧 owner 标记 sent 不应报错: %v", err)
	}
	if ok {
		t.Fatalf("旧 owner 不应能覆盖新 claim")
	}

	ok, err = database.MarkLedgerReceiptOutboxRetryIfProcessing(first.ChatID, first.ID, first.ProcessingStarted, "stale retry", staleNow.Add(time.Minute), false)
	if err != nil {
		t.Fatalf("旧 owner 标记 retry 不应报错: %v", err)
	}
	if ok {
		t.Fatalf("旧 owner 不应能覆盖新 claim 为 retry")
	}

	ok, err = database.MarkLedgerReceiptOutboxSentIfProcessing(second.ChatID, second.ID, second.ProcessingStarted, staleNow.Add(2*time.Second))
	if err != nil {
		t.Fatalf("新 owner 标记 sent 失败: %v", err)
	}
	if !ok {
		t.Fatalf("新 owner 应能标记 sent")
	}

	claimedAfterSent, err := database.ClaimLedgerReceiptOutbox(staleNow.Add(3*time.Second), time.Minute)
	if err != nil {
		t.Fatalf("sent 后再次 claim 失败: %v", err)
	}
	if claimedAfterSent != nil {
		t.Fatalf("sent 后不应再 claim 到 outbox: %+v", claimedAfterSent)
	}
}

func TestLedgerReceiptOutboxClaimRotatesTenantShards(t *testing.T) {
	database, err := NewDatabaseWithOptions(filepath.Join(t.TempDir(), "ledger-receipt-outbox-shards.db"), Options{
		TenantShardCount: 2,
	})
	if err != nil {
		t.Fatalf("初始化分片数据库失败: %v", err)
	}
	defer database.Close()

	shard0Chats := ledgerReceiptOutboxChatsForShard(t, 0, 2, 2)
	shard1Chats := ledgerReceiptOutboxChatsForShard(t, 1, 2, 1)
	now := time.Now().UTC().Add(time.Second)
	shard0First := mustApplyLedgerWithOutboxForTest(t, database, LedgerEntryInput{
		ChatID:      shard0Chats[0],
		MessageID:   720001,
		UserID:      7201,
		EntryType:   "deposit",
		Amount:      10,
		Currency:    "CNY",
		BaseAmount:  10,
		Rate:        1,
		FinalAmount: 10,
	})
	shard0Second := mustApplyLedgerWithOutboxForTest(t, database, LedgerEntryInput{
		ChatID:      shard0Chats[1],
		MessageID:   720002,
		UserID:      7202,
		EntryType:   "deposit",
		Amount:      20,
		Currency:    "CNY",
		BaseAmount:  20,
		Rate:        1,
		FinalAmount: 20,
	})
	shard1First := mustApplyLedgerWithOutboxForTest(t, database, LedgerEntryInput{
		ChatID:      shard1Chats[0],
		MessageID:   720003,
		UserID:      7203,
		EntryType:   "deposit",
		Amount:      30,
		Currency:    "CNY",
		BaseAmount:  30,
		Rate:        1,
		FinalAmount: 30,
	})

	first, err := database.ClaimLedgerReceiptOutbox(now, time.Minute)
	if err != nil {
		t.Fatalf("第一次 claim 失败: %v", err)
	}
	if first == nil || first.EntryID != shard0First {
		t.Fatalf("第一次应从 shard 0 claim 第一条: got=%+v want_entry=%d", first, shard0First)
	}

	second, err := database.ClaimLedgerReceiptOutbox(now, time.Minute)
	if err != nil {
		t.Fatalf("第二次 claim 失败: %v", err)
	}
	if second == nil || second.EntryID != shard1First {
		t.Fatalf("第二次应轮转到 shard 1，而不是继续取 shard 0: got=%+v want_shard1=%d shard0_second=%d", second, shard1First, shard0Second)
	}
}

func ledgerReceiptOutboxChatsForShard(t *testing.T, shardIndex, shardCount, count int) []int64 {
	t.Helper()
	chats := make([]int64, 0, count)
	for candidate := int64(-100000); len(chats) < count && candidate > -200000; candidate-- {
		if tenantShardIndexForChat(candidate, shardCount) == shardIndex {
			chats = append(chats, candidate)
		}
	}
	if len(chats) != count {
		t.Fatalf("无法为 shard=%d 找到 %d 个 chat_id", shardIndex, count)
	}
	return chats
}

func mustApplyLedgerWithOutboxForTest(t *testing.T, database *Database, input LedgerEntryInput) int64 {
	t.Helper()
	if input.Currency == "" {
		input.Currency = "CNY"
	}
	if input.Rate == 0 {
		input.Rate = 1
	}
	result, err := database.ApplyLedgerEntryWithReceiptOutbox(input, func(result LedgerApplyResult) (*LedgerReceiptOutboxInput, error) {
		return &LedgerReceiptOutboxInput{
			ChatID:          input.ChatID,
			SourceMessageID: input.MessageID,
			EntryID:         result.EntryID,
			UserID:          input.UserID,
			CommandKey:      "ledger",
			Payload:         `{"kind":"ledger_receipt"}`,
		}, nil
	})
	if err != nil {
		t.Fatalf("写入 outbox 失败: %v", err)
	}
	if result.EntryID == 0 {
		t.Fatalf("写入 outbox 未返回 entry id: %+v", result)
	}
	return result.EntryID
}
