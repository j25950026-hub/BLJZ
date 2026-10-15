package bot

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/db"
)

func TestControlPoint222UserFSM(t *testing.T) {
	t.Run("控制点 2.2.2 功能验证", func(t *testing.T) {
		mini, err := miniredis.Run()
		if err != nil {
			t.Fatalf("启动 miniredis 失败: %v", err)
		}
		defer mini.Close()

		const (
			chatID int64 = -100222001
			userID int64 = 222001
		)

		storeA, err := NewRedisUserFSM(RedisUserFSMConfig{
			Addr:      mini.Addr(),
			KeyPrefix: "session:fsm:",
			TTL:       5 * time.Minute,
			Timeout:   time.Second,
		})
		if err != nil {
			t.Fatalf("初始化用户状态机节点A失败: %v", err)
		}
		defer storeA.Close()

		storeB, err := NewRedisUserFSM(RedisUserFSMConfig{
			Addr:      mini.Addr(),
			KeyPrefix: "session:fsm:",
			TTL:       5 * time.Minute,
			Timeout:   time.Second,
		})
		if err != nil {
			t.Fatalf("初始化用户状态机节点B失败: %v", err)
		}
		defer storeB.Close()

		prev, hadPrev, err := storeA.Transition(context.Background(), chatID, userID, UserInteractionContext{
			State: UserInteractionStateAwaitCustomRate,
			Scene: "正在设置汇率中",
		})
		if err != nil {
			t.Fatalf("写入 AWAIT_RATE 状态失败: %v", err)
		}
		if hadPrev {
			t.Fatalf("首次写入不应存在前置状态: %+v", prev)
		}

		snapshot, ok, err := storeB.Get(context.Background(), chatID, userID)
		if err != nil {
			t.Fatalf("跨节点读取状态失败: %v", err)
		}
		if !ok {
			t.Fatalf("跨节点应读取到用户状态")
		}
		if snapshot.State != UserInteractionStateAwaitCustomRate {
			t.Fatalf("状态不匹配: want=%s got=%s", UserInteractionStateAwaitCustomRate, snapshot.State)
		}
		if snapshot.Scene != "正在设置汇率中" {
			t.Fatalf("场景描述异常: got=%q", snapshot.Scene)
		}

		redisKey := storeA.stateKey(chatID, userID)
		if !mini.Exists(redisKey) {
			t.Fatalf("Redis 中不存在状态键: %s", redisKey)
		}
		if ttl := mini.TTL(redisKey); ttl <= 0 {
			t.Fatalf("状态 TTL 异常: %v", ttl)
		}

		prev, hadPrev, err = storeB.Transition(context.Background(), chatID, userID, UserInteractionContext{
			State: UserInteractionStateAwaitPayoutRate,
			Scene: "正在设置下发汇率中",
		})
		if err != nil {
			t.Fatalf("状态流转到 AWAIT_PAYOUT_RATE 失败: %v", err)
		}
		if !hadPrev {
			t.Fatalf("状态流转应返回前置状态")
		}
		if prev.State != UserInteractionStateAwaitCustomRate {
			t.Fatalf("前置状态应为 AWAIT_RATE: %+v", prev)
		}

		snapshot, ok, err = storeA.Get(context.Background(), chatID, userID)
		if err != nil {
			t.Fatalf("回读流转后状态失败: %v", err)
		}
		if !ok || snapshot.State != UserInteractionStateAwaitPayoutRate {
			t.Fatalf("流转后状态异常: ok=%v snapshot=%+v", ok, snapshot)
		}

		mini.FastForward(6 * time.Minute)
		_, ok, err = storeA.Get(context.Background(), chatID, userID)
		if err != nil {
			t.Fatalf("读取过期状态失败: %v", err)
		}
		if ok {
			t.Fatalf("超过 TTL 后状态应自动过期")
		}

		if _, _, err = storeA.Transition(context.Background(), chatID, userID, UserInteractionContext{State: UserInteractionStateAwaitCustomRate}); err != nil {
			t.Fatalf("重新写入状态失败: %v", err)
		}
		if err := storeA.Clear(context.Background(), chatID, userID); err != nil {
			t.Fatalf("清除状态失败: %v", err)
		}
		_, ok, err = storeB.Get(context.Background(), chatID, userID)
		if err != nil {
			t.Fatalf("清除后读取状态失败: %v", err)
		}
		if ok {
			t.Fatalf("状态清除后不应可读")
		}
	})
}

func TestAppRateCommandUsesUserFSM(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "user-fsm-app.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("关闭数据库失败: %v", closeErr)
		}
	}()

	const (
		chatID int64 = -100222002
		userID int64 = 222002
	)
	if _, err := database.EnsureGroup(chatID, "用户状态机测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, userID); err != nil {
		t.Fatalf("设置权限人失败: %v", err)
	}

	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	initialPayoutRate := group.PayoutRate
	app := &App{
		db:      database,
		userFSM: NewMemoryUserFSM(MemoryUserFSMConfig{TTL: 5 * time.Minute}),
	}

	if err := app.dispatchGroupMessage(context.Background(), buildFSMGroupMessage(chatID, userID, 22201, "设置汇率"), group, "设置汇率"); err != nil {
		t.Fatalf("进入设置汇率状态失败: %v", err)
	}
	state, ok, err := app.getUserFSM().Get(context.Background(), chatID, userID)
	if err != nil {
		t.Fatalf("读取用户状态失败: %v", err)
	}
	if !ok || state.State != UserInteractionStateAwaitCustomRate {
		t.Fatalf("应进入 AWAIT_RATE 状态: ok=%v state=%+v", ok, state)
	}
	stagePayload, stageExists, err := database.GetTempData(chatID, userID, rateSetupTempDataKey)
	if err != nil {
		t.Fatalf("读取汇率暂存数据失败: %v", err)
	}
	if !stageExists {
		t.Fatalf("进入设置汇率状态后应写入临时暂存数据")
	}
	if stagePayload["mode"] != "custom" || stagePayload["scene"] != "正在设置汇率中" {
		t.Fatalf("汇率暂存数据内容异常: %+v", stagePayload)
	}

	group, err = database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("刷新群配置失败: %v", err)
	}
	if err := app.dispatchGroupMessage(context.Background(), buildFSMGroupMessage(chatID, userID, 22202, "7.18"), group, "7.18"); err != nil {
		t.Fatalf("消费设置汇率状态失败: %v", err)
	}

	group, err = database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取更新后的群配置失败: %v", err)
	}
	if almostZero(group.CustomRate - 7.18) {
		// noop
	} else {
		t.Fatalf("入款汇率未按状态机更新: got=%v", group.CustomRate)
	}
	if group.UseRealtimeRate != 0 {
		t.Fatalf("设置自定义汇率后应关闭实时汇率: got=%d", group.UseRealtimeRate)
	}
	_, ok, err = app.getUserFSM().Get(context.Background(), chatID, userID)
	if err != nil {
		t.Fatalf("读取设置完成后的状态失败: %v", err)
	}
	if ok {
		t.Fatalf("设置完成后状态应清空")
	}
	if _, stageExists, err = database.GetTempData(chatID, userID, rateSetupTempDataKey); err != nil {
		t.Fatalf("读取设置完成后的临时暂存数据失败: %v", err)
	} else if stageExists {
		t.Fatalf("设置完成后临时暂存数据应清空")
	}

	group, err = database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("刷新群配置失败: %v", err)
	}
	if err := app.dispatchGroupMessage(context.Background(), buildFSMGroupMessage(chatID, userID, 22203, "设置下发汇率"), group, "设置下发汇率"); err != nil {
		t.Fatalf("进入设置下发汇率状态失败: %v", err)
	}
	state, ok, err = app.getUserFSM().Get(context.Background(), chatID, userID)
	if err != nil {
		t.Fatalf("读取下发汇率状态失败: %v", err)
	}
	if !ok || state.State != UserInteractionStateAwaitPayoutRate {
		t.Fatalf("应进入 AWAIT_PAYOUT_RATE 状态: ok=%v state=%+v", ok, state)
	}
	stagePayload, stageExists, err = database.GetTempData(chatID, userID, rateSetupTempDataKey)
	if err != nil {
		t.Fatalf("读取下发汇率暂存数据失败: %v", err)
	}
	if !stageExists {
		t.Fatalf("进入设置下发汇率状态后应写入临时暂存数据")
	}
	if stagePayload["mode"] != "payout" || stagePayload["scene"] != "正在设置下发汇率中" {
		t.Fatalf("下发汇率暂存数据内容异常: %+v", stagePayload)
	}

	if err := app.dispatchGroupMessage(context.Background(), buildFSMGroupMessage(chatID, userID, 22204, "取消"), group, "取消"); err != nil {
		t.Fatalf("取消设置下发汇率失败: %v", err)
	}
	_, ok, err = app.getUserFSM().Get(context.Background(), chatID, userID)
	if err != nil {
		t.Fatalf("读取取消后的状态失败: %v", err)
	}
	if ok {
		t.Fatalf("取消后状态应清空")
	}
	if _, stageExists, err = database.GetTempData(chatID, userID, rateSetupTempDataKey); err != nil {
		t.Fatalf("读取取消后的临时暂存数据失败: %v", err)
	} else if stageExists {
		t.Fatalf("取消后临时暂存数据应清空")
	}

	group, err = database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取取消后的群配置失败: %v", err)
	}
	if !almostZero(group.PayoutRate - initialPayoutRate) {
		t.Fatalf("取消后不应写入下发汇率: got=%v want=%v", group.PayoutRate, initialPayoutRate)
	}
}

func TestControlPoint225SessionRecovery(t *testing.T) {
	t.Run("控制点 2.2.5 功能验证", func(t *testing.T) {
		database, err := db.NewDatabase(filepath.Join(t.TempDir(), "session-recovery.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer func() {
			if closeErr := database.Close(); closeErr != nil {
				t.Fatalf("关闭数据库失败: %v", closeErr)
			}
		}()

		const (
			chatID int64 = -100225001
			userID int64 = 225001
		)
		if _, err := database.EnsureGroup(chatID, "会话恢复测试群"); err != nil {
			t.Fatalf("初始化群配置失败: %v", err)
		}
		if err := database.SetOwner(chatID, userID); err != nil {
			t.Fatalf("设置权限人失败: %v", err)
		}

		bootA := &App{
			db:      database,
			userFSM: NewMemoryUserFSM(MemoryUserFSMConfig{TTL: 5 * time.Minute}),
		}
		group, err := database.GetGroup(chatID)
		if err != nil {
			t.Fatalf("读取群配置失败: %v", err)
		}
		if err := bootA.dispatchGroupMessage(context.Background(), buildFSMGroupMessage(chatID, userID, 22501, "设置汇率"), group, "设置汇率"); err != nil {
			t.Fatalf("进入设置汇率状态失败: %v", err)
		}
		if _, ok, err := database.GetTempData(chatID, userID, rateSetupTempDataKey); err != nil {
			t.Fatalf("读取会话暂存数据失败: %v", err)
		} else if !ok {
			t.Fatalf("进入设置汇率状态后应写入会话暂存数据")
		}

		bootB := &App{
			db:      database,
			userFSM: NewMemoryUserFSM(MemoryUserFSMConfig{TTL: 5 * time.Minute}),
		}
		if _, ok, err := bootB.getUserFSM().Get(context.Background(), chatID, userID); err != nil {
			t.Fatalf("重启后读取用户状态失败: %v", err)
		} else if ok {
			t.Fatalf("重启后内存状态机应为空")
		}
		group, err = database.GetGroup(chatID)
		if err != nil {
			t.Fatalf("刷新群配置失败: %v", err)
		}
		if err := bootB.dispatchGroupMessage(context.Background(), buildFSMGroupMessage(chatID, userID, 22502, "7.29"), group, "7.29"); err != nil {
			t.Fatalf("恢复后消费设置汇率状态失败: %v", err)
		}
		group, err = database.GetGroup(chatID)
		if err != nil {
			t.Fatalf("读取恢复后的群配置失败: %v", err)
		}
		if !almostZero(group.CustomRate - 7.29) {
			t.Fatalf("恢复后应写入入款汇率: got=%v", group.CustomRate)
		}
		if group.UseRealtimeRate != 0 {
			t.Fatalf("恢复后写入自定义汇率时应关闭实时汇率: got=%d", group.UseRealtimeRate)
		}
		if _, ok, err := bootB.getUserFSM().Get(context.Background(), chatID, userID); err != nil {
			t.Fatalf("读取恢复后状态失败: %v", err)
		} else if ok {
			t.Fatalf("恢复后完成设置应清空状态")
		}
		if _, ok, err := database.GetTempData(chatID, userID, rateSetupTempDataKey); err != nil {
			t.Fatalf("读取恢复后会话暂存数据失败: %v", err)
		} else if ok {
			t.Fatalf("恢复后完成设置应清空会话暂存数据")
		}

		group, err = database.GetGroup(chatID)
		if err != nil {
			t.Fatalf("读取群配置失败: %v", err)
		}
		if err := bootB.dispatchGroupMessage(context.Background(), buildFSMGroupMessage(chatID, userID, 22503, "设置下发汇率"), group, "设置下发汇率"); err != nil {
			t.Fatalf("进入设置下发汇率状态失败: %v", err)
		}
		bootC := &App{
			db:      database,
			userFSM: NewMemoryUserFSM(MemoryUserFSMConfig{TTL: 5 * time.Minute}),
		}
		group, err = database.GetGroup(chatID)
		if err != nil {
			t.Fatalf("刷新群配置失败: %v", err)
		}
		if err := bootC.dispatchGroupMessage(context.Background(), buildFSMGroupMessage(chatID, userID, 22504, "7.18"), group, "7.18"); err != nil {
			t.Fatalf("恢复后消费设置下发汇率状态失败: %v", err)
		}
		group, err = database.GetGroup(chatID)
		if err != nil {
			t.Fatalf("读取设置下发汇率后的群配置失败: %v", err)
		}
		if !almostZero(group.PayoutRate - 7.18) {
			t.Fatalf("恢复后应写入下发汇率: got=%v", group.PayoutRate)
		}
	})
}

func buildFSMGroupMessage(chatID, userID int64, messageID int, text string) *tgbotapi.Message {
	return &tgbotapi.Message{
		MessageID: messageID,
		Date:      int(time.Now().Unix()),
		Chat: &tgbotapi.Chat{
			ID:    chatID,
			Type:  "supergroup",
			Title: "用户状态机测试群",
		},
		From: &tgbotapi.User{
			ID:        userID,
			UserName:  "cp222",
			FirstName: "控制点",
			LastName:  "二二二",
		},
		Text: text,
	}
}
