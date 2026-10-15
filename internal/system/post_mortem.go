package system

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	DefaultPostMortemEnabled        = true
	DefaultPostMortemServiceName    = "ledgerbot"
	DefaultPostMortemReviewOwner    = "sre-oncall"
	DefaultPostMortemReviewChannel  = "#ledgerbot-incident"
	DefaultPostMortemRCAMethod      = PostMortemRCAMethodFiveWhys
	DefaultPostMortemDeadlineHours  = int32(24)
	DefaultPostMortemMinActionItems = int32(3)
	DefaultPostMortemSeverity       = "S1"
	DefaultPostMortemWhyDepth       = int32(5)

	maxPostMortemDeadlineHours = int32(168)
	maxPostMortemActionItems   = int32(20)
)

const (
	PostMortemRCAMethodFiveWhys = "5-whys"
	PostMortemRCAMethodFishbone = "fishbone"
)

type PostMortemSpec struct {
	Enabled        bool
	ServiceName    string
	ReviewOwner    string
	ReviewChannel  string
	RCAMethod      string
	DeadlineHours  int32
	MinActionItems int32
}

type PostMortemEvidence struct {
	OccurredAt time.Time
	Source     string
	Detail     string
}

type PostMortemActionItem struct {
	Description string
	Owner       string
	DueAt       time.Time
}

type PostMortemIncident struct {
	IncidentID  string
	Severity    string
	Summary     string
	StartedAt   time.Time
	RecoveredAt time.Time
	WhyChain    []string
	Evidence    []PostMortemEvidence
	ActionItems []PostMortemActionItem
}

type PostMortemReport struct {
	Enabled       bool
	IncidentID    string
	Severity      string
	ServiceName   string
	ReviewOwner   string
	ReviewChannel string
	RCAMethod     string
	Summary       string
	RootCause     string
	StartedAt     time.Time
	RecoveredAt   time.Time
	MTTR          time.Duration
	WhyChain      []string
	Timeline      []PostMortemEvidence
	ActionItems   []PostMortemActionItem
}

func DefaultPostMortemSpec() PostMortemSpec {
	return PostMortemSpec{
		Enabled:        DefaultPostMortemEnabled,
		ServiceName:    DefaultPostMortemServiceName,
		ReviewOwner:    DefaultPostMortemReviewOwner,
		ReviewChannel:  DefaultPostMortemReviewChannel,
		RCAMethod:      DefaultPostMortemRCAMethod,
		DeadlineHours:  DefaultPostMortemDeadlineHours,
		MinActionItems: DefaultPostMortemMinActionItems,
	}
}

func NormalizePostMortemSpec(spec PostMortemSpec) (PostMortemSpec, error) {
	normalized := DefaultPostMortemSpec()
	normalized.Enabled = spec.Enabled

	if serviceName := strings.TrimSpace(spec.ServiceName); serviceName != "" {
		normalized.ServiceName = serviceName
	}
	if reviewOwner := strings.TrimSpace(spec.ReviewOwner); reviewOwner != "" {
		normalized.ReviewOwner = reviewOwner
	}
	if reviewChannel := strings.TrimSpace(spec.ReviewChannel); reviewChannel != "" {
		normalized.ReviewChannel = reviewChannel
	}

	switch {
	case spec.DeadlineHours < 0:
		return PostMortemSpec{}, fmt.Errorf("deadline hours must be greater than 0")
	case spec.DeadlineHours > 0:
		normalized.DeadlineHours = spec.DeadlineHours
	}
	switch {
	case spec.MinActionItems < 0:
		return PostMortemSpec{}, fmt.Errorf("min action items must be greater than 0")
	case spec.MinActionItems > 0:
		normalized.MinActionItems = spec.MinActionItems
	}

	methodRaw := normalized.RCAMethod
	if strings.TrimSpace(spec.RCAMethod) != "" {
		methodRaw = spec.RCAMethod
	}
	method, methodErr := normalizePostMortemMethod(methodRaw)
	if methodErr != nil {
		return PostMortemSpec{}, methodErr
	}
	normalized.RCAMethod = method

	if normalized.ServiceName == "" {
		return PostMortemSpec{}, fmt.Errorf("service name must not be empty")
	}
	if normalized.ReviewOwner == "" {
		return PostMortemSpec{}, fmt.Errorf("review owner must not be empty")
	}
	if normalized.ReviewChannel == "" {
		return PostMortemSpec{}, fmt.Errorf("review channel must not be empty")
	}
	if normalized.DeadlineHours <= 0 {
		return PostMortemSpec{}, fmt.Errorf("deadline hours must be greater than 0")
	}
	if normalized.DeadlineHours > maxPostMortemDeadlineHours {
		return PostMortemSpec{}, fmt.Errorf(
			"deadline hours must be less than or equal to %d",
			maxPostMortemDeadlineHours,
		)
	}
	if normalized.MinActionItems <= 0 {
		return PostMortemSpec{}, fmt.Errorf("min action items must be greater than 0")
	}
	if normalized.MinActionItems > maxPostMortemActionItems {
		return PostMortemSpec{}, fmt.Errorf(
			"min action items must be less than or equal to %d",
			maxPostMortemActionItems,
		)
	}

	return normalized, nil
}

func LoadPostMortemSpecFromEnv(getenv func(string) string) (PostMortemSpec, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	spec := DefaultPostMortemSpec()
	warnings := make([]string, 0, 7)

	enabledRaw := strings.TrimSpace(getenv("POST_MORTEM_ENABLED"))
	if enabledRaw != "" {
		enabled, err := parsePostMortemBool(enabledRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"POST_MORTEM_ENABLED=%q 无效，回退默认值 %t: %v",
					enabledRaw,
					DefaultPostMortemEnabled,
					err,
				),
			)
		} else {
			spec.Enabled = enabled
		}
	}

	if serviceName := strings.TrimSpace(getenv("POST_MORTEM_SERVICE_NAME")); serviceName != "" {
		spec.ServiceName = serviceName
	}
	if reviewOwner := strings.TrimSpace(getenv("POST_MORTEM_REVIEW_OWNER")); reviewOwner != "" {
		spec.ReviewOwner = reviewOwner
	}
	if reviewChannel := strings.TrimSpace(getenv("POST_MORTEM_REVIEW_CHANNEL")); reviewChannel != "" {
		spec.ReviewChannel = reviewChannel
	}

	methodRaw := strings.TrimSpace(getenv("POST_MORTEM_RCA_METHOD"))
	if methodRaw != "" {
		method, err := normalizePostMortemMethod(methodRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"POST_MORTEM_RCA_METHOD=%q 无效，回退默认值 %s: %v",
					methodRaw,
					DefaultPostMortemRCAMethod,
					err,
				),
			)
		} else {
			spec.RCAMethod = method
		}
	}

	deadlineRaw := strings.TrimSpace(getenv("POST_MORTEM_DEADLINE_HOURS"))
	if deadlineRaw != "" {
		deadline, err := parsePositiveInt32(deadlineRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"POST_MORTEM_DEADLINE_HOURS=%q 无效，回退默认值 %d: %v",
					deadlineRaw,
					DefaultPostMortemDeadlineHours,
					err,
				),
			)
		} else if deadline > 0 {
			spec.DeadlineHours = deadline
		}
	}

	minActionsRaw := strings.TrimSpace(getenv("POST_MORTEM_MIN_ACTION_ITEMS"))
	if minActionsRaw != "" {
		minActions, err := parsePositiveInt32(minActionsRaw)
		if err != nil {
			warnings = append(
				warnings,
				fmt.Sprintf(
					"POST_MORTEM_MIN_ACTION_ITEMS=%q 无效，回退默认值 %d: %v",
					minActionsRaw,
					DefaultPostMortemMinActionItems,
					err,
				),
			)
		} else if minActions > 0 {
			spec.MinActionItems = minActions
		}
	}

	normalized, err := NormalizePostMortemSpec(spec)
	if err != nil {
		return DefaultPostMortemSpec(), fmt.Errorf("事后复盘配置非法，回退默认值: %w", err)
	}
	if len(warnings) > 0 {
		return normalized, errors.New(strings.Join(warnings, "; "))
	}
	return normalized, nil
}

func BuildPostMortemReport(spec PostMortemSpec, incident PostMortemIncident) (PostMortemReport, error) {
	normalized, err := NormalizePostMortemSpec(spec)
	if err != nil {
		return PostMortemReport{}, err
	}

	report := PostMortemReport{
		Enabled:       normalized.Enabled,
		ServiceName:   normalized.ServiceName,
		ReviewOwner:   normalized.ReviewOwner,
		ReviewChannel: normalized.ReviewChannel,
		RCAMethod:     normalized.RCAMethod,
	}
	if !normalized.Enabled {
		return report, nil
	}

	report.IncidentID = strings.TrimSpace(incident.IncidentID)
	if report.IncidentID == "" {
		return PostMortemReport{}, fmt.Errorf("incident id must not be empty")
	}

	report.Severity = strings.ToUpper(strings.TrimSpace(incident.Severity))
	if report.Severity == "" {
		report.Severity = DefaultPostMortemSeverity
	}

	report.Summary = strings.TrimSpace(incident.Summary)
	if report.Summary == "" {
		return PostMortemReport{}, fmt.Errorf("summary must not be empty")
	}

	startedAt := incident.StartedAt.UTC()
	if startedAt.IsZero() {
		return PostMortemReport{}, fmt.Errorf("started at must not be empty")
	}
	recoveredAt := incident.RecoveredAt.UTC()
	if recoveredAt.IsZero() {
		return PostMortemReport{}, fmt.Errorf("recovered at must not be empty")
	}
	if recoveredAt.Before(startedAt) {
		return PostMortemReport{}, fmt.Errorf("recovered at must be greater than or equal to started at")
	}
	report.StartedAt = startedAt
	report.RecoveredAt = recoveredAt
	report.MTTR = recoveredAt.Sub(startedAt).Round(time.Second)

	whyChain := normalizePostMortemWhyChain(incident.WhyChain)
	if len(whyChain) == 0 {
		return PostMortemReport{}, fmt.Errorf("why chain must not be empty")
	}
	if normalized.RCAMethod == PostMortemRCAMethodFiveWhys && len(whyChain) < int(DefaultPostMortemWhyDepth) {
		return PostMortemReport{}, fmt.Errorf("why chain must contain at least %d entries", DefaultPostMortemWhyDepth)
	}
	report.WhyChain = whyChain
	report.RootCause = whyChain[len(whyChain)-1]

	report.Timeline = normalizePostMortemTimeline(incident.Evidence, startedAt)
	if len(report.Timeline) == 0 {
		return PostMortemReport{}, fmt.Errorf("timeline evidence must not be empty")
	}

	defaultDueAt := recoveredAt.Add(time.Duration(normalized.DeadlineHours) * time.Hour)
	report.ActionItems = normalizePostMortemActionItems(incident.ActionItems, normalized.ReviewOwner, defaultDueAt)
	if len(report.ActionItems) < int(normalized.MinActionItems) {
		return PostMortemReport{}, fmt.Errorf(
			"action items must be greater than or equal to %d",
			normalized.MinActionItems,
		)
	}

	return report, nil
}

func BuildPostMortemMarkdown(report PostMortemReport) (string, error) {
	if !report.Enabled {
		return "事后复盘未启用：POST_MORTEM_ENABLED=false", nil
	}
	if strings.TrimSpace(report.IncidentID) == "" {
		return "", fmt.Errorf("incident id must not be empty")
	}
	if strings.TrimSpace(report.RootCause) == "" {
		return "", fmt.Errorf("root cause must not be empty")
	}

	var builder strings.Builder
	fmt.Fprintf(&builder, "# %s 事后复盘报告\n\n", report.IncidentID)
	fmt.Fprintf(&builder, "- 服务：%s\n", report.ServiceName)
	fmt.Fprintf(&builder, "- 故障等级：%s\n", report.Severity)
	fmt.Fprintf(&builder, "- 复盘负责人：%s\n", report.ReviewOwner)
	fmt.Fprintf(&builder, "- 复盘频道：%s\n", report.ReviewChannel)
	fmt.Fprintf(&builder, "- RCA 方法：%s\n", report.RCAMethod)
	fmt.Fprintf(&builder, "- 故障起止：%s -> %s\n", report.StartedAt.UTC().Format(time.RFC3339), report.RecoveredAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&builder, "- MTTR：%s\n\n", report.MTTR)

	fmt.Fprintf(&builder, "## 事件摘要\n")
	fmt.Fprintf(&builder, "- %s\n\n", report.Summary)

	fmt.Fprintf(&builder, "## 根因分析\n")
	for index, why := range report.WhyChain {
		fmt.Fprintf(&builder, "%d. Why%d: %s\n", index+1, index+1, why)
	}
	fmt.Fprintf(&builder, "- 最终根因：%s\n\n", report.RootCause)

	fmt.Fprintf(&builder, "## 证据时间线\n")
	for index, evidence := range report.Timeline {
		fmt.Fprintf(
			&builder,
			"%d. %s | %s | %s\n",
			index+1,
			evidence.OccurredAt.UTC().Format(time.RFC3339),
			evidence.Source,
			evidence.Detail,
		)
	}
	fmt.Fprintf(&builder, "\n## 改进行动项\n")
	for index, actionItem := range report.ActionItems {
		fmt.Fprintf(
			&builder,
			"%d. [%s] %s (截止 %s)\n",
			index+1,
			actionItem.Owner,
			actionItem.Description,
			actionItem.DueAt.UTC().Format(time.RFC3339),
		)
	}
	return strings.TrimSpace(builder.String()), nil
}

func BuildPostMortemLog(spec PostMortemSpec) string {
	normalized, err := NormalizePostMortemSpec(spec)
	if err != nil {
		return fmt.Sprintf("事后复盘未启用：配置非法 %v", err)
	}
	if !normalized.Enabled {
		return "事后复盘未启用：POST_MORTEM_ENABLED=false"
	}
	return fmt.Sprintf(
		"事后复盘已启用：service=%s owner=%s channel=%s method=%s deadline<=%dh min_actions=%d strategy=故障后根因分析与改进行动闭环",
		normalized.ServiceName,
		normalized.ReviewOwner,
		normalized.ReviewChannel,
		normalized.RCAMethod,
		normalized.DeadlineHours,
		normalized.MinActionItems,
	)
}

func normalizePostMortemMethod(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", PostMortemRCAMethodFiveWhys, "5whys", "five-whys", "five_whys":
		return PostMortemRCAMethodFiveWhys, nil
	case PostMortemRCAMethodFishbone, "fish-bone", "fish_bone", "ishikawa":
		return PostMortemRCAMethodFishbone, nil
	default:
		return "", fmt.Errorf("rca method must be one of [%s,%s]", PostMortemRCAMethodFiveWhys, PostMortemRCAMethodFishbone)
	}
}

func normalizePostMortemWhyChain(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		normalized = append(normalized, trimmed)
	}
	return normalized
}

func normalizePostMortemTimeline(values []PostMortemEvidence, fallbackAt time.Time) []PostMortemEvidence {
	normalized := make([]PostMortemEvidence, 0, len(values))
	for _, value := range values {
		source := strings.TrimSpace(value.Source)
		detail := strings.TrimSpace(value.Detail)
		if source == "" || detail == "" {
			continue
		}
		occurredAt := value.OccurredAt.UTC()
		if occurredAt.IsZero() {
			occurredAt = fallbackAt.UTC()
		}
		normalized = append(normalized, PostMortemEvidence{
			OccurredAt: occurredAt,
			Source:     source,
			Detail:     detail,
		})
	}
	sort.SliceStable(normalized, func(i, j int) bool {
		if normalized[i].OccurredAt.Equal(normalized[j].OccurredAt) {
			return normalized[i].Source < normalized[j].Source
		}
		return normalized[i].OccurredAt.Before(normalized[j].OccurredAt)
	})
	return normalized
}

func normalizePostMortemActionItems(
	values []PostMortemActionItem,
	fallbackOwner string,
	fallbackDueAt time.Time,
) []PostMortemActionItem {
	normalized := make([]PostMortemActionItem, 0, len(values))
	for _, value := range values {
		description := strings.TrimSpace(value.Description)
		if description == "" {
			continue
		}
		owner := strings.TrimSpace(value.Owner)
		if owner == "" {
			owner = fallbackOwner
		}

		dueAt := value.DueAt.UTC()
		if dueAt.IsZero() {
			dueAt = fallbackDueAt.UTC()
		}

		normalized = append(normalized, PostMortemActionItem{
			Description: description,
			Owner:       owner,
			DueAt:       dueAt,
		})
	}
	sort.SliceStable(normalized, func(i, j int) bool {
		if normalized[i].DueAt.Equal(normalized[j].DueAt) {
			return normalized[i].Description < normalized[j].Description
		}
		return normalized[i].DueAt.Before(normalized[j].DueAt)
	})
	return normalized
}

func parsePostMortemBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "t", "yes", "y", "on", "enable", "enabled":
		return true, nil
	case "0", "false", "f", "no", "n", "off", "disable", "disabled":
		return false, nil
	default:
		return false, fmt.Errorf("must be a boolean")
	}
}
