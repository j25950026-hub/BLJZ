package bot

import (
	"errors"
	"strings"
	"testing"
	"time"

	"go-bot/internal/db"
)

func TestExecuteWebAppPrivateActionTrialApplyIsClosed(t *testing.T) {
	app, database, _ := newFourLayerTestApp(t, "webapp-private-action-trial.db")
	userID := int64(4501001)

	applyResult, err := app.ExecuteWebAppPrivateAction(userID, 0, "申请试用")
	if err != nil {
		t.Fatalf("WebApp 申请试用失败: %v", err)
	}
	if !strings.Contains(applyResult.DisplayText, "联系管理员") {
		t.Fatalf("关闭试用申请后应提示联系管理员: %+v", applyResult)
	}

	profile, err := database.GetPrivateUserProfile(userID)
	if err != nil {
		t.Fatalf("读取私域用户档案失败: %v", err)
	}
	if strings.TrimSpace(profile.TrialStatus) != "" || strings.TrimSpace(profile.ExpireAt) != "" {
		t.Fatalf("关闭试用申请后不应写入试用权限: %+v", profile)
	}

	expireResult, err := app.ExecuteWebAppPrivateAction(userID, 0, "到期时间")
	if err != nil {
		t.Fatalf("WebApp 查询到期时间失败: %v", err)
	}
	if !strings.Contains(expireResult.DisplayText, "联系管理员") {
		t.Fatalf("到期时间回执不符合预期: %+v", expireResult)
	}
}

func TestExecuteWebAppPrivateActionGetIDAndSupport(t *testing.T) {
	app, _, _ := newFourLayerTestApp(t, "webapp-private-action-id-support.db")
	app.cfg.SupportURL = "@support_demo"

	userID := int64(4502001)
	chatID := int64(-1004502001)

	idResult, err := app.ExecuteWebAppPrivateAction(userID, chatID, "获取ID")
	if err != nil {
		t.Fatalf("WebApp 获取ID失败: %v", err)
	}
	if !idResult.Mono {
		t.Fatalf("获取ID应返回 monospace 文案")
	}
	if !strings.Contains(idResult.DisplayText, "当前用户ID：4502001") || !strings.Contains(idResult.DisplayText, "当前群ID：-1004502001") {
		t.Fatalf("获取ID文案不符合预期: %+v", idResult)
	}

	supportResult, err := app.ExecuteWebAppPrivateAction(userID, chatID, "人工客服")
	if err != nil {
		t.Fatalf("WebApp 人工客服请求失败: %v", err)
	}
	if supportResult.OpenURL != "https://t.me/support_demo" {
		t.Fatalf("人工客服直达链接错误: %s", supportResult.OpenURL)
	}
	if !strings.Contains(supportResult.DisplayText, "直达入口") {
		t.Fatalf("人工客服文案应包含直达入口提示: %+v", supportResult)
	}

	contactResult, err := app.ExecuteWebAppPrivateAction(userID, chatID, "联系管理员")
	if err != nil {
		t.Fatalf("WebApp 联系管理员请求失败: %v", err)
	}
	if contactResult.OpenURL != "https://t.me/support_demo" {
		t.Fatalf("联系管理员直达链接错误: %s", contactResult.OpenURL)
	}
}

func TestExecuteWebAppPrivateActionExpirePathAndUnsupportedAction(t *testing.T) {
	app, database, _ := newFourLayerTestApp(t, "webapp-private-action-expired.db")
	userID := int64(4503001)
	expiredAt := time.Now().UTC().Add(-2 * time.Hour)

	if err := database.UpsertPrivateUserProfile(db.PrivateUserProfile{
		UserID:         userID,
		TrialStatus:    "active",
		TrialAppliedAt: expiredAt.Add(-24 * time.Hour).Format(time.RFC3339),
		ExpireAt:       expiredAt.Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("写入到期用户档案失败: %v", err)
	}

	expireResult, err := app.ExecuteWebAppPrivateAction(userID, 0, "到期时间")
	if err != nil {
		t.Fatalf("到期路径查询失败: %v", err)
	}
	if !strings.Contains(expireResult.DisplayText, "已到期") {
		t.Fatalf("到期提示文案缺失: %+v", expireResult)
	}

	profile, err := database.GetPrivateUserProfile(userID)
	if err != nil {
		t.Fatalf("读取到期档案失败: %v", err)
	}
	if profile.TrialStatus != "expired" {
		t.Fatalf("到期查询后应更新为 expired，got=%s", profile.TrialStatus)
	}

	if _, err := app.ExecuteWebAppPrivateAction(userID, 0, "菜单"); !errors.Is(err, ErrUnsupportedWebAppPrivateAction) {
		t.Fatalf("未知动作应返回 ErrUnsupportedWebAppPrivateAction，got=%v", err)
	}
}
