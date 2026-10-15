package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestControlPoint221MultiTenantRouting(t *testing.T) {
	t.Run("控制点 2.2.1 功能验证", func(t *testing.T) {
		const shardCount = 4
		dbPath := filepath.Join(t.TempDir(), "tenant-routing.db")
		database, err := NewDatabaseWithOptions(dbPath, Options{TenantShardCount: shardCount})
		if err != nil {
			t.Fatalf("初始化分片数据库失败: %v", err)
		}
		defer database.Close()

		chatA := int64(-100101)
		chatB := int64(-100202)
		if tenantShardIndexForChat(chatA, shardCount) == tenantShardIndexForChat(chatB, shardCount) {
			chatB = -100303
		}
		if tenantShardIndexForChat(chatA, shardCount) == tenantShardIndexForChat(chatB, shardCount) {
			t.Fatalf("测试数据未命中不同分片: chatA=%d chatB=%d", chatA, chatB)
		}

		if _, err := database.EnsureGroup(chatA, "tenant-a"); err != nil {
			t.Fatalf("写入租户A群配置失败: %v", err)
		}
		if _, err := database.EnsureGroup(chatB, "tenant-b"); err != nil {
			t.Fatalf("写入租户B群配置失败: %v", err)
		}

		_, err = database.ApplyLedgerEntry(LedgerEntryInput{
			ChatID:      chatA,
			MessageID:   1,
			UserID:      2001,
			EntryType:   "deposit",
			Amount:      100,
			Currency:    "CNY",
			BaseAmount:  100,
			FeeRate:     0,
			Rate:        1,
			FinalAmount: 100,
		})
		if err != nil {
			t.Fatalf("写入租户A流水失败: %v", err)
		}
		_, err = database.ApplyLedgerEntry(LedgerEntryInput{
			ChatID:      chatB,
			MessageID:   1,
			UserID:      2002,
			EntryType:   "deposit",
			Amount:      200,
			Currency:    "CNY",
			BaseAmount:  200,
			FeeRate:     0,
			Rate:        1,
			FinalAmount: 200,
		})
		if err != nil {
			t.Fatalf("写入租户B流水失败: %v", err)
		}

		groups, err := database.ListAllGroups()
		if err != nil {
			t.Fatalf("聚合查询群配置失败: %v", err)
		}
		if len(groups) != 2 {
			t.Fatalf("跨分片群配置聚合数量错误: got=%d want=2", len(groups))
		}

		shardA := tenantShardPathForTest(dbPath, tenantShardIndexForChat(chatA, shardCount))
		shardB := tenantShardPathForTest(dbPath, tenantShardIndexForChat(chatB, shardCount))
		if shardA == shardB {
			t.Fatalf("租户分片路径不应相同: %s", shardA)
		}

		assertRowCount(t, shardA, `SELECT COUNT(1) FROM group_config WHERE chat_id = ?`, chatA, 1)
		assertRowCount(t, shardA, `SELECT COUNT(1) FROM group_config WHERE chat_id = ?`, chatB, 0)
		assertRowCount(t, shardA, `SELECT COUNT(1) FROM ledger_entries WHERE chat_id = ?`, chatA, 1)
		assertRowCount(t, shardA, `SELECT COUNT(1) FROM ledger_entries WHERE chat_id = ?`, chatB, 0)

		assertRowCount(t, shardB, `SELECT COUNT(1) FROM group_config WHERE chat_id = ?`, chatA, 0)
		assertRowCount(t, shardB, `SELECT COUNT(1) FROM group_config WHERE chat_id = ?`, chatB, 1)
		assertRowCount(t, shardB, `SELECT COUNT(1) FROM ledger_entries WHERE chat_id = ?`, chatA, 0)
		assertRowCount(t, shardB, `SELECT COUNT(1) FROM ledger_entries WHERE chat_id = ?`, chatB, 1)
	})
}

func TestControlPoint412GroupHashSharding(t *testing.T) {
	t.Run("控制点 4.1.2 功能验证", func(t *testing.T) {
		const shardCount = 8
		dbPath := filepath.Join(t.TempDir(), "group-hash-sharding.db")
		database, err := NewDatabaseWithOptions(dbPath, Options{TenantShardCount: shardCount})
		if err != nil {
			t.Fatalf("初始化分片数据库失败: %v", err)
		}
		defer database.Close()

		groupA, shardA, shardByModulo, ok := findGroupIDWithHashModuloDiff(shardCount)
		if !ok {
			t.Fatalf("未找到可验证哈希分片差异的 Group_ID 样本")
		}
		if shardA == shardByModulo {
			t.Fatalf("哈希分片应与直接取模分片不同: group=%d hash=%d modulo=%d", groupA, shardA, shardByModulo)
		}

		groupB, shardB, ok := findGroupIDOnDifferentShard(groupA, shardCount)
		if !ok {
			t.Fatalf("未找到不同分片的 Group_ID 样本: base_group=%d base_shard=%d", groupA, shardA)
		}
		if shardA == shardB {
			t.Fatalf("测试样本未命中不同分片: groupA=%d groupB=%d shard=%d", groupA, groupB, shardA)
		}

		for shardIndex := 1; shardIndex < shardCount; shardIndex++ {
			shardPath := tenantShardPathForTest(dbPath, shardIndex)
			if _, err := os.Stat(shardPath); err != nil {
				t.Fatalf("分片文件不存在: shard=%d path=%s err=%v", shardIndex, shardPath, err)
			}
		}

		if _, err := database.EnsureGroup(groupA, "group-a"); err != nil {
			t.Fatalf("写入 Group_A 配置失败: %v", err)
		}
		if _, err := database.EnsureGroup(groupB, "group-b"); err != nil {
			t.Fatalf("写入 Group_B 配置失败: %v", err)
		}

		for i := 0; i < 2; i++ {
			_, err = database.ApplyLedgerEntry(LedgerEntryInput{
				ChatID:      groupA,
				MessageID:   100 + i,
				UserID:      8100,
				EntryType:   "deposit",
				Amount:      50,
				Currency:    "CNY",
				BaseAmount:  50,
				FeeRate:     0,
				Rate:        1,
				FinalAmount: 50,
			})
			if err != nil {
				t.Fatalf("写入 Group_A 流水失败: %v", err)
			}
		}
		_, err = database.ApplyLedgerEntry(LedgerEntryInput{
			ChatID:      groupB,
			MessageID:   200,
			UserID:      8200,
			EntryType:   "deposit",
			Amount:      70,
			Currency:    "CNY",
			BaseAmount:  70,
			FeeRate:     0,
			Rate:        1,
			FinalAmount: 70,
		})
		if err != nil {
			t.Fatalf("写入 Group_B 流水失败: %v", err)
		}

		groups, err := database.ListAllGroups()
		if err != nil {
			t.Fatalf("跨分片聚合查询失败: %v", err)
		}
		if len(groups) != 2 {
			t.Fatalf("跨分片聚合数量错误: got=%d want=2", len(groups))
		}

		shardPathA := tenantShardPathForTest(dbPath, shardA)
		shardPathB := tenantShardPathForTest(dbPath, shardB)
		assertRowCount(t, shardPathA, `SELECT COUNT(1) FROM group_config WHERE chat_id = ?`, groupA, 1)
		assertRowCount(t, shardPathA, `SELECT COUNT(1) FROM group_config WHERE chat_id = ?`, groupB, 0)
		assertRowCount(t, shardPathA, `SELECT COUNT(1) FROM ledger_entries WHERE chat_id = ?`, groupA, 2)
		assertRowCount(t, shardPathA, `SELECT COUNT(1) FROM ledger_entries WHERE chat_id = ?`, groupB, 0)

		assertRowCount(t, shardPathB, `SELECT COUNT(1) FROM group_config WHERE chat_id = ?`, groupA, 0)
		assertRowCount(t, shardPathB, `SELECT COUNT(1) FROM group_config WHERE chat_id = ?`, groupB, 1)
		assertRowCount(t, shardPathB, `SELECT COUNT(1) FROM ledger_entries WHERE chat_id = ?`, groupA, 0)
		assertRowCount(t, shardPathB, `SELECT COUNT(1) FROM ledger_entries WHERE chat_id = ?`, groupB, 1)
	})
}

func tenantShardPathForTest(basePath string, shardIndex int) string {
	if shardIndex == 0 {
		return basePath
	}
	return buildTenantShardPath(basePath, shardIndex)
}

func findGroupIDWithHashModuloDiff(shardCount int) (int64, int, int, bool) {
	for groupID := int64(-100000); groupID > -200000; groupID-- {
		hashShard := tenantShardIndexForGroup(groupID, shardCount)
		moduloShard := int(absInt64ToUint64(groupID) % uint64(shardCount))
		if hashShard != moduloShard {
			return groupID, hashShard, moduloShard, true
		}
	}
	return 0, 0, 0, false
}

func findGroupIDOnDifferentShard(baseGroupID int64, shardCount int) (int64, int, bool) {
	baseShard := tenantShardIndexForGroup(baseGroupID, shardCount)
	for groupID := int64(-200001); groupID > -300000; groupID-- {
		shard := tenantShardIndexForGroup(groupID, shardCount)
		if shard != baseShard {
			return groupID, shard, true
		}
	}
	return 0, 0, false
}

func assertRowCount(t *testing.T, dbPath, sqlText string, chatID int64, want int) {
	t.Helper()
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("打开数据库失败 path=%s err=%v", dbPath, err)
	}
	defer raw.Close()

	var got int
	if err := raw.QueryRow(sqlText, chatID).Scan(&got); err != nil {
		t.Fatalf("查询计数失败 path=%s err=%v", dbPath, err)
	}
	if got != want {
		t.Fatalf("分片计数错误 path=%s chat_id=%d got=%d want=%d", dbPath, chatID, got, want)
	}
}
