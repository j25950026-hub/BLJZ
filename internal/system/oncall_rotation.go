package system

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultOnCallRotationEnabled     = true
	DefaultOnCallRotationTimezone    = "Asia/Shanghai"
	DefaultOnCallRotationHandoffHour = int32(0)
	DefaultOnCallRotationShiftHours  = int32(24)
	DefaultOnCallRotationChannel     = "#ledgerbot-oncall"
	maxOnCallRotationEngineers       = 64
)

var defaultOnCallRotationEngineers = []string{"alice", "bob", "carol"}

type OnCallRotationSpec struct {
	Enabled     bool
	Timezone    string
	HandoffHour int32
	Engineers   []string
	Channel     string
}

type OnCallShift struct {
	Engineer string
	StartAt  time.Time
	EndAt    time.Time
}

func DefaultOnCallRotationSpec() OnCallRotationSpec {
	return OnCallRotationSpec{
		Enabled:     DefaultOnCallRotationEnabled,
		Timezone:    DefaultOnCallRotationTimezone,
		HandoffHour: DefaultOnCallRotationHandoffHour,
		Engineers:   cloneOnCallEngineers(defaultOnCallRotationEngineers),
		Channel:     DefaultOnCallRotationChannel,
	}
}

func NormalizeOnCallRotationSpec(spec OnCallRotationSpec) (OnCallRotationSpec, error) {
	normalized := DefaultOnCallRotationSpec()
	normalized.Enabled = spec.Enabled

	if timezone := strings.TrimSpace(spec.Timezone); timezone != "" {
		normalized.Timezone = timezone
	}
	if channel := strings.TrimSpace(spec.Channel); channel != "" {
		normalized.Channel = channel
	}
	if spec.HandoffHour != 0 {
		normalized.HandoffHour = spec.HandoffHour
	}
	if len(spec.Engineers) > 0 {
		engineers, err := normalizeOnCallEngineers(spec.Engineers)
		if err != nil {
			return OnCallRotationSpec{}, err
		}
		normalized.Engineers = engineers
	}

	if normalized.Timezone == "" {
		return OnCallRotationSpec{}, fmt.Errorf("timezone must not be empty")
	}
	if _, err := time.LoadLocation(normalized.Timezone); err != nil {
		return OnCallRotationSpec{}, fmt.Errorf("timezone is invalid: %w", err)
	}
	if normalized.HandoffHour < 0 || normalized.HandoffHour >= DefaultOnCallRotationShiftHours {
		return OnCallRotationSpec{}, fmt.Errorf("handoff hour must be in range [0,23]")
	}
	if strings.TrimSpace(normalized.Channel) == "" {
		return OnCallRotationSpec{}, fmt.Errorf("channel must not be empty")
	}
	engineers, err := normalizeOnCallEngineers(normalized.Engineers)
	if err != nil {
		return OnCallRotationSpec{}, err
	}
	normalized.Engineers = engineers

	return normalized, nil
}

func LoadOnCallRotationSpecFromEnv(getenv func(string) string) (OnCallRotationSpec, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	spec := DefaultOnCallRotationSpec()
	warnings := make([]string, 0, 5)

	enabledRaw := strings.TrimSpace(getenv("ONCALL_ROTATION_ENABLED"))
	if enabledRaw != "" {
		enabled, err := parseOnCallRotationBool(enabledRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"ONCALL_ROTATION_ENABLED=%q 无效，回退默认值 %t: %v",
					enabledRaw,
					DefaultOnCallRotationEnabled,
					err,
				),
			)
		} else {
			spec.Enabled = enabled
		}
	}

	timezoneRaw := strings.TrimSpace(getenv("ONCALL_TIMEZONE"))
	if timezoneRaw != "" {
		if _, err := time.LoadLocation(timezoneRaw); err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"ONCALL_TIMEZONE=%q 无效，回退默认值 %s: %v",
					timezoneRaw,
					DefaultOnCallRotationTimezone,
					err,
				),
			)
		} else {
			spec.Timezone = timezoneRaw
		}
	}

	handoffRaw := strings.TrimSpace(getenv("ONCALL_HANDOFF_HOUR"))
	if handoffRaw != "" {
		hour, err := parseOnCallHandoffHour(handoffRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"ONCALL_HANDOFF_HOUR=%q 无效，回退默认值 %d: %v",
					handoffRaw,
					DefaultOnCallRotationHandoffHour,
					err,
				),
			)
		} else {
			spec.HandoffHour = hour
		}
	}

	if channelRaw := strings.TrimSpace(getenv("ONCALL_CHANNEL")); channelRaw != "" {
		spec.Channel = channelRaw
	}

	engineersRaw := strings.TrimSpace(getenv("ONCALL_ENGINEERS"))
	if engineersRaw != "" {
		engineers, err := parseOnCallEngineers(engineersRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"ONCALL_ENGINEERS=%q 无效，回退默认值 %s: %v",
					engineersRaw,
					strings.Join(defaultOnCallRotationEngineers, ","),
					err,
				),
			)
		} else {
			spec.Engineers = engineers
		}
	}

	normalized, err := NormalizeOnCallRotationSpec(spec)
	if err != nil {
		return DefaultOnCallRotationSpec(), fmt.Errorf("On-Call 轮值配置非法，回退默认值: %w", err)
	}
	if len(warnings) > 0 {
		return normalized, errors.New(strings.Join(warnings, "; "))
	}
	return normalized, nil
}

func ResolveOnCallEngineer(spec OnCallRotationSpec, at time.Time) (OnCallShift, error) {
	normalized, err := NormalizeOnCallRotationSpec(spec)
	if err != nil {
		return OnCallShift{}, err
	}
	return resolveOnCallShift(normalized, at)
}

func BuildOnCallRotationSchedule(spec OnCallRotationSpec, windowStart time.Time, days int32) ([]OnCallShift, error) {
	normalized, err := NormalizeOnCallRotationSpec(spec)
	if err != nil {
		return nil, err
	}
	if !normalized.Enabled {
		return []OnCallShift{}, nil
	}
	if days <= 0 {
		return nil, fmt.Errorf("days must be greater than 0")
	}

	location, _ := time.LoadLocation(normalized.Timezone)
	localWindowStart := windowStart.In(location)
	shiftDuration := time.Duration(DefaultOnCallRotationShiftHours) * time.Hour

	firstShiftStart := time.Date(
		localWindowStart.Year(),
		localWindowStart.Month(),
		localWindowStart.Day(),
		int(normalized.HandoffHour),
		0,
		0,
		0,
		location,
	)
	if firstShiftStart.After(localWindowStart) {
		firstShiftStart = firstShiftStart.Add(-shiftDuration)
	}

	shifts := make([]OnCallShift, 0, days)
	for day := int32(0); day < days; day++ {
		shiftTime := firstShiftStart.Add(time.Duration(day) * shiftDuration)
		shift, shiftErr := resolveOnCallShift(normalized, shiftTime)
		if shiftErr != nil {
			return nil, shiftErr
		}
		shifts = append(shifts, shift)
	}
	return shifts, nil
}

func BuildOnCallRotationLog(spec OnCallRotationSpec) string {
	normalized, err := NormalizeOnCallRotationSpec(spec)
	if err != nil {
		return fmt.Sprintf("On-Call 轮值未启用：配置非法 %v", err)
	}
	if !normalized.Enabled {
		return "On-Call 轮值未启用：ONCALL_ROTATION_ENABLED=false"
	}

	return fmt.Sprintf(
		"On-Call 轮值已启用：timezone=%s handoff=%02d:00 shift=%dh engineers=%d[%s] channel=%s coverage=24x7",
		normalized.Timezone,
		normalized.HandoffHour,
		DefaultOnCallRotationShiftHours,
		len(normalized.Engineers),
		strings.Join(normalized.Engineers, ","),
		normalized.Channel,
	)
}

func resolveOnCallShift(normalized OnCallRotationSpec, at time.Time) (OnCallShift, error) {
	if !normalized.Enabled {
		return OnCallShift{}, fmt.Errorf("on-call rotation is disabled")
	}
	location, _ := time.LoadLocation(normalized.Timezone)
	localAt := at.In(location)

	shiftDuration := time.Duration(DefaultOnCallRotationShiftHours) * time.Hour
	anchor := time.Date(2024, 1, 1, int(normalized.HandoffHour), 0, 0, 0, location)
	shiftIndex := floorDivideInt64(
		localAt.Unix()-anchor.Unix(),
		int64(shiftDuration/time.Second),
	)

	engineerIndex := positiveModulo(shiftIndex, int64(len(normalized.Engineers)))
	shiftStart := anchor.Add(time.Duration(shiftIndex) * shiftDuration)
	shiftEnd := shiftStart.Add(shiftDuration)

	return OnCallShift{
		Engineer: normalized.Engineers[engineerIndex],
		StartAt:  shiftStart,
		EndAt:    shiftEnd,
	}, nil
}

func parseOnCallRotationBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "t", "yes", "y", "on":
		return true, nil
	case "0", "false", "f", "no", "n", "off":
		return false, nil
	default:
		return false, fmt.Errorf("must be a boolean")
	}
}

func parseOnCallHandoffHour(raw string) (int32, error) {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, err
	}
	if value < 0 || value >= int(DefaultOnCallRotationShiftHours) {
		return 0, fmt.Errorf("must be in range [0,23]")
	}
	return int32(value), nil
}

func parseOnCallEngineers(raw string) ([]string, error) {
	return normalizeOnCallEngineers(strings.Split(raw, ","))
}

func normalizeOnCallEngineers(values []string) ([]string, error) {
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	if len(normalized) < 2 {
		return nil, fmt.Errorf("engineers must contain at least 2 members")
	}
	if len(normalized) > maxOnCallRotationEngineers {
		return nil, fmt.Errorf("engineers must be less than or equal to %d members", maxOnCallRotationEngineers)
	}
	return normalized, nil
}

func cloneOnCallEngineers(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
}

func floorDivideInt64(dividend, divisor int64) int64 {
	if divisor == 0 {
		return 0
	}
	quotient := dividend / divisor
	remainder := dividend % divisor
	if remainder != 0 && ((remainder < 0) != (divisor < 0)) {
		quotient--
	}
	return quotient
}

func positiveModulo(value, modulus int64) int {
	if modulus <= 0 {
		return 0
	}
	result := value % modulus
	if result < 0 {
		result += modulus
	}
	return int(result)
}
