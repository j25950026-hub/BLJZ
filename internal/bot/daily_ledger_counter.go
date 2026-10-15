package bot

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"go-bot/internal/utils"
)

const (
	defaultDailyLedgerCounterKeyPrefix = "ledger:daily:"
	defaultDailyLedgerCounterTTL       = 72 * time.Hour
	defaultDailyLedgerCounterTimeout   = 2 * time.Second

	dailyLedgerFieldDepositTotal = "deposit_total"
	dailyLedgerFieldPayoutTotal  = "payout_total"
	dailyLedgerFieldDepositCount = "deposit_count"
	dailyLedgerFieldPayoutCount  = "payout_count"
)

type DailyLedgerCounter interface {
	Incr(ctx context.Context, chatID int64, periodKey, entryType string, finalAmount, rate float64) error
	Snapshot(ctx context.Context, chatID int64, periodKey string) (ledgerSummary, bool, error)
	RateStats(ctx context.Context, chatID int64, periodKey, entryType string) (map[string]float64, bool, error)
	Replace(
		ctx context.Context,
		chatID int64,
		periodKey string,
		summary ledgerSummary,
		depositRates map[string]float64,
		payoutRates map[string]float64,
	) error
	Close() error
}

type noopDailyLedgerCounter struct{}

func (noopDailyLedgerCounter) Incr(context.Context, int64, string, string, float64, float64) error {
	return nil
}

func (noopDailyLedgerCounter) Snapshot(context.Context, int64, string) (ledgerSummary, bool, error) {
	return ledgerSummary{}, false, nil
}

func (noopDailyLedgerCounter) RateStats(context.Context, int64, string, string) (map[string]float64, bool, error) {
	return nil, false, nil
}

func (noopDailyLedgerCounter) Replace(context.Context, int64, string, ledgerSummary, map[string]float64, map[string]float64) error {
	return nil
}

func (noopDailyLedgerCounter) Close() error {
	return nil
}

type RedisDailyLedgerCounterConfig struct {
	Addr      string
	Password  string
	DB        int
	KeyPrefix string
	TTL       time.Duration
	Timeout   time.Duration
}

type RedisDailyLedgerCounter struct {
	client    *redis.Client
	keyPrefix string
	ttl       time.Duration
	timeout   time.Duration
}

func NewRedisDailyLedgerCounter(cfg RedisDailyLedgerCounterConfig) (*RedisDailyLedgerCounter, error) {
	addr := strings.TrimSpace(cfg.Addr)
	if addr == "" {
		return nil, fmt.Errorf("redis 地址不能为空")
	}
	keyPrefix := strings.TrimSpace(cfg.KeyPrefix)
	if keyPrefix == "" {
		keyPrefix = defaultDailyLedgerCounterKeyPrefix
	}
	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = defaultDailyLedgerCounterTTL
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultDailyLedgerCounterTimeout
	}

	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: strings.TrimSpace(cfg.Password),
		DB:       cfg.DB,
	})
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, err
	}
	return &RedisDailyLedgerCounter{
		client:    client,
		keyPrefix: keyPrefix,
		ttl:       ttl,
		timeout:   timeout,
	}, nil
}

func (r *RedisDailyLedgerCounter) Incr(ctx context.Context, chatID int64, periodKey, entryType string, finalAmount, rate float64) error {
	if r == nil || r.client == nil || chatID == 0 {
		return nil
	}
	period := normalizeDailyLedgerPeriodKey(periodKey)
	if period == "" {
		return nil
	}
	kind := normalizeDailyLedgerEntryType(entryType)
	totalField, countField := dailyLedgerSummaryFields(kind)
	if totalField == "" {
		return nil
	}
	rateKey := r.rateKey(chatID, period, kind)
	summaryKey := r.summaryKey(chatID, period)
	depositRateKey := r.rateKey(chatID, period, "deposit")
	payoutRateKey := r.rateKey(chatID, period, "payout")
	rateField := normalizeDailyLedgerRateField(rate)

	runCtx, cancel := r.withTimeout(ctx)
	defer cancel()

	pipe := r.client.TxPipeline()
	pipe.HIncrByFloat(runCtx, summaryKey, totalField, finalAmount)
	pipe.HIncrBy(runCtx, summaryKey, countField, 1)
	pipe.HIncrByFloat(runCtx, rateKey, rateField, finalAmount)
	pipe.Expire(runCtx, summaryKey, r.ttl)
	pipe.Expire(runCtx, depositRateKey, r.ttl)
	pipe.Expire(runCtx, payoutRateKey, r.ttl)
	_, err := pipe.Exec(runCtx)
	return err
}

func (r *RedisDailyLedgerCounter) Snapshot(ctx context.Context, chatID int64, periodKey string) (ledgerSummary, bool, error) {
	if r == nil || r.client == nil || chatID == 0 {
		return ledgerSummary{}, false, nil
	}
	period := normalizeDailyLedgerPeriodKey(periodKey)
	if period == "" {
		return ledgerSummary{}, false, nil
	}

	runCtx, cancel := r.withTimeout(ctx)
	defer cancel()

	summaryKey := r.summaryKey(chatID, period)
	exists, err := r.client.Exists(runCtx, summaryKey).Result()
	if err != nil {
		return ledgerSummary{}, false, err
	}
	if exists == 0 {
		return ledgerSummary{}, false, nil
	}

	fields, err := r.client.HMGet(
		runCtx,
		summaryKey,
		dailyLedgerFieldDepositTotal,
		dailyLedgerFieldPayoutTotal,
		dailyLedgerFieldDepositCount,
		dailyLedgerFieldPayoutCount,
	).Result()
	if err != nil {
		return ledgerSummary{}, false, err
	}
	if len(fields) != 4 {
		return ledgerSummary{}, true, nil
	}

	depositTotal, err := parseRedisFloat(fields[0])
	if err != nil {
		return ledgerSummary{}, false, err
	}
	payoutTotal, err := parseRedisFloat(fields[1])
	if err != nil {
		return ledgerSummary{}, false, err
	}
	depositCount, err := parseRedisInt(fields[2])
	if err != nil {
		return ledgerSummary{}, false, err
	}
	payoutCount, err := parseRedisInt(fields[3])
	if err != nil {
		return ledgerSummary{}, false, err
	}
	return ledgerSummary{
		depCount: int(depositCount),
		payCount: int(payoutCount),
		depTotal: depositTotal,
		payTotal: payoutTotal,
	}, true, nil
}

func (r *RedisDailyLedgerCounter) RateStats(ctx context.Context, chatID int64, periodKey, entryType string) (map[string]float64, bool, error) {
	if r == nil || r.client == nil || chatID == 0 {
		return nil, false, nil
	}
	period := normalizeDailyLedgerPeriodKey(periodKey)
	if period == "" {
		return nil, false, nil
	}
	kind := normalizeDailyLedgerEntryType(entryType)
	if kind == "" {
		return nil, false, nil
	}

	runCtx, cancel := r.withTimeout(ctx)
	defer cancel()

	key := r.rateKey(chatID, period, kind)
	exists, err := r.client.Exists(runCtx, key).Result()
	if err != nil {
		return nil, false, err
	}
	if exists == 0 {
		return nil, false, nil
	}
	raw, err := r.client.HGetAll(runCtx, key).Result()
	if err != nil {
		return nil, false, err
	}
	out := make(map[string]float64, len(raw))
	for rate, amount := range raw {
		value, parseErr := strconv.ParseFloat(strings.TrimSpace(amount), 64)
		if parseErr != nil {
			return nil, false, parseErr
		}
		out[strings.TrimSpace(rate)] = value
	}
	return out, true, nil
}

func (r *RedisDailyLedgerCounter) Replace(
	ctx context.Context,
	chatID int64,
	periodKey string,
	summary ledgerSummary,
	depositRates map[string]float64,
	payoutRates map[string]float64,
) error {
	if r == nil || r.client == nil || chatID == 0 {
		return nil
	}
	period := normalizeDailyLedgerPeriodKey(periodKey)
	if period == "" {
		return nil
	}

	summaryKey := r.summaryKey(chatID, period)
	depositRateKey := r.rateKey(chatID, period, "deposit")
	payoutRateKey := r.rateKey(chatID, period, "payout")

	runCtx, cancel := r.withTimeout(ctx)
	defer cancel()

	if summary.depCount == 0 && summary.payCount == 0 &&
		almostZero(summary.depTotal) && almostZero(summary.payTotal) &&
		len(depositRates) == 0 && len(payoutRates) == 0 {
		return r.client.Del(runCtx, summaryKey, depositRateKey, payoutRateKey).Err()
	}

	pipe := r.client.TxPipeline()
	pipe.Del(runCtx, summaryKey, depositRateKey, payoutRateKey)
	pipe.HSet(runCtx, summaryKey,
		dailyLedgerFieldDepositTotal, summary.depTotal,
		dailyLedgerFieldPayoutTotal, summary.payTotal,
		dailyLedgerFieldDepositCount, summary.depCount,
		dailyLedgerFieldPayoutCount, summary.payCount,
	)
	if len(depositRates) > 0 {
		fields := flattenDailyLedgerRateStats(depositRates)
		if len(fields) > 0 {
			pipe.HSet(runCtx, depositRateKey, fields...)
			pipe.Expire(runCtx, depositRateKey, r.ttl)
		}
	}
	if len(payoutRates) > 0 {
		fields := flattenDailyLedgerRateStats(payoutRates)
		if len(fields) > 0 {
			pipe.HSet(runCtx, payoutRateKey, fields...)
			pipe.Expire(runCtx, payoutRateKey, r.ttl)
		}
	}
	pipe.Expire(runCtx, summaryKey, r.ttl)
	_, err := pipe.Exec(runCtx)
	return err
}

func (r *RedisDailyLedgerCounter) Close() error {
	if r == nil || r.client == nil {
		return nil
	}
	return r.client.Close()
}

func (r *RedisDailyLedgerCounter) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, r.timeout)
}

func (r *RedisDailyLedgerCounter) summaryKey(chatID int64, periodKey string) string {
	return fmt.Sprintf("%ssummary:%d:%s", r.keyPrefix, chatID, periodKey)
}

func (r *RedisDailyLedgerCounter) rateKey(chatID int64, periodKey, entryType string) string {
	return fmt.Sprintf("%srate:%s:%d:%s", r.keyPrefix, entryType, chatID, periodKey)
}

func normalizeDailyLedgerEntryType(entryType string) string {
	switch strings.TrimSpace(entryType) {
	case "deposit":
		return "deposit"
	case "payout":
		return "payout"
	default:
		return ""
	}
}

func dailyLedgerSummaryFields(entryType string) (string, string) {
	switch normalizeDailyLedgerEntryType(entryType) {
	case "deposit":
		return dailyLedgerFieldDepositTotal, dailyLedgerFieldDepositCount
	case "payout":
		return dailyLedgerFieldPayoutTotal, dailyLedgerFieldPayoutCount
	default:
		return "", ""
	}
}

func normalizeDailyLedgerPeriodKey(periodKey string) string {
	return strings.TrimSpace(periodKey)
}

func normalizeDailyLedgerRateField(rate float64) string {
	return utils.FormatAmount(rate)
}

func flattenDailyLedgerRateStats(stats map[string]float64) []any {
	args := make([]any, 0, len(stats)*2)
	for rawRate, amount := range stats {
		rate := strings.TrimSpace(rawRate)
		if rate == "" {
			continue
		}
		args = append(args, rate, amount)
	}
	return args
}

func parseRedisFloat(raw any) (float64, error) {
	if raw == nil {
		return 0, nil
	}
	switch value := raw.(type) {
	case float64:
		return value, nil
	case float32:
		return float64(value), nil
	case int:
		return float64(value), nil
	case int64:
		return float64(value), nil
	case uint64:
		return float64(value), nil
	case string:
		return strconv.ParseFloat(strings.TrimSpace(value), 64)
	case []byte:
		return strconv.ParseFloat(strings.TrimSpace(string(value)), 64)
	default:
		return strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(value)), 64)
	}
}

func parseRedisInt(raw any) (int64, error) {
	if raw == nil {
		return 0, nil
	}
	switch value := raw.(type) {
	case int:
		return int64(value), nil
	case int8:
		return int64(value), nil
	case int16:
		return int64(value), nil
	case int32:
		return int64(value), nil
	case int64:
		return value, nil
	case uint:
		return int64(value), nil
	case uint8:
		return int64(value), nil
	case uint16:
		return int64(value), nil
	case uint32:
		return int64(value), nil
	case uint64:
		return int64(value), nil
	case float64:
		return int64(value), nil
	case float32:
		return int64(value), nil
	case string:
		return strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	case []byte:
		return strconv.ParseInt(strings.TrimSpace(string(value)), 10, 64)
	default:
		return strconv.ParseInt(strings.TrimSpace(fmt.Sprint(value)), 10, 64)
	}
}
