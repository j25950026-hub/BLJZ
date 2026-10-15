package system

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
)

const (
	DefaultDBTuningSharedBuffers = "512MB"
	DefaultDBTuningWorkMem       = "16MB"
)

var pgMemorySizePattern = regexp.MustCompile(`(?i)^([1-9][0-9]*)\s*(KB|MB|GB|TB)$`)

type DBTuningSpec struct {
	SharedBuffers string
	WorkMem       string
}

func DefaultDBTuningSpec() DBTuningSpec {
	return DBTuningSpec{
		SharedBuffers: DefaultDBTuningSharedBuffers,
		WorkMem:       DefaultDBTuningWorkMem,
	}
}

func NormalizeDBTuningSpec(spec DBTuningSpec) (DBTuningSpec, error) {
	normalized := DefaultDBTuningSpec()

	if sharedBuffersRaw := strings.TrimSpace(spec.SharedBuffers); sharedBuffersRaw != "" {
		sharedBuffers, err := normalizePGMemorySetting(sharedBuffersRaw)
		if err != nil {
			return DBTuningSpec{}, fmt.Errorf("shared_buffers: %w", err)
		}
		normalized.SharedBuffers = sharedBuffers
	}
	if workMemRaw := strings.TrimSpace(spec.WorkMem); workMemRaw != "" {
		workMem, err := normalizePGMemorySetting(workMemRaw)
		if err != nil {
			return DBTuningSpec{}, fmt.Errorf("work_mem: %w", err)
		}
		normalized.WorkMem = workMem
	}

	return normalized, nil
}

func LoadDBTuningSpecFromEnv(getenv func(string) string) (DBTuningSpec, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	spec := DefaultDBTuningSpec()
	warnings := make([]string, 0, 2)

	sharedBuffersRaw := strings.TrimSpace(getenv("PG_SHARED_BUFFERS"))
	if sharedBuffersRaw != "" {
		sharedBuffers, err := normalizePGMemorySetting(sharedBuffersRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"PG_SHARED_BUFFERS=%q 无效，回退默认值 %s: %v",
					sharedBuffersRaw,
					DefaultDBTuningSharedBuffers,
					err,
				),
			)
		} else {
			spec.SharedBuffers = sharedBuffers
		}
	}

	workMemRaw := strings.TrimSpace(getenv("PG_WORK_MEM"))
	if workMemRaw != "" {
		workMem, err := normalizePGMemorySetting(workMemRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"PG_WORK_MEM=%q 无效，回退默认值 %s: %v",
					workMemRaw,
					DefaultDBTuningWorkMem,
					err,
				),
			)
		} else {
			spec.WorkMem = workMem
		}
	}

	normalized, err := NormalizeDBTuningSpec(spec)
	if err != nil {
		return DefaultDBTuningSpec(), fmt.Errorf("PostgreSQL 调优配置非法，回退默认值: %w", err)
	}
	if len(warnings) > 0 {
		return normalized, errors.New(strings.Join(warnings, "; "))
	}
	return normalized, nil
}

func BuildDBTuningSQL(spec DBTuningSpec) (string, error) {
	normalized, err := NormalizeDBTuningSpec(spec)
	if err != nil {
		return "", err
	}
	sqlText := fmt.Sprintf(
		"ALTER SYSTEM SET shared_buffers = '%s';\nALTER SYSTEM SET work_mem = '%s';\nSELECT pg_reload_conf();",
		normalized.SharedBuffers,
		normalized.WorkMem,
	)
	return sqlText, nil
}

func BuildDBTuningLog(spec DBTuningSpec) string {
	normalized, err := NormalizeDBTuningSpec(spec)
	if err != nil {
		return fmt.Sprintf("PostgreSQL 调优未启用：配置非法 %v", err)
	}
	return fmt.Sprintf(
		"PostgreSQL 调优已定义：shared_buffers=%s work_mem=%s apply=ALTER SYSTEM+pg_reload_conf note=shared_buffers 需重启生效",
		normalized.SharedBuffers,
		normalized.WorkMem,
	)
}

func normalizePGMemorySetting(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("value must not be empty")
	}
	match := pgMemorySizePattern.FindStringSubmatch(trimmed)
	if len(match) != 3 {
		return "", fmt.Errorf("value must match pattern <number><unit>, e.g. 512MB/1GB/64KB")
	}
	return match[1] + strings.ToUpper(match[2]), nil
}
