package config

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	BotToken                       string
	DBPath                         string
	DBMasterPath                   string
	DBSlavePath                    string
	DBTenantShards                 int
	DBWaitTimeoutMS                int
	DBDeadlockRetryMax             int
	DBDeadlockBackoffMS            int
	DBDeadlockJitterMS             int
	DBDataEncryptionKey            string
	SQLiteJournalMode              string
	SQLiteSynchronous              string
	SQLiteOptimizeOnStart          bool
	SQLiteIncrementalVacuumOnStart bool
	HotAccountUserIDs              []int64
	HotAccountBatchSize            int
	HotAccountFlushMS              int
	ColdArchivePath                string
	HotDataRetentionDays           int
	ColdArchiveSweepMinutes        int
	ColdArchiveBatchSize           int
	PGWALSourceDir                 string
	PGWALArchiveDir                string
	PGWALArchiveScanSec            int
	CrossRegionDRTargetDir         string
	CrossRegionDRSyncSec           int
	TimescaleDSN                   string
	ElasticsearchURL               string
	ElasticsearchIndex             string
	ElasticsearchUsername          string
	ElasticsearchPassword          string
	ElasticsearchAPIKey            string
	ElasticsearchTimeoutSec        int
	Timezone                       string
	LogLevel                       string
	ReplayWindowSeconds            int
	LargeTxnThreshold              float64
	WorkHoursStart                 string
	WorkHoursEnd                   string
	AdminAddr                      string
	AdminToken                     string
	AdminUsername                  string
	AdminPasswordHash              string
	AdminTOTPSecret                string
	AdminSetupToken                string
	PollStatusPath                 string
	PollStartupGrace               time.Duration
	PollMaxSuccessAge              time.Duration
	PollMinFailureCount            int
	ReleaseID                      string
	ReleaseSHA256                  string
	SupportChatID                  int64
	SupportURL                     string
	PrivateTrialDays               int
	TGWebAppURL                    string
	TGWebAppSecret                 string
	BankBranchAPIURL               string
	BankBranchAPIToken             string
	BankBranchAPIHeader            string
	BankBranchAPITimeoutSec        int
	GeoGeocodeEndpoint             string
	GeoRouteEndpoint               string
	GeoTimeoutMS                   int
	GeoRetryMax                    int
	GeoBackoffMS                   int
	GeoCacheTTLSeconds             int
	ExchangeTimeoutMS              int
	ExchangeRetryMax               int
	ExchangeBackoffMS              int
	ExchangeMinSample              int
	CardTimeoutMS                  int
	CardRetryMax                   int
	CardBackoffMS                  int
	CardPrimaryEndpoint            string
	CardPrimaryAPIKey              string
	CardPrimaryAPIHeader           string
	CardPrimaryTimeoutMS           int
	CardProviderTimeoutMS          int
	CardAlipayTimeoutMS            int
	CardBinlistTimeoutMS           int
	CardCacheTTLSeconds            int
	CardCacheMaxEntries            int
	CacheL1Enabled                 bool
	CacheL1TTLSeconds              int
	CacheL1MaxEntries              int
	CacheL2TTLSeconds              int
	CacheRedisAddr                 string
	CacheRedisPass                 string
	CacheRedisDB                   int
	StateBroadcastRedisAddr        string
	StateBroadcastRedisPass        string
	StateBroadcastRedisDB          int
	StateBroadcastChannel          string
	WebhookAddr                    string
	WebhookPath                    string
	WebhookSecretToken             string
	WebhookIPBlacklist             []string
	OutboundQueueWorkers           int
	OutboundQueueDepth             int
	LedgerReceiptOutboxWorkers     int

	AddressVerifyMediaMode          string
	AddressVerifyMediaDir           string
	AddressVerifyMediaPublicBaseURL string
	AddressVerifyMediaTTL           time.Duration

	MediaS3Endpoint       string
	MediaS3Region         string
	MediaS3Bucket         string
	MediaS3AccessKey      string
	MediaS3SecretKey      string
	MediaS3Prefix         string
	MediaS3PublicBaseURL  string
	MediaHandlerWorkers   int
	MediaHandlerQueueSize int
	MediaHandlerMaxBytes  int64
}

func LoadFromEnv() (Config, error) {
	pollStatusPath := strings.TrimSpace(os.Getenv("POLL_STATUS_PATH"))
	pollStartupGrace := parseDurationWithDefault(os.Getenv("POLL_STARTUP_GRACE"), 90*time.Second)
	pollMaxSuccessAge := parseDurationWithDefault(os.Getenv("POLL_MAX_SUCCESS_AGE"), 90*time.Second)
	pollMinFailureCount, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("POLL_MIN_FAILURE_COUNT")))
	if pollMinFailureCount <= 0 {
		pollMinFailureCount = 3
	}
	dbTenantShards, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("DB_TENANT_SHARDS")))
	if dbTenantShards <= 0 {
		dbTenantShards = 1
	}
	dbWaitTimeoutMS, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("DB_WAIT_TIMEOUT_MS")))
	if dbWaitTimeoutMS <= 0 {
		dbWaitTimeoutMS = 1000
	}
	dbDeadlockRetryMax, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("DB_DEADLOCK_RETRY_MAX")))
	if dbDeadlockRetryMax <= 0 {
		dbDeadlockRetryMax = 3
	}
	dbDeadlockBackoffMS, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("DB_DEADLOCK_BACKOFF_MS")))
	if dbDeadlockBackoffMS <= 0 {
		dbDeadlockBackoffMS = 50
	}
	dbDeadlockJitterMS, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("DB_DEADLOCK_JITTER_MS")))
	if dbDeadlockJitterMS <= 0 {
		dbDeadlockJitterMS = 25
	}
	sqliteJournalMode := strings.ToUpper(strings.TrimSpace(os.Getenv("SQLITE_JOURNAL_MODE")))
	sqliteSynchronous := strings.ToUpper(strings.TrimSpace(os.Getenv("SQLITE_SYNCHRONOUS")))
	sqliteOptimizeOnStart := parseBool(os.Getenv("SQLITE_OPTIMIZE_ON_START"))
	sqliteIncrementalVacuumOnStart := parseBool(os.Getenv("SQLITE_INCREMENTAL_VACUUM_ON_START"))
	hotAccountUserIDs := parseUserIDsCSV(os.Getenv("HOT_ACCOUNT_USER_IDS"))
	hotAccountBatchSize, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("HOT_ACCOUNT_BATCH_SIZE")))
	if len(hotAccountUserIDs) > 0 && hotAccountBatchSize <= 0 {
		hotAccountBatchSize = 8
	}
	hotAccountFlushMS, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("HOT_ACCOUNT_FLUSH_MS")))
	if len(hotAccountUserIDs) > 0 && hotAccountFlushMS <= 0 {
		hotAccountFlushMS = 250
	}
	coldArchivePath := strings.TrimSpace(os.Getenv("COLD_ARCHIVE_PATH"))
	hotDataRetentionDays, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("HOT_DATA_RETENTION_DAYS")))
	if coldArchivePath != "" && hotDataRetentionDays <= 0 {
		hotDataRetentionDays = 90
	}
	coldArchiveSweepMinutes, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("COLD_ARCHIVE_SWEEP_MINUTES")))
	if coldArchivePath != "" && coldArchiveSweepMinutes <= 0 {
		coldArchiveSweepMinutes = 60
	}
	coldArchiveBatchSize, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("COLD_ARCHIVE_BATCH_SIZE")))
	if coldArchivePath != "" && coldArchiveBatchSize <= 0 {
		coldArchiveBatchSize = 500
	}
	pgWALSourceDir := strings.TrimSpace(os.Getenv("PG_WAL_SOURCE_DIR"))
	pgWALArchiveDir := strings.TrimSpace(os.Getenv("PG_WAL_ARCHIVE_DIR"))
	pgWALArchiveScanSec, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("PG_WAL_ARCHIVE_SCAN_SEC")))
	if pgWALSourceDir != "" && pgWALArchiveDir != "" && pgWALArchiveScanSec <= 0 {
		pgWALArchiveScanSec = 2
	}
	crossRegionDRTargetDir := strings.TrimSpace(os.Getenv("CROSS_REGION_DR_TARGET_DIR"))
	crossRegionDRSyncSec, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("CROSS_REGION_DR_SYNC_SEC")))
	if crossRegionDRSyncSec <= 0 {
		crossRegionDRSyncSec = 30
	}
	branchTimeout, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("BANK_BRANCH_API_TIMEOUT_SEC")))
	if branchTimeout <= 0 {
		branchTimeout = 8
	}
	geoGeocodeEndpoint := strings.TrimSpace(os.Getenv("GEO_GEOCODE_ENDPOINT"))
	if geoGeocodeEndpoint == "" {
		geoGeocodeEndpoint = "https://nominatim.openstreetmap.org/search"
	}
	geoRouteEndpoint := strings.TrimSpace(os.Getenv("GEO_ROUTE_ENDPOINT"))
	if geoRouteEndpoint == "" {
		geoRouteEndpoint = "https://router.project-osrm.org/route/v1/driving"
	}
	geoTimeoutMS, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("GEO_TIMEOUT_MS")))
	if geoTimeoutMS <= 0 {
		geoTimeoutMS = 3000
	}
	geoRetryMax, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("GEO_RETRY_MAX")))
	if geoRetryMax < 0 {
		geoRetryMax = 0
	}
	if geoRetryMax == 0 {
		geoRetryMax = 1
	}
	geoBackoffMS, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("GEO_BACKOFF_MS")))
	if geoBackoffMS <= 0 {
		geoBackoffMS = 200
	}
	geoCacheTTLSeconds, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("GEO_CACHE_TTL_SEC")))
	if geoCacheTTLSeconds <= 0 {
		geoCacheTTLSeconds = 1800
	}
	exchangeTimeoutMS, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("EXCHANGE_TIMEOUT_MS")))
	if exchangeTimeoutMS <= 0 {
		exchangeTimeoutMS = 3000
	}
	exchangeRetryMax, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("EXCHANGE_RETRY_MAX")))
	if exchangeRetryMax < 0 {
		exchangeRetryMax = 0
	}
	if exchangeRetryMax == 0 {
		exchangeRetryMax = 1
	}
	exchangeBackoffMS, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("EXCHANGE_BACKOFF_MS")))
	if exchangeBackoffMS <= 0 {
		exchangeBackoffMS = 200
	}
	exchangeMinSample, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("EXCHANGE_MIN_SAMPLE")))
	if exchangeMinSample <= 0 {
		exchangeMinSample = 3
	}
	cardTimeoutMS, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("CARD_TIMEOUT_MS")))
	if cardTimeoutMS <= 0 {
		cardTimeoutMS = 3000
	}
	cardRetryMax, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("CARD_RETRY_MAX")))
	if cardRetryMax < 0 {
		cardRetryMax = 0
	}
	if cardRetryMax == 0 {
		cardRetryMax = 1
	}
	cardBackoffMS, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("CARD_BACKOFF_MS")))
	if cardBackoffMS <= 0 {
		cardBackoffMS = 200
	}
	cardPrimaryEndpoint := strings.TrimSpace(os.Getenv("CARD_PRIMARY_ENDPOINT"))
	if cardPrimaryEndpoint == "" {
		cardPrimaryEndpoint = "https://www.haoshudi.com/yinhangka/"
	}
	cardPrimaryAPIKey := strings.TrimSpace(os.Getenv("CARD_PRIMARY_API_KEY"))
	cardPrimaryAPIHeader := strings.TrimSpace(os.Getenv("CARD_PRIMARY_API_HEADER"))
	if cardPrimaryAPIHeader == "" {
		cardPrimaryAPIHeader = "X-API-Key"
	}
	cardPrimaryTimeoutMS, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("CARD_PRIMARY_TIMEOUT_MS")))
	cardProviderTimeoutMS, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("CARD_PROVIDER_TIMEOUT_MS")))
	if cardProviderTimeoutMS <= 0 {
		cardProviderTimeoutMS = cardTimeoutMS
	}
	if cardPrimaryTimeoutMS <= 0 {
		cardPrimaryTimeoutMS = cardProviderTimeoutMS
	}
	cardAlipayTimeoutMS, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("CARD_ALIPAY_TIMEOUT_MS")))
	if cardAlipayTimeoutMS <= 0 {
		cardAlipayTimeoutMS = cardProviderTimeoutMS
	}
	cardBinlistTimeoutMS, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("CARD_BINLIST_TIMEOUT_MS")))
	if cardBinlistTimeoutMS <= 0 {
		cardBinlistTimeoutMS = cardProviderTimeoutMS
	}
	cardCacheTTLSeconds, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("CARD_CACHE_TTL_SEC")))
	if cardCacheTTLSeconds <= 0 {
		cardCacheTTLSeconds = 300
	}
	cardCacheMaxEntries, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("CARD_CACHE_MAX_ENTRIES")))
	if cardCacheMaxEntries <= 0 {
		cardCacheMaxEntries = 2048
	}
	stateBroadcastRedisDB, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("STATE_BROADCAST_REDIS_DB")))
	if stateBroadcastRedisDB < 0 {
		stateBroadcastRedisDB = 0
	}
	cacheL1Enabled := true
	if raw := strings.TrimSpace(os.Getenv("CACHE_L1_ENABLED")); raw != "" {
		cacheL1Enabled = parseBool(raw)
	}
	cacheL1TTLSeconds, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("CACHE_L1_TTL_SEC")))
	if cacheL1TTLSeconds <= 0 {
		cacheL1TTLSeconds = 30
	}
	cacheL1MaxEntries, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("CACHE_L1_MAX_ENTRIES")))
	if cacheL1MaxEntries <= 0 {
		cacheL1MaxEntries = 4096
	}
	cacheL2TTLSeconds, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("CACHE_L2_TTL_SEC")))
	if cacheL2TTLSeconds <= 0 {
		cacheL2TTLSeconds = 300
	}
	cacheRedisDB := stateBroadcastRedisDB
	if raw := strings.TrimSpace(os.Getenv("CACHE_REDIS_DB")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
			cacheRedisDB = parsed
		}
	}
	cacheRedisAddr := strings.TrimSpace(os.Getenv("CACHE_REDIS_ADDR"))
	if cacheRedisAddr == "" {
		cacheRedisAddr = strings.TrimSpace(os.Getenv("STATE_BROADCAST_REDIS_ADDR"))
	}
	cacheRedisPass := strings.TrimSpace(os.Getenv("CACHE_REDIS_PASSWORD"))
	if cacheRedisPass == "" {
		cacheRedisPass = strings.TrimSpace(os.Getenv("STATE_BROADCAST_REDIS_PASSWORD"))
	}
	stateBroadcastChannel := strings.TrimSpace(os.Getenv("STATE_BROADCAST_CHANNEL_PREFIX"))
	if stateBroadcastChannel == "" {
		stateBroadcastChannel = "channel:group:"
	}
	webhookPath := strings.TrimSpace(os.Getenv("WEBHOOK_PATH"))
	if webhookPath == "" {
		webhookPath = "/tg/webhook"
	}
	if !strings.HasPrefix(webhookPath, "/") {
		webhookPath = "/" + webhookPath
	}
	webhookIPBlacklist := parseStringCSV(os.Getenv("WEBHOOK_IP_BLACKLIST"))
	outboundQueueWorkers, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("OUTBOUND_QUEUE_WORKERS")))
	if outboundQueueWorkers <= 0 {
		outboundQueueWorkers = 4
	}
	outboundQueueDepth, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("OUTBOUND_QUEUE_DEPTH")))
	if outboundQueueDepth <= 0 {
		outboundQueueDepth = 2048
	}
	ledgerReceiptOutboxWorkers, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("LEDGER_RECEIPT_OUTBOX_WORKERS")))
	if ledgerReceiptOutboxWorkers <= 0 {
		ledgerReceiptOutboxWorkers = 4
	}
	mediaHandlerWorkers, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("MEDIA_HANDLER_WORKERS")))
	if mediaHandlerWorkers <= 0 {
		mediaHandlerWorkers = 2
	}
	mediaHandlerQueueSize, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("MEDIA_HANDLER_QUEUE_SIZE")))
	if mediaHandlerQueueSize <= 0 {
		mediaHandlerQueueSize = 256
	}
	mediaHandlerMaxBytes, _ := strconv.ParseInt(strings.TrimSpace(os.Getenv("MEDIA_HANDLER_MAX_BYTES")), 10, 64)
	if mediaHandlerMaxBytes <= 0 {
		mediaHandlerMaxBytes = 32 * 1024 * 1024
	}
	addressVerifyMediaMode := strings.ToLower(strings.TrimSpace(os.Getenv("ADDRESS_VERIFY_MEDIA_MODE")))
	if addressVerifyMediaMode == "" {
		addressVerifyMediaMode = "multipart"
	}
	addressVerifyMediaTTL := time.Hour
	if rawTTL := strings.TrimSpace(os.Getenv("ADDRESS_VERIFY_MEDIA_TTL")); rawTTL != "" {
		if parsedTTL, err := time.ParseDuration(rawTTL); err == nil && parsedTTL > 0 {
			addressVerifyMediaTTL = parsedTTL
		}
	}
	elasticsearchTimeoutSec, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("ELASTICSEARCH_TIMEOUT_SEC")))
	if elasticsearchTimeoutSec <= 0 {
		elasticsearchTimeoutSec = 3
	}
	replayWindowSeconds, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("REPLAY_WINDOW_SECONDS")))
	if replayWindowSeconds <= 0 {
		replayWindowSeconds = 60
	}
	largeTxnThreshold, _ := strconv.ParseFloat(strings.TrimSpace(os.Getenv("LARGE_TXN_THRESHOLD")), 64)
	if largeTxnThreshold <= 0 {
		largeTxnThreshold = 50000
	}
	workHoursStart := strings.TrimSpace(os.Getenv("WORK_HOURS_START"))
	workHoursEnd := strings.TrimSpace(os.Getenv("WORK_HOURS_END"))
	trialDays, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("PRIVATE_TRIAL_DAYS")))
	if trialDays <= 0 {
		trialDays = 3
	}
	supportChatID, _ := strconv.ParseInt(strings.TrimSpace(os.Getenv("SUPPORT_CHAT_ID")), 10, 64)
	if supportChatID == 0 {
		supportChatID, _ = strconv.ParseInt(strings.TrimSpace(os.Getenv("TEST_USER_ID")), 10, 64)
	}
	dbPath := strings.TrimSpace(os.Getenv("DB_PATH"))
	dbMasterPath := strings.TrimSpace(os.Getenv("DB_MASTER_PATH"))
	if dbMasterPath == "" {
		dbMasterPath = dbPath
	}
	dbSlavePath := strings.TrimSpace(os.Getenv("DB_SLAVE_PATH"))
	if dbSlavePath == "" {
		dbSlavePath = dbMasterPath
	}
	cfg := Config{
		BotToken:                       strings.TrimSpace(os.Getenv("BOT_TOKEN")),
		DBPath:                         dbPath,
		DBMasterPath:                   dbMasterPath,
		DBSlavePath:                    dbSlavePath,
		DBTenantShards:                 dbTenantShards,
		DBWaitTimeoutMS:                dbWaitTimeoutMS,
		DBDeadlockRetryMax:             dbDeadlockRetryMax,
		DBDeadlockBackoffMS:            dbDeadlockBackoffMS,
		DBDeadlockJitterMS:             dbDeadlockJitterMS,
		DBDataEncryptionKey:            strings.TrimSpace(os.Getenv("DB_DATA_ENCRYPTION_KEY")),
		SQLiteJournalMode:              sqliteJournalMode,
		SQLiteSynchronous:              sqliteSynchronous,
		SQLiteOptimizeOnStart:          sqliteOptimizeOnStart,
		SQLiteIncrementalVacuumOnStart: sqliteIncrementalVacuumOnStart,
		HotAccountUserIDs:              hotAccountUserIDs,
		HotAccountBatchSize:            hotAccountBatchSize,
		HotAccountFlushMS:              hotAccountFlushMS,
		ColdArchivePath:                coldArchivePath,
		HotDataRetentionDays:           hotDataRetentionDays,
		ColdArchiveSweepMinutes:        coldArchiveSweepMinutes,
		ColdArchiveBatchSize:           coldArchiveBatchSize,
		PGWALSourceDir:                 pgWALSourceDir,
		PGWALArchiveDir:                pgWALArchiveDir,
		PGWALArchiveScanSec:            pgWALArchiveScanSec,
		CrossRegionDRTargetDir:         crossRegionDRTargetDir,
		CrossRegionDRSyncSec:           crossRegionDRSyncSec,
		TimescaleDSN:                   strings.TrimSpace(os.Getenv("TIMESCALE_DSN")),
		ElasticsearchURL:               strings.TrimSpace(os.Getenv("ELASTICSEARCH_URL")),
		ElasticsearchIndex:             strings.TrimSpace(os.Getenv("ELASTICSEARCH_INDEX")),
		ElasticsearchUsername:          strings.TrimSpace(os.Getenv("ELASTICSEARCH_USERNAME")),
		ElasticsearchPassword:          strings.TrimSpace(os.Getenv("ELASTICSEARCH_PASSWORD")),
		ElasticsearchAPIKey:            strings.TrimSpace(os.Getenv("ELASTICSEARCH_API_KEY")),
		ElasticsearchTimeoutSec:        elasticsearchTimeoutSec,
		Timezone:                       strings.TrimSpace(os.Getenv("BOT_TIMEZONE")),
		LogLevel:                       strings.ToUpper(strings.TrimSpace(os.Getenv("LOG_LEVEL"))),
		ReplayWindowSeconds:            replayWindowSeconds,
		LargeTxnThreshold:              largeTxnThreshold,
		WorkHoursStart:                 workHoursStart,
		WorkHoursEnd:                   workHoursEnd,
		AdminAddr:                      strings.TrimSpace(os.Getenv("ADMIN_ADDR")),
		AdminToken:                     strings.TrimSpace(os.Getenv("ADMIN_TOKEN")),
		AdminUsername:                  strings.TrimSpace(os.Getenv("ADMIN_USERNAME")),
		AdminPasswordHash:              strings.TrimSpace(os.Getenv("ADMIN_PASSWORD_HASH")),
		AdminTOTPSecret:                strings.TrimSpace(os.Getenv("ADMIN_TOTP_SECRET")),
		AdminSetupToken:                strings.TrimSpace(os.Getenv("ADMIN_SETUP_TOKEN")),
		PollStatusPath:                 pollStatusPath,
		PollStartupGrace:               pollStartupGrace,
		PollMaxSuccessAge:              pollMaxSuccessAge,
		PollMinFailureCount:            pollMinFailureCount,
		ReleaseID:                      strings.TrimSpace(os.Getenv("RELEASE_ID")),
		ReleaseSHA256:                  strings.TrimSpace(os.Getenv("RELEASE_SHA256")),
		SupportChatID:                  supportChatID,
		SupportURL:                     strings.TrimSpace(os.Getenv("SUPPORT_URL")),
		PrivateTrialDays:               trialDays,
		TGWebAppURL:                    strings.TrimSpace(os.Getenv("TG_WEBAPP_URL")),
		TGWebAppSecret:                 strings.TrimSpace(os.Getenv("TG_WEBAPP_SECRET")),
		BankBranchAPIURL:               strings.TrimSpace(os.Getenv("BANK_BRANCH_API_URL")),
		BankBranchAPIToken:             strings.TrimSpace(os.Getenv("BANK_BRANCH_API_TOKEN")),
		BankBranchAPIHeader:            strings.TrimSpace(os.Getenv("BANK_BRANCH_API_HEADER")),
		BankBranchAPITimeoutSec:        branchTimeout,
		GeoGeocodeEndpoint:             geoGeocodeEndpoint,
		GeoRouteEndpoint:               geoRouteEndpoint,
		GeoTimeoutMS:                   geoTimeoutMS,
		GeoRetryMax:                    geoRetryMax,
		GeoBackoffMS:                   geoBackoffMS,
		GeoCacheTTLSeconds:             geoCacheTTLSeconds,
		ExchangeTimeoutMS:              exchangeTimeoutMS,
		ExchangeRetryMax:               exchangeRetryMax,
		ExchangeBackoffMS:              exchangeBackoffMS,
		ExchangeMinSample:              exchangeMinSample,
		CardTimeoutMS:                  cardTimeoutMS,
		CardRetryMax:                   cardRetryMax,
		CardBackoffMS:                  cardBackoffMS,
		CardPrimaryEndpoint:            cardPrimaryEndpoint,
		CardPrimaryAPIKey:              cardPrimaryAPIKey,
		CardPrimaryAPIHeader:           cardPrimaryAPIHeader,
		CardPrimaryTimeoutMS:           cardPrimaryTimeoutMS,
		CardProviderTimeoutMS:          cardProviderTimeoutMS,
		CardAlipayTimeoutMS:            cardAlipayTimeoutMS,
		CardBinlistTimeoutMS:           cardBinlistTimeoutMS,
		CardCacheTTLSeconds:            cardCacheTTLSeconds,
		CardCacheMaxEntries:            cardCacheMaxEntries,
		CacheL1Enabled:                 cacheL1Enabled,
		CacheL1TTLSeconds:              cacheL1TTLSeconds,
		CacheL1MaxEntries:              cacheL1MaxEntries,
		CacheL2TTLSeconds:              cacheL2TTLSeconds,
		CacheRedisAddr:                 cacheRedisAddr,
		CacheRedisPass:                 cacheRedisPass,
		CacheRedisDB:                   cacheRedisDB,
		StateBroadcastRedisAddr:        strings.TrimSpace(os.Getenv("STATE_BROADCAST_REDIS_ADDR")),
		StateBroadcastRedisPass:        strings.TrimSpace(os.Getenv("STATE_BROADCAST_REDIS_PASSWORD")),
		StateBroadcastRedisDB:          stateBroadcastRedisDB,
		StateBroadcastChannel:          stateBroadcastChannel,
		WebhookAddr:                    strings.TrimSpace(os.Getenv("WEBHOOK_ADDR")),
		WebhookPath:                    webhookPath,
		WebhookSecretToken:             strings.TrimSpace(os.Getenv("WEBHOOK_SECRET_TOKEN")),
		WebhookIPBlacklist:             webhookIPBlacklist,
		OutboundQueueWorkers:           outboundQueueWorkers,
		OutboundQueueDepth:             outboundQueueDepth,
		LedgerReceiptOutboxWorkers:     ledgerReceiptOutboxWorkers,

		AddressVerifyMediaMode:          addressVerifyMediaMode,
		AddressVerifyMediaDir:           strings.TrimSpace(os.Getenv("ADDRESS_VERIFY_MEDIA_DIR")),
		AddressVerifyMediaPublicBaseURL: strings.TrimSpace(os.Getenv("ADDRESS_VERIFY_MEDIA_PUBLIC_BASE_URL")),
		AddressVerifyMediaTTL:           addressVerifyMediaTTL,

		MediaS3Endpoint:       strings.TrimSpace(os.Getenv("MEDIA_S3_ENDPOINT")),
		MediaS3Region:         strings.TrimSpace(os.Getenv("MEDIA_S3_REGION")),
		MediaS3Bucket:         strings.TrimSpace(os.Getenv("MEDIA_S3_BUCKET")),
		MediaS3AccessKey:      strings.TrimSpace(os.Getenv("MEDIA_S3_ACCESS_KEY")),
		MediaS3SecretKey:      strings.TrimSpace(os.Getenv("MEDIA_S3_SECRET_KEY")),
		MediaS3Prefix:         strings.TrimSpace(os.Getenv("MEDIA_S3_PREFIX")),
		MediaS3PublicBaseURL:  strings.TrimSpace(os.Getenv("MEDIA_S3_PUBLIC_BASE_URL")),
		MediaHandlerWorkers:   mediaHandlerWorkers,
		MediaHandlerQueueSize: mediaHandlerQueueSize,
		MediaHandlerMaxBytes:  mediaHandlerMaxBytes,
	}
	if cfg.BotToken == "" {
		return Config{}, errors.New("缺少 BOT_TOKEN 环境变量")
	}
	if cfg.DBPath == "" {
		cfg.DBPath = filepath.FromSlash("data/bot_go.db")
	}
	if cfg.PollStatusPath == "" {
		cfg.PollStatusPath = filepath.FromSlash("data/recovery/poll-status.json")
	}
	if cfg.DBMasterPath == "" {
		cfg.DBMasterPath = cfg.DBPath
	}
	if cfg.DBSlavePath == "" {
		cfg.DBSlavePath = cfg.DBMasterPath
	}
	if cfg.Timezone == "" {
		cfg.Timezone = "Asia/Shanghai"
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "INFO"
	}
	if cfg.AdminAddr == "" {
		cfg.AdminAddr = ":8090"
	}
	if cfg.BankBranchAPIHeader == "" {
		cfg.BankBranchAPIHeader = "Authorization"
	}
	if cfg.ElasticsearchURL != "" && cfg.ElasticsearchIndex == "" {
		cfg.ElasticsearchIndex = "ledger_entries"
	}
	return cfg, nil
}

func parseBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "on", "enable", "enabled":
		return true
	default:
		return false
	}
}

func parseDurationWithDefault(raw string, fallback time.Duration) time.Duration {
	parsed, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func parseStringCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	if len(parts) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(parts))
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func parseUserIDsCSV(raw string) []int64 {
	parts := strings.Split(raw, ",")
	if len(parts) == 0 {
		return nil
	}
	seen := make(map[int64]struct{}, len(parts))
	out := make([]int64, 0, len(parts))
	for _, part := range parts {
		idText := strings.TrimSpace(part)
		if idText == "" {
			continue
		}
		userID, err := strconv.ParseInt(idText, 10, 64)
		if err != nil || userID <= 0 {
			continue
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		out = append(out, userID)
	}
	return out
}
