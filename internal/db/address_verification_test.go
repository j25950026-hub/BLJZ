package db

import (
	"path/filepath"
	"testing"
	"time"
)

func TestTrackAddressVerification(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "address-verification.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("关闭数据库失败: %v", closeErr)
		}
	}()

	chatID := int64(-1005501001)
	address := "TQjRjNw6h4g7Vz3QxQ3SxWsvQ8D4Nf8oPq"

	first, err := database.TrackAddressVerification(chatID, address, 101, "alice", time.Date(2026, 2, 21, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("首次记录地址验证失败: %v", err)
	}
	if first.VerificationCount != 1 {
		t.Fatalf("首次验证次数错误: got=%d", first.VerificationCount)
	}
	if first.PreviousSenderUserID != 0 {
		t.Fatalf("首次上次发送人应为空: got=%d", first.PreviousSenderUserID)
	}

	second, err := database.TrackAddressVerification(chatID, address, 202, "bob", time.Date(2026, 2, 21, 12, 1, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("第二次记录地址验证失败: %v", err)
	}
	if second.VerificationCount != 2 {
		t.Fatalf("第二次验证次数错误: got=%d", second.VerificationCount)
	}
	if second.PreviousSenderUserID != 101 {
		t.Fatalf("第二次上次发送人应为 alice: got=%d", second.PreviousSenderUserID)
	}
	if second.PreviousSenderUsername != "alice" {
		t.Fatalf("第二次上次发送用户名错误: got=%s", second.PreviousSenderUsername)
	}
	if second.LastSenderUserID != 202 {
		t.Fatalf("第二次当前发送人错误: got=%d", second.LastSenderUserID)
	}
}

func TestTrackAddressVerificationIsolatedAcrossChats(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "address-verification-global.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("关闭数据库失败: %v", closeErr)
		}
	}()

	address := "TQjRjNw6h4g7Vz3QxQ3SxWsvQ8D4Nf8oPq"
	first, err := database.TrackAddressVerification(-1005502001, address, 101, "alice", time.Date(2026, 2, 21, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("A 群首次记录地址验证失败: %v", err)
	}
	if first.VerificationCount != 1 {
		t.Fatalf("A 群首次验证次数错误: got=%d", first.VerificationCount)
	}

	second, err := database.TrackAddressVerification(-1005502002, address, 202, "bob", time.Date(2026, 2, 21, 12, 1, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("B 群首次记录地址验证失败: %v", err)
	}
	if second.VerificationCount != 1 {
		t.Fatalf("B 群首次验证次数应按群从 1 开始: got=%d", second.VerificationCount)
	}
	if second.PreviousSenderUserID != 0 {
		t.Fatalf("B 群首次验证不应读取 A 群上次发送人: got=%d", second.PreviousSenderUserID)
	}
	if second.PreviousSenderUsername != "" {
		t.Fatalf("B 群首次验证不应读取 A 群上次发送用户名: got=%s", second.PreviousSenderUsername)
	}

	third, err := database.TrackAddressVerification(-1005502001, address, 303, "carol", time.Date(2026, 2, 21, 12, 2, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("A 群第二次记录地址验证失败: %v", err)
	}
	if third.VerificationCount != 2 {
		t.Fatalf("A 群第二次验证次数应只累计 A 群记录: got=%d", third.VerificationCount)
	}
	if third.PreviousSenderUserID != 101 || third.PreviousSenderUsername != "alice" {
		t.Fatalf("A 群第二次验证应读取 A 群上次发送人: got=%d/%s", third.PreviousSenderUserID, third.PreviousSenderUsername)
	}
}

func TestTrackAddressVerificationIsolatedAcrossTenantShards(t *testing.T) {
	database, err := NewDatabaseWithOptions(filepath.Join(t.TempDir(), "address-verification-sharded.db"), Options{TenantShardCount: 2})
	if err != nil {
		t.Fatalf("初始化分片数据库失败: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("关闭数据库失败: %v", closeErr)
		}
	}()

	chatA := int64(-1005503001)
	chatB := int64(0)
	for candidate := int64(-1005503002); candidate > -1005503100; candidate-- {
		if tenantShardIndexForChat(candidate, 2) != tenantShardIndexForChat(chatA, 2) {
			chatB = candidate
			break
		}
	}
	if chatB == 0 {
		t.Fatalf("未找到不同分片的测试群")
	}

	address := "TQjRjNw6h4g7Vz3QxQ3SxWsvQ8D4Nf8oPq"
	first, err := database.TrackAddressVerification(chatA, address, 101, "alice", time.Date(2026, 2, 21, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("A 分片首次记录地址验证失败: %v", err)
	}
	if first.VerificationCount != 1 {
		t.Fatalf("A 分片首次验证次数错误: got=%d", first.VerificationCount)
	}

	second, err := database.TrackAddressVerification(chatB, address, 202, "bob", time.Date(2026, 2, 21, 12, 1, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("B 分片首次记录地址验证失败: %v", err)
	}
	if second.VerificationCount != 1 {
		t.Fatalf("B 分片首次验证次数应按群从 1 开始: got=%d", second.VerificationCount)
	}
	if second.PreviousSenderUserID != 0 {
		t.Fatalf("B 分片首次验证不应读取 A 分片上次发送人: got=%d", second.PreviousSenderUserID)
	}
	if second.PreviousSenderUsername != "" {
		t.Fatalf("B 分片首次验证不应读取 A 分片上次发送用户名: got=%s", second.PreviousSenderUsername)
	}

	third, err := database.TrackAddressVerification(chatA, address, 303, "carol", time.Date(2026, 2, 21, 12, 2, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("A 分片第二次记录地址验证失败: %v", err)
	}
	if third.VerificationCount != 2 {
		t.Fatalf("A 分片第二次验证次数应只累计 A 群记录: got=%d", third.VerificationCount)
	}
	if third.PreviousSenderUserID != 101 || third.PreviousSenderUsername != "alice" {
		t.Fatalf("A 分片第二次验证应读取 A 群上次发送人: got=%d/%s", third.PreviousSenderUserID, third.PreviousSenderUsername)
	}
}

func TestTrackAddressVerificationIgnoresLegacyGlobalSnapshot(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "address-verification-legacy.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("关闭数据库失败: %v", closeErr)
		}
	}()

	const address = "TQjRjNw6h4g7Vz3QxQ3SxWsvQ8D4Nf8oPq"
	_, err = database.raw.Exec(`
INSERT INTO address_verification_stats (
	chat_id, address, verification_count, last_sender_user_id, last_sender_username, last_verified_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		int64(0),
		address,
		9,
		909,
		"legacy",
		"2026-02-21T11:59:00Z",
		"2026-02-21T11:59:00Z",
	)
	if err != nil {
		t.Fatalf("写入 legacy 全局统计失败: %v", err)
	}

	snapshot, err := database.TrackAddressVerification(-1005502501, address, 101, "alice", time.Date(2026, 2, 21, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("记录新群地址验证失败: %v", err)
	}
	if snapshot.VerificationCount != 1 {
		t.Fatalf("新群首次验证不应继承 legacy 全局次数: got=%d", snapshot.VerificationCount)
	}
	if snapshot.PreviousSenderUserID != 0 || snapshot.PreviousSenderUsername != "" {
		t.Fatalf("新群首次验证不应继承 legacy 全局发送人: got=%d/%s", snapshot.PreviousSenderUserID, snapshot.PreviousSenderUsername)
	}
}

func TestAddressVerificationMessageTracksOriginalAndEditedAddress(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "address-verification-message-edit.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("关闭数据库失败: %v", closeErr)
		}
	}()

	chatID := int64(-1005504001)
	messageID := 550401
	originalAddress := "TQjRjNw6h4g7Vz3QxQ3SxWsvQ8D4Nf8oPq"
	editedAddress := "TNo2PhgN4DMmtyvb5chhY6WJyuQSAhaA3B"

	if err := database.TrackAddressVerificationMessage(
		chatID,
		messageID,
		101,
		"alice",
		originalAddress,
		time.Date(2026, 7, 12, 16, 30, 0, 0, time.UTC),
	); err != nil {
		t.Fatalf("记录原始地址消息失败: %v", err)
	}
	if err := database.MarkAddressVerificationMessageEdited(
		chatID,
		messageID,
		editedAddress,
		time.Date(2026, 7, 12, 16, 31, 0, 0, time.UTC),
	); err != nil {
		t.Fatalf("记录地址消息编辑失败: %v", err)
	}

	record, ok, err := database.GetAddressVerificationMessage(chatID, messageID)
	if err != nil {
		t.Fatalf("读取地址消息记录失败: %v", err)
	}
	if !ok {
		t.Fatalf("缺少地址消息记录")
	}
	if record.OriginalAddress != originalAddress {
		t.Fatalf("原始地址不应被编辑覆盖: got=%s", record.OriginalAddress)
	}
	if record.CurrentAddress != editedAddress {
		t.Fatalf("当前地址应记录编辑后地址: got=%s", record.CurrentAddress)
	}
	if record.EditCount != 1 {
		t.Fatalf("编辑次数应为 1: got=%d", record.EditCount)
	}
}
