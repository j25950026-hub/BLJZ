package bot

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/db"
	"go-bot/internal/services"
)

type roundTripFuncBot func(req *http.Request) (*http.Response, error)

func (f roundTripFuncBot) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type stubMigrationExchange struct {
	getFn     func(provider, payType, fiat string) (*services.RateResult, error)
	compareFn func(payType, fiat string) ([]services.RateResult, error)
}

func (s *stubMigrationExchange) GetRate(provider, payType, fiat string) (*services.RateResult, error) {
	if s.getFn != nil {
		return s.getFn(provider, payType, fiat)
	}
	return &services.RateResult{
		Provider: provider,
		PayType:  payType,
		Price:    7.2,
		Top10:    []float64{7.2, 7.21, 7.22},
	}, nil
}

func (s *stubMigrationExchange) CompareRates(payType, fiat string) ([]services.RateResult, error) {
	if s.compareFn != nil {
		return s.compareFn(payType, fiat)
	}
	return []services.RateResult{
		{Provider: "okx", Price: 7.2},
		{Provider: "huobi", Price: 7.21},
		{Provider: "binance", Price: 7.19},
	}, nil
}

func TestDistanceCommandKeepsAliasAndOutput(t *testing.T) {
	app, _, capture := newFourLayerTestApp(t, "distance-compat.db")

	mux := http.NewServeMux()
	mux.HandleFunc("/geocode", func(w http.ResponseWriter, r *http.Request) {
		query := strings.TrimSpace(r.URL.Query().Get("q"))
		switch query {
		case "北京":
			_, _ = w.Write([]byte(`[{"lat":"39.9042","lon":"116.4074","display_name":"北京"}]`))
		case "上海":
			_, _ = w.Write([]byte(`[{"lat":"31.2304","lon":"121.4737","display_name":"上海"}]`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	})
	mux.HandleFunc("/route", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"routes":[{"distance":1213000.0,"duration":50640.0}]}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	app.geo = services.NewGeoServiceWithConfig(services.GeoConfig{
		GeocodeEndpoint: ts.URL + "/geocode",
		RouteEndpoint:   ts.URL + "/route",
		Timeout:         2 * time.Second,
		RetryMax:        1,
		Backoff:         5 * time.Millisecond,
	})

	msg1 := &tgbotapi.Message{MessageID: 9001, Date: int(time.Now().Unix()), Chat: &tgbotapi.Chat{ID: -100600001, Type: "supergroup"}, From: &tgbotapi.User{ID: 600001}}
	if err := app.handleDistanceQuery(msg1, "距离 北京到上海"); err != nil {
		t.Fatalf("handleDistanceQuery failed: %v", err)
	}
	if !fourLayerHasReply(capture.snapshot(), msg1.Chat.ID, msg1.MessageID, "路线距离：1213.0 公里") {
		t.Fatalf("distance response should contain route km")
	}
	if !fourLayerHasReply(capture.snapshot(), msg1.Chat.ID, msg1.MessageID, "预计时长：14小时4分钟") {
		t.Fatalf("distance response should contain duration")
	}

	msg2 := &tgbotapi.Message{MessageID: 9002, Date: int(time.Now().Unix()), Chat: &tgbotapi.Chat{ID: -100600001, Type: "supergroup"}, From: &tgbotapi.User{ID: 600001}}
	if err := app.handleDistanceQuery(msg2, "jl 北京到上海"); err != nil {
		t.Fatalf("handleDistanceQuery alias failed: %v", err)
	}
	if !fourLayerHasReply(capture.snapshot(), msg2.Chat.ID, msg2.MessageID, "路线距离：1213.0 公里") {
		t.Fatalf("distance alias response should contain route km")
	}
}

func TestWorldTimeJapanReplyIncludesWeekday(t *testing.T) {
	app, _, capture := newFourLayerTestApp(t, "worldtime-weekday.db")
	app.worldTime = services.NewWorldTimeService()

	msg := &tgbotapi.Message{
		MessageID: 990101,
		Chat: &tgbotapi.Chat{
			ID:   -100990101,
			Type: "supergroup",
		},
		From: &tgbotapi.User{
			ID:       990101,
			UserName: "weekday_tester",
		},
	}

	if err := app.handleWorldTime(msg, "日本"); err != nil {
		t.Fatalf("处理日本时间失败: %v", err)
	}

	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, msg.Chat.ID, msg.MessageID, "日本时间：") {
		t.Fatalf("日本时间回复缺失: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, msg.Chat.ID, msg.MessageID, "时区：日本/东京") {
		t.Fatalf("日本时间回复应显示中文时区: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, msg.Chat.ID, msg.MessageID, "星期：") {
		t.Fatalf("日本时间回复应显示星期几: %+v", snapshot)
	}
}

func TestWorldTimeIANAReplyUsesChineseTimezoneLabel(t *testing.T) {
	app, _, capture := newFourLayerTestApp(t, "worldtime-iana-chinese-label.db")
	app.worldTime = services.NewWorldTimeService()

	msg := &tgbotapi.Message{
		MessageID: 990102,
		Chat: &tgbotapi.Chat{
			ID:   -100990102,
			Type: "supergroup",
		},
		From: &tgbotapi.User{
			ID:       990102,
			UserName: "timezone_label_tester",
		},
	}

	if err := app.handleWorldTime(msg, "America/Chicago"); err != nil {
		t.Fatalf("处理美国中部时间失败: %v", err)
	}

	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, msg.Chat.ID, msg.MessageID, "America/Chicago时间：") {
		t.Fatalf("美国中部 IANA 时间回复缺失: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, msg.Chat.ID, msg.MessageID, "时区：美国/芝加哥") {
		t.Fatalf("美国中部 IANA 回复应显示中文时区: %+v", snapshot)
	}
	if fourLayerHasReply(snapshot, msg.Chat.ID, msg.MessageID, "时区：America/Chicago") {
		t.Fatalf("美国中部 IANA 回复不应直接显示英文时区码: %+v", snapshot)
	}
}

func TestRateCommandKeepsExistingCommands(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "rate-compat.db")
	app.exchange = &stubMigrationExchange{}

	const (
		chatID  int64 = -100600002
		ownerID int64 = 600002
	)
	if _, err := database.EnsureGroup(chatID, "汇率兼容测试群"); err != nil {
		t.Fatalf("EnsureGroup failed: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("SetOwner failed: %v", err)
	}
	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("GetGroup failed: %v", err)
	}

	queryCommands := []string{"z0", "lk", "lz", "lw", "汇率对比"}
	for i, cmd := range queryCommands {
		msg := &tgbotapi.Message{
			MessageID: 9100 + i,
			Date:      int(time.Now().Unix()),
			Text:      cmd,
			Chat:      &tgbotapi.Chat{ID: chatID, Type: "supergroup"},
			From:      &tgbotapi.User{ID: ownerID, UserName: "owner"},
		}
		if err := app.handleRateQuery(msg, group, cmd); err != nil {
			t.Fatalf("handleRateQuery failed for %s: %v", cmd, err)
		}
		if !fourLayerHasReply(capture.snapshot(), chatID, msg.MessageID, "实时汇率") &&
			!fourLayerHasReply(capture.snapshot(), chatID, msg.MessageID, "汇率对比") {
			t.Fatalf("rate reply missing for %s", cmd)
		}
	}

	setCases := []struct {
		cmd    string
		source string
	}{
		{cmd: "设置欧易交易所", source: "okx"},
		{cmd: "设置火币交易所", source: "huobi"},
		{cmd: "设置币安交易所", source: "binance"},
	}
	for i, item := range setCases {
		msg := &tgbotapi.Message{
			MessageID: 9200 + i,
			Date:      int(time.Now().Unix()),
			Text:      item.cmd,
			Chat:      &tgbotapi.Chat{ID: chatID, Type: "supergroup"},
			From:      &tgbotapi.User{ID: ownerID, UserName: "owner"},
		}
		if err := app.handleRateQuery(msg, group, item.cmd); err != nil {
			t.Fatalf("handleRateQuery set source failed for %s: %v", item.cmd, err)
		}
		updated, err := database.GetGroup(chatID)
		if err != nil {
			t.Fatalf("GetGroup after set failed: %v", err)
		}
		if updated.RateSource != item.source {
			t.Fatalf("rate source mismatch for %s: want=%s got=%s", item.cmd, item.source, updated.RateSource)
		}
	}
}

func TestRateQueryShowsMerchantTop10Details(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "rate-top10-merchant.db")
	app.exchange = &stubMigrationExchange{
		getFn: func(provider, payType, fiat string) (*services.RateResult, error) {
			if fiat != "INR" {
				t.Fatalf("rate query fiat mismatch: want=INR got=%s", fiat)
			}
			return &services.RateResult{
				Provider: provider,
				PayType:  payType,
				Price:    103.86,
				Top10:    []float64{103.82, 103.84},
				Top10Quotes: []services.RateQuote{
					{Merchant: "Srivenkateswara", Price: 103.82},
					{Merchant: "EXPRESS-P2P-TRADER", Price: 103.84},
				},
			}, nil
		},
	}

	const (
		chatID  int64 = -100600022
		ownerID int64 = 600022
	)
	if _, err := database.EnsureGroup(chatID, "汇率明细测试群"); err != nil {
		t.Fatalf("EnsureGroup failed: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("SetOwner failed: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{"rate_source": "binance"}); err != nil {
		t.Fatalf("UpdateGroupFields failed: %v", err)
	}
	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("GetGroup failed: %v", err)
	}

	msg := &tgbotapi.Message{
		MessageID: 9122,
		Date:      int(time.Now().Unix()),
		Text:      "Z0 印度卢比",
		Chat:      &tgbotapi.Chat{ID: chatID, Type: "supergroup"},
		From:      &tgbotapi.User{ID: ownerID, UserName: "owner"},
	}
	if err := app.handleRateQuery(msg, group, msg.Text); err != nil {
		t.Fatalf("handleRateQuery failed: %v", err)
	}

	reply, ok := fourLayerFindReply(capture.snapshot(), chatID, msg.MessageID, "今日 Binance 商家实时交易汇率 Top10:")
	if !ok {
		t.Fatalf("rate reply should include top10 merchant detail header: %+v", capture.snapshot())
	}
	if reply.parseMode != telegramParseModeHTML {
		t.Fatalf("rate reply parse mode mismatch: want=%q got=%q", telegramParseModeHTML, reply.parseMode)
	}
	if !strings.Contains(reply.text, "BINANCE 实时汇率") {
		t.Fatalf("rate reply should render exchange header: %+v", reply)
	}
	if !strings.Contains(reply.text, "法币市场：印度卢比 (INR)") {
		t.Fatalf("rate reply should render fiat market label: %+v", reply)
	}
	if !strings.Contains(reply.text, "Top10均价：103.86 INR/USDT") {
		t.Fatalf("rate reply should render top10 average with currency pair: %+v", reply)
	}
	if !strings.Contains(reply.text, "<b>今日 Binance 商家实时交易汇率 Top10:</b>") {
		t.Fatalf("rate reply should render bold top10 title: %+v", reply)
	}
	copyableTop10 := "<pre>1) 103.82 INR/USDT  Srivenkateswara\n2) 103.84 INR/USDT  EXPRESS-P2P-TRADER</pre>"
	if !strings.Contains(reply.text, copyableTop10) {
		t.Fatalf("rate reply should render merchant quotes in one copyable block: %+v", reply)
	}
}

func TestRateQueryKRWRendersKoreanSpotTemplate(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "rate-krw-korean-spot.db")
	app.exchange = &stubMigrationExchange{
		getFn: func(provider, payType, fiat string) (*services.RateResult, error) {
			if provider != "binance" {
				t.Fatalf("krw query should keep requested default provider context: got=%s", provider)
			}
			if payType != "all" {
				t.Fatalf("krw query pay type mismatch: got=%s want=all", payType)
			}
			if fiat != "KRW" {
				t.Fatalf("krw query fiat mismatch: got=%s want=KRW", fiat)
			}
			return &services.RateResult{
				Provider:    "korea_spot",
				PayType:     "spot",
				Price:       1491,
				SampleCount: 4,
				Top10Quotes: []services.RateQuote{
					{Merchant: "BITHUMB", Price: 1490},
					{Merchant: "UPBIT", Price: 1491},
					{Merchant: "COINONE", Price: 1492},
					{Merchant: "KORBIT", Price: 1493},
				},
			}, nil
		},
	}

	const (
		chatID  int64 = -100600121
		ownerID int64 = 600121
	)
	if _, err := database.EnsureGroup(chatID, "韩元现货汇率测试群"); err != nil {
		t.Fatalf("EnsureGroup failed: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("SetOwner failed: %v", err)
	}
	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("GetGroup failed: %v", err)
	}

	msg := &tgbotapi.Message{
		MessageID: 9221,
		Date:      int(time.Now().Unix()),
		Text:      "z0 韩元",
		Chat:      &tgbotapi.Chat{ID: chatID, Type: "supergroup"},
		From:      &tgbotapi.User{ID: ownerID, UserName: "owner"},
	}
	if err := app.handleRateQuery(msg, group, msg.Text); err != nil {
		t.Fatalf("handleRateQuery failed: %v", err)
	}

	reply, ok := fourLayerFindReply(capture.snapshot(), chatID, msg.MessageID, "韩国交易所 实时汇率")
	if !ok {
		t.Fatalf("krw spot reply missing: %+v", capture.snapshot())
	}
	for _, fragment := range []string{
		"市场类型：现货盘口",
		"法币市场：韩元 (KRW)",
		"均价：1491 KRW/USDT",
		"样本数量：4",
		"请求交易所：BINANCE",
		"实际来源：韩国现货源",
		"<b>今日韩国交易所实时交易汇率:</b>",
		"<pre>1) 1490 KRW/USDT  BITHUMB\n2) 1491 KRW/USDT  UPBIT\n3) 1492 KRW/USDT  COINONE\n4) 1493 KRW/USDT  KORBIT</pre>",
	} {
		if !strings.Contains(reply.text, fragment) {
			t.Fatalf("krw spot reply missing %q: %+v", fragment, reply)
		}
	}
	if strings.Contains(reply.text, "商家实时交易汇率 Top10") {
		t.Fatalf("krw spot reply should not use merchant top10 wording: %+v", reply)
	}
}

func TestRateQuerySupportsFiatSuffixes(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "rate-fiat-suffix.db")
	type getCall struct {
		provider string
		payType  string
		fiat     string
	}
	type compareCall struct {
		payType string
		fiat    string
	}
	getCalls := make([]getCall, 0, 2)
	compareCalls := make([]compareCall, 0, 1)
	app.exchange = &stubMigrationExchange{
		getFn: func(provider, payType, fiat string) (*services.RateResult, error) {
			getCalls = append(getCalls, getCall{provider: provider, payType: payType, fiat: fiat})
			return &services.RateResult{
				Provider: provider,
				PayType:  payType,
				Price:    7.77,
				Top10:    []float64{7.77},
			}, nil
		},
		compareFn: func(payType, fiat string) ([]services.RateResult, error) {
			compareCalls = append(compareCalls, compareCall{payType: payType, fiat: fiat})
			return []services.RateResult{
				{Provider: "okx", Price: 7.77},
				{Provider: "binance", Price: 7.78},
			}, nil
		},
	}

	const (
		chatID  int64 = -100600023
		ownerID int64 = 600023
	)
	if _, err := database.EnsureGroup(chatID, "汇率法币后缀测试群"); err != nil {
		t.Fatalf("EnsureGroup failed: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("SetOwner failed: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{"rate_source": "binance"}); err != nil {
		t.Fatalf("UpdateGroupFields failed: %v", err)
	}
	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("GetGroup failed: %v", err)
	}

	cases := []struct {
		messageID int
		text      string
	}{
		{messageID: 9130, text: "欧易交易所 美元"},
		{messageID: 9131, text: "lk 日元"},
		{messageID: 9132, text: "汇率对比 欧元"},
	}
	for _, tc := range cases {
		msg := &tgbotapi.Message{
			MessageID: tc.messageID,
			Date:      int(time.Now().Unix()),
			Text:      tc.text,
			Chat:      &tgbotapi.Chat{ID: chatID, Type: "supergroup"},
			From:      &tgbotapi.User{ID: ownerID, UserName: "owner"},
		}
		if err := app.handleRateQuery(msg, group, tc.text); err != nil {
			t.Fatalf("handleRateQuery failed for %q: %v", tc.text, err)
		}
	}

	if len(getCalls) != 2 {
		t.Fatalf("get rate calls mismatch: got=%v", getCalls)
	}
	if getCalls[0] != (getCall{provider: "okx", payType: "all", fiat: "USD"}) {
		t.Fatalf("provider query fiat mismatch: got=%+v", getCalls[0])
	}
	if getCalls[1] != (getCall{provider: "binance", payType: "bank", fiat: "JPY"}) {
		t.Fatalf("pay type query fiat mismatch: got=%+v", getCalls[1])
	}
	if len(compareCalls) != 1 || compareCalls[0] != (compareCall{payType: "all", fiat: "EUR"}) {
		t.Fatalf("compare query fiat mismatch: got=%v", compareCalls)
	}

	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, chatID, 9130, "法币市场：美元 (USD)") {
		t.Fatalf("provider fiat reply should include usd market label: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 9131, "法币市场：日元 (JPY)") {
		t.Fatalf("paytype fiat reply should include jpy market label: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, chatID, 9132, "汇率对比（Top10均价 / EUR）") {
		t.Fatalf("compare fiat reply should include eur header: %+v", snapshot)
	}
}

func TestRateQueryRejectsUnsupportedFiatSuffix(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "rate-fiat-invalid.db")
	app.exchange = &stubMigrationExchange{}

	const (
		chatID  int64 = -100600024
		ownerID int64 = 600024
	)
	if _, err := database.EnsureGroup(chatID, "汇率非法法币测试群"); err != nil {
		t.Fatalf("EnsureGroup failed: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("SetOwner failed: %v", err)
	}
	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("GetGroup failed: %v", err)
	}

	msg := &tgbotapi.Message{
		MessageID: 9133,
		Date:      int(time.Now().Unix()),
		Text:      "欧易交易所 英镑",
		Chat:      &tgbotapi.Chat{ID: chatID, Type: "supergroup"},
		From:      &tgbotapi.User{ID: ownerID, UserName: "owner"},
	}
	if err := app.handleRateQuery(msg, group, msg.Text); err != nil {
		t.Fatalf("handleRateQuery failed: %v", err)
	}

	if !fourLayerHasReply(capture.snapshot(), chatID, msg.MessageID, "支持法币：CNY / JPY / KRW / VND / INR / EUR / USD") {
		t.Fatalf("invalid fiat reply missing: %+v", capture.snapshot())
	}
}

func TestRateQueryIncludesFiatSwitchMarkup(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "rate-fiat-markup.db")
	app.exchange = &stubMigrationExchange{
		getFn: func(provider, payType, fiat string) (*services.RateResult, error) {
			return &services.RateResult{
				Provider: provider,
				PayType:  payType,
				Price:    7.11,
				Top10:    []float64{7.11},
			}, nil
		},
	}

	const (
		chatID  int64 = -100600025
		ownerID int64 = 600025
	)
	if _, err := database.EnsureGroup(chatID, "汇率法币按钮测试群"); err != nil {
		t.Fatalf("EnsureGroup failed: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("SetOwner failed: %v", err)
	}
	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("GetGroup failed: %v", err)
	}

	msg := &tgbotapi.Message{
		MessageID: 9134,
		Date:      int(time.Now().Unix()),
		Text:      "欧易交易所 美元",
		Chat:      &tgbotapi.Chat{ID: chatID, Type: "supergroup"},
		From:      &tgbotapi.User{ID: ownerID, UserName: "owner"},
	}
	if err := app.handleRateQuery(msg, group, msg.Text); err != nil {
		t.Fatalf("handleRateQuery failed: %v", err)
	}

	reply, ok := fourLayerFindReply(capture.snapshot(), chatID, msg.MessageID, "法币市场：美元 (USD)")
	if !ok {
		t.Fatalf("rate query reply missing: %+v", capture.snapshot())
	}
	for _, fragment := range []string{"欧易", "火币", "币安"} {
		if !strings.Contains(reply.replyMarkup, fragment) {
			t.Fatalf("reply markup missing provider %q: %+v", fragment, reply)
		}
	}
	for _, fragment := range []string{"人民币", "美元", "欧元", "日元", "韩元", "越南盾", "印度卢比"} {
		if !strings.Contains(reply.replyMarkup, fragment) {
			t.Fatalf("reply markup missing %q: %+v", fragment, reply)
		}
	}
	if !strings.Contains(reply.replyMarkup, "✅ 欧易") {
		t.Fatalf("reply markup should use green check for selected provider: %+v", reply)
	}
	if !strings.Contains(reply.replyMarkup, "✅ 美元") {
		t.Fatalf("reply markup should use green check for selected fiat: %+v", reply)
	}
	if strings.Contains(reply.replyMarkup, "✓") {
		t.Fatalf("reply markup should not use plain check mark: %+v", reply)
	}
}

func TestRateQueryDefaultsToBinanceWhenSourceEmpty(t *testing.T) {
	app, database, _ := newFourLayerTestApp(t, "rate-default-provider.db")
	calls := make([]string, 0, 1)
	app.exchange = &stubMigrationExchange{
		getFn: func(provider, payType, fiat string) (*services.RateResult, error) {
			calls = append(calls, provider+"|"+payType+"|"+fiat)
			return &services.RateResult{
				Provider: provider,
				PayType:  payType,
				Price:    7.11,
				Top10:    []float64{7.11},
			}, nil
		},
	}

	const (
		chatID  int64 = -100600028
		ownerID int64 = 600028
	)
	if _, err := database.EnsureGroup(chatID, "汇率默认交易所测试群"); err != nil {
		t.Fatalf("EnsureGroup failed: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("SetOwner failed: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{"rate_source": ""}); err != nil {
		t.Fatalf("UpdateGroupFields failed: %v", err)
	}
	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("GetGroup failed: %v", err)
	}

	msg := &tgbotapi.Message{
		MessageID: 9137,
		Date:      int(time.Now().Unix()),
		Text:      "z0 印度卢比",
		Chat:      &tgbotapi.Chat{ID: chatID, Type: "supergroup"},
		From:      &tgbotapi.User{ID: ownerID, UserName: "owner"},
	}
	if err := app.handleRateQuery(msg, group, msg.Text); err != nil {
		t.Fatalf("handleRateQuery failed: %v", err)
	}

	if len(calls) != 1 {
		t.Fatalf("default provider calls mismatch: %+v", calls)
	}
	if calls[0] != "binance|all|INR" {
		t.Fatalf("default provider should be binance when source empty: got=%s", calls[0])
	}
}

func TestRateQueryZ0DefaultsToBinanceWhenStoredSourceIsOKX(t *testing.T) {
	app, database, _ := newFourLayerTestApp(t, "rate-z0-default-binance.db")
	calls := make([]string, 0, 1)
	app.exchange = &stubMigrationExchange{
		getFn: func(provider, payType, fiat string) (*services.RateResult, error) {
			calls = append(calls, provider+"|"+payType+"|"+fiat)
			return &services.RateResult{
				Provider: provider,
				PayType:  payType,
				Price:    103.86,
				Top10:    []float64{103.86},
			}, nil
		},
	}

	const (
		chatID  int64 = -100600029
		ownerID int64 = 600029
	)
	if _, err := database.EnsureGroup(chatID, "z0默认币安测试群"); err != nil {
		t.Fatalf("EnsureGroup failed: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("SetOwner failed: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{"rate_source": "okx"}); err != nil {
		t.Fatalf("UpdateGroupFields failed: %v", err)
	}
	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("GetGroup failed: %v", err)
	}

	msg := &tgbotapi.Message{
		MessageID: 9138,
		Date:      int(time.Now().Unix()),
		Text:      "Z0 印度卢比",
		Chat:      &tgbotapi.Chat{ID: chatID, Type: "supergroup"},
		From:      &tgbotapi.User{ID: ownerID, UserName: "owner"},
	}
	if err := app.handleRateQuery(msg, group, msg.Text); err != nil {
		t.Fatalf("handleRateQuery failed: %v", err)
	}

	if len(calls) != 1 {
		t.Fatalf("z0 default provider calls mismatch: %+v", calls)
	}
	if calls[0] != "binance|all|INR" {
		t.Fatalf("z0 should default to binance even when stored source is okx: got=%s", calls[0])
	}
}

func TestRateQueryFiatSwitchCallbackEditsMessage(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "rate-fiat-callback.db")
	app.exchange = &stubMigrationExchange{
		getFn: func(provider, payType, fiat string) (*services.RateResult, error) {
			priceMap := map[string]float64{"USD": 7.10, "JPY": 108.5}
			return &services.RateResult{
				Provider: provider,
				PayType:  payType,
				Price:    priceMap[fiat],
				Top10:    []float64{priceMap[fiat]},
			}, nil
		},
	}

	const (
		chatID  int64 = -100600026
		ownerID int64 = 600026
	)
	if _, err := database.EnsureGroup(chatID, "汇率法币回调测试群"); err != nil {
		t.Fatalf("EnsureGroup failed: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("SetOwner failed: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{"rate_source": "okx"}); err != nil {
		t.Fatalf("UpdateGroupFields failed: %v", err)
	}

	callback := &tgbotapi.CallbackQuery{
		ID:   "rate-fiat-switch",
		Data: buildRateQueryCallbackData("欧易交易所", "JPY"),
		From: &tgbotapi.User{ID: ownerID, UserName: "owner"},
		Message: &tgbotapi.Message{
			MessageID: 9135,
			Chat:      &tgbotapi.Chat{ID: chatID, Type: "supergroup"},
		},
	}
	if err := app.handleCallbackQuery(context.Background(), callback); err != nil {
		t.Fatalf("handleCallbackQuery failed: %v", err)
	}

	reply, ok := fourLayerFindReply(capture.snapshot(), chatID, 9135, "法币市场：日元 (JPY)")
	if !ok {
		t.Fatalf("callback edited rate reply missing: %+v", capture.snapshot())
	}
	if reply.parseMode != telegramParseModeHTML {
		t.Fatalf("callback reply parse mode mismatch: want=%q got=%q", telegramParseModeHTML, reply.parseMode)
	}
	if !strings.Contains(reply.replyMarkup, "日元") || !strings.Contains(reply.replyMarkup, "人民币") {
		t.Fatalf("callback reply markup missing fiat buttons: %+v", reply)
	}
}

func TestRateQueryProviderSwitchMarkupPreservesProviderContext(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "rate-provider-callback.db")
	app.exchange = &stubMigrationExchange{
		getFn: func(provider, payType, fiat string) (*services.RateResult, error) {
			priceMap := map[string]map[string]float64{
				"okx":     {"USD": 7.10, "JPY": 108.10},
				"huobi":   {"USD": 7.20, "JPY": 108.20},
				"binance": {"USD": 7.30, "JPY": 108.30},
			}
			return &services.RateResult{
				Provider: provider,
				PayType:  payType,
				Price:    priceMap[provider][fiat],
				Top10:    []float64{priceMap[provider][fiat]},
			}, nil
		},
	}

	const (
		chatID  int64 = -100600027
		ownerID int64 = 600027
	)
	if _, err := database.EnsureGroup(chatID, "汇率交易所回调测试群"); err != nil {
		t.Fatalf("EnsureGroup failed: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("SetOwner failed: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{"rate_source": "okx"}); err != nil {
		t.Fatalf("UpdateGroupFields failed: %v", err)
	}

	callback := &tgbotapi.CallbackQuery{
		ID:   "rate-provider-switch",
		Data: buildRateQueryCallbackData("z0", "USD", "binance"),
		From: &tgbotapi.User{ID: ownerID, UserName: "owner"},
		Message: &tgbotapi.Message{
			MessageID: 9136,
			Chat:      &tgbotapi.Chat{ID: chatID, Type: "supergroup"},
		},
	}
	if err := app.handleCallbackQuery(context.Background(), callback); err != nil {
		t.Fatalf("handleCallbackQuery failed: %v", err)
	}

	reply, ok := fourLayerFindReply(capture.snapshot(), chatID, 9136, "法币市场：美元 (USD)")
	if !ok {
		t.Fatalf("provider callback edited rate reply missing: %+v", capture.snapshot())
	}
	if !strings.Contains(reply.text, "请求交易所：BINANCE") {
		t.Fatalf("provider callback should switch requested provider: %+v", reply)
	}
	if !strings.Contains(reply.text, "实际来源：BINANCE") {
		t.Fatalf("provider callback should switch actual provider: %+v", reply)
	}
	if !strings.Contains(reply.replyMarkup, buildRateQueryCallbackData("z0", "JPY", "binance")) {
		t.Fatalf("provider callback markup should preserve provider context for fiat buttons: %+v", reply)
	}
}

func TestCardCommandKeepsExistingCommands(t *testing.T) {
	app, _, capture := newFourLayerTestApp(t, "card-compat.db")
	client := &http.Client{
		Transport: roundTripFuncBot(func(req *http.Request) (*http.Response, error) {
			switch {
			case strings.Contains(req.URL.Host, "ccdcapi.alipay.com"):
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"bank":"CMB","cardType":"DC"}`)),
					Header:     make(http.Header),
				}, nil
			case strings.Contains(req.URL.Host, "lookup.binlist.net"):
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"scheme":"visa","type":"debit","bank":{"name":"招商银行"},"country":{"name":"CN"}}`)),
					Header:     make(http.Header),
				}, nil
			default:
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(strings.NewReader(`{}`)),
					Header:     make(http.Header),
				}, nil
			}
		}),
		Timeout: 2 * time.Second,
	}
	app.card = services.NewCardServiceWithClients(client, client, services.BranchAPIConfig{})

	msg := &tgbotapi.Message{
		MessageID: 9301,
		Date:      int(time.Now().Unix()),
		Chat:      &tgbotapi.Chat{ID: -100600003, Type: "supergroup"},
		From:      &tgbotapi.User{ID: 600003},
	}
	if err := app.handleCardQuery(msg, "4539578763621486"); err != nil {
		t.Fatalf("handleCardQuery failed: %v", err)
	}
	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, msg.Chat.ID, msg.MessageID, "银行卡信息不完整") {
		t.Fatalf("incomplete card reply missing: %+v", snapshot)
	}
}

func TestCardCommandRendersNineBankTXTFields(t *testing.T) {
	app, _, capture := newFourLayerTestApp(t, "card-banktxt-complete.db")
	client := &http.Client{
		Transport: roundTripFuncBot(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Host {
			case "tenant.api.cn":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(strings.NewReader(
						`{"status":true,"code":10200,"msg":"查询成功","data":{"num":"622305453434432224","bankname":"江苏省农村信用社联合社","province":"江苏省","city":"淮安","cardname":"圆鼎借记IC卡 公","cardtype":"借记卡","banken":"ICBC","banktel":"96008","luhn":true}}`,
					)),
					Header: make(http.Header),
				}, nil
			case "branch.example.test":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(strings.NewReader(
						`{"data":{"cnaps_code":"102100099996","bank_name":"江苏省农村信用社联合社","branch_name":"江苏省农村信用社联合社淮安支行","province":"江苏省","city":"淮安"}}`,
					)),
					Header: make(http.Header),
				}, nil
			default:
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(strings.NewReader(`{}`)),
					Header:     make(http.Header),
				}, nil
			}
		}),
		Timeout: 2 * time.Second,
	}
	app.card = services.NewCardServiceWithPolicyAndRouting(
		client,
		client,
		services.BranchAPIConfig{URL: "https://branch.example.test/query"},
		0,
		10*time.Millisecond,
		services.CardRoutingConfig{
			PrimaryEndpoint: "https://tenant.api.cn/yinhangka/",
			PrimaryAPIKey:   "secret-token",
			DisableCache:    true,
		},
	)

	msg := &tgbotapi.Message{
		MessageID: 9305,
		Date:      int(time.Now().Unix()),
		Chat:      &tgbotapi.Chat{ID: -100600003, Type: "supergroup"},
		From:      &tgbotapi.User{ID: 600003},
	}
	if err := app.handleCardQuery(msg, "622305453434432224"); err != nil {
		t.Fatalf("handleCardQuery failed: %v", err)
	}

	reply, ok := fourLayerFindReply(capture.snapshot(), msg.Chat.ID, msg.MessageID, "银行卡号: 622305453434432224")
	if !ok {
		t.Fatalf("bank.txt card reply missing: %+v", capture.snapshot())
	}
	expected := strings.Join([]string{
		"银行卡号: 622305453434432224",
		"银行名称: 江苏省农村信用社联合社",
		"卡类型: 借记卡",
		"卡名称: 圆鼎借记IC卡 公",
		"借识卡/贷记卡识别码: 借记卡",
		"银行名称编码: ICBC",
		"银行客服电话: 96008",
		"省份: 江苏省",
		"城市: 淮安",
	}, "\n")
	if reply.text != expected {
		t.Fatalf("unexpected bank.txt card reply:\n%s", reply.text)
	}
}

func TestCardCommandDoesNotRequireExactBranchFieldsYet(t *testing.T) {
	app, _, capture := newFourLayerTestApp(t, "card-banktxt-incomplete.db")
	client := &http.Client{
		Transport: roundTripFuncBot(func(req *http.Request) (*http.Response, error) {
			if req.URL.Host == "tenant.api.cn" {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(strings.NewReader(
						`{"status":true,"code":10200,"msg":"查询成功","data":{"num":"622305453434432224","bankname":"江苏省农村信用社联合社","province":"江苏省","city":"淮安","cardname":"圆鼎借记IC卡 公","cardtype":"借记卡","banken":"ICBC","banktel":"96008","luhn":true}}`,
					)),
					Header: make(http.Header),
				}, nil
			}
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
		}),
		Timeout: 2 * time.Second,
	}
	app.card = services.NewCardServiceWithPolicyAndRouting(
		client,
		client,
		services.BranchAPIConfig{},
		0,
		10*time.Millisecond,
		services.CardRoutingConfig{
			PrimaryEndpoint: "https://tenant.api.cn/yinhangka/",
			PrimaryAPIKey:   "secret-token",
			DisableCache:    true,
		},
	)

	msg := &tgbotapi.Message{
		MessageID: 9306,
		Date:      int(time.Now().Unix()),
		Chat:      &tgbotapi.Chat{ID: -100600003, Type: "supergroup"},
		From:      &tgbotapi.User{ID: 600003},
	}
	if err := app.handleCardQuery(msg, "622305453434432224"); err != nil {
		t.Fatalf("handleCardQuery failed: %v", err)
	}
	reply, ok := fourLayerFindReply(capture.snapshot(), msg.Chat.ID, msg.MessageID, "银行卡号: 622305453434432224")
	if !ok {
		t.Fatalf("nine-field card reply missing: %+v", capture.snapshot())
	}
	if strings.Contains(reply.text, "联行号") || strings.Contains(reply.text, "开户行名称") {
		t.Fatalf("nine-field response should omit exact branch fields: %s", reply.text)
	}
	if strings.Contains(reply.text, "103100000026") {
		t.Fatalf("head-office CNAPS must not be used in nine-field response: %s", reply.text)
	}
}

func TestCardCommandNineFieldModeSkipsExactBranchLookup(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "card-banktxt-override.db")
	const chatID int64 = -100600003
	if err := database.SetBranchCodeOverride(chatID, "622305453434432224", "320800000001", "中国工商银行淮安支行", "", 600003); err != nil {
		t.Fatalf("SetBranchCodeOverride failed: %v", err)
	}
	client := &http.Client{
		Transport: roundTripFuncBot(func(req *http.Request) (*http.Response, error) {
			if req.URL.Host == "tenant.api.cn" {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(strings.NewReader(
						`{"status":true,"code":10200,"msg":"查询成功","data":{"num":"622305453434432224","bankname":"江苏省农村信用社联合社","province":"江苏省","city":"淮安","cardname":"圆鼎借记IC卡 公","cardtype":"借记卡","banken":"ICBC","banktel":"96008","luhn":true}}`,
					)),
					Header: make(http.Header),
				}, nil
			}
			if req.URL.Host == "branch.example.test" {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"data":{"cnaps_code":"999999999999","branch_name":"不应使用的外部支行"}}`)),
					Header:     make(http.Header),
				}, nil
			}
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
		}),
		Timeout: 2 * time.Second,
	}
	app.card = services.NewCardServiceWithPolicyAndRouting(
		client,
		client,
		services.BranchAPIConfig{URL: "https://branch.example.test/query"},
		0,
		10*time.Millisecond,
		services.CardRoutingConfig{
			PrimaryEndpoint: "https://tenant.api.cn/yinhangka/",
			PrimaryAPIKey:   "secret-token",
			DisableCache:    true,
		},
	)

	msg := &tgbotapi.Message{
		MessageID: 9307,
		Date:      int(time.Now().Unix()),
		Chat:      &tgbotapi.Chat{ID: chatID, Type: "supergroup"},
		From:      &tgbotapi.User{ID: 600003},
	}
	if err := app.handleCardQuery(msg, "622305453434432224"); err != nil {
		t.Fatalf("handleCardQuery failed: %v", err)
	}
	reply, ok := fourLayerFindReply(capture.snapshot(), chatID, msg.MessageID, "银行卡号: 622305453434432224")
	if !ok {
		t.Fatalf("override card reply missing: %+v", capture.snapshot())
	}
	if strings.Contains(reply.text, "联行号") || strings.Contains(reply.text, "开户行名称") {
		t.Fatalf("nine-field response should omit exact branch fields: %s", reply.text)
	}
	if strings.Contains(reply.text, "320800000001") || strings.Contains(reply.text, "999999999999") || strings.Contains(reply.text, "不应使用的外部支行") {
		t.Fatalf("nine-field response should not contain branch values: %s", reply.text)
	}
}

func TestCardCommandShowsDomesticBankcardValidationSource(t *testing.T) {
	app, _, capture := newFourLayerTestApp(t, "card-domestic-source.db")

	msg := &tgbotapi.Message{
		MessageID: 9302,
		Date:      int(time.Now().Unix()),
		Chat:      &tgbotapi.Chat{ID: -100600003, Type: "supergroup"},
		From:      &tgbotapi.User{ID: 600003},
	}
	if err := app.handleCardQuery(msg, "622305453434432224"); err != nil {
		t.Fatalf("handleCardQuery failed: %v", err)
	}
	snapshot := capture.snapshot()
	reply, ok := fourLayerFindReply(snapshot, msg.Chat.ID, msg.MessageID, "银行卡信息不完整")
	if !ok {
		t.Fatalf("domestic incomplete reply missing: %+v", snapshot)
	}
	if !strings.Contains(reply.text, "卡名称") || !strings.Contains(reply.text, "省份") || !strings.Contains(reply.text, "城市") {
		t.Fatalf("domestic missing fields should be listed: %s", reply.text)
	}
}

func TestCardCommandShowsAPICNDetailNineFieldTemplate(t *testing.T) {
	app, _, capture := newFourLayerTestApp(t, "card-apicn-detail-template.db")
	client := &http.Client{
		Transport: roundTripFuncBot(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Host, "tenant.api.cn") {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(strings.NewReader(
						`{"status":true,"code":10200,"msg":"查询成功","data":{"num":"622305453434432224","bankname":"江苏省农村信用社联合社","province":"江苏省","city":"淮安","code":"320800","cardname":"圆鼎借记IC卡 公","cardtype":"借记卡","bin":"622305","banken":"ICBC","banktel":"96008","bankurl":"http://www.jsnx.net/cn/","luhn":true}}`,
					)),
					Header: make(http.Header),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader(`{}`)),
				Header:     make(http.Header),
			}, nil
		}),
		Timeout: 2 * time.Second,
	}
	app.card = services.NewCardServiceWithPolicyAndRouting(
		client,
		client,
		services.BranchAPIConfig{},
		0,
		10*time.Millisecond,
		services.CardRoutingConfig{
			PrimaryEndpoint: "https://tenant.api.cn/yinhangka/",
			PrimaryAPIKey:   "secret-token",
			DisableCache:    true,
		},
	)

	msg := &tgbotapi.Message{
		MessageID: 9303,
		Date:      int(time.Now().Unix()),
		Chat:      &tgbotapi.Chat{ID: -100600003, Type: "supergroup"},
		From:      &tgbotapi.User{ID: 600003},
	}
	if err := app.handleCardQuery(msg, "622305453434432224"); err != nil {
		t.Fatalf("handleCardQuery failed: %v", err)
	}

	reply, ok := fourLayerFindReply(capture.snapshot(), msg.Chat.ID, msg.MessageID, "银行卡号: 622305453434432224")
	if !ok {
		t.Fatalf("card detail nine-field reply missing: %+v", capture.snapshot())
	}
	expected := strings.Join([]string{
		"银行卡号: 622305453434432224",
		"银行名称: 江苏省农村信用社联合社",
		"卡类型: 借记卡",
		"卡名称: 圆鼎借记IC卡 公",
		"借识卡/贷记卡识别码: 借记卡",
		"银行名称编码: ICBC",
		"银行客服电话: 96008",
		"省份: 江苏省",
		"城市: 淮安",
	}, "\n")
	if reply.text != expected {
		t.Fatalf("unexpected card detail nine-field reply: %s", reply.text)
	}
}

func TestCardCommandShowsHaoshudiWebNineFieldTemplate(t *testing.T) {
	app, _, capture := newFourLayerTestApp(t, "card-haoshudi-web-template.db")
	client := &http.Client{
		Transport: roundTripFuncBot(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/api/token/create/":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"status":true,"code":0,"msg":"ok","data":{"ticket":"web-ticket","expire_time":"1783791951"}}`)),
					Header:     make(http.Header),
				}, nil
			case "/api/bank/query/":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(strings.NewReader(
						`{"status":true,"code":10200,"msg":"查询成功","data":{"num":"622305453434432224","bankname":"工商银行","bin":"622305","cardtype":"贷记卡","type":"借记卡","cardname":"E时代卡","length":"18","luhn":false}}`,
					)),
					Header: make(http.Header),
				}, nil
			case "/api/bank/area/":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"status":true,"code":10200,"msg":"查询成功","data":{"address":"江苏省 淮安"}}`)),
					Header:     make(http.Header),
				}, nil
			default:
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(strings.NewReader(`{}`)),
					Header:     make(http.Header),
				}, nil
			}
		}),
		Timeout: 2 * time.Second,
	}
	app.card = services.NewCardServiceWithPolicyAndRouting(
		client,
		client,
		services.BranchAPIConfig{},
		0,
		10*time.Millisecond,
		services.CardRoutingConfig{
			PrimaryEndpoint: "https://www.haoshudi.com/yinhangka/",
			DisableCache:    true,
		},
	)

	msg := &tgbotapi.Message{
		MessageID: 9304,
		Date:      int(time.Now().Unix()),
		Chat:      &tgbotapi.Chat{ID: -100600003, Type: "supergroup"},
		From:      &tgbotapi.User{ID: 600003},
	}
	if err := app.handleCardQuery(msg, "622305453434432224"); err != nil {
		t.Fatalf("handleCardQuery failed: %v", err)
	}

	reply, ok := fourLayerFindReply(capture.snapshot(), msg.Chat.ID, msg.MessageID, "银行卡号: 622305453434432224")
	if !ok {
		t.Fatalf("haoshudi web nine-field reply missing: %+v", capture.snapshot())
	}
	expected := strings.Join([]string{
		"银行卡号: 622305453434432224",
		"银行名称: 工商银行",
		"卡类型: 借记卡",
		"卡名称: E时代卡",
		"借识卡/贷记卡识别码: 借记卡",
		"银行名称编码: ICBC",
		"银行客服电话: 95588",
		"省份: 江苏省",
		"城市: 淮安",
	}, "\n")
	if reply.text != expected {
		t.Fatalf("unexpected haoshudi web nine-field reply: %s", reply.text)
	}
}

func TestRateSettingPermissionUnchanged(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "rate-permission.db")
	app.exchange = &stubMigrationExchange{}

	const (
		chatID   int64 = -100600004
		ownerID  int64 = 600004
		normalID int64 = 600005
	)
	if _, err := database.EnsureGroup(chatID, "权限兼容测试群"); err != nil {
		t.Fatalf("EnsureGroup failed: %v", err)
	}
	if err := database.SetOwner(chatID, ownerID); err != nil {
		t.Fatalf("SetOwner failed: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{"rate_source": "okx"}); err != nil {
		t.Fatalf("UpdateGroupFields failed: %v", err)
	}
	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("GetGroup failed: %v", err)
	}

	for i, cmd := range []string{"设置欧易交易所", "设置火币交易所", "设置币安交易所"} {
		msg := &tgbotapi.Message{
			MessageID: 9400 + i,
			Date:      int(time.Now().Unix()),
			Text:      cmd,
			Chat:      &tgbotapi.Chat{ID: chatID, Type: "supergroup"},
			From:      &tgbotapi.User{ID: normalID, UserName: "normal"},
		}
		if err := app.handleRateQuery(msg, group, cmd); err != nil {
			t.Fatalf("handleRateQuery permission case failed for %s: %v", cmd, err)
		}
		updated, err := database.GetGroup(chatID)
		if err != nil {
			t.Fatalf("GetGroup failed: %v", err)
		}
		if updated.RateSource != "okx" {
			t.Fatalf("rate source should remain okx for normal user, got=%s", updated.RateSource)
		}
		if fourLayerHasAnyReply(capture.snapshot(), chatID, msg.MessageID) {
			t.Fatalf("normal user rate source command should be silent for %s", cmd)
		}
	}
}

func TestUnknownAliasNotRouted(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "unknown-alias.db")
	const chatID int64 = -100600005
	if _, err := database.EnsureGroup(chatID, "别名测试群"); err != nil {
		t.Fatalf("EnsureGroup failed: %v", err)
	}
	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("GetGroup failed: %v", err)
	}
	msg := &tgbotapi.Message{
		MessageID: 9501,
		Date:      int(time.Now().Unix()),
		Text:      "k0",
		Chat:      &tgbotapi.Chat{ID: chatID, Type: "supergroup"},
		From:      &tgbotapi.User{ID: 600006},
	}
	if err := app.dispatchGroupMessage(context.Background(), msg, group, msg.Text); err != nil {
		t.Fatalf("dispatchGroupMessage failed: %v", err)
	}
	if fourLayerHasReply(capture.snapshot(), chatID, msg.MessageID, "实时汇率") {
		t.Fatalf("unknown alias should not be routed as rate command")
	}
}

func TestRateQueryFallsBackToGroupRateWhenRealtimeUnavailable(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "rate-fallback.db")
	app.exchange = &stubMigrationExchange{
		getFn: func(provider, payType, fiat string) (*services.RateResult, error) {
			return nil, errors.New("upstream unavailable")
		},
	}

	const (
		chatID int64 = -100600007
		owner  int64 = 600008
	)
	if _, err := database.EnsureGroup(chatID, "汇率回退测试群"); err != nil {
		t.Fatalf("EnsureGroup failed: %v", err)
	}
	if err := database.SetOwner(chatID, owner); err != nil {
		t.Fatalf("SetOwner failed: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{"custom_rate": 7.15}); err != nil {
		t.Fatalf("UpdateGroupFields failed: %v", err)
	}
	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("GetGroup failed: %v", err)
	}

	msg := &tgbotapi.Message{
		MessageID: 9701,
		Date:      int(time.Now().Unix()),
		Text:      "lk",
		Chat:      &tgbotapi.Chat{ID: chatID, Type: "supergroup"},
		From:      &tgbotapi.User{ID: owner, UserName: "owner"},
	}
	if err := app.handleRateQuery(msg, group, msg.Text); err != nil {
		t.Fatalf("handleRateQuery failed: %v", err)
	}
	snapshot := capture.snapshot()
	if !fourLayerHasReply(snapshot, chatID, msg.MessageID, "实时汇率暂不可用，已回退群内汇率") {
		t.Fatalf("fallback hint missing")
	}
	if !fourLayerHasReply(snapshot, chatID, msg.MessageID, "当前汇率：7.15") {
		t.Fatalf("fallback rate missing")
	}
}

func TestGroupStartCommandShouldBeSilent(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "group-start-silent.db")
	const chatID int64 = -100600006
	if _, err := database.EnsureGroup(chatID, "群start静默测试群"); err != nil {
		t.Fatalf("EnsureGroup failed: %v", err)
	}
	msg := &tgbotapi.Message{
		MessageID: 9601,
		Date:      int(time.Now().Unix()),
		Text:      "/start",
		Chat:      &tgbotapi.Chat{ID: chatID, Type: "supergroup"},
		From:      &tgbotapi.User{ID: 600007, UserName: "member"},
	}
	if err := app.handleMessage(context.Background(), msg); err != nil {
		t.Fatalf("handleMessage failed: %v", err)
	}
	if fourLayerHasReply(capture.snapshot(), chatID, msg.MessageID, "查记机器人已启动") {
		t.Fatalf("group /start should not send startup guidance reply")
	}
}

// Ensure imported db package is kept for type references in this file.
var _ db.GroupConfig
