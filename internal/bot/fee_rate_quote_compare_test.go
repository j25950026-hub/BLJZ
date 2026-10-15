package bot

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestMemberFeeRateQuoteCompareUsesDefaultAmount(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "member-fee-rate-quote-compare.db")

	const (
		chatID   int64 = -1004499101
		ownerID  int64 = 4499101
		memberID int64 = 4499199
		msgID          = 4499102
	)
	if _, err := database.EnsureGroup(chatID, "普通成员比价测试群"); err != nil {
		t.Fatalf("初始化群配置失败: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("设置群权限人失败: %v", err)
	}

	app.dispatchUpdate(context.Background(), fourLayerGroupTextUpdate(
		msgID, msgID, chatID, memberID, "member_compare", "比价 11-205 10-210 10-220", int(time.Now().Unix()),
	))

	snapshot := capture.snapshot()
	reply, ok := fourLayerFindReply(snapshot, chatID, msgID, "比价结果（默认金额：")
	if !ok {
		t.Fatalf("普通成员比价命令未返回: %+v", snapshot)
	}
	if reply.parseMode != telegramParseModeHTML {
		t.Fatalf("比价结果应使用 HTML parse mode: got=%q reply=%+v", reply.parseMode, reply)
	}
	required := []string{
		"默认金额：<code>100000</code>",
		"公式：<code>金额*(1-费率/100)/汇率</code>",
		"1. <code>11-205</code> = <code>434.15</code> 最高",
		"2. <code>10-210</code> = <code>428.57</code>，较最高低 <code>1.28%</code>",
		"3. <code>10-220</code> = <code>409.09</code>，较最高低 <code>5.77%</code>",
		"<code>11-205</code> 比 <code>10-210</code> 高 <code>1.3%</code>",
		"<code>11-205</code> 比 <code>10-220</code> 高 <code>6.12%</code>",
		"<code>10-210</code> 比 <code>10-220</code> 高 <code>4.76%</code>",
	}
	for _, fragment := range required {
		if !strings.Contains(reply.text, fragment) {
			t.Fatalf("比价结果缺少片段 %q，got=%q", fragment, reply.text)
		}
	}
}

func TestFeeRateQuoteCompareSupportsMultilineInput(t *testing.T) {
	reply, handled := renderFeeRateQuoteCompareReply("金额最大\n11-205\n10-210\n10-220")
	if !handled {
		t.Fatalf("多行比价命令应被识别")
	}
	if !strings.Contains(reply, "默认金额：<code>100000</code>") ||
		!strings.Contains(reply, "1. <code>11-205</code> = <code>434.15</code> 最高") {
		t.Fatalf("多行比价结果异常: %q", reply)
	}
}

func TestFeeRateQuoteCompareRejectsInvalidInput(t *testing.T) {
	reply, handled := renderFeeRateQuoteCompareReply("比价 11-205")
	if !handled {
		t.Fatalf("比价命令格式错误时也应返回说明")
	}
	if !strings.Contains(reply, "至少发送 2 组方案") ||
		!strings.Contains(reply, "默认金额：<code>100000</code>") {
		t.Fatalf("比价格式错误说明异常: %q", reply)
	}
}
