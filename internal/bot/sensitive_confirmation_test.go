package bot

import (
	"testing"
	"time"
)

func TestControlPoint243SensitiveOperationConfirmation(t *testing.T) {
	t.Run("控制点 2.4.3 功能验证", func(t *testing.T) {
		confirmCommand, ok := sensitiveOperationConfirmCommand("清理今日数据")
		if !ok || confirmCommand != "确认清理今日数据" {
			t.Fatalf("确认指令映射异常: ok=%v cmd=%q", ok, confirmCommand)
		}
		command, ok := sensitiveOperationByConfirm("确认清理今日数据")
		if !ok || command != "清理今日数据" {
			t.Fatalf("反向映射异常: ok=%v cmd=%q", ok, command)
		}

		base := time.Unix(1700000000, 0)
		confirmer := newSensitiveOperationConfirmer(45 * time.Second)
		expireAt := confirmer.RequestAt(1001, 2001, "清理今日数据", base)
		if !expireAt.Equal(base.Add(45 * time.Second)) {
			t.Fatalf("确认过期时间异常: want=%v got=%v", base.Add(45*time.Second), expireAt)
		}
		status, pending := confirmer.ConfirmAt(1001, 2001, "清理今日数据", base.Add(10*time.Second))
		if status != sensitiveOperationConfirmationAccepted {
			t.Fatalf("二次确认应通过: status=%d pending=%q", status, pending)
		}
	})

	t.Run("控制点 2.4.3 超时验证", func(t *testing.T) {
		base := time.Unix(1700000000, 0)
		confirmer := newSensitiveOperationConfirmer(10 * time.Second)
		confirmer.RequestAt(1001, 2001, "清理入款数据", base)
		status, pending := confirmer.ConfirmAt(1001, 2001, "清理入款数据", base.Add(11*time.Second))
		if status != sensitiveOperationConfirmationExpired {
			t.Fatalf("二次确认应超时: status=%d pending=%q", status, pending)
		}
		if pending != "清理入款数据" {
			t.Fatalf("超时应返回原始命令: got=%q", pending)
		}
	})

	t.Run("控制点 2.4.3 指令错配验证", func(t *testing.T) {
		base := time.Unix(1700000000, 0)
		confirmer := newSensitiveOperationConfirmer(30 * time.Second)
		confirmer.RequestAt(1001, 2001, "清理下发数据", base)
		status, pending := confirmer.ConfirmAt(1001, 2001, "清理今日数据", base.Add(5*time.Second))
		if status != sensitiveOperationConfirmationMismatch {
			t.Fatalf("二次确认应命令错配: status=%d pending=%q", status, pending)
		}
		if pending != "清理下发数据" {
			t.Fatalf("错配应返回待确认命令: got=%q", pending)
		}
		status, pending = confirmer.ConfirmAt(1001, 2001, "清理下发数据", base.Add(8*time.Second))
		if status != sensitiveOperationConfirmationAccepted {
			t.Fatalf("修正后二次确认应通过: status=%d pending=%q", status, pending)
		}
	})
}
