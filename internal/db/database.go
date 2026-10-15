package db

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go-bot/internal/accounting"

	_ "modernc.org/sqlite"
)

type Database struct {
	raw     *sql.DB
	readRaw *sql.DB
	mu      sync.RWMutex

	dataCipher *sensitiveDataCipher

	waitTimeout           time.Duration
	deadlockRetryMax      int
	deadlockBackoffBase   time.Duration
	deadlockBackoffJitter time.Duration
	retryWait             func(ctx context.Context, delay time.Duration) error
	randIntn              func(n int) int

	hotAccountBatch     *hotAccountBatcher
	hotAccountFlushStop chan struct{}
	hotAccountFlushDone chan struct{}

	coldArchiveRaw       *sql.DB
	hotDataRetention     time.Duration
	coldArchiveBatchSize int
	coldArchiveSweepStop chan struct{}
	coldArchiveSweepDone chan struct{}

	tenantRouter           *tenantRouter
	contextCache           ContextCache
	groupKeepAliveMu       sync.Mutex
	groupKeepAliveInterval time.Duration
	groupKeepAliveQueue    chan int64
	groupKeepAliveStop     chan struct{}
	groupKeepAlivePending  map[int64]struct{}
	groupKeepAliveLastKick map[int64]time.Time
	groupKeepAliveWG       sync.WaitGroup
	orderSearch            OrderSearchBackend
	orderQueryBloom        *orderQueryBloomFilter
	orderQueryBloomBlocked atomic.Int64
}

type tenantRouter struct {
	shards               []*Database
	nextOutboxClaimShard atomic.Uint64
}

const trialBalanceCriticalAlert = "Critical_Alert_Ledger_Imbalance"
const accountBalanceLockSQL = `SELECT balance FROM account_balances WHERE user_id = ? AND currency = ? FOR UPDATE`

const (
	defaultDBWaitTimeout          = time.Second
	defaultDeadlockRetryMax       = 3
	defaultDeadlockBackoffBase    = 50 * time.Millisecond
	defaultDeadlockBackoffJitter  = 25 * time.Millisecond
	defaultHotAccountBatchSize    = 8
	defaultHotAccountFlushWindow  = 250 * time.Millisecond
	defaultHotDataRetention       = 90 * 24 * time.Hour
	defaultColdArchiveSweep       = time.Hour
	defaultColdArchiveBatchSize   = 500
	defaultAccountBalanceCASMax   = 3
	defaultTenantShardCount       = 1
	defaultDeadLetterListLimit    = 50
	maxDeadLetterListLimit        = 200
	deadLetterRetentionSize       = 2000
	defaultOrderSearchLimit       = 50
	maxOrderSearchLimit           = 200
	defaultPrivateOrderListLimit  = 50
	maxPrivateOrderListLimit      = 500
	defaultGroupKeepAliveInterval = 10 * time.Second
	defaultGroupKeepAliveWorkers  = 2
	defaultGroupKeepAliveQueueCap = 1024

	scheduleShardIDShift = 48
	scheduleLocalIDMask  = (int64(1) << scheduleShardIDShift) - 1

	reversalAuditRiskEventType  = "reversal_rate_high"
	reversalAuditRiskSeverity   = "warn"
	reversalAuditMinActionCount = 8
	reversalAuditRateThreshold  = 0.35
	duplicateAlertWindow        = 10 * time.Minute

	duplicateAlertDimensionAmount  = "amount"
	duplicateAlertDimensionCard    = "card"
	duplicateAlertDimensionAddress = "address"
	duplicateAlertDimensionAccount = "account"
)

var (
	duplicateAlertTRC20Pattern = regexp.MustCompile(`^T[1-9A-HJ-NP-Za-km-z]{33}$`)
	duplicateAlertERC20Pattern = regexp.MustCompile(`^0[xX][0-9a-fA-F]{40}$`)
	duplicateAlertTRC20Finder  = regexp.MustCompile(`\bT[1-9A-HJ-NP-Za-km-z]{33}\b`)
	duplicateAlertERC20Finder  = regexp.MustCompile(`\b0[xX][0-9a-fA-F]{40}\b`)
	groupChatMigrationTables   = []string{
		"group_config",
		"operators",
		"ledger_entries",
		"custom_contents",
		"auto_replies",
		"identities",
		"evaluations",
		"timers",
		"scheduled_broadcasts",
		"group_users",
		"card_history",
		"duplicate_alert_history",
		"forward_rules",
		"delayed_queries",
		"user_runtime_state",
		"temp_data_staging",
		"branch_code_overrides",
		"address_verification_stats",
		"address_verification_messages",
		"ledger_balances",
		"daily_settlement_reports",
		"day_cut_settlement_reports",
		"group_closing_reminder_config",
		"group_closing_dm_recipients",
		"group_closing_reminder_runs",
		"message_idempotency",
		"audit_logs",
		"risk_events",
		"outbound_dead_letters",
		"ledger_receipt_outbox",
	}
)

type Options struct {
	WaitTimeout                    time.Duration
	DeadlockRetryMax               int
	DeadlockBackoffBase            time.Duration
	DeadlockBackoffJitter          time.Duration
	DataEncryptionKey              string
	ReadReplicaPath                string
	SQLiteJournalMode              string
	SQLiteSynchronous              string
	SQLiteOptimizeOnStart          bool
	SQLiteIncrementalVacuumOnStart bool
	HotAccountUserIDs              []int64
	HotAccountBatchSize            int
	HotAccountFlushWindow          time.Duration
	ColdArchivePath                string
	HotDataRetention               time.Duration
	ColdArchiveSweep               time.Duration
	ColdArchiveBatchSize           int
	TenantShardCount               int
}

func NewDatabase(path string) (*Database, error) {
	return NewDatabaseWithOptions(path, Options{})
}

func NewDatabaseWithOptions(path string, opts Options) (*Database, error) {
	normalized := normalizeOptions(opts)
	readReplicaPath := resolveReadReplicaPath(path, normalized.ReadReplicaPath)
	database, err := newSingleDatabase(path, readReplicaPath, normalized)
	if err != nil {
		return nil, err
	}
	if normalized.TenantShardCount <= 1 {
		return database, nil
	}

	router := &tenantRouter{
		shards: make([]*Database, normalized.TenantShardCount),
	}
	router.shards[0] = database

	shardOpts := normalized
	shardOpts.TenantShardCount = 1
	for shardIndex := 1; shardIndex < normalized.TenantShardCount; shardIndex++ {
		shardPath := buildTenantShardPath(path, shardIndex)
		shardReadPath := buildTenantShardPath(readReplicaPath, shardIndex)
		if normalized.ColdArchivePath != "" {
			shardOpts.ColdArchivePath = buildTenantShardPath(normalized.ColdArchivePath, shardIndex)
		}
		shardDB, shardErr := newSingleDatabase(shardPath, shardReadPath, shardOpts)
		if shardErr != nil {
			_ = closeShardList(router.shards, database)
			_ = database.closeSelf()
			return nil, shardErr
		}
		router.shards[shardIndex] = shardDB
	}
	database.tenantRouter = router
	return database, nil
}

func resolveReadReplicaPath(masterPath, replicaPath string) string {
	trimmed := strings.TrimSpace(replicaPath)
	if trimmed == "" {
		return masterPath
	}
	return trimmed
}

func openSQLite(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	raw.SetMaxOpenConns(1)
	return raw, nil
}

func sameDBPath(pathA, pathB string) bool {
	if pathA == pathB {
		return true
	}
	absA, errA := filepath.Abs(pathA)
	absB, errB := filepath.Abs(pathB)
	if errA == nil && errB == nil {
		return absA == absB
	}
	return filepath.Clean(pathA) == filepath.Clean(pathB)
}

func ensureReadReplicaSchema(raw *sql.DB) error {
	requiredTables := []string{
		"ledger_entries",
		"ledger_balances",
	}
	for _, table := range requiredTables {
		var exists int
		if err := raw.QueryRow(`SELECT COUNT(1) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return fmt.Errorf("读库缺少必要表: %s", table)
		}
	}
	return nil
}

func newSingleDatabase(path string, readReplicaPath string, opts Options) (*Database, error) {
	raw, err := openSQLite(path)
	if err != nil {
		return nil, err
	}
	d := &Database{
		raw:                   raw,
		readRaw:               raw,
		waitTimeout:           opts.WaitTimeout,
		deadlockRetryMax:      opts.DeadlockRetryMax,
		deadlockBackoffBase:   opts.DeadlockBackoffBase,
		deadlockBackoffJitter: opts.DeadlockBackoffJitter,
		retryWait:             waitForRetryWithContext,
		randIntn:              rand.Intn,
	}
	cipher, err := newSensitiveDataCipher(opts.DataEncryptionKey)
	if err != nil {
		_ = raw.Close()
		return nil, err
	}
	d.dataCipher = cipher
	if err := d.configureWaitTimeout(); err != nil {
		_ = raw.Close()
		return nil, err
	}
	if err := configureSQLiteConnectionPRAGMAs(raw, opts); err != nil {
		_ = raw.Close()
		return nil, err
	}
	if err := d.init(); err != nil {
		_ = raw.Close()
		return nil, err
	}
	if err := runSQLiteStartupMaintenance(raw, opts); err != nil {
		_ = raw.Close()
		return nil, err
	}
	if !sameDBPath(path, readReplicaPath) {
		readRaw, readErr := openSQLite(readReplicaPath)
		if readErr != nil {
			_ = raw.Close()
			return nil, readErr
		}
		if err := configureWaitTimeoutForDB(readRaw, d.waitTimeout); err != nil {
			_ = readRaw.Close()
			_ = raw.Close()
			return nil, err
		}
		if err := ensureReadReplicaSchema(readRaw); err != nil {
			_ = readRaw.Close()
			_ = raw.Close()
			return nil, err
		}
		d.readRaw = readRaw
	}
	d.hotAccountBatch = newHotAccountBatcher(opts.HotAccountUserIDs, opts.HotAccountBatchSize)
	if d.hotAccountBatch != nil && opts.HotAccountFlushWindow > 0 {
		d.hotAccountFlushStop = make(chan struct{})
		d.hotAccountFlushDone = make(chan struct{})
		go d.runHotAccountFlushWorker(opts.HotAccountFlushWindow)
	}
	if err := d.initColdArchive(path, opts); err != nil {
		_ = d.closeColdArchive()
		if d.readRaw != nil && d.readRaw != d.raw {
			_ = d.readRaw.Close()
		}
		_ = d.raw.Close()
		return nil, err
	}
	d.initOrderQueryBloom()
	d.initGroupKeepAlive(defaultGroupKeepAliveInterval, defaultGroupKeepAliveWorkers, defaultGroupKeepAliveQueueCap)
	return d, nil
}

func (d *Database) Close() error {
	if d.tenantRouter != nil {
		closeErr := closeShardList(d.tenantRouter.shards, d)
		d.tenantRouter = nil
		if err := d.closeSelf(); err != nil && closeErr == nil {
			closeErr = err
		}
		return closeErr
	}
	return d.closeSelf()
}

func (d *Database) closeSelf() error {
	d.stopGroupKeepAlive()
	if d.hotAccountFlushStop != nil {
		close(d.hotAccountFlushStop)
		<-d.hotAccountFlushDone
		d.hotAccountFlushStop = nil
		d.hotAccountFlushDone = nil
	}
	if d.coldArchiveSweepStop != nil {
		close(d.coldArchiveSweepStop)
		<-d.coldArchiveSweepDone
		d.coldArchiveSweepStop = nil
		d.coldArchiveSweepDone = nil
	}
	if err := d.flushHotAccountPending(); err != nil {
		_ = d.raw.Close()
		if d.readRaw != nil && d.readRaw != d.raw {
			_ = d.readRaw.Close()
		}
		_ = d.closeColdArchive()
		return err
	}
	closeErr := d.raw.Close()
	if d.readRaw != nil && d.readRaw != d.raw {
		if err := d.readRaw.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	if err := d.closeColdArchive(); err != nil && closeErr == nil {
		closeErr = err
	}
	d.readRaw = nil
	return closeErr
}

func closeShardList(shards []*Database, exclude *Database) error {
	var closeErr error
	for _, shard := range shards {
		if shard == nil {
			continue
		}
		if shard == exclude {
			continue
		}
		if err := shard.closeSelf(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func normalizeOptions(opts Options) Options {
	normalized := opts
	normalized.ReadReplicaPath = strings.TrimSpace(normalized.ReadReplicaPath)
	normalized.DataEncryptionKey = strings.TrimSpace(normalized.DataEncryptionKey)
	normalized.SQLiteJournalMode = strings.ToUpper(strings.TrimSpace(normalized.SQLiteJournalMode))
	normalized.SQLiteSynchronous = strings.ToUpper(strings.TrimSpace(normalized.SQLiteSynchronous))
	if normalized.WaitTimeout <= 0 {
		normalized.WaitTimeout = defaultDBWaitTimeout
	}
	if normalized.DeadlockRetryMax <= 0 {
		normalized.DeadlockRetryMax = defaultDeadlockRetryMax
	}
	if normalized.DeadlockBackoffBase <= 0 {
		normalized.DeadlockBackoffBase = defaultDeadlockBackoffBase
	}
	if normalized.DeadlockBackoffJitter <= 0 {
		normalized.DeadlockBackoffJitter = defaultDeadlockBackoffJitter
	}
	normalized.HotAccountUserIDs = normalizeHotAccountUserIDs(normalized.HotAccountUserIDs)
	if len(normalized.HotAccountUserIDs) > 0 {
		if normalized.HotAccountBatchSize <= 0 {
			normalized.HotAccountBatchSize = defaultHotAccountBatchSize
		}
		if normalized.HotAccountFlushWindow <= 0 {
			normalized.HotAccountFlushWindow = defaultHotAccountFlushWindow
		}
	} else {
		normalized.HotAccountBatchSize = 0
		normalized.HotAccountFlushWindow = 0
	}
	normalized.ColdArchivePath = strings.TrimSpace(normalized.ColdArchivePath)
	if normalized.ColdArchivePath != "" {
		if normalized.HotDataRetention <= 0 {
			normalized.HotDataRetention = defaultHotDataRetention
		}
		if normalized.ColdArchiveSweep <= 0 {
			normalized.ColdArchiveSweep = defaultColdArchiveSweep
		}
		if normalized.ColdArchiveBatchSize <= 0 {
			normalized.ColdArchiveBatchSize = defaultColdArchiveBatchSize
		}
	} else {
		normalized.HotDataRetention = 0
		normalized.ColdArchiveSweep = 0
		normalized.ColdArchiveBatchSize = 0
	}
	if normalized.TenantShardCount <= 0 {
		normalized.TenantShardCount = defaultTenantShardCount
	}
	return normalized
}

func buildTenantShardPath(basePath string, shardIndex int) string {
	ext := filepath.Ext(basePath)
	base := strings.TrimSuffix(basePath, ext)
	return fmt.Sprintf("%s.tenant_%02d%s", base, shardIndex, ext)
}

func absInt64ToUint64(v int64) uint64 {
	if v >= 0 {
		return uint64(v)
	}
	return uint64(-(v + 1)) + 1
}

func tenantShardHash(groupID int64) uint64 {
	var payload [8]byte
	binary.LittleEndian.PutUint64(payload[:], uint64(groupID))

	hasher := fnv.New64a()
	_, _ = hasher.Write(payload[:])
	return hasher.Sum64()
}

func tenantShardIndexForGroup(groupID int64, shardCount int) int {
	if shardCount <= 1 || groupID == 0 {
		return 0
	}
	return int(tenantShardHash(groupID) % uint64(shardCount))
}

func tenantShardIndexForChat(chatID int64, shardCount int) int {
	return tenantShardIndexForGroup(chatID, shardCount)
}

func (d *Database) databaseForChat(chatID int64) *Database {
	if d == nil || d.tenantRouter == nil || chatID == 0 {
		return d
	}
	index := tenantShardIndexForChat(chatID, len(d.tenantRouter.shards))
	if index < 0 || index >= len(d.tenantRouter.shards) {
		return d
	}
	shard := d.tenantRouter.shards[index]
	if shard == nil {
		return d
	}
	return shard
}

func (d *Database) SetContextCache(cache ContextCache) {
	if d == nil {
		return
	}
	d.contextCache = cache
	if d.tenantRouter == nil {
		return
	}
	for _, shard := range d.tenantRouter.shards {
		if shard == nil {
			continue
		}
		shard.contextCache = cache
	}
}

func (d *Database) SetOrderSearchBackend(backend OrderSearchBackend) {
	if d == nil {
		return
	}
	d.orderSearch = backend
	if d.tenantRouter == nil {
		return
	}
	for _, shard := range d.tenantRouter.shards {
		if shard == nil {
			continue
		}
		shard.orderSearch = backend
	}
}

func (d *Database) OrderQueryBloomEnabled() bool {
	return d != nil && d.orderQueryBloom != nil
}

func (d *Database) initOrderQueryBloom() {
	if d == nil {
		return
	}
	bloom := newOrderQueryBloomFilter(defaultOrderQueryBloomBits, defaultOrderQueryBloomHashCount)
	if err := bloom.warmupFromDB(d.reportDB(), d.decryptSensitiveFieldValue); err != nil {
		return
	}
	d.orderQueryBloom = bloom
}

func (d *Database) shouldShortCircuitOrderQuery(chatID int64, keyword string, amount *float64) bool {
	if d == nil || d.orderQueryBloom == nil {
		return false
	}
	// Read-replica topology may advance independently; avoid hard negative gating to prevent false negatives.
	if d.readRaw != nil && d.readRaw != d.raw {
		return false
	}
	mayContain, supported := d.orderQueryBloom.mayContainQuery(chatID, keyword, amount)
	if !supported || mayContain {
		return false
	}
	d.orderQueryBloomBlocked.Add(1)
	return true
}

func (d *Database) indexOrderQueryBloomEntry(item LedgerEntryInput) {
	if d == nil || d.orderQueryBloom == nil {
		return
	}
	d.orderQueryBloom.addLedgerEntry(item.ChatID, item.Amount, item.Note, item.Username, item.Nickname)
}

func (d *Database) scheduleShardIndex(chatID int64) int {
	if d == nil || d.tenantRouter == nil {
		return 0
	}
	return tenantShardIndexForChat(chatID, len(d.tenantRouter.shards))
}

func (d *Database) encodeScheduleID(shardIndex int, localID int64) int64 {
	if d == nil || d.tenantRouter == nil || shardIndex <= 0 {
		return localID
	}
	if localID <= 0 || localID > scheduleLocalIDMask {
		return localID
	}
	return (int64(shardIndex) << scheduleShardIDShift) | localID
}

func (d *Database) decodeScheduleID(scheduleID int64) (int, int64, bool) {
	if d == nil || d.tenantRouter == nil {
		return 0, scheduleID, true
	}
	if scheduleID <= 0 {
		return 0, 0, false
	}
	shardIndex := int(scheduleID >> scheduleShardIDShift)
	if shardIndex == 0 {
		return 0, scheduleID, true
	}
	if shardIndex >= len(d.tenantRouter.shards) {
		return 0, 0, false
	}
	localID := scheduleID & scheduleLocalIDMask
	if localID <= 0 {
		return 0, 0, false
	}
	return shardIndex, localID, true
}

func normalizeHotAccountUserIDs(userIDs []int64) []int64 {
	if len(userIDs) == 0 {
		return nil
	}
	seen := make(map[int64]struct{}, len(userIDs))
	normalized := make([]int64, 0, len(userIDs))
	for _, userID := range userIDs {
		if userID <= 0 {
			continue
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		normalized = append(normalized, userID)
	}
	return normalized
}

func (d *Database) runHotAccountFlushWorker(window time.Duration) {
	ticker := time.NewTicker(window)
	defer ticker.Stop()
	defer close(d.hotAccountFlushDone)

	for {
		select {
		case <-ticker.C:
			_ = d.flushHotAccountPending()
		case <-d.hotAccountFlushStop:
			_ = d.flushHotAccountPending()
			return
		}
	}
}

func configureWaitTimeoutForDB(raw *sql.DB, waitTimeout time.Duration) error {
	waitTimeoutMS := int(waitTimeout / time.Millisecond)
	if waitTimeoutMS <= 0 {
		waitTimeoutMS = 1
	}
	if _, err := raw.Exec(fmt.Sprintf("PRAGMA busy_timeout = %d", waitTimeoutMS)); err != nil {
		return fmt.Errorf("设置 busy_timeout 失败: %w", err)
	}
	return nil
}

func configureSQLiteConnectionPRAGMAs(raw *sql.DB, opts Options) error {
	if raw == nil {
		return nil
	}
	if mode := strings.ToUpper(strings.TrimSpace(opts.SQLiteJournalMode)); mode != "" {
		if !validSQLiteJournalMode(mode) {
			return fmt.Errorf("invalid sqlite journal mode %q", opts.SQLiteJournalMode)
		}
		var applied string
		if err := raw.QueryRow(fmt.Sprintf("PRAGMA journal_mode = %s", mode)).Scan(&applied); err != nil {
			return fmt.Errorf("设置 journal_mode=%s 失败: %w", mode, err)
		}
	}
	if synchronous := strings.ToUpper(strings.TrimSpace(opts.SQLiteSynchronous)); synchronous != "" {
		if !validSQLiteSynchronousMode(synchronous) {
			return fmt.Errorf("invalid sqlite synchronous mode %q", opts.SQLiteSynchronous)
		}
		if _, err := raw.Exec(fmt.Sprintf("PRAGMA synchronous = %s", synchronous)); err != nil {
			return fmt.Errorf("设置 synchronous=%s 失败: %w", synchronous, err)
		}
	}
	return nil
}

func runSQLiteStartupMaintenance(raw *sql.DB, opts Options) error {
	if raw == nil {
		return nil
	}
	if opts.SQLiteIncrementalVacuumOnStart {
		if _, err := raw.Exec(`PRAGMA incremental_vacuum`); err != nil {
			return fmt.Errorf("执行 incremental_vacuum 失败: %w", err)
		}
	}
	if opts.SQLiteOptimizeOnStart {
		if _, err := raw.Exec(`PRAGMA optimize`); err != nil {
			return fmt.Errorf("执行 optimize 失败: %w", err)
		}
	}
	return nil
}

func validSQLiteJournalMode(mode string) bool {
	switch mode {
	case "DELETE", "TRUNCATE", "PERSIST", "MEMORY", "WAL", "OFF":
		return true
	default:
		return false
	}
}

func validSQLiteSynchronousMode(mode string) bool {
	switch mode {
	case "OFF", "NORMAL", "FULL", "EXTRA":
		return true
	default:
		return false
	}
}

func (d *Database) configureWaitTimeout() error {
	return configureWaitTimeoutForDB(d.raw, d.waitTimeout)
}

func (d *Database) reportDB() *sql.DB {
	if d == nil {
		return nil
	}
	if d.readRaw == nil {
		return d.raw
	}
	return d.readRaw
}

func (d *Database) init() error {
	schema := `
CREATE TABLE IF NOT EXISTS group_config (
	chat_id INTEGER PRIMARY KEY,
	title TEXT DEFAULT '',
	owner_user_id INTEGER DEFAULT 0,
	bookkeeping_enabled INTEGER DEFAULT 0,
	bookkeeping_cycle_key TEXT DEFAULT '',
	display_nickname INTEGER DEFAULT 1,
	show_usdt_conversion INTEGER DEFAULT 0,
	fee_rate REAL DEFAULT 0,
	payout_fee_rate REAL DEFAULT 0,
	custom_rate REAL DEFAULT 1,
	payout_rate REAL DEFAULT 1,
	use_realtime_rate INTEGER DEFAULT 0,
	rate_source TEXT DEFAULT 'binance',
	day_cut_time TEXT DEFAULT '04:00:00',
	payout_address TEXT DEFAULT '',
	payout_address_remark TEXT DEFAULT '',
	exclusive_address TEXT DEFAULT '',
	exclusive_address_owner TEXT DEFAULT '',
	exclusive_address_remark TEXT DEFAULT '',
	guarantee_rule TEXT DEFAULT '',
	group_type TEXT DEFAULT '',
	duplicate_alert_enabled INTEGER DEFAULT 0,
	group_tags TEXT DEFAULT '',
	at_broadcast_enabled INTEGER DEFAULT 0,
	auto_reply_enabled INTEGER DEFAULT 1,
	custom_content_with_time INTEGER DEFAULT 0,
	class_start_ad TEXT DEFAULT '上课时间到，请专注工作，非必要信息暂缓发送。',
	class_end_ad TEXT DEFAULT '下课时间到，可恢复正常交流。',
	welcome_enabled INTEGER DEFAULT 0,
	welcome_text TEXT DEFAULT '欢迎加入本群，请联系操作人了解群规则。',
	ledger_card_hide_reversed INTEGER NOT NULL DEFAULT 1,
	created_at TEXT DEFAULT CURRENT_TIMESTAMP,
	updated_at TEXT DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS group_exclusive_addresses (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	chat_id INTEGER NOT NULL,
	address TEXT NOT NULL,
	owner TEXT DEFAULT '',
	remark TEXT NOT NULL,
	created_at TEXT DEFAULT CURRENT_TIMESTAMP,
	updated_at TEXT DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(chat_id, address)
);

CREATE TABLE IF NOT EXISTS group_closing_reminder_config (
	chat_id INTEGER PRIMARY KEY,
	enabled INTEGER NOT NULL DEFAULT 0,
	reminder_time TEXT NOT NULL DEFAULT '04:00',
	threshold_u REAL NOT NULL DEFAULT 1.5,
	dm_mode TEXT NOT NULL DEFAULT 'owner_operators_custom',
	text_normal TEXT DEFAULT '',
	text_pending TEXT DEFAULT '',
	text_no_data TEXT DEFAULT '',
	text_review TEXT DEFAULT '',
	text_failed TEXT DEFAULT '',
	updated_by INTEGER DEFAULT 0,
	updated_at TEXT DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS group_closing_dm_recipients (
	chat_id INTEGER NOT NULL,
	user_id INTEGER NOT NULL,
	username TEXT DEFAULT '',
	nickname TEXT DEFAULT '',
	added_by INTEGER DEFAULT 0,
	added_at TEXT DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY(chat_id, user_id)
);

CREATE TABLE IF NOT EXISTS group_closing_reminder_runs (
	chat_id INTEGER NOT NULL,
	business_date TEXT NOT NULL,
	status TEXT NOT NULL,
	sent_at TEXT DEFAULT CURRENT_TIMESTAMP,
	manual INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY(chat_id, business_date)
);

CREATE TABLE IF NOT EXISTS operators (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	chat_id INTEGER NOT NULL,
	user_id INTEGER NOT NULL,
	username TEXT DEFAULT '',
	is_global INTEGER DEFAULT 0,
	created_at TEXT DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(chat_id, user_id, is_global)
);

CREATE TABLE IF NOT EXISTS global_owners (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL UNIQUE,
	username TEXT DEFAULT '',
	created_at TEXT DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS ledger_entries (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	chat_id INTEGER NOT NULL,
	message_id INTEGER NOT NULL,
	user_id INTEGER NOT NULL,
	username TEXT DEFAULT '',
	nickname TEXT DEFAULT '',
	entry_type TEXT NOT NULL,
	amount REAL NOT NULL,
	currency TEXT NOT NULL,
	base_amount REAL NOT NULL,
	fee_rate REAL NOT NULL,
	rate REAL NOT NULL,
	final_amount REAL NOT NULL,
	note TEXT DEFAULT '',
	card_no TEXT DEFAULT '',
	track_message_link TEXT DEFAULT '',
	created_at TEXT DEFAULT CURRENT_TIMESTAMP,
	reversed INTEGER DEFAULT 0,
	reversal_of INTEGER,
	FOREIGN KEY(reversal_of) REFERENCES ledger_entries(id)
);

CREATE TABLE IF NOT EXISTS custom_contents (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	chat_id INTEGER NOT NULL,
	trigger_key TEXT NOT NULL,
	content TEXT NOT NULL,
	created_by INTEGER DEFAULT 0,
	created_at TEXT DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(chat_id, trigger_key)
);

CREATE TABLE IF NOT EXISTS admin_saved_views (
	owner_username TEXT NOT NULL DEFAULT '',
	name TEXT NOT NULL,
	filters_json TEXT NOT NULL DEFAULT '{}',
	created_at TEXT DEFAULT CURRENT_TIMESTAMP,
	updated_at TEXT DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY(owner_username, name)
);

CREATE TABLE IF NOT EXISTS admin_change_snapshots (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	owner_username TEXT NOT NULL DEFAULT '',
	action TEXT NOT NULL,
	summary_json TEXT NOT NULL DEFAULT '{}',
	status TEXT NOT NULL DEFAULT 'completed',
	created_at TEXT DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS admin_change_snapshot_items (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	snapshot_id INTEGER NOT NULL,
	chat_id INTEGER NOT NULL DEFAULT 0,
	before_json TEXT NOT NULL DEFAULT '{}',
	after_json TEXT NOT NULL DEFAULT '{}',
	status TEXT NOT NULL DEFAULT '',
	failure_reason TEXT NOT NULL DEFAULT '',
	created_at TEXT DEFAULT CURRENT_TIMESTAMP,
	FOREIGN KEY(snapshot_id) REFERENCES admin_change_snapshots(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS admin_template_library (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	kind TEXT NOT NULL,
	name TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	version INTEGER NOT NULL,
	content_json TEXT NOT NULL,
	owner_username TEXT NOT NULL DEFAULT '',
	created_at TEXT DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(kind, name, version)
);

CREATE TABLE IF NOT EXISTS admin_retry_items (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	operation_id TEXT NOT NULL,
	item_key TEXT NOT NULL,
	kind TEXT NOT NULL DEFAULT '',
	chat_id INTEGER NOT NULL DEFAULT 0,
	payload_json TEXT NOT NULL DEFAULT '{}',
	failure_reason TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'failed',
	attempt_count INTEGER NOT NULL DEFAULT 0,
	created_at TEXT DEFAULT CURRENT_TIMESTAMP,
	updated_at TEXT DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(operation_id, item_key)
);

CREATE TABLE IF NOT EXISTS admin_setup_state (
	singleton_id INTEGER PRIMARY KEY CHECK (singleton_id = 1),
	consumed_at INTEGER,
	consumed_by TEXT NOT NULL DEFAULT '',
	credential_fingerprint TEXT NOT NULL DEFAULT '',
	auth_epoch INTEGER NOT NULL DEFAULT 1,
	created_at INTEGER NOT NULL DEFAULT 0,
	updated_at INTEGER NOT NULL DEFAULT 0
);

INSERT OR IGNORE INTO admin_setup_state
	(singleton_id, consumed_at, consumed_by, credential_fingerprint, auth_epoch, created_at, updated_at)
VALUES (1, NULL, '', '', 1, 0, 0);

CREATE TABLE IF NOT EXISTS admin_sessions (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_handle TEXT NOT NULL UNIQUE,
	token_hash TEXT NOT NULL UNIQUE,
	username TEXT NOT NULL,
	auth_epoch INTEGER NOT NULL,
	is_primary INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL,
	last_seen_at INTEGER NOT NULL,
	expires_at INTEGER,
	user_agent TEXT NOT NULL DEFAULT '',
	remote_addr TEXT NOT NULL DEFAULT '',
	revoked_at INTEGER
);

CREATE INDEX IF NOT EXISTS idx_admin_sessions_active
	ON admin_sessions(username, revoked_at, expires_at);
CREATE INDEX IF NOT EXISTS idx_admin_sessions_primary_lookup
	ON admin_sessions(is_primary, revoked_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_admin_sessions_one_primary
	ON admin_sessions(is_primary)
	WHERE is_primary = 1 AND revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS admin_device_bindings (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	binding_id TEXT NOT NULL UNIQUE,
	token_hash TEXT NOT NULL UNIQUE,
	username TEXT NOT NULL,
	initiator_session_handle TEXT NOT NULL,
	target_cookie_hash TEXT,
	status TEXT NOT NULL DEFAULT 'pending',
	status_version INTEGER NOT NULL DEFAULT 1,
	target_user_agent TEXT NOT NULL DEFAULT '',
	target_remote_addr TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	claimed_at INTEGER,
	approved_at INTEGER,
	expires_at INTEGER NOT NULL,
	completed_at INTEGER
);
CREATE INDEX IF NOT EXISTS idx_admin_device_bindings_status
	ON admin_device_bindings(username, status, expires_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_admin_device_bindings_one_active
	ON admin_device_bindings(initiator_session_handle)
	WHERE status IN ('pending', 'claimed');

CREATE TABLE IF NOT EXISTS admin_group_dictionary_items (
	kind TEXT NOT NULL,
	name TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	sort_order INTEGER NOT NULL DEFAULT 0,
	created_at TEXT DEFAULT CURRENT_TIMESTAMP,
	updated_at TEXT DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY(kind, name)
);

CREATE TABLE IF NOT EXISTS auto_replies (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	chat_id INTEGER NOT NULL,
	keyword TEXT DEFAULT '',
	content TEXT NOT NULL,
	enabled INTEGER DEFAULT 1,
	created_at TEXT DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS identities (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	chat_id INTEGER NOT NULL,
	identity_name TEXT NOT NULL,
	user_id INTEGER NOT NULL,
	username TEXT DEFAULT '',
	created_at TEXT DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(chat_id, identity_name, user_id)
);

CREATE TABLE IF NOT EXISTS evaluations (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	chat_id INTEGER NOT NULL,
	user_id INTEGER NOT NULL,
	username TEXT DEFAULT '',
	target_type TEXT DEFAULT 'group',
	target_user_id INTEGER DEFAULT 0,
	score INTEGER NOT NULL,
	comment TEXT DEFAULT '',
	created_at TEXT DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS timers (
	chat_id INTEGER NOT NULL,
	user_id INTEGER NOT NULL,
	started_at TEXT NOT NULL,
	PRIMARY KEY(chat_id, user_id)
);

CREATE TABLE IF NOT EXISTS scheduled_broadcasts (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	chat_id INTEGER NOT NULL,
	send_at TEXT NOT NULL,
	content TEXT NOT NULL,
	source_chat_id INTEGER DEFAULT 0,
	source_message_id INTEGER DEFAULT 0,
	identity_name TEXT DEFAULT '',
	target_tags TEXT DEFAULT '',
	status TEXT DEFAULT 'pending',
	creator_user_id INTEGER DEFAULT 0,
	created_at TEXT DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS group_users (
	chat_id INTEGER NOT NULL,
	user_id INTEGER NOT NULL,
	username TEXT DEFAULT '',
	nickname TEXT DEFAULT '',
	last_seen_at TEXT DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY(chat_id, user_id)
);

CREATE TABLE IF NOT EXISTS group_user_aliases (
	user_id INTEGER NOT NULL,
	username TEXT NOT NULL,
	source_chat_id INTEGER NOT NULL DEFAULT 0,
	first_seen_at TEXT NOT NULL,
	last_seen_at TEXT NOT NULL,
	PRIMARY KEY(user_id, username)
);

CREATE TABLE IF NOT EXISTS card_history (
	chat_id INTEGER NOT NULL,
	card_no TEXT NOT NULL,
	seen_count INTEGER DEFAULT 1,
	last_entry_id INTEGER DEFAULT 0,
	last_seen_at TEXT DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY(chat_id, card_no)
);

CREATE TABLE IF NOT EXISTS duplicate_alert_history (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	chat_id INTEGER NOT NULL,
	entry_id INTEGER NOT NULL,
	dimension TEXT NOT NULL,
	fingerprint TEXT NOT NULL,
	created_at TEXT DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS forward_rules (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	chat_id INTEGER NOT NULL,
	keyword TEXT NOT NULL,
	target_chat_ids TEXT NOT NULL,
	enabled INTEGER DEFAULT 1,
	UNIQUE(chat_id, keyword)
);

CREATE TABLE IF NOT EXISTS delayed_queries (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	chat_id INTEGER NOT NULL,
	creator_user_id INTEGER NOT NULL,
	keyword TEXT DEFAULT '',
	amount REAL,
	note TEXT DEFAULT '',
	status TEXT DEFAULT 'pending',
	created_at TEXT DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS user_runtime_state (
	chat_id INTEGER NOT NULL,
	user_id INTEGER NOT NULL,
	next_payout_rate REAL,
	updated_at TEXT DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY(chat_id, user_id)
);

CREATE TABLE IF NOT EXISTS temp_data_staging (
	chat_id INTEGER NOT NULL,
	user_id INTEGER NOT NULL,
	stage_key TEXT NOT NULL,
	payload TEXT NOT NULL DEFAULT '{}',
	expire_at_unix INTEGER NOT NULL DEFAULT 0,
	updated_at TEXT DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY(chat_id, user_id, stage_key)
);

CREATE TABLE IF NOT EXISTS branch_code_overrides (
	chat_id INTEGER NOT NULL,
	card_no TEXT NOT NULL,
	cnaps_code TEXT NOT NULL,
	branch_name TEXT DEFAULT '',
	note TEXT DEFAULT '',
	updated_by INTEGER DEFAULT 0,
	updated_at TEXT DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY(chat_id, card_no)
);

CREATE TABLE IF NOT EXISTS private_user_profiles (
	user_id INTEGER PRIMARY KEY,
	trial_status TEXT DEFAULT '',
	trial_applied_at TEXT DEFAULT '',
	expire_at TEXT DEFAULT '',
	updated_at TEXT DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS private_purchase_orders (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL,
	plan_name TEXT NOT NULL DEFAULT '',
	duration_days INTEGER NOT NULL DEFAULT 0,
	amount REAL NOT NULL DEFAULT 0,
	currency TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'pending',
	request_note TEXT DEFAULT '',
	review_note TEXT DEFAULT '',
	source TEXT DEFAULT '',
	expires_at TEXT DEFAULT '',
	reviewed_by INTEGER DEFAULT 0,
	reviewed_at TEXT DEFAULT '',
	created_at TEXT DEFAULT CURRENT_TIMESTAMP,
	updated_at TEXT DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS address_verification_stats (
	chat_id INTEGER NOT NULL,
	address TEXT NOT NULL,
	verification_count INTEGER NOT NULL DEFAULT 0,
	last_sender_user_id INTEGER NOT NULL DEFAULT 0,
	last_sender_username TEXT DEFAULT '',
	last_verified_at TEXT DEFAULT '',
	updated_at TEXT DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY(chat_id, address)
);

CREATE TABLE IF NOT EXISTS address_verification_messages (
	chat_id INTEGER NOT NULL,
	message_id INTEGER NOT NULL,
	sender_user_id INTEGER NOT NULL DEFAULT 0,
	sender_username TEXT DEFAULT '',
	original_address TEXT NOT NULL DEFAULT '',
	current_address TEXT NOT NULL DEFAULT '',
	sent_at TEXT DEFAULT '',
	first_seen_at TEXT DEFAULT CURRENT_TIMESTAMP,
	last_edit_at TEXT DEFAULT '',
	edit_count INTEGER NOT NULL DEFAULT 0,
	updated_at TEXT DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY(chat_id, message_id)
);

CREATE TABLE IF NOT EXISTS ledger_balances (
	chat_id INTEGER PRIMARY KEY,
	deposit_total REAL NOT NULL DEFAULT 0,
	payout_total REAL NOT NULL DEFAULT 0,
	updated_at TEXT DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS account_balances (
	user_id INTEGER NOT NULL,
	currency TEXT NOT NULL,
	balance REAL NOT NULL DEFAULT 0,
	version INTEGER NOT NULL DEFAULT 0,
	updated_at TEXT DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY(user_id, currency)
);

CREATE TABLE IF NOT EXISTS daily_settlement_reports (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	chat_id INTEGER NOT NULL,
	report_date TEXT NOT NULL,
	period_start TEXT NOT NULL,
	period_end TEXT NOT NULL,
	total_entries INTEGER NOT NULL DEFAULT 0,
	deposit_count INTEGER NOT NULL DEFAULT 0,
	payout_count INTEGER NOT NULL DEFAULT 0,
	deposit_total REAL NOT NULL DEFAULT 0,
	payout_total REAL NOT NULL DEFAULT 0,
	net_profit REAL NOT NULL DEFAULT 0,
	currency_breakdown TEXT NOT NULL DEFAULT '[]',
	generated_at TEXT DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(chat_id, report_date)
);

CREATE TABLE IF NOT EXISTS day_cut_settlement_reports (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	chat_id INTEGER NOT NULL,
	report_date TEXT NOT NULL,
	day_cut_time TEXT NOT NULL DEFAULT '04:00:00',
	period_start TEXT NOT NULL,
	period_end TEXT NOT NULL,
	total_entries INTEGER NOT NULL DEFAULT 0,
	deposit_count INTEGER NOT NULL DEFAULT 0,
	payout_count INTEGER NOT NULL DEFAULT 0,
	deposit_fee_total REAL NOT NULL DEFAULT 0,
	payout_fee_total REAL NOT NULL DEFAULT 0,
	net_fee_total REAL NOT NULL DEFAULT 0,
	currency_breakdown TEXT NOT NULL DEFAULT '[]',
	generated_at TEXT DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(chat_id, report_date)
);

CREATE TABLE IF NOT EXISTS message_idempotency (
	chat_id INTEGER NOT NULL,
	message_id INTEGER NOT NULL,
	command_key TEXT NOT NULL,
	request_fingerprint TEXT NOT NULL DEFAULT '',
	created_at TEXT DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY(chat_id, message_id, command_key)
);

CREATE TABLE IF NOT EXISTS audit_logs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	chat_id INTEGER NOT NULL,
	message_id INTEGER NOT NULL,
	operator_user_id INTEGER NOT NULL,
	action TEXT NOT NULL,
	detail TEXT DEFAULT '',
	created_at TEXT DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS risk_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	chat_id INTEGER NOT NULL,
	message_id INTEGER DEFAULT 0,
	user_id INTEGER DEFAULT 0,
	event_type TEXT NOT NULL,
	severity TEXT DEFAULT 'info',
	detail TEXT DEFAULT '',
	created_at TEXT DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS outbound_dead_letters (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	chat_id INTEGER NOT NULL DEFAULT 0,
	priority INTEGER NOT NULL DEFAULT 2,
	operation TEXT NOT NULL DEFAULT 'send',
	message_type TEXT DEFAULT '',
	payload TEXT DEFAULT '',
	error_message TEXT NOT NULL DEFAULT '',
	created_at TEXT DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS ledger_receipt_outbox (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	chat_id INTEGER NOT NULL,
	source_message_id INTEGER NOT NULL,
	entry_id INTEGER NOT NULL,
	user_id INTEGER NOT NULL DEFAULT 0,
	command_key TEXT NOT NULL DEFAULT 'ledger',
	payload TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'pending',
	attempt_count INTEGER NOT NULL DEFAULT 0,
	last_error TEXT DEFAULT '',
	next_attempt_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	processing_started_at TEXT DEFAULT '',
	sent_at TEXT DEFAULT '',
	created_at TEXT DEFAULT CURRENT_TIMESTAMP,
	updated_at TEXT DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(chat_id, source_message_id, command_key)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_msg_id ON ledger_entries(chat_id, message_id);
CREATE INDEX IF NOT EXISTS idx_ledger_chat_created_id ON ledger_entries(chat_id, datetime(created_at), id DESC);
CREATE INDEX IF NOT EXISTS idx_ledger_chat_type_created_id ON ledger_entries(chat_id, entry_type, datetime(created_at), id DESC);
CREATE INDEX IF NOT EXISTS idx_ledger_chat_reversed_id ON ledger_entries(chat_id, reversed, id DESC);
CREATE INDEX IF NOT EXISTS idx_audit_chat_created ON audit_logs(chat_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_admin_change_snapshots_owner_created ON admin_change_snapshots(owner_username, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_admin_change_snapshot_items_snapshot ON admin_change_snapshot_items(snapshot_id, id ASC);
CREATE INDEX IF NOT EXISTS idx_admin_template_library_kind_name_version ON admin_template_library(kind, name, version DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_admin_retry_items_status_updated ON admin_retry_items(status, updated_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_risk_chat_created ON risk_events(chat_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_dead_letters_created ON outbound_dead_letters(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_ledger_receipt_outbox_pending ON ledger_receipt_outbox(status, next_attempt_at, id);
CREATE INDEX IF NOT EXISTS idx_ledger_receipt_outbox_entry ON ledger_receipt_outbox(chat_id, entry_id, id DESC);
CREATE INDEX IF NOT EXISTS idx_message_idempotency_fingerprint ON message_idempotency(request_fingerprint);
CREATE INDEX IF NOT EXISTS idx_daily_settlement_report_date ON daily_settlement_reports(report_date, chat_id);
CREATE INDEX IF NOT EXISTS idx_day_cut_settlement_report_date ON day_cut_settlement_reports(report_date, chat_id);
CREATE INDEX IF NOT EXISTS idx_group_closing_reminder_enabled_time ON group_closing_reminder_config(enabled, reminder_time, chat_id);
CREATE INDEX IF NOT EXISTS idx_group_closing_dm_recipients_chat ON group_closing_dm_recipients(chat_id, added_at);
CREATE INDEX IF NOT EXISTS idx_group_closing_reminder_runs_date ON group_closing_reminder_runs(business_date, chat_id);
CREATE INDEX IF NOT EXISTS idx_temp_data_staging_expire ON temp_data_staging(expire_at_unix);
CREATE INDEX IF NOT EXISTS idx_duplicate_alert_lookup ON duplicate_alert_history(chat_id, dimension, fingerprint, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_private_purchase_orders_user_created ON private_purchase_orders(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_private_purchase_orders_status_created ON private_purchase_orders(status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_address_verification_lookup ON address_verification_stats(chat_id, address);
CREATE INDEX IF NOT EXISTS idx_address_verification_global_address ON address_verification_stats(address, last_verified_at DESC, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_address_verification_messages_current ON address_verification_messages(current_address, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_group_exclusive_addresses_chat_updated ON group_exclusive_addresses(chat_id, updated_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_group_user_aliases_username ON group_user_aliases(username, last_seen_at DESC);
CREATE INDEX IF NOT EXISTS idx_group_user_aliases_chat_username ON group_user_aliases(source_chat_id, username, last_seen_at DESC);
CREATE INDEX IF NOT EXISTS idx_admin_saved_views_owner ON admin_saved_views(owner_username, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_admin_group_dictionary_kind_sort ON admin_group_dictionary_items(kind, sort_order, name);
`
	if _, err := d.raw.Exec(schema); err != nil {
		return err
	}
	if err := d.ensureAccountBalanceVersionColumn(); err != nil {
		return err
	}
	if err := d.ensureMessageIdempotencyFingerprintColumn(); err != nil {
		return err
	}
	if err := d.ensureGroupConfigBookkeepingCycleKeyColumn(); err != nil {
		return err
	}
	if err := d.ensureGroupConfigPayoutAddressRemarkColumn(); err != nil {
		return err
	}
	if err := d.ensureGroupConfigExclusiveAddressOwnerColumn(); err != nil {
		return err
	}
	if err := d.ensureGroupConfigExclusiveAddressRemarkColumn(); err != nil {
		return err
	}
	if err := d.ensureExclusiveAddressBookUniqueByAddress(); err != nil {
		return err
	}
	if err := d.ensureGroupConfigDuplicateAlertEnabledColumn(); err != nil {
		return err
	}
	if err := d.ensureGroupConfigWelcomeEnabledColumn(); err != nil {
		return err
	}
	if err := d.ensureGroupConfigLedgerCardHideReversedColumn(); err != nil {
		return err
	}
	return nil
}

func (d *Database) ensureAccountBalanceVersionColumn() error {
	rows, err := d.raw.Query(`PRAGMA table_info(account_balances)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var (
		cid       int
		name      string
		dataType  string
		notNull   int
		defaultV  sql.NullString
		primaryID int
	)
	for rows.Next() {
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultV, &primaryID); err != nil {
			return err
		}
		if strings.EqualFold(name, "version") {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	_, err = d.raw.Exec(`ALTER TABLE account_balances ADD COLUMN version INTEGER NOT NULL DEFAULT 0`)
	return err
}

func (d *Database) ensureMessageIdempotencyFingerprintColumn() error {
	rows, err := d.raw.Query(`PRAGMA table_info(message_idempotency)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var (
		cid       int
		name      string
		dataType  string
		notNull   int
		defaultV  sql.NullString
		primaryID int
		hasColumn bool
	)
	for rows.Next() {
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultV, &primaryID); err != nil {
			return err
		}
		if strings.EqualFold(name, "request_fingerprint") {
			hasColumn = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !hasColumn {
		if _, err := d.raw.Exec(`ALTER TABLE message_idempotency ADD COLUMN request_fingerprint TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	_, err = d.raw.Exec(`CREATE INDEX IF NOT EXISTS idx_message_idempotency_fingerprint ON message_idempotency(request_fingerprint)`)
	return err
}

func (d *Database) ensureGroupConfigBookkeepingCycleKeyColumn() error {
	rows, err := d.raw.Query(`PRAGMA table_info(group_config)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var (
		cid       int
		name      string
		dataType  string
		notNull   int
		defaultV  sql.NullString
		primaryID int
		hasColumn bool
	)
	for rows.Next() {
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultV, &primaryID); err != nil {
			return err
		}
		if strings.EqualFold(name, "bookkeeping_cycle_key") {
			hasColumn = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if hasColumn {
		return nil
	}
	_, err = d.raw.Exec(`ALTER TABLE group_config ADD COLUMN bookkeeping_cycle_key TEXT DEFAULT ''`)
	return err
}

func (d *Database) ensureGroupConfigPayoutAddressRemarkColumn() error {
	rows, err := d.raw.Query(`PRAGMA table_info(group_config)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var (
		cid       int
		name      string
		dataType  string
		notNull   int
		defaultV  sql.NullString
		primaryID int
		hasColumn bool
	)
	for rows.Next() {
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultV, &primaryID); err != nil {
			return err
		}
		if strings.EqualFold(name, "payout_address_remark") {
			hasColumn = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if hasColumn {
		return nil
	}
	_, err = d.raw.Exec(`ALTER TABLE group_config ADD COLUMN payout_address_remark TEXT DEFAULT ''`)
	return err
}

func (d *Database) ensureGroupConfigExclusiveAddressOwnerColumn() error {
	rows, err := d.raw.Query(`PRAGMA table_info(group_config)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var (
		cid       int
		name      string
		dataType  string
		notNull   int
		defaultV  sql.NullString
		primaryID int
		hasColumn bool
	)
	for rows.Next() {
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultV, &primaryID); err != nil {
			return err
		}
		if strings.EqualFold(name, "exclusive_address_owner") {
			hasColumn = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if hasColumn {
		return nil
	}
	_, err = d.raw.Exec(`ALTER TABLE group_config ADD COLUMN exclusive_address_owner TEXT DEFAULT ''`)
	return err
}

func (d *Database) ensureGroupConfigExclusiveAddressRemarkColumn() error {
	rows, err := d.raw.Query(`PRAGMA table_info(group_config)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var (
		cid       int
		name      string
		dataType  string
		notNull   int
		defaultV  sql.NullString
		primaryID int
		hasColumn bool
	)
	for rows.Next() {
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultV, &primaryID); err != nil {
			return err
		}
		if strings.EqualFold(name, "exclusive_address_remark") {
			hasColumn = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if hasColumn {
		return nil
	}
	_, err = d.raw.Exec(`ALTER TABLE group_config ADD COLUMN exclusive_address_remark TEXT DEFAULT ''`)
	return err
}

func (d *Database) ensureExclusiveAddressBookUniqueByAddress() error {
	row := d.raw.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='group_exclusive_addresses'`)
	var createSQL string
	if err := row.Scan(&createSQL); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	normalized := strings.ReplaceAll(strings.ToLower(createSQL), " ", "")
	if strings.Contains(normalized, "unique(chat_id,address)") {
		return nil
	}

	tx, err := d.raw.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS group_exclusive_addresses_tmp (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL,
			address TEXT NOT NULL,
			owner TEXT DEFAULT '',
			remark TEXT NOT NULL,
			created_at TEXT DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(chat_id, address)
		)
	`); err != nil {
		return err
	}

	if _, err := tx.Exec(`
		INSERT INTO group_exclusive_addresses_tmp(chat_id, address, owner, remark, created_at, updated_at)
		SELECT chat_id, address, owner, remark, created_at, updated_at
		FROM group_exclusive_addresses
		WHERE TRIM(address) <> ''
		ORDER BY COALESCE(updated_at, created_at, ''), id
		ON CONFLICT(chat_id, address) DO UPDATE SET
			owner = excluded.owner,
			remark = excluded.remark,
			updated_at = excluded.updated_at
	`); err != nil {
		return err
	}

	if _, err := tx.Exec(`DROP TABLE group_exclusive_addresses`); err != nil {
		return err
	}
	if _, err := tx.Exec(`ALTER TABLE group_exclusive_addresses_tmp RENAME TO group_exclusive_addresses`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_group_exclusive_addresses_chat_updated ON group_exclusive_addresses(chat_id, updated_at DESC, id DESC)`); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (d *Database) ensureGroupConfigDuplicateAlertEnabledColumn() error {
	rows, err := d.raw.Query(`PRAGMA table_info(group_config)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var (
		cid       int
		name      string
		dataType  string
		notNull   int
		defaultV  sql.NullString
		primaryID int
		hasColumn bool
	)
	for rows.Next() {
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultV, &primaryID); err != nil {
			return err
		}
		if strings.EqualFold(name, "duplicate_alert_enabled") {
			hasColumn = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if hasColumn {
		return nil
	}
	_, err = d.raw.Exec(`ALTER TABLE group_config ADD COLUMN duplicate_alert_enabled INTEGER DEFAULT 0`)
	return err
}

func (d *Database) ensureGroupConfigWelcomeEnabledColumn() error {
	rows, err := d.raw.Query(`PRAGMA table_info(group_config)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var (
		cid       int
		name      string
		dataType  string
		notNull   int
		defaultV  sql.NullString
		primaryID int
		hasColumn bool
	)
	for rows.Next() {
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultV, &primaryID); err != nil {
			return err
		}
		if strings.EqualFold(name, "welcome_enabled") {
			hasColumn = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if hasColumn {
		return nil
	}
	_, err = d.raw.Exec(`ALTER TABLE group_config ADD COLUMN welcome_enabled INTEGER DEFAULT 0`)
	return err
}

func (d *Database) ensureGroupConfigLedgerCardHideReversedColumn() error {
	rows, err := d.raw.Query(`PRAGMA table_info(group_config)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var (
		cid       int
		name      string
		dataType  string
		notNull   int
		defaultV  sql.NullString
		primaryID int
		hasColumn bool
	)
	for rows.Next() {
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultV, &primaryID); err != nil {
			return err
		}
		if strings.EqualFold(name, "ledger_card_hide_reversed") {
			hasColumn = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if hasColumn {
		return nil
	}
	_, err = d.raw.Exec(`ALTER TABLE group_config ADD COLUMN ledger_card_hide_reversed INTEGER NOT NULL DEFAULT 1`)
	return err
}

func (d *Database) EnsureGroup(chatID int64, title string) (GroupConfig, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.EnsureGroup(chatID, title)
	}
	row, err := d.GetGroup(chatID)
	if err == nil {
		if title != "" && title != row.Title {
			_ = d.UpdateGroupFields(chatID, map[string]any{"title": title})
			return d.GetGroup(chatID)
		}
		return row, nil
	}
	_, err = d.raw.Exec(
		`INSERT INTO group_config (chat_id, title, bookkeeping_enabled, fee_rate, payout_fee_rate, custom_rate, payout_rate) VALUES (?, ?, 0, 0, 0, 1, 1)`,
		chatID,
		title,
	)
	if err != nil {
		return GroupConfig{}, err
	}
	return d.GetGroup(chatID)
}

func (d *Database) GetGroup(chatID int64) (GroupConfig, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.GetGroup(chatID)
	}
	if cached, ok := d.getCachedGroupConfig(chatID); ok {
		d.scheduleGroupKeepAlive(chatID)
		return cached, nil
	}
	g, err := d.loadGroupConfigFromDB(chatID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			d.deleteCachedGroupConfig(chatID)
		}
		return GroupConfig{}, err
	}
	d.setCachedGroupConfig(g)
	return g, nil
}

func (d *Database) loadGroupConfigFromDB(chatID int64) (GroupConfig, error) {
	sqlRow := d.raw.QueryRow(`
		SELECT chat_id, title, owner_user_id, bookkeeping_enabled, bookkeeping_cycle_key, display_nickname, show_usdt_conversion,
		       fee_rate, payout_fee_rate, custom_rate, payout_rate, use_realtime_rate, rate_source, day_cut_time,
		       payout_address, payout_address_remark, exclusive_address, exclusive_address_owner, exclusive_address_remark, guarantee_rule, group_type, duplicate_alert_enabled, group_tags, at_broadcast_enabled,
		       auto_reply_enabled, custom_content_with_time, class_start_ad, class_end_ad, welcome_enabled, welcome_text, ledger_card_hide_reversed
		FROM group_config WHERE chat_id = ?`, chatID)
	var g GroupConfig
	err := sqlRow.Scan(
		&g.ChatID, &g.Title, &g.OwnerUserID, &g.BookkeepingEnabled, &g.BookkeepingCycleKey, &g.DisplayNickname, &g.ShowUSDTConversion,
		&g.FeeRate, &g.PayoutFeeRate, &g.CustomRate, &g.PayoutRate, &g.UseRealtimeRate, &g.RateSource, &g.DayCutTime,
		&g.PayoutAddress, &g.PayoutAddressRemark, &g.ExclusiveAddress, &g.ExclusiveAddressOwner, &g.ExclusiveAddressRemark, &g.GuaranteeRule, &g.GroupType, &g.DuplicateAlertEnabled, &g.GroupTags, &g.AtBroadcastEnabled,
		&g.AutoReplyEnabled, &g.CustomContentWithTime, &g.ClassStartAd, &g.ClassEndAd, &g.WelcomeEnabled, &g.WelcomeText, &g.LedgerCardHideReversed,
	)
	if err != nil {
		return GroupConfig{}, err
	}
	if g.ChatID == 0 {
		g.ChatID = chatID
	}
	return g, nil
}

func (d *Database) UpdateGroupFields(chatID int64, fields map[string]any) error {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.UpdateGroupFields(chatID, fields)
	}
	if len(fields) == 0 {
		return nil
	}
	for k := range fields {
		if !allowedGroupField(k) {
			return fmt.Errorf("unsupported group field: %s", k)
		}
	}
	ownerChanged := false
	var oldOwnerUserID int64
	var newOwnerUserID int64
	if rawOwner, ok := fields["owner_user_id"]; ok {
		if parsedOwner, ok := parseAnyInt64(rawOwner); ok {
			ownerChanged = true
			newOwnerUserID = parsedOwner
			if row, err := d.GetGroup(chatID); err == nil {
				oldOwnerUserID = row.OwnerUserID
			}
		}
	}
	sets := make([]string, 0, len(fields)+1)
	args := make([]any, 0, len(fields)+1)
	for k, v := range fields {
		sets = append(sets, fmt.Sprintf("%s = ?", quoteSQLiteIdentifier(k)))
		args = append(args, v)
	}
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, "updated_at = CURRENT_TIMESTAMP")
	args = append(args, chatID)
	sqlText := fmt.Sprintf("UPDATE group_config SET %s WHERE chat_id = ?", strings.Join(sets, ", "))
	_, err := d.raw.Exec(sqlText, args...)
	if err != nil {
		return err
	}
	d.deleteCachedGroupConfig(chatID)
	if ownerChanged {
		if oldOwnerUserID != 0 {
			d.deleteCachedUserAccess(chatID, oldOwnerUserID)
		}
		if newOwnerUserID != 0 {
			d.deleteCachedUserAccess(chatID, newOwnerUserID)
		}
	}
	return nil
}

func (d *Database) UpdateGroupTextFieldIfCurrent(chatID int64, field, currentValue, nextValue string) (bool, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.UpdateGroupTextFieldIfCurrent(chatID, field, currentValue, nextValue)
	}
	field = strings.TrimSpace(field)
	if field != "group_type" && field != "group_tags" {
		return false, fmt.Errorf("unsupported group text field: %s", field)
	}
	quotedField := quoteSQLiteIdentifier(field)
	res, err := d.raw.Exec(
		fmt.Sprintf(`UPDATE group_config SET %s = ?, updated_at = CURRENT_TIMESTAMP WHERE chat_id = ? AND COALESCE(%s, '') = ?`, quotedField, quotedField),
		nextValue,
		chatID,
		currentValue,
	)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected <= 0 {
		return false, nil
	}
	d.deleteCachedGroupConfig(chatID)
	return true, nil
}

func (d *Database) UpsertExclusiveAddress(chatID int64, address, owner, remark string) error {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.UpsertExclusiveAddress(chatID, address, owner, remark)
	}
	addr := strings.TrimSpace(address)
	ownerText := strings.TrimSpace(owner)
	remarkText := strings.TrimSpace(remark)
	if addr == "" || remarkText == "" {
		return nil
	}
	if ownerText == "" {
		ownerText = "@所属人"
	}
	_, err := d.raw.Exec(`
		INSERT INTO group_exclusive_addresses(chat_id, address, owner, remark)
		VALUES(?, ?, ?, ?)
		ON CONFLICT(chat_id, address) DO UPDATE SET
			owner = excluded.owner,
			remark = excluded.remark,
			updated_at = CURRENT_TIMESTAMP
	`, chatID, addr, ownerText, remarkText)
	return err
}

func (d *Database) ClearExclusiveAddresses(chatID int64) error {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.ClearExclusiveAddresses(chatID)
	}
	_, err := d.raw.Exec(`DELETE FROM group_exclusive_addresses WHERE chat_id = ?`, chatID)
	return err
}

func (d *Database) ListExclusiveAddresses(chatID int64) ([]ExclusiveAddressEntry, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.ListExclusiveAddresses(chatID)
	}
	rows, err := d.raw.Query(`
		SELECT id, chat_id, address, owner, remark, created_at, updated_at
		FROM group_exclusive_addresses
		WHERE chat_id = ?
		ORDER BY updated_at DESC, id DESC
	`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ExclusiveAddressEntry, 0, 8)
	for rows.Next() {
		var item ExclusiveAddressEntry
		if err := rows.Scan(&item.ID, &item.ChatID, &item.Address, &item.Owner, &item.Remark, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (d *Database) GetExclusiveAddressByRemark(chatID int64, remark string) (ExclusiveAddressEntry, bool, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.GetExclusiveAddressByRemark(chatID, remark)
	}
	remarkText := strings.TrimSpace(remark)
	if remarkText == "" {
		return ExclusiveAddressEntry{}, false, nil
	}
	row := d.raw.QueryRow(`
		SELECT id, chat_id, address, owner, remark, created_at, updated_at
		FROM group_exclusive_addresses
		WHERE chat_id = ? AND remark = ?
		ORDER BY updated_at DESC, id DESC
		LIMIT 1
	`, chatID, remarkText)
	var item ExclusiveAddressEntry
	if err := row.Scan(&item.ID, &item.ChatID, &item.Address, &item.Owner, &item.Remark, &item.CreatedAt, &item.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ExclusiveAddressEntry{}, false, nil
		}
		return ExclusiveAddressEntry{}, false, err
	}
	return item, true, nil
}

func (d *Database) DeleteExclusiveAddressesByRemark(chatID int64, remark string) (bool, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.DeleteExclusiveAddressesByRemark(chatID, remark)
	}
	remarkText := strings.TrimSpace(remark)
	if remarkText == "" {
		return false, nil
	}
	res, err := d.raw.Exec(`DELETE FROM group_exclusive_addresses WHERE chat_id = ? AND remark = ?`, chatID, remarkText)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (d *Database) MigrateGroupChatData(oldChatID, newChatID int64, newTitle string) (bool, error) {
	if oldChatID == 0 || newChatID == 0 || oldChatID == newChatID {
		return false, nil
	}
	oldDB := d.databaseForChat(oldChatID)
	newDB := d.databaseForChat(newChatID)
	if oldDB != newDB {
		return false, fmt.Errorf("跨分片群升级迁移暂不支持: old_chat=%d new_chat=%d", oldChatID, newChatID)
	}
	if oldDB != d {
		return oldDB.MigrateGroupChatData(oldChatID, newChatID, newTitle)
	}
	return d.migrateGroupChatDataLocal(oldChatID, newChatID, newTitle)
}

func (d *Database) migrateGroupChatDataLocal(oldChatID, newChatID int64, newTitle string) (bool, error) {
	normalizedTitle := strings.TrimSpace(newTitle)
	migrated := false
	affectedUserIDs := map[int64]struct{}{}
	err := d.withWriteTx(func(tx *sql.Tx) error {
		for _, tableName := range groupChatMigrationTables {
			moved, moveErr := migrateChatScopedTableTx(tx, tableName, oldChatID, newChatID)
			if moveErr != nil {
				return moveErr
			}
			if moved {
				migrated = true
			}
		}
		if !migrated {
			return nil
		}
		if normalizedTitle != "" {
			if _, err := tx.Exec(`UPDATE group_config SET title = ?, updated_at = CURRENT_TIMESTAMP WHERE chat_id = ?`, normalizedTitle, newChatID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`UPDATE scheduled_broadcasts SET source_chat_id = ? WHERE source_chat_id = ?`, newChatID, oldChatID); err != nil {
			return err
		}
		oldChatIDText := strconv.FormatInt(oldChatID, 10)
		newChatIDText := strconv.FormatInt(newChatID, 10)
		rows, err := tx.Query(`SELECT id, target_chat_ids FROM forward_rules WHERE instr(target_chat_ids, ?) > 0`, oldChatIDText)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var (
				ruleID    int64
				rawTarget string
			)
			if err := rows.Scan(&ruleID, &rawTarget); err != nil {
				return err
			}
			updatedTarget, changed := replaceForwardRuleTargetChatIDs(rawTarget, oldChatIDText, newChatIDText)
			if !changed {
				continue
			}
			if _, err := tx.Exec(`UPDATE forward_rules SET target_chat_ids = ? WHERE id = ?`, updatedTarget, ruleID); err != nil {
				return err
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		ids, err := collectMigrationAffectedUserIDsTx(tx, oldChatID, newChatID)
		if err != nil {
			return err
		}
		affectedUserIDs = ids
		return nil
	})
	if err != nil {
		return false, err
	}
	if !migrated {
		return false, nil
	}
	d.deleteCachedGroupConfig(oldChatID)
	d.deleteCachedGroupConfig(newChatID)
	for userID := range affectedUserIDs {
		d.deleteCachedUserAccess(oldChatID, userID)
		d.deleteCachedUserAccess(newChatID, userID)
		d.deleteCachedUserAccessByUserID(userID)
	}
	return true, nil
}

func migrateChatScopedTableTx(tx *sql.Tx, tableName string, oldChatID, newChatID int64) (bool, error) {
	if !allowedGroupMigrationTable(tableName) {
		return false, fmt.Errorf("unsupported migration table: %s", tableName)
	}
	columns, err := loadSQLiteTableColumnsTx(tx, tableName)
	if err != nil {
		return false, err
	}
	if len(columns) == 0 {
		return false, fmt.Errorf("表结构为空: %s", tableName)
	}
	hasChatID := false
	for _, columnName := range columns {
		if columnName == "chat_id" {
			hasChatID = true
			break
		}
	}
	if !hasChatID {
		return false, fmt.Errorf("表缺少 chat_id 列: %s", tableName)
	}
	var sourceCount int64
	countSQL := fmt.Sprintf(`SELECT COUNT(1) FROM %s WHERE chat_id = ?`, quoteSQLiteIdentifier(tableName))
	if err := tx.QueryRow(countSQL, oldChatID).Scan(&sourceCount); err != nil {
		return false, err
	}
	if sourceCount <= 0 {
		return false, nil
	}
	quotedTable := quoteSQLiteIdentifier(tableName)
	insertColumns := make([]string, 0, len(columns))
	selectColumns := make([]string, 0, len(columns))
	args := make([]any, 0, 2)
	for _, columnName := range columns {
		quotedColumn := quoteSQLiteIdentifier(columnName)
		insertColumns = append(insertColumns, quotedColumn)
		if columnName == "chat_id" {
			selectColumns = append(selectColumns, "?")
			args = append(args, newChatID)
		} else {
			selectColumns = append(selectColumns, quotedColumn)
		}
	}
	args = append(args, oldChatID)
	insertSQL := fmt.Sprintf(
		`INSERT OR REPLACE INTO %s (%s) SELECT %s FROM %s WHERE chat_id = ?`,
		quotedTable,
		strings.Join(insertColumns, ", "),
		strings.Join(selectColumns, ", "),
		quotedTable,
	)
	if _, err := tx.Exec(insertSQL, args...); err != nil {
		return false, err
	}
	deleteSQL := fmt.Sprintf(`DELETE FROM %s WHERE chat_id = ?`, quotedTable)
	if _, err := tx.Exec(deleteSQL, oldChatID); err != nil {
		return false, err
	}
	return true, nil
}

func allowedGroupMigrationTable(name string) bool {
	for _, allowed := range groupChatMigrationTables {
		if name == allowed {
			return true
		}
	}
	return false
}

func loadSQLiteTableColumnsTx(tx *sql.Tx, tableName string) ([]string, error) {
	querySQL := fmt.Sprintf(`PRAGMA table_info(%s)`, quoteSQLiteIdentifier(tableName))
	rows, err := tx.Query(querySQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := make([]string, 0, 16)
	for rows.Next() {
		var (
			cid       int
			name      string
			columnTyp string
			notNull   int
			defaultV  sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &columnTyp, &notNull, &defaultV, &pk); err != nil {
			return nil, err
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return columns, nil
}

func collectMigrationAffectedUserIDsTx(tx *sql.Tx, oldChatID, newChatID int64) (map[int64]struct{}, error) {
	ids := make(map[int64]struct{})
	ownerRows, err := tx.Query(`SELECT owner_user_id FROM group_config WHERE chat_id IN (?, ?) AND owner_user_id <> 0`, oldChatID, newChatID)
	if err != nil {
		return nil, err
	}
	defer ownerRows.Close()
	for ownerRows.Next() {
		var userID int64
		if err := ownerRows.Scan(&userID); err != nil {
			return nil, err
		}
		if userID != 0 {
			ids[userID] = struct{}{}
		}
	}
	if err := ownerRows.Err(); err != nil {
		return nil, err
	}
	operatorRows, err := tx.Query(`SELECT user_id FROM operators WHERE chat_id IN (?, ?) AND is_global = 0`, oldChatID, newChatID)
	if err != nil {
		return nil, err
	}
	defer operatorRows.Close()
	for operatorRows.Next() {
		var userID int64
		if err := operatorRows.Scan(&userID); err != nil {
			return nil, err
		}
		if userID != 0 {
			ids[userID] = struct{}{}
		}
	}
	if err := operatorRows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

func replaceForwardRuleTargetChatIDs(rawTargets, oldChatIDText, newChatIDText string) (string, bool) {
	parts := strings.Split(strings.TrimSpace(rawTargets), ",")
	if len(parts) == 0 {
		return strings.TrimSpace(rawTargets), false
	}
	changed := false
	seen := make(map[string]struct{}, len(parts))
	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		token := strings.TrimSpace(part)
		if token == "" {
			continue
		}
		if token == oldChatIDText {
			token = newChatIDText
			changed = true
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		normalized = append(normalized, token)
	}
	result := strings.Join(normalized, ",")
	return result, changed
}

func quoteSQLiteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(strings.TrimSpace(name), `"`, `""`) + `"`
}

func allowedGroupField(name string) bool {
	allow := map[string]struct{}{
		"title": {}, "owner_user_id": {}, "bookkeeping_enabled": {}, "bookkeeping_cycle_key": {}, "display_nickname": {}, "show_usdt_conversion": {},
		"fee_rate": {}, "payout_fee_rate": {}, "custom_rate": {}, "payout_rate": {}, "use_realtime_rate": {}, "rate_source": {},
		"day_cut_time": {}, "payout_address": {}, "payout_address_remark": {}, "exclusive_address": {}, "exclusive_address_owner": {}, "exclusive_address_remark": {}, "guarantee_rule": {}, "group_type": {}, "duplicate_alert_enabled": {}, "group_tags": {},
		"at_broadcast_enabled": {}, "auto_reply_enabled": {}, "custom_content_with_time": {}, "class_start_ad": {}, "class_end_ad": {},
		"welcome_enabled": {}, "welcome_text": {}, "ledger_card_hide_reversed": {},
	}
	_, ok := allow[name]
	return ok
}

func (d *Database) SetOwner(chatID, userID int64) error {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.SetOwner(chatID, userID)
	}
	return d.UpdateGroupFields(chatID, map[string]any{"owner_user_id": userID})
}

func (d *Database) IsOwner(chatID, userID int64) bool {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.IsOwner(chatID, userID)
	}
	access, ok := d.resolveUserAccess(chatID, userID)
	if !ok {
		return false
	}
	return access.IsOwner
}

func (d *Database) AddGlobalOwner(userID int64, username string) error {
	_, err := d.raw.Exec(`
INSERT INTO global_owners (user_id, username)
VALUES (?, ?)
ON CONFLICT(user_id) DO UPDATE SET username = excluded.username`,
		userID, username,
	)
	if err == nil {
		d.deleteCachedUserAccessByUserID(userID)
	}
	return err
}

func (d *Database) RemoveGlobalOwner(userID int64) error {
	_, err := d.raw.Exec(`DELETE FROM global_owners WHERE user_id = ?`, userID)
	if err == nil {
		d.deleteCachedUserAccessByUserID(userID)
	}
	return err
}

func (d *Database) ListGlobalOwners() ([]GlobalOwner, error) {
	rows, err := d.raw.Query(`SELECT user_id, username FROM global_owners ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GlobalOwner{}
	for rows.Next() {
		var item GlobalOwner
		if err := rows.Scan(&item.UserID, &item.Username); err == nil {
			out = append(out, item)
		}
	}
	return out, nil
}

func (d *Database) IsGlobalOwner(userID int64) bool {
	ok, err := d.isGlobalOwnerRaw(userID)
	if err != nil {
		return false
	}
	return ok
}

func (d *Database) AddOperator(chatID, userID int64, username string, isGlobal bool) error {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.AddOperator(chatID, userID, username, isGlobal)
	}
	scopeChat := chatID
	flag := 0
	if isGlobal {
		scopeChat = 0
		flag = 1
	}
	_, err := d.raw.Exec(`
INSERT INTO operators (chat_id, user_id, username, is_global)
VALUES (?, ?, ?, ?)
ON CONFLICT(chat_id, user_id, is_global) DO UPDATE SET username = excluded.username`,
		scopeChat, userID, username, flag,
	)
	if err == nil {
		if isGlobal {
			d.deleteCachedUserAccessByUserID(userID)
		} else {
			d.deleteCachedUserAccess(chatID, userID)
		}
	}
	return err
}

func (d *Database) RemoveOperator(chatID, userID int64, isGlobal bool) error {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.RemoveOperator(chatID, userID, isGlobal)
	}
	scopeChat := chatID
	flag := 0
	if isGlobal {
		scopeChat = 0
		flag = 1
	}
	_, err := d.raw.Exec(`DELETE FROM operators WHERE chat_id = ? AND user_id = ? AND is_global = ?`, scopeChat, userID, flag)
	if err == nil {
		if isGlobal {
			d.deleteCachedUserAccessByUserID(userID)
		} else {
			d.deleteCachedUserAccess(chatID, userID)
		}
	}
	return err
}

func (d *Database) ListOperators(chatID int64, isGlobal bool) ([]Operator, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.ListOperators(chatID, isGlobal)
	}
	scopeChat := chatID
	flag := 0
	if isGlobal {
		scopeChat = 0
		flag = 1
	}
	rows, err := d.raw.Query(`SELECT user_id, username, is_global FROM operators WHERE chat_id = ? AND is_global = ? ORDER BY created_at ASC`, scopeChat, flag)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Operator{}
	for rows.Next() {
		var item Operator
		if err := rows.Scan(&item.UserID, &item.Username, &item.IsGlobal); err == nil {
			out = append(out, item)
		}
	}
	return out, nil
}

func (d *Database) IsOperator(chatID, userID int64) bool {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.IsOperator(chatID, userID)
	}
	access, ok := d.resolveUserAccess(chatID, userID)
	if !ok {
		return false
	}
	return access.IsOperator
}

func (d *Database) IsPrivileged(chatID, userID int64) bool {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.IsPrivileged(chatID, userID)
	}
	access, ok := d.resolveUserAccess(chatID, userID)
	if !ok {
		return false
	}
	return access.PermissionLevel >= permissionLevelOperator
}

func (d *Database) ListPrivilegedGroups(userID int64) ([]GroupConfig, error) {
	if d == nil || userID == 0 {
		return nil, nil
	}
	globalOwner, err := d.isGlobalOwnerRaw(userID)
	if err != nil {
		return nil, err
	}
	globalOperator, err := d.isOperatorRaw(0, userID)
	if err != nil {
		return nil, err
	}
	if globalOwner || globalOperator {
		return d.ListAllGroups()
	}

	sqlText := `SELECT chat_id, title, owner_user_id, bookkeeping_enabled, bookkeeping_cycle_key, display_nickname, show_usdt_conversion,
	fee_rate, payout_fee_rate, custom_rate, payout_rate, use_realtime_rate, rate_source, day_cut_time,
	payout_address, payout_address_remark, exclusive_address, exclusive_address_owner, exclusive_address_remark, guarantee_rule, group_type, duplicate_alert_enabled, group_tags, at_broadcast_enabled,
		auto_reply_enabled, custom_content_with_time, class_start_ad, class_end_ad, welcome_enabled, welcome_text, ledger_card_hide_reversed
		FROM group_config g
		WHERE g.owner_user_id = ?
		   OR EXISTS (
			SELECT 1 FROM operators o
			WHERE o.chat_id = g.chat_id AND o.user_id = ? AND o.is_global = 0
		)
		ORDER BY updated_at DESC`
	if d.tenantRouter != nil {
		out := make([]GroupConfig, 0, 16)
		for _, shard := range d.tenantRouter.shards {
			rows, err := shard.listGroupConfigsBySQL(sqlText, userID, userID)
			if err != nil {
				return nil, err
			}
			out = append(out, rows...)
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].ChatID < out[j].ChatID })
		return out, nil
	}
	rows, err := d.listGroupConfigsBySQL(sqlText, userID, userID)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].ChatID < rows[j].ChatID })
	return rows, nil
}

func (d *Database) resolveUserAccess(chatID, userID int64) (UserAccessContext, bool) {
	if chatID == 0 || userID == 0 {
		return newMemberUserAccessContext(), false
	}
	if cached, ok := d.getCachedUserAccess(chatID, userID); ok {
		return cached, true
	}
	access, ok := d.loadUserAccess(chatID, userID)
	if !ok {
		return newMemberUserAccessContext(), false
	}
	d.setCachedUserAccess(chatID, userID, access)
	return access, true
}

func (d *Database) loadUserAccess(chatID, userID int64) (UserAccessContext, bool) {
	operator, err := d.isOperatorRaw(chatID, userID)
	if err != nil {
		return newMemberUserAccessContext(), false
	}
	globalOwner, err := d.isGlobalOwnerRaw(userID)
	if err != nil {
		return newMemberUserAccessContext(), false
	}

	group, err := d.GetGroup(chatID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return buildUserAccessContext(globalOwner, operator), true
		}
		return newMemberUserAccessContext(), false
	}
	return buildUserAccessContext(group.OwnerUserID == userID || globalOwner, operator), true
}

func (d *Database) isOperatorRaw(chatID, userID int64) (bool, error) {
	row := d.raw.QueryRow(`
SELECT id FROM operators
WHERE (chat_id = ? AND is_global = 0 AND user_id = ?)
   OR (chat_id = 0 AND is_global = 1 AND user_id = ?)
LIMIT 1`, chatID, userID, userID)
	var id int64
	err := row.Scan(&id)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, err
}

func (d *Database) isGlobalOwnerRaw(userID int64) (bool, error) {
	row := d.raw.QueryRow(`
SELECT id FROM global_owners
WHERE user_id = ?
LIMIT 1`, userID)
	var id int64
	err := row.Scan(&id)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, err
}

func (d *Database) getCachedGroupConfig(chatID int64) (GroupConfig, bool) {
	if d == nil || d.contextCache == nil || chatID == 0 {
		return GroupConfig{}, false
	}
	group, ok, err := d.contextCache.GetGroupConfig(chatID)
	if err != nil || !ok {
		return GroupConfig{}, false
	}
	if group.ChatID == 0 {
		group.ChatID = chatID
	}
	return group, true
}

func (d *Database) setCachedGroupConfig(group GroupConfig) {
	if d == nil || d.contextCache == nil || group.ChatID == 0 {
		return
	}
	_ = d.contextCache.SetGroupConfig(group)
}

func (d *Database) deleteCachedGroupConfig(chatID int64) {
	if d == nil || d.contextCache == nil || chatID == 0 {
		return
	}
	_ = d.contextCache.DeleteGroupConfig(chatID)
}

func (d *Database) getCachedUserAccess(chatID, userID int64) (UserAccessContext, bool) {
	if d == nil || d.contextCache == nil || chatID == 0 || userID == 0 {
		return newMemberUserAccessContext(), false
	}
	access, ok, err := d.contextCache.GetUserAccess(chatID, userID)
	if err != nil || !ok {
		return newMemberUserAccessContext(), false
	}
	return normalizeUserAccessContext(access), true
}

func (d *Database) setCachedUserAccess(chatID, userID int64, access UserAccessContext) {
	if d == nil || d.contextCache == nil || chatID == 0 || userID == 0 {
		return
	}
	_ = d.contextCache.SetUserAccess(chatID, userID, access)
}

func (d *Database) deleteCachedUserAccess(chatID, userID int64) {
	if d == nil || d.contextCache == nil || chatID == 0 || userID == 0 {
		return
	}
	_ = d.contextCache.DeleteUserAccess(chatID, userID)
}

func (d *Database) deleteCachedUserAccessByUserID(userID int64) {
	if d == nil || d.contextCache == nil || userID == 0 {
		return
	}
	_ = d.contextCache.DeleteUserAccessByUserID(userID)
}

func nowUTCText() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05")
}

func reserveMessageTx(tx *sql.Tx, chatID int64, messageID int, commandKey string) (bool, error) {
	key := strings.TrimSpace(commandKey)
	if key == "" {
		key = "default"
	}
	fingerprint := BuildRequestFingerprint(chatID, messageID)
	res, err := tx.Exec(`
INSERT OR IGNORE INTO message_idempotency (chat_id, message_id, command_key, request_fingerprint, created_at)
VALUES (?, ?, ?, ?, ?)`, chatID, messageID, key, fingerprint, nowUTCText())
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func ledgerBalanceDelta(entryType string, finalAmount float64) (float64, float64) {
	if strings.TrimSpace(entryType) == "deposit" {
		return finalAmount, 0
	}
	return 0, finalAmount
}

func normalizeLedgerCurrency(currency string) string {
	return strings.ToUpper(strings.TrimSpace(currency))
}

func ledgerAccountDelta(entryType string, finalAmount float64) float64 {
	switch strings.TrimSpace(entryType) {
	case "deposit":
		return finalAmount
	case "payout":
		return -finalAmount
	default:
		return finalAmount
	}
}

func isSelectForUpdateSyntaxError(err error) bool {
	if err == nil {
		return false
	}
	upper := strings.ToUpper(err.Error())
	return strings.Contains(upper, "FOR UPDATE") ||
		strings.Contains(upper, "NEAR \"FOR\"") ||
		strings.Contains(upper, "AT OR NEAR \"FOR\"")
}

type accountBalanceSnapshot struct {
	balance float64
	version int64
}

func getAccountBalanceSnapshotTx(tx *sql.Tx, userID int64, currency string) (accountBalanceSnapshot, bool, error) {
	var snapshot accountBalanceSnapshot
	err := tx.QueryRow(`SELECT balance, version FROM account_balances WHERE user_id = ? AND currency = ?`, userID, currency).
		Scan(&snapshot.balance, &snapshot.version)
	if err == nil {
		return snapshot, true, nil
	}
	if err == sql.ErrNoRows {
		return accountBalanceSnapshot{}, false, nil
	}
	return accountBalanceSnapshot{}, false, err
}

func queryAccountBalanceForUpdateTx(tx *sql.Tx, userID int64, currency string) (float64, bool, error) {
	var balance float64
	err := tx.QueryRow(accountBalanceLockSQL, userID, currency).Scan(&balance)
	if err == nil {
		return balance, true, nil
	}
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if !isSelectForUpdateSyntaxError(err) {
		return 0, false, err
	}

	err = tx.QueryRow(`SELECT balance FROM account_balances WHERE user_id = ? AND currency = ?`, userID, currency).Scan(&balance)
	if err == nil {
		return balance, true, nil
	}
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	return 0, false, err
}

func ensureAccountBalanceRowTx(tx *sql.Tx, userID int64, currency string) error {
	_, err := tx.Exec(`
INSERT INTO account_balances (user_id, currency, balance, version, updated_at)
VALUES (?, ?, 0, 0, ?)
ON CONFLICT(user_id, currency) DO NOTHING`,
		userID,
		currency,
		nowUTCText(),
	)
	return err
}

func updateAccountBalanceCASTx(tx *sql.Tx, userID int64, currency string, newBalance float64, expectedVersion int64) (bool, error) {
	res, err := tx.Exec(`
UPDATE account_balances
SET balance = ?, version = version + 1, updated_at = ?
WHERE user_id = ? AND currency = ? AND version = ?`,
		newBalance,
		nowUTCText(),
		userID,
		currency,
		expectedVersion,
	)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

func addAccountBalanceDeltaWithCASTx(tx *sql.Tx, userID int64, currency string, delta float64, maxAttempts int) (bool, error) {
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	if err := ensureAccountBalanceRowTx(tx, userID, currency); err != nil {
		return false, err
	}
	for attempt := 0; attempt < maxAttempts; attempt++ {
		snapshot, found, err := getAccountBalanceSnapshotTx(tx, userID, currency)
		if err != nil {
			return false, err
		}
		if !found {
			if err := ensureAccountBalanceRowTx(tx, userID, currency); err != nil {
				return false, err
			}
			continue
		}
		swapped, err := updateAccountBalanceCASTx(tx, userID, currency, snapshot.balance+delta, snapshot.version)
		if err != nil {
			return false, err
		}
		if swapped {
			return true, nil
		}
	}
	return false, nil
}

func lockAccountBalanceTx(tx *sql.Tx, userID int64, currency string) (float64, error) {
	balance, found, err := queryAccountBalanceForUpdateTx(tx, userID, currency)
	if err != nil {
		return 0, err
	}
	if found {
		return balance, nil
	}

	if err := ensureAccountBalanceRowTx(tx, userID, currency); err != nil {
		return 0, err
	}

	balance, found, err = queryAccountBalanceForUpdateTx(tx, userID, currency)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, fmt.Errorf("账户余额锁定失败: user_id=%d currency=%s", userID, currency)
	}
	return balance, nil
}

func addAccountBalanceDeltaValueTx(tx *sql.Tx, userID int64, currency string, delta float64) error {
	normalizedCurrency := normalizeLedgerCurrency(currency)

	updated, err := addAccountBalanceDeltaWithCASTx(tx, userID, normalizedCurrency, delta, defaultAccountBalanceCASMax)
	if err != nil {
		return err
	}
	if updated {
		return nil
	}

	lockedBalance, err := lockAccountBalanceTx(tx, userID, normalizedCurrency)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
UPDATE account_balances
SET balance = ?, version = version + 1, updated_at = ?
WHERE user_id = ? AND currency = ?`,
		lockedBalance+delta,
		nowUTCText(),
		userID,
		normalizedCurrency,
	)
	return err
}

func addLedgerBalanceDeltaTx(tx *sql.Tx, chatID int64, depositDelta, payoutDelta float64) error {
	_, err := tx.Exec(`
INSERT INTO ledger_balances (chat_id, deposit_total, payout_total, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(chat_id) DO UPDATE SET
	deposit_total = ledger_balances.deposit_total + excluded.deposit_total,
	payout_total = ledger_balances.payout_total + excluded.payout_total,
	updated_at = excluded.updated_at`,
		chatID, depositDelta, payoutDelta, nowUTCText(),
	)
	return err
}

func addAuditLogTx(tx *sql.Tx, chatID int64, messageID int, operatorUserID int64, action, detail string) error {
	_, err := tx.Exec(`
INSERT INTO audit_logs (chat_id, message_id, operator_user_id, action, detail)
VALUES (?, ?, ?, ?, ?)`,
		chatID,
		messageID,
		operatorUserID,
		strings.TrimSpace(action),
		strings.TrimSpace(detail),
	)
	return err
}

func normalizeRiskSeverity(severity string) string {
	level := strings.ToLower(strings.TrimSpace(severity))
	if level == "" {
		level = "info"
	}
	return level
}

func addRiskEventTx(tx *sql.Tx, chatID int64, messageID int, userID int64, eventType, severity, detail string) error {
	_, err := tx.Exec(`
INSERT INTO risk_events (chat_id, message_id, user_id, event_type, severity, detail)
VALUES (?, ?, ?, ?, ?, ?)`,
		chatID,
		messageID,
		userID,
		strings.TrimSpace(eventType),
		normalizeRiskSeverity(severity),
		strings.TrimSpace(detail),
	)
	return err
}

func (d *Database) AddAuditLog(chatID int64, messageID int, operatorUserID int64, action, detail string) error {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.AddAuditLog(chatID, messageID, operatorUserID, action, detail)
	}
	_, err := d.raw.Exec(`
INSERT INTO audit_logs (chat_id, message_id, operator_user_id, action, detail)
VALUES (?, ?, ?, ?, ?)`,
		chatID,
		messageID,
		operatorUserID,
		strings.TrimSpace(action),
		strings.TrimSpace(detail),
	)
	return err
}

func (d *Database) AddRiskEvent(chatID int64, messageID int, userID int64, eventType, severity, detail string) error {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.AddRiskEvent(chatID, messageID, userID, eventType, severity, detail)
	}
	_, err := d.raw.Exec(`
INSERT INTO risk_events (chat_id, message_id, user_id, event_type, severity, detail)
VALUES (?, ?, ?, ?, ?, ?)`,
		chatID,
		messageID,
		userID,
		strings.TrimSpace(eventType),
		normalizeRiskSeverity(severity),
		strings.TrimSpace(detail),
	)
	return err
}

func (d *Database) AddOutboundDeadLetter(item OutboundDeadLetterInput) (int64, error) {
	if routed := d.databaseForChat(item.ChatID); routed != d {
		return routed.AddOutboundDeadLetter(item)
	}
	operation := strings.TrimSpace(item.Operation)
	if operation == "" {
		operation = "send"
	}
	messageType := strings.TrimSpace(item.MessageType)
	if messageType == "" {
		messageType = "unknown"
	}
	errorMessage := strings.TrimSpace(item.ErrorMessage)
	if errorMessage == "" {
		errorMessage = "unknown outbound send error"
	}

	res, err := d.raw.Exec(`
INSERT INTO outbound_dead_letters (chat_id, priority, operation, message_type, payload, error_message)
VALUES (?, ?, ?, ?, ?, ?)`,
		item.ChatID,
		item.Priority,
		limitDeadLetterText(operation, 32),
		limitDeadLetterText(messageType, 128),
		limitDeadLetterText(item.Payload, 2048),
		limitDeadLetterText(errorMessage, 512),
	)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	if deadLetterRetentionSize > 0 {
		_, _ = d.raw.Exec(
			`DELETE FROM outbound_dead_letters WHERE id NOT IN (SELECT id FROM outbound_dead_letters ORDER BY id DESC LIMIT ?)`,
			deadLetterRetentionSize,
		)
	}
	return id, nil
}

func (d *Database) ListOutboundDeadLetters(limit int) ([]OutboundDeadLetter, error) {
	limit = normalizeDeadLetterListLimit(limit)
	if d.tenantRouter != nil {
		out := make([]OutboundDeadLetter, 0, limit)
		for _, shard := range d.tenantRouter.shards {
			if shard == nil {
				continue
			}
			rows, err := shard.listOutboundDeadLettersLocal(limit)
			if err != nil {
				return nil, err
			}
			out = append(out, rows...)
		}
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].CreatedAt == out[j].CreatedAt {
				return out[i].ID > out[j].ID
			}
			return out[i].CreatedAt > out[j].CreatedAt
		})
		if len(out) > limit {
			out = out[:limit]
		}
		return out, nil
	}
	return d.listOutboundDeadLettersLocal(limit)
}

func (d *Database) listOutboundDeadLettersLocal(limit int) ([]OutboundDeadLetter, error) {
	rows, err := d.raw.Query(`
SELECT id, chat_id, priority, operation, message_type, payload, error_message, created_at
FROM outbound_dead_letters
ORDER BY id DESC
LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]OutboundDeadLetter, 0, limit)
	for rows.Next() {
		var item OutboundDeadLetter
		if err := rows.Scan(
			&item.ID,
			&item.ChatID,
			&item.Priority,
			&item.Operation,
			&item.MessageType,
			&item.Payload,
			&item.ErrorMessage,
			&item.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func normalizeDeadLetterListLimit(limit int) int {
	if limit <= 0 {
		return defaultDeadLetterListLimit
	}
	if limit > maxDeadLetterListLimit {
		return maxDeadLetterListLimit
	}
	return limit
}

func normalizeOrderSearchLimit(limit int) int {
	if limit <= 0 {
		return defaultOrderSearchLimit
	}
	if limit > maxOrderSearchLimit {
		return maxOrderSearchLimit
	}
	return limit
}

func limitDeadLetterText(raw string, maxRunes int) string {
	trimmed := strings.TrimSpace(raw)
	if maxRunes <= 0 {
		return trimmed
	}
	runes := []rune(trimmed)
	if len(runes) <= maxRunes {
		return trimmed
	}
	return string(runes[:maxRunes])
}

func addLedgerEntryTx(tx *sql.Tx, item LedgerEntryInput) (int64, error) {
	res, err := tx.Exec(`
INSERT INTO ledger_entries (
	chat_id, message_id, user_id, username, nickname, entry_type, amount, currency,
	base_amount, fee_rate, rate, final_amount, note, card_no, track_message_link, reversal_of
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ChatID, item.MessageID, item.UserID, item.Username, item.Nickname, item.EntryType, item.Amount, item.Currency,
		item.BaseAmount, item.FeeRate, item.Rate, item.FinalAmount, item.Note, item.CardNo, item.TrackMessageLink, item.ReversalOf,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (d *Database) addLedgerEntryWithBalanceTx(tx *sql.Tx, item LedgerEntryInput) (int64, float64, error) {
	// Keep Transaction(ledger_entries) + Balance(ledger_balances) mutations in one SQL transaction.
	encryptedItem, err := d.encryptLedgerSensitiveFields(item)
	if err != nil {
		return 0, 0, err
	}
	entryID, err := addLedgerEntryTx(tx, encryptedItem)
	if err != nil {
		return 0, 0, err
	}
	batchedDelta := ledgerAccountDelta(item.EntryType, item.FinalAmount)
	if d.shouldBatchHotAccount(item.UserID, item.Currency) {
		// Defer hot-account balance snapshots into in-memory batching to reduce write amplification.
	} else {
		if err := addAccountBalanceDeltaValueTx(tx, item.UserID, item.Currency, batchedDelta); err != nil {
			return 0, 0, err
		}
		batchedDelta = 0
	}
	depositDelta, payoutDelta := ledgerBalanceDelta(item.EntryType, item.FinalAmount)
	if err := addLedgerBalanceDeltaTx(tx, item.ChatID, depositDelta, payoutDelta); err != nil {
		return 0, 0, err
	}
	return entryID, batchedDelta, nil
}

func (d *Database) shouldBatchHotAccount(userID int64, currency string) bool {
	return d.hotAccountBatch != nil && d.hotAccountBatch.shouldBatch(userID, currency)
}

func (d *Database) enqueueHotAccountDelta(userID int64, currency string, delta float64) {
	if d.hotAccountBatch == nil || delta == 0 {
		return
	}
	shouldFlush := d.hotAccountBatch.addDelta(userID, currency, delta)
	if !shouldFlush {
		return
	}
	_ = d.flushHotAccountPending()
}

func (d *Database) flushHotAccountPending() error {
	if d.hotAccountBatch == nil {
		return nil
	}
	pending := d.hotAccountBatch.drainAll()
	if len(pending) == 0 {
		return nil
	}
	err := d.withWriteTx(func(tx *sql.Tx) error {
		for key, delta := range pending {
			if err := addAccountBalanceDeltaValueTx(tx, key.userID, key.currency, delta); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		d.hotAccountBatch.mergeBack(pending)
		return err
	}
	return nil
}

func updateCardHistoryTx(tx *sql.Tx, chatID int64, cardNo string, entryID int64) (int, error) {
	card := strings.TrimSpace(cardNo)
	if card == "" {
		return 0, nil
	}
	_, err := tx.Exec(`
INSERT INTO card_history (chat_id, card_no, seen_count, last_entry_id, last_seen_at)
VALUES (?, ?, 1, ?, ?)
ON CONFLICT(chat_id, card_no)
DO UPDATE SET
	seen_count = card_history.seen_count + 1,
	last_entry_id = excluded.last_entry_id,
	last_seen_at = excluded.last_seen_at`,
		chatID,
		card,
		entryID,
		nowUTCText(),
	)
	if err != nil {
		return 0, err
	}
	row := tx.QueryRow(`SELECT seen_count FROM card_history WHERE chat_id = ? AND card_no = ?`, chatID, card)
	var seen int
	if err := row.Scan(&seen); err != nil {
		return 0, err
	}
	return seen, nil
}

func buildDuplicateAlertAmountFingerprint(amount float64, currency string) string {
	normalizedCurrency := strings.ToUpper(strings.TrimSpace(currency))
	if normalizedCurrency == "" {
		normalizedCurrency = "UNKNOWN"
	}
	return fmt.Sprintf("%s|%.8f", normalizedCurrency, amount)
}

func normalizeDuplicateAlertAddress(address string) string {
	addr := strings.TrimSpace(address)
	if addr == "" {
		return ""
	}
	switch {
	case duplicateAlertERC20Pattern.MatchString(addr):
		return strings.ToLower(addr)
	case duplicateAlertTRC20Pattern.MatchString(addr):
		return addr
	default:
		return ""
	}
}

func extractDuplicateAlertAddresses(note string) []string {
	text := strings.TrimSpace(note)
	if text == "" {
		return nil
	}
	seen := make(map[string]struct{})
	addresses := make([]string, 0, 2)
	collect := func(raw string) {
		normalized := normalizeDuplicateAlertAddress(raw)
		if normalized == "" {
			return
		}
		if _, exists := seen[normalized]; exists {
			return
		}
		seen[normalized] = struct{}{}
		addresses = append(addresses, normalized)
	}
	for _, address := range duplicateAlertERC20Finder.FindAllString(text, -1) {
		collect(address)
	}
	for _, address := range duplicateAlertTRC20Finder.FindAllString(text, -1) {
		collect(address)
	}
	return addresses
}

func insertDuplicateAlertFingerprintTx(tx *sql.Tx, chatID int64, entryID int64, dimension, fingerprint, windowExpr string) (int, error) {
	normalizedDimension := strings.TrimSpace(dimension)
	normalizedFingerprint := strings.TrimSpace(fingerprint)
	if normalizedDimension == "" || normalizedFingerprint == "" {
		return 0, nil
	}
	if _, err := tx.Exec(`
INSERT INTO duplicate_alert_history (chat_id, entry_id, dimension, fingerprint)
VALUES (?, ?, ?, ?)`,
		chatID,
		entryID,
		normalizedDimension,
		normalizedFingerprint,
	); err != nil {
		return 0, err
	}

	row := tx.QueryRow(`
SELECT COUNT(1)
FROM duplicate_alert_history
WHERE chat_id = ?
  AND dimension = ?
  AND fingerprint = ?
  AND datetime(created_at) >= datetime('now', ?)`,
		chatID,
		normalizedDimension,
		normalizedFingerprint,
		windowExpr,
	)
	var seenCount int
	if err := row.Scan(&seenCount); err != nil {
		return 0, err
	}
	return seenCount, nil
}

func loadDuplicateAlertScopeTx(tx *sql.Tx, chatID int64) (string, bool, error) {
	row := tx.QueryRow(`SELECT group_type, duplicate_alert_enabled FROM group_config WHERE chat_id = ?`, chatID)
	var (
		groupType string
		enabled   int
	)
	if err := row.Scan(&groupType, &enabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return strings.TrimSpace(groupType), enabled == 1, nil
}

func countDuplicateFingerprintByGroupTypeTx(tx *sql.Tx, dimension, fingerprint, groupType, windowExpr string) (int, error) {
	normalizedDimension := strings.TrimSpace(dimension)
	normalizedFingerprint := strings.TrimSpace(fingerprint)
	normalizedType := strings.TrimSpace(groupType)
	if normalizedDimension == "" || normalizedType == "" || normalizedFingerprint == "" {
		return 0, nil
	}
	row := tx.QueryRow(`
SELECT COUNT(1)
FROM duplicate_alert_history dah
JOIN group_config gc ON gc.chat_id = dah.chat_id
WHERE dah.dimension = ?
  AND dah.fingerprint = ?
  AND datetime(dah.created_at) >= datetime('now', ?)
  AND gc.group_type = ?
  AND gc.duplicate_alert_enabled = 1`,
		normalizedDimension,
		normalizedFingerprint,
		windowExpr,
		normalizedType,
	)
	var seenCount int
	if err := row.Scan(&seenCount); err != nil {
		return 0, err
	}
	return seenCount, nil
}

func countDuplicateCardByGroupTypeTx(tx *sql.Tx, groupType, cardNo, windowExpr string) (int, error) {
	return countDuplicateFingerprintByGroupTypeTx(tx, duplicateAlertDimensionCard, cardNo, groupType, windowExpr)
}

func updateDuplicateAlertHistoryTx(tx *sql.Tx, item LedgerEntryInput, entryID int64, window time.Duration) (int, int, int, string, error) {
	windowSeconds := int(window / time.Second)
	if windowSeconds <= 0 {
		windowSeconds = int((10 * time.Minute) / time.Second)
	}
	windowExpr := fmt.Sprintf("-%d seconds", windowSeconds)
	groupType, duplicateAlertEnabled, err := loadDuplicateAlertScopeTx(tx, item.ChatID)
	if err != nil {
		return 0, 0, 0, "", err
	}

	amountCount, err := insertDuplicateAlertFingerprintTx(
		tx,
		item.ChatID,
		entryID,
		duplicateAlertDimensionAmount,
		buildDuplicateAlertAmountFingerprint(item.Amount, item.Currency),
		windowExpr,
	)
	if err != nil {
		return 0, 0, 0, "", err
	}
	cardCount, err := insertDuplicateAlertFingerprintTx(
		tx,
		item.ChatID,
		entryID,
		duplicateAlertDimensionCard,
		strings.TrimSpace(item.CardNo),
		windowExpr,
	)
	if err != nil {
		return 0, 0, 0, "", err
	}
	if duplicateAlertEnabled && groupType != "" {
		cardCount, err = countDuplicateCardByGroupTypeTx(tx, groupType, item.CardNo, windowExpr)
		if err != nil {
			return 0, 0, 0, "", err
		}
	}

	addressCount := 0
	addressMatched := ""
	for _, address := range extractDuplicateAlertAddresses(item.Note) {
		currentCount, currentErr := insertDuplicateAlertFingerprintTx(
			tx,
			item.ChatID,
			entryID,
			duplicateAlertDimensionAddress,
			address,
			windowExpr,
		)
		if currentErr != nil {
			return 0, 0, 0, "", currentErr
		}
		if currentCount > addressCount {
			addressCount = currentCount
			addressMatched = address
		}
	}

	return amountCount, cardCount, addressCount, addressMatched, nil
}

func (d *Database) RecordTextDuplicateAccount(chatID int64, messageID int, accountNo string) (int, string, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.RecordTextDuplicateAccount(chatID, messageID, accountNo)
	}
	normalizedAccount := strings.TrimSpace(accountNo)
	if normalizedAccount == "" {
		return 0, "", nil
	}
	windowSeconds := int(duplicateAlertWindow / time.Second)
	if windowSeconds <= 0 {
		windowSeconds = int((10 * time.Minute) / time.Second)
	}
	windowExpr := fmt.Sprintf("-%d seconds", windowSeconds)
	seenCount := 0
	scopeGroupType := ""
	err := d.withWriteTx(func(tx *sql.Tx) error {
		groupType, duplicateAlertEnabled, err := loadDuplicateAlertScopeTx(tx, chatID)
		if err != nil {
			return err
		}
		seenCount, err = insertDuplicateAlertFingerprintTx(
			tx,
			chatID,
			int64(messageID),
			duplicateAlertDimensionAccount,
			normalizedAccount,
			windowExpr,
		)
		if err != nil {
			return err
		}
		if duplicateAlertEnabled && groupType != "" {
			seenCount, err = countDuplicateFingerprintByGroupTypeTx(
				tx,
				duplicateAlertDimensionAccount,
				normalizedAccount,
				groupType,
				windowExpr,
			)
			if err != nil {
				return err
			}
			scopeGroupType = groupType
		}
		return nil
	})
	if err != nil {
		return 0, "", err
	}
	return seenCount, scopeGroupType, nil
}

func waitForRetryWithContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isRetryableDeadlockError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	text := strings.ToLower(err.Error())
	patterns := []string{
		"deadlock",
		"lock wait timeout",
		"database is locked",
		"database table is locked",
		"database schema is locked",
		"sqlstate 40001",
		"serialization failure",
	}
	for _, pattern := range patterns {
		if strings.Contains(text, pattern) {
			return true
		}
	}
	return false
}

func isLedgerMessageUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	upper := strings.ToUpper(err.Error())
	return strings.Contains(upper, "UNIQUE CONSTRAINT FAILED") &&
		strings.Contains(upper, "LEDGER_ENTRIES.CHAT_ID") &&
		strings.Contains(upper, "LEDGER_ENTRIES.MESSAGE_ID")
}

func (d *Database) deadlockBackoff(retryIndex int) time.Duration {
	delay := d.deadlockBackoffBase
	if retryIndex > 0 {
		delay *= time.Duration(1 << retryIndex)
	}
	jitter := d.deadlockBackoffJitter
	if jitter <= 0 || d.randIntn == nil {
		return delay
	}
	jitterMS := int(jitter / time.Millisecond)
	if jitterMS <= 0 {
		return delay
	}
	return delay + time.Duration(d.randIntn(jitterMS+1))*time.Millisecond
}

func (d *Database) withWriteTx(fn func(tx *sql.Tx) error) error {
	return d.withWriteTxContext(context.Background(), fn)
}

func (d *Database) withWriteTxContext(ctx context.Context, fn func(tx *sql.Tx) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	attempts := d.deadlockRetryMax
	if attempts <= 0 {
		attempts = 1
	}
	for attempt := 0; attempt < attempts; attempt++ {
		err := d.withWriteTxAttempt(ctx, fn)
		if err == nil {
			return nil
		}
		if !isRetryableDeadlockError(err) || attempt == attempts-1 {
			return err
		}
		backoff := d.deadlockBackoff(attempt)
		waiter := d.retryWait
		if waiter == nil {
			waiter = waitForRetryWithContext
		}
		if waitErr := waiter(ctx, backoff); waitErr != nil {
			return waitErr
		}
	}
	return nil
}

func (d *Database) withWriteTxAttempt(ctx context.Context, fn func(tx *sql.Tx) error) error {
	// Serialize write transactions so ledger entry + balance changes always share one commit boundary.
	d.mu.Lock()
	defer d.mu.Unlock()

	tx, err := d.raw.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if recovered := recover(); recovered != nil {
			_ = tx.Rollback()
			panic(recovered)
		}
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (d *Database) withLedgerAtomicTx(fn func(tx *sql.Tx) error) error {
	// ACID orchestration entrypoint for Transaction/Balance commit boundaries.
	return d.withWriteTx(func(tx *sql.Tx) error {
		return fn(tx)
	})
}

func ensureTrialBalanceTx(tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT id, entry_type, final_amount FROM ledger_entries`)
	if err != nil {
		return err
	}
	defer rows.Close()

	sumDebit := 0.0
	sumCredit := 0.0
	for rows.Next() {
		var (
			entryID     int64
			entryType   string
			finalAmount float64
		)
		if err := rows.Scan(&entryID, &entryType, &finalAmount); err != nil {
			return err
		}

		entry, err := accounting.BuildJournalEntry(entryType, finalAmount)
		if err != nil {
			return fmt.Errorf(
				"%s: entry_id=%d entry_type=%q final_amount=%.8f: %w",
				trialBalanceCriticalAlert,
				entryID,
				strings.TrimSpace(entryType),
				finalAmount,
				err,
			)
		}
		for _, line := range entry.Lines {
			switch line.Direction {
			case accounting.JournalLineDirectionDebit:
				sumDebit += line.Amount
			case accounting.JournalLineDirectionCredit:
				sumCredit += line.Amount
			default:
				return fmt.Errorf(
					"%s: entry_id=%d has unsupported line direction %q",
					trialBalanceCriticalAlert,
					entryID,
					line.Direction,
				)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if sumDebit != sumCredit {
		return fmt.Errorf(
			"%s: sum(debit)=%.8f sum(credit)=%.8f",
			trialBalanceCriticalAlert,
			sumDebit,
			sumCredit,
		)
	}
	return nil
}

func validateLedgerEntryAccounting(item LedgerEntryInput) error {
	entry, err := accounting.BuildJournalEntry(item.EntryType, item.FinalAmount)
	if err != nil {
		return fmt.Errorf(
			"%s: entry_type=%q final_amount=%.8f: %w",
			trialBalanceCriticalAlert,
			strings.TrimSpace(item.EntryType),
			item.FinalAmount,
			err,
		)
	}
	sumDebit := 0.0
	sumCredit := 0.0
	for _, line := range entry.Lines {
		switch line.Direction {
		case accounting.JournalLineDirectionDebit:
			sumDebit += line.Amount
		case accounting.JournalLineDirectionCredit:
			sumCredit += line.Amount
		default:
			return fmt.Errorf(
				"%s: entry_type=%q has unsupported line direction %q",
				trialBalanceCriticalAlert,
				strings.TrimSpace(item.EntryType),
				line.Direction,
			)
		}
	}
	if sumDebit != sumCredit {
		return fmt.Errorf(
			"%s: entry_type=%q sum(debit)=%.8f sum(credit)=%.8f",
			trialBalanceCriticalAlert,
			strings.TrimSpace(item.EntryType),
			sumDebit,
			sumCredit,
		)
	}
	return nil
}

func (d *Database) ApplyLedgerEntry(item LedgerEntryInput) (LedgerApplyResult, error) {
	return d.applyLedgerEntry(item, nil)
}

func (d *Database) ApplyLedgerEntryWithReceiptOutbox(item LedgerEntryInput, buildOutbox func(result LedgerApplyResult) (*LedgerReceiptOutboxInput, error)) (LedgerApplyResult, error) {
	return d.applyLedgerEntry(item, buildOutbox)
}

func (d *Database) applyLedgerEntry(item LedgerEntryInput, buildOutbox func(result LedgerApplyResult) (*LedgerReceiptOutboxInput, error)) (LedgerApplyResult, error) {
	if routed := d.databaseForChat(item.ChatID); routed != d {
		if buildOutbox == nil {
			return routed.ApplyLedgerEntry(item)
		}
		return routed.ApplyLedgerEntryWithReceiptOutbox(item, buildOutbox)
	}
	result := LedgerApplyResult{}
	var batchedDelta float64
	err := d.withLedgerAtomicTx(func(tx *sql.Tx) error {
		reserved, err := reserveMessageTx(tx, item.ChatID, item.MessageID, "ledger")
		if err != nil {
			return err
		}
		if !reserved {
			result.Duplicate = true
			return nil
		}
		if err := validateLedgerEntryAccounting(item); err != nil {
			return err
		}

		entryID, pendingDelta, err := d.addLedgerEntryWithBalanceTx(tx, item)
		if err != nil {
			if isLedgerMessageUniqueConstraintError(err) {
				result.Duplicate = true
				return nil
			}
			return err
		}
		batchedDelta = pendingDelta

		seenCount, err := updateCardHistoryTx(tx, item.ChatID, item.CardNo, entryID)
		if err != nil {
			return err
		}
		recentAmountCount, recentCardCount, recentAddressCount, recentAddress, err := updateDuplicateAlertHistoryTx(tx, item, entryID, duplicateAlertWindow)
		if err != nil {
			return err
		}

		detail := fmt.Sprintf("entry_id=%d entry_type=%s final_amount=%.8f", entryID, strings.TrimSpace(item.EntryType), item.FinalAmount)
		if err := addAuditLogTx(tx, item.ChatID, item.MessageID, item.UserID, "ledger_applied", detail); err != nil {
			return err
		}

		result.EntryID = entryID
		result.CardSeenCount = seenCount
		result.RecentAmountSeenCount = recentAmountCount
		result.RecentCardSeenCount = recentCardCount
		result.RecentAddressSeenCount = recentAddressCount
		result.RecentAddress = recentAddress
		if buildOutbox != nil {
			outboxItem, buildErr := buildOutbox(result)
			if buildErr != nil {
				return buildErr
			}
			if outboxItem != nil {
				if outboxItem.ChatID == 0 {
					outboxItem.ChatID = item.ChatID
				}
				if outboxItem.SourceMessageID == 0 {
					outboxItem.SourceMessageID = item.MessageID
				}
				if outboxItem.EntryID == 0 {
					outboxItem.EntryID = entryID
				}
				if outboxItem.UserID == 0 {
					outboxItem.UserID = item.UserID
				}
				if _, insertErr := insertLedgerReceiptOutboxTx(tx, *outboxItem); insertErr != nil {
					return insertErr
				}
			}
		}
		return nil
	})
	if err != nil {
		return LedgerApplyResult{}, err
	}
	d.enqueueHotAccountDelta(item.UserID, item.Currency, batchedDelta)
	if result.EntryID > 0 {
		d.indexOrderQueryBloomEntry(item)
	}
	d.syncLedgerEntryToSearchByID(result.EntryID)
	return result, nil
}

func (d *Database) syncLedgerEntryToSearchByID(entryID int64) {
	if d == nil || d.orderSearch == nil || entryID <= 0 {
		return
	}
	entry, found, err := d.getLedgerByIDFromDB(d.raw, entryID)
	if err != nil || !found {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = d.orderSearch.IndexLedgerEntry(ctx, entry)
}

func (d *Database) getLedgerByIDFromDB(raw *sql.DB, entryID int64) (LedgerEntry, bool, error) {
	if raw == nil || entryID <= 0 {
		return LedgerEntry{}, false, nil
	}
	row := raw.QueryRow(`
SELECT id, chat_id, message_id, user_id, username, nickname, entry_type, amount, currency,
	base_amount, fee_rate, rate, final_amount, note, card_no, track_message_link, created_at, reversed, reversal_of
FROM ledger_entries
WHERE id = ?`, entryID)
	var item LedgerEntry
	var reversal sql.NullInt64
	err := row.Scan(
		&item.ID, &item.ChatID, &item.MessageID, &item.UserID, &item.Username, &item.Nickname, &item.EntryType, &item.Amount, &item.Currency,
		&item.BaseAmount, &item.FeeRate, &item.Rate, &item.FinalAmount, &item.Note, &item.CardNo, &item.TrackLink, &item.CreatedAt, &item.Reversed, &reversal,
	)
	if err == sql.ErrNoRows {
		return LedgerEntry{}, false, nil
	}
	if err != nil {
		return LedgerEntry{}, false, err
	}
	if reversal.Valid {
		item.ReversalOf = &reversal.Int64
	}
	if err := d.decryptLedgerSensitiveFields(&item); err != nil {
		return LedgerEntry{}, false, err
	}
	return item, true, nil
}

func (d *Database) getLedgerByIDTx(tx *sql.Tx, entryID int64) (LedgerEntry, error) {
	row := tx.QueryRow(`
SELECT id, chat_id, message_id, user_id, username, nickname, entry_type, amount, currency,
	base_amount, fee_rate, rate, final_amount, note, card_no, track_message_link, created_at, reversed, reversal_of
FROM ledger_entries
WHERE id = ?`, entryID)
	var item LedgerEntry
	var reversal sql.NullInt64
	if err := row.Scan(
		&item.ID, &item.ChatID, &item.MessageID, &item.UserID, &item.Username, &item.Nickname, &item.EntryType, &item.Amount, &item.Currency,
		&item.BaseAmount, &item.FeeRate, &item.Rate, &item.FinalAmount, &item.Note, &item.CardNo, &item.TrackLink, &item.CreatedAt, &item.Reversed, &reversal,
	); err != nil {
		return LedgerEntry{}, err
	}
	if reversal.Valid {
		item.ReversalOf = &reversal.Int64
	}
	if err := d.decryptLedgerSensitiveFields(&item); err != nil {
		return LedgerEntry{}, err
	}
	return item, nil
}

func buildLedgerReversalReason(targetType string) string {
	reason := "manual_reversal"
	normalizedTargetType := strings.ToLower(strings.TrimSpace(targetType))
	if normalizedTargetType == "" {
		return reason
	}
	return reason + ":" + normalizedTargetType
}

func marshalAuditSnapshot(payload any) (string, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func buildLedgerReversalAuditDetail(origin LedgerEntry, reversal LedgerEntry, actorUserID int64, targetType string) (string, error) {
	operatedAt := nowUTCText()
	reason := buildLedgerReversalReason(targetType)

	beforeSnapshot, err := marshalAuditSnapshot(origin)
	if err != nil {
		return "", err
	}

	originAfter := origin
	originAfter.Reversed = 1
	originAfterReversalID := reversal.ID
	originAfter.ReversalOf = &originAfterReversalID

	afterSnapshotPayload := struct {
		Origin   LedgerEntry `json:"origin"`
		Reversal LedgerEntry `json:"reversal"`
	}{
		Origin:   originAfter,
		Reversal: reversal,
	}
	afterSnapshot, err := marshalAuditSnapshot(afterSnapshotPayload)
	if err != nil {
		return "", err
	}

	detail := fmt.Sprintf(
		"origin_id=%d reversal_id=%d entry_type=%s reason=%s operator_user_id=%d operated_at=%s before_snapshot=%s after_snapshot=%s",
		origin.ID,
		reversal.ID,
		strings.TrimSpace(origin.EntryType),
		reason,
		actorUserID,
		operatedAt,
		beforeSnapshot,
		afterSnapshot,
	)
	return detail, nil
}

func reversalAuditStatsTx(tx *sql.Tx, chatID, operatorUserID int64) (int64, int64, float64, error) {
	row := tx.QueryRow(`
SELECT
	COALESCE(SUM(CASE WHEN action = 'ledger_reversal' THEN 1 ELSE 0 END), 0),
	COALESCE(COUNT(1), 0)
FROM audit_logs
WHERE chat_id = ? AND operator_user_id = ? AND action IN ('ledger_applied', 'ledger_reversal')`,
		chatID,
		operatorUserID,
	)
	var reversalCount int64
	var totalCount int64
	if err := row.Scan(&reversalCount, &totalCount); err != nil {
		return 0, 0, 0, err
	}
	rate := 0.0
	if totalCount > 0 {
		rate = float64(reversalCount) / float64(totalCount)
	}
	return reversalCount, totalCount, rate, nil
}

func shouldMarkReversalAuditRisk(reversalCount, totalCount int64, reversalRate float64) bool {
	if reversalCount <= 0 || totalCount < reversalAuditMinActionCount {
		return false
	}
	return reversalRate >= reversalAuditRateThreshold
}

func (d *Database) ReverseLastLedger(chatID int64, messageID int, actorUserID int64, actorUsername, actorNickname, targetType, trackLink string) (LedgerReverseResult, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.ReverseLastLedger(chatID, messageID, actorUserID, actorUsername, actorNickname, targetType, trackLink)
	}
	return d.reverseLastLedger(chatID, messageID, actorUserID, actorUsername, actorNickname, targetType, trackLink, "")
}

func (d *Database) ReverseLastLedgerInPeriod(chatID int64, messageID int, actorUserID int64, actorUsername, actorNickname, targetType, trackLink, periodStartAt string) (LedgerReverseResult, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.ReverseLastLedgerInPeriod(chatID, messageID, actorUserID, actorUsername, actorNickname, targetType, trackLink, periodStartAt)
	}
	return d.reverseLastLedger(chatID, messageID, actorUserID, actorUsername, actorNickname, targetType, trackLink, periodStartAt)
}

func (d *Database) reverseLastLedger(chatID int64, messageID int, actorUserID int64, actorUsername, actorNickname, targetType, trackLink, periodStartAt string) (LedgerReverseResult, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.reverseLastLedger(chatID, messageID, actorUserID, actorUsername, actorNickname, targetType, trackLink, periodStartAt)
	}
	result := LedgerReverseResult{}
	normalizedTargetType := strings.TrimSpace(targetType)
	normalizedPeriodStartAt := strings.TrimSpace(periodStartAt)
	var batchedUserID int64
	var batchedCurrency string
	var batchedDelta float64
	var reversalBloomInput *LedgerEntryInput
	err := d.withLedgerAtomicTx(func(tx *sql.Tx) error {
		reserved, err := reserveMessageTx(tx, chatID, messageID, "reversal:"+normalizedTargetType)
		if err != nil {
			return err
		}
		if !reserved {
			result.Duplicate = true
			return nil
		}

		sqlText := `SELECT id, chat_id, message_id, user_id, username, nickname, entry_type, amount, currency,
	base_amount, fee_rate, rate, final_amount, note, card_no, track_message_link, created_at, reversed, reversal_of
	FROM ledger_entries WHERE chat_id = ? AND reversed = 0 AND reversal_of IS NULL`
		args := []any{chatID}
		if normalizedTargetType != "" {
			sqlText += " AND entry_type = ?"
			args = append(args, normalizedTargetType)
		}
		sqlText += " ORDER BY id DESC LIMIT 1"

		var origin LedgerEntry
		var reversal sql.NullInt64
		row := tx.QueryRow(sqlText, args...)
		if err := row.Scan(
			&origin.ID, &origin.ChatID, &origin.MessageID, &origin.UserID, &origin.Username, &origin.Nickname, &origin.EntryType, &origin.Amount, &origin.Currency,
			&origin.BaseAmount, &origin.FeeRate, &origin.Rate, &origin.FinalAmount, &origin.Note, &origin.CardNo, &origin.TrackLink, &origin.CreatedAt, &origin.Reversed, &reversal,
		); err != nil {
			if err == sql.ErrNoRows {
				result.NotFound = true
				return nil
			}
			return err
		}
		if reversal.Valid {
			origin.ReversalOf = &reversal.Int64
		}
		if err := d.decryptLedgerSensitiveFields(&origin); err != nil {
			return err
		}
		if normalizedPeriodStartAt != "" {
			var inPeriod int
			if err := tx.QueryRow(`SELECT CASE WHEN datetime(?) >= datetime(?) THEN 1 ELSE 0 END`, origin.CreatedAt, normalizedPeriodStartAt).Scan(&inPeriod); err != nil {
				return err
			}
			if inPeriod == 0 {
				result.Settled = true
				return nil
			}
		}

		revOf := origin.ID
		reversalInput := LedgerEntryInput{
			ChatID:           chatID,
			MessageID:        messageID,
			UserID:           actorUserID,
			Username:         strings.ToLower(strings.TrimSpace(actorUsername)),
			Nickname:         strings.TrimSpace(actorNickname),
			EntryType:        origin.EntryType,
			Amount:           -origin.Amount,
			Currency:         origin.Currency,
			BaseAmount:       -origin.BaseAmount,
			FeeRate:          origin.FeeRate,
			Rate:             origin.Rate,
			FinalAmount:      -origin.FinalAmount,
			Note:             strings.TrimSpace(fmt.Sprintf("REV:%d %s", origin.ID, origin.Note)),
			CardNo:           origin.CardNo,
			TrackMessageLink: trackLink,
			ReversalOf:       &revOf,
		}
		if err := validateLedgerEntryAccounting(reversalInput); err != nil {
			return err
		}
		// Reversal entry and balance snapshot delta must succeed or fail together.
		reversalID, pendingDelta, err := d.addLedgerEntryWithBalanceTx(tx, reversalInput)
		if err != nil {
			if isLedgerMessageUniqueConstraintError(err) {
				result.Duplicate = true
				return nil
			}
			return err
		}
		batchedUserID = reversalInput.UserID
		batchedCurrency = reversalInput.Currency
		batchedDelta = pendingDelta
		reversalCopy := reversalInput
		reversalBloomInput = &reversalCopy

		res, err := tx.Exec(`UPDATE ledger_entries SET reversed = 1, reversal_of = ? WHERE id = ? AND reversed = 0`, reversalID, origin.ID)
		if err != nil {
			return err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return fmt.Errorf("目标记录已被撤回")
		}

		reversalSnapshot, err := d.getLedgerByIDTx(tx, reversalID)
		if err != nil {
			return err
		}
		detail, err := buildLedgerReversalAuditDetail(origin, reversalSnapshot, actorUserID, normalizedTargetType)
		if err != nil {
			return err
		}
		if err := addAuditLogTx(tx, chatID, messageID, actorUserID, "ledger_reversal", detail); err != nil {
			return err
		}
		reversalCount, totalCount, reversalRate, err := reversalAuditStatsTx(tx, chatID, actorUserID)
		if err != nil {
			return err
		}
		if shouldMarkReversalAuditRisk(reversalCount, totalCount, reversalRate) {
			riskDetail := fmt.Sprintf(
				"operator_user_id=%d reversal_count=%d total_count=%d reversal_rate=%.4f threshold=%.4f min_actions=%d",
				actorUserID,
				reversalCount,
				totalCount,
				reversalRate,
				reversalAuditRateThreshold,
				reversalAuditMinActionCount,
			)
			if err := addRiskEventTx(tx, chatID, messageID, actorUserID, reversalAuditRiskEventType, reversalAuditRiskSeverity, riskDetail); err != nil {
				return err
			}
		}

		result.OriginalID = origin.ID
		result.ReversalID = reversalID
		return nil
	})
	if err != nil {
		return LedgerReverseResult{}, err
	}
	d.enqueueHotAccountDelta(batchedUserID, batchedCurrency, batchedDelta)
	if result.ReversalID > 0 && reversalBloomInput != nil {
		d.indexOrderQueryBloomEntry(*reversalBloomInput)
	}
	d.syncLedgerEntryToSearchByID(result.ReversalID)
	return result, nil
}

func (d *Database) GetLedgerBalance(chatID int64) (float64, float64, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.GetLedgerBalance(chatID)
	}
	row := d.reportDB().QueryRow(`SELECT deposit_total, payout_total FROM ledger_balances WHERE chat_id = ?`, chatID)
	var depositTotal, payoutTotal float64
	if err := row.Scan(&depositTotal, &payoutTotal); err != nil {
		if err == sql.ErrNoRows {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	return depositTotal, payoutTotal, nil
}

func (d *Database) GetUserMonthlyTradeVolume(chatID, userID int64, at time.Time) (float64, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.GetUserMonthlyTradeVolume(chatID, userID, at)
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	at = at.UTC()
	monthStart := time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, time.UTC)
	nextMonthStart := monthStart.AddDate(0, 1, 0)

	row := d.raw.QueryRow(`
SELECT COALESCE(SUM(ABS(base_amount)), 0)
FROM ledger_entries
WHERE chat_id = ? AND user_id = ? AND reversed = 0 AND reversal_of IS NULL
  AND datetime(created_at) >= datetime(?)
  AND datetime(created_at) < datetime(?)`,
		chatID,
		userID,
		monthStart.Format("2006-01-02 15:04:05"),
		nextMonthStart.Format("2006-01-02 15:04:05"),
	)
	var monthlyVolume float64
	if err := row.Scan(&monthlyVolume); err != nil {
		return 0, err
	}
	return monthlyVolume, nil
}

func (d *Database) GetAccountBalance(userID int64, currency string) (float64, error) {
	normalizedCurrency := normalizeLedgerCurrency(currency)
	row := d.raw.QueryRow(`SELECT balance FROM account_balances WHERE user_id = ? AND currency = ?`, userID, normalizedCurrency)
	var balance float64
	if err := row.Scan(&balance); err != nil {
		if err == sql.ErrNoRows {
			return d.hotAccountBatch.pending(userID, normalizedCurrency), nil
		}
		return 0, err
	}
	balance += d.hotAccountBatch.pending(userID, normalizedCurrency)
	return balance, nil
}

func rebuildLedgerBalanceSnapshotTx(tx *sql.Tx, chatID int64) error {
	row := tx.QueryRow(`
SELECT
	COALESCE(SUM(CASE WHEN entry_type = 'deposit' THEN final_amount ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN entry_type = 'payout' THEN final_amount ELSE 0 END), 0)
FROM ledger_entries
WHERE chat_id = ?`, chatID)
	var depositTotal, payoutTotal float64
	if err := row.Scan(&depositTotal, &payoutTotal); err != nil {
		return err
	}
	_, err := tx.Exec(`
INSERT INTO ledger_balances (chat_id, deposit_total, payout_total, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(chat_id) DO UPDATE SET
	deposit_total = excluded.deposit_total,
	payout_total = excluded.payout_total,
	updated_at = excluded.updated_at`,
		chatID,
		depositTotal,
		payoutTotal,
		nowUTCText(),
	)
	return err
}

func rebuildAccountBalanceSnapshotTx(tx *sql.Tx) error {
	if _, err := tx.Exec(`DELETE FROM account_balances`); err != nil {
		return err
	}
	_, err := tx.Exec(`
INSERT INTO account_balances (user_id, currency, balance, version, updated_at)
SELECT
	user_id,
	UPPER(TRIM(currency)),
	COALESCE(SUM(
		CASE
			WHEN entry_type = 'deposit' THEN final_amount
			WHEN entry_type = 'payout' THEN -final_amount
			ELSE final_amount
		END
	), 0),
	0,
	?
FROM ledger_entries
GROUP BY user_id, UPPER(TRIM(currency))`,
		nowUTCText(),
	)
	return err
}

func (d *Database) AddLedgerEntry(item LedgerEntryInput) (int64, error) {
	if routed := d.databaseForChat(item.ChatID); routed != d {
		return routed.AddLedgerEntry(item)
	}
	var entryID int64
	var batchedDelta float64
	err := d.withLedgerAtomicTx(func(tx *sql.Tx) error {
		var err error
		entryID, batchedDelta, err = d.addLedgerEntryWithBalanceTx(tx, item)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	d.enqueueHotAccountDelta(item.UserID, item.Currency, batchedDelta)
	d.indexOrderQueryBloomEntry(item)
	d.syncLedgerEntryToSearchByID(entryID)
	return entryID, nil
}

func (d *Database) GetLastLedger(chatID int64, entryType string) (*LedgerEntry, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.GetLastLedger(chatID, entryType)
	}
	sqlText := `SELECT id, chat_id, message_id, user_id, username, nickname, entry_type, amount, currency,
	base_amount, fee_rate, rate, final_amount, note, card_no, track_message_link, created_at, reversed, reversal_of
	FROM ledger_entries WHERE chat_id = ? AND reversed = 0`
	args := []any{chatID}
	if strings.TrimSpace(entryType) != "" {
		sqlText += " AND entry_type = ?"
		args = append(args, entryType)
	}
	sqlText += " ORDER BY id DESC LIMIT 1"
	row := d.reportDB().QueryRow(sqlText, args...)
	var item LedgerEntry
	var reversal sql.NullInt64
	if err := row.Scan(
		&item.ID, &item.ChatID, &item.MessageID, &item.UserID, &item.Username, &item.Nickname, &item.EntryType, &item.Amount, &item.Currency,
		&item.BaseAmount, &item.FeeRate, &item.Rate, &item.FinalAmount, &item.Note, &item.CardNo, &item.TrackLink, &item.CreatedAt, &item.Reversed, &reversal,
	); err != nil {
		return nil, err
	}
	if reversal.Valid {
		item.ReversalOf = &reversal.Int64
	}
	if err := d.decryptLedgerSensitiveFields(&item); err != nil {
		return nil, err
	}
	return &item, nil
}

func (d *Database) MarkReversed(entryID, reversalID int64) error {
	return d.withLedgerAtomicTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE ledger_entries SET reversed = 1, reversal_of = ? WHERE id = ?`, reversalID, entryID)
		if err != nil {
			return err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return nil
		}

		// Keep legacy reversal flag updates and balance snapshots in the same commit unit.
		var chatID int64
		if err := tx.QueryRow(`SELECT chat_id FROM ledger_entries WHERE id = ?`, entryID).Scan(&chatID); err != nil {
			return err
		}
		return rebuildLedgerBalanceSnapshotTx(tx, chatID)
	})
}

func (d *Database) listLedgerBySQL(sqlText string, args ...any) ([]LedgerEntry, error) {
	rows, err := d.reportDB().Query(sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LedgerEntry{}
	for rows.Next() {
		var item LedgerEntry
		var reversal sql.NullInt64
		if err := rows.Scan(
			&item.ID, &item.ChatID, &item.MessageID, &item.UserID, &item.Username, &item.Nickname, &item.EntryType, &item.Amount, &item.Currency,
			&item.BaseAmount, &item.FeeRate, &item.Rate, &item.FinalAmount, &item.Note, &item.CardNo, &item.TrackLink, &item.CreatedAt, &item.Reversed, &reversal,
		); err != nil {
			return nil, err
		}
		if reversal.Valid {
			item.ReversalOf = &reversal.Int64
		}
		if err := d.decryptLedgerSensitiveFields(&item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func normalizeLedgerPeriodStart(startAt string) string {
	normalized := strings.TrimSpace(startAt)
	if normalized == "" {
		return "1970-01-01T00:00:00"
	}
	return normalized
}

func (d *Database) ListLedgerSince(chatID int64, startAt string) ([]LedgerEntry, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.ListLedgerSince(chatID, startAt)
	}
	normalizedStartAt := normalizeLedgerPeriodStart(startAt)
	return d.listLedgerBySQL(`
SELECT id, chat_id, message_id, user_id, username, nickname, entry_type, amount, currency,
	base_amount, fee_rate, rate, final_amount, note, card_no, track_message_link, created_at, reversed, reversal_of
	FROM ledger_entries WHERE chat_id = ? AND datetime(created_at) >= datetime(?) ORDER BY id ASC`, chatID, normalizedStartAt)
}

func (d *Database) ListLedgerByTypeSince(chatID int64, entryType, startAt string) ([]LedgerEntry, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.ListLedgerByTypeSince(chatID, entryType, startAt)
	}
	normalizedStartAt := normalizeLedgerPeriodStart(startAt)
	return d.listLedgerBySQL(`
SELECT id, chat_id, message_id, user_id, username, nickname, entry_type, amount, currency,
	base_amount, fee_rate, rate, final_amount, note, card_no, track_message_link, created_at, reversed, reversal_of
	FROM ledger_entries WHERE chat_id = ? AND entry_type = ? AND datetime(created_at) >= datetime(?) ORDER BY id ASC`, chatID, entryType, normalizedStartAt)
}

func (d *Database) ClearLedgerSince(chatID int64, startAt string) (int64, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.ClearLedgerSince(chatID, startAt)
	}
	if err := d.flushHotAccountPending(); err != nil {
		return 0, err
	}
	normalizedStartAt := normalizeLedgerPeriodStart(startAt)
	var deleted int64
	err := d.withLedgerAtomicTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`DELETE FROM ledger_entries WHERE chat_id = ? AND datetime(created_at) >= datetime(?)`, chatID, normalizedStartAt)
		if err != nil {
			return err
		}
		deleted, err = res.RowsAffected()
		if err != nil {
			return err
		}
		if deleted == 0 {
			return nil
		}
		if err := rebuildLedgerBalanceSnapshotTx(tx, chatID); err != nil {
			return err
		}
		return rebuildAccountBalanceSnapshotTx(tx)
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}

func (d *Database) ClearLedgerTypeSince(chatID int64, entryType, startAt string) (int64, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.ClearLedgerTypeSince(chatID, entryType, startAt)
	}
	if err := d.flushHotAccountPending(); err != nil {
		return 0, err
	}
	normalizedStartAt := normalizeLedgerPeriodStart(startAt)
	var deleted int64
	err := d.withLedgerAtomicTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`DELETE FROM ledger_entries WHERE chat_id = ? AND entry_type = ? AND datetime(created_at) >= datetime(?)`, chatID, entryType, normalizedStartAt)
		if err != nil {
			return err
		}
		deleted, err = res.RowsAffected()
		if err != nil {
			return err
		}
		if deleted == 0 {
			return nil
		}
		if err := rebuildLedgerBalanceSnapshotTx(tx, chatID); err != nil {
			return err
		}
		return rebuildAccountBalanceSnapshotTx(tx)
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}

func (d *Database) ListOrderList(chatID int64, startAt string, limit int) ([]LedgerEntry, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.ListOrderList(chatID, startAt, limit)
	}
	if limit <= 0 {
		limit = 50
	}
	normalizedStartAt := normalizeLedgerPeriodStart(startAt)
	return d.listLedgerBySQL(`
SELECT id, chat_id, message_id, user_id, username, nickname, entry_type, amount, currency,
	base_amount, fee_rate, rate, final_amount, note, card_no, track_message_link, created_at, reversed, reversal_of
	FROM ledger_entries WHERE chat_id = ? AND datetime(created_at) >= datetime(?) ORDER BY id DESC LIMIT ?`, chatID, normalizedStartAt, limit)
}

func (d *Database) SearchOrders(chatID int64, keyword string, amount *float64, startAt string) ([]LedgerEntry, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.SearchOrders(chatID, keyword, amount, startAt)
	}
	normalizedKeyword := strings.TrimSpace(keyword)
	if d.shouldShortCircuitOrderQuery(chatID, normalizedKeyword, amount) {
		return nil, nil
	}
	if rows, attempted, err := d.searchOrdersByBackend(chatID, normalizedKeyword, amount, startAt, defaultOrderSearchLimit); attempted {
		if err == nil && len(rows) > 0 {
			return rows, nil
		}
		if err == nil && len(rows) == 0 {
			// 新启用全文检索时历史数据可能尚未回填，空结果回退 SQL 保持兼容。
		}
	}
	return d.searchOrdersBySQL(chatID, normalizedKeyword, amount, startAt, defaultOrderSearchLimit)
}

func (d *Database) SearchOrdersInRange(chatID int64, keyword string, amount *float64, startAt, endAt string) ([]LedgerEntry, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.SearchOrdersInRange(chatID, keyword, amount, startAt, endAt)
	}
	normalizedStartAt := strings.TrimSpace(startAt)
	normalizedEndAt := strings.TrimSpace(endAt)
	if normalizedEndAt == "" {
		return d.SearchOrders(chatID, keyword, amount, normalizedStartAt)
	}
	normalizedKeyword := strings.TrimSpace(keyword)
	if d.shouldShortCircuitOrderQuery(chatID, normalizedKeyword, amount) {
		return nil, nil
	}
	return d.searchOrdersBySQLInRange(chatID, normalizedKeyword, amount, normalizedStartAt, normalizedEndAt, defaultOrderSearchLimit)
}

func (d *Database) searchOrdersBySQL(chatID int64, keyword string, amount *float64, startAt string, limit int) ([]LedgerEntry, error) {
	return d.searchOrdersBySQLInRange(chatID, keyword, amount, startAt, "", limit)
}

func (d *Database) searchOrdersBySQLInRange(chatID int64, keyword string, amount *float64, startAt, endAt string, limit int) ([]LedgerEntry, error) {
	normalizedLimit := normalizeOrderSearchLimit(limit)
	sqlText := `
SELECT id, chat_id, message_id, user_id, username, nickname, entry_type, amount, currency,
	base_amount, fee_rate, rate, final_amount, note, card_no, track_message_link, created_at, reversed, reversal_of
FROM ledger_entries WHERE chat_id = ?`
	args := []any{chatID}
	normalizedStartAt := strings.TrimSpace(startAt)
	if normalizedStartAt != "" {
		sqlText += " AND datetime(created_at) >= datetime(?)"
		args = append(args, normalizedStartAt)
	}
	normalizedEndAt := strings.TrimSpace(endAt)
	if normalizedEndAt != "" {
		sqlText += " AND datetime(created_at) < datetime(?)"
		args = append(args, normalizedEndAt)
	}
	if amount != nil {
		sqlText += " AND ABS(amount - ?) < 0.000001"
		args = append(args, *amount)
	}
	if keyword == "" {
		sqlText += " ORDER BY id DESC LIMIT ?"
		args = append(args, normalizedLimit)
		return d.listLedgerBySQL(sqlText, args...)
	}

	batchSize := normalizedLimit * searchOrdersScanMultiplier
	if batchSize < normalizedLimit {
		batchSize = normalizedLimit
	}
	if batchSize < defaultOrderSearchLimit {
		batchSize = defaultOrderSearchLimit
	}
	out := make([]LedgerEntry, 0, normalizedLimit)
	offset := 0
	for {
		pagedSQL := sqlText + " ORDER BY id DESC LIMIT ? OFFSET ?"
		pagedArgs := append(append([]any{}, args...), batchSize, offset)
		rows, err := d.listLedgerBySQL(pagedSQL, pagedArgs...)
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			break
		}
		for _, item := range rows {
			if !orderMatchKeyword(item, keyword) {
				continue
			}
			out = append(out, item)
			if len(out) >= normalizedLimit {
				return out, nil
			}
		}
		if len(rows) < batchSize {
			break
		}
		offset += len(rows)
	}
	return out, nil
}

func (d *Database) searchOrdersByBackend(chatID int64, keyword string, amount *float64, startAt string, limit int) ([]LedgerEntry, bool, error) {
	if d == nil || d.orderSearch == nil {
		return nil, false, nil
	}
	if keyword == "" && amount == nil {
		return nil, false, nil
	}
	normalizedLimit := normalizeOrderSearchLimit(limit)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ids, err := d.orderSearch.SearchLedgerEntryIDs(ctx, OrderSearchQuery{
		ChatID:  chatID,
		Keyword: keyword,
		Amount:  amount,
		StartAt: startAt,
		Limit:   normalizedLimit,
	})
	if err != nil {
		return nil, true, err
	}
	if len(ids) == 0 {
		return nil, true, nil
	}
	rows, err := d.listLedgerByIDs(d.reportDB(), chatID, startAt, ids)
	if err != nil {
		return nil, true, err
	}
	if len(rows) == 0 && d.reportDB() != d.raw {
		rows, err = d.listLedgerByIDs(d.raw, chatID, startAt, ids)
		if err != nil {
			return nil, true, err
		}
	}
	return rows, true, nil
}

func (d *Database) listLedgerByIDs(raw *sql.DB, chatID int64, startAt string, ids []int64) ([]LedgerEntry, error) {
	if raw == nil || len(ids) == 0 {
		return nil, nil
	}
	uniqueIDs := make([]int64, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
	}
	if len(uniqueIDs) == 0 {
		return nil, nil
	}
	placeholders := strings.Repeat("?,", len(uniqueIDs))
	placeholders = strings.TrimSuffix(placeholders, ",")
	args := make([]any, 0, len(uniqueIDs)+2)
	for _, id := range uniqueIDs {
		args = append(args, id)
	}
	args = append(args, chatID, normalizeLedgerPeriodStart(startAt))
	sqlText := `
SELECT id, chat_id, message_id, user_id, username, nickname, entry_type, amount, currency,
	base_amount, fee_rate, rate, final_amount, note, card_no, track_message_link, created_at, reversed, reversal_of
	FROM ledger_entries
	WHERE id IN (` + placeholders + `) AND chat_id = ? AND datetime(created_at) >= datetime(?)`
	rows, err := raw.Query(sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entryByID := make(map[int64]LedgerEntry, len(uniqueIDs))
	for rows.Next() {
		var item LedgerEntry
		var reversal sql.NullInt64
		if err := rows.Scan(
			&item.ID, &item.ChatID, &item.MessageID, &item.UserID, &item.Username, &item.Nickname, &item.EntryType, &item.Amount, &item.Currency,
			&item.BaseAmount, &item.FeeRate, &item.Rate, &item.FinalAmount, &item.Note, &item.CardNo, &item.TrackLink, &item.CreatedAt, &item.Reversed, &reversal,
		); err != nil {
			return nil, err
		}
		if reversal.Valid {
			item.ReversalOf = &reversal.Int64
		}
		if err := d.decryptLedgerSensitiveFields(&item); err != nil {
			return nil, err
		}
		entryByID[item.ID] = item
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	ordered := make([]LedgerEntry, 0, len(uniqueIDs))
	for _, id := range uniqueIDs {
		entry, ok := entryByID[id]
		if !ok {
			continue
		}
		ordered = append(ordered, entry)
	}
	return ordered, nil
}

func (d *Database) SetCustomContent(chatID int64, key, content string, createdBy int64) error {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.SetCustomContent(chatID, key, content, createdBy)
	}
	_, err := d.raw.Exec(`
INSERT INTO custom_contents (chat_id, trigger_key, content, created_by)
VALUES (?, ?, ?, ?)
ON CONFLICT(chat_id, trigger_key) DO UPDATE SET content = excluded.content`,
		chatID, strings.TrimSpace(key), content, createdBy,
	)
	return err
}

func (d *Database) DeleteCustomContent(chatID int64, key string) (int64, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.DeleteCustomContent(chatID, key)
	}
	res, err := d.raw.Exec(`DELETE FROM custom_contents WHERE chat_id = ? AND trigger_key = ?`, chatID, strings.TrimSpace(key))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (d *Database) GetCustomContent(chatID int64, key string) (string, bool, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.GetCustomContent(chatID, key)
	}
	row := d.raw.QueryRow(`SELECT content FROM custom_contents WHERE chat_id = ? AND trigger_key = ?`, chatID, strings.TrimSpace(key))
	var content string
	err := row.Scan(&content)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return content, true, nil
}

func (d *Database) ListCustomContents(chatID int64) ([]CustomContent, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.ListCustomContents(chatID)
	}
	rows, err := d.raw.Query(`SELECT trigger_key, content, created_by, created_at FROM custom_contents WHERE chat_id = ? ORDER BY id DESC`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CustomContent{}
	for rows.Next() {
		var item CustomContent
		if err := rows.Scan(&item.TriggerKey, &item.Content, &item.CreatedBy, &item.CreatedAt); err == nil {
			out = append(out, item)
		}
	}
	return out, nil
}

func (d *Database) SetAutoReply(chatID int64, keyword, content string) error {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.SetAutoReply(chatID, keyword, content)
	}
	_, err := d.raw.Exec(`INSERT INTO auto_replies (chat_id, keyword, content, enabled) VALUES (?, ?, ?, 1)`, chatID, strings.TrimSpace(keyword), content)
	return err
}

func (d *Database) DeleteAutoReply(chatID int64, keyword string) (int64, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.DeleteAutoReply(chatID, keyword)
	}
	res, err := d.raw.Exec(`DELETE FROM auto_replies WHERE chat_id = ? AND keyword = ?`, chatID, strings.TrimSpace(keyword))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (d *Database) ListAutoReplies(chatID int64) ([]AutoReply, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.ListAutoReplies(chatID)
	}
	rows, err := d.raw.Query(`SELECT keyword, content FROM auto_replies WHERE chat_id = ? AND enabled = 1 ORDER BY id ASC`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AutoReply{}
	for rows.Next() {
		var item AutoReply
		if err := rows.Scan(&item.Keyword, &item.Content); err == nil {
			out = append(out, item)
		}
	}
	return out, nil
}

func (d *Database) SetForwardRule(chatID int64, keyword, targetChatIDs string) error {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.SetForwardRule(chatID, keyword, targetChatIDs)
	}
	_, err := d.raw.Exec(`
INSERT INTO forward_rules (chat_id, keyword, target_chat_ids, enabled)
VALUES (?, ?, ?, 1)
ON CONFLICT(chat_id, keyword)
DO UPDATE SET target_chat_ids = excluded.target_chat_ids, enabled = 1`,
		chatID, strings.TrimSpace(keyword), strings.TrimSpace(targetChatIDs),
	)
	return err
}

func (d *Database) DeleteForwardRule(chatID int64, keyword string) (int64, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.DeleteForwardRule(chatID, keyword)
	}
	res, err := d.raw.Exec(`DELETE FROM forward_rules WHERE chat_id = ? AND keyword = ?`, chatID, strings.TrimSpace(keyword))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (d *Database) ListForwardRules(chatID int64) ([]ForwardRule, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.ListForwardRules(chatID)
	}
	rows, err := d.raw.Query(`SELECT keyword, target_chat_ids FROM forward_rules WHERE chat_id = ? AND enabled = 1 ORDER BY id ASC`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ForwardRule{}
	for rows.Next() {
		var item ForwardRule
		if err := rows.Scan(&item.Keyword, &item.TargetChatIDs); err == nil {
			out = append(out, item)
		}
	}
	return out, nil
}

func (d *Database) UpsertGroupUser(chatID, userID int64, username, nickname string) (bool, bool, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.UpsertGroupUser(chatID, userID, username, nickname)
	}
	diff, err := d.UpsertGroupUserWithDiff(chatID, userID, username, nickname)
	return diff.UsernameChanged, diff.NicknameChanged, err
}

func (d *Database) UpsertGroupUserWithDiff(chatID, userID int64, username, nickname string) (GroupUserProfileDiff, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.UpsertGroupUserWithDiff(chatID, userID, username, nickname)
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	row := d.raw.QueryRow(`SELECT username, nickname FROM group_users WHERE chat_id = ? AND user_id = ?`, chatID, userID)
	diff := GroupUserProfileDiff{
		NewUsername: username,
		NewNickname: nickname,
	}
	var oldU, oldN string
	err := row.Scan(&oldU, &oldN)
	if err != nil && err != sql.ErrNoRows {
		return diff, err
	}
	oldExists := err == nil
	diff.OldUsername = oldU
	diff.OldNickname = oldN
	diff.UsernameChanged = oldExists && oldU != username
	diff.NicknameChanged = oldExists && oldN != nickname

	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	_, err = d.raw.Exec(`
INSERT INTO group_users (chat_id, user_id, username, nickname, last_seen_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(chat_id, user_id)
DO UPDATE SET username = excluded.username, nickname = excluded.nickname, last_seen_at = excluded.last_seen_at`,
		chatID, userID, username, nickname, now,
	)
	if err != nil {
		return diff, err
	}
	if aliasErr := d.upsertGroupUserAliasOnCurrentDB(userID, username, chatID, now); aliasErr != nil {
		log.Printf("写入群成员 alias 失败(chat=%d user=%d username=%s): %v", chatID, userID, normalizeUsernameLookup(username), aliasErr)
	}
	return diff, err
}

func (d *Database) UpsertGroupUserAlias(userID int64, username string, sourceChatID int64) error {
	if routed := d.databaseForChat(sourceChatID); routed != d {
		return routed.UpsertGroupUserAlias(userID, username, sourceChatID)
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	return d.upsertGroupUserAliasOnCurrentDB(userID, username, sourceChatID, now)
}

func (d *Database) upsertGroupUserAliasOnCurrentDB(userID int64, username string, sourceChatID int64, now string) error {
	normalized := normalizeUsernameLookup(username)
	if userID == 0 || normalized == "" {
		return nil
	}
	_, err := d.raw.Exec(`
INSERT INTO group_user_aliases (user_id, username, source_chat_id, first_seen_at, last_seen_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(user_id, username)
DO UPDATE SET source_chat_id = excluded.source_chat_id, last_seen_at = excluded.last_seen_at`,
		userID, normalized, sourceChatID, now, now,
	)
	return err
}

func (d *Database) ListGroupUsersByOffset(chatID int64, limit, offset int) ([]GroupUser, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.ListGroupUsersByOffset(chatID, limit, offset)
	}
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := d.raw.Query(`SELECT chat_id, user_id, username, nickname, last_seen_at FROM group_users WHERE chat_id = ? ORDER BY last_seen_at DESC LIMIT ? OFFSET ?`, chatID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GroupUser{}
	for rows.Next() {
		var item GroupUser
		if err := rows.Scan(&item.ChatID, &item.UserID, &item.Username, &item.Nickname, &item.LastSeenAt); err == nil {
			out = append(out, item)
		}
	}
	return out, nil
}

func (d *Database) FindUserByUsername(chatID int64, username string) (*GroupUser, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.FindUserByUsername(chatID, username)
	}
	normalized := normalizeUsernameLookup(username)
	if normalized == "" {
		return nil, nil
	}
	row := d.raw.QueryRow(`SELECT chat_id, user_id, username, nickname, last_seen_at FROM group_users WHERE chat_id = ? AND lower(username) = ?`, chatID, normalized)
	var item GroupUser
	err := row.Scan(&item.ChatID, &item.UserID, &item.Username, &item.Nickname, &item.LastSeenAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (d *Database) FindUserByID(chatID, userID int64) (*GroupUser, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.FindUserByID(chatID, userID)
	}
	if chatID == 0 || userID == 0 {
		return nil, nil
	}
	row := d.raw.QueryRow(`SELECT chat_id, user_id, username, nickname, last_seen_at FROM group_users WHERE chat_id = ? AND user_id = ?`, chatID, userID)
	var item GroupUser
	err := row.Scan(&item.ChatID, &item.UserID, &item.Username, &item.Nickname, &item.LastSeenAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (d *Database) FindUserByAlias(chatID int64, username string) (*GroupUser, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.FindUserByAlias(chatID, username)
	}
	normalized := normalizeUsernameLookup(username)
	if normalized == "" {
		return nil, nil
	}
	row := d.raw.QueryRow(`
SELECT gu.chat_id, gu.user_id, gu.username, gu.nickname, gu.last_seen_at
FROM group_user_aliases AS alias
JOIN group_users AS gu
  ON gu.chat_id = alias.source_chat_id
 AND gu.user_id = alias.user_id
WHERE alias.source_chat_id = ?
  AND alias.username = ?
ORDER BY alias.last_seen_at DESC, gu.last_seen_at DESC
LIMIT 1`,
		chatID,
		normalized,
	)
	var item GroupUser
	err := row.Scan(&item.ChatID, &item.UserID, &item.Username, &item.Nickname, &item.LastSeenAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (d *Database) FindAnyUserByUsername(username string) (*GroupUser, error) {
	normalized := normalizeUsernameLookup(username)
	if normalized == "" {
		return nil, nil
	}
	if d.tenantRouter == nil {
		return d.findLatestUserByUsernameOnCurrentDB(normalized)
	}
	var latest *GroupUser
	for _, shard := range d.tenantRouter.shards {
		if shard == nil {
			continue
		}
		candidate, err := shard.findLatestUserByUsernameOnCurrentDB(normalized)
		if err != nil {
			return nil, err
		}
		if candidate == nil {
			continue
		}
		if latest == nil || candidate.LastSeenAt > latest.LastSeenAt {
			copyCandidate := *candidate
			latest = &copyCandidate
		}
	}
	return latest, nil
}

func (d *Database) FindAnyUserByAlias(username string) (*GroupUser, error) {
	normalized := normalizeUsernameLookup(username)
	if normalized == "" {
		return nil, nil
	}
	if d.tenantRouter == nil {
		return d.findLatestUserByAliasOnCurrentDB(normalized)
	}
	var latest *GroupUser
	for _, shard := range d.tenantRouter.shards {
		if shard == nil {
			continue
		}
		candidate, err := shard.findLatestUserByAliasOnCurrentDB(normalized)
		if err != nil {
			return nil, err
		}
		if candidate == nil {
			continue
		}
		if latest == nil || candidate.LastSeenAt > latest.LastSeenAt {
			copyCandidate := *candidate
			latest = &copyCandidate
		}
	}
	return latest, nil
}

func (d *Database) findLatestUserByUsernameOnCurrentDB(normalizedUsername string) (*GroupUser, error) {
	row := d.raw.QueryRow(`SELECT chat_id, user_id, username, nickname, last_seen_at FROM group_users WHERE lower(username) = ? ORDER BY last_seen_at DESC LIMIT 1`, normalizedUsername)
	var item GroupUser
	err := row.Scan(&item.ChatID, &item.UserID, &item.Username, &item.Nickname, &item.LastSeenAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (d *Database) findLatestUserByAliasOnCurrentDB(normalizedUsername string) (*GroupUser, error) {
	row := d.raw.QueryRow(`
SELECT gu.chat_id, gu.user_id, gu.username, gu.nickname, gu.last_seen_at
FROM group_user_aliases AS alias
JOIN group_users AS gu
  ON gu.user_id = alias.user_id
WHERE alias.username = ?
ORDER BY alias.last_seen_at DESC, gu.last_seen_at DESC
LIMIT 1`,
		normalizedUsername,
	)
	var item GroupUser
	err := row.Scan(&item.ChatID, &item.UserID, &item.Username, &item.Nickname, &item.LastSeenAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func normalizeUsernameLookup(username string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(username), "@"))
}

func (d *Database) ListRecentUsers(chatID int64, limit int) ([]GroupUser, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.ListRecentUsers(chatID, limit)
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := d.raw.Query(`SELECT chat_id, user_id, username, nickname, last_seen_at FROM group_users WHERE chat_id = ? ORDER BY last_seen_at DESC LIMIT ?`, chatID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GroupUser{}
	for rows.Next() {
		var item GroupUser
		if err := rows.Scan(&item.ChatID, &item.UserID, &item.Username, &item.Nickname, &item.LastSeenAt); err == nil {
			out = append(out, item)
		}
	}
	return out, nil
}

func (d *Database) ListGroupLastSeenAt(chatIDs []int64) (map[int64]string, error) {
	ids := uniqueNonZeroInt64s(chatIDs)
	if len(ids) == 0 {
		return map[int64]string{}, nil
	}
	if d.tenantRouter != nil {
		out := make(map[int64]string, len(ids))
		for _, shard := range d.tenantRouter.shards {
			rows, err := shard.listGroupLastSeenAtLocal(ids)
			if err != nil {
				return nil, err
			}
			for chatID, seenAt := range rows {
				out[chatID] = seenAt
			}
		}
		return out, nil
	}
	return d.listGroupLastSeenAtLocal(ids)
}

func (d *Database) listGroupLastSeenAtLocal(chatIDs []int64) (map[int64]string, error) {
	ids := uniqueNonZeroInt64s(chatIDs)
	if len(ids) == 0 {
		return map[int64]string{}, nil
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = strings.TrimSuffix(placeholders, ",")
	args := make([]any, 0, len(ids))
	for _, chatID := range ids {
		args = append(args, chatID)
	}
	rows, err := d.raw.Query(`
SELECT chat_id, MAX(datetime(REPLACE(TRIM(last_seen_at), 'T', ' '))) AS last_seen_at
FROM group_users
WHERE chat_id IN (`+placeholders+`)
GROUP BY chat_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]string, len(ids))
	for rows.Next() {
		var chatID int64
		var lastSeen sql.NullString
		if err := rows.Scan(&chatID, &lastSeen); err != nil {
			return nil, err
		}
		if lastSeen.Valid && strings.TrimSpace(lastSeen.String) != "" {
			out[chatID] = strings.TrimSpace(lastSeen.String)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func uniqueNonZeroInt64s(values []int64) []int64 {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[int64]struct{}, len(values))
	out := make([]int64, 0, len(values))
	for _, value := range values {
		if value == 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func (d *Database) ClearCardHistory(chatID int64) (int64, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.ClearCardHistory(chatID)
	}
	res, err := d.raw.Exec(`DELETE FROM card_history WHERE chat_id = ?`, chatID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (d *Database) ClearDuplicateAlertHistory(chatID int64) (int64, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.ClearDuplicateAlertHistory(chatID)
	}
	res, err := d.raw.Exec(`DELETE FROM duplicate_alert_history WHERE chat_id = ?`, chatID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (d *Database) ClearGroupUserCache(chatID int64) (int64, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.ClearGroupUserCache(chatID)
	}
	res, err := d.raw.Exec(`DELETE FROM group_users WHERE chat_id = ?`, chatID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (d *Database) RecordCardSeen(chatID int64, cardNo string, entryID int64) (int, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.RecordCardSeen(chatID, cardNo, entryID)
	}
	row := d.raw.QueryRow(`SELECT seen_count FROM card_history WHERE chat_id = ? AND card_no = ?`, chatID, cardNo)
	var count int
	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	err := row.Scan(&count)
	if err == sql.ErrNoRows {
		_, err := d.raw.Exec(`INSERT INTO card_history (chat_id, card_no, seen_count, last_entry_id, last_seen_at) VALUES (?, ?, 1, ?, ?)`, chatID, cardNo, entryID, now)
		return 1, err
	}
	if err != nil {
		return 0, err
	}
	count++
	_, err = d.raw.Exec(`UPDATE card_history SET seen_count = ?, last_entry_id = ?, last_seen_at = ? WHERE chat_id = ? AND card_no = ?`, count, entryID, now, chatID, cardNo)
	return count, err
}

func (d *Database) SetIdentity(chatID int64, identity string, userID int64, username string) error {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.SetIdentity(chatID, identity, userID, username)
	}
	_, err := d.raw.Exec(`
INSERT INTO identities (chat_id, identity_name, user_id, username)
VALUES (?, ?, ?, ?)
ON CONFLICT(chat_id, identity_name, user_id) DO UPDATE SET username = excluded.username`,
		chatID, strings.TrimSpace(identity), userID, strings.TrimSpace(username),
	)
	return err
}

func (d *Database) RemoveIdentity(chatID int64, userID int64) error {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.RemoveIdentity(chatID, userID)
	}
	_, err := d.raw.Exec(`DELETE FROM identities WHERE chat_id = ? AND user_id = ?`, chatID, userID)
	return err
}

func (d *Database) RemoveIdentityByName(chatID int64, identity string, userID int64) error {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.RemoveIdentityByName(chatID, identity, userID)
	}
	_, err := d.raw.Exec(`DELETE FROM identities WHERE chat_id = ? AND identity_name = ? AND user_id = ?`, chatID, strings.TrimSpace(identity), userID)
	return err
}

func (d *Database) ListIdentityNames(chatID int64) ([]string, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.ListIdentityNames(chatID)
	}
	rows, err := d.raw.Query(`SELECT DISTINCT identity_name FROM identities WHERE chat_id = ? ORDER BY identity_name ASC`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			out = append(out, name)
		}
	}
	return out, nil
}

func (d *Database) ListIdentityUsers(chatID int64, identity string) ([]IdentityUser, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.ListIdentityUsers(chatID, identity)
	}
	rows, err := d.raw.Query(`SELECT user_id, username FROM identities WHERE chat_id = ? AND identity_name = ? ORDER BY id ASC`, chatID, strings.TrimSpace(identity))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IdentityUser{}
	for rows.Next() {
		var item IdentityUser
		if err := rows.Scan(&item.UserID, &item.Username); err == nil {
			out = append(out, item)
		}
	}
	return out, nil
}

func (d *Database) ListIdentityAssignments(chatID int64) ([]IdentityAssignment, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.ListIdentityAssignments(chatID)
	}
	rows, err := d.raw.Query(`SELECT identity_name, user_id, username, created_at FROM identities WHERE chat_id = ? ORDER BY id DESC`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IdentityAssignment{}
	for rows.Next() {
		var item IdentityAssignment
		if err := rows.Scan(&item.IdentityName, &item.UserID, &item.Username, &item.CreatedAt); err == nil {
			out = append(out, item)
		}
	}
	return out, nil
}

func (d *Database) AddEvaluation(chatID, userID int64, username string, score int, comment, targetType string, targetUserID int64) error {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.AddEvaluation(chatID, userID, username, score, comment, targetType, targetUserID)
	}
	_, err := d.raw.Exec(`
INSERT INTO evaluations (chat_id, user_id, username, target_type, target_user_id, score, comment)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		chatID, userID, username, targetType, targetUserID, score, comment,
	)
	return err
}

func (d *Database) ListEvaluations(chatID int64, targetType string) ([]map[string]any, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.ListEvaluations(chatID, targetType)
	}
	rows, err := d.raw.Query(`SELECT user_id, username, score, comment FROM evaluations WHERE chat_id = ? AND target_type = ? ORDER BY id DESC LIMIT 100`, chatID, targetType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var userID int64
		var username, comment string
		var score int
		if err := rows.Scan(&userID, &username, &score, &comment); err == nil {
			out = append(out, map[string]any{"user_id": userID, "username": username, "score": score, "comment": comment})
		}
	}
	return out, nil
}

func (d *Database) StartTimer(chatID, userID int64, startedAt string) error {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.StartTimer(chatID, userID, startedAt)
	}
	_, err := d.raw.Exec(`
INSERT INTO timers (chat_id, user_id, started_at)
VALUES (?, ?, ?)
ON CONFLICT(chat_id, user_id) DO UPDATE SET started_at = excluded.started_at`,
		chatID, userID, startedAt,
	)
	return err
}

func (d *Database) EndTimer(chatID, userID int64) (string, bool, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.EndTimer(chatID, userID)
	}
	row := d.raw.QueryRow(`SELECT started_at FROM timers WHERE chat_id = ? AND user_id = ?`, chatID, userID)
	var startedAt string
	err := row.Scan(&startedAt)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	_, err = d.raw.Exec(`DELETE FROM timers WHERE chat_id = ? AND user_id = ?`, chatID, userID)
	return startedAt, true, err
}

func (d *Database) AddSchedule(chatID int64, sendAt, content string, sourceChatID int64, sourceMessageID int, identityName, targetTags string, creatorUserID int64) (int64, error) {
	if d.tenantRouter != nil {
		shardIndex := d.scheduleShardIndex(chatID)
		shard := d.databaseForChat(chatID)
		localID, err := shard.addScheduleLocal(chatID, sendAt, content, sourceChatID, sourceMessageID, identityName, targetTags, creatorUserID)
		if err != nil {
			return 0, err
		}
		return d.encodeScheduleID(shardIndex, localID), nil
	}
	return d.addScheduleLocal(chatID, sendAt, content, sourceChatID, sourceMessageID, identityName, targetTags, creatorUserID)
}

func (d *Database) addScheduleLocal(chatID int64, sendAt, content string, sourceChatID int64, sourceMessageID int, identityName, targetTags string, creatorUserID int64) (int64, error) {
	res, err := d.raw.Exec(`
INSERT INTO scheduled_broadcasts (
	chat_id, send_at, content, source_chat_id, source_message_id, identity_name, target_tags, creator_user_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		chatID, sendAt, content, sourceChatID, sourceMessageID, identityName, targetTags, creatorUserID,
	)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, nil
}

func (d *Database) ListPendingSchedules() ([]Schedule, error) {
	if d.tenantRouter != nil {
		out := make([]Schedule, 0, 32)
		for shardIndex, shard := range d.tenantRouter.shards {
			rows, err := shard.listSchedulesBySQL(`SELECT id, chat_id, send_at, content, source_chat_id, source_message_id, identity_name, target_tags, status, creator_user_id FROM scheduled_broadcasts WHERE status = 'pending' ORDER BY send_at ASC`)
			if err != nil {
				return nil, err
			}
			for i := range rows {
				rows[i].ID = d.encodeScheduleID(shardIndex, rows[i].ID)
			}
			out = append(out, rows...)
		}
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].SendAt == out[j].SendAt {
				return out[i].ID < out[j].ID
			}
			return out[i].SendAt < out[j].SendAt
		})
		return out, nil
	}
	return d.listSchedulesBySQL(`SELECT id, chat_id, send_at, content, source_chat_id, source_message_id, identity_name, target_tags, status, creator_user_id FROM scheduled_broadcasts WHERE status = 'pending' ORDER BY send_at ASC`)
}

func (d *Database) GetSchedule(scheduleID int64) (*Schedule, error) {
	if d.tenantRouter != nil {
		shardIndex, localID, ok := d.decodeScheduleID(scheduleID)
		if !ok {
			return nil, nil
		}
		shard := d.tenantRouter.shards[shardIndex]
		rows, err := shard.listSchedulesBySQL(`SELECT id, chat_id, send_at, content, source_chat_id, source_message_id, identity_name, target_tags, status, creator_user_id FROM scheduled_broadcasts WHERE id = ?`, localID)
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			return nil, nil
		}
		rows[0].ID = d.encodeScheduleID(shardIndex, rows[0].ID)
		return &rows[0], nil
	}
	rows, err := d.listSchedulesBySQL(`SELECT id, chat_id, send_at, content, source_chat_id, source_message_id, identity_name, target_tags, status, creator_user_id FROM scheduled_broadcasts WHERE id = ?`, scheduleID)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

func (d *Database) ListSchedulesByChat(chatID int64, limit int) ([]Schedule, error) {
	if limit <= 0 {
		limit = 30
	}
	if d.tenantRouter != nil {
		shardIndex := d.scheduleShardIndex(chatID)
		rows, err := d.databaseForChat(chatID).listSchedulesBySQL(`SELECT id, chat_id, send_at, content, source_chat_id, source_message_id, identity_name, target_tags, status, creator_user_id FROM scheduled_broadcasts WHERE chat_id = ? ORDER BY id DESC LIMIT ?`, chatID, limit)
		if err != nil {
			return nil, err
		}
		for i := range rows {
			rows[i].ID = d.encodeScheduleID(shardIndex, rows[i].ID)
		}
		return rows, nil
	}
	return d.listSchedulesBySQL(`SELECT id, chat_id, send_at, content, source_chat_id, source_message_id, identity_name, target_tags, status, creator_user_id FROM scheduled_broadcasts WHERE chat_id = ? ORDER BY id DESC LIMIT ?`, chatID, limit)
}

func (d *Database) SetScheduleStatus(scheduleID int64, status string) error {
	if d.tenantRouter != nil {
		shardIndex, localID, ok := d.decodeScheduleID(scheduleID)
		if !ok {
			return nil
		}
		return d.tenantRouter.shards[shardIndex].setScheduleStatusLocal(localID, status)
	}
	return d.setScheduleStatusLocal(scheduleID, status)
}

func (d *Database) setScheduleStatusLocal(scheduleID int64, status string) error {
	_, err := d.raw.Exec(`UPDATE scheduled_broadcasts SET status = ? WHERE id = ?`, status, scheduleID)
	return err
}

func (d *Database) DeleteSchedule(scheduleID int64) error {
	if d.tenantRouter != nil {
		shardIndex, localID, ok := d.decodeScheduleID(scheduleID)
		if !ok {
			return nil
		}
		return d.tenantRouter.shards[shardIndex].deleteScheduleLocal(localID)
	}
	return d.deleteScheduleLocal(scheduleID)
}

func (d *Database) deleteScheduleLocal(scheduleID int64) error {
	_, err := d.raw.Exec(`DELETE FROM scheduled_broadcasts WHERE id = ?`, scheduleID)
	return err
}

func (d *Database) UpdateSchedule(scheduleID int64, fields map[string]any) error {
	if d.tenantRouter != nil {
		shardIndex, localID, ok := d.decodeScheduleID(scheduleID)
		if !ok {
			return nil
		}
		return d.tenantRouter.shards[shardIndex].updateScheduleLocal(localID, fields)
	}
	return d.updateScheduleLocal(scheduleID, fields)
}

func (d *Database) updateScheduleLocal(scheduleID int64, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	allow := map[string]struct{}{
		"send_at": {}, "content": {}, "source_chat_id": {}, "source_message_id": {}, "identity_name": {}, "target_tags": {}, "status": {},
	}
	for k := range fields {
		if _, ok := allow[k]; !ok {
			return fmt.Errorf("unsupported schedule field: %s", k)
		}
	}
	sets := []string{}
	args := []any{}
	for k, v := range fields {
		sets = append(sets, fmt.Sprintf("%s = ?", quoteSQLiteIdentifier(k)))
		args = append(args, v)
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, scheduleID)
	sqlText := fmt.Sprintf("UPDATE scheduled_broadcasts SET %s WHERE id = ?", strings.Join(sets, ", "))
	_, err := d.raw.Exec(sqlText, args...)
	return err
}

func (d *Database) listSchedulesBySQL(sqlText string, args ...any) ([]Schedule, error) {
	rows, err := d.raw.Query(sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Schedule{}
	for rows.Next() {
		var item Schedule
		if err := rows.Scan(&item.ID, &item.ChatID, &item.SendAt, &item.Content, &item.SourceChatID, &item.SourceMessageID, &item.IdentityName, &item.TargetTags, &item.Status, &item.CreatorUserID); err == nil {
			out = append(out, item)
		}
	}
	return out, nil
}

func (d *Database) ListGroupsByTags(tags []string) ([]GroupConfig, error) {
	if len(tags) == 0 {
		return nil, nil
	}
	parts := []string{}
	args := []any{}
	for _, t := range tags {
		parts = append(parts, "group_tags LIKE ?")
		args = append(args, "%"+t+"%")
	}
	sqlText := `SELECT chat_id, title, owner_user_id, bookkeeping_enabled, bookkeeping_cycle_key, display_nickname, show_usdt_conversion,
	fee_rate, payout_fee_rate, custom_rate, payout_rate, use_realtime_rate, rate_source, day_cut_time,
	payout_address, payout_address_remark, exclusive_address, exclusive_address_owner, exclusive_address_remark, guarantee_rule, group_type, duplicate_alert_enabled, group_tags, at_broadcast_enabled,
	auto_reply_enabled, custom_content_with_time, class_start_ad, class_end_ad, welcome_enabled, welcome_text, ledger_card_hide_reversed
	FROM group_config WHERE ` + strings.Join(parts, " OR ")
	if d.tenantRouter != nil {
		out := make([]GroupConfig, 0, len(tags))
		for _, shard := range d.tenantRouter.shards {
			rows, err := shard.listGroupConfigsBySQL(sqlText, args...)
			if err != nil {
				return nil, err
			}
			out = append(out, rows...)
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].ChatID < out[j].ChatID })
		return out, nil
	}
	return d.listGroupConfigsBySQL(sqlText, args...)
}

func (d *Database) ListGroupsByType(groupType string) ([]GroupConfig, error) {
	sqlText := `SELECT chat_id, title, owner_user_id, bookkeeping_enabled, bookkeeping_cycle_key, display_nickname, show_usdt_conversion,
	fee_rate, payout_fee_rate, custom_rate, payout_rate, use_realtime_rate, rate_source, day_cut_time,
	payout_address, payout_address_remark, exclusive_address, exclusive_address_owner, exclusive_address_remark, guarantee_rule, group_type, duplicate_alert_enabled, group_tags, at_broadcast_enabled,
		auto_reply_enabled, custom_content_with_time, class_start_ad, class_end_ad, welcome_enabled, welcome_text, ledger_card_hide_reversed
		FROM group_config WHERE group_type = ?`
	groupType = strings.TrimSpace(groupType)
	if d.tenantRouter != nil {
		out := make([]GroupConfig, 0, 8)
		for _, shard := range d.tenantRouter.shards {
			rows, err := shard.listGroupConfigsBySQL(sqlText, groupType)
			if err != nil {
				return nil, err
			}
			out = append(out, rows...)
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].ChatID < out[j].ChatID })
		return out, nil
	}
	return d.listGroupConfigsBySQL(sqlText, groupType)
}

func (d *Database) ListAllGroups() ([]GroupConfig, error) {
	sqlText := `SELECT chat_id, title, owner_user_id, bookkeeping_enabled, bookkeeping_cycle_key, display_nickname, show_usdt_conversion,
	fee_rate, payout_fee_rate, custom_rate, payout_rate, use_realtime_rate, rate_source, day_cut_time,
	payout_address, payout_address_remark, exclusive_address, exclusive_address_owner, exclusive_address_remark, guarantee_rule, group_type, duplicate_alert_enabled, group_tags, at_broadcast_enabled,
		auto_reply_enabled, custom_content_with_time, class_start_ad, class_end_ad, welcome_enabled, welcome_text, ledger_card_hide_reversed
		FROM group_config ORDER BY updated_at DESC`
	if d.tenantRouter != nil {
		out := make([]GroupConfig, 0, 16)
		for _, shard := range d.tenantRouter.shards {
			rows, err := shard.listGroupConfigsBySQL(sqlText)
			if err != nil {
				return nil, err
			}
			out = append(out, rows...)
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].ChatID < out[j].ChatID })
		return out, nil
	}
	return d.listGroupConfigsBySQL(sqlText)
}

func (d *Database) ListEnabledGroups() ([]GroupConfig, error) {
	sqlText := `SELECT chat_id, title, owner_user_id, bookkeeping_enabled, bookkeeping_cycle_key, display_nickname, show_usdt_conversion,
	fee_rate, payout_fee_rate, custom_rate, payout_rate, use_realtime_rate, rate_source, day_cut_time,
	payout_address, payout_address_remark, exclusive_address, exclusive_address_owner, exclusive_address_remark, guarantee_rule, group_type, duplicate_alert_enabled, group_tags, at_broadcast_enabled,
		auto_reply_enabled, custom_content_with_time, class_start_ad, class_end_ad, welcome_enabled, welcome_text, ledger_card_hide_reversed
		FROM group_config
	WHERE bookkeeping_enabled = 1
	ORDER BY updated_at DESC`
	if d.tenantRouter != nil {
		out := make([]GroupConfig, 0, 16)
		for _, shard := range d.tenantRouter.shards {
			rows, err := shard.listGroupConfigsBySQL(sqlText)
			if err != nil {
				return nil, err
			}
			out = append(out, rows...)
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].ChatID < out[j].ChatID })
		return out, nil
	}
	return d.listGroupConfigsBySQL(sqlText)
}

func (d *Database) listGroupConfigsBySQL(sqlText string, args ...any) ([]GroupConfig, error) {
	rows, err := d.raw.Query(sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GroupConfig{}
	for rows.Next() {
		var g GroupConfig
		if err := rows.Scan(
			&g.ChatID, &g.Title, &g.OwnerUserID, &g.BookkeepingEnabled, &g.BookkeepingCycleKey, &g.DisplayNickname, &g.ShowUSDTConversion,
			&g.FeeRate, &g.PayoutFeeRate, &g.CustomRate, &g.PayoutRate, &g.UseRealtimeRate, &g.RateSource, &g.DayCutTime,
			&g.PayoutAddress, &g.PayoutAddressRemark, &g.ExclusiveAddress, &g.ExclusiveAddressOwner, &g.ExclusiveAddressRemark, &g.GuaranteeRule, &g.GroupType, &g.DuplicateAlertEnabled, &g.GroupTags, &g.AtBroadcastEnabled,
			&g.AutoReplyEnabled, &g.CustomContentWithTime, &g.ClassStartAd, &g.ClassEndAd, &g.WelcomeEnabled, &g.WelcomeText, &g.LedgerCardHideReversed,
		); err == nil {
			out = append(out, g)
		}
	}
	return out, nil
}

func (d *Database) AddDelayedQuery(chatID, creatorUserID int64, keyword string, amount *float64, note string) (int64, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.AddDelayedQuery(chatID, creatorUserID, keyword, amount, note)
	}
	encryptedNote, err := d.encryptSensitiveFieldValue(note)
	if err != nil {
		return 0, err
	}
	res, err := d.raw.Exec(`
INSERT INTO delayed_queries (chat_id, creator_user_id, keyword, amount, note, status)
VALUES (?, ?, ?, ?, ?, 'pending')`, chatID, creatorUserID, keyword, amount, encryptedNote)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, nil
}

func (d *Database) ListDelayedQueries(chatID int64, status string) ([]DelayedQuery, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.ListDelayedQueries(chatID, status)
	}
	if strings.TrimSpace(status) == "" {
		status = "pending"
	}
	rows, err := d.raw.Query(`SELECT id, keyword, amount, note, status FROM delayed_queries WHERE chat_id = ? AND status = ? ORDER BY id DESC LIMIT 100`, chatID, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DelayedQuery{}
	for rows.Next() {
		var item DelayedQuery
		var amount sql.NullFloat64
		if err := rows.Scan(&item.ID, &item.Keyword, &amount, &item.Note, &item.Status); err == nil {
			if amount.Valid {
				val := amount.Float64
				item.Amount = &val
			}
			decryptedNote, err := d.decryptSensitiveFieldValue(item.Note)
			if err != nil {
				return nil, err
			}
			item.Note = decryptedNote
			out = append(out, item)
		}
	}
	return out, nil
}

func (d *Database) ClearDelayedQueries(chatID int64) (int64, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.ClearDelayedQueries(chatID)
	}
	res, err := d.raw.Exec(`DELETE FROM delayed_queries WHERE chat_id = ?`, chatID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (d *Database) SetNextPayoutRate(chatID, userID int64, rate float64) error {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.SetNextPayoutRate(chatID, userID, rate)
	}
	_, err := d.raw.Exec(`
INSERT INTO user_runtime_state (chat_id, user_id, next_payout_rate, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(chat_id, user_id) DO UPDATE SET next_payout_rate = excluded.next_payout_rate, updated_at = excluded.updated_at`,
		chatID, userID, rate, time.Now().UTC().Format("2006-01-02T15:04:05"),
	)
	return err
}

func (d *Database) PopNextPayoutRate(chatID, userID int64) (*float64, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.PopNextPayoutRate(chatID, userID)
	}
	row := d.raw.QueryRow(`SELECT next_payout_rate FROM user_runtime_state WHERE chat_id = ? AND user_id = ?`, chatID, userID)
	var val sql.NullFloat64
	err := row.Scan(&val)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_, err = d.raw.Exec(`UPDATE user_runtime_state SET next_payout_rate = NULL, updated_at = ? WHERE chat_id = ? AND user_id = ?`, time.Now().UTC().Format("2006-01-02T15:04:05"), chatID, userID)
	if err != nil {
		return nil, err
	}
	if !val.Valid {
		return nil, nil
	}
	out := val.Float64
	return &out, nil
}

func normalizeCardNo(v string) string {
	out := strings.Builder{}
	out.Grow(len(v))
	for _, r := range v {
		if r >= '0' && r <= '9' {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func (d *Database) SetBranchCodeOverride(chatID int64, cardNo, cnapsCode, branchName, note string, updatedBy int64) error {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.SetBranchCodeOverride(chatID, cardNo, cnapsCode, branchName, note, updatedBy)
	}
	cardNo = normalizeCardNo(cardNo)
	if chatID == 0 || cardNo == "" || strings.TrimSpace(cnapsCode) == "" {
		return fmt.Errorf("参数不完整")
	}
	encryptedNote, err := d.encryptSensitiveFieldValue(strings.TrimSpace(note))
	if err != nil {
		return err
	}
	_, err = d.raw.Exec(`
INSERT INTO branch_code_overrides (chat_id, card_no, cnaps_code, branch_name, note, updated_by, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(chat_id, card_no) DO UPDATE SET
cnaps_code = excluded.cnaps_code,
branch_name = excluded.branch_name,
note = excluded.note,
updated_by = excluded.updated_by,
updated_at = excluded.updated_at`,
		chatID, cardNo, strings.TrimSpace(cnapsCode), strings.TrimSpace(branchName), encryptedNote, updatedBy, time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

func (d *Database) GetBranchCodeOverride(chatID int64, cardNo string) (*BranchCodeOverride, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.GetBranchCodeOverride(chatID, cardNo)
	}
	cardNo = normalizeCardNo(cardNo)
	if chatID == 0 || cardNo == "" {
		return nil, nil
	}
	row := d.raw.QueryRow(`SELECT chat_id, card_no, cnaps_code, branch_name, note, updated_by, updated_at FROM branch_code_overrides WHERE chat_id = ? AND card_no = ?`, chatID, cardNo)
	var item BranchCodeOverride
	if err := row.Scan(&item.ChatID, &item.CardNo, &item.CNAPSCode, &item.BranchName, &item.Note, &item.UpdatedBy, &item.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	decryptedNote, err := d.decryptSensitiveFieldValue(item.Note)
	if err != nil {
		return nil, err
	}
	item.Note = decryptedNote
	return &item, nil
}

func (d *Database) DeleteBranchCodeOverride(chatID int64, cardNo string) (int64, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.DeleteBranchCodeOverride(chatID, cardNo)
	}
	cardNo = normalizeCardNo(cardNo)
	if chatID == 0 || cardNo == "" {
		return 0, nil
	}
	res, err := d.raw.Exec(`DELETE FROM branch_code_overrides WHERE chat_id = ? AND card_no = ?`, chatID, cardNo)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (d *Database) GetPrivateUserProfile(userID int64) (PrivateUserProfile, error) {
	if userID == 0 {
		return PrivateUserProfile{}, nil
	}
	row := d.raw.QueryRow(`SELECT user_id, trial_status, trial_applied_at, expire_at, updated_at FROM private_user_profiles WHERE user_id = ?`, userID)
	var p PrivateUserProfile
	if err := row.Scan(&p.UserID, &p.TrialStatus, &p.TrialAppliedAt, &p.ExpireAt, &p.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return PrivateUserProfile{UserID: userID}, nil
		}
		return PrivateUserProfile{}, err
	}
	return p, nil
}

func (d *Database) UpsertPrivateUserProfile(profile PrivateUserProfile) error {
	if profile.UserID == 0 {
		return fmt.Errorf("userID 不能为空")
	}
	_, err := d.raw.Exec(`
INSERT INTO private_user_profiles (user_id, trial_status, trial_applied_at, expire_at, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(user_id) DO UPDATE SET
trial_status = excluded.trial_status,
trial_applied_at = excluded.trial_applied_at,
expire_at = excluded.expire_at,
updated_at = excluded.updated_at`,
		profile.UserID, strings.TrimSpace(profile.TrialStatus), strings.TrimSpace(profile.TrialAppliedAt), strings.TrimSpace(profile.ExpireAt), time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

func (d *Database) TrackAddressVerification(chatID int64, address string, senderUserID int64, senderUsername string, verifiedAt time.Time) (AddressVerificationSnapshot, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.TrackAddressVerification(chatID, address, senderUserID, senderUsername, verifiedAt)
	}
	normalizedAddress := strings.TrimSpace(address)
	if chatID == 0 || normalizedAddress == "" {
		return AddressVerificationSnapshot{}, fmt.Errorf("chatID 与 address 不能为空")
	}
	if senderUserID == 0 {
		return AddressVerificationSnapshot{}, fmt.Errorf("senderUserID 不能为空")
	}
	if verifiedAt.IsZero() {
		verifiedAt = time.Now().UTC()
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	tx, err := d.raw.Begin()
	if err != nil {
		return AddressVerificationSnapshot{}, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	snapshot := AddressVerificationSnapshot{
		ChatID:             chatID,
		Address:            normalizedAddress,
		LastSenderUserID:   senderUserID,
		LastSenderUsername: strings.TrimSpace(senderUsername),
		LastVerifiedAt:     verifiedAt.UTC().Format(time.RFC3339),
	}

	row := tx.QueryRow(`
SELECT verification_count, last_sender_user_id, last_sender_username
FROM address_verification_stats
	WHERE chat_id = ? AND address = ?`,
		chatID,
		normalizedAddress,
	)
	if err := row.Scan(&snapshot.VerificationCount, &snapshot.PreviousSenderUserID, &snapshot.PreviousSenderUsername); err != nil {
		if err != sql.ErrNoRows {
			return AddressVerificationSnapshot{}, err
		}
	}
	snapshot.VerificationCount++
	nowText := verifiedAt.UTC().Format(time.RFC3339)
	if _, err := tx.Exec(`
INSERT INTO address_verification_stats (
	chat_id, address, verification_count, last_sender_user_id, last_sender_username, last_verified_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(chat_id, address) DO UPDATE SET
	verification_count = excluded.verification_count,
	last_sender_user_id = excluded.last_sender_user_id,
	last_sender_username = excluded.last_sender_username,
	last_verified_at = excluded.last_verified_at,
	updated_at = excluded.updated_at`,
		chatID,
		normalizedAddress,
		snapshot.VerificationCount,
		senderUserID,
		strings.TrimSpace(senderUsername),
		nowText,
		nowText,
	); err != nil {
		return AddressVerificationSnapshot{}, err
	}

	if err := tx.Commit(); err != nil {
		return AddressVerificationSnapshot{}, err
	}
	tx = nil
	return snapshot, nil
}

func (d *Database) TrackAddressVerificationMessage(chatID int64, messageID int, senderUserID int64, senderUsername, address string, sentAt time.Time) error {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.TrackAddressVerificationMessage(chatID, messageID, senderUserID, senderUsername, address, sentAt)
	}
	normalizedAddress := strings.TrimSpace(address)
	if chatID == 0 || messageID == 0 || normalizedAddress == "" {
		return fmt.Errorf("chatID、messageID 与 address 不能为空")
	}
	if sentAt.IsZero() {
		sentAt = time.Now().UTC()
	}
	nowText := time.Now().UTC().Format(time.RFC3339)
	sentAtText := sentAt.UTC().Format(time.RFC3339)

	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.raw.Exec(`
INSERT INTO address_verification_messages (
	chat_id, message_id, sender_user_id, sender_username, original_address, current_address,
	sent_at, first_seen_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(chat_id, message_id) DO NOTHING`,
		chatID,
		messageID,
		senderUserID,
		strings.TrimSpace(senderUsername),
		normalizedAddress,
		normalizedAddress,
		sentAtText,
		nowText,
		nowText,
	)
	return err
}

func (d *Database) MarkAddressVerificationMessageEdited(chatID int64, messageID int, editedAddress string, editedAt time.Time) error {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.MarkAddressVerificationMessageEdited(chatID, messageID, editedAddress, editedAt)
	}
	if chatID == 0 || messageID == 0 {
		return fmt.Errorf("chatID 与 messageID 不能为空")
	}
	if editedAt.IsZero() {
		editedAt = time.Now().UTC()
	}
	editedAtText := editedAt.UTC().Format(time.RFC3339)
	nowText := time.Now().UTC().Format(time.RFC3339)

	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.raw.Exec(`
UPDATE address_verification_messages
SET current_address = ?,
	edit_count = edit_count + 1,
	last_edit_at = ?,
	updated_at = ?
WHERE chat_id = ? AND message_id = ?`,
		strings.TrimSpace(editedAddress),
		editedAtText,
		nowText,
		chatID,
		messageID,
	)
	return err
}

func (d *Database) GetAddressVerificationMessage(chatID int64, messageID int) (AddressVerificationMessageRecord, bool, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.GetAddressVerificationMessage(chatID, messageID)
	}
	if chatID == 0 || messageID == 0 {
		return AddressVerificationMessageRecord{}, false, nil
	}
	row := d.raw.QueryRow(`
SELECT chat_id, message_id, sender_user_id, sender_username, original_address, current_address,
	sent_at, first_seen_at, last_edit_at, edit_count, updated_at
FROM address_verification_messages
WHERE chat_id = ? AND message_id = ?`,
		chatID,
		messageID,
	)
	var item AddressVerificationMessageRecord
	err := row.Scan(
		&item.ChatID,
		&item.MessageID,
		&item.SenderUserID,
		&item.SenderUsername,
		&item.OriginalAddress,
		&item.CurrentAddress,
		&item.SentAt,
		&item.FirstSeenAt,
		&item.LastEditAt,
		&item.EditCount,
		&item.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return AddressVerificationMessageRecord{}, false, nil
	}
	if err != nil {
		return AddressVerificationMessageRecord{}, false, err
	}
	return item, true, nil
}

func normalizePrivatePurchaseOrderLimit(limit int) int {
	if limit <= 0 {
		return defaultPrivateOrderListLimit
	}
	if limit > maxPrivateOrderListLimit {
		return maxPrivateOrderListLimit
	}
	return limit
}

func normalizePrivatePurchaseOrderStatus(status string) string {
	normalized := strings.ToLower(strings.TrimSpace(status))
	switch normalized {
	case "pending", "approved", "rejected", "canceled":
		return normalized
	default:
		return ""
	}
}

func (d *Database) AddPrivatePurchaseOrder(input PrivatePurchaseOrderInput) (int64, error) {
	if input.UserID == 0 {
		return 0, fmt.Errorf("userID 不能为空")
	}
	planName := strings.TrimSpace(input.PlanName)
	if planName == "" {
		planName = "标准套餐"
	}
	durationDays := input.DurationDays
	if durationDays <= 0 {
		durationDays = 30
	}
	currency := strings.ToUpper(strings.TrimSpace(input.Currency))
	status := normalizePrivatePurchaseOrderStatus("pending")
	note := strings.TrimSpace(input.RequestNote)
	source := strings.TrimSpace(input.Source)
	now := time.Now().UTC().Format(time.RFC3339)

	res, err := d.raw.Exec(`
INSERT INTO private_purchase_orders (
	user_id, plan_name, duration_days, amount, currency, status, request_note, source, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		input.UserID, planName, durationDays, input.Amount, currency, status, note, source, now, now,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (d *Database) GetPrivatePurchaseOrder(orderID int64) (PrivatePurchaseOrder, error) {
	if orderID == 0 {
		return PrivatePurchaseOrder{}, sql.ErrNoRows
	}
	row := d.raw.QueryRow(`
SELECT id, user_id, plan_name, duration_days, amount, currency, status, request_note, review_note, source, expires_at, reviewed_by, reviewed_at, created_at, updated_at
FROM private_purchase_orders
WHERE id = ?`, orderID)
	var item PrivatePurchaseOrder
	if err := row.Scan(
		&item.ID,
		&item.UserID,
		&item.PlanName,
		&item.DurationDays,
		&item.Amount,
		&item.Currency,
		&item.Status,
		&item.RequestNote,
		&item.ReviewNote,
		&item.Source,
		&item.ExpiresAt,
		&item.ReviewedBy,
		&item.ReviewedAt,
		&item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		return PrivatePurchaseOrder{}, err
	}
	return item, nil
}

func (d *Database) ListPrivatePurchaseOrdersByUser(userID int64, limit int) ([]PrivatePurchaseOrder, error) {
	if userID == 0 {
		return nil, nil
	}
	limit = normalizePrivatePurchaseOrderLimit(limit)
	rows, err := d.raw.Query(`
SELECT id, user_id, plan_name, duration_days, amount, currency, status, request_note, review_note, source, expires_at, reviewed_by, reviewed_at, created_at, updated_at
FROM private_purchase_orders
WHERE user_id = ?
ORDER BY id DESC
LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]PrivatePurchaseOrder, 0, limit)
	for rows.Next() {
		var item PrivatePurchaseOrder
		if err := rows.Scan(
			&item.ID,
			&item.UserID,
			&item.PlanName,
			&item.DurationDays,
			&item.Amount,
			&item.Currency,
			&item.Status,
			&item.RequestNote,
			&item.ReviewNote,
			&item.Source,
			&item.ExpiresAt,
			&item.ReviewedBy,
			&item.ReviewedAt,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (d *Database) ListPrivatePurchaseOrders(status string, limit int) ([]PrivatePurchaseOrder, error) {
	limit = normalizePrivatePurchaseOrderLimit(limit)
	status = normalizePrivatePurchaseOrderStatus(status)

	query := `
SELECT id, user_id, plan_name, duration_days, amount, currency, status, request_note, review_note, source, expires_at, reviewed_by, reviewed_at, created_at, updated_at
FROM private_purchase_orders`
	args := make([]any, 0, 2)
	if status != "" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := d.raw.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]PrivatePurchaseOrder, 0, limit)
	for rows.Next() {
		var item PrivatePurchaseOrder
		if err := rows.Scan(
			&item.ID,
			&item.UserID,
			&item.PlanName,
			&item.DurationDays,
			&item.Amount,
			&item.Currency,
			&item.Status,
			&item.RequestNote,
			&item.ReviewNote,
			&item.Source,
			&item.ExpiresAt,
			&item.ReviewedBy,
			&item.ReviewedAt,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (d *Database) ReviewPrivatePurchaseOrder(orderID int64, status, reviewNote string, reviewedBy int64, expiresAt string) error {
	if orderID == 0 {
		return fmt.Errorf("orderID 不能为空")
	}
	normalizedStatus := normalizePrivatePurchaseOrderStatus(status)
	if normalizedStatus == "" {
		return fmt.Errorf("无效状态: %s", status)
	}
	reviewedAt := ""
	if normalizedStatus != "pending" {
		reviewedAt = time.Now().UTC().Format(time.RFC3339)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := d.raw.Exec(`
UPDATE private_purchase_orders
SET status = ?, review_note = ?, reviewed_by = ?, reviewed_at = ?, expires_at = ?, updated_at = ?
WHERE id = ?`,
		normalizedStatus,
		strings.TrimSpace(reviewNote),
		reviewedBy,
		reviewedAt,
		strings.TrimSpace(expiresAt),
		now,
		orderID,
	)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}
