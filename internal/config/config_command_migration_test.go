package config

import "testing"

func TestCommandMigrationConfigDefaults(t *testing.T) {
	t.Setenv("BOT_TOKEN", "test-token")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv failed: %v", err)
	}

	if cfg.GeoGeocodeEndpoint == "" || cfg.GeoRouteEndpoint == "" {
		t.Fatalf("geo endpoint defaults must not be empty: geocode=%q route=%q", cfg.GeoGeocodeEndpoint, cfg.GeoRouteEndpoint)
	}
	if cfg.GeoTimeoutMS != 3000 {
		t.Fatalf("GeoTimeoutMS mismatch: want=3000 got=%d", cfg.GeoTimeoutMS)
	}
	if cfg.GeoRetryMax != 1 {
		t.Fatalf("GeoRetryMax mismatch: want=1 got=%d", cfg.GeoRetryMax)
	}
	if cfg.GeoBackoffMS != 200 {
		t.Fatalf("GeoBackoffMS mismatch: want=200 got=%d", cfg.GeoBackoffMS)
	}
	if cfg.GeoCacheTTLSeconds != 1800 {
		t.Fatalf("GeoCacheTTLSeconds mismatch: want=1800 got=%d", cfg.GeoCacheTTLSeconds)
	}

	if cfg.ExchangeTimeoutMS != 3000 || cfg.ExchangeRetryMax != 1 || cfg.ExchangeBackoffMS != 200 {
		t.Fatalf("exchange defaults mismatch: timeout=%d retry=%d backoff=%d", cfg.ExchangeTimeoutMS, cfg.ExchangeRetryMax, cfg.ExchangeBackoffMS)
	}
	if cfg.ExchangeMinSample != 3 {
		t.Fatalf("ExchangeMinSample mismatch: want=3 got=%d", cfg.ExchangeMinSample)
	}

	if cfg.CardTimeoutMS != 3000 || cfg.CardRetryMax != 1 || cfg.CardBackoffMS != 200 {
		t.Fatalf("card defaults mismatch: timeout=%d retry=%d backoff=%d", cfg.CardTimeoutMS, cfg.CardRetryMax, cfg.CardBackoffMS)
	}
	if cfg.CardPrimaryEndpoint != "https://www.haoshudi.com/yinhangka/" || cfg.CardPrimaryAPIHeader != "X-API-Key" || cfg.CardPrimaryTimeoutMS != 3000 {
		t.Fatalf(
			"card primary defaults mismatch: endpoint=%q header=%q timeout=%d",
			cfg.CardPrimaryEndpoint,
			cfg.CardPrimaryAPIHeader,
			cfg.CardPrimaryTimeoutMS,
		)
	}
	if cfg.CardProviderTimeoutMS != 3000 || cfg.CardAlipayTimeoutMS != 3000 || cfg.CardBinlistTimeoutMS != 3000 {
		t.Fatalf(
			"card provider timeout defaults mismatch: provider=%d alipay=%d binlist=%d",
			cfg.CardProviderTimeoutMS,
			cfg.CardAlipayTimeoutMS,
			cfg.CardBinlistTimeoutMS,
		)
	}
	if cfg.CardCacheTTLSeconds != 300 || cfg.CardCacheMaxEntries != 2048 {
		t.Fatalf("card cache defaults mismatch: ttl=%d max=%d", cfg.CardCacheTTLSeconds, cfg.CardCacheMaxEntries)
	}
	if cfg.OutboundQueueWorkers != 4 || cfg.OutboundQueueDepth != 2048 || cfg.LedgerReceiptOutboxWorkers != 4 {
		t.Fatalf(
			"outbound defaults mismatch: workers=%d depth=%d outbox_workers=%d",
			cfg.OutboundQueueWorkers,
			cfg.OutboundQueueDepth,
			cfg.LedgerReceiptOutboxWorkers,
		)
	}
	if cfg.SQLiteJournalMode != "" || cfg.SQLiteSynchronous != "" || cfg.SQLiteOptimizeOnStart || cfg.SQLiteIncrementalVacuumOnStart {
		t.Fatalf(
			"sqlite runtime defaults must be opt-in: journal=%q synchronous=%q optimize=%t vacuum=%t",
			cfg.SQLiteJournalMode,
			cfg.SQLiteSynchronous,
			cfg.SQLiteOptimizeOnStart,
			cfg.SQLiteIncrementalVacuumOnStart,
		)
	}
}

func TestCommandMigrationConfigEnvOverride(t *testing.T) {
	t.Setenv("BOT_TOKEN", "test-token")
	pairs := map[string]string{
		"GEO_GEOCODE_ENDPOINT":               "https://geo.example/geocode",
		"GEO_ROUTE_ENDPOINT":                 "https://geo.example/route",
		"GEO_TIMEOUT_MS":                     "3500",
		"GEO_RETRY_MAX":                      "2",
		"GEO_BACKOFF_MS":                     "333",
		"GEO_CACHE_TTL_SEC":                  "999",
		"EXCHANGE_TIMEOUT_MS":                "3600",
		"EXCHANGE_RETRY_MAX":                 "2",
		"EXCHANGE_BACKOFF_MS":                "444",
		"EXCHANGE_MIN_SAMPLE":                "5",
		"CARD_TIMEOUT_MS":                    "3700",
		"CARD_RETRY_MAX":                     "2",
		"CARD_BACKOFF_MS":                    "555",
		"CARD_PRIMARY_ENDPOINT":              "https://primary.example/cards",
		"CARD_PRIMARY_API_KEY":               "test-key",
		"CARD_PRIMARY_API_HEADER":            "Authorization",
		"CARD_PRIMARY_TIMEOUT_MS":            "1900",
		"CARD_PROVIDER_TIMEOUT_MS":           "2400",
		"CARD_ALIPAY_TIMEOUT_MS":             "1500",
		"CARD_BINLIST_TIMEOUT_MS":            "1300",
		"CARD_CACHE_TTL_SEC":                 "90",
		"CARD_CACHE_MAX_ENTRIES":             "321",
		"OUTBOUND_QUEUE_WORKERS":             "6",
		"OUTBOUND_QUEUE_DEPTH":               "4096",
		"LEDGER_RECEIPT_OUTBOX_WORKERS":      "5",
		"SQLITE_JOURNAL_MODE":                "WAL",
		"SQLITE_SYNCHRONOUS":                 "NORMAL",
		"SQLITE_OPTIMIZE_ON_START":           "1",
		"SQLITE_INCREMENTAL_VACUUM_ON_START": "true",
	}
	for key, value := range pairs {
		t.Setenv(key, value)
	}

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv failed: %v", err)
	}

	if cfg.GeoGeocodeEndpoint != pairs["GEO_GEOCODE_ENDPOINT"] {
		t.Fatalf("GeoGeocodeEndpoint mismatch: got=%q", cfg.GeoGeocodeEndpoint)
	}
	if cfg.GeoRouteEndpoint != pairs["GEO_ROUTE_ENDPOINT"] {
		t.Fatalf("GeoRouteEndpoint mismatch: got=%q", cfg.GeoRouteEndpoint)
	}
	if cfg.GeoTimeoutMS != 3500 || cfg.GeoRetryMax != 2 || cfg.GeoBackoffMS != 333 || cfg.GeoCacheTTLSeconds != 999 {
		t.Fatalf("geo overrides mismatch: %+v", cfg)
	}
	if cfg.ExchangeTimeoutMS != 3600 || cfg.ExchangeRetryMax != 2 || cfg.ExchangeBackoffMS != 444 || cfg.ExchangeMinSample != 5 {
		t.Fatalf("exchange overrides mismatch: %+v", cfg)
	}
	if cfg.CardTimeoutMS != 3700 || cfg.CardRetryMax != 2 || cfg.CardBackoffMS != 555 {
		t.Fatalf("card overrides mismatch: %+v", cfg)
	}
	if cfg.CardPrimaryEndpoint != "https://primary.example/cards" || cfg.CardPrimaryAPIKey != "test-key" || cfg.CardPrimaryAPIHeader != "Authorization" || cfg.CardPrimaryTimeoutMS != 1900 {
		t.Fatalf("card primary overrides mismatch: %+v", cfg)
	}
	if cfg.CardProviderTimeoutMS != 2400 || cfg.CardAlipayTimeoutMS != 1500 || cfg.CardBinlistTimeoutMS != 1300 {
		t.Fatalf("card provider timeout overrides mismatch: %+v", cfg)
	}
	if cfg.CardCacheTTLSeconds != 90 || cfg.CardCacheMaxEntries != 321 {
		t.Fatalf("card cache overrides mismatch: %+v", cfg)
	}
	if cfg.OutboundQueueWorkers != 6 || cfg.OutboundQueueDepth != 4096 || cfg.LedgerReceiptOutboxWorkers != 5 {
		t.Fatalf("outbound overrides mismatch: %+v", cfg)
	}
	if cfg.SQLiteJournalMode != "WAL" || cfg.SQLiteSynchronous != "NORMAL" || !cfg.SQLiteOptimizeOnStart || !cfg.SQLiteIncrementalVacuumOnStart {
		t.Fatalf(
			"sqlite runtime overrides mismatch: journal=%q synchronous=%q optimize=%t vacuum=%t",
			cfg.SQLiteJournalMode,
			cfg.SQLiteSynchronous,
			cfg.SQLiteOptimizeOnStart,
			cfg.SQLiteIncrementalVacuumOnStart,
		)
	}
}
