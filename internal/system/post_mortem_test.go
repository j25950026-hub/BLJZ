package system

import (
	"strings"
	"testing"
	"time"
)

func TestControlPoint555PostMortem(t *testing.T) {
	t.Run("控制点 5.5.5 功能验证", func(t *testing.T) {
		spec, err := LoadPostMortemSpecFromEnv(func(key string) string {
			switch key {
			case "POST_MORTEM_ENABLED":
				return "true"
			case "POST_MORTEM_SERVICE_NAME":
				return "ledgerbot-prod"
			case "POST_MORTEM_REVIEW_OWNER":
				return "sre-lead"
			case "POST_MORTEM_REVIEW_CHANNEL":
				return "#finance-incident"
			case "POST_MORTEM_RCA_METHOD":
				return "five_whys"
			case "POST_MORTEM_DEADLINE_HOURS":
				return "48"
			case "POST_MORTEM_MIN_ACTION_ITEMS":
				return "2"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatalf("加载事后复盘配置失败: %v", err)
		}
		if spec.ServiceName != "ledgerbot-prod" {
			t.Fatalf("服务名称解析错误: want=%q got=%q", "ledgerbot-prod", spec.ServiceName)
		}
		if spec.ReviewOwner != "sre-lead" {
			t.Fatalf("复盘负责人解析错误: want=%q got=%q", "sre-lead", spec.ReviewOwner)
		}
		if spec.RCAMethod != PostMortemRCAMethodFiveWhys {
			t.Fatalf("RCA 方法解析错误: want=%q got=%q", PostMortemRCAMethodFiveWhys, spec.RCAMethod)
		}
		if spec.DeadlineHours != 48 {
			t.Fatalf("复盘时限解析错误: want=%d got=%d", 48, spec.DeadlineHours)
		}

		startedAt := time.Date(2026, 2, 20, 8, 0, 0, 0, time.UTC)
		recoveredAt := startedAt.Add(32 * time.Minute)
		incident := PostMortemIncident{
			IncidentID:  "INC-20260220-001",
			Severity:    "S1",
			Summary:     "提现回调错误率飙升导致部分订单失败",
			StartedAt:   startedAt,
			RecoveredAt: recoveredAt,
			WhyChain: []string{
				"为什么提现失败？因为回调接口持续返回 502",
				"为什么返回 502？因为网关上游连接池耗尽",
				"为什么连接池耗尽？因为重试策略出现无限重试",
				"为什么会无限重试？因为缺少最大重试次数配置",
				"为什么配置缺失未被拦截？因为发布检查缺少该项校验",
			},
			Evidence: []PostMortemEvidence{
				{
					OccurredAt: startedAt.Add(8 * time.Minute),
					Source:     "metrics",
					Detail:     "/withdraw/callback 5xx rate=78%",
				},
				{
					OccurredAt: startedAt.Add(2 * time.Minute),
					Source:     "alerts",
					Detail:     "S1 告警触发：提现失败率超过阈值",
				},
				{
					OccurredAt: startedAt.Add(20 * time.Minute),
					Source:     "deploy",
					Detail:     "回滚至 v1.9.3 后错误率回落",
				},
			},
			ActionItems: []PostMortemActionItem{
				{
					Description: "为重试策略增加最大重试次数和配置校验",
					Owner:       "platform",
				},
				{
					Description: "将提现回调 5xx 阈值纳入发布前检查清单",
					Owner:       "sre",
					DueAt:       recoveredAt.Add(36 * time.Hour),
				},
			},
		}

		report, reportErr := BuildPostMortemReport(spec, incident)
		if reportErr != nil {
			t.Fatalf("构建事后复盘报告失败: %v", reportErr)
		}
		if report.RootCause != "为什么配置缺失未被拦截？因为发布检查缺少该项校验" {
			t.Fatalf("根因提炼错误: %s", report.RootCause)
		}
		if report.MTTR != 32*time.Minute {
			t.Fatalf("MTTR 计算错误: want=%s got=%s", 32*time.Minute, report.MTTR)
		}
		if len(report.Timeline) != 3 {
			t.Fatalf("证据时间线数量错误: want=%d got=%d", 3, len(report.Timeline))
		}
		if report.Timeline[0].Source != "alerts" {
			t.Fatalf("证据时间线应按时间排序: %+v", report.Timeline)
		}
		if len(report.ActionItems) != 2 {
			t.Fatalf("改进行动项数量错误: want=%d got=%d", 2, len(report.ActionItems))
		}
		if report.ActionItems[0].Owner != "sre" {
			t.Fatalf("行动项应按截止时间排序: %+v", report.ActionItems)
		}
		if report.ActionItems[1].DueAt != recoveredAt.Add(48*time.Hour) {
			t.Fatalf("缺省截止时间应基于复盘时限: %s", report.ActionItems[1].DueAt)
		}

		markdown, markdownErr := BuildPostMortemMarkdown(report)
		if markdownErr != nil {
			t.Fatalf("生成事后复盘文档失败: %v", markdownErr)
		}
		requiredMarkdownFragments := []string{
			"# INC-20260220-001 事后复盘报告",
			"## 事件摘要",
			"## 根因分析",
			"## 证据时间线",
			"## 改进行动项",
			"最终根因：为什么配置缺失未被拦截？因为发布检查缺少该项校验",
		}
		for _, fragment := range requiredMarkdownFragments {
			if !strings.Contains(markdown, fragment) {
				t.Fatalf("复盘文档缺少关键片段 %q\nmarkdown=%s", fragment, markdown)
			}
		}

		logLine := BuildPostMortemLog(spec)
		requiredLogFragments := []string{
			"事后复盘已启用",
			"service=ledgerbot-prod",
			"owner=sre-lead",
			"channel=#finance-incident",
			"method=5-whys",
			"deadline<=48h",
			"min_actions=2",
		}
		for _, fragment := range requiredLogFragments {
			if !strings.Contains(logLine, fragment) {
				t.Fatalf("复盘日志缺少关键片段 %q: %s", fragment, logLine)
			}
		}
	})
}

func TestLoadPostMortemSpecFromEnvFallback(t *testing.T) {
	spec, err := LoadPostMortemSpecFromEnv(func(key string) string {
		switch key {
		case "POST_MORTEM_ENABLED":
			return "unknown"
		case "POST_MORTEM_RCA_METHOD":
			return "invalid-method"
		case "POST_MORTEM_DEADLINE_HOURS":
			return "bad"
		case "POST_MORTEM_MIN_ACTION_ITEMS":
			return "0"
		default:
			return ""
		}
	})
	if err == nil {
		t.Fatalf("非法事后复盘配置应返回回退告警")
	}
	requiredErrorFragments := []string{
		"POST_MORTEM_ENABLED",
		"POST_MORTEM_RCA_METHOD",
		"POST_MORTEM_DEADLINE_HOURS",
		"POST_MORTEM_MIN_ACTION_ITEMS",
	}
	for _, fragment := range requiredErrorFragments {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("错误信息缺少变量名 %q: %v", fragment, err)
		}
	}
	if spec.RCAMethod != DefaultPostMortemRCAMethod {
		t.Fatalf("非法 RCA 方法应回退默认值: want=%s got=%s", DefaultPostMortemRCAMethod, spec.RCAMethod)
	}
	if spec.DeadlineHours != DefaultPostMortemDeadlineHours {
		t.Fatalf(
			"非法复盘时限应回退默认值: want=%d got=%d",
			DefaultPostMortemDeadlineHours,
			spec.DeadlineHours,
		)
	}
	if spec.MinActionItems != DefaultPostMortemMinActionItems {
		t.Fatalf(
			"非法行动项数量应回退默认值: want=%d got=%d",
			DefaultPostMortemMinActionItems,
			spec.MinActionItems,
		)
	}
}

func TestBuildPostMortemDisabled(t *testing.T) {
	spec := DefaultPostMortemSpec()
	spec.Enabled = false

	report, err := BuildPostMortemReport(spec, PostMortemIncident{})
	if err != nil {
		t.Fatalf("禁用事后复盘时构建报告不应报错: %v", err)
	}
	if report.Enabled {
		t.Fatalf("禁用状态下报告应标记为未启用")
	}
	if report.RootCause != "" {
		t.Fatalf("禁用状态下不应产出根因: %s", report.RootCause)
	}

	markdown, markdownErr := BuildPostMortemMarkdown(report)
	if markdownErr != nil {
		t.Fatalf("禁用状态下生成文档不应报错: %v", markdownErr)
	}
	if !strings.Contains(markdown, "POST_MORTEM_ENABLED=false") {
		t.Fatalf("禁用状态文档应提示关闭: %s", markdown)
	}

	logLine := BuildPostMortemLog(spec)
	if !strings.Contains(logLine, "事后复盘未启用") {
		t.Fatalf("禁用状态日志应提示未启用: %s", logLine)
	}
}

func TestBuildPostMortemReportRequiresFiveWhysDepth(t *testing.T) {
	spec := DefaultPostMortemSpec()
	incident := PostMortemIncident{
		IncidentID:  "INC-1",
		Severity:    "S2",
		Summary:     "callback errors",
		StartedAt:   time.Date(2026, 2, 20, 9, 0, 0, 0, time.UTC),
		RecoveredAt: time.Date(2026, 2, 20, 9, 30, 0, 0, time.UTC),
		WhyChain: []string{
			"why-1",
			"why-2",
			"why-3",
		},
		Evidence: []PostMortemEvidence{
			{
				OccurredAt: time.Date(2026, 2, 20, 9, 1, 0, 0, time.UTC),
				Source:     "alerts",
				Detail:     "s2 alert fired",
			},
		},
		ActionItems: []PostMortemActionItem{
			{Description: "update checklist", Owner: "sre"},
			{Description: "fix retry", Owner: "backend"},
			{Description: "add integration test", Owner: "qa"},
		},
	}

	_, err := BuildPostMortemReport(spec, incident)
	if err == nil {
		t.Fatalf("5-whys 深度不足应返回错误")
	}
	if !strings.Contains(err.Error(), "why chain") {
		t.Fatalf("错误信息应包含 why chain: %v", err)
	}
}
