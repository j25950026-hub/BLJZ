package system

import (
	"strconv"
	"strings"
	"testing"
)

func TestControlPoint544PgBouncerConnectionPooling(t *testing.T) {
	t.Run("控制点 5.4.4 功能验证", func(t *testing.T) {
		spec, err := LoadPgBouncerSpecFromEnv(func(key string) string {
			switch key {
			case "PGBOUNCER_ENABLED":
				return "true"
			case "PGBOUNCER_LISTEN_ADDR":
				return "10.10.0.8"
			case "PGBOUNCER_LISTEN_PORT":
				return "7432"
			case "PGBOUNCER_UPSTREAM_HOST":
				return "timescaledb.finance.svc.cluster.local"
			case "PGBOUNCER_UPSTREAM_PORT":
				return "5432"
			case "PGBOUNCER_POOL_MODE":
				return "transaction"
			case "PGBOUNCER_DEFAULT_POOL_SIZE":
				return "48"
			case "PGBOUNCER_MIN_POOL_SIZE":
				return "12"
			case "PGBOUNCER_RESERVE_POOL_SIZE":
				return "16"
			case "PGBOUNCER_MAX_CLIENT_CONN":
				return "1200"
			case "PGBOUNCER_AUTH_TYPE":
				return "scram"
			case "PGBOUNCER_AUTH_FILE":
				return "/etc/pgbouncer/custom-users.txt"
			case "PGBOUNCER_IGNORE_STARTUP_PARAMETERS":
				return "extra_float_digits,options"
			case "PGBOUNCER_SERVER_RESET_QUERY":
				return "DISCARD ALL"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("加载 PgBouncer 配置失败: %v", err)
		}
		if !spec.Enabled {
			t.Fatalf("PgBouncer 应处于启用状态")
		}
		if spec.ListenAddr != "10.10.0.8" || spec.ListenPort != 7432 {
			t.Fatalf("监听地址配置错误: %+v", spec)
		}
		if spec.UpstreamHost != "timescaledb.finance.svc.cluster.local" || spec.UpstreamPort != 5432 {
			t.Fatalf("上游地址配置错误: %+v", spec)
		}
		if spec.PoolMode != PgBouncerPoolModeTransaction {
			t.Fatalf("连接池模式解析错误: %s", spec.PoolMode)
		}
		if spec.DefaultPoolSize != 48 || spec.MinPoolSize != 12 || spec.ReservePoolSize != 16 {
			t.Fatalf("连接池容量参数错误: %+v", spec)
		}
		if spec.MaxClientConn != 1200 {
			t.Fatalf("最大客户端连接数解析错误: %d", spec.MaxClientConn)
		}
		if spec.AuthType != PgBouncerAuthTypeSCRAMSHA256 {
			t.Fatalf("鉴权类型解析错误: %s", spec.AuthType)
		}
		if spec.AuthFile != "/etc/pgbouncer/custom-users.txt" {
			t.Fatalf("鉴权文件解析错误: %s", spec.AuthFile)
		}

		configText, err := BuildPgBouncerConfig(spec)
		if err != nil {
			t.Fatalf("生成 PgBouncer 配置失败: %v", err)
		}
		requiredConfig := []string{
			"[databases]",
			"* = host=timescaledb.finance.svc.cluster.local port=5432 pool_size=48 min_pool_size=12 reserve_pool=16",
			"[pgbouncer]",
			"listen_addr = 10.10.0.8",
			"listen_port = 7432",
			"auth_type = scram-sha-256",
			"auth_file = /etc/pgbouncer/custom-users.txt",
			"pool_mode = transaction",
			"max_client_conn = 1200",
			"default_pool_size = 48",
			"min_pool_size = 12",
			"reserve_pool_size = 16",
			"ignore_startup_parameters = extra_float_digits,options",
			"server_reset_query = DISCARD ALL",
		}
		for _, fragment := range requiredConfig {
			if !strings.Contains(configText, fragment) {
				t.Fatalf("PgBouncer 配置缺少关键片段 %q: %s", fragment, configText)
			}
		}

		logLine := BuildPgBouncerLog(spec)
		requiredLog := []string{
			"PgBouncer 连接池已启用",
			"listen=10.10.0.8:7432",
			"upstream=timescaledb.finance.svc.cluster.local:5432",
			"mode=transaction",
			"max_client_conn=1200",
			"pool(default=48 min=12 reserve=16)",
			"auth=scram-sha-256",
		}
		for _, fragment := range requiredLog {
			if !strings.Contains(logLine, fragment) {
				t.Fatalf("PgBouncer 日志缺少关键片段 %q: %s", fragment, logLine)
			}
		}
	})
}

func TestLoadPgBouncerSpecFromEnvFallback(t *testing.T) {
	spec, err := LoadPgBouncerSpecFromEnv(func(key string) string {
		switch key {
		case "PGBOUNCER_LISTEN_PORT":
			return "bad"
		case "PGBOUNCER_MAX_CLIENT_CONN":
			return "invalid"
		case "PGBOUNCER_POOL_MODE":
			return "broken"
		default:
			return ""
		}
	})
	if err == nil {
		t.Fatalf("非法 PgBouncer 配置应返回回退告警")
	}
	if !strings.Contains(err.Error(), "PGBOUNCER_LISTEN_PORT") {
		t.Fatalf("错误信息应包含 PGBOUNCER_LISTEN_PORT: %v", err)
	}
	if !strings.Contains(err.Error(), "PGBOUNCER_MAX_CLIENT_CONN") {
		t.Fatalf("错误信息应包含 PGBOUNCER_MAX_CLIENT_CONN: %v", err)
	}
	if !strings.Contains(err.Error(), "PGBOUNCER_POOL_MODE") {
		t.Fatalf("错误信息应包含 PGBOUNCER_POOL_MODE: %v", err)
	}
	if spec.ListenPort != DefaultPgBouncerListenPort {
		t.Fatalf("非法监听端口应回退默认值: %d", spec.ListenPort)
	}
	if spec.MaxClientConn != DefaultPgBouncerMaxClientConn {
		t.Fatalf("非法最大连接数应回退默认值: %d", spec.MaxClientConn)
	}
	if spec.PoolMode != DefaultPgBouncerPoolMode {
		t.Fatalf("非法连接池模式应回退默认值: %s", spec.PoolMode)
	}
}

func TestNormalizePgBouncerSpecRejectInvalid(t *testing.T) {
	_, err := NormalizePgBouncerSpec(PgBouncerSpec{
		Enabled:         true,
		DefaultPoolSize: 10,
		MinPoolSize:     12,
	})
	if err == nil {
		t.Fatalf("min_pool_size 大于 default_pool_size 应被拒绝")
	}
	if !strings.Contains(err.Error(), "min pool size") {
		t.Fatalf("错误信息应包含 min pool size: %v", err)
	}
}

func TestBuildPgBouncerLogDisabled(t *testing.T) {
	spec := DefaultPgBouncerSpec()
	spec.Enabled = false
	logLine := BuildPgBouncerLog(spec)
	if !strings.Contains(logLine, "PgBouncer 连接池未启用") {
		t.Fatalf("关闭日志应提示未启用: %s", logLine)
	}
}

func TestPgBouncerDefaultConstantsSanity(t *testing.T) {
	if DefaultPgBouncerListenPort <= 0 || DefaultPgBouncerListenPort > 65535 {
		t.Fatalf("默认监听端口越界: %d", DefaultPgBouncerListenPort)
	}
	if DefaultPgBouncerUpstreamPort <= 0 || DefaultPgBouncerUpstreamPort > 65535 {
		t.Fatalf("默认上游端口越界: %d", DefaultPgBouncerUpstreamPort)
	}
	if DefaultPgBouncerDefaultPoolSize <= 0 {
		t.Fatalf("默认连接池大小应大于 0")
	}
	if DefaultPgBouncerMaxClientConn < DefaultPgBouncerDefaultPoolSize {
		t.Fatalf(
			"默认 max_client_conn 应大于等于 default_pool_size: max=%s pool=%s",
			strconv.Itoa(int(DefaultPgBouncerMaxClientConn)),
			strconv.Itoa(int(DefaultPgBouncerDefaultPoolSize)),
		)
	}
}
