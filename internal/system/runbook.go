package system

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

const (
	DefaultRunbookEnabled               = true
	DefaultRunbookServiceName           = "ledgerbot"
	DefaultRunbookIncidentChannel       = "#ledgerbot-incident"
	DefaultRunbookAcknowledgeMinutes    = int32(5)
	DefaultRunbookMitigationMinutes     = int32(15)
	DefaultRunbookStatusUpdateMinutes   = int32(10)
	DefaultRunbookPostmortemDeadlineHrs = int32(24)
	maxRunbookPostmortemDeadlineHours   = int32(168)
)

type RunbookSpec struct {
	Enabled               bool
	ServiceName           string
	IncidentChannel       string
	AcknowledgeMinutes    int32
	MitigationMinutes     int32
	StatusUpdateMinutes   int32
	PostmortemDeadlineHrs int32
}

type RunbookStep struct {
	Phase        string
	Objective    string
	SLA          string
	Actions      []string
	ExitCriteria string
}

type IncidentRunbook struct {
	ServiceName     string
	IncidentChannel string
	Steps           []RunbookStep
}

func DefaultRunbookSpec() RunbookSpec {
	return RunbookSpec{
		Enabled:               DefaultRunbookEnabled,
		ServiceName:           DefaultRunbookServiceName,
		IncidentChannel:       DefaultRunbookIncidentChannel,
		AcknowledgeMinutes:    DefaultRunbookAcknowledgeMinutes,
		MitigationMinutes:     DefaultRunbookMitigationMinutes,
		StatusUpdateMinutes:   DefaultRunbookStatusUpdateMinutes,
		PostmortemDeadlineHrs: DefaultRunbookPostmortemDeadlineHrs,
	}
}

func NormalizeRunbookSpec(spec RunbookSpec) (RunbookSpec, error) {
	normalized := DefaultRunbookSpec()
	normalized.Enabled = spec.Enabled

	if serviceName := strings.TrimSpace(spec.ServiceName); serviceName != "" {
		normalized.ServiceName = serviceName
	}
	if incidentChannel := strings.TrimSpace(spec.IncidentChannel); incidentChannel != "" {
		normalized.IncidentChannel = incidentChannel
	}

	switch {
	case spec.AcknowledgeMinutes < 0:
		return RunbookSpec{}, fmt.Errorf("acknowledge minutes must be greater than 0")
	case spec.AcknowledgeMinutes > 0:
		normalized.AcknowledgeMinutes = spec.AcknowledgeMinutes
	}
	switch {
	case spec.MitigationMinutes < 0:
		return RunbookSpec{}, fmt.Errorf("mitigation minutes must be greater than 0")
	case spec.MitigationMinutes > 0:
		normalized.MitigationMinutes = spec.MitigationMinutes
	}
	switch {
	case spec.StatusUpdateMinutes < 0:
		return RunbookSpec{}, fmt.Errorf("status update minutes must be greater than 0")
	case spec.StatusUpdateMinutes > 0:
		normalized.StatusUpdateMinutes = spec.StatusUpdateMinutes
	}
	switch {
	case spec.PostmortemDeadlineHrs < 0:
		return RunbookSpec{}, fmt.Errorf("postmortem deadline hours must be greater than 0")
	case spec.PostmortemDeadlineHrs > 0:
		normalized.PostmortemDeadlineHrs = spec.PostmortemDeadlineHrs
	}

	if normalized.ServiceName == "" {
		return RunbookSpec{}, fmt.Errorf("service name must not be empty")
	}
	if normalized.IncidentChannel == "" {
		return RunbookSpec{}, fmt.Errorf("incident channel must not be empty")
	}
	if normalized.AcknowledgeMinutes <= 0 {
		return RunbookSpec{}, fmt.Errorf("acknowledge minutes must be greater than 0")
	}
	if normalized.MitigationMinutes <= 0 {
		return RunbookSpec{}, fmt.Errorf("mitigation minutes must be greater than 0")
	}
	if normalized.StatusUpdateMinutes <= 0 {
		return RunbookSpec{}, fmt.Errorf("status update minutes must be greater than 0")
	}
	if normalized.PostmortemDeadlineHrs <= 0 {
		return RunbookSpec{}, fmt.Errorf("postmortem deadline hours must be greater than 0")
	}
	if normalized.AcknowledgeMinutes > normalized.MitigationMinutes {
		return RunbookSpec{}, fmt.Errorf("acknowledge minutes must be less than or equal to mitigation minutes")
	}
	if normalized.StatusUpdateMinutes > normalized.MitigationMinutes {
		return RunbookSpec{}, fmt.Errorf("status update minutes must be less than or equal to mitigation minutes")
	}
	if normalized.PostmortemDeadlineHrs > maxRunbookPostmortemDeadlineHours {
		return RunbookSpec{}, fmt.Errorf(
			"postmortem deadline hours must be less than or equal to %d",
			maxRunbookPostmortemDeadlineHours,
		)
	}

	return normalized, nil
}

func LoadRunbookSpecFromEnv(getenv func(string) string) (RunbookSpec, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	spec := DefaultRunbookSpec()
	warnings := make([]string, 0, 5)

	enabledRaw := strings.TrimSpace(getenv("RUNBOOK_ENABLED"))
	if enabledRaw != "" {
		enabled, err := parseRunbookBool(enabledRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"RUNBOOK_ENABLED=%q 无效，回退默认值 %t: %v",
					enabledRaw,
					DefaultRunbookEnabled,
					err,
				),
			)
		} else {
			spec.Enabled = enabled
		}
	}

	if serviceName := strings.TrimSpace(getenv("RUNBOOK_SERVICE_NAME")); serviceName != "" {
		spec.ServiceName = serviceName
	}
	if incidentChannel := strings.TrimSpace(getenv("RUNBOOK_INCIDENT_CHANNEL")); incidentChannel != "" {
		spec.IncidentChannel = incidentChannel
	}

	ackRaw := strings.TrimSpace(getenv("RUNBOOK_ACK_MINUTES"))
	if ackRaw != "" {
		ack, err := parsePositiveInt32(ackRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"RUNBOOK_ACK_MINUTES=%q 无效，回退默认值 %d: %v",
					ackRaw,
					DefaultRunbookAcknowledgeMinutes,
					err,
				),
			)
		} else if ack > 0 {
			spec.AcknowledgeMinutes = ack
		}
	}

	mitigationRaw := strings.TrimSpace(getenv("RUNBOOK_MITIGATION_MINUTES"))
	if mitigationRaw != "" {
		mitigation, err := parsePositiveInt32(mitigationRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"RUNBOOK_MITIGATION_MINUTES=%q 无效，回退默认值 %d: %v",
					mitigationRaw,
					DefaultRunbookMitigationMinutes,
					err,
				),
			)
		} else if mitigation > 0 {
			spec.MitigationMinutes = mitigation
		}
	}

	statusUpdateRaw := strings.TrimSpace(getenv("RUNBOOK_STATUS_UPDATE_MINUTES"))
	if statusUpdateRaw != "" {
		statusUpdate, err := parsePositiveInt32(statusUpdateRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"RUNBOOK_STATUS_UPDATE_MINUTES=%q 无效，回退默认值 %d: %v",
					statusUpdateRaw,
					DefaultRunbookStatusUpdateMinutes,
					err,
				),
			)
		} else if statusUpdate > 0 {
			spec.StatusUpdateMinutes = statusUpdate
		}
	}

	postmortemRaw := strings.TrimSpace(getenv("RUNBOOK_POSTMORTEM_DEADLINE_HOURS"))
	if postmortemRaw != "" {
		postmortem, err := parsePositiveInt32(postmortemRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"RUNBOOK_POSTMORTEM_DEADLINE_HOURS=%q 无效，回退默认值 %d: %v",
					postmortemRaw,
					DefaultRunbookPostmortemDeadlineHrs,
					err,
				),
			)
		} else if postmortem > 0 {
			spec.PostmortemDeadlineHrs = postmortem
		}
	}

	normalized, err := NormalizeRunbookSpec(spec)
	if err != nil {
		return DefaultRunbookSpec(), fmt.Errorf("Runbook 配置非法，回退默认值: %w", err)
	}
	if len(warnings) > 0 {
		return normalized, errors.New(strings.Join(warnings, "; "))
	}
	return normalized, nil
}

func BuildIncidentRunbook(spec RunbookSpec) (IncidentRunbook, error) {
	normalized, err := NormalizeRunbookSpec(spec)
	if err != nil {
		return IncidentRunbook{}, err
	}

	runbook := IncidentRunbook{
		ServiceName:     normalized.ServiceName,
		IncidentChannel: normalized.IncidentChannel,
		Steps:           make([]RunbookStep, 0, 5),
	}
	if !normalized.Enabled {
		return runbook, nil
	}

	statusUpdateAction := fmt.Sprintf("每%d分钟同步故障状态和下一步计划", normalized.StatusUpdateMinutes)

	runbook.Steps = append(runbook.Steps,
		RunbookStep{
			Phase:     "1. 故障受理与分级",
			Objective: "快速确认影响面并建立统一指挥面",
			SLA:       fmt.Sprintf("%d分钟内完成建单、分级与指挥官指派", normalized.AcknowledgeMinutes),
			Actions: []string{
				fmt.Sprintf("在 %s 创建故障线程，记录告警来源与首个发现时间", normalized.IncidentChannel),
				"指定 Incident Commander，并冻结发布/变更入口",
				"按 S0/S1/S2 评估影响范围（用户、交易、资金风险）",
			},
			ExitCriteria: "故障级别、影响范围、负责人已明确",
		},
		RunbookStep{
			Phase:     "2. 止损与隔离",
			Objective: "优先控制故障扩散，保证资金与数据安全",
			SLA:       fmt.Sprintf("%d分钟内完成核心止损动作", normalized.MitigationMinutes),
			Actions: []string{
				"执行止损动作：可用时触发 Kill-Switch、限流、降级或只读",
				"隔离故障影响面：按模块/租户关闭高风险入口",
				statusUpdateAction,
			},
			ExitCriteria: "故障影响范围不再扩大，关键风险已受控",
		},
		RunbookStep{
			Phase:     "3. 根因定位",
			Objective: "基于证据定位根因并给出可执行修复方案",
			SLA:       "止损完成后立即并行推进，优先恢复可用性",
			Actions: []string{
				"按 日志 -> 指标 -> 链路 -> 最近变更 的顺序排查",
				"对可疑变更执行二分回滚或流量切换验证",
				"记录每个假设和验证结果，避免重复排查",
			},
			ExitCriteria: "形成可复现根因或确定可靠修复路径",
		},
		RunbookStep{
			Phase:     "4. 恢复与验收",
			Objective: "分阶段恢复服务并验证业务闭环",
			SLA:       "修复后连续两个监控周期无新增严重告警",
			Actions: []string{
				"执行修复或回滚后逐步放量，观察错误率与时延",
				"校验 /livez、/readyz、/metrics 与关键业务链路",
				statusUpdateAction,
			},
			ExitCriteria: "服务稳定，核心交易路径恢复且监控正常",
		},
		RunbookStep{
			Phase:     "5. 复盘与改进",
			Objective: "沉淀经验并闭环改进，降低复发概率",
			SLA:       fmt.Sprintf("%d小时内完成复盘文档", normalized.PostmortemDeadlineHrs),
			Actions: []string{
				"整理时间线（告警、确认、止损、恢复）与关键证据",
				"明确根因分类、行动项、责任人与截止时间",
				"将结论回写到 Runbook、告警阈值和自动化脚本",
			},
			ExitCriteria: "复盘评审完成且改进行动可追踪",
		},
	)

	return runbook, nil
}

func BuildIncidentRunbookMarkdown(spec RunbookSpec) (string, error) {
	normalized, err := NormalizeRunbookSpec(spec)
	if err != nil {
		return "", err
	}
	if !normalized.Enabled {
		return "Runbook(操作手册)未启用：RUNBOOK_ENABLED=false", nil
	}

	runbook, err := BuildIncidentRunbook(normalized)
	if err != nil {
		return "", err
	}

	var builder strings.Builder
	fmt.Fprintf(&builder, "# %s 故障处理 Runbook\n\n", runbook.ServiceName)
	fmt.Fprintf(&builder, "- 事件沟通频道：%s\n", runbook.IncidentChannel)
	fmt.Fprintf(&builder, "- 状态播报频率：每%d分钟\n", normalized.StatusUpdateMinutes)
	fmt.Fprintf(&builder, "- 复盘时限：%d小时\n\n", normalized.PostmortemDeadlineHrs)

	for _, step := range runbook.Steps {
		fmt.Fprintf(&builder, "## %s\n", step.Phase)
		fmt.Fprintf(&builder, "- 目标：%s\n", step.Objective)
		fmt.Fprintf(&builder, "- SLA：%s\n", step.SLA)
		fmt.Fprintf(&builder, "- 操作：\n")
		for actionIndex, action := range step.Actions {
			fmt.Fprintf(&builder, "  %d. %s\n", actionIndex+1, action)
		}
		fmt.Fprintf(&builder, "- 退出条件：%s\n\n", step.ExitCriteria)
	}

	fmt.Fprintf(&builder, "## 证据清单\n")
	fmt.Fprintf(&builder, "- 告警截图与监控链接\n")
	fmt.Fprintf(&builder, "- 根因定位证据（日志/指标/链路）\n")
	fmt.Fprintf(&builder, "- 修复或回滚记录\n")
	fmt.Fprintf(&builder, "- 复盘文档与改进行动项\n")

	return strings.TrimSpace(builder.String()), nil
}

func BuildRunbookLog(spec RunbookSpec) string {
	normalized, err := NormalizeRunbookSpec(spec)
	if err != nil {
		return fmt.Sprintf("Runbook(操作手册)未启用：配置非法 %v", err)
	}
	if !normalized.Enabled {
		return "Runbook(操作手册)未启用：RUNBOOK_ENABLED=false"
	}

	runbook, err := BuildIncidentRunbook(normalized)
	if err != nil {
		return fmt.Sprintf("Runbook(操作手册)未启用：构建失败 %v", err)
	}
	return fmt.Sprintf(
		"Runbook(操作手册)已启用：service=%s channel=%s steps=%d ack<=%dmin mitigate<=%dmin status_every=%dmin postmortem<=%dh",
		runbook.ServiceName,
		runbook.IncidentChannel,
		len(runbook.Steps),
		normalized.AcknowledgeMinutes,
		normalized.MitigationMinutes,
		normalized.StatusUpdateMinutes,
		normalized.PostmortemDeadlineHrs,
	)
}

func parseRunbookBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "t", "yes", "y", "on":
		return true, nil
	case "0", "false", "f", "no", "n", "off":
		return false, nil
	default:
		return false, fmt.Errorf("must be a boolean")
	}
}
