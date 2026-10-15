package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	defaultTimescaleConnectTimeout = 5 * time.Second
	defaultTimescaleMaxOpenConns   = 8
	defaultTimescaleMaxIdleConns   = 2
)

const insertRateTickSQL = `INSERT INTO fx_rate_ticks (
	recorded_at,
	chat_id,
	provider,
	pay_type,
	price,
	source
) VALUES ($1, $2, $3, $4, $5, $6)`

const insertCapitalFlowTickSQL = `INSERT INTO capital_flow_ticks (
	recorded_at,
	chat_id,
	user_id,
	entry_type,
	currency,
	amount,
	base_amount,
	final_amount,
	rate,
	fee_rate
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`

var timescaleSchemaStatements = []string{
	`CREATE EXTENSION IF NOT EXISTS timescaledb`,
	`CREATE TABLE IF NOT EXISTS fx_rate_ticks (
		recorded_at TIMESTAMPTZ NOT NULL,
		chat_id BIGINT NOT NULL,
		provider TEXT NOT NULL,
		pay_type TEXT NOT NULL,
		price DOUBLE PRECISION NOT NULL,
		source TEXT NOT NULL DEFAULT ''
	)`,
	`SELECT create_hypertable('fx_rate_ticks', 'recorded_at', if_not_exists => TRUE)`,
	`CREATE INDEX IF NOT EXISTS idx_fx_rate_ticks_chat_provider_time ON fx_rate_ticks(chat_id, provider, recorded_at DESC)`,
	`CREATE TABLE IF NOT EXISTS capital_flow_ticks (
		recorded_at TIMESTAMPTZ NOT NULL,
		chat_id BIGINT NOT NULL,
		user_id BIGINT NOT NULL,
		entry_type TEXT NOT NULL,
		currency TEXT NOT NULL,
		amount DOUBLE PRECISION NOT NULL,
		base_amount DOUBLE PRECISION NOT NULL,
		final_amount DOUBLE PRECISION NOT NULL,
		rate DOUBLE PRECISION NOT NULL,
		fee_rate DOUBLE PRECISION NOT NULL
	)`,
	`SELECT create_hypertable('capital_flow_ticks', 'recorded_at', if_not_exists => TRUE)`,
	`CREATE INDEX IF NOT EXISTS idx_capital_flow_ticks_chat_type_time ON capital_flow_ticks(chat_id, entry_type, recorded_at DESC)`,
}

type TimeSeriesRecorder interface {
	Enabled() bool
	RecordRateTick(ctx context.Context, tick RateTick) error
	RecordCapitalFlowTick(ctx context.Context, tick CapitalFlowTick) error
	Close() error
}

type RateTick struct {
	OccurredAt time.Time
	ChatID     int64
	Provider   string
	PayType    string
	Price      float64
	Source     string
}

type CapitalFlowTick struct {
	OccurredAt  time.Time
	ChatID      int64
	UserID      int64
	EntryType   string
	Currency    string
	Amount      float64
	BaseAmount  float64
	FinalAmount float64
	Rate        float64
	FeeRate     float64
}

type TimescaleStoreOptions struct {
	ConnectTimeout time.Duration
	MaxOpenConns   int
	MaxIdleConns   int
}

type noopTimeSeriesRecorder struct{}

func NewNoopTimeSeriesRecorder() TimeSeriesRecorder {
	return noopTimeSeriesRecorder{}
}

func (noopTimeSeriesRecorder) Enabled() bool {
	return false
}

func (noopTimeSeriesRecorder) RecordRateTick(context.Context, RateTick) error {
	return nil
}

func (noopTimeSeriesRecorder) RecordCapitalFlowTick(context.Context, CapitalFlowTick) error {
	return nil
}

func (noopTimeSeriesRecorder) Close() error {
	return nil
}

type sqlExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

type TimescaleStore struct {
	raw  *sql.DB
	exec sqlExecutor
}

func NewTimescaleStore(dsn string, opts TimescaleStoreOptions) (*TimescaleStore, error) {
	trimmedDSN := strings.TrimSpace(dsn)
	if trimmedDSN == "" {
		return nil, errors.New("TIMESCALE_DSN 不能为空")
	}

	normalized := normalizeTimescaleStoreOptions(opts)
	raw, err := sql.Open("pgx", trimmedDSN)
	if err != nil {
		return nil, err
	}
	raw.SetMaxOpenConns(normalized.MaxOpenConns)
	raw.SetMaxIdleConns(normalized.MaxIdleConns)
	raw.SetConnMaxIdleTime(10 * time.Minute)
	raw.SetConnMaxLifetime(time.Hour)

	ctx, cancel := context.WithTimeout(context.Background(), normalized.ConnectTimeout)
	defer cancel()
	if err := raw.PingContext(ctx); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("TimescaleDB 连接失败: %w", err)
	}

	store := &TimescaleStore{raw: raw, exec: raw}
	if err := store.initSchema(ctx); err != nil {
		_ = raw.Close()
		return nil, err
	}
	return store, nil
}

func normalizeTimescaleStoreOptions(opts TimescaleStoreOptions) TimescaleStoreOptions {
	normalized := opts
	if normalized.ConnectTimeout <= 0 {
		normalized.ConnectTimeout = defaultTimescaleConnectTimeout
	}
	if normalized.MaxOpenConns <= 0 {
		normalized.MaxOpenConns = defaultTimescaleMaxOpenConns
	}
	if normalized.MaxIdleConns < 0 {
		normalized.MaxIdleConns = 0
	}
	if normalized.MaxIdleConns > normalized.MaxOpenConns {
		normalized.MaxIdleConns = normalized.MaxOpenConns
	}
	if normalized.MaxIdleConns == 0 && normalized.MaxOpenConns > 1 {
		normalized.MaxIdleConns = defaultTimescaleMaxIdleConns
		if normalized.MaxIdleConns > normalized.MaxOpenConns {
			normalized.MaxIdleConns = normalized.MaxOpenConns
		}
	}
	return normalized
}

func (s *TimescaleStore) Enabled() bool {
	return s != nil && s.exec != nil
}

func (s *TimescaleStore) Close() error {
	if s == nil || s.raw == nil {
		return nil
	}
	return s.raw.Close()
}

func (s *TimescaleStore) initSchema(ctx context.Context) error {
	if s == nil || s.exec == nil {
		return errors.New("TimescaleStore 未初始化")
	}
	return executeTimescaleSchema(ctx, s.exec)
}

func executeTimescaleSchema(ctx context.Context, exec sqlExecutor) error {
	if exec == nil {
		return errors.New("schema executor 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for _, stmt := range timescaleSchemaStatements {
		if _, err := exec.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("初始化 Timescale schema 失败: %w", err)
		}
	}
	return nil
}

func (s *TimescaleStore) RecordRateTick(ctx context.Context, tick RateTick) error {
	if !s.Enabled() {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	occurredAt := tick.OccurredAt.UTC()
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	provider := strings.ToLower(strings.TrimSpace(tick.Provider))
	if provider == "" {
		provider = "unknown"
	}
	payType := strings.ToLower(strings.TrimSpace(tick.PayType))
	if payType == "" {
		payType = "all"
	}
	source := strings.TrimSpace(tick.Source)
	if source == "" {
		source = "n/a"
	}
	_, err := s.exec.ExecContext(ctx, insertRateTickSQL,
		occurredAt,
		tick.ChatID,
		provider,
		payType,
		tick.Price,
		source,
	)
	if err != nil {
		return fmt.Errorf("写入汇率时序失败: %w", err)
	}
	return nil
}

func (s *TimescaleStore) RecordCapitalFlowTick(ctx context.Context, tick CapitalFlowTick) error {
	if !s.Enabled() {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	occurredAt := tick.OccurredAt.UTC()
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	entryType := strings.ToLower(strings.TrimSpace(tick.EntryType))
	if entryType == "" {
		entryType = "unknown"
	}
	currency := strings.ToUpper(strings.TrimSpace(tick.Currency))
	_, err := s.exec.ExecContext(ctx, insertCapitalFlowTickSQL,
		occurredAt,
		tick.ChatID,
		tick.UserID,
		entryType,
		currency,
		tick.Amount,
		tick.BaseAmount,
		tick.FinalAmount,
		tick.Rate,
		tick.FeeRate,
	)
	if err != nil {
		return fmt.Errorf("写入资金流向时序失败: %w", err)
	}
	return nil
}
