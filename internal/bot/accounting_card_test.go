package bot

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/config"
	"go-bot/internal/db"
)

func TestBuildAccountingDataCardFromRowsStrictLayout(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	rows := []db.LedgerEntry{
		{
			EntryType:   "deposit",
			FinalAmount: 10000,
			Rate:        10,
			Note:        "首笔入款",
			CreatedAt:   "2026-02-21T01:07:12Z",
		},
		{
			EntryType:   "payout",
			FinalAmount: 2000,
			Currency:    "CNY",
			Rate:        10,
			Note:        "下发备注",
			CreatedAt:   "2026-02-21T02:00:00Z",
		},
	}

	got := buildAccountingDataCardFromRows(rows, 0, 10, loc)
	required := []string{
		"入款 (1) 笔:",
		"09:07:12 10000 * 1 / 10 = 1000.00U 首笔入款",
		"下发 (1) 笔:",
		"10:00:00 2000 [200.00 U] 下发备注",
		"当前入款: 10000 | 1000.00 U",
		"入款费率: 0%",
		"入款汇率: 10",
		"应下发: 1000.00 U",
		"总下发: 200.00 U",
		"未下发: 800.00 U",
	}
	for _, fragment := range required {
		if !strings.Contains(got, fragment) {
			t.Fatalf("记账卡片缺少片段 %q，got=%s", fragment, got)
		}
	}
}

func TestBuildAccountingDataCardFromRowsPayoutColumnsAligned(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	rows := []db.LedgerEntry{
		{
			EntryType: "payout",
			Amount:    5868,
			Currency:  "CNY",
			Rate:      7,
			Nickname:  "IN Clearing Hub2",
			CreatedAt: "2026-02-21T07:04:07Z",
		},
		{
			EntryType: "payout",
			Amount:    3000,
			Currency:  "CNY",
			Rate:      7,
			Nickname:  "IN Clearing Hub2",
			CreatedAt: "2026-02-21T07:25:58Z",
		},
		{
			EntryType: "payout",
			Amount:    5000,
			Rate:      0,
			Nickname:  "IN Clearing Hub2",
			CreatedAt: "2026-02-21T07:27:08Z",
		},
		{
			EntryType: "payout",
			Amount:    252,
			Rate:      0,
			Nickname:  "IN Clearing Hub2",
			CreatedAt: "2026-02-21T07:48:05Z",
		},
		{
			EntryType: "payout",
			Amount:    3145,
			Rate:      0,
			Nickname:  "IN Clearing Hub2",
			CreatedAt: "2026-02-21T08:11:56Z",
		},
	}

	got := buildAccountingDataCardFromRows(rows, 0, 0, loc)
	required := []string{
		"15:04:07 5868 [ 838.29 U]  |  IN Clearing Hub2",
		"15:25:58 3000 [ 428.57 U]  |  IN Clearing Hub2",
		"15:27:08 5000 [5000.00 U]  |  IN Clearing Hub2",
		"15:48:05  252 [ 252.00 U]  |  IN Clearing Hub2",
		"16:11:56 3145 [3145.00 U]  |  IN Clearing Hub2",
	}
	for _, fragment := range required {
		if !strings.Contains(got, fragment) {
			t.Fatalf("下发列宽应按账单内最大值补空格对齐，缺少 %q，got=%s", fragment, got)
		}
	}
}

func TestBuildAccountingDataCardFromRowsPayoutUSDTUsesUnifiedUDisplay(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	rows := []db.LedgerEntry{
		{
			EntryType: "payout",
			Amount:    100,
			Currency:  "CNY",
			Rate:      0,
			Nickname:  "IN Clearing Hub2",
			CreatedAt: "2026-02-21T09:34:05Z",
		},
		{
			EntryType: "payout",
			Amount:    100,
			Currency:  "USDT",
			Rate:      7,
			Nickname:  "IN Clearing Hub2",
			CreatedAt: "2026-02-21T09:34:27Z",
		},
		{
			EntryType: "payout",
			Amount:    -100,
			Currency:  "USDT",
			Rate:      7,
			Nickname:  "IN Clearing Hub2",
			CreatedAt: "2026-02-21T09:35:42Z",
		},
		{
			EntryType: "payout",
			Amount:    -100,
			Currency:  "CNY",
			Rate:      0,
			Nickname:  "IN Clearing Hub2",
			CreatedAt: "2026-02-21T09:36:12Z",
		},
	}

	got := buildAccountingDataCardFromRows(rows, 0, 0, loc)
	required := []string{
		"17:34:05  100 [ 100.00 U]  |  IN Clearing Hub2",
		"17:34:27  100 [ 100.00 U]  |  IN Clearing Hub2",
		"17:35:42 -100 [-100.00 U]  |  IN Clearing Hub2",
		"17:36:12 -100 [-100.00 U]  |  IN Clearing Hub2",
	}
	for _, fragment := range required {
		if !strings.Contains(got, fragment) {
			t.Fatalf("USDT 下发应隐藏金额列币种且括号内统一显示 U，缺少 %q，got=%s", fragment, got)
		}
	}
	if strings.Contains(got, "USDT") {
		t.Fatalf("USDT 下发金额列不应显示 USDT，got=%s", got)
	}
}

func TestBuildAccountingDataCardFromRowsNoRemarkHidden(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	rows := []db.LedgerEntry{
		{
			EntryType: "deposit",
			Amount:    7000,
			Rate:      7,
			CreatedAt: "2026-02-21T00:01:09Z",
		},
	}

	got := buildAccountingDataCardFromRows(rows, 0, 7, loc)
	if strings.Contains(got, "无") {
		t.Fatalf("空备注不应显示“无”，got=%s", got)
	}
	if !strings.Contains(got, "08:01:09 7000 * 1 / 7 = 1000.00U") {
		t.Fatalf("入款换算应在等号右侧追加大写U，got=%s", got)
	}
}

func TestBuildAccountingDataCardFromRowsReversalEntryRemarkHidden(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	rows := []db.LedgerEntry{
		{
			EntryType: "payout",
			Amount:    500,
			Currency:  "USDT",
			Rate:      7,
			Note:      "REV:44 原始备注",
			CreatedAt: "2026-02-21T00:01:09Z",
		},
	}
	got := buildAccountingDataCardFromRows(rows, 0, 7, loc)
	if strings.Contains(got, "撤回") {
		t.Fatalf("反向撤回分录不应额外展示撤回标记，got=%s", got)
	}
	if strings.Contains(got, "REV:44") {
		t.Fatalf("撤回备注不应展示原始REV标记，got=%s", got)
	}
	if strings.Contains(got, "08:01:09") {
		t.Fatalf("反向撤回分录整行不应展示，got=%s", got)
	}
	required := []string{
		"下发 (0) 笔:",
		"总下发: 0.00 U",
	}
	for _, fragment := range required {
		if !strings.Contains(got, fragment) {
			t.Fatalf("反向撤回分录应从账单统计中剔除，缺少 %q，got=%s", fragment, got)
		}
	}
}

func TestBuildAccountingDataCardFromRowsOriginalEntryMarkedReversed(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	reversalID := int64(88)
	originID := int64(44)
	rows := []db.LedgerEntry{
		{
			ID:         originID,
			EntryType:  "deposit",
			Amount:     5000,
			Rate:       137,
			CreatedAt:  "2026-02-21T00:01:09Z",
			Reversed:   1,
			ReversalOf: &reversalID,
		},
		{
			ID:         reversalID,
			EntryType:  "deposit",
			Amount:     -5000,
			Rate:       137,
			Note:       "REV:44 原始备注",
			CreatedAt:  "2026-02-21T00:02:09Z",
			ReversalOf: &originID,
		},
	}
	got := buildAccountingDataCardFromRows(rows, 0, 137, loc)
	if !strings.Contains(got, "入款 (1) 笔:") {
		t.Fatalf("已撤回原始分录仍应保留可见历史行，got=%s", got)
	}
	if !strings.Contains(got, "08:01:09 5000 * 1 / 137 = 36.50U 已撤回") {
		t.Fatalf("原始分录应标记为已撤回，got=%s", got)
	}
	if strings.Contains(got, "08:02:09 -5000 * 1 / 137 = -36.50U") {
		t.Fatalf("反向撤回分录整行不应展示，got=%s", got)
	}
	if strings.Contains(got, "REV:44") {
		t.Fatalf("反向撤回分录不应展示原始REV标记，got=%s", got)
	}
	required := []string{
		"当前入款: 0 | 0.00 U",
		"应下发: 0.00 U",
		"未下发: 0.00 U",
	}
	for _, fragment := range required {
		if !strings.Contains(got, fragment) {
			t.Fatalf("已撤回原始分录不应继续计入当前统计，缺少 %q，got=%s", fragment, got)
		}
	}
}

func TestBuildAccountingDataCardHidesReversedRowsWhenEnabled(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	reversalID := int64(188)
	originID := int64(144)
	rows := []db.LedgerEntry{
		{
			ID:         originID,
			EntryType:  "deposit",
			Amount:     5000,
			Rate:       137,
			CreatedAt:  "2026-02-21T00:01:09Z",
			Reversed:   1,
			ReversalOf: &reversalID,
		},
		{
			ID:         reversalID,
			EntryType:  "deposit",
			Amount:     -5000,
			Rate:       137,
			Note:       "REV:144 原始备注",
			CreatedAt:  "2026-02-21T00:02:09Z",
			ReversalOf: &originID,
		},
		{
			ID:        155,
			EntryType: "deposit",
			Amount:    7000,
			Rate:      137,
			Note:      "保留交易",
			CreatedAt: "2026-02-21T00:03:09Z",
		},
	}
	config := ledgerCardDisplayConfig{HideReversed: true}

	plain := buildAccountingDataCardFromRows(rows, 0, 137, loc, config)
	markdown := buildAccountingDataCardMarkdownV2FromRows(rows, 0, 137, loc, config)
	for name, got := range map[string]string{"plain": plain, "markdown": markdown} {
		if strings.Contains(got, "已撤回") || strings.Contains(got, "REV:144") || strings.Contains(got, "5000") {
			t.Fatalf("%s 卡片不应显示已撤回交易: %s", name, got)
		}
		if !strings.Contains(got, "保留交易") {
			t.Fatalf("%s 卡片应保留未撤回交易: %s", name, got)
		}
	}
}

func TestLedgerCardReceiptMarkupSingleButton(t *testing.T) {
	markup := ledgerCardReceiptMarkup()
	if len(markup.InlineKeyboard) != 1 {
		t.Fatalf("应只有一行按钮，got=%d", len(markup.InlineKeyboard))
	}
	if len(markup.InlineKeyboard[0]) != 1 {
		t.Fatalf("按钮行应为单按钮，got=%d", len(markup.InlineKeyboard[0]))
	}
	btn := markup.InlineKeyboard[0][0]
	if btn.Text != "💰 查看账单" {
		t.Fatalf("按钮文案异常: %s", btn.Text)
	}
	if btn.CallbackData == nil || *btn.CallbackData != ledgerCardCallbackPrefix+ledgerCardActionDetail {
		t.Fatalf("回调路由异常: %+v", btn.CallbackData)
	}
}

func TestBuildAccountingDataCardMarkdownV2FromRowsCodeStyle(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	rows := []db.LedgerEntry{
		{
			EntryType: "deposit",
			Amount:    7000,
			Rate:      7,
			CreatedAt: "2026-02-21T00:01:09Z",
			Note:      "RICH-戒酒",
		},
	}
	got := buildAccountingDataCardMarkdownV2FromRows(rows, 0, 7, loc)
	required := []string{
		"今日入款 \\(`1`\\) 笔:",
		"`08:01:09` 7000 \\* 1 / 7 \\= `1000.00U`  \\| RICH\\-戒酒",
		"当前入款: `7000` \\| `1000.00` U",
		"应下发: `1000.00` U",
	}
	for _, fragment := range required {
		if !strings.Contains(got, fragment) {
			t.Fatalf("MarkdownV2 卡片缺少片段 %q，got=%s", fragment, got)
		}
	}
}

func TestBuildAccountingDataCardMarkdownV2FromRowsDepositFormulaCompactAndResultCopyable(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	rows := []db.LedgerEntry{
		{
			EntryType: "deposit",
			Amount:    1000,
			Rate:      7,
			Nickname:  "甲",
			CreatedAt: "2026-02-21T00:01:09Z",
		},
		{
			EntryType: "deposit",
			Amount:    231480,
			Rate:      124,
			FeeRate:   7,
			Nickname:  "乙",
			CreatedAt: "2026-02-21T00:02:09Z",
		},
	}

	got := buildAccountingDataCardMarkdownV2FromRows(rows, 0, 7, loc)
	required := []string{
		"`08:01:09` 1000 \\* 1 / 7 \\= `142.86U`  \\|  甲",
		"`08:02:09` 231480 \\* 0\\.93 / 124 \\= `1736.10U`  \\|  乙",
	}
	for _, fragment := range required {
		if !strings.Contains(got, fragment) {
			t.Fatalf("MarkdownV2 入款公式不够紧致或最终金额不可独立复制，缺少 %q，got=%s", fragment, got)
		}
	}
	if strings.Contains(got, "\\= ` ` `") || strings.Contains(got, "\\= `  ` `") {
		t.Fatalf("MarkdownV2 紧致格式不应在结算金额前生成空白 code spacer，got=%s", got)
	}
	if strings.Contains(got, "`1000 * 1 / 7 = 142.86U`") {
		t.Fatalf("最终金额不应和整条计算式绑定在同一个 code 片段中，got=%s", got)
	}
}

func TestBuildAccountingDataCardMarkdownV2FromRowsPayoutColumnsAligned(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	rows := []db.LedgerEntry{
		{
			EntryType: "payout",
			Amount:    5868,
			Currency:  "CNY",
			Rate:      7,
			Nickname:  "IN Clearing Hub2",
			CreatedAt: "2026-02-21T07:04:07Z",
		},
		{
			EntryType: "payout",
			Amount:    3000,
			Currency:  "CNY",
			Rate:      7,
			Nickname:  "IN Clearing Hub2",
			CreatedAt: "2026-02-21T07:25:58Z",
		},
		{
			EntryType: "payout",
			Amount:    5000,
			Rate:      0,
			Nickname:  "IN Clearing Hub2",
			CreatedAt: "2026-02-21T07:27:08Z",
		},
		{
			EntryType: "payout",
			Amount:    252,
			Rate:      0,
			Nickname:  "IN Clearing Hub2",
			CreatedAt: "2026-02-21T07:48:05Z",
		},
		{
			EntryType: "payout",
			Amount:    3145,
			Rate:      0,
			Nickname:  "IN Clearing Hub2",
			CreatedAt: "2026-02-21T08:11:56Z",
		},
	}

	got := buildAccountingDataCardMarkdownV2FromRows(rows, 0, 0, loc)
	required := []string{
		"`15:04:07` 5868 \\[` 838.29 U`\\]  \\|  IN Clearing Hub2",
		"`15:25:58` 3000 \\[` 428.57 U`\\]  \\|  IN Clearing Hub2",
		"`15:27:08` 5000 \\[`5000.00 U`\\]  \\|  IN Clearing Hub2",
		"`15:48:05`  252 \\[` 252.00 U`\\]  \\|  IN Clearing Hub2",
		"`16:11:56` 3145 \\[`3145.00 U`\\]  \\|  IN Clearing Hub2",
	}
	for _, fragment := range required {
		if !strings.Contains(got, fragment) {
			t.Fatalf("MarkdownV2 下发列宽应按账单内最大值补空格对齐，缺少 %q，got=%s", fragment, got)
		}
	}
}

func TestBuildAccountingDataCardMarkdownV2FromRowsPayoutUSDTUsesUnifiedUDisplay(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	rows := []db.LedgerEntry{
		{
			EntryType: "payout",
			Amount:    100,
			Currency:  "CNY",
			Rate:      0,
			Nickname:  "IN Clearing Hub2",
			CreatedAt: "2026-02-21T09:34:05Z",
		},
		{
			EntryType: "payout",
			Amount:    100,
			Currency:  "USDT",
			Rate:      7,
			Nickname:  "IN Clearing Hub2",
			CreatedAt: "2026-02-21T09:34:27Z",
		},
		{
			EntryType: "payout",
			Amount:    -100,
			Currency:  "USDT",
			Rate:      7,
			Nickname:  "IN Clearing Hub2",
			CreatedAt: "2026-02-21T09:35:42Z",
		},
		{
			EntryType: "payout",
			Amount:    -100,
			Currency:  "CNY",
			Rate:      0,
			Nickname:  "IN Clearing Hub2",
			CreatedAt: "2026-02-21T09:36:12Z",
		},
	}

	got := buildAccountingDataCardMarkdownV2FromRows(rows, 0, 0, loc)
	required := []string{
		"`17:34:05`  100 \\[` 100.00 U`\\]  \\|  IN Clearing Hub2",
		"`17:34:27`  100 \\[` 100.00 U`\\]  \\|  IN Clearing Hub2",
		"`17:35:42` \\-100 \\[`-100.00 U`\\]  \\|  IN Clearing Hub2",
		"`17:36:12` \\-100 \\[`-100.00 U`\\]  \\|  IN Clearing Hub2",
	}
	for _, fragment := range required {
		if !strings.Contains(got, fragment) {
			t.Fatalf("MarkdownV2 USDT 下发应隐藏金额列币种且括号内统一显示 U，缺少 %q，got=%s", fragment, got)
		}
	}
	if strings.Contains(got, "USDT") {
		t.Fatalf("MarkdownV2 USDT 下发金额列不应显示 USDT，got=%s", got)
	}
}

func TestReplyAccountingDataCardWithStatusShowsZeroCardWhenNoRows(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "empty-ledger.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("关闭数据库失败: %v", closeErr)
		}
	}()

	type capturedSendMessage struct {
		replyTo   int
		parseMode string
		text      string
	}

	var (
		mu       sync.Mutex
		captured []capturedSendMessage
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(strings.TrimSpace(r.URL.Path), "/sendMessage") {
			if parseErr := r.ParseForm(); parseErr != nil {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"ok":false,"description":"invalid form"}`))
				return
			}
			replyTo, _ := strconv.Atoi(strings.TrimSpace(r.PostForm.Get("reply_to_message_id")))
			parseMode := strings.TrimSpace(r.PostForm.Get("parse_mode"))
			text := strings.TrimSpace(r.PostForm.Get("text"))
			mu.Lock()
			captured = append(captured, capturedSendMessage{
				replyTo:   replyTo,
				parseMode: parseMode,
				text:      text,
			})
			mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":88001}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer server.Close()

	api := &tgbotapi.BotAPI{
		Token:  "accounting-card-empty-token",
		Client: server.Client(),
	}
	api.SetAPIEndpoint(server.URL + "/bot%s/%s")

	app := &App{
		cfg: config.Config{
			Timezone: "Asia/Shanghai",
		},
		loc: time.FixedZone("CST", 8*3600),
		db:  database,
		api: api,
	}

	msg := &tgbotapi.Message{
		MessageID: 8801,
		Chat: &tgbotapi.Chat{
			ID:   -1008801,
			Type: "supergroup",
		},
	}
	group := db.GroupConfig{
		DayCutTime: "04:00:00",
		CustomRate: 7,
	}
	if err := app.replyAccountingDataCardWithStatus(msg, group, "", "ledger_card", false); err != nil {
		t.Fatalf("发送空账期卡片失败: %v", err)
	}

	mu.Lock()
	snapshot := append([]capturedSendMessage(nil), captured...)
	mu.Unlock()
	if len(snapshot) != 1 {
		t.Fatalf("发送回执数量异常: want=1 got=%d", len(snapshot))
	}
	if snapshot[0].replyTo != 8801 {
		t.Fatalf("回执 reply_to 异常: want=8801 got=%d", snapshot[0].replyTo)
	}
	if snapshot[0].parseMode != telegramParseModeMarkdownV {
		t.Fatalf("空账期卡片应使用 MarkdownV2，got=%s", snapshot[0].parseMode)
	}
	if strings.Contains(snapshot[0].text, "当前账期暂无数据") {
		t.Fatalf("空账期不应回退纯文本提示，got=%s", snapshot[0].text)
	}
	required := []string{
		"今日入款 \\(`0`\\) 笔:",
		"今日下发 \\(`0`\\) 笔:",
		"应下发: `0.00` U",
	}
	for _, fragment := range required {
		if !strings.Contains(snapshot[0].text, fragment) {
			t.Fatalf("空账期卡片缺少片段 %q，got=%s", fragment, snapshot[0].text)
		}
	}
}

func TestBuildAccountingDataCardMarkdownV2FromRowsHidesPayoutConfig(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	rows := []db.LedgerEntry{
		{
			EntryType: "deposit",
			Amount:    7000,
			Rate:      7,
			CreatedAt: "2026-02-21T00:01:09Z",
		},
	}

	got := buildAccountingDataCardMarkdownV2FromRows(rows, 0, 7, loc, ledgerCardDisplayConfig{
		PayoutFeeRate: 0,
		PayoutRate:    10,
	})

	idxDepositRate := strings.Index(got, "入款汇率: `7`")
	idxExpected := strings.Index(got, "应下发: `1000.00` U")
	if idxDepositRate == -1 || idxExpected == -1 {
		t.Fatalf("账单卡片缺少必要汇总字段，got=%s", got)
	}
	if strings.Contains(got, "下发费率") || strings.Contains(got, "下发汇率") {
		t.Fatalf("卡片账单不应展示下发配置字段，got=%s", got)
	}
	if !(idxDepositRate < idxExpected) {
		t.Fatalf("入款汇率应保持在应下发之前，got=%s", got)
	}
}

func TestBuildAccountingDataCardFromRowsHidesPayoutConfig(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	rows := []db.LedgerEntry{
		{
			EntryType: "deposit",
			Amount:    7000,
			Rate:      7,
			CreatedAt: "2026-02-21T00:01:09Z",
		},
	}

	got := buildAccountingDataCardFromRows(rows, 0, 7, loc, ledgerCardDisplayConfig{
		PayoutFeeRate: 0,
		PayoutRate:    10,
	})

	if !strings.Contains(got, "入款汇率: 7") || !strings.Contains(got, "应下发: 1000.00 U") {
		t.Fatalf("账单卡片缺少必要汇总字段，got=%s", got)
	}
	if strings.Contains(got, "下发费率") || strings.Contains(got, "下发汇率") {
		t.Fatalf("卡片账单不应展示下发配置字段，got=%s", got)
	}
}

func TestBuildAccountingDataCardMarkdownV2FromRowsReversalEntryRemarkHidden(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	rows := []db.LedgerEntry{
		{
			EntryType: "payout",
			Amount:    500,
			Currency:  "USDT",
			Rate:      7,
			Note:      "REV:44 原始备注",
			CreatedAt: "2026-02-21T00:01:09Z",
		},
	}
	got := buildAccountingDataCardMarkdownV2FromRows(rows, 0, 7, loc)
	if strings.Contains(got, "撤回") {
		t.Fatalf("MarkdownV2 反向撤回分录不应额外展示撤回标记，got=%s", got)
	}
	if strings.Contains(got, "REV:44") {
		t.Fatalf("MarkdownV2 不应展示原始REV标记，got=%s", got)
	}
	if strings.Contains(got, "`08:01:09`") {
		t.Fatalf("MarkdownV2 反向撤回分录整行不应展示，got=%s", got)
	}
	required := []string{
		"今日下发 \\(`0`\\) 笔:",
		"总下发: `0.00` U",
	}
	for _, fragment := range required {
		if !strings.Contains(got, fragment) {
			t.Fatalf("MarkdownV2 反向撤回分录应从账单统计中剔除，缺少 %q，got=%s", fragment, got)
		}
	}
}

func TestBuildAccountingDataCardMarkdownV2FromRowsOriginalEntryMarkedReversed(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	reversalID := int64(88)
	originID := int64(44)
	rows := []db.LedgerEntry{
		{
			ID:         originID,
			EntryType:  "deposit",
			Amount:     5000,
			Rate:       137,
			CreatedAt:  "2026-02-21T00:01:09Z",
			Reversed:   1,
			ReversalOf: &reversalID,
		},
		{
			ID:         reversalID,
			EntryType:  "deposit",
			Amount:     -5000,
			Rate:       137,
			Note:       "REV:44 原始备注",
			CreatedAt:  "2026-02-21T00:02:09Z",
			ReversalOf: &originID,
		},
	}
	got := buildAccountingDataCardMarkdownV2FromRows(rows, 0, 137, loc)
	if !strings.Contains(got, "今日入款 \\(`1`\\) 笔:") {
		t.Fatalf("MarkdownV2 已撤回原始分录仍应保留可见历史行，got=%s", got)
	}
	if !strings.Contains(got, "`08:01:09` 5000 \\* 1 / 137 \\= `36.50U`  \\| 已撤回") {
		t.Fatalf("MarkdownV2 原始分录应标记为已撤回，got=%s", got)
	}
	if strings.Contains(got, "`08:02:09` \\-5000 \\* 1 / 137 \\= `-36.50U`") {
		t.Fatalf("MarkdownV2 反向撤回分录整行不应展示，got=%s", got)
	}
	if strings.Contains(got, "REV:44") {
		t.Fatalf("MarkdownV2 反向撤回分录不应展示原始REV标记，got=%s", got)
	}
	required := []string{
		"当前入款: `0` \\| `0.00` U",
		"应下发: `0.00` U",
		"未下发: `0.00` U",
	}
	for _, fragment := range required {
		if !strings.Contains(got, fragment) {
			t.Fatalf("MarkdownV2 已撤回原始分录不应继续计入当前统计，缺少 %q，got=%s", fragment, got)
		}
	}
}

func TestBuildAccountingDataCardFromRowsApplyFeeRate(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	rows := []db.LedgerEntry{
		{
			EntryType: "deposit",
			Amount:    10000,
			Rate:      10,
			CreatedAt: "2026-02-21T00:01:09Z",
		},
		{
			EntryType: "payout",
			Amount:    1000,
			Rate:      10,
			CreatedAt: "2026-02-21T01:01:09Z",
		},
	}

	got := buildAccountingDataCardFromRows(rows, 10, 10, loc)
	required := []string{
		"08:01:09 10000 * 0.9 / 10 = 900.00U",
		"入款费率: 10%",
		"应下发: 900.00 U",
		"总下发: 100.00 U",
		"未下发: 800.00 U",
	}
	for _, fragment := range required {
		if !strings.Contains(got, fragment) {
			t.Fatalf("费率折算缺少片段 %q，got=%s", fragment, got)
		}
	}
}

func TestBuildAccountingDataCardMarkdownV2FromRowsApplyFeeRate(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	rows := []db.LedgerEntry{
		{
			EntryType: "deposit",
			Amount:    10000,
			Rate:      10,
			CreatedAt: "2026-02-21T00:01:09Z",
		},
		{
			EntryType: "payout",
			Amount:    1000,
			Rate:      10,
			CreatedAt: "2026-02-21T01:01:09Z",
		},
	}

	got := buildAccountingDataCardMarkdownV2FromRows(rows, 10, 10, loc)
	required := []string{
		"`08:01:09` 10000 \\* 0\\.9 / 10 \\= `900.00U`",
		"入款费率: `10`%",
		"应下发: `900.00` U",
		"总下发: `100.00` U",
		"未下发: `800.00` U",
	}
	for _, fragment := range required {
		if !strings.Contains(got, fragment) {
			t.Fatalf("MarkdownV2 费率折算缺少片段 %q，got=%s", fragment, got)
		}
	}
}

func TestBuildAccountingDataCardFromRowsPayoutUsesFinalAmountForUSettlement(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	rows := []db.LedgerEntry{
		{
			EntryType:   "payout",
			Amount:      10000,
			Rate:        10,
			FeeRate:     10,
			FinalAmount: 9000,
			CreatedAt:   "2026-02-21T01:01:09Z",
		},
	}

	got := buildAccountingDataCardFromRows(rows, 0, 10, loc, ledgerCardDisplayConfig{
		PayoutFeeRate: 10,
		PayoutRate:    10,
	})
	required := []string{
		"09:01:09 10000 [ 900.00 U]",
		"总下发: 900.00 U",
		"未下发: -900.00 U",
	}
	for _, fragment := range required {
		if !strings.Contains(got, fragment) {
			t.Fatalf("下发应按最终金额折算U，缺少片段 %q，got=%s", fragment, got)
		}
	}
}

func TestBuildAccountingDataCardMarkdownV2FromRowsPayoutUsesFinalAmountForUSettlement(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	rows := []db.LedgerEntry{
		{
			EntryType:   "payout",
			Amount:      10000,
			Rate:        10,
			FeeRate:     10,
			FinalAmount: 9000,
			CreatedAt:   "2026-02-21T01:01:09Z",
		},
	}

	got := buildAccountingDataCardMarkdownV2FromRows(rows, 0, 10, loc, ledgerCardDisplayConfig{
		PayoutFeeRate: 10,
		PayoutRate:    10,
	})
	required := []string{
		"`09:01:09` 10000 \\[` 900.00 U`\\]",
		"总下发: `900.00` U",
		"未下发: `-900.00` U",
	}
	for _, fragment := range required {
		if !strings.Contains(got, fragment) {
			t.Fatalf("MarkdownV2 下发应按最终金额折算U，缺少片段 %q，got=%s", fragment, got)
		}
	}
}

func TestBuildAccountingDataCardFromRowsUSDTNoRateDivision(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	rows := []db.LedgerEntry{
		{
			EntryType: "deposit",
			Amount:    1000,
			Currency:  "USDT",
			Rate:      7,
			CreatedAt: "2026-02-21T00:01:09Z",
		},
	}

	got := buildAccountingDataCardFromRows(rows, 0, 7, loc)
	if strings.Contains(got, "/ 7 = 1000.00U") {
		t.Fatalf("USDT 明细不应显示除汇率表达式，got=%s", got)
	}
	if !strings.Contains(got, "08:01:09 1000 * 1 = 1000.00U") {
		t.Fatalf("USDT 明细应直接展示等值换算，got=%s", got)
	}
}

func TestBuildAccountingDataCardMarkdownV2FromRowsUSDTNoRateDivision(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	rows := []db.LedgerEntry{
		{
			EntryType: "deposit",
			Amount:    1000,
			Currency:  "USDT",
			Rate:      7,
			CreatedAt: "2026-02-21T00:01:09Z",
		},
	}

	got := buildAccountingDataCardMarkdownV2FromRows(rows, 0, 7, loc)
	if strings.Contains(got, "/ `7` \\= `1000.00`U") {
		t.Fatalf("MarkdownV2 USDT 明细不应显示除汇率表达式，got=%s", got)
	}
	if !strings.Contains(got, "`08:01:09` 1000 \\* 1 \\= `1000.00U`") {
		t.Fatalf("MarkdownV2 USDT 明细应直接展示等值换算，got=%s", got)
	}
}

func TestBuildAccountingDataCardFromRowsDepositTrackLink(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	rows := []db.LedgerEntry{
		{
			EntryType:   "deposit",
			Amount:      1000,
			Rate:        10,
			FeeRate:     5,
			TrackLink:   "https://t.me/c/123/456",
			Username:    "alice",
			Nickname:    "爱丽丝",
			CreatedAt:   "2026-02-21T00:01:09Z",
			Currency:    "CNY",
			FinalAmount: 0,
		},
	}
	got := buildAccountingDataCardFromRows(rows, 5, 10, loc)
	required := []string{
		"08:01:09 1000 * 0.95 / 10 = 95.00U (https://t.me/c/123/456)  |  爱丽丝",
		"当前入款: 1000 | 95.00 U",
		"应下发: 95.00 U",
	}
	for _, fragment := range required {
		if !strings.Contains(got, fragment) {
			t.Fatalf("带跟踪链接卡片缺少片段 %q，got=%s", fragment, got)
		}
	}
}

func TestBuildAccountingDataCardMarkdownV2FromRowsDepositTrackLink(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	rows := []db.LedgerEntry{
		{
			EntryType: "deposit",
			Amount:    1000,
			Rate:      10,
			FeeRate:   5,
			TrackLink: "https://t.me/c/123/456",
			Username:  "alice",
			Nickname:  "爱丽丝",
			CreatedAt: "2026-02-21T00:01:09Z",
			Currency:  "CNY",
		},
	}
	got := buildAccountingDataCardMarkdownV2FromRows(rows, 5, 10, loc)
	required := []string{
		"`08:01:09` [1000](https://t.me/c/123/456) \\* 0\\.95 / 10 \\= `95.00U`  \\|  [爱丽丝](https://t.me/c/123/456)",
		"当前入款: `1000` \\| `95.00` U",
		"应下发: `95.00` U",
	}
	for _, fragment := range required {
		if !strings.Contains(got, fragment) {
			t.Fatalf("MarkdownV2 带跟踪链接卡片缺少片段 %q，got=%s", fragment, got)
		}
	}
	if strings.Contains(got, "🔗原消息") {
		t.Fatalf("MarkdownV2 不应追加重复的原消息文案，got=%s", got)
	}
}

func TestBuildAccountingDataCardFromRowsDepositTrackLinkFallbackByMessageID(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	rows := []db.LedgerEntry{
		{
			ChatID:      -100123,
			MessageID:   456,
			EntryType:   "deposit",
			Amount:      1000,
			Rate:        10,
			FeeRate:     5,
			Nickname:    "爱丽丝",
			CreatedAt:   "2026-02-21T00:01:09Z",
			Currency:    "CNY",
			TrackLink:   "",
			FinalAmount: 0,
		},
	}
	got := buildAccountingDataCardFromRows(rows, 5, 10, loc)
	if !strings.Contains(got, "08:01:09 1000 * 0.95 / 10 = 95.00U (https://t.me/c/123/456)  |  爱丽丝") {
		t.Fatalf("缺少回填生成的跟踪链接，got=%s", got)
	}
}

func TestBuildAccountingDataCardMarkdownV2FromRowsDepositTrackLinkFallbackByMessageID(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	rows := []db.LedgerEntry{
		{
			ChatID:      -100123,
			MessageID:   456,
			EntryType:   "deposit",
			Amount:      1000,
			Rate:        10,
			FeeRate:     5,
			Nickname:    "爱丽丝",
			CreatedAt:   "2026-02-21T00:01:09Z",
			Currency:    "CNY",
			TrackLink:   "",
			FinalAmount: 0,
		},
	}
	got := buildAccountingDataCardMarkdownV2FromRows(rows, 5, 10, loc)
	if !strings.Contains(got, "`08:01:09` [1000](https://t.me/c/123/456) \\* 0\\.95 / 10 \\= `95.00U`  \\|  [爱丽丝](https://t.me/c/123/456)") {
		t.Fatalf("MarkdownV2 缺少回填生成的跟踪链接，got=%s", got)
	}
	if strings.Contains(got, "🔗原消息") {
		t.Fatalf("MarkdownV2 不应追加重复的原消息文案，got=%s", got)
	}
}

func TestBuildAccountingDataCardMarkdownV2FromRowsDepositTrackLinkFallbackBySender(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	rows := []db.LedgerEntry{
		{
			ChatID:    -5266232385,
			MessageID: 375,
			UserID:    8114441184,
			EntryType: "deposit",
			Amount:    100,
			Rate:      7,
			FeeRate:   0,
			Nickname:  "Nova_PAY_CS Rep",
			CreatedAt: "2026-02-25T08:22:39Z",
			Currency:  "CNY",
			TrackLink: "",
		},
	}
	got := buildAccountingDataCardMarkdownV2FromRows(rows, 0, 7, loc)
	if !strings.Contains(got, "`16:22:39` 100 \\* 1 / 7 \\= `14.29U`  \\|  Nova\\_PAY\\_CS Rep") {
		t.Fatalf("非超级群消息不应生成不可访问链接，got=%s", got)
	}
	if strings.Contains(got, "https://t.me/c/5266232385/375") {
		t.Fatalf("非超级群消息不应生成 t.me/c 跟踪链接，got=%s", got)
	}
}

func TestBuildAccountingDataCardMarkdownV2FromRowsIgnoreLegacyTrackLinkForNonSupergroup(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	rows := []db.LedgerEntry{
		{
			ChatID:    -5266232385,
			MessageID: 391,
			EntryType: "deposit",
			Amount:    100,
			Rate:      7,
			FeeRate:   0,
			Nickname:  "Nova_PAY_CS Rep",
			CreatedAt: "2026-02-25T08:22:39Z",
			Currency:  "CNY",
			TrackLink: "https://t.me/c/5266232385/391",
		},
	}
	got := buildAccountingDataCardMarkdownV2FromRows(rows, 0, 7, loc)
	if strings.Contains(got, "https://t.me/c/5266232385/391") {
		t.Fatalf("历史遗留的不可访问链接应被过滤，got=%s", got)
	}
}

func TestBuildAccountingDataCardFromRowsPayoutActorShown(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	rows := []db.LedgerEntry{
		{
			EntryType:   "payout",
			Amount:      700,
			Rate:        7,
			Username:    "bob",
			Nickname:    "鲍勃",
			CreatedAt:   "2026-02-21T00:01:09Z",
			Currency:    "CNY",
			FinalAmount: 0,
		},
	}
	got := buildAccountingDataCardFromRows(rows, 0, 7, loc)
	if !strings.Contains(got, "08:01:09 700 [100.00 U]  |  鲍勃") {
		t.Fatalf("下发行应显示操作人昵称，got=%s", got)
	}
}

func TestBuildAccountingDataCardMarkdownV2FromRowsPayoutActorShown(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	rows := []db.LedgerEntry{
		{
			EntryType: "payout",
			Amount:    700,
			Rate:      7,
			Username:  "bob",
			Nickname:  "鲍勃",
			CreatedAt: "2026-02-21T00:01:09Z",
			Currency:  "CNY",
		},
	}
	got := buildAccountingDataCardMarkdownV2FromRows(rows, 0, 7, loc)
	if !strings.Contains(got, "`08:01:09` 700 \\[`100.00 U`\\]  \\|  鲍勃") {
		t.Fatalf("MarkdownV2 下发行应显示操作人昵称，got=%s", got)
	}
}

func TestBuildAccountingDataCardMarkdownV2FromRowsPayoutAmountTrackLink(t *testing.T) {
	loc := time.FixedZone("ICT", 7*3600)
	rows := []db.LedgerEntry{
		{
			EntryType: "payout",
			Amount:    100,
			Rate:      10,
			Nickname:  "IN Clearing Hub2",
			CreatedAt: "2026-07-14T12:31:48Z",
			Currency:  "CNY",
			TrackLink: "https://t.me/c/4455839808/494",
		},
	}

	got := buildAccountingDataCardMarkdownV2FromRows(rows, 0, 10, loc)
	want := "`19:31:48` [100](https://t.me/c/4455839808/494) \\[`10.00 U`\\]  \\|  [IN Clearing Hub2](https://t.me/c/4455839808/494)"
	if !strings.Contains(got, want) {
		t.Fatalf("MarkdownV2 下发金额和操作人应链接到原消息，want=%q got=%s", want, got)
	}
}

func TestBuildAccountingDataCardMarkdownV2FromRowsPayoutTrackLinkFallbackByMessageID(t *testing.T) {
	loc := time.FixedZone("ICT", 7*3600)
	rows := []db.LedgerEntry{
		{
			ChatID:    -1004455839808,
			MessageID: 494,
			EntryType: "payout",
			Amount:    100,
			Rate:      10,
			Nickname:  "IN Clearing Hub2",
			CreatedAt: "2026-07-14T12:31:48Z",
			Currency:  "CNY",
		},
	}

	got := buildAccountingDataCardMarkdownV2FromRows(rows, 0, 10, loc)
	want := "`19:31:48` [100](https://t.me/c/4455839808/494) \\[`10.00 U`\\]  \\|  [IN Clearing Hub2](https://t.me/c/4455839808/494)"
	if !strings.Contains(got, want) {
		t.Fatalf("MarkdownV2 下发金额和操作人应回填原消息链接，want=%q got=%s", want, got)
	}
}

func TestBuildAccountingDataCardMarkdownV2FromRowsPayoutIgnoresInaccessibleTrackLink(t *testing.T) {
	loc := time.FixedZone("ICT", 7*3600)
	rows := []db.LedgerEntry{
		{
			ChatID:    -5266232385,
			MessageID: 494,
			EntryType: "payout",
			Amount:    100,
			Rate:      10,
			Nickname:  "IN Clearing Hub2",
			CreatedAt: "2026-07-14T12:31:48Z",
			Currency:  "CNY",
			TrackLink: "https://t.me/c/4455839808/494",
		},
	}

	got := buildAccountingDataCardMarkdownV2FromRows(rows, 0, 10, loc)
	if strings.Contains(got, "https://t.me/c/4455839808/494") {
		t.Fatalf("MarkdownV2 下发行不应使用不可访问链接，got=%s", got)
	}
	want := "`19:31:48` 100 \\[`10.00 U`\\]  \\|  IN Clearing Hub2"
	if !strings.Contains(got, want) {
		t.Fatalf("MarkdownV2 下发行不可访问链接应回退普通文本，want=%q got=%s", want, got)
	}
}

func TestBuildAccountingDataCardMarkdownV2FromRowsDisplayTotalsFollowTwoDecimalRoundedLines(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	rows := []db.LedgerEntry{
		{
			EntryType: "deposit",
			Amount:    5000,
			Rate:      137,
			Nickname:  "IN Clearing Hub2",
			CreatedAt: "2026-07-08T09:25:03Z",
			Currency:  "CNY",
		},
		{
			EntryType: "deposit",
			Amount:    3000,
			Rate:      137,
			Nickname:  "IN Clearing Hub2",
			CreatedAt: "2026-07-08T09:39:47Z",
			Currency:  "CNY",
		},
	}

	got := buildAccountingDataCardMarkdownV2FromRows(rows, 0, 137, loc)
	required := []string{
		"`17:25:03` 5000 \\* 1 / 137 \\= `36.50U`  \\|  IN Clearing Hub2",
		"`17:39:47` 3000 \\* 1 / 137 \\= `21.90U`  \\|  IN Clearing Hub2",
		"当前入款: `8000` \\| `58.40` U",
		"应下发: `58.40` U",
	}
	for _, fragment := range required {
		if !strings.Contains(got, fragment) {
			t.Fatalf("两位小数展示口径缺少片段 %q，got=%s", fragment, got)
		}
	}
}
