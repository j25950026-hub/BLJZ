package system

import (
	"strings"
	"testing"
)

func TestControlPoint543DBTuning(t *testing.T) {
	t.Run("控制点 5.4.3 功能验证", func(t *testing.T) {
		spec, err := LoadDBTuningSpecFromEnv(func(key string) string {
			switch key {
			case "PG_SHARED_BUFFERS":
				return "1gb"
			case "PG_WORK_MEM":
				return "32mb"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("加载 DB 调优配置失败: %v", err)
		}
		if spec.SharedBuffers != "1GB" {
			t.Fatalf("shared_buffers 解析错误: %s", spec.SharedBuffers)
		}
		if spec.WorkMem != "32MB" {
			t.Fatalf("work_mem 解析错误: %s", spec.WorkMem)
		}

		sqlText, err := BuildDBTuningSQL(spec)
		if err != nil {
			t.Fatalf("生成 DB 调优 SQL 失败: %v", err)
		}
		requiredSQL := []string{
			"ALTER SYSTEM SET shared_buffers = '1GB';",
			"ALTER SYSTEM SET work_mem = '32MB';",
			"SELECT pg_reload_conf();",
		}
		for _, fragment := range requiredSQL {
			if !strings.Contains(sqlText, fragment) {
				t.Fatalf("DB 调优 SQL 缺少关键片段 %q: %s", fragment, sqlText)
			}
		}

		logLine := BuildDBTuningLog(spec)
		requiredLog := []string{
			"PostgreSQL 调优已定义",
			"shared_buffers=1GB",
			"work_mem=32MB",
			"shared_buffers 需重启生效",
		}
		for _, fragment := range requiredLog {
			if !strings.Contains(logLine, fragment) {
				t.Fatalf("DB 调优日志缺少关键片段 %q: %s", fragment, logLine)
			}
		}
	})
}

func TestLoadDBTuningSpecFromEnvFallback(t *testing.T) {
	spec, err := LoadDBTuningSpecFromEnv(func(key string) string {
		switch key {
		case "PG_SHARED_BUFFERS":
			return "4096"
		case "PG_WORK_MEM":
			return "invalid"
		default:
			return ""
		}
	})
	if err == nil {
		t.Fatalf("非法 DB 调优配置应返回回退告警")
	}
	if !strings.Contains(err.Error(), "PG_SHARED_BUFFERS") {
		t.Fatalf("错误信息应包含 PG_SHARED_BUFFERS: %v", err)
	}
	if !strings.Contains(err.Error(), "PG_WORK_MEM") {
		t.Fatalf("错误信息应包含 PG_WORK_MEM: %v", err)
	}
	if spec.SharedBuffers != DefaultDBTuningSharedBuffers {
		t.Fatalf("非法 shared_buffers 应回退默认值: %s", spec.SharedBuffers)
	}
	if spec.WorkMem != DefaultDBTuningWorkMem {
		t.Fatalf("非法 work_mem 应回退默认值: %s", spec.WorkMem)
	}
}

func TestNormalizeDBTuningSpecRejectInvalid(t *testing.T) {
	_, err := NormalizeDBTuningSpec(DBTuningSpec{
		SharedBuffers: "512mb",
		WorkMem:       "128",
	})
	if err == nil {
		t.Fatalf("非法 work_mem 配置应被拒绝")
	}
	if !strings.Contains(err.Error(), "work_mem") {
		t.Fatalf("错误信息应包含 work_mem: %v", err)
	}
}

func TestBuildDBTuningLogFallback(t *testing.T) {
	logLine := BuildDBTuningLog(DBTuningSpec{
		SharedBuffers: "bad-value",
	})
	if !strings.Contains(logLine, "PostgreSQL 调优未启用") {
		t.Fatalf("非法配置日志应提示未启用: %s", logLine)
	}
}
