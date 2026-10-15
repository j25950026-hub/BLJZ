package db

import (
	"path/filepath"
	"testing"
	"time"
)

func TestPrivatePurchaseOrderFlow(t *testing.T) {
	t.Run("购买订单创建与审核闭环", func(t *testing.T) {
		database, err := NewDatabase(filepath.Join(t.TempDir(), "private-purchase-order.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		orderID, err := database.AddPrivatePurchaseOrder(PrivatePurchaseOrderInput{
			UserID:       90001,
			PlanName:     "标准月卡",
			DurationDays: 30,
			Amount:       299,
			Currency:     "cny",
			RequestNote:  "测试订单",
			Source:       "private_buy_button",
		})
		if err != nil {
			t.Fatalf("创建购买订单失败: %v", err)
		}
		if orderID == 0 {
			t.Fatalf("订单ID不应为0")
		}

		userOrders, err := database.ListPrivatePurchaseOrdersByUser(90001, 10)
		if err != nil {
			t.Fatalf("读取用户订单失败: %v", err)
		}
		if len(userOrders) != 1 {
			t.Fatalf("用户订单数量异常: got=%d", len(userOrders))
		}
		if userOrders[0].Status != "pending" {
			t.Fatalf("新建订单状态应为pending: got=%s", userOrders[0].Status)
		}

		expireAt := time.Now().UTC().Add(30 * 24 * time.Hour).Format(time.RFC3339)
		if err := database.ReviewPrivatePurchaseOrder(orderID, "approved", "已收款", 10001, expireAt); err != nil {
			t.Fatalf("审核购买订单失败: %v", err)
		}

		order, err := database.GetPrivatePurchaseOrder(orderID)
		if err != nil {
			t.Fatalf("读取购买订单失败: %v", err)
		}
		if order.Status != "approved" {
			t.Fatalf("审核后状态错误: got=%s", order.Status)
		}
		if order.ExpiresAt != expireAt {
			t.Fatalf("审核后过期时间错误: want=%s got=%s", expireAt, order.ExpiresAt)
		}
		if order.ReviewedBy != 10001 {
			t.Fatalf("审核人记录错误: got=%d", order.ReviewedBy)
		}
	})
}

func TestPrivatePurchaseOrderReviewRejectInvalidStatus(t *testing.T) {
	database, err := NewDatabase(filepath.Join(t.TempDir(), "private-purchase-order-invalid-status.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer database.Close()

	orderID, err := database.AddPrivatePurchaseOrder(PrivatePurchaseOrderInput{
		UserID:       90002,
		PlanName:     "标准季卡",
		DurationDays: 90,
	})
	if err != nil {
		t.Fatalf("创建购买订单失败: %v", err)
	}
	order, err := database.GetPrivatePurchaseOrder(orderID)
	if err != nil {
		t.Fatalf("读取购买订单失败: %v", err)
	}
	if order.Currency != "" {
		t.Fatalf("未指定币种时不应回填默认币种: got=%q", order.Currency)
	}

	if err := database.ReviewPrivatePurchaseOrder(orderID, "unknown", "无效状态测试", 10002, ""); err == nil {
		t.Fatalf("非法审核状态应返回错误")
	}
}
