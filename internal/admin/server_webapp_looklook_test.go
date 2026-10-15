package admin

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go-bot/internal/db"
)

func TestBuildTGWebAppLooklookViewAggregatesByRange(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	beginAt := time.Date(2026, 2, 21, 8, 0, 0, 0, loc)
	endAt := time.Date(2026, 2, 21, 12, 0, 0, 0, loc)

	rows := []db.LedgerEntry{
		{
			UserID:    9001,
			EntryType: "deposit",
			Amount:    1000,
			Currency:  "CNY",
			Rate:      10,
			FeeRate:   5,
			Nickname:  "Nova",
			Username:  "nova_op",
			TrackLink: "https://t.me/c/1/100",
			CreatedAt: "2026-02-21T01:00:00Z",
		},
		{
			UserID:    9001,
			EntryType: "payout",
			Amount:    200,
			Currency:  "USDT",
			Rate:      7,
			Nickname:  "Nova",
			Username:  "nova_op",
			CreatedAt: "2026-02-21T02:30:00Z",
		},
		{
			EntryType: "deposit",
			Amount:    500,
			Currency:  "CNY",
			Rate:      10,
			FeeRate:   5,
			Nickname:  "Other",
			Username:  "other_op",
			CreatedAt: "2026-02-20T01:00:00Z",
		},
	}
	group := db.GroupConfig{
		ChatID:     -100123,
		Title:      "looklook测试群",
		FeeRate:    5,
		CustomRate: 10,
	}

	view := buildTGWebAppLooklookView(rows, group, loc, beginAt, endAt, "bjr", "", beginAt.Format("2006-01-02 15:04:05")+" - "+endAt.Format("2006-01-02 15:04:05"))
	if len(view.Rows) != 2 {
		t.Fatalf("时间窗口过滤异常: len=%d", len(view.Rows))
	}
	if view.TotalDepositAmount != 1000 {
		t.Fatalf("总入款金额异常: %f", view.TotalDepositAmount)
	}
	if view.TotalPayoutAmount != 200 {
		t.Fatalf("总下发金额异常: %f", view.TotalPayoutAmount)
	}
	if view.TotalDepositU != 95 {
		t.Fatalf("总入款U异常: %f", view.TotalDepositU)
	}
	if view.TotalPayoutU != 200 {
		t.Fatalf("总下发U异常: %f", view.TotalPayoutU)
	}
	if len(view.MarkerStats) == 0 || view.MarkerStats[0].Name != "Nova" {
		t.Fatalf("标记人统计异常: %+v", view.MarkerStats)
	}
	if len(view.OperatorStats) == 0 || view.OperatorStats[0].Name != "nova_op" {
		t.Fatalf("操作人统计异常: %+v", view.OperatorStats)
	}
	if len(view.Rows) == 0 || view.Rows[0].OperatorUsername != "nova_op" {
		t.Fatalf("操作人用户名映射异常: %+v", view.Rows)
	}
	if len(view.OperatorStats) == 0 || view.OperatorStats[0].Username != "nova_op" {
		t.Fatalf("操作人统计用户名映射异常: %+v", view.OperatorStats)
	}
}

func TestRenderTGWebAppLooklookHTMLContainsServerSideData(t *testing.T) {
	view := tgWebAppLooklookView{
		Mode:               "bjr",
		QueryMode:          "day",
		GroupID:            -100123,
		GroupTitle:         "looklook测试群",
		BillDateText:       "2026-02-21",
		BeginTimeText:      "2026-02-21 08:00:00",
		EndTimeText:        "2026-02-21 12:00:00",
		BeginTimeISO:       "2026-02-21T08:00:00",
		EndTimeISO:         "2026-02-21T12:00:00",
		DateRangeText:      "2026-02-21 08:00:00 - 2026-02-21 12:00:00",
		TotalDepositAmount: 1000,
		TotalPayoutAmount:  200,
		TotalDepositU:      95,
		TotalPayoutU:       200,
		Rows: []tgWebAppLooklookRow{
			{
				TimeText:         "2026-02-21 09:00:00",
				EntryType:        "deposit",
				IsReversal:       true,
				Amount:           1000,
				FeeFactor:        0.95,
				Rate:             10,
				ConvertedU:       95,
				MarkerName:       "Nova",
				MarkerUserID:     9001,
				MarkerUsername:   "nova_op",
				OperatorName:     "Nova",
				OperatorUserID:   9001,
				OperatorUsername: "nova_op",
				Remarks:          "撤回",
				Track:            "https://t.me/c/1/100",
			},
		},
		MarkerStats:   []tgWebAppLooklookPartyStat{{Name: "Nova", UserID: 9001, Username: "nova_op", Count: 1, DepositAmount: 1000, DepositU: 95}},
		OperatorStats: []tgWebAppLooklookPartyStat{{Name: "Nova", UserID: 9001, Username: "nova_op", Count: 1, DepositAmount: 1000, DepositU: 95}},
		DailyData:     []tgWebAppLooklookDailyStat{{Day: "2026-02-21", DepositU: 95, PayoutU: 200}},
		RateStats:     []tgWebAppLooklookRateStat{{Rate: "10", Count: 1}},
		UserStats:     []tgWebAppLooklookUserStat{{Name: "nova_op", Count: 1, DepositU: 95, PayoutU: 200, NetU: -105}},
	}
	html := renderTGWebAppLooklookHTML(
		view,
		1001,
		2002,
		"sig-demo",
		"/tg/webapp/looklook.php?export=csv",
		"/tg/webapp/looklook.php?export=excel",
		"/tg/webapp/looklook.php?export=pdf",
	)
	required := []string{
		"账单详情",
		`name="bill_date"`,
		`name="query_mode"`,
		`name="begintime"`,
		`name="endtime"`,
		`type="date"`,
		`type="datetime-local"`,
		"单日查询",
		"时间区间查询",
		"账单明细",
		"统计（按标记人）",
		"统计（按操作人）",
		"总入账金额",
		"const dailyData =",
		"const exchangeRateStats =",
		"const userStats =",
		"/tg/webapp/looklook.php?export=csv",
		"/tg/webapp/looklook.php?export=excel",
		"/tg/webapp/looklook.php?export=pdf",
		`href="https://t.me/nova_op"`,
		"@nova_op",
		"撤回",
	}
	for _, want := range required {
		if !strings.Contains(html, want) {
			t.Fatalf("HTML 缺少片段 %q", want)
		}
	}
}

func TestParseTGWebAppLooklookDate(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	parsed, err := parseTGWebAppLooklookDate("2026-02-21", loc)
	if err != nil {
		t.Fatalf("解析日期失败: %v", err)
	}
	if parsed.Year() != 2026 || parsed.Month() != time.February || parsed.Day() != 21 {
		t.Fatalf("解析日期异常: %v", parsed)
	}
	if parsed.Hour() != 0 || parsed.Minute() != 0 || parsed.Second() != 0 {
		t.Fatalf("解析日期时分秒应为零点: %v", parsed)
	}
	if _, err := parseTGWebAppLooklookDate("2026/02/21", loc); err == nil {
		t.Fatalf("非法日期格式应返回错误")
	}
}

func TestNormalizeTGWebAppLedgerRemark(t *testing.T) {
	if got := normalizeTGWebAppLedgerRemark("REV:72"); got != "撤回" {
		t.Fatalf("撤回备注归一化异常: %q", got)
	}
	if got := normalizeTGWebAppLedgerRemark("REV:72 原始备注"); got != "撤回 原始备注" {
		t.Fatalf("撤回备注保留尾部异常: %q", got)
	}
	if got := normalizeTGWebAppLedgerRemark("普通备注"); got != "普通备注" {
		t.Fatalf("普通备注不应变化: %q", got)
	}
}

func TestNormalizeTGWebAppLooklookQueryMode(t *testing.T) {
	if got := normalizeTGWebAppLooklookQueryMode("day"); got != "day" {
		t.Fatalf("day 模式解析异常: %q", got)
	}
	if got := normalizeTGWebAppLooklookQueryMode("range"); got != "range" {
		t.Fatalf("range 模式解析异常: %q", got)
	}
	if got := normalizeTGWebAppLooklookQueryMode("unknown"); got != "" {
		t.Fatalf("未知模式应返回空: %q", got)
	}
}

func TestParseTGWebAppLooklookTimeDateTimeLocal(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	parsed, err := parseTGWebAppLooklookTime("2026-02-21T08:30", loc)
	if err != nil {
		t.Fatalf("datetime-local 时间解析失败: %v", err)
	}
	if parsed.Year() != 2026 || parsed.Month() != time.February || parsed.Day() != 21 {
		t.Fatalf("解析日期异常: %v", parsed)
	}
	if parsed.Hour() != 8 || parsed.Minute() != 30 || parsed.Second() != 0 {
		t.Fatalf("解析时间异常: %v", parsed)
	}
}

func TestResolveTGWebAppLooklookMode(t *testing.T) {
	mode, ok := resolveTGWebAppLooklookMode("bjr", false)
	if !ok || mode != "bjr" {
		t.Fatalf("bjr 模式判定异常: mode=%s ok=%v", mode, ok)
	}

	mode, ok = resolveTGWebAppLooklookMode("czr", true)
	if !ok || mode != "czr" {
		t.Fatalf("czr 模式授权判定异常: mode=%s ok=%v", mode, ok)
	}

	mode, ok = resolveTGWebAppLooklookMode("czr", false)
	if ok || mode != "czr" {
		t.Fatalf("czr 模式越权应拒绝: mode=%s ok=%v", mode, ok)
	}

	mode, ok = resolveTGWebAppLooklookMode("unknown", false)
	if !ok || mode != "bjr" {
		t.Fatalf("未知模式应回落到 bjr: mode=%s ok=%v", mode, ok)
	}
}

func TestRenderTGWebAppLooklookModeField(t *testing.T) {
	limited := renderTGWebAppLooklookModeField(tgWebAppLooklookView{
		Mode:     "czr",
		AllowCZR: false,
	})
	if strings.Contains(limited, `value="czr"`) {
		t.Fatalf("无 czr 权限时不应渲染 czr 选项: %s", limited)
	}
	if !strings.Contains(limited, `type="hidden" name="type" value="bjr"`) {
		t.Fatalf("无 czr 权限时应固定提交 bjr: %s", limited)
	}

	enabled := renderTGWebAppLooklookModeField(tgWebAppLooklookView{
		Mode:     "czr",
		AllowCZR: true,
	})
	if !strings.Contains(enabled, `<option value="czr" selected>czr</option>`) {
		t.Fatalf("有 czr 权限时应渲染并选中 czr: %s", enabled)
	}
}

func TestWriteTGWebAppLooklookExcelAndPDF(t *testing.T) {
	view := tgWebAppLooklookView{
		Mode:               "bjr",
		GroupID:            -100123,
		GroupTitle:         "looklook测试群",
		DateRangeText:      "2026-02-21 08:00:00 - 2026-02-21 12:00:00",
		TotalDepositAmount: 1000,
		TotalPayoutAmount:  200,
		TotalDepositU:      95,
		TotalPayoutU:       200,
		Rows: []tgWebAppLooklookRow{
			{
				TimeText:     "2026-02-21 09:00:00",
				EntryType:    "deposit",
				Amount:       1000,
				Currency:     "CNY",
				FeeFactor:    0.95,
				Rate:         10,
				ConvertedU:   95,
				MarkerName:   "Nova",
				OperatorName: "nova_op",
				Track:        "https://t.me/c/1/100",
			},
		},
	}

	xlsxRecorder := httptest.NewRecorder()
	if err := writeTGWebAppLooklookExcel(xlsxRecorder, view); err != nil {
		t.Fatalf("导出 Excel 失败: %v", err)
	}
	if got := strings.TrimSpace(xlsxRecorder.Header().Get("Content-Type")); !strings.Contains(got, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet") {
		t.Fatalf("Excel Content-Type 异常: %q", got)
	}
	if got := strings.TrimSpace(xlsxRecorder.Header().Get("Content-Disposition")); !strings.Contains(got, ".xlsx") {
		t.Fatalf("Excel 文件名异常: %q", got)
	}
	xlsxBody := xlsxRecorder.Body.Bytes()
	if len(xlsxBody) < 4 || string(xlsxBody[:2]) != "PK" {
		t.Fatalf("Excel 内容不是有效的 xlsx(zip) 结构")
	}

	pdfRecorder := httptest.NewRecorder()
	if err := writeTGWebAppLooklookPDF(pdfRecorder, view); err != nil {
		t.Fatalf("导出 PDF 失败: %v", err)
	}
	if got := strings.TrimSpace(pdfRecorder.Header().Get("Content-Type")); !strings.Contains(got, "application/pdf") {
		t.Fatalf("PDF Content-Type 异常: %q", got)
	}
	if got := strings.TrimSpace(pdfRecorder.Header().Get("Content-Disposition")); !strings.Contains(got, ".pdf") {
		t.Fatalf("PDF 文件名异常: %q", got)
	}
	if !strings.HasPrefix(pdfRecorder.Body.String(), "%PDF-1.4") {
		t.Fatalf("PDF 内容头异常")
	}
}
