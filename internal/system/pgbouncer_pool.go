package system

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type PgBouncerPoolMode string

type PgBouncerAuthType string

const (
	PgBouncerPoolModeSession     PgBouncerPoolMode = "session"
	PgBouncerPoolModeTransaction PgBouncerPoolMode = "transaction"
	PgBouncerPoolModeStatement   PgBouncerPoolMode = "statement"
)

const (
	PgBouncerAuthTypeSCRAMSHA256 PgBouncerAuthType = "scram-sha-256"
	PgBouncerAuthTypeMD5         PgBouncerAuthType = "md5"
	PgBouncerAuthTypePlain       PgBouncerAuthType = "plain"
	PgBouncerAuthTypeTrust       PgBouncerAuthType = "trust"
)

const (
	DefaultPgBouncerEnabled                       = true
	DefaultPgBouncerListenAddr                    = "127.0.0.1"
	DefaultPgBouncerListenPort              int32 = 6432
	DefaultPgBouncerUpstreamHost                  = "postgres.default.svc.cluster.local"
	DefaultPgBouncerUpstreamPort            int32 = 5432
	DefaultPgBouncerPoolMode                      = PgBouncerPoolModeTransaction
	DefaultPgBouncerDefaultPoolSize         int32 = 30
	DefaultPgBouncerMinPoolSize             int32 = 5
	DefaultPgBouncerReservePoolSize         int32 = 10
	DefaultPgBouncerMaxClientConn           int32 = 500
	DefaultPgBouncerAuthType                      = PgBouncerAuthTypeSCRAMSHA256
	DefaultPgBouncerAuthFile                      = "/etc/pgbouncer/userlist.txt"
	DefaultPgBouncerIgnoreStartupParameters       = "extra_float_digits"
	DefaultPgBouncerServerResetQuery              = "DISCARD ALL"
)

type PgBouncerSpec struct {
	Enabled                 bool
	ListenAddr              string
	ListenPort              int32
	UpstreamHost            string
	UpstreamPort            int32
	PoolMode                PgBouncerPoolMode
	DefaultPoolSize         int32
	MinPoolSize             int32
	ReservePoolSize         int32
	MaxClientConn           int32
	AuthType                PgBouncerAuthType
	AuthFile                string
	IgnoreStartupParameters string
	ServerResetQuery        string
}

func DefaultPgBouncerSpec() PgBouncerSpec {
	return PgBouncerSpec{
		Enabled:                 DefaultPgBouncerEnabled,
		ListenAddr:              DefaultPgBouncerListenAddr,
		ListenPort:              DefaultPgBouncerListenPort,
		UpstreamHost:            DefaultPgBouncerUpstreamHost,
		UpstreamPort:            DefaultPgBouncerUpstreamPort,
		PoolMode:                DefaultPgBouncerPoolMode,
		DefaultPoolSize:         DefaultPgBouncerDefaultPoolSize,
		MinPoolSize:             DefaultPgBouncerMinPoolSize,
		ReservePoolSize:         DefaultPgBouncerReservePoolSize,
		MaxClientConn:           DefaultPgBouncerMaxClientConn,
		AuthType:                DefaultPgBouncerAuthType,
		AuthFile:                DefaultPgBouncerAuthFile,
		IgnoreStartupParameters: DefaultPgBouncerIgnoreStartupParameters,
		ServerResetQuery:        DefaultPgBouncerServerResetQuery,
	}
}

func NormalizePgBouncerSpec(spec PgBouncerSpec) (PgBouncerSpec, error) {
	normalized := DefaultPgBouncerSpec()
	normalized.Enabled = spec.Enabled

	if listenAddr := strings.TrimSpace(spec.ListenAddr); listenAddr != "" {
		normalized.ListenAddr = listenAddr
	}
	switch {
	case spec.ListenPort < 0:
		return PgBouncerSpec{}, fmt.Errorf("listen port must be in range 1-65535")
	case spec.ListenPort > 0:
		normalized.ListenPort = spec.ListenPort
	}

	if upstreamHost := strings.TrimSpace(spec.UpstreamHost); upstreamHost != "" {
		normalized.UpstreamHost = upstreamHost
	}
	switch {
	case spec.UpstreamPort < 0:
		return PgBouncerSpec{}, fmt.Errorf("upstream port must be in range 1-65535")
	case spec.UpstreamPort > 0:
		normalized.UpstreamPort = spec.UpstreamPort
	}

	if poolModeRaw := strings.TrimSpace(string(spec.PoolMode)); poolModeRaw != "" {
		poolMode, err := parsePgBouncerPoolMode(poolModeRaw)
		if err != nil {
			return PgBouncerSpec{}, err
		}
		normalized.PoolMode = poolMode
	}

	switch {
	case spec.DefaultPoolSize < 0:
		return PgBouncerSpec{}, fmt.Errorf("default pool size must be greater than 0")
	case spec.DefaultPoolSize > 0:
		normalized.DefaultPoolSize = spec.DefaultPoolSize
	}
	switch {
	case spec.MinPoolSize < 0:
		return PgBouncerSpec{}, fmt.Errorf("min pool size must be greater than or equal to 0")
	case spec.MinPoolSize > 0:
		normalized.MinPoolSize = spec.MinPoolSize
	}
	switch {
	case spec.ReservePoolSize < 0:
		return PgBouncerSpec{}, fmt.Errorf("reserve pool size must be greater than or equal to 0")
	case spec.ReservePoolSize > 0:
		normalized.ReservePoolSize = spec.ReservePoolSize
	}
	switch {
	case spec.MaxClientConn < 0:
		return PgBouncerSpec{}, fmt.Errorf("max client conn must be greater than 0")
	case spec.MaxClientConn > 0:
		normalized.MaxClientConn = spec.MaxClientConn
	}

	if authTypeRaw := strings.TrimSpace(string(spec.AuthType)); authTypeRaw != "" {
		authType, err := parsePgBouncerAuthType(authTypeRaw)
		if err != nil {
			return PgBouncerSpec{}, err
		}
		normalized.AuthType = authType
	}
	if authFile := strings.TrimSpace(spec.AuthFile); authFile != "" {
		normalized.AuthFile = authFile
	}
	if ignoreStartupParams := strings.TrimSpace(spec.IgnoreStartupParameters); ignoreStartupParams != "" {
		normalized.IgnoreStartupParameters = ignoreStartupParams
	}
	if serverResetQuery := strings.TrimSpace(spec.ServerResetQuery); serverResetQuery != "" {
		normalized.ServerResetQuery = serverResetQuery
	}

	if !isValidPgBouncerHost(normalized.ListenAddr) {
		return PgBouncerSpec{}, fmt.Errorf("listen addr must not be empty or contain spaces")
	}
	if normalized.ListenPort <= 0 || normalized.ListenPort > 65535 {
		return PgBouncerSpec{}, fmt.Errorf("listen port must be in range 1-65535")
	}
	if !isValidPgBouncerHost(normalized.UpstreamHost) {
		return PgBouncerSpec{}, fmt.Errorf("upstream host must not be empty or contain spaces")
	}
	if normalized.UpstreamPort <= 0 || normalized.UpstreamPort > 65535 {
		return PgBouncerSpec{}, fmt.Errorf("upstream port must be in range 1-65535")
	}
	if normalized.PoolMode == "" {
		return PgBouncerSpec{}, fmt.Errorf("pool mode must not be empty")
	}
	if normalized.DefaultPoolSize <= 0 {
		return PgBouncerSpec{}, fmt.Errorf("default pool size must be greater than 0")
	}
	if normalized.MinPoolSize < 0 {
		return PgBouncerSpec{}, fmt.Errorf("min pool size must be greater than or equal to 0")
	}
	if normalized.MinPoolSize > normalized.DefaultPoolSize {
		return PgBouncerSpec{}, fmt.Errorf("min pool size must be <= default pool size")
	}
	if normalized.ReservePoolSize < 0 {
		return PgBouncerSpec{}, fmt.Errorf("reserve pool size must be greater than or equal to 0")
	}
	if normalized.MaxClientConn <= 0 {
		return PgBouncerSpec{}, fmt.Errorf("max client conn must be greater than 0")
	}
	if normalized.MaxClientConn < normalized.DefaultPoolSize {
		return PgBouncerSpec{}, fmt.Errorf("max client conn must be >= default pool size")
	}
	if normalized.AuthType == "" {
		return PgBouncerSpec{}, fmt.Errorf("auth type must not be empty")
	}
	if strings.TrimSpace(normalized.AuthFile) == "" {
		return PgBouncerSpec{}, fmt.Errorf("auth file must not be empty")
	}
	if strings.TrimSpace(normalized.IgnoreStartupParameters) == "" {
		return PgBouncerSpec{}, fmt.Errorf("ignore startup parameters must not be empty")
	}
	if strings.TrimSpace(normalized.ServerResetQuery) == "" {
		return PgBouncerSpec{}, fmt.Errorf("server reset query must not be empty")
	}

	return normalized, nil
}

func LoadPgBouncerSpecFromEnv(getenv func(string) string) (PgBouncerSpec, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	spec := DefaultPgBouncerSpec()
	warnings := make([]string, 0, 13)

	enabledRaw := strings.TrimSpace(getenv("PGBOUNCER_ENABLED"))
	if enabledRaw != "" {
		enabled, err := parsePgBouncerBool(enabledRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"PGBOUNCER_ENABLED=%q 无效，回退默认值 %t: %v",
					enabledRaw,
					DefaultPgBouncerEnabled,
					err,
				),
			)
		} else {
			spec.Enabled = enabled
		}
	}

	listenAddrRaw := strings.TrimSpace(getenv("PGBOUNCER_LISTEN_ADDR"))
	if listenAddrRaw != "" {
		spec.ListenAddr = listenAddrRaw
	}
	listenPortRaw := strings.TrimSpace(getenv("PGBOUNCER_LISTEN_PORT"))
	if listenPortRaw != "" {
		listenPort, err := parsePgBouncerPositiveInt32(listenPortRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"PGBOUNCER_LISTEN_PORT=%q 无效，回退默认值 %d: %v",
					listenPortRaw,
					DefaultPgBouncerListenPort,
					err,
				),
			)
		} else {
			spec.ListenPort = listenPort
		}
	}

	upstreamHostRaw := strings.TrimSpace(getenv("PGBOUNCER_UPSTREAM_HOST"))
	if upstreamHostRaw != "" {
		spec.UpstreamHost = upstreamHostRaw
	}
	upstreamPortRaw := strings.TrimSpace(getenv("PGBOUNCER_UPSTREAM_PORT"))
	if upstreamPortRaw != "" {
		upstreamPort, err := parsePgBouncerPositiveInt32(upstreamPortRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"PGBOUNCER_UPSTREAM_PORT=%q 无效，回退默认值 %d: %v",
					upstreamPortRaw,
					DefaultPgBouncerUpstreamPort,
					err,
				),
			)
		} else {
			spec.UpstreamPort = upstreamPort
		}
	}

	poolModeRaw := strings.TrimSpace(getenv("PGBOUNCER_POOL_MODE"))
	if poolModeRaw != "" {
		poolMode, err := parsePgBouncerPoolMode(poolModeRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"PGBOUNCER_POOL_MODE=%q 无效，回退默认值 %s: %v",
					poolModeRaw,
					DefaultPgBouncerPoolMode,
					err,
				),
			)
		} else {
			spec.PoolMode = poolMode
		}
	}

	defaultPoolRaw := strings.TrimSpace(getenv("PGBOUNCER_DEFAULT_POOL_SIZE"))
	if defaultPoolRaw != "" {
		defaultPool, err := parsePgBouncerPositiveInt32(defaultPoolRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"PGBOUNCER_DEFAULT_POOL_SIZE=%q 无效，回退默认值 %d: %v",
					defaultPoolRaw,
					DefaultPgBouncerDefaultPoolSize,
					err,
				),
			)
		} else {
			spec.DefaultPoolSize = defaultPool
		}
	}

	minPoolRaw := strings.TrimSpace(getenv("PGBOUNCER_MIN_POOL_SIZE"))
	if minPoolRaw != "" {
		minPool, err := parsePgBouncerNonNegativeInt32(minPoolRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"PGBOUNCER_MIN_POOL_SIZE=%q 无效，回退默认值 %d: %v",
					minPoolRaw,
					DefaultPgBouncerMinPoolSize,
					err,
				),
			)
		} else {
			spec.MinPoolSize = minPool
		}
	}

	reservePoolRaw := strings.TrimSpace(getenv("PGBOUNCER_RESERVE_POOL_SIZE"))
	if reservePoolRaw != "" {
		reservePool, err := parsePgBouncerNonNegativeInt32(reservePoolRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"PGBOUNCER_RESERVE_POOL_SIZE=%q 无效，回退默认值 %d: %v",
					reservePoolRaw,
					DefaultPgBouncerReservePoolSize,
					err,
				),
			)
		} else {
			spec.ReservePoolSize = reservePool
		}
	}

	maxClientRaw := strings.TrimSpace(getenv("PGBOUNCER_MAX_CLIENT_CONN"))
	if maxClientRaw != "" {
		maxClient, err := parsePgBouncerPositiveInt32(maxClientRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"PGBOUNCER_MAX_CLIENT_CONN=%q 无效，回退默认值 %d: %v",
					maxClientRaw,
					DefaultPgBouncerMaxClientConn,
					err,
				),
			)
		} else {
			spec.MaxClientConn = maxClient
		}
	}

	authTypeRaw := strings.TrimSpace(getenv("PGBOUNCER_AUTH_TYPE"))
	if authTypeRaw != "" {
		authType, err := parsePgBouncerAuthType(authTypeRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"PGBOUNCER_AUTH_TYPE=%q 无效，回退默认值 %s: %v",
					authTypeRaw,
					DefaultPgBouncerAuthType,
					err,
				),
			)
		} else {
			spec.AuthType = authType
		}
	}

	authFileRaw := strings.TrimSpace(getenv("PGBOUNCER_AUTH_FILE"))
	if authFileRaw != "" {
		spec.AuthFile = authFileRaw
	}

	ignoreStartupRaw := strings.TrimSpace(getenv("PGBOUNCER_IGNORE_STARTUP_PARAMETERS"))
	if ignoreStartupRaw != "" {
		spec.IgnoreStartupParameters = ignoreStartupRaw
	}

	serverResetRaw := strings.TrimSpace(getenv("PGBOUNCER_SERVER_RESET_QUERY"))
	if serverResetRaw != "" {
		spec.ServerResetQuery = serverResetRaw
	}

	normalized, err := NormalizePgBouncerSpec(spec)
	if err != nil {
		return DefaultPgBouncerSpec(), fmt.Errorf("PgBouncer 配置非法，回退默认值: %w", err)
	}
	if len(warnings) > 0 {
		return normalized, errors.New(strings.Join(warnings, "; "))
	}
	return normalized, nil
}

func BuildPgBouncerConfig(spec PgBouncerSpec) (string, error) {
	normalized, err := NormalizePgBouncerSpec(spec)
	if err != nil {
		return "", err
	}
	configText := fmt.Sprintf(`[databases]
* = host=%s port=%d pool_size=%d min_pool_size=%d reserve_pool=%d

[pgbouncer]
listen_addr = %s
listen_port = %d
auth_type = %s
auth_file = %s
pool_mode = %s
max_client_conn = %d
default_pool_size = %d
min_pool_size = %d
reserve_pool_size = %d
ignore_startup_parameters = %s
server_reset_query = %s
`,
		normalized.UpstreamHost,
		normalized.UpstreamPort,
		normalized.DefaultPoolSize,
		normalized.MinPoolSize,
		normalized.ReservePoolSize,
		normalized.ListenAddr,
		normalized.ListenPort,
		normalized.AuthType,
		normalized.AuthFile,
		normalized.PoolMode,
		normalized.MaxClientConn,
		normalized.DefaultPoolSize,
		normalized.MinPoolSize,
		normalized.ReservePoolSize,
		normalized.IgnoreStartupParameters,
		normalized.ServerResetQuery,
	)
	return configText, nil
}

func BuildPgBouncerLog(spec PgBouncerSpec) string {
	normalized, err := NormalizePgBouncerSpec(spec)
	if err != nil {
		return fmt.Sprintf("PgBouncer 连接池未启用：配置非法 %v", err)
	}
	if !normalized.Enabled {
		return "PgBouncer 连接池未启用：PGBOUNCER_ENABLED=false"
	}
	return fmt.Sprintf(
		"PgBouncer 连接池已启用：listen=%s:%d upstream=%s:%d mode=%s max_client_conn=%d pool(default=%d min=%d reserve=%d) auth=%s",
		normalized.ListenAddr,
		normalized.ListenPort,
		normalized.UpstreamHost,
		normalized.UpstreamPort,
		normalized.PoolMode,
		normalized.MaxClientConn,
		normalized.DefaultPoolSize,
		normalized.MinPoolSize,
		normalized.ReservePoolSize,
		normalized.AuthType,
	)
}

func parsePgBouncerBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "on", "yes", "enabled", "enable":
		return true, nil
	case "0", "false", "off", "no", "disabled", "disable":
		return false, nil
	default:
		return false, fmt.Errorf("must be one of true/false/1/0/on/off/yes/no")
	}
}

func parsePgBouncerPoolMode(raw string) (PgBouncerPoolMode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "session":
		return PgBouncerPoolModeSession, nil
	case "transaction", "txn":
		return PgBouncerPoolModeTransaction, nil
	case "statement", "stmt":
		return PgBouncerPoolModeStatement, nil
	default:
		return "", fmt.Errorf("unsupported pool mode %q", raw)
	}
}

func parsePgBouncerAuthType(raw string) (PgBouncerAuthType, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "scram-sha-256", "scram_sha_256", "scramsha256", "scram":
		return PgBouncerAuthTypeSCRAMSHA256, nil
	case "md5":
		return PgBouncerAuthTypeMD5, nil
	case "plain":
		return PgBouncerAuthTypePlain, nil
	case "trust":
		return PgBouncerAuthTypeTrust, nil
	default:
		return "", fmt.Errorf("unsupported auth type %q", raw)
	}
}

func parsePgBouncerPositiveInt32(raw string) (int32, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, err
	}
	if value <= 0 {
		return 0, fmt.Errorf("must be > 0")
	}
	if value > 65535 {
		return 0, fmt.Errorf("must be <= 65535")
	}
	return int32(value), nil
}

func parsePgBouncerNonNegativeInt32(raw string) (int32, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, err
	}
	if value < 0 {
		return 0, fmt.Errorf("must be >= 0")
	}
	if value > 65535 {
		return 0, fmt.Errorf("must be <= 65535")
	}
	return int32(value), nil
}

func isValidPgBouncerHost(host string) bool {
	trimmed := strings.TrimSpace(host)
	if trimmed == "" {
		return false
	}
	return !strings.ContainsAny(trimmed, " \t\r\n")
}
