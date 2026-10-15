package bot

import (
	"testing"
	"time"
)

func TestControlPoint154ReplayAttackProtection(t *testing.T) {
	t.Run("控制点 1.5.4 功能验证", func(t *testing.T) {
		now := time.Date(2026, 2, 18, 10, 0, 0, 0, time.UTC)

		msgInWindow := now.Add(-30 * time.Second)
		expired, _ := isReplayExpired(int(msgInWindow.Unix()), now, 60)
		if expired {
			t.Fatalf("窗口内消息被误判为重放")
		}

		msgOnBoundary := now.Add(-60 * time.Second)
		expired, _ = isReplayExpired(int(msgOnBoundary.Unix()), now, 60)
		if expired {
			t.Fatalf("刚好 60 秒的消息不应被拦截")
		}

		msgOutWindow := now.Add(-61 * time.Second)
		expired, age := isReplayExpired(int(msgOutWindow.Unix()), now, 60)
		if !expired {
			t.Fatalf("超过 60 秒的旧消息应被识别为重放")
		}
		if int(age.Seconds()) < 61 {
			t.Fatalf("重放年龄计算异常: %v", age)
		}

		expired, _ = isReplayExpired(0, now, 60)
		if expired {
			t.Fatalf("无日期消息不应触发重放拦截")
		}
	})
}

func TestTrimForAudit(t *testing.T) {
	short := trimForAudit("  abc  ", 10)
	if short != "abc" {
		t.Fatalf("短文本裁剪异常: %q", short)
	}

	long := trimForAudit("123456789", 4)
	if long != "1234" {
		t.Fatalf("长文本裁剪异常: %q", long)
	}
}
