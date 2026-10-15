package bot

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type fourLayerRoleType string

const (
	fourLayerRoleOwner         fourLayerRoleType = "owner"
	fourLayerRoleLocalOperator fourLayerRoleType = "local_operator"
	fourLayerRoleGlobalOp      fourLayerRoleType = "global_operator"
	fourLayerRoleMember        fourLayerRoleType = "member"

	fourLayerSmokeUsers           = 500
	fourLayerSmokeLocalOperators  = 120
	fourLayerSmokeGlobalOperators = 30
	fourLayerStressUsers          = 3500
	fourLayerStressLocalOperators = 840
	fourLayerStressGlobalOps      = 210
)

type fourLayerRoleUser struct {
	userID   int64
	username string
	role     fourLayerRoleType
}

type fourLayerLoadPlan struct {
	mode                string
	totalUsers          int
	localOperatorCount  int
	globalOperatorCount int
}

func (u fourLayerRoleUser) expectAutoReplyKeyword() string {
	switch u.role {
	case fourLayerRoleOwner, fourLayerRoleLocalOperator, fourLayerRoleGlobalOp:
		return "当前无自动回复规则。"
	default:
		return ""
	}
}

func (u fourLayerRoleUser) expectGuaranteeKeyword() string {
	switch u.role {
	case fourLayerRoleOwner:
		return "担保规则已更新。"
	default:
		return ""
	}
}

func TestFourLayerLoadPlanDefaultsToSmoke(t *testing.T) {
	t.Setenv("FOUR_LAYER_LOAD_STRESS", "")
	t.Setenv("FOUR_LAYER_LOAD_USERS", "")
	t.Setenv("FOUR_LAYER_LOAD_LOCAL_OPERATORS", "")
	t.Setenv("FOUR_LAYER_LOAD_GLOBAL_OPERATORS", "")

	plan := fourLayerLoadPlanFromEnv()

	if plan.mode != "smoke" {
		t.Fatalf("默认压测模式错误: got=%s want=smoke", plan.mode)
	}
	if plan.totalUsers != 500 {
		t.Fatalf("默认用户数应使用 smoke 规模: got=%d want=500", plan.totalUsers)
	}
	if plan.localOperatorCount != 120 {
		t.Fatalf("默认本群操作人数应使用 smoke 规模: got=%d want=120", plan.localOperatorCount)
	}
	if plan.globalOperatorCount != 30 {
		t.Fatalf("默认全局操作人数应使用 smoke 规模: got=%d want=30", plan.globalOperatorCount)
	}
}

func TestFourLayerLoadPlanStressEnvUses3500Defaults(t *testing.T) {
	t.Setenv("FOUR_LAYER_LOAD_STRESS", "1")
	t.Setenv("FOUR_LAYER_LOAD_USERS", "")
	t.Setenv("FOUR_LAYER_LOAD_LOCAL_OPERATORS", "")
	t.Setenv("FOUR_LAYER_LOAD_GLOBAL_OPERATORS", "")

	plan := fourLayerLoadPlanFromEnv()

	if plan.mode != "stress" {
		t.Fatalf("显式压测模式错误: got=%s want=stress", plan.mode)
	}
	if plan.totalUsers != 3500 {
		t.Fatalf("压测用户数错误: got=%d want=3500", plan.totalUsers)
	}
	if plan.localOperatorCount != 840 {
		t.Fatalf("压测本群操作人数错误: got=%d want=840", plan.localOperatorCount)
	}
	if plan.globalOperatorCount != 210 {
		t.Fatalf("压测全局操作人数错误: got=%d want=210", plan.globalOperatorCount)
	}
}

func TestFourLayerLoadPlanExplicitUsersCanOverrideScale(t *testing.T) {
	t.Setenv("FOUR_LAYER_LOAD_STRESS", "")
	t.Setenv("FOUR_LAYER_LOAD_USERS", "3500")
	t.Setenv("FOUR_LAYER_LOAD_LOCAL_OPERATORS", "840")
	t.Setenv("FOUR_LAYER_LOAD_GLOBAL_OPERATORS", "210")

	plan := fourLayerLoadPlanFromEnv()

	if plan.mode != "custom" {
		t.Fatalf("显式用户数应标记为 custom: got=%s want=custom", plan.mode)
	}
	if plan.totalUsers != 3500 {
		t.Fatalf("显式用户数未生效: got=%d want=3500", plan.totalUsers)
	}
	if plan.localOperatorCount != 840 {
		t.Fatalf("显式本群操作人数未生效: got=%d want=840", plan.localOperatorCount)
	}
	if plan.globalOperatorCount != 210 {
		t.Fatalf("显式全局操作人数未生效: got=%d want=210", plan.globalOperatorCount)
	}
}

func TestFourLayer3500UsersMultiRoleShortBurst(t *testing.T) {
	plan := fourLayerLoadPlanFromEnv()
	totalUsers := plan.totalUsers
	localOperatorCount := plan.localOperatorCount
	globalOperatorCount := plan.globalOperatorCount
	if 1+localOperatorCount+globalOperatorCount > totalUsers {
		t.Fatalf("角色配置无效: users=%d local=%d global=%d", totalUsers, localOperatorCount, globalOperatorCount)
	}

	app, database, capture := newFourLayerTestApp(t, "four-layer-short-burst-load.db")

	chatID := int64(-1004405001)
	ownerUser := fourLayerRoleUser{
		userID:   55000001,
		username: "owner_55000001",
		role:     fourLayerRoleOwner,
	}
	if _, err := database.EnsureGroup(chatID, "四层3500用户压测群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerUser.userID); err != nil {
		t.Fatalf("设置群权限人失败: %v", err)
	}

	users := make([]fourLayerRoleUser, 0, totalUsers)
	users = append(users, ownerUser)
	baseUserID := int64(55000002)
	for i := 0; i < totalUsers-1; i++ {
		user := fourLayerRoleUser{
			userID:   baseUserID + int64(i),
			username: fmt.Sprintf("u_%d", baseUserID+int64(i)),
			role:     fourLayerRoleMember,
		}
		switch {
		case i < localOperatorCount:
			user.role = fourLayerRoleLocalOperator
			if err := database.AddOperator(chatID, user.userID, user.username, false); err != nil {
				t.Fatalf("写入本群操作人失败: %v", err)
			}
		case i < localOperatorCount+globalOperatorCount:
			user.role = fourLayerRoleGlobalOp
			if err := database.AddOperator(chatID, user.userID, user.username, true); err != nil {
				t.Fatalf("写入全局操作人失败: %v", err)
			}
		}
		users = append(users, user)
	}

	baseDate := int(time.Now().Unix())
	warmup := fourLayerGroupTextUpdate(560000, 560000, chatID, ownerUser.userID, ownerUser.username, "开始记账", baseDate)
	app.dispatchUpdate(context.Background(), warmup)

	expectedByMessageID := make(map[int]string, totalUsers*2)
	silentMessageIDs := make(map[int]struct{}, totalUsers*2)
	updates := make([]tgbotapi.Update, 0, totalUsers*2)
	updateID := 560001
	messageID := 560001
	for _, user := range users {
		autoReplyMsgID := messageID
		messageID++
		if keyword := user.expectAutoReplyKeyword(); keyword != "" {
			expectedByMessageID[autoReplyMsgID] = keyword
		} else {
			silentMessageIDs[autoReplyMsgID] = struct{}{}
		}
		updates = append(updates, fourLayerGroupTextUpdate(
			updateID,
			autoReplyMsgID,
			chatID,
			user.userID,
			user.username,
			"自动回复列表",
			baseDate+1,
		))
		updateID++

		guaranteeMsgID := messageID
		messageID++
		if keyword := user.expectGuaranteeKeyword(); keyword != "" {
			expectedByMessageID[guaranteeMsgID] = keyword
		} else {
			silentMessageIDs[guaranteeMsgID] = struct{}{}
		}
		updates = append(updates, fourLayerGroupTextUpdate(
			updateID,
			guaranteeMsgID,
			chatID,
			user.userID,
			user.username,
			fmt.Sprintf("设置担保规则 压测规则-%d", user.userID),
			baseDate+2,
		))
		updateID++
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	startAt := time.Now()
	for _, upd := range updates {
		upd := upd
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			app.dispatchUpdate(context.Background(), upd)
		}()
	}
	close(start)
	wg.Wait()
	elapsed := time.Since(startAt)

	if elapsed > 90*time.Second {
		t.Fatalf("四层短时并发处理超时: mode=%s users=%d updates=%d elapsed=%s", plan.mode, totalUsers, len(updates), elapsed)
	}

	snapshot := capture.snapshot()
	missing := make([]int, 0, 16)
	for msgID, keyword := range expectedByMessageID {
		if !fourLayerHasReply(snapshot, chatID, msgID, keyword) {
			missing = append(missing, msgID)
			if len(missing) >= 16 {
				break
			}
		}
	}
	if len(missing) > 0 {
		t.Fatalf("并发回执缺失或文案不匹配: missing_message_ids=%v total_expected=%d total_captured=%d", missing, len(expectedByMessageID), len(snapshot))
	}
	unexpected := make([]int, 0, 16)
	for msgID := range silentMessageIDs {
		if fourLayerHasAnyReply(snapshot, chatID, msgID) {
			unexpected = append(unexpected, msgID)
			if len(unexpected) >= 16 {
				break
			}
		}
	}
	if len(unexpected) > 0 {
		t.Fatalf("静默命令不应产生回执: unexpected_message_ids=%v total_silent=%d total_captured=%d", unexpected, len(silentMessageIDs), len(snapshot))
	}

	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	if group.BookkeepingEnabled != 1 {
		t.Fatalf("压测预热后记账状态异常: want=1 got=%d", group.BookkeepingEnabled)
	}
	if group.GuaranteeRule != fmt.Sprintf("压测规则-%d", ownerUser.userID) {
		t.Fatalf("最终担保规则应由权限人写入: got=%q", group.GuaranteeRule)
	}

	t.Logf(
		"四层短时并发测试完成: mode=%s users=%d local_ops=%d global_ops=%d updates=%d elapsed=%s",
		plan.mode,
		totalUsers,
		localOperatorCount,
		globalOperatorCount,
		len(updates),
		elapsed,
	)
}

func fourLayerLoadPlanFromEnv() fourLayerLoadPlan {
	mode := "smoke"
	defaultUsers := fourLayerSmokeUsers
	defaultLocalOperators := fourLayerSmokeLocalOperators
	defaultGlobalOperators := fourLayerSmokeGlobalOperators
	if fourLayerStressEnabled() {
		mode = "stress"
		defaultUsers = fourLayerStressUsers
		defaultLocalOperators = fourLayerStressLocalOperators
		defaultGlobalOperators = fourLayerStressGlobalOps
	} else if raw, ok := os.LookupEnv("FOUR_LAYER_LOAD_USERS"); ok && raw != "" {
		mode = "custom"
	}

	totalUsers := fourLayerLoadConfig("FOUR_LAYER_LOAD_USERS", defaultUsers, 64, 10000)
	localFallback := fourLayerMinInt(defaultLocalOperators, totalUsers-1)
	localOperatorCount := fourLayerLoadConfig("FOUR_LAYER_LOAD_LOCAL_OPERATORS", localFallback, 0, totalUsers-1)
	globalFallback := fourLayerMinInt(defaultGlobalOperators, totalUsers-1-localOperatorCount)
	globalOperatorCount := fourLayerLoadConfig("FOUR_LAYER_LOAD_GLOBAL_OPERATORS", globalFallback, 0, totalUsers-1-localOperatorCount)

	return fourLayerLoadPlan{
		mode:                mode,
		totalUsers:          totalUsers,
		localOperatorCount:  localOperatorCount,
		globalOperatorCount: globalOperatorCount,
	}
}

func fourLayerStressEnabled() bool {
	enabled, err := strconv.ParseBool(os.Getenv("FOUR_LAYER_LOAD_STRESS"))
	return err == nil && enabled
}

func fourLayerMinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func fourLayerLoadConfig(key string, fallback, minValue, maxValue int) int {
	if minValue > maxValue {
		return fallback
	}
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	if value < minValue || value > maxValue {
		return fallback
	}
	return value
}
