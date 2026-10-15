package system

import (
	"strings"
	"testing"
)

func TestControlPoint551Runbook(t *testing.T) {
	t.Run("控制点 5.5.1 功能验证", func(t *testing.T) {
		spec, err := LoadRunbookSpecFromEnv(func(key string) string {
			switch key {
			case "RUNBOOK_ENABLED":
				return "true"
			case "RUNBOOK_SERVICE_NAME":
				return "ledgerbot-prod"
			case "RUNBOOK_INCIDENT_CHANNEL":
				return "#finance-incident"
			case "RUNBOOK_ACK_MINUTES":
				return "3"
			case "RUNBOOK_MITIGATION_MINUTES":
				return "12"
			case "RUNBOOK_STATUS_UPDATE_MINUTES":
				return "6"
			case "RUNBOOK_POSTMORTEM_DEADLINE_HOURS":
				return "24"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("加载 runbook 配置失败: %v", err)
		}
		if spec.ServiceName != "ledgerbot-prod" {
			t.Fatalf("服务名称错误: want=%q got=%q", "ledgerbot-prod", spec.ServiceName)
		}
		if spec.IncidentChannel != "#finance-incident" {
			t.Fatalf("故障频道错误: want=%q got=%q", "#finance-incident", spec.IncidentChannel)
		}
		if spec.AcknowledgeMinutes != 3 || spec.MitigationMinutes != 12 {
			t.Fatalf("故障时限解析错误: %+v", spec)
		}

		runbook, err := BuildIncidentRunbook(spec)
		if err != nil {
			t.Fatalf("构建 runbook 失败: %v", err)
		}
		if len(runbook.Steps) != 5 {
			t.Fatalf("标准化故障流程步骤数量错误: want=%d got=%d", 5, len(runbook.Steps))
		}
		if runbook.Steps[0].Phase != "1. 故障受理与分级" {
			t.Fatalf("首个流程阶段错误: %+v", runbook.Steps[0])
		}
		if !strings.Contains(runbook.Steps[1].SLA, "12分钟") {
			t.Fatalf("止损阶段 SLA 错误: %s", runbook.Steps[1].SLA)
		}
		if !strings.Contains(runbook.Steps[4].SLA, "24小时") {
			t.Fatalf("复盘阶段 SLA 错误: %s", runbook.Steps[4].SLA)
		}

		markdown, err := BuildIncidentRunbookMarkdown(spec)
		if err != nil {
			t.Fatalf("生成 runbook 文档失败: %v", err)
		}
		required := []string{
			"# ledgerbot-prod 故障处理 Runbook",
			"## 1. 故障受理与分级",
			"## 2. 止损与隔离",
			"## 5. 复盘与改进",
			"每6分钟同步故障状态和下一步计划",
			"24小时内完成复盘文档",
		}
		for _, fragment := range required {
			if !strings.Contains(markdown, fragment) {
				t.Fatalf("runbook 文档缺少关键片段 %q\nmarkdown=%s", fragment, markdown)
			}
		}

		logLine := BuildRunbookLog(spec)
		if !strings.Contains(logLine, "Runbook(操作手册)已启用") {
			t.Fatalf("runbook 启用日志错误: %s", logLine)
		}
		if !strings.Contains(logLine, "steps=5") {
			t.Fatalf("runbook 日志缺少步骤计数: %s", logLine)
		}
		if !strings.Contains(logLine, "ack<=3min") {
			t.Fatalf("runbook 日志缺少时限信息: %s", logLine)
		}
	})
}

func TestLoadRunbookSpecFromEnvFallback(t *testing.T) {
	spec, err := LoadRunbookSpecFromEnv(func(key string) string {
		switch key {
		case "RUNBOOK_ACK_MINUTES":
			return "invalid"
		case "RUNBOOK_STATUS_UPDATE_MINUTES":
			return "-3"
		default:
			return ""
		}
	})
	if err == nil {
		t.Fatalf("非法 runbook 配置应返回回退告警")
	}
	if !strings.Contains(err.Error(), "RUNBOOK_ACK_MINUTES") {
		t.Fatalf("错误信息应包含非法变量名: %v", err)
	}
	if !strings.Contains(err.Error(), "RUNBOOK_STATUS_UPDATE_MINUTES") {
		t.Fatalf("错误信息应包含非法变量名: %v", err)
	}
	if spec.AcknowledgeMinutes != DefaultRunbookAcknowledgeMinutes {
		t.Fatalf(
			"非法 ACK 时限应回退默认值: want=%d got=%d",
			DefaultRunbookAcknowledgeMinutes,
			spec.AcknowledgeMinutes,
		)
	}
	if spec.StatusUpdateMinutes != DefaultRunbookStatusUpdateMinutes {
		t.Fatalf(
			"非法播报时限应回退默认值: want=%d got=%d",
			DefaultRunbookStatusUpdateMinutes,
			spec.StatusUpdateMinutes,
		)
	}
}

func TestNormalizeRunbookSpecRejectsInvalidSLAOrder(t *testing.T) {
	_, err := NormalizeRunbookSpec(RunbookSpec{
		Enabled:               true,
		ServiceName:           "ledgerbot",
		IncidentChannel:       "#incident",
		AcknowledgeMinutes:    20,
		MitigationMinutes:     10,
		StatusUpdateMinutes:   5,
		PostmortemDeadlineHrs: 24,
	})
	if err == nil {
		t.Fatalf("ack 大于 mitigation 应被拒绝")
	}
	if !strings.Contains(err.Error(), "acknowledge minutes") {
		t.Fatalf("错误信息应包含 acknowledge minutes: %v", err)
	}
}

func TestBuildRunbookDisabled(t *testing.T) {
	spec := DefaultRunbookSpec()
	spec.Enabled = false

	runbook, err := BuildIncidentRunbook(spec)
	if err != nil {
		t.Fatalf("构建禁用 runbook 不应报错: %v", err)
	}
	if len(runbook.Steps) != 0 {
		t.Fatalf("禁用 runbook 不应产出流程步骤: %+v", runbook.Steps)
	}

	markdown, err := BuildIncidentRunbookMarkdown(spec)
	if err != nil {
		t.Fatalf("禁用 runbook 文档构建不应报错: %v", err)
	}
	if !strings.Contains(markdown, "RUNBOOK_ENABLED=false") {
		t.Fatalf("禁用 runbook 文档应提示关闭状态: %s", markdown)
	}

	logLine := BuildRunbookLog(spec)
	if !strings.Contains(logLine, "Runbook(操作手册)未启用") {
		t.Fatalf("禁用 runbook 日志应提示未启用: %s", logLine)
	}
}
