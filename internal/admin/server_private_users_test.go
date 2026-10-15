package admin

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"go-bot/internal/config"
	"go-bot/internal/db"
)

func TestAdminPrivateUsersReviewOrder(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "admin-private-users.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	orderID, err := database.AddPrivatePurchaseOrder(db.PrivatePurchaseOrderInput{
		UserID:       70001,
		PlanName:     "标准月卡",
		DurationDays: 30,
		Amount:       299,
		Currency:     "CNY",
		RequestNote:  "后台审批测试",
		Source:       "test",
	})
	if err != nil {
		t.Fatalf("创建测试订单失败: %v", err)
	}

	server := NewServer(config.Config{AdminToken: "private-token"}, database, nil)

	postBody := "action=review_order&order_id=" + strconv.FormatInt(orderID, 10) + "&review_status=approved&override_days=30&reviewed_by=90001&review_note=%E6%B5%8B%E8%AF%95%E9%80%9A%E8%BF%87"
	req := httptest.NewRequest(http.MethodPost, "/admin/private-users?token=private-token", strings.NewReader(postBody))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp := httptest.NewRecorder()

	server.handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("审批页面状态码错误: %d", resp.Code)
	}
	if !strings.Contains(resp.Body.String(), "订单 #"+strconv.FormatInt(orderID, 10)+" 已更新为 approved") {
		t.Fatalf("页面应返回审批成功信息: %s", resp.Body.String())
	}

	order, err := database.GetPrivatePurchaseOrder(orderID)
	if err != nil {
		t.Fatalf("读取订单失败: %v", err)
	}
	if order.Status != "approved" {
		t.Fatalf("订单状态应更新为approved，got=%s", order.Status)
	}

	profile, err := database.GetPrivateUserProfile(70001)
	if err != nil {
		t.Fatalf("读取用户档案失败: %v", err)
	}
	if strings.TrimSpace(profile.ExpireAt) == "" {
		t.Fatalf("审批通过后应写入到期时间")
	}
}
