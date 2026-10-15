package system

import (
	"strings"
	"testing"
	"time"
)

func TestControlPoint552OnCallRotation(t *testing.T) {
	t.Run("控制点 5.5.2 功能验证", func(t *testing.T) {
		spec, err := LoadOnCallRotationSpecFromEnv(func(key string) string {
			switch key {
			case "ONCALL_ROTATION_ENABLED":
				return "true"
			case "ONCALL_TIMEZONE":
				return "Asia/Shanghai"
			case "ONCALL_HANDOFF_HOUR":
				return "9"
			case "ONCALL_ENGINEERS":
				return "alice,bob,carol"
			case "ONCALL_CHANNEL":
				return "#finance-oncall"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("加载 on-call 配置失败: %v", err)
		}
		if spec.Timezone != "Asia/Shanghai" {
			t.Fatalf("时区解析错误: want=%q got=%q", "Asia/Shanghai", spec.Timezone)
		}
		if spec.HandoffHour != 9 {
			t.Fatalf("交接班小时解析错误: want=%d got=%d", 9, spec.HandoffHour)
		}
		if spec.Channel != "#finance-oncall" {
			t.Fatalf("on-call 频道解析错误: want=%q got=%q", "#finance-oncall", spec.Channel)
		}
		if len(spec.Engineers) != 3 {
			t.Fatalf("轮值工程师数量错误: want=%d got=%d", 3, len(spec.Engineers))
		}

		location, locationErr := time.LoadLocation(spec.Timezone)
		if locationErr != nil {
			t.Fatalf("加载时区失败: %v", locationErr)
		}
		firstProbe := time.Date(2024, 1, 1, 10, 0, 0, 0, location)

		firstShift, resolveErr := ResolveOnCallEngineer(spec, firstProbe)
		if resolveErr != nil {
			t.Fatalf("解析首班 on-call 失败: %v", resolveErr)
		}
		if firstShift.Engineer != "alice" {
			t.Fatalf("首班工程师错误: want=%q got=%q", "alice", firstShift.Engineer)
		}
		if firstShift.EndAt.Sub(firstShift.StartAt) != 24*time.Hour {
			t.Fatalf("首班时长错误: want=%s got=%s", 24*time.Hour, firstShift.EndAt.Sub(firstShift.StartAt))
		}

		secondShift, resolveSecondErr := ResolveOnCallEngineer(spec, firstProbe.Add(24*time.Hour))
		if resolveSecondErr != nil {
			t.Fatalf("解析次班 on-call 失败: %v", resolveSecondErr)
		}
		if secondShift.Engineer != "bob" {
			t.Fatalf("次班工程师错误: want=%q got=%q", "bob", secondShift.Engineer)
		}

		thirdShift, resolveThirdErr := ResolveOnCallEngineer(spec, firstProbe.Add(48*time.Hour))
		if resolveThirdErr != nil {
			t.Fatalf("解析第三班 on-call 失败: %v", resolveThirdErr)
		}
		if thirdShift.Engineer != "carol" {
			t.Fatalf("第三班工程师错误: want=%q got=%q", "carol", thirdShift.Engineer)
		}

		schedule, scheduleErr := BuildOnCallRotationSchedule(spec, firstProbe, 3)
		if scheduleErr != nil {
			t.Fatalf("构建 on-call 轮值排班失败: %v", scheduleErr)
		}
		if len(schedule) != 3 {
			t.Fatalf("轮值排班数量错误: want=%d got=%d", 3, len(schedule))
		}
		expectedOrder := []string{"alice", "bob", "carol"}
		for idx, expectedEngineer := range expectedOrder {
			if schedule[idx].Engineer != expectedEngineer {
				t.Fatalf("轮值顺序错误 idx=%d want=%q got=%q", idx, expectedEngineer, schedule[idx].Engineer)
			}
			if schedule[idx].EndAt.Sub(schedule[idx].StartAt) != 24*time.Hour {
				t.Fatalf(
					"轮值时长错误 idx=%d want=%s got=%s",
					idx,
					24*time.Hour,
					schedule[idx].EndAt.Sub(schedule[idx].StartAt),
				)
			}
		}

		logLine := BuildOnCallRotationLog(spec)
		required := []string{
			"On-Call 轮值已启用",
			"timezone=Asia/Shanghai",
			"handoff=09:00",
			"shift=24h",
			"engineers=3[alice,bob,carol]",
			"channel=#finance-oncall",
			"coverage=24x7",
		}
		for _, fragment := range required {
			if !strings.Contains(logLine, fragment) {
				t.Fatalf("on-call 日志缺少关键片段 %q: %s", fragment, logLine)
			}
		}
	})
}

func TestLoadOnCallRotationSpecFromEnvFallback(t *testing.T) {
	spec, err := LoadOnCallRotationSpecFromEnv(func(key string) string {
		switch key {
		case "ONCALL_HANDOFF_HOUR":
			return "24"
		case "ONCALL_ENGINEERS":
			return "solo"
		default:
			return ""
		}
	})
	if err == nil {
		t.Fatalf("非法 on-call 配置应返回回退告警")
	}
	if !strings.Contains(err.Error(), "ONCALL_HANDOFF_HOUR") {
		t.Fatalf("错误信息应包含非法变量名: %v", err)
	}
	if !strings.Contains(err.Error(), "ONCALL_ENGINEERS") {
		t.Fatalf("错误信息应包含非法变量名: %v", err)
	}
	if spec.HandoffHour != DefaultOnCallRotationHandoffHour {
		t.Fatalf(
			"非法交接小时应回退默认值: want=%d got=%d",
			DefaultOnCallRotationHandoffHour,
			spec.HandoffHour,
		)
	}
	if len(spec.Engineers) != len(defaultOnCallRotationEngineers) {
		t.Fatalf("非法工程师列表应回退默认值: %+v", spec.Engineers)
	}
}

func TestBuildOnCallRotationDisabled(t *testing.T) {
	spec := DefaultOnCallRotationSpec()
	spec.Enabled = false

	schedule, err := BuildOnCallRotationSchedule(spec, time.Now(), 2)
	if err != nil {
		t.Fatalf("禁用 on-call 时构建排班不应报错: %v", err)
	}
	if len(schedule) != 0 {
		t.Fatalf("禁用 on-call 时不应产出排班: %+v", schedule)
	}

	_, resolveErr := ResolveOnCallEngineer(spec, time.Now())
	if resolveErr == nil {
		t.Fatalf("禁用 on-call 时解析值班工程师应报错")
	}

	logLine := BuildOnCallRotationLog(spec)
	if !strings.Contains(logLine, "On-Call 轮值未启用") {
		t.Fatalf("禁用 on-call 日志应提示未启用: %s", logLine)
	}
}
