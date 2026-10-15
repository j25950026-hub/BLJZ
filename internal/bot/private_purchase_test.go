package bot

import (
	"path/filepath"
	"strings"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/config"
	"go-bot/internal/db"
)

func TestPrivatePurchaseMarkupHasCallbacks(t *testing.T) {
	app := &App{cfg: configForPurchaseTest()}
	markup := app.privatePurchaseMarkup(90001)

	required := []string{
		privateCallbackPrefix + "buy_30",
		privateCallbackPrefix + "buy_90",
		privateCallbackPrefix + "buy_365",
		privateCallbackPrefix + "buy_orders",
		privateCallbackPrefix + "menu",
	}
	for _, key := range required {
		if !hasPurchaseCallback(markup, key) {
			t.Fatalf("购买菜单缺少回调按钮: %s", key)
		}
	}
}

func TestPrivateReplyMenuUsesContactAdminInsteadOfTrialApply(t *testing.T) {
	markup := privateReplyMenuMarkup()
	labels := map[string]bool{}
	for _, row := range markup.Keyboard {
		for _, button := range row {
			labels[strings.TrimSpace(button.Text)] = true
		}
	}

	if labels["申请试用"] {
		t.Fatalf("私聊菜单不应继续展示申请试用按钮: %+v", labels)
	}
	if !labels["联系管理员"] {
		t.Fatalf("私聊菜单应展示联系管理员按钮: %+v", labels)
	}
}

func TestCreatePrivatePurchaseOrder(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "private-purchase-bot.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	app := &App{
		db:  database,
		cfg: configForPurchaseTest(),
	}
	text, err := app.createPrivatePurchaseOrder(90002, "buy_30", &tgbotapi.User{ID: 90002, UserName: "buyer"})
	if err != nil {
		t.Fatalf("创建购买订单失败: %v", err)
	}
	if !strings.Contains(text, "购买申请已提交") {
		t.Fatalf("返回文案错误: %s", text)
	}

	orders, err := database.ListPrivatePurchaseOrdersByUser(90002, 10)
	if err != nil {
		t.Fatalf("读取购买订单失败: %v", err)
	}
	if len(orders) != 1 {
		t.Fatalf("购买订单数量错误: got=%d", len(orders))
	}
	if orders[0].Status != "pending" {
		t.Fatalf("购买订单初始状态错误: got=%s", orders[0].Status)
	}
	if orders[0].PlanName != "标准月卡" {
		t.Fatalf("购买订单套餐错误: got=%s", orders[0].PlanName)
	}
}

func hasPurchaseCallback(markup tgbotapi.InlineKeyboardMarkup, target string) bool {
	for _, row := range markup.InlineKeyboard {
		for _, btn := range row {
			if btn.CallbackData != nil && strings.TrimSpace(*btn.CallbackData) == target {
				return true
			}
		}
	}
	return false
}

func configForPurchaseTest() config.Config {
	return config.Config{
		SupportChatID: 0,
		SupportURL:    "",
	}
}
