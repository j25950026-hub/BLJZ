package bot

import (
	"bytes"
	"context"
	"crypto/hmac"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"go-bot/internal/config"
	"go-bot/internal/db"
	"go-bot/internal/services"
	"go-bot/internal/system"
	"go-bot/internal/utils"
	"go-bot/internal/webhook"
)

var (
	scheduleEditRegex                 = regexp.MustCompile(`^编辑定时群发\s+(\d+)\s+(\d{4}-\d{2}-\d{2})\s+(\d{2}:\d{2})(?::(\d{2}))?(?:\s+(.+))?$`)
	pureNumberRegex                   = regexp.MustCompile(`^-?\d+(?:\.\d+)?$`)
	orderQueryDateRangeRegex          = regexp.MustCompile(`^\s*(\d{4}-\d{2}-\d{2})\s*(?:-|~|至|到)\s*(\d{4}-\d{2}-\d{2})(.*)$`)
	trc20AddressFinderRegex           = regexp.MustCompile(`\bT[1-9A-HJ-NP-Za-km-z]{33}\b`)
	trc20AddressLooseFinderRegex      = regexp.MustCompile(`\bT[A-Za-z0-9]{33}\b`)
	erc20AddressFinderRegex           = regexp.MustCompile(`\b0[xX][a-fA-F0-9]{40}\b`)
	accountNumberFinderRegex          = regexp.MustCompile(`\b\d{10,20}\b`)
	ErrUnsupportedWebAppPrivateAction = errors.New("unsupported webapp private action")
)

const (
	privateCallbackPrefix             = "pv:"
	privateGroupControlCallbackPrefix = "pc:"
	groupPanelCallbackPrefix          = "gp:"
	broadcastTagCallbackPrefix        = "bt:"
	batchTagCallbackPrefix            = "bg:"
	batchTypeCallbackPrefix           = "gt:"
	ledgerCardCallbackPrefix          = "lc:"
	trc20QueryCallbackPrefix          = "tq:"
	rateQueryCallbackPrefix           = "rq:"
	ledgerCardActionDetail            = "detail_bill"
	ledgerCardActionOpen              = "open_bill"
	ledgerCardActionCancel            = "cancel_bill_open"
	trc20ActionRefresh                = "refresh"
	trc20ActionChart                  = "chart"
	trc20ActionWatch                  = "watch"
	trc20ActionRisk                   = "risk"
	trc20ActionMenu                   = "menu"
	bookkeepingNotStartedTip          = "今天账期未开始，请先发送【开始记账】"
	webAppAuthTTL                     = 15 * time.Minute
	webAppURLVersionTag               = "tg-ui-replica-20260221"
	groupUserWatchInterval            = 15 * time.Second
	groupUserWatchBatchSize           = 50
	groupUserWatchReqGap              = 150 * time.Millisecond
	bookkeepingQueueWorkers           = 8
	bookkeepingQueueDepth             = 1000
	bookkeepingQueueStopWait          = 10 * time.Second
	webhookUpdateQueueDepth           = 2048
	outboundQueueDepth                = 2048
	outboundQueueWorkers              = 4
	outboundQueueStopWait             = 10 * time.Second
	ledgerReceiptOutboxWorkers        = 4
	timeSeriesWriteTimeout            = 2 * time.Second
	rateSetupTempDataKey              = "fsm:rate_setup"
	rateSetupTempDataTTL              = 5 * time.Minute
	ledgerOpenTempDataKey             = "fsm:ledger_open"
	duplicateAlertWindowText          = "10分钟内"
	botInviteWelcomeDedupTTL          = 30 * time.Second
	memberJoinWelcomeDedupTTL         = 30 * time.Second
	businessRiskControlsEnabled       = false
	rateDeviationThreshold            = 0.10
	rateDeviationEventType            = "rate_deviation"
	rateDeviationSeverity             = "warn"
	orderQueryDateLayout              = "2006-01-02"
	orderQueryTimeLayout              = "2006-01-02 15:04:05"
	interactiveBroadcastMaxTags       = 30
	batchTagPageSize                  = 10
	batchTagSessionTTL                = 15 * time.Minute
	legacyDefaultJoinWelcome          = "欢迎加入本群，请联系操作人了解群规则。"
	legacyDefaultClassStartAd         = "上课时间到，请专注工作，非必要信息暂缓发送。"
	legacyDefaultClassEndAd           = "下课时间到，可恢复正常交流。"
	currentDefaultClassStartAd        = "上课时间到，可恢复正常发言。"
	currentDefaultClassEndAd          = "下课时间到，群聊已禁言。"
)

const botInviteWelcomeFirstMessage = "查记机器人\n感谢您把我添加到贵群！下一步设置费率，请发：设置费率xx"

type memberJoinWelcomeKey struct {
	ChatID int64
	UserID int64
}

type UpdateMetricsRecorder interface {
	ObserveUpdate(source string, duration time.Duration, success bool)
}

type noopUpdateMetricsRecorder struct{}

func (noopUpdateMetricsRecorder) ObserveUpdate(string, time.Duration, bool) {}

type exchangeRateProvider interface {
	GetRate(provider, payType, fiat string) (*services.RateResult, error)
	CompareRates(payType, fiat string) ([]services.RateResult, error)
}

func RateDeviationAlertThreshold() float64 {
	return rateDeviationThreshold
}

func (a *App) businessRiskControlsEnabled() bool {
	return businessRiskControlsEnabled
}

func outboundQueueWorkersFromConfig(cfg config.Config) int {
	if cfg.OutboundQueueWorkers > 0 {
		return cfg.OutboundQueueWorkers
	}
	return outboundQueueWorkers
}

func outboundQueueDepthFromConfig(cfg config.Config) int {
	if cfg.OutboundQueueDepth > 0 {
		return cfg.OutboundQueueDepth
	}
	return outboundQueueDepth
}

func ledgerReceiptOutboxWorkersFromConfig(cfg config.Config) int {
	if cfg.LedgerReceiptOutboxWorkers > 0 {
		return cfg.LedgerReceiptOutboxWorkers
	}
	return ledgerReceiptOutboxWorkers
}

type App struct {
	cfg                               config.Config
	loc                               *time.Location
	now                               func() time.Time
	db                                *db.Database
	api                               *tgbotapi.BotAPI
	apiBucket                         *tokenBucket
	longPoller                        *LongPoller
	pollStartedAt                     time.Time
	exchange                          exchangeRateProvider
	address                           *services.AddressService
	card                              *services.CardService
	geo                               *services.GeoService
	worldTime                         *services.WorldTimeService
	exportEngine                      *services.ExportEngine
	timeSeries                        services.TimeSeriesRecorder
	updateMetrics                     UpdateMetricsRecorder
	outboundMetrics                   OutboundMetricsRecorder
	mediaHandler                      MediaHandler
	addressVerificationMediaPublisher AddressVerificationMediaPublisher
	addressVerificationMediaLifecycle addressVerificationMediaLifecycle

	schedMu sync.Mutex
	timers  map[int64]*time.Timer

	botInviteWelcomeMu sync.Mutex
	botInviteWelcomeAt map[int64]time.Time

	memberJoinWelcomeMu sync.Mutex
	memberJoinWelcomeAt map[memberJoinWelcomeKey]time.Time

	groupUserScanOffset    map[int64]int
	bookkeepingQueue       *serializedExecutionQueue
	outboundQueue          *priorityDispatchQueue
	webhookUpdates         chan tgbotapi.Update
	stateHub               *WebSocketStateHub
	stateBroadcaster       StateBroadcaster
	requestLocker          RequestLocker
	sessionStore           DistributedSessionStore
	userFSM                UserFSM
	dailyLedgerCounter     DailyLedgerCounter
	requestResultCache     *requestResultCache
	sensitiveConfirmer     *sensitiveOperationConfirmer
	commandVelocity        *userCommandVelocityChecker
	templateRenderer       *templateRenderer
	isGroupMemberFn        func(chatID, userID int64) bool
	ledgerReceiptAsyncOnly bool

	interactiveBroadcastMu    sync.Mutex
	interactiveBroadcastState map[int64]interactiveBroadcastSession

	batchTagMu       sync.Mutex
	batchTagSessions map[int64]batchTagSelectionSession

	batchTypeMu       sync.Mutex
	batchTypeSessions map[int64]batchTypeSelectionSession

	privateGroupControlMu       sync.Mutex
	privateGroupControlSessions map[int64]privateGroupControlSession
	privateGroupControlGroups   map[int64]privateGroupControlGroupsCacheEntry
	privateGroupControlMembers  map[privateGroupControlMemberCacheKey]privateGroupControlMemberCacheEntry
}

type interactiveBroadcastSession struct {
	InitiatorUserID int64
	AwaitingContent bool
	Content         string
	ContentMode     string
	ContentPreview  string
	SourceChatID    int64
	SourceMessageID int
	Tags            []string
	UpdatedAt       time.Time
}

type batchTagSelectionSession struct {
	InitiatorUserID int64
	Groups          []db.GroupConfig
	CurrentPage     int
	UpdatedAt       time.Time
}

type batchTypeSelectionSession struct {
	InitiatorUserID int64
	Groups          []db.GroupConfig
	CurrentPage     int
	UpdatedAt       time.Time
}

func NewApp(cfg config.Config, database *db.Database) (*App, error) {
	api, apiBucket, err := newRateLimitedBotAPI(cfg.BotToken)
	if err != nil {
		return nil, err
	}
	stateHub := NewWebSocketStateHub()
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	pollStatusPath := strings.TrimSpace(cfg.PollStatusPath)
	if pollStatusPath == "" {
		pollStatusPath = "data/recovery/poll-status.json"
	}
	return &App{
		cfg:       cfg,
		loc:       loc,
		now:       time.Now,
		db:        database,
		api:       api,
		apiBucket: apiBucket,
		longPoller: NewLongPoller(api, system.NewPollStatusStore(pollStatusPath), LongPollerOptions{
			ReleaseID:  cfg.ReleaseID,
			ReleaseSHA: cfg.ReleaseSHA256,
		}),
		pollStartedAt: time.Now(),
		exchange: services.NewExchangeServiceWithConfig(services.ExchangeConfig{
			Timeout:   time.Duration(cfg.ExchangeTimeoutMS) * time.Millisecond,
			RetryMax:  cfg.ExchangeRetryMax,
			Backoff:   time.Duration(cfg.ExchangeBackoffMS) * time.Millisecond,
			MinSample: cfg.ExchangeMinSample,
		}),
		address: services.NewAddressService(10 * time.Second),
		card: services.NewCardServiceWithPolicyAndRouting(
			system.WrapHTTPClientWithTracing(&http.Client{Timeout: time.Duration(cfg.CardTimeoutMS) * time.Millisecond}, "card-service"),
			system.WrapHTTPClientWithTracing(&http.Client{Timeout: time.Duration(cfg.BankBranchAPITimeoutSec) * time.Second}, "branch-code-service"),
			services.BranchAPIConfig{
				URL:     cfg.BankBranchAPIURL,
				Token:   cfg.BankBranchAPIToken,
				Header:  cfg.BankBranchAPIHeader,
				Timeout: time.Duration(cfg.BankBranchAPITimeoutSec) * time.Second,
			},
			cfg.CardRetryMax,
			time.Duration(cfg.CardBackoffMS)*time.Millisecond,
			services.CardRoutingConfig{
				PrimaryEndpoint:        cfg.CardPrimaryEndpoint,
				PrimaryAPIKey:          cfg.CardPrimaryAPIKey,
				PrimaryAPIHeader:       cfg.CardPrimaryAPIHeader,
				DefaultProviderTimeout: time.Duration(cfg.CardProviderTimeoutMS) * time.Millisecond,
				ProviderTimeouts: map[string]time.Duration{
					"primary": time.Duration(cfg.CardPrimaryTimeoutMS) * time.Millisecond,
					"binlist": time.Duration(cfg.CardBinlistTimeoutMS) * time.Millisecond,
				},
				CacheTTL:        time.Duration(cfg.CardCacheTTLSeconds) * time.Second,
				CacheMaxEntries: cfg.CardCacheMaxEntries,
			},
		),
		geo: services.NewGeoServiceWithConfig(services.GeoConfig{
			GeocodeEndpoint: cfg.GeoGeocodeEndpoint,
			RouteEndpoint:   cfg.GeoRouteEndpoint,
			Timeout:         time.Duration(cfg.GeoTimeoutMS) * time.Millisecond,
			RetryMax:        cfg.GeoRetryMax,
			Backoff:         time.Duration(cfg.GeoBackoffMS) * time.Millisecond,
			CacheTTL:        time.Duration(cfg.GeoCacheTTLSeconds) * time.Second,
			CacheMaxEntries: 1000,
		}),
		worldTime:       services.NewWorldTimeService(),
		exportEngine:    services.NewExportEngine(),
		timeSeries:      services.NewNoopTimeSeriesRecorder(),
		updateMetrics:   noopUpdateMetricsRecorder{},
		outboundMetrics: noopOutboundMetricsRecorder{},
		mediaHandler:    noopMediaHandler{},
		timers:          map[int64]*time.Timer{},

		botInviteWelcomeAt:          map[int64]time.Time{},
		memberJoinWelcomeAt:         map[memberJoinWelcomeKey]time.Time{},
		groupUserScanOffset:         map[int64]int{},
		bookkeepingQueue:            newSerializedExecutionQueue(bookkeepingQueueWorkers, bookkeepingQueueDepth),
		outboundQueue:               newPriorityDispatchQueueWithWorkers(outboundQueueDepthFromConfig(cfg), outboundQueueWorkersFromConfig(cfg)),
		webhookUpdates:              make(chan tgbotapi.Update, webhookUpdateQueueDepth),
		stateHub:                    stateHub,
		stateBroadcaster:            stateHub,
		requestLocker:               noopRequestLocker{},
		sessionStore:                noopDistributedSessionStore{},
		userFSM:                     newDefaultUserFSM(),
		dailyLedgerCounter:          noopDailyLedgerCounter{},
		requestResultCache:          newRequestResultCache(0),
		sensitiveConfirmer:          newSensitiveOperationConfirmer(sensitiveOperationConfirmTTL),
		commandVelocity:             newUserCommandVelocityChecker(defaultVelocityWindow, defaultVelocityMaxPerWindow),
		templateRenderer:            newTemplateRenderer(),
		interactiveBroadcastState:   map[int64]interactiveBroadcastSession{},
		batchTagSessions:            map[int64]batchTagSelectionSession{},
		batchTypeSessions:           map[int64]batchTypeSelectionSession{},
		privateGroupControlSessions: map[int64]privateGroupControlSession{},
		privateGroupControlGroups:   map[int64]privateGroupControlGroupsCacheEntry{},
		privateGroupControlMembers:  map[privateGroupControlMemberCacheKey]privateGroupControlMemberCacheEntry{},
	}, nil
}

func (a *App) Run(ctx context.Context) error {
	defer a.shutdownAPIBucket()
	defer a.shutdownBookkeepingQueue()
	defer a.shutdownOutboundQueue()
	defer a.shutdownMediaHandler()
	defer a.shutdownAddressVerificationMediaPublisher()

	if err := a.restoreSchedules(); err != nil {
		log.Printf("恢复定时任务失败: %v", err)
	}
	a.syncCommandMenus()
	a.runLedgerReceiptOutboxWorkers(ctx)
	go a.runClosingReminderLoop(ctx)
	go func() {
		if err := a.warmupGroupAdministrators(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("管理员缓存预热失败: %v", err)
		}
	}()
	go a.watchGroupUserProfiles(ctx)
	if strings.TrimSpace(a.cfg.WebhookAddr) != "" {
		return a.runWebhookLoop(ctx)
	}
	return a.runLongPollingLoop(ctx)
}

func (a *App) shutdownAPIBucket() {
	if a == nil || a.apiBucket == nil {
		return
	}
	a.apiBucket.Close()
}

func (a *App) EnqueueWebhookUpdate(update tgbotapi.Update) bool {
	if a == nil || a.webhookUpdates == nil {
		return false
	}
	select {
	case a.webhookUpdates <- update:
		return true
	default:
		return false
	}
}

func (a *App) WebhookQueueUsage() (queued, capacity int) {
	if a == nil || a.webhookUpdates == nil {
		return 0, 0
	}
	return len(a.webhookUpdates), cap(a.webhookUpdates)
}

func (a *App) runLongPollingLoop(ctx context.Context) error {
	if err := a.longPoller.MarkStarted(ctx); err != nil {
		return fmt.Errorf("记录 Telegram Long Polling 启动状态失败: %w", err)
	}
	updateCfg := tgbotapi.NewUpdate(0)
	updateCfg.Timeout = 30
	updateCfg.AllowedUpdates = []string{"message", "edited_message", "callback_query", "chat_member", "my_chat_member"}
	log.Printf("机器人已启动(Long Polling): @%s", a.api.Self.UserName)
	return runLongPollerLoop(ctx, a.longPoller, updateCfg, func(update tgbotapi.Update) {
		a.dispatchUpdate(ctx, update)
	}, nil)
}

func (a *App) PollReadiness(now time.Time) system.PollReadiness {
	if a == nil {
		return system.PollReadiness{Reason: system.PollReadinessReasonMissing}
	}
	return a.longPoller.Readiness(context.Background(), now, system.PollReadinessPolicy{
		StartedAt:       a.pollStartedAt,
		StartupGrace:    a.cfg.PollStartupGrace,
		MaxSuccessAge:   a.cfg.PollMaxSuccessAge,
		MinFailureCount: a.cfg.PollMinFailureCount,
	})
}

func (a *App) runWebhookLoop(ctx context.Context) error {
	server := webhook.NewServer(a.cfg, a)
	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- server.Run(ctx)
	}()
	log.Printf(
		"机器人已启动(Webhook): @%s addr=%s path=%s queue=%d",
		a.api.Self.UserName,
		a.cfg.WebhookAddr,
		a.cfg.WebhookPath,
		cap(a.webhookUpdates),
	)
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-serverErrCh:
			return err
		case upd := <-a.webhookUpdates:
			a.dispatchUpdate(ctx, upd)
		}
	}
}

func (a *App) dispatchUpdate(ctx context.Context, upd tgbotapi.Update) {
	startedAt := time.Now()
	source := "unknown"
	var err error
	var logPrefix string

	if upd.CallbackQuery != nil {
		source = "callback_query"
		err = a.handleCallbackQuery(ctx, upd.CallbackQuery)
		logPrefix = "处理回调失败"
	} else if upd.Message != nil {
		source = "message"
		logInboundMessage(upd.UpdateID, upd.Message)
		err = a.handleMessage(ctx, upd.Message)
		logPrefix = "处理消息失败"
	} else if upd.EditedMessage != nil {
		source = "edited_message"
		err = a.handleEditedMessage(ctx, upd.EditedMessage)
		logPrefix = "处理编辑消息失败"
	} else if upd.ChatMember != nil {
		source = "chat_member"
		err = a.handleChatMemberUpdate(upd.ChatMember)
		logPrefix = "处理成员变更失败"
	} else if upd.MyChatMember != nil {
		source = "my_chat_member"
		err = a.handleMyChatMemberUpdate(upd.MyChatMember)
		logPrefix = "处理机器人成员变更失败"
	}

	if err != nil {
		log.Printf("%s: %v", logPrefix, err)
	}
	a.observeUpdateMetrics(source, time.Since(startedAt), err == nil)
}

func logInboundMessage(updateID int, msg *tgbotapi.Message) {
	if msg == nil || msg.Chat == nil {
		return
	}
	fromID := int64(0)
	if msg.From != nil {
		fromID = msg.From.ID
	}
	text := trimForAudit(messageTextForDispatch(msg), 200)
	log.Printf(
		"收到入站消息: update_id=%d chat_id=%d chat_type=%s message_id=%d from_id=%d text=%q",
		updateID,
		msg.Chat.ID,
		msg.Chat.Type,
		msg.MessageID,
		fromID,
		text,
	)
}

func (a *App) shutdownBookkeepingQueue() {
	if a.bookkeepingQueue == nil {
		return
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), bookkeepingQueueStopWait)
	defer cancel()
	if err := a.bookkeepingQueue.Close(stopCtx); err != nil {
		log.Printf("记账队列关闭失败: %v", err)
	}
}

func (a *App) shutdownOutboundQueue() {
	if a.outboundQueue == nil {
		return
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), outboundQueueStopWait)
	defer cancel()
	if err := a.outboundQueue.Close(stopCtx); err != nil {
		log.Printf("消息优先级队列关闭失败: %v", err)
	}
}

func (a *App) StateWebSocketHub() *WebSocketStateHub {
	if a == nil {
		return nil
	}
	return a.stateHub
}

func (a *App) SetStateBroadcaster(broadcaster StateBroadcaster) {
	if a == nil {
		return
	}
	if broadcaster == nil {
		broadcaster = a.stateHub
	}
	a.stateBroadcaster = broadcaster
}

func (a *App) SetRequestLocker(locker RequestLocker) {
	if a == nil {
		return
	}
	if locker == nil {
		locker = noopRequestLocker{}
	}
	a.requestLocker = locker
}

func (a *App) SetDistributedSessionStore(store DistributedSessionStore) {
	if a == nil {
		return
	}
	if store == nil {
		store = noopDistributedSessionStore{}
	}
	a.sessionStore = store
}

func (a *App) SetUserFSM(fsm UserFSM) {
	if a == nil {
		return
	}
	if fsm == nil {
		fsm = newDefaultUserFSM()
	}
	a.userFSM = fsm
}

func (a *App) getUserFSM() UserFSM {
	if a == nil {
		return noopUserFSM{}
	}
	if a.userFSM == nil {
		a.userFSM = newDefaultUserFSM()
	}
	return a.userFSM
}

func (a *App) SetDailyLedgerCounter(counter DailyLedgerCounter) {
	if a == nil {
		return
	}
	if counter == nil {
		counter = noopDailyLedgerCounter{}
	}
	a.dailyLedgerCounter = counter
}

func (a *App) SetAddressAPIQuotaRecorder(recorder services.APIQuotaRecorder) {
	if a == nil || a.address == nil {
		return
	}
	a.address.SetAPIQuotaRecorder(recorder)
}

func (a *App) SetTimeSeriesRecorder(recorder services.TimeSeriesRecorder) {
	if a == nil {
		return
	}
	if recorder == nil {
		recorder = services.NewNoopTimeSeriesRecorder()
	}
	a.timeSeries = recorder
}

func (a *App) SetUpdateMetricsRecorder(recorder UpdateMetricsRecorder) {
	if a == nil {
		return
	}
	if recorder == nil {
		recorder = noopUpdateMetricsRecorder{}
	}
	a.updateMetrics = recorder
}

func (a *App) SetOutboundMetricsRecorder(recorder OutboundMetricsRecorder) {
	if a == nil {
		return
	}
	if recorder == nil {
		recorder = noopOutboundMetricsRecorder{}
	}
	a.outboundMetrics = recorder
	if a.outboundQueue != nil {
		a.outboundQueue.SetMetrics(recorder)
	}
}

func (a *App) EnableAddressVerificationMediaPublisher(publisher AddressVerificationMediaPublisher) error {
	if a == nil {
		return errors.New("app is nil")
	}
	if publisher == nil {
		a.addressVerificationMediaPublisher = nil
		a.addressVerificationMediaLifecycle = nil
		return nil
	}

	lifecycle, hasLifecycle := publisher.(addressVerificationMediaLifecycle)
	if hasLifecycle {
		if err := lifecycle.Start(context.Background()); err != nil {
			return fmt.Errorf("start address verification media publisher: %w", err)
		}
	}
	a.addressVerificationMediaPublisher = publisher
	if hasLifecycle {
		a.addressVerificationMediaLifecycle = lifecycle
	} else {
		a.addressVerificationMediaLifecycle = nil
	}
	return nil
}

func (a *App) shutdownAddressVerificationMediaPublisher() {
	if a == nil || a.addressVerificationMediaLifecycle == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultMediaHandlerStopWait)
	defer cancel()
	if err := a.addressVerificationMediaLifecycle.Close(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("关闭地址验证媒体发布器失败: %v", err)
	}
}

func (a *App) currentOutboundMetrics() OutboundMetricsRecorder {
	if a == nil || a.outboundMetrics == nil {
		return noopOutboundMetricsRecorder{}
	}
	return a.outboundMetrics
}

func (a *App) observeUpdateMetrics(source string, duration time.Duration, success bool) {
	if a == nil || a.updateMetrics == nil {
		return
	}
	a.updateMetrics.ObserveUpdate(source, duration, success)
}

func (a *App) syncCommandMenus() {
	defaultCommands := []tgbotapi.BotCommand{
		{Command: "start", Description: "启动机器人"},
		{Command: "menu", Description: "打开功能菜单"},
		{Command: "help", Description: "查看帮助"},
	}
	privateCommands := []tgbotapi.BotCommand{
		{Command: "start", Description: "打开私聊菜单"},
		{Command: "menu", Description: "打开私聊功能菜单"},
		{Command: "buy", Description: "购买使用/续费"},
		{Command: "orders", Description: "查看购买订单"},
		{Command: "tutorial", Description: "查看入群教程"},
		{Command: "panel", Description: "查看群控入口说明"},
		{Command: "time", Description: "世界时间示例"},
		{Command: "help", Description: "查看帮助"},
	}
	groupCommands := []tgbotapi.BotCommand{
		{Command: "menu", Description: "查看群内命令总览"},
		{Command: "panel", Description: "打开群控面板"},
		{Command: "bill", Description: "查看一键账单"},
		{Command: "rate", Description: "查看汇率对比"},
		{Command: "help", Description: "查看帮助"},
	}
	if _, err := a.api.Request(tgbotapi.NewSetMyCommands(defaultCommands...)); err != nil {
		log.Printf("设置默认命令菜单失败: %v", err)
	}
	if _, err := a.api.Request(tgbotapi.NewSetMyCommandsWithScope(tgbotapi.NewBotCommandScopeAllPrivateChats(), privateCommands...)); err != nil {
		log.Printf("设置私聊命令菜单失败: %v", err)
	}
	if _, err := a.api.Request(tgbotapi.NewSetMyCommandsWithScope(tgbotapi.NewBotCommandScopeAllGroupChats(), groupCommands...)); err != nil {
		log.Printf("设置群聊命令菜单失败: %v", err)
	}
}

func (a *App) handleMessage(ctx context.Context, msg *tgbotapi.Message) error {
	if msg == nil {
		return nil
	}
	migrationEvent, migrationErr := a.handleGroupUpgradeMigration(msg)
	if migrationErr != nil {
		log.Printf("自动迁移群升级数据失败: %v", migrationErr)
	}
	if migrationEvent && msg.MigrateToChatID != 0 {
		return nil
	}
	text := messageTextForDispatch(msg)
	a.enqueueMediaUpload(msg)

	if msg.Chat.IsPrivate() {
		userID := msg.Chat.ID
		if msg.From != nil {
			userID = msg.From.ID
		}
		if msg.IsCommand() {
			switch strings.ToLower(msg.Command()) {
			case "start", "menu", "help":
				return a.replyPrivateMenu(msg.Chat.ID, msg.MessageID, userID)
			case "panel":
				return a.replyPrivateGroupControlPanel(msg.Chat.ID, msg.MessageID, userID)
			}
		}
		if text == "取消" {
			a.clearPrivateGroupControlSession(userID)
			return a.replyPrivateGroupControlPanel(msg.Chat.ID, msg.MessageID, userID)
		}
		if text == "群控面板" || text == "控制面板" {
			return a.replyPrivateGroupControlPanel(msg.Chat.ID, msg.MessageID, userID)
		}
		if handled, err := a.handlePrivateGroupControlInput(ctx, msg, userID, text); handled || err != nil {
			return err
		}
		if msg.IsCommand() {
			switch strings.ToLower(msg.Command()) {
			case "buy":
				return a.replyPrivatePurchaseMenu(msg.Chat.ID, msg.MessageID, userID)
			case "orders":
				return a.replyPrivatePurchaseOrders(msg.Chat.ID, msg.MessageID, userID)
			case "tutorial":
				return a.replyPrivateInviteGuide(msg.Chat.ID, msg.MessageID, userID)
			case "time", "worldtime":
				return a.reply(msg.Chat.ID, msg.MessageID, "示例：北京时间、巴西时间、德国时间、美国中部时间、日本时间")
			}
		}
		if text == "" {
			return nil
		}
		if text == "帮助" || text == "菜单" || text == "界面" || text == "功能" || strings.EqualFold(text, "help") || text == "/help" {
			return a.replyPrivateMenu(msg.Chat.ID, msg.MessageID, userID)
		}
		if text == "申请试用" {
			return a.handlePrivateTrialApply(msg, userID)
		}
		if text == "联系管理员" {
			return a.handlePrivateSupport(msg, userID)
		}
		if text == "获取ID" {
			return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("当前用户ID：%d\n如需群ID，请在目标群发送【显示群ID】。", userID))
		}
		if text == "邀请进群" || text == "入群教程" || text == "图文教程" || text == "教程" {
			return a.replyPrivateInviteGuide(msg.Chat.ID, msg.MessageID, userID)
		}
		if text == "购买使用" || text == "购买" || text == "续费" || text == "套餐购买" {
			return a.replyPrivatePurchaseMenu(msg.Chat.ID, msg.MessageID, userID)
		}
		if text == "我的订单" || text == "订单状态" || text == "购买记录" {
			return a.replyPrivatePurchaseOrders(msg.Chat.ID, msg.MessageID, userID)
		}
		if text == "使用说明" {
			return a.reply(msg.Chat.ID, msg.MessageID, privateUsageGuideText())
		}
		if text == "到期时间" {
			return a.handlePrivateExpireQuery(msg, userID)
		}
		if text == "人工客服" {
			return a.handlePrivateSupport(msg, userID)
		}
		if text == "功能总览" {
			return a.reply(msg.Chat.ID, msg.MessageID, privateFeatureOverviewText())
		}
		if text == "群组管理" {
			return a.reply(msg.Chat.ID, msg.MessageID, privateGroupManageText())
		}
		if text == "查单管理" {
			return a.reply(msg.Chat.ID, msg.MessageID, privateOrderManageText())
		}
		if text == "命令示例" {
			return a.reply(msg.Chat.ID, msg.MessageID, privateExampleText())
		}
		if text == "更新日志" {
			return a.reply(msg.Chat.ID, msg.MessageID, privateUpdateText())
		}
		if text == "世界时间示例" {
			return a.reply(msg.Chat.ID, msg.MessageID, "示例：北京时间、巴西时间、德国时间、美国中部时间、日本时间")
		}
		if region, ok := utils.ParseWorldTimeQuery(text); ok {
			return a.handleWorldTime(msg, region)
		}
		return a.replyPrivateMenu(msg.Chat.ID, msg.MessageID, userID)
	}

	group, err := a.db.EnsureGroup(msg.Chat.ID, msg.Chat.Title)
	if err != nil {
		return err
	}

	if len(msg.NewChatMembers) > 0 {
		botJoined := false
		memberJoinCandidates := make([]tgbotapi.User, 0, len(msg.NewChatMembers))
		for _, member := range msg.NewChatMembers {
			uname := strings.ToLower(strings.TrimSpace(member.UserName))
			nick := composeNickname(member.FirstName, member.LastName)
			_, _, _ = a.db.UpsertGroupUser(msg.Chat.ID, member.ID, uname, nick)
			if a.isSelfNewChatMember(member) {
				botJoined = true
			} else if !member.IsBot {
				memberJoinCandidates = append(memberJoinCandidates, member)
			}
		}
		log.Printf("收到入群服务消息: chat_id=%d message_id=%d new_members=%d bot_joined=%t", msg.Chat.ID, msg.MessageID, len(msg.NewChatMembers), botJoined)
		if botJoined {
			return a.replyBotInviteWelcomeOrRenewal(
				msg.Chat.ID,
				msg.MessageID,
				group,
				orDefault(strings.TrimSpace(group.Title), strings.TrimSpace(msg.Chat.Title)),
			)
		}
		if active, _ := a.checkGroupEntitlement(group); !active {
			return nil
		}
		if group.WelcomeEnabled != 1 {
			return nil
		}
		welcomedMembers := make([]tgbotapi.User, 0, len(memberJoinCandidates))
		for _, member := range memberJoinCandidates {
			if a.acquireMemberJoinWelcomeSlot(msg.Chat.ID, member.ID) {
				welcomedMembers = append(welcomedMembers, member)
			}
		}
		if len(welcomedMembers) == 0 {
			return nil
		}
		return a.replyHTML(msg.Chat.ID, msg.MessageID, buildMemberJoinWelcomeText(group, msg.Chat.Title, welcomedMembers...))
	}

	var deferredProfileDiff *db.GroupUserProfileDiff
	if msg.From != nil {
		if _, hasAddressVerification := parseMemberAddressVerificationRequest(text); hasAddressVerification {
			diff, err := a.recordGroupUserProfile(msg.Chat.ID, msg.Chat.Type, msg.From)
			if err != nil {
				log.Printf("同步群成员资料失败(chat=%d user=%d): %v", msg.Chat.ID, msg.From.ID, err)
			} else if diff.UsernameChanged || diff.NicknameChanged {
				deferredProfileDiff = &diff
			}
		} else if err := a.syncGroupUserProfile(msg.Chat.ID, msg.Chat.Type, msg.MessageID, msg.From); err != nil {
			log.Printf("同步群成员资料失败(chat=%d user=%d): %v", msg.Chat.ID, msg.From.ID, err)
		}
	}
	if blocked, err := a.inspectQRCodeAddressByImage(msg); err != nil {
		log.Printf("二维码地址解析失败(chat=%d msg=%d): %v", msg.Chat.ID, msg.MessageID, err)
	} else if blocked {
		return nil
	}

	if text == "" {
		if handled, err := a.handleInteractiveBroadcastDraftInput(msg, group, text); handled {
			return err
		}
		return nil
	}
	text = normalizeGroupTextForDispatch(text, a.currentBotUsername())
	if text == "" {
		return nil
	}
	if msg.IsCommand() {
		switch strings.ToLower(msg.Command()) {
		case "start":
			return nil
		case "menu", "help":
			return a.dispatchGroupMessage(ctx, msg, group, "菜单")
		case "panel":
			return a.dispatchGroupMessage(ctx, msg, group, "群控面板")
		case "bill":
			return a.dispatchGroupMessage(ctx, msg, group, "一键账单")
		case "rate":
			return a.dispatchGroupMessage(ctx, msg, group, "汇率对比")
		case "time", "worldtime":
			return a.dispatchGroupMessage(ctx, msg, group, "世界时间")
		}
	}

	dispatchErr := a.dispatchGroupMessage(ctx, msg, group, text)
	if deferredProfileDiff == nil {
		return dispatchErr
	}
	alertErr := a.sendUserProfileChangeAlert(msg.Chat.ID, msg.MessageID, msg.From.ID, *deferredProfileDiff)
	if dispatchErr != nil {
		return dispatchErr
	}
	return alertErr
}

func (a *App) handleEditedMessage(ctx context.Context, msg *tgbotapi.Message) error {
	if msg == nil {
		return nil
	}
	if err := a.syncGroupUserProfileFromMessage(msg); err != nil {
		return err
	}
	if a == nil || a.db == nil || msg.Chat == nil || msg.Chat.IsPrivate() {
		return nil
	}

	text := normalizeGroupTextForDispatch(messageTextForDispatch(msg), a.currentBotUsername())
	editedAddress, hasEditedAddress := extractTRC20AddressForVerification(text)
	record, known, err := a.db.GetAddressVerificationMessage(msg.Chat.ID, msg.MessageID)
	if err != nil {
		return err
	}
	if !known && !hasEditedAddress {
		return nil
	}

	originalKnown := known
	if !known && hasEditedAddress {
		if err := a.db.TrackAddressVerificationMessage(
			msg.Chat.ID,
			msg.MessageID,
			messageSenderUserID(msg),
			messageSenderUsername(msg),
			editedAddress,
			messageSentAtUTC(msg),
		); err != nil {
			return err
		}
		if record, known, err = a.db.GetAddressVerificationMessage(msg.Chat.ID, msg.MessageID); err != nil {
			return err
		}
	}
	if !known {
		return nil
	}

	editAt := messageEditAtUTC(msg)
	loc := a.loc
	if loc == nil {
		loc = time.Local
	}
	if err := a.db.MarkAddressVerificationMessageEdited(msg.Chat.ID, msg.MessageID, editedAddress, editAt); err != nil {
		return err
	}

	warning := buildAddressMessageEditedWarning(
		record.OriginalAddress,
		editedAddress,
		hasEditedAddress,
		originalKnown,
		editAt.In(loc).Format("2006-01-02 15:04:05"),
		buildTelegramUserMentionHTML(messageSenderUserID(msg), messageSenderUsername(msg)),
	)
	if err := a.replyHTML(msg.Chat.ID, msg.MessageID, warning); err != nil {
		return err
	}
	if !hasEditedAddress {
		return nil
	}
	group, err := a.db.EnsureGroup(msg.Chat.ID, msg.Chat.Title)
	if err != nil {
		return err
	}
	return a.handleTRC20AddressVerification(msg, group, editedAddress)
}

func messageSenderUserID(msg *tgbotapi.Message) int64 {
	if msg == nil || msg.From == nil {
		return 0
	}
	return msg.From.ID
}

func messageSenderUsername(msg *tgbotapi.Message) string {
	if msg == nil || msg.From == nil {
		return ""
	}
	return strings.TrimSpace(msg.From.UserName)
}

func messageSentAtUTC(msg *tgbotapi.Message) time.Time {
	if msg == nil || msg.Date == 0 {
		return time.Now().UTC()
	}
	return time.Unix(int64(msg.Date), 0).UTC()
}

func messageEditAtUTC(msg *tgbotapi.Message) time.Time {
	if msg == nil || msg.EditDate == 0 {
		return time.Now().UTC()
	}
	return time.Unix(int64(msg.EditDate), 0).UTC()
}

func buildAddressMessageEditedWarning(originalAddress, editedAddress string, hasEditedAddress, originalKnown bool, editTime, senderMention string) string {
	originalText := "未记录"
	if originalKnown {
		originalText = fmt.Sprintf("<code>%s</code>", html.EscapeString(strings.TrimSpace(originalAddress)))
	}
	editedText := "未检测到有效地址"
	if hasEditedAddress {
		editedText = fmt.Sprintf("<code>%s</code>", html.EscapeString(strings.TrimSpace(editedAddress)))
	}
	status := "‼️地址消息已被编辑，请重新核对后再转账。"
	if originalKnown && hasEditedAddress {
		if sameAddressForVerification(originalAddress, editedAddress) {
			status = "⚠️消息被编辑过，当前地址与原始地址一致，请仍然重新核对。"
		} else {
			status = "‼️地址内容已发生变化，请暂停转账并重新确认。"
		}
	}
	if originalKnown && !hasEditedAddress {
		status = "‼️原地址消息已被编辑，当前未检测到有效地址。"
	}
	return strings.Join([]string{
		"⚠️ 检测到地址消息已被编辑",
		"",
		fmt.Sprintf("原始地址：%s", originalText),
		fmt.Sprintf("编辑后地址：%s", editedText),
		fmt.Sprintf("编辑时间：%s", html.EscapeString(strings.TrimSpace(editTime))),
		fmt.Sprintf("发送人：%s", normalizeAddressVerificationSenderMention(senderMention)),
		"",
		status,
		"温馨提示：避免损失，请转账方与收款方多人确认，四位一体，是否一致。",
	}, "\n")
}

func messageTextForDispatch(msg *tgbotapi.Message) string {
	if msg == nil {
		return ""
	}
	text := strings.TrimSpace(msg.Text)
	if text != "" {
		return text
	}
	return strings.TrimSpace(msg.Caption)
}

func isShowGroupIDCommand(text string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(text), " ", ""))
	return normalized == "显示群id" || normalized == "群id"
}

func (a *App) handleGroupUpgradeMigration(msg *tgbotapi.Message) (bool, error) {
	if a == nil || a.db == nil || msg == nil || msg.Chat == nil {
		return false, nil
	}
	var (
		oldChatID int64
		newChatID int64
	)
	if msg.MigrateToChatID != 0 {
		oldChatID = msg.Chat.ID
		newChatID = msg.MigrateToChatID
	} else if msg.MigrateFromChatID != 0 {
		oldChatID = msg.MigrateFromChatID
		newChatID = msg.Chat.ID
	} else {
		return false, nil
	}
	if oldChatID == 0 || newChatID == 0 || oldChatID == newChatID {
		return true, nil
	}
	migrated, err := a.db.MigrateGroupChatData(oldChatID, newChatID, strings.TrimSpace(msg.Chat.Title))
	if err != nil {
		return true, fmt.Errorf("old_chat=%d new_chat=%d: %w", oldChatID, newChatID, err)
	}
	if migrated {
		log.Printf("检测到群升级并已迁移数据: old_chat=%d new_chat=%d title=%q", oldChatID, newChatID, strings.TrimSpace(msg.Chat.Title))
	}
	return true, nil
}

func buildBotInviteWelcomeMessages(groupTitle string) []string {
	title := strings.TrimSpace(groupTitle)
	if title == "" {
		title = "本群"
	}
	return []string{
		botInviteWelcomeFirstMessage,
		fmt.Sprintf("查记机器人\n欢迎加入【%s】", title),
	}
}

func buildMemberJoinWelcomeText(group db.GroupConfig, chatTitle string, members ...tgbotapi.User) string {
	title := strings.TrimSpace(group.Title)
	if title == "" {
		title = strings.TrimSpace(chatTitle)
	}
	if title == "" {
		title = "本群"
	}
	memberMentions := buildMemberJoinMentionsHTML(members)
	defaultWelcome := fmt.Sprintf("欢迎加入【%s】", html.EscapeString(title))
	welcome := strings.TrimSpace(group.WelcomeText)
	if welcome == "" || welcome == legacyDefaultJoinWelcome {
		if memberMentions != "" {
			return memberMentions + " " + defaultWelcome
		}
		return defaultWelcome
	}
	escapedWelcome := html.EscapeString(welcome)
	hasVariable := false
	for _, token := range memberJoinWelcomeVariableTokens {
		if strings.Contains(welcome, token) {
			hasVariable = true
			break
		}
	}
	escapedWelcome = strings.NewReplacer(
		"{group_name}", html.EscapeString(title),
		"{user_mention}", memberMentions,
		"{user_id}", buildMemberJoinUserIDs(members),
		"{first_name}", buildMemberJoinFirstNamesHTML(members),
	).Replace(escapedWelcome)
	if !hasVariable && memberMentions != "" {
		return memberMentions + " " + escapedWelcome
	}
	return escapedWelcome
}

var memberJoinWelcomeVariableTokens = []string{
	"{group_name}",
	"{user_mention}",
	"{user_id}",
	"{first_name}",
}

func buildMemberJoinUserIDs(members []tgbotapi.User) string {
	values := make([]string, 0, len(members))
	for _, member := range members {
		if member.ID != 0 {
			values = append(values, strconv.FormatInt(member.ID, 10))
		}
	}
	return strings.Join(values, "、")
}

func buildMemberJoinFirstNamesHTML(members []tgbotapi.User) string {
	values := make([]string, 0, len(members))
	for _, member := range members {
		if member.ID == 0 {
			continue
		}
		name := strings.TrimSpace(member.FirstName)
		if name == "" {
			name = strings.TrimSpace(member.LastName)
		}
		if name == "" {
			name = strings.TrimSpace(strings.TrimPrefix(member.UserName, "@"))
		}
		if name == "" {
			name = "用户" + strconv.FormatInt(member.ID, 10)
		}
		values = append(values, html.EscapeString(name))
	}
	return strings.Join(values, "、")
}

func buildMemberJoinMentionsHTML(members []tgbotapi.User) string {
	mentions := make([]string, 0, len(members))
	for _, member := range members {
		if member.ID == 0 {
			continue
		}
		mentions = append(mentions, buildTelegramUserMentionHTMLForUser(member))
	}
	if len(mentions) == 0 {
		return ""
	}
	return strings.Join(mentions, " ")
}

func buildTelegramUserMentionHTMLForUser(user tgbotapi.User) string {
	if user.ID == 0 {
		return "未知"
	}
	label := strings.TrimSpace(strings.TrimPrefix(user.UserName, "@"))
	if label != "" {
		label = "@" + label
	} else {
		label = strings.TrimSpace(strings.TrimSpace(user.FirstName) + " " + strings.TrimSpace(user.LastName))
		if label == "" {
			label = "用户" + strconv.FormatInt(user.ID, 10)
		}
	}
	return fmt.Sprintf(`<a href="tg://user?id=%d">%s</a>`, user.ID, html.EscapeString(label))
}

func (a *App) sendBotInviteWelcome(chatID int64, replyTo int, groupTitle string) error {
	if !a.acquireBotInviteWelcomeSlot(chatID) {
		return nil
	}
	for _, content := range buildBotInviteWelcomeMessages(groupTitle) {
		if err := a.reply(chatID, replyTo, content); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) replyBotInviteWelcomeOrRenewal(chatID int64, replyTo int, g db.GroupConfig, groupTitle string) error {
	if active, reason := a.checkGroupEntitlement(g); !active {
		return a.reply(chatID, replyTo, reason)
	}
	return a.sendBotInviteWelcome(chatID, replyTo, groupTitle)
}

func (a *App) clearBotInviteWelcomeSlot(chatID int64) {
	if a == nil || chatID == 0 {
		return
	}
	a.botInviteWelcomeMu.Lock()
	defer a.botInviteWelcomeMu.Unlock()
	if a.botInviteWelcomeAt == nil {
		return
	}
	delete(a.botInviteWelcomeAt, chatID)
}

func (a *App) acquireBotInviteWelcomeSlot(chatID int64) bool {
	if a == nil || chatID == 0 {
		return false
	}
	nowFn := a.now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn()
	a.botInviteWelcomeMu.Lock()
	defer a.botInviteWelcomeMu.Unlock()
	if a.botInviteWelcomeAt == nil {
		a.botInviteWelcomeAt = map[int64]time.Time{}
	}
	for id, ts := range a.botInviteWelcomeAt {
		if now.Sub(ts) > botInviteWelcomeDedupTTL {
			delete(a.botInviteWelcomeAt, id)
		}
	}
	if ts, ok := a.botInviteWelcomeAt[chatID]; ok && now.Sub(ts) <= botInviteWelcomeDedupTTL {
		return false
	}
	a.botInviteWelcomeAt[chatID] = now
	return true
}

func (a *App) acquireMemberJoinWelcomeSlot(chatID, userID int64) bool {
	if a == nil || chatID == 0 || userID == 0 {
		return false
	}
	nowFn := a.now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn()
	a.memberJoinWelcomeMu.Lock()
	defer a.memberJoinWelcomeMu.Unlock()
	if a.memberJoinWelcomeAt == nil {
		a.memberJoinWelcomeAt = map[memberJoinWelcomeKey]time.Time{}
	}
	for key, ts := range a.memberJoinWelcomeAt {
		if now.Sub(ts) > memberJoinWelcomeDedupTTL {
			delete(a.memberJoinWelcomeAt, key)
		}
	}
	key := memberJoinWelcomeKey{ChatID: chatID, UserID: userID}
	if ts, ok := a.memberJoinWelcomeAt[key]; ok && now.Sub(ts) <= memberJoinWelcomeDedupTTL {
		return false
	}
	a.memberJoinWelcomeAt[key] = now
	return true
}

func (a *App) isSelfNewChatMember(member tgbotapi.User) bool {
	if a == nil || a.api == nil {
		return false
	}
	if a.api.Self.ID != 0 && member.ID == a.api.Self.ID {
		return true
	}
	selfUsername := strings.TrimSpace(strings.TrimPrefix(a.api.Self.UserName, "@"))
	memberUsername := strings.TrimSpace(strings.TrimPrefix(member.UserName, "@"))
	if selfUsername == "" || memberUsername == "" {
		return false
	}
	return strings.EqualFold(selfUsername, memberUsername)
}

func isActiveGroupMemberStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "member", "administrator", "creator", "restricted":
		return true
	default:
		return false
	}
}

func (a *App) currentBotUsername() string {
	if a == nil || a.api == nil {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(a.api.Self.UserName, "@"))
}

func normalizeGroupTextForDispatch(text, botUsername string) string {
	normalized := strings.TrimSpace(text)
	username := strings.TrimSpace(strings.TrimPrefix(botUsername, "@"))
	if normalized == "" || username == "" {
		return normalized
	}
	mentionPrefix := "@" + username
	if len(normalized) < len(mentionPrefix) {
		return normalized
	}
	if !strings.EqualFold(normalized[:len(mentionPrefix)], mentionPrefix) {
		return normalized
	}
	rest := strings.TrimSpace(normalized[len(mentionPrefix):])
	rest = strings.TrimSpace(strings.TrimLeft(rest, ":：,， \t\r\n"))
	if rest == "" {
		return normalized
	}
	return rest
}

func isQuickAccountingDataCardCommand(text string) bool {
	normalized := strings.TrimSpace(text)
	if normalized == "" {
		return false
	}
	normalized = strings.Map(func(r rune) rune {
		switch r {
		case '＋', '﹢':
			return '+'
		case '　', '\u00A0':
			return ' '
		}
		if r >= '！' && r <= '～' {
			return r - 0xFEE0
		}
		return r
	}, normalized)
	switch strings.ToLower(strings.TrimSpace(normalized)) {
	case "+0", "+0u", "+0usdt":
		return true
	default:
		return false
	}
}

func (a *App) syncGroupUserProfileFromMessage(msg *tgbotapi.Message) error {
	if msg == nil || msg.Chat == nil || msg.From == nil {
		return nil
	}
	return a.syncGroupUserProfile(msg.Chat.ID, msg.Chat.Type, msg.MessageID, msg.From)
}

func (a *App) handleChatMemberUpdate(upd *tgbotapi.ChatMemberUpdated) error {
	if upd == nil || upd.NewChatMember.User == nil {
		return nil
	}
	if upd.Chat.Type != "" && upd.Chat.Type != "group" && upd.Chat.Type != "supergroup" {
		return nil
	}
	if !isActiveGroupMemberStatus(upd.NewChatMember.Status) {
		return nil
	}
	group, err := a.db.EnsureGroup(upd.Chat.ID, strings.TrimSpace(upd.Chat.Title))
	if err != nil {
		return err
	}
	if err := a.syncGroupUserProfile(upd.Chat.ID, upd.Chat.Type, 0, upd.NewChatMember.User); err != nil {
		return err
	}
	oldActive := isActiveGroupMemberStatus(upd.OldChatMember.Status)
	if oldActive || a.isSelfNewChatMember(*upd.NewChatMember.User) {
		return nil
	}
	if upd.NewChatMember.User.IsBot {
		return nil
	}
	log.Printf("收到成员入群状态更新: chat_id=%d user_id=%d old_status=%s new_status=%s", upd.Chat.ID, upd.NewChatMember.User.ID, upd.OldChatMember.Status, upd.NewChatMember.Status)
	if active, _ := a.checkGroupEntitlement(group); !active {
		return nil
	}
	if group.WelcomeEnabled != 1 {
		return nil
	}
	if !a.acquireMemberJoinWelcomeSlot(upd.Chat.ID, upd.NewChatMember.User.ID) {
		return nil
	}
	return a.replyHTML(upd.Chat.ID, 0, buildMemberJoinWelcomeText(group, upd.Chat.Title, *upd.NewChatMember.User))
}

func (a *App) handleMyChatMemberUpdate(upd *tgbotapi.ChatMemberUpdated) error {
	if upd == nil || upd.NewChatMember.User == nil {
		return nil
	}
	if upd.Chat.Type != "group" && upd.Chat.Type != "supergroup" {
		return nil
	}
	if !a.isSelfNewChatMember(*upd.NewChatMember.User) {
		return nil
	}

	newActive := isActiveGroupMemberStatus(upd.NewChatMember.Status)
	oldActive := isActiveGroupMemberStatus(upd.OldChatMember.Status)

	if !newActive {
		a.clearBotInviteWelcomeSlot(upd.Chat.ID)
		if !oldActive {
			return nil
		}
		if a.db == nil {
			return nil
		}
		if _, err := a.db.EnsureGroup(upd.Chat.ID, strings.TrimSpace(upd.Chat.Title)); err != nil {
			return err
		}
		return a.db.UpdateGroupFields(upd.Chat.ID, map[string]any{
			"bookkeeping_enabled":   0,
			"bookkeeping_cycle_key": "",
		})
	}
	if oldActive {
		if a.db != nil {
			_, _ = a.db.EnsureGroup(upd.Chat.ID, strings.TrimSpace(upd.Chat.Title))
		}
		return nil
	}

	groupTitle := strings.TrimSpace(upd.Chat.Title)
	group := db.GroupConfig{ChatID: upd.Chat.ID, Title: groupTitle}
	if a.db != nil {
		ensuredGroup, err := a.db.EnsureGroup(upd.Chat.ID, groupTitle)
		if err != nil {
			return err
		}
		if err := a.db.UpdateGroupFields(upd.Chat.ID, map[string]any{
			"bookkeeping_enabled":   0,
			"bookkeeping_cycle_key": "",
		}); err != nil {
			return err
		}
		group = ensuredGroup
		groupTitle = orDefault(strings.TrimSpace(group.Title), groupTitle)
	}
	group.Title = groupTitle
	return a.replyBotInviteWelcomeOrRenewal(upd.Chat.ID, 0, group, groupTitle)
}

func (a *App) syncGroupUserProfile(chatID int64, chatType string, replyTo int, user *tgbotapi.User) error {
	diff, err := a.recordGroupUserProfile(chatID, chatType, user)
	if err != nil {
		return err
	}
	return a.sendUserProfileChangeAlert(chatID, replyTo, userIDForProfileAlert(user), diff)
}

func (a *App) recordGroupUserProfile(chatID int64, chatType string, user *tgbotapi.User) (db.GroupUserProfileDiff, error) {
	if user == nil || chatID == 0 {
		return db.GroupUserProfileDiff{}, nil
	}
	if chatType != "" && chatType != "group" && chatType != "supergroup" {
		return db.GroupUserProfileDiff{}, nil
	}
	username := strings.ToLower(strings.TrimSpace(user.UserName))
	nickname := composeNickname(user.FirstName, user.LastName)
	return a.db.UpsertGroupUserWithDiff(chatID, user.ID, username, nickname)
}

func userIDForProfileAlert(user *tgbotapi.User) int64 {
	if user == nil {
		return 0
	}
	return user.ID
}

func (a *App) sendUserProfileChangeAlert(chatID int64, replyTo int, userID int64, diff db.GroupUserProfileDiff) error {
	if !diff.UsernameChanged && !diff.NicknameChanged {
		return nil
	}
	m := tgbotapi.NewMessage(chatID, buildUserProfileChangeAlertText(diff))
	m.ParseMode = telegramParseModeHTML
	m.ReplyMarkup = userProfileChangeAlertMarkup(userID)
	if replyTo > 0 {
		m.ReplyToMessageID = replyTo
	}
	return a.sendWithPriority(outboundPriorityNormalChat, m)
}

func buildUserProfileChangeAlertText(diff db.GroupUserProfileDiff) string {
	return strings.Join([]string{
		"👱 <b>用户修改用户名、昵称提示：</b>",
		"├原用户名：" + formatSecurityAlertUsername(diff.OldUsername),
		"├新用户名：" + formatSecurityAlertUsername(diff.NewUsername),
		"├原昵称：" + formatSecurityAlertNickname(diff.OldNickname),
		"├新昵称：" + formatSecurityAlertNickname(diff.NewNickname),
		"",
		"请注意规避风险,谨防假冒管理员",
	}, "\n")
}

func formatSecurityAlertUsername(v string) string {
	trimmed := strings.TrimSpace(strings.TrimPrefix(v, "@"))
	if trimmed == "" {
		return "无"
	}
	return "@" + html.EscapeString(trimmed)
}

func formatSecurityAlertNickname(v string) string {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" {
		return "无"
	}
	return html.EscapeString(trimmed)
}

func userProfileChangeAlertMarkup(targetUserID int64) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonURL("变更用户", fmt.Sprintf("tg://user?id=%d", targetUserID)),
		),
	)
}

func (a *App) warmupGroupAdministrators(ctx context.Context) error {
	if a == nil || a.db == nil || a.api == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	groups, err := a.db.ListEnabledGroups()
	if err != nil {
		return err
	}
	for _, group := range groups {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		admins, err := a.api.GetChatAdministrators(tgbotapi.ChatAdministratorsConfig{
			ChatConfig: tgbotapi.ChatConfig{ChatID: group.ChatID},
		})
		if err != nil {
			a.disableInvalidGroupOnBackgroundError(group.ChatID, err)
			log.Printf("管理员缓存预热失败(chat=%d): %v", group.ChatID, err)
			continue
		}
		for _, member := range admins {
			if member.User == nil {
				continue
			}
			if err := a.syncGroupUserProfile(group.ChatID, "", 0, member.User); err != nil {
				log.Printf("管理员缓存预热失败(chat=%d): %v", group.ChatID, err)
			}
		}
	}
	return nil
}

func (a *App) watchGroupUserProfiles(ctx context.Context) {
	ticker := time.NewTicker(groupUserWatchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.scanGroupUserProfiles(ctx)
		}
	}
}

func (a *App) scanGroupUserProfiles(ctx context.Context) {
	groups, err := a.db.ListEnabledGroups()
	if err != nil {
		log.Printf("群成员资料巡检失败: %v", err)
		return
	}
	for _, group := range groups {
		select {
		case <-ctx.Done():
			return
		default:
		}
		offset := a.groupUserScanOffset[group.ChatID]
		users, err := a.db.ListGroupUsersByOffset(group.ChatID, groupUserWatchBatchSize, offset)
		if err != nil {
			log.Printf("读取群成员缓存失败(chat=%d): %v", group.ChatID, err)
			continue
		}
		if len(users) == 0 && offset > 0 {
			offset = 0
			a.groupUserScanOffset[group.ChatID] = 0
			users, err = a.db.ListGroupUsersByOffset(group.ChatID, groupUserWatchBatchSize, 0)
		}
		if err != nil {
			log.Printf("读取群成员缓存失败(chat=%d): %v", group.ChatID, err)
			continue
		}
		for _, item := range users {
			select {
			case <-ctx.Done():
				return
			default:
			}
			member, err := a.api.GetChatMember(tgbotapi.GetChatMemberConfig{
				ChatConfigWithUser: tgbotapi.ChatConfigWithUser{
					ChatID: group.ChatID,
					UserID: item.UserID,
				},
			})
			if err != nil {
				a.disableInvalidGroupOnBackgroundError(group.ChatID, err)
				if !ignoreMemberLookupError(err) {
					log.Printf("群成员资料巡检查询失败(chat=%d user=%d): %v", group.ChatID, item.UserID, err)
				}
				continue
			}
			status := strings.ToLower(strings.TrimSpace(member.Status))
			if status == "left" || status == "kicked" {
				continue
			}
			if member.User == nil {
				continue
			}
			if err := a.syncGroupUserProfile(group.ChatID, "", 0, member.User); err != nil {
				log.Printf("群成员资料巡检更新失败(chat=%d user=%d): %v", group.ChatID, item.UserID, err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(groupUserWatchReqGap):
			}
		}
		if len(users) < groupUserWatchBatchSize {
			a.groupUserScanOffset[group.ChatID] = 0
		} else {
			a.groupUserScanOffset[group.ChatID] = offset + len(users)
		}
	}
}

func ignoreMemberLookupError(err error) bool {
	if err == nil {
		return false
	}
	v := strings.ToLower(err.Error())
	return strings.Contains(v, "user not found") ||
		strings.Contains(v, "participant_id_invalid") ||
		strings.Contains(v, "chat not found") ||
		strings.Contains(v, "bot was kicked") ||
		strings.Contains(v, "upgraded to a supergroup chat") ||
		strings.Contains(v, "have no rights")
}

func shouldDisableGroupOnBackgroundError(err error) bool {
	if err == nil {
		return false
	}
	v := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(v, "chat not found") ||
		strings.Contains(v, "bot was kicked") ||
		strings.Contains(v, "bot is not a member")
}

func (a *App) disableInvalidGroupOnBackgroundError(chatID int64, err error) {
	if a == nil || a.db == nil || chatID == 0 || !shouldDisableGroupOnBackgroundError(err) {
		return
	}
	if updateErr := a.db.UpdateGroupFields(chatID, map[string]any{
		"bookkeeping_enabled":   0,
		"bookkeeping_cycle_key": "",
	}); updateErr != nil {
		log.Printf("后台任务停用失效群失败(chat=%d): %v", chatID, updateErr)
	}
}

func (a *App) dispatchOutboundTask(ctx context.Context, priority outboundMessagePriority, run func(context.Context) error) error {
	_, err := a.dispatchOutboundTaskWithResult(ctx, priority, run)
	return err
}

func (a *App) dispatchOutboundTaskWithResult(ctx context.Context, priority outboundMessagePriority, run func(context.Context) error) (bool, error) {
	if run == nil {
		return false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if a == nil || a.outboundQueue == nil {
		return false, run(ctx)
	}
	err := a.outboundQueue.Submit(ctx, priority, run)
	if err == nil {
		return false, nil
	}
	metrics := a.currentOutboundMetrics()
	priorityLabel := outboundPriorityMetricLabel(priority)
	if errors.Is(err, errPriorityQueueBackpressure) {
		if priority == outboundPriorityBroadcastAd {
			metrics.ObserveOutboundDispatchFallback(priorityLabel, outboundMetricResultDroppedBackpressure)
			log.Printf("消息优先级队列触发背压：丢弃低优先级群发任务")
			return true, nil
		}
		metrics.ObserveOutboundDispatchFallback(priorityLabel, outboundMetricResultFallbackBackpressure)
		return false, run(ctx)
	}
	if errors.Is(err, errPriorityQueueClosed) {
		metrics.ObserveOutboundDispatchFallback(priorityLabel, outboundMetricResultFallbackClosed)
		return false, run(ctx)
	}
	return false, err
}

func (a *App) sendWithPriority(priority outboundMessagePriority, message tgbotapi.Chattable) error {
	_, err := a.sendWithPriorityOutcome(priority, message)
	return err
}

func (a *App) sendWithPriorityOutcome(priority outboundMessagePriority, message tgbotapi.Chattable) (bool, error) {
	return a.sendWithPriorityOutcomeAndTiming(priority, message, nil)
}

func (a *App) sendWithPriorityTiming(priority outboundMessagePriority, message tgbotapi.Chattable) (telegramRequestTimingSnapshot, error) {
	return a.sendWithPriorityTimingMode(priority, message, false)
}

func (a *App) sendWithPrioritySingleAttemptTiming(priority outboundMessagePriority, message tgbotapi.Chattable) (telegramRequestTimingSnapshot, error) {
	return a.sendWithPriorityTimingMode(priority, message, true)
}

func (a *App) sendWithPriorityTimingMode(priority outboundMessagePriority, message tgbotapi.Chattable, singleAttempt bool) (telegramRequestTimingSnapshot, error) {
	timing := newTelegramRequestTiming()
	_, err := a.sendWithPriorityOutcomeAndTimingMode(priority, message, timing, singleAttempt)
	return timing.snapshot(), err
}

func (a *App) sendWithPriorityOutcomeAndTiming(priority outboundMessagePriority, message tgbotapi.Chattable, timing *telegramRequestTiming) (bool, error) {
	return a.sendWithPriorityOutcomeAndTimingMode(priority, message, timing, false)
}

func (a *App) sendWithPriorityOutcomeAndTimingMode(priority outboundMessagePriority, message tgbotapi.Chattable, timing *telegramRequestTiming, singleAttempt bool) (bool, error) {
	if a == nil || a.api == nil || message == nil {
		return false, nil
	}
	dropped, err := a.dispatchOutboundTaskWithResult(context.Background(), priority, func(context.Context) error {
		api := a.api
		if timing != nil {
			baseClient := a.api.Client
			if singleAttempt {
				baseClient = singleAttemptTelegramHTTPClient(a.api.Client)
			}
			apiCopy := *a.api
			apiCopy.Client = &telegramTimingHTTPClient{
				base:   baseClient,
				timing: timing,
			}
			api = &apiCopy
		}
		_, err := api.Send(message)
		if err != nil {
			a.enqueueOutboundDeadLetter("send", priority, message, err)
		}
		return err
	})
	result := outboundSendResultMetricLabel(err)
	if dropped {
		result = outboundMetricResultDropped
	}
	metrics := a.currentOutboundMetrics()
	priorityLabel := outboundPriorityMetricLabel(priority)
	metrics.ObserveOutboundSend("send", priorityLabel, result)
	if retryAfter := telegramRetryAfterDuration(err); retryAfter > 0 {
		metrics.ObserveTelegramRetryAfter("send", priorityLabel, retryAfter)
	}
	return dropped, err
}

func (a *App) requestWithPriority(priority outboundMessagePriority, cfg tgbotapi.Chattable) error {
	if a == nil || a.api == nil || cfg == nil {
		return nil
	}
	dropped, err := a.dispatchOutboundTaskWithResult(context.Background(), priority, func(context.Context) error {
		_, err := a.api.Request(cfg)
		if err != nil {
			a.enqueueOutboundDeadLetter("request", priority, cfg, err)
		}
		return err
	})
	result := outboundSendResultMetricLabel(err)
	if dropped {
		result = outboundMetricResultDropped
	}
	metrics := a.currentOutboundMetrics()
	priorityLabel := outboundPriorityMetricLabel(priority)
	metrics.ObserveOutboundSend("request", priorityLabel, result)
	if retryAfter := telegramRetryAfterDuration(err); retryAfter > 0 {
		metrics.ObserveTelegramRetryAfter("request", priorityLabel, retryAfter)
	}
	return err
}

func (a *App) replyWithPriority(priority outboundMessagePriority, chatID int64, replyTo int, text string) error {
	m := tgbotapi.NewMessage(chatID, text)
	m.ReplyToMessageID = replyTo
	return a.sendWithPriority(priority, m)
}

func (a *App) reply(chatID int64, replyTo int, text string) error {
	return a.replyWithPriority(outboundPriorityNormalChat, chatID, replyTo, text)
}

func (a *App) replyWithMarkup(chatID int64, replyTo int, text string, markup tgbotapi.InlineKeyboardMarkup) error {
	m := tgbotapi.NewMessage(chatID, text)
	m.ReplyToMessageID = replyTo
	m.ReplyMarkup = markup
	return a.sendWithPriority(outboundPriorityNormalChat, m)
}

func (a *App) replyWithMarkupAndParseMode(chatID int64, replyTo int, text string, markup tgbotapi.InlineKeyboardMarkup, parseMode string) error {
	m := tgbotapi.NewMessage(chatID, text)
	m.ReplyToMessageID = replyTo
	m.ReplyMarkup = markup
	if strings.TrimSpace(parseMode) != "" {
		m.ParseMode = parseMode
	}
	return a.sendWithPriority(outboundPriorityNormalChat, m)
}

func (a *App) replyWithParseMode(chatID int64, replyTo int, text, parseMode string) error {
	m := tgbotapi.NewMessage(chatID, text)
	m.ReplyToMessageID = replyTo
	if strings.TrimSpace(parseMode) != "" {
		m.ParseMode = parseMode
	}
	return a.sendWithPriority(outboundPriorityNormalChat, m)
}

func (a *App) replyHTML(chatID int64, replyTo int, text string) error {
	return a.replyWithParseMode(chatID, replyTo, text, telegramParseModeHTML)
}

func (a *App) replyCalculatorResult(chatID int64, replyTo int, val float64) error {
	return a.replyHTML(
		chatID,
		replyTo,
		fmt.Sprintf("计算结果：<code>%s</code>", html.EscapeString(utils.FormatAmount(val))),
	)
}

func (a *App) renderTemplateMessage(rawTemplate string, data messageTemplateData) renderedTemplate {
	renderer := a.templateRenderer
	if renderer == nil {
		renderer = newTemplateRenderer()
	}
	rendered, err := renderer.RenderMessageTemplate(rawTemplate, data)
	if err != nil {
		log.Printf("消息模板渲染失败: %v", err)
		return renderedTemplate{Text: rawTemplate}
	}
	return rendered
}

func (a *App) replyPrivateMenu(chatID int64, replyTo int, userID int64) error {
	m := tgbotapi.NewMessage(chatID, privateIntroText())
	m.ReplyToMessageID = replyTo
	m.ReplyMarkup = privateReplyMenuMarkup()
	return a.sendWithPriority(outboundPriorityNormalChat, m)
}

func privateIntroText() string {
	return strings.Join([]string{
		"查记机器人",
		"私信机器人的界面与功能介绍",
		"请使用下方按钮进行操作。",
	}, "\n")
}

func privateReplyMenuMarkup() tgbotapi.ReplyKeyboardMarkup {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("联系管理员"),
			tgbotapi.NewKeyboardButton("获取ID"),
			tgbotapi.NewKeyboardButton("邀请进群"),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("入群教程"),
			tgbotapi.NewKeyboardButton("使用说明"),
			tgbotapi.NewKeyboardButton("到期时间"),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("购买使用"),
			tgbotapi.NewKeyboardButton("我的订单"),
			tgbotapi.NewKeyboardButton("人工客服"),
		),
	)
	kb.ResizeKeyboard = true
	kb.InputFieldPlaceholder = "请选择按钮，或直接输入命令"
	return kb
}

func (a *App) handlePrivateTrialApply(msg *tgbotapi.Message, userID int64) error {
	return a.reply(msg.Chat.ID, msg.MessageID, privateTrialApplyClosedText())
}

func privateTrialApplyClosedText() string {
	return "使用申请已关闭，请联系管理员开通。"
}

func (a *App) replyPrivateInviteGuide(chatID int64, replyTo int, userID int64) error {
	m := tgbotapi.NewMessage(chatID, privateTutorialOverviewText(strings.TrimSpace(a.cfg.TGWebAppURL) != "", a.privateBotUsername()))
	m.ReplyToMessageID = replyTo
	m.ReplyMarkup = a.privateTutorialMarkup(userID)
	return a.sendWithPriority(outboundPriorityNormalChat, m)
}

func privateTutorialOverviewText(webAppEnabled bool, botUsername string) string {
	lines := []string{
		"入群教程（业务链路版）",
		"方式A：一键邀请入群（推荐）",
		"方式B：手动拉群后初始化",
		"",
		"核心流程：进群 -> 设置操作人 -> 开始记账 -> 一键设置汇率 -> 群控面板/查单管理",
		"点击下方按钮可查看分步教程与排障指引。",
	}
	if strings.TrimSpace(botUsername) != "" {
		lines = append(lines, "当前机器人账号：@"+strings.TrimSpace(botUsername))
	}
	if webAppEnabled {
		lines = append(lines, "已开启 WebApp：可在教程中直接进入移动端群控执行。")
	}
	return strings.Join(lines, "\n")
}

func privateInviteMethodAText() string {
	return strings.Join([]string{
		"方式A：一键邀请入群（推荐）",
		"1. 私聊发送【邀请进群】或【入群教程】。",
		"2. 点击按钮【一键邀请进群】，选择目标群并确认。",
		"3. 建议给机器人管理员权限（删消息/封禁按需）。",
		"4. 进群后由群主发送【设置操作人】完成权限初始化。",
		"5. 发送【开始记账】+【一键设置汇率】后即可进入正式记账。",
	}, "\n")
}

func privateInviteMethodBText(botUsername string) string {
	username := strings.TrimSpace(botUsername)
	account := "@你的机器人用户名"
	if username != "" {
		account = "@" + username
	}
	return strings.Join([]string{
		"方式B：手动邀请入群",
		"1. 在目标群点击“添加成员/管理员”，搜索 " + account + " 并拉入群。",
		"2. 如无法搜索，先在私聊给机器人发 /start，再回群重试。",
		"3. 入群后发送【显示群ID】确认群已接入。",
		"4. 发送【设置操作人】并验证权限提示已消失。",
		"5. 发送【群控面板】确认按钮面板可正常弹出。",
	}, "\n")
}

func privateInviteInitText(webAppEnabled bool) string {
	lines := []string{
		"群内初始化清单（按顺序执行）",
		"1. 设置操作人",
		"2. 开始记账",
		"3. 一键设置汇率（或设置汇率7.2）",
		"4. 开关u显示（按需）",
		"5. 一键账单 / 查单列表验证账务链路",
		"6. 群控面板验证快捷操作链路",
	}
	if webAppEnabled {
		lines = append(lines, "7. 打开WebApp验证移动端命令路由与权限一致。")
	}
	return strings.Join(lines, "\n")
}

func privateInviteFAQText() string {
	return strings.Join([]string{
		"常见问题排查",
		"1. 按钮无响应：确认机器人在群内且未被限制发言。",
		"2. 提示权限不足：先执行【设置操作人】并确认操作者 ID 正确。",
		"3. 无法邀请：先私聊 /start 激活，再使用手动邀请流程。",
		"4. 查单为空：确认已【开始记账】且账期时间范围正确。",
		"5. WebApp 无法执行：检查 TG_WEBAPP_URL、签名时钟与用户权限。",
	}, "\n")
}

func (a *App) privateBotUsername() string {
	if a == nil || a.api == nil {
		return ""
	}
	return strings.TrimSpace(a.api.Self.UserName)
}

func (a *App) privateTutorialMarkup(userID int64) tgbotapi.InlineKeyboardMarkup {
	rows := make([][]tgbotapi.InlineKeyboardButton, 0, 7)
	username := a.privateBotUsername()
	inviteURL := ""
	if username != "" {
		inviteURL = fmt.Sprintf("https://t.me/%s?startgroup=true", username)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonURL("一键邀请进群", inviteURL),
			tgbotapi.NewInlineKeyboardButtonURL("机器人主页", fmt.Sprintf("https://t.me/%s", username)),
		))
	}

	methodRow := tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("方式A：一键邀请", privateCallbackPrefix+"tutorial_auto"),
		tgbotapi.NewInlineKeyboardButtonData("方式B：手动邀请", privateCallbackPrefix+"tutorial_manual"),
	)
	if inviteURL != "" {
		methodRow = tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonURL("方式A：一键邀请", inviteURL),
			tgbotapi.NewInlineKeyboardButtonData("方式B：手动邀请", privateCallbackPrefix+"tutorial_manual"),
		)
	}
	rows = append(rows,
		methodRow,
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("初始化清单", privateCallbackPrefix+"tutorial_init"),
			tgbotapi.NewInlineKeyboardButtonData("常见问题", privateCallbackPrefix+"tutorial_faq"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("群控入口", privateCallbackPrefix+"panel"),
			tgbotapi.NewInlineKeyboardButtonData("返回主菜单", privateCallbackPrefix+"menu"),
		),
	)
	if webURL := a.BuildWebAppURL(userID); webURL != "" {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonURL("打开WebApp", webURL),
		))
	}
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

type privatePurchasePlan struct {
	Action       string
	Name         string
	DurationDays int
	Amount       float64
	Currency     string
}

func privatePurchasePlans() []privatePurchasePlan {
	return []privatePurchasePlan{
		{Action: "buy_30", Name: "标准月卡", DurationDays: 30, Amount: 299, Currency: ""},
		{Action: "buy_90", Name: "标准季卡", DurationDays: 90, Amount: 799, Currency: ""},
		{Action: "buy_365", Name: "标准年卡", DurationDays: 365, Amount: 2999, Currency: ""},
	}
}

func findPrivatePurchasePlan(action string) (privatePurchasePlan, bool) {
	for _, item := range privatePurchasePlans() {
		if item.Action == strings.TrimSpace(action) {
			return item, true
		}
	}
	return privatePurchasePlan{}, false
}

func privatePurchaseGuideText() string {
	lines := []string{
		"购买使用",
		"1. 选择套餐后系统会生成订单并通知后台审核。",
		"2. 审核通过后自动更新你的到期时间。",
		"3. 如需人工对接，可直接点击“联系人工客服”。",
		"",
		"提示：你也可以发送“我的订单”查看最近申请状态。",
	}
	return strings.Join(lines, "\n")
}

func (a *App) privatePurchaseMarkup(userID int64) tgbotapi.InlineKeyboardMarkup {
	plans := privatePurchasePlans()
	rows := make([][]tgbotapi.InlineKeyboardButton, 0, 6)
	if len(plans) >= 2 {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("%s(%d天)", plans[0].Name, plans[0].DurationDays), privateCallbackPrefix+plans[0].Action),
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("%s(%d天)", plans[1].Name, plans[1].DurationDays), privateCallbackPrefix+plans[1].Action),
		))
	}
	if len(plans) >= 3 {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("%s(%d天)", plans[2].Name, plans[2].DurationDays), privateCallbackPrefix+plans[2].Action),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("查看我的订单", privateCallbackPrefix+"buy_orders"),
		tgbotapi.NewInlineKeyboardButtonData("返回主菜单", privateCallbackPrefix+"menu"),
	))
	if supportURL := normalizeSupportURL(a.cfg.SupportURL); supportURL != "" {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonURL("联系人工客服", supportURL),
		))
	}
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func (a *App) replyPrivatePurchaseMenu(chatID int64, replyTo int, userID int64) error {
	m := tgbotapi.NewMessage(chatID, privatePurchaseGuideText())
	m.ReplyToMessageID = replyTo
	m.ReplyMarkup = a.privatePurchaseMarkup(userID)
	return a.sendWithPriority(outboundPriorityNormalChat, m)
}

func (a *App) replyPrivatePurchaseOrders(chatID int64, replyTo int, userID int64) error {
	text := a.privatePurchaseOrdersText(userID)
	m := tgbotapi.NewMessage(chatID, text)
	m.ReplyToMessageID = replyTo
	m.ReplyMarkup = a.privatePurchaseMarkup(userID)
	return a.sendWithPriority(outboundPriorityNormalChat, m)
}

func (a *App) privatePurchaseOrdersText(userID int64) string {
	rows, err := a.db.ListPrivatePurchaseOrdersByUser(userID, 5)
	if err != nil {
		return "读取订单失败，请稍后重试。"
	}
	if len(rows) == 0 {
		return "暂无购买订单。点击“购买使用”可发起新的套餐申请。"
	}
	lines := []string{"最近购买订单："}
	for i, row := range rows {
		if i >= 5 {
			break
		}
		expireText := "待审核"
		if strings.TrimSpace(row.ExpiresAt) != "" {
			expireText = row.ExpiresAt
		}
		lines = append(lines, fmt.Sprintf(
			"#%d %s(%d天) %s 状态=%s 到期=%s",
			row.ID, row.PlanName, row.DurationDays, formatAmountWithDisplayCurrency(row.Amount, row.Currency), privatePurchaseStatusLabel(row.Status), expireText,
		))
	}
	return strings.Join(lines, "\n")
}

func normalizeDisplayCurrency(raw string) string {
	currency := strings.ToUpper(strings.TrimSpace(raw))
	if currency == "" || currency == "CNY" {
		return ""
	}
	return currency
}

func formatAmountWithDisplayCurrency(amount float64, currency string) string {
	text := fmt.Sprintf("%.2f", amount)
	displayCurrency := normalizeDisplayCurrency(currency)
	if displayCurrency == "" {
		return text
	}
	return text + " " + displayCurrency
}

func privatePurchaseStatusLabel(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "approved":
		return "已通过"
	case "rejected":
		return "已拒绝"
	case "canceled":
		return "已取消"
	default:
		return "待审核"
	}
}

func (a *App) createPrivatePurchaseOrder(userID int64, action string, user *tgbotapi.User) (string, error) {
	plan, ok := findPrivatePurchasePlan(action)
	if !ok {
		return "", errors.New("未知套餐")
	}
	orderID, err := a.db.AddPrivatePurchaseOrder(db.PrivatePurchaseOrderInput{
		UserID:       userID,
		PlanName:     plan.Name,
		DurationDays: plan.DurationDays,
		Amount:       plan.Amount,
		Currency:     plan.Currency,
		RequestNote:  "私聊按钮下单",
		Source:       "private_inline_buy",
	})
	if err != nil {
		return "", err
	}
	detail := fmt.Sprintf("订单#%d，套餐=%s，时长=%d天，金额=%s", orderID, plan.Name, plan.DurationDays, formatAmountWithDisplayCurrency(plan.Amount, plan.Currency))
	_ = a.notifySupportWithUser("购买申请", userID, user, detail)
	return strings.Join([]string{
		"购买申请已提交。",
		fmt.Sprintf("订单号：#%d", orderID),
		fmt.Sprintf("套餐：%s（%d天）", plan.Name, plan.DurationDays),
		fmt.Sprintf("金额：%s", formatAmountWithDisplayCurrency(plan.Amount, plan.Currency)),
		"状态：待审核（通过后自动更新到期时间）",
	}, "\n"), nil
}

func (a *App) notifySupportWithUser(scene string, userID int64, user *tgbotapi.User, detail string) error {
	if a.cfg.SupportChatID == 0 {
		return nil
	}
	username := ""
	nickname := "未知用户"
	if user != nil {
		username = strings.TrimSpace(user.UserName)
		nickname = composeNickname(user.FirstName, user.LastName)
	}
	lines := []string{
		fmt.Sprintf("【%s】", scene),
		fmt.Sprintf("用户ID：%d", userID),
		fmt.Sprintf("用户名：@%s", orDefault(username, "无")),
		fmt.Sprintf("昵称：%s", nickname),
	}
	if strings.TrimSpace(detail) != "" {
		lines = append(lines, "详情："+strings.TrimSpace(detail))
	}
	lines = append(lines, fmt.Sprintf("时间：%s", a.currentTimeInLocation().Format("2006-01-02 15:04:05")))
	m := tgbotapi.NewMessage(a.cfg.SupportChatID, strings.Join(lines, "\n"))
	return a.sendWithPriority(outboundPriorityErrorAlert, m)
}

func privateUsageGuideText() string {
	return strings.Join([]string{
		"查记机器人",
		"【群组管理】功能一览",
		"一键开始机器人",
		"一键设置汇率",
		"一键显示账单",
		"一键群发信息",
		"设置群标签",
		"设置群类型",
		"同类型群数据统计",
		"单独群数据统计",
		"",
		"【查单管理】功能一览",
		"查单列表-展示每个订单的详细数据与总数据",
		"延时查单-即先查后录数据",
		"建议：查单列表一日一清理，延时查单一卡一清理",
	}, "\n")
}

func (a *App) handlePrivateExpireQuery(msg *tgbotapi.Message, userID int64) error {
	profile, err := a.db.GetPrivateUserProfile(userID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(profile.ExpireAt) == "" {
		return a.reply(msg.Chat.ID, msg.MessageID, "当前无有效使用权限，请联系管理员开通。")
	}
	expireAt, ok := parseRFC3339(profile.ExpireAt)
	if !ok {
		return a.reply(msg.Chat.ID, msg.MessageID, "到期时间数据异常，请联系人工客服。")
	}
	now := time.Now().UTC()
	if !expireAt.After(now) {
		profile.TrialStatus = "expired"
		_ = a.db.UpsertPrivateUserProfile(profile)
		return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("你的使用权限已到期。\n到期时间：%s\n请联系人工客服续期。", expireAt.In(a.loc).Format("2006-01-02 15:04:05")))
	}
	return a.reply(msg.Chat.ID, msg.MessageID, strings.Join([]string{
		fmt.Sprintf("到期时间：%s", expireAt.In(a.loc).Format("2006-01-02 15:04:05")),
		fmt.Sprintf("剩余时长：%s", formatDuration(expireAt.Sub(now))),
	}, "\n"))
}

func (a *App) handlePrivateSupport(msg *tgbotapi.Message, userID int64) error {
	content := "人工客服请求"
	supportURL := normalizeSupportURL(a.cfg.SupportURL)
	hasSupportInbox := a.cfg.SupportChatID != 0
	if hasSupportInbox {
		if err := a.notifySupport("人工客服", msg, userID, content); err != nil {
			log.Printf("转发人工客服请求失败: %v", err)
		}
	}

	lines := []string{}
	if hasSupportInbox {
		lines = append(lines, "已提交人工客服请求。")
	}
	if supportURL != "" {
		if !hasSupportInbox {
			lines = append(lines, "当前未配置自动转发，已为你提供人工客服直达入口。")
		}
		lines = append(lines, "你也可以点击下方按钮直接联系人工客服。")
	}
	if len(lines) == 0 {
		lines = append(lines, "当前未配置客服通道，请联系管理员。")
	}
	m := tgbotapi.NewMessage(msg.Chat.ID, strings.Join(lines, "\n"))
	m.ReplyToMessageID = msg.MessageID
	if supportURL != "" {
		m.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonURL("联系人工客服", supportURL)),
		)
	}
	return a.sendWithPriority(outboundPriorityNormalChat, m)
}

func (a *App) notifySupport(scene string, msg *tgbotapi.Message, userID int64, detail string) error {
	if a.cfg.SupportChatID == 0 {
		return nil
	}
	username := ""
	nickname := ""
	if msg != nil && msg.From != nil {
		username = strings.TrimSpace(msg.From.UserName)
		nickname = composeNickname(msg.From.FirstName, msg.From.LastName)
	}
	lines := []string{
		fmt.Sprintf("【%s】", scene),
		fmt.Sprintf("用户ID：%d", userID),
		fmt.Sprintf("用户名：@%s", orDefault(username, "无")),
		fmt.Sprintf("昵称：%s", orDefault(nickname, "未知用户")),
		fmt.Sprintf("时间：%s", a.currentTimeInLocation().Format("2006-01-02 15:04:05")),
	}
	if strings.TrimSpace(detail) != "" {
		lines = append(lines, detail)
	}
	m := tgbotapi.NewMessage(a.cfg.SupportChatID, strings.Join(lines, "\n"))
	return a.sendWithPriority(outboundPriorityErrorAlert, m)
}

func (a *App) currentTimeInLocation() time.Time {
	now := time.Now()
	if a != nil && a.now != nil {
		now = a.now()
	}
	if a != nil && a.loc != nil {
		return now.In(a.loc)
	}
	return now
}

func (a *App) configuredWorkHours() (workHoursWindow, bool) {
	if a == nil {
		return workHoursWindow{}, false
	}
	return parseWorkHoursWindow(a.cfg.WorkHoursStart, a.cfg.WorkHoursEnd)
}

func (a *App) timezoneLabel() string {
	if a == nil {
		return "UTC"
	}
	if tz := strings.TrimSpace(a.cfg.Timezone); tz != "" {
		return tz
	}
	if a.loc != nil {
		return a.loc.String()
	}
	return "UTC"
}

func (a *App) notifyLargeTransaction(msg *tgbotapi.Message, parsed *utils.LedgerParsed, entryID int64, finalAmount, feeRate, rate float64) error {
	if a == nil || msg == nil || msg.Chat == nil || parsed == nil {
		return nil
	}
	entryTypeLabel := "下发"
	if parsed.EntryType == "deposit" {
		entryTypeLabel = "入款"
	}
	groupTitle := strings.TrimSpace(msg.Chat.Title)
	if groupTitle == "" {
		groupTitle = "未知"
	}
	originalAmountText := utils.FormatAmount(parsed.Amount)
	if currency := strings.TrimSpace(parsed.Currency); currency != "" {
		originalAmountText += " " + currency
	}
	lines := []string{
		fmt.Sprintf("记账ID：%d", entryID),
		fmt.Sprintf("群组ID：%d", msg.Chat.ID),
		fmt.Sprintf("群标题：%s", groupTitle),
		fmt.Sprintf("类型：%s", entryTypeLabel),
		fmt.Sprintf("原始金额：%s", originalAmountText),
		fmt.Sprintf("记账金额：%s", utils.FormatAmount(finalAmount)),
		fmt.Sprintf("阈值：%s", utils.FormatAmount(a.cfg.LargeTxnThreshold)),
		fmt.Sprintf("汇率：%s", utils.FormatAmount(rate)),
		fmt.Sprintf("费率：%s%%", utils.FormatAmount(feeRate)),
		fmt.Sprintf("备注：%s", orDefault(parsed.Note, "无")),
	}
	if track := utils.BuildMessageLink(msg.Chat.ID, msg.MessageID); track != "" {
		lines = append(lines, "跟踪："+track)
	}
	userID := int64(0)
	if msg.From != nil {
		userID = msg.From.ID
	}
	return a.notifySupport("大额交易预警", msg, userID, strings.Join(lines, "\n"))
}

func (a *App) notifyTimeAnomaly(msg *tgbotapi.Message, parsed *utils.LedgerParsed, entryID int64, finalAmount float64, occurredAt time.Time, workHours workHoursWindow) error {
	if a == nil || msg == nil || msg.Chat == nil || parsed == nil {
		return nil
	}
	entryTypeLabel := "下发"
	if parsed.EntryType == "deposit" {
		entryTypeLabel = "入款"
	}
	groupTitle := strings.TrimSpace(msg.Chat.Title)
	if groupTitle == "" {
		groupTitle = "未知"
	}
	originalAmountText := utils.FormatAmount(parsed.Amount)
	if currency := strings.TrimSpace(parsed.Currency); currency != "" {
		originalAmountText += " " + currency
	}
	lines := []string{
		fmt.Sprintf("记账ID：%d", entryID),
		fmt.Sprintf("群组ID：%d", msg.Chat.ID),
		fmt.Sprintf("群标题：%s", groupTitle),
		fmt.Sprintf("类型：%s", entryTypeLabel),
		fmt.Sprintf("交易时间：%s", occurredAt.Format("2006-01-02 15:04:05")),
		fmt.Sprintf("时区：%s", a.timezoneLabel()),
		fmt.Sprintf("工作时间：%s", workHours.label()),
		fmt.Sprintf("原始金额：%s", originalAmountText),
		fmt.Sprintf("记账金额：%s", utils.FormatAmount(finalAmount)),
		fmt.Sprintf("备注：%s", orDefault(parsed.Note, "无")),
	}
	if track := utils.BuildMessageLink(msg.Chat.ID, msg.MessageID); track != "" {
		lines = append(lines, "跟踪："+track)
	}
	userID := int64(0)
	if msg.From != nil {
		userID = msg.From.ID
	}
	return a.notifySupport("异常时间交易预警", msg, userID, strings.Join(lines, "\n"))
}

func (a *App) applyRateSetting(msg *tgbotapi.Message, g db.GroupConfig, rate float64, payout bool) error {
	if payout {
		_ = a.db.UpdateGroupFields(msg.Chat.ID, map[string]any{"payout_rate": rate})
	} else {
		_ = a.db.UpdateGroupFields(msg.Chat.ID, map[string]any{"custom_rate": rate, "use_realtime_rate": 0})
	}

	rateLabel := "入款汇率"
	if payout {
		rateLabel = "下发汇率"
	}
	replyText := fmt.Sprintf("%s已设置为 %s", rateLabel, utils.FormatAmount(rate))
	if warning := a.emitRateDeviationAlert(msg, g, rate, payout); warning != "" {
		replyText += "\n" + warning
	}
	return a.reply(msg.Chat.ID, msg.MessageID, replyText)
}

func (a *App) emitRateDeviationAlert(msg *tgbotapi.Message, g db.GroupConfig, configuredRate float64, payout bool) string {
	if !a.businessRiskControlsEnabled() {
		return ""
	}
	marketRate, deviation, marketSnapshot, ok := a.resolveRateDeviationSignal(configuredRate)
	if !ok || deviation < rateDeviationThreshold {
		return ""
	}

	rateType := "deposit"
	rateLabel := "入款汇率"
	if payout {
		rateType = "payout"
		rateLabel = "下发汇率"
	}

	userID := int64(0)
	if msg != nil && msg.From != nil {
		userID = msg.From.ID
	}
	source := strings.TrimSpace(g.RateSource)
	if source == "" {
		source = defaultRateQueryProviderSource()
	}
	detail := fmt.Sprintf(
		"rate_type=%s configured_rate=%.8f market_rate=%.8f deviation=%.4f threshold=%.4f source=%s snapshot=%s",
		rateType,
		configuredRate,
		marketRate,
		deviation,
		rateDeviationThreshold,
		source,
		marketSnapshot,
	)
	if a != nil && a.db != nil && msg != nil && msg.Chat != nil {
		_ = a.db.AddRiskEvent(msg.Chat.ID, msg.MessageID, userID, rateDeviationEventType, rateDeviationSeverity, detail)
	}
	if err := a.notifyOwnerRateDeviation(g.OwnerUserID, msg, rateLabel, configuredRate, marketRate, deviation, marketSnapshot); err != nil {
		log.Printf("发送汇率偏离告警失败: %v", err)
	}
	return fmt.Sprintf(
		"⚠️ 汇率偏离预警：%s=%s，市场价约 %s，偏离 %s（阈值 %s），已通知权限人复核。",
		rateLabel,
		utils.FormatAmount(configuredRate),
		utils.FormatAmount(marketRate),
		formatDeviationPercent(deviation),
		formatDeviationPercent(rateDeviationThreshold),
	)
}

func (a *App) resolveRateDeviationSignal(configuredRate float64) (float64, float64, string, bool) {
	if a == nil || a.exchange == nil || configuredRate <= 0 {
		return 0, 0, "", false
	}
	rows, err := a.exchange.CompareRates("all", "CNY")
	if err != nil || len(rows) == 0 {
		return 0, 0, "", false
	}
	prices := make([]string, 0, len(rows))
	marketSamples := make([]float64, 0, len(rows))
	for _, row := range rows {
		if row.Price <= 0 {
			continue
		}
		provider := strings.ToUpper(strings.TrimSpace(row.Provider))
		if provider == "" {
			provider = "UNKNOWN"
		}
		prices = append(prices, fmt.Sprintf("%s=%s", provider, utils.FormatAmount(row.Price)))
		marketSamples = append(marketSamples, row.Price)
	}
	if len(marketSamples) == 0 {
		return 0, 0, "", false
	}
	sort.Strings(prices)
	marketRate := services.ComputeFairPrice(marketSamples)
	if marketRate <= 0 {
		return 0, 0, "", false
	}
	deviation := math.Abs(configuredRate-marketRate) / marketRate
	return marketRate, deviation, strings.Join(prices, ", "), true
}

func (a *App) notifyOwnerRateDeviation(ownerUserID int64, msg *tgbotapi.Message, rateLabel string, configuredRate, marketRate, deviation float64, marketSnapshot string) error {
	if ownerUserID == 0 || msg == nil || msg.Chat == nil {
		return nil
	}
	lines := []string{
		"【汇率偏离告警】",
		fmt.Sprintf("群组ID：%d", msg.Chat.ID),
		fmt.Sprintf("群标题：%s", orDefault(strings.TrimSpace(msg.Chat.Title), "未知")),
		fmt.Sprintf("设置项：%s", rateLabel),
		fmt.Sprintf("设置汇率：%s", utils.FormatAmount(configuredRate)),
		fmt.Sprintf("市场汇率：%s", utils.FormatAmount(marketRate)),
		fmt.Sprintf("偏离比例：%s（阈值：%s）", formatDeviationPercent(deviation), formatDeviationPercent(rateDeviationThreshold)),
	}
	if strings.TrimSpace(marketSnapshot) != "" {
		lines = append(lines, "市场快照："+marketSnapshot)
	}
	if msg.From != nil {
		lines = append(lines, fmt.Sprintf("执行人：%s", permissionAlertOperatorLabel(msg.From)))
	}
	lines = append(lines, fmt.Sprintf("时间：%s", a.currentTimeInLocation().Format("2006-01-02 15:04:05")))

	alert := tgbotapi.NewMessage(ownerUserID, strings.Join(lines, "\n"))
	return a.sendWithPriority(outboundPriorityErrorAlert, alert)
}

func formatDeviationPercent(v float64) string {
	return fmt.Sprintf("%.2f%%", v*100)
}

func normalizeSupportURL(v string) string {
	out := strings.TrimSpace(v)
	if out == "" {
		return ""
	}
	if strings.HasPrefix(out, "@") {
		return "https://t.me/" + strings.TrimPrefix(out, "@")
	}
	if strings.HasPrefix(out, "https://") || strings.HasPrefix(out, "http://") {
		return out
	}
	if strings.HasPrefix(out, "t.me/") {
		return "https://" + out
	}
	return "https://t.me/" + strings.TrimPrefix(out, "/")
}

func parseRFC3339(v string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(v))
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func privatePanelGuideText(webAppEnabled bool) string {
	lines := []string{
		"群控入口说明",
		"1. 群里发送【群控面板】可打开 Inline 快捷控制。",
		"2. 面板支持：一键开始、一键设置汇率、一键账单、一键群发、群标签/群类型、统计、查单、定时、自动化。",
	}
	if webAppEnabled {
		lines = append(lines, "3. 点击“打开WebApp”可在移动端执行完整命令路由（权限受控）。")
	}
	return strings.Join(lines, "\n")
}

func (a *App) privateMenuMarkup(userID int64) tgbotapi.InlineKeyboardMarkup {
	rows := [][]tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("功能总览", privateCallbackPrefix+"overview"),
			tgbotapi.NewInlineKeyboardButtonData("群组管理", privateCallbackPrefix+"group"),
			tgbotapi.NewInlineKeyboardButtonData("查单管理", privateCallbackPrefix+"order"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("入群教程", privateCallbackPrefix+"tutorial"),
			tgbotapi.NewInlineKeyboardButtonData("购买使用", privateCallbackPrefix+"buy"),
			tgbotapi.NewInlineKeyboardButtonData("命令示例", privateCallbackPrefix+"example"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("更新日志", privateCallbackPrefix+"update"),
			tgbotapi.NewInlineKeyboardButtonData("世界时间示例", privateCallbackPrefix+"world"),
			tgbotapi.NewInlineKeyboardButtonData("群控入口", privateCallbackPrefix+"panel"),
			tgbotapi.NewInlineKeyboardButtonData("刷新菜单", privateCallbackPrefix+"menu"),
		),
	}
	if webURL := a.BuildWebAppURL(userID); webURL != "" {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonURL("打开WebApp", webURL),
		))
	}
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func privateFeatureOverviewText() string {
	return strings.Join([]string{
		"功能总览",
		"1. 账务：记账、备注、USDT换算、负费率、记账撤回",
		"2. 汇率：实时汇率、汇率对比、一键设置汇率、交易所切换",
		"3. 查询：自助查单、查卡、查联行号、TRC20记录、距离、世界时间",
		"4. 群管：操作人、担保规则、上课下课、进群欢迎、显示群ID",
		"5. 群发：一键群发、指定@群发、定时群发、转发群发",
		"6. 自动化：自定义内容、自动回复、关键词转发、记账信息跟踪",
	}, "\n")
}

func privateGroupManageText() string {
	return strings.Join([]string{
		"查记机器人",
		"【群组管理】功能一览",
		"一键开始机器人",
		"一键设置汇率",
		"一键显示账单",
		"一键群发信息",
		"设置群标签",
		"设置群类型",
		"批量设置群标签",
		"批量设置群类型",
		"同类型群数据统计",
		"单独群数据统计",
	}, "\n")
}

func privateOrderManageText() string {
	return strings.Join([]string{
		"【查单管理】功能一览",
		"查单列表-展示每个订单的详细数据与总数据",
		"延时查单-即先查后录数据",
		"建议：",
		"查单列表一日一清理",
		"延时查单一卡一清理",
	}, "\n")
}

func privateExampleText() string {
	return strings.Join([]string{
		"常用命令示例：",
		"- 入款：+100#备注信息",
		"- 下发：-100u#备注信息",
		"- 撤回：撤回入款 / 撤回下发",
		"- 汇率：z0 / lk / lz / lw / 汇率对比",
		"- 地址：设置下发地址 Txxxx / 验证地址 Txxxx / 查询地址",
		"- 群发：一键群发 测试消息 / 指定@群发 合作伙伴 开会",
		"- 定时：定时群发 2026-02-16 09:00 内容",
	}, "\n")
}

func privateUpdateText() string {
	return strings.Join([]string{
		"近期更新摘要：",
		"2025/04/24：机器人退出群组、开启/关闭换算、显示入款/下发信息、显示群ID、显示昵称。",
		"2025/09/01：优化自定义内容符号检测、定时群发可编辑重发、记账信息跟踪、自动回复增强。",
		"2025/12/12：转发群发、全局操作人、世界时间、币安交易所、关键词转发。",
	}, "\n")
}

func groupHelpText() string {
	return strings.Join([]string{
		"群内命令总览：",
		"记账：+100 / -100 / +100u#备注 / 记账撤回 / 一键账单 / 导出账单",
		"汇率：设置汇率7.2 / 设置下发汇率7.2 / 一键设置汇率 / 汇率对比 / z0",
		"查询：自助查单 关键词+金额 / 自助查单 2026-02-01-2026-02-07 关键词+金额 / 查卡+卡号 / 查联行号+卡号 / 查询TRC20 地址 / 查询链上 地址",
		"联行号维护：设置联行号+卡号+联行号+支行名 / 删除联行号+卡号",
		"地址：设置下发地址 / 设置专属地址 / 下发地址 / 专属地址 / 查询地址 / 验证地址",
		"群管：设置操作人 / 设置全局操作人 / 设置全局权限人 / 显示全局权限人 / 转移本群权限人 / 设置担保规则 / 上课 / 下课 / 显示群ID / 群组管理 / 批量设置群标签 / 批量设置群类型",
		"统计：同类型群数据统计 / 单独群数据统计",
		"广播：一键群发 / 指定@群发 / 定时群发 / 转发",
		"自动化：设置自动回复 / 删除自动回复 / 自动回复列表 / 开启自动回复 / 关闭自动回复 / 设置关键词转发 / 关键词转发列表",
		"界面：群控面板（Inline 快捷操作）",
	}, "\n")
}

func composeNickname(first, last string) string {
	out := strings.TrimSpace(strings.TrimSpace(first) + " " + strings.TrimSpace(last))
	if out == "" {
		return "未知用户"
	}
	return out
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func (a *App) BuildWebAppURL(userID int64) string {
	base := strings.TrimSpace(a.cfg.TGWebAppURL)
	if base == "" || userID == 0 {
		return ""
	}
	if strings.TrimSpace(a.cfg.TGWebAppSecret) == "" {
		log.Printf("WebApp 未配置 TG_WEBAPP_SECRET，拒绝生成入口 URL")
		return ""
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return ""
	}
	now := a.currentTime()
	exp := now.Add(webAppAuthTTL).Unix()
	sig := a.webAppSign(userID, exp)
	if err := a.persistWebAppSession(userID, exp, sig, now); err != nil {
		log.Printf("创建 WebApp 分布式会话失败: %v", err)
		return ""
	}
	query := parsed.Query()
	query.Set("uid", strconv.FormatInt(userID, 10))
	query.Set("exp", strconv.FormatInt(exp, 10))
	query.Set("sig", sig)
	query.Set("v", webAppURLVersionTag)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func (a *App) BuildWebAppLooklookURL(userID, chatID int64, dayCutTime string) string {
	if a == nil || userID == 0 || chatID == 0 {
		return ""
	}
	base := strings.TrimSpace(a.BuildWebAppURL(userID))
	if base == "" {
		return ""
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return ""
	}
	parsed.Path = resolveWebAppLooklookPath(parsed.Path)

	loc := a.loc
	if loc == nil {
		loaded, loadErr := time.LoadLocation(strings.TrimSpace(a.cfg.Timezone))
		if loadErr != nil || loaded == nil {
			loc = time.FixedZone("CST", 8*3600)
		} else {
			loc = loaded
		}
	}
	nowAt := a.currentTimeInLocation().In(loc)
	beginAt := nowAt.Add(-24 * time.Hour)
	if startUTC := strings.TrimSpace(utils.GetDayStartISO(dayCutTime, a.cfg.Timezone)); startUTC != "" {
		if parsedStart, parseErr := time.ParseInLocation("2006-01-02T15:04:05", startUTC, time.UTC); parseErr == nil {
			beginAt = parsedStart.In(loc)
		}
	}
	beginText := beginAt.Format("2006-01-02 15:04:05")
	endText := nowAt.Format("2006-01-02 15:04:05")

	query := parsed.Query()
	query.Set("type", "bjr")
	query.Set("groupId", strconv.FormatInt(chatID, 10))
	query.Set("begintime", beginText)
	query.Set("endtime", endText)
	query.Set("daterange", beginText+" - "+endText)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func resolveWebAppLooklookPath(rawPath string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(rawPath), "/")
	if strings.HasSuffix(trimmed, "/tg/webapp") {
		return strings.TrimSuffix(trimmed, "/tg/webapp") + "/tg/webapp/looklook.php"
	}
	return "/tg/webapp/looklook.php"
}

func (a *App) VerifyWebAppAuth(userID, exp int64, sig string) error {
	if strings.TrimSpace(a.cfg.TGWebAppSecret) == "" {
		return errors.New("WebApp 签名密钥未配置")
	}
	if userID == 0 || exp == 0 || strings.TrimSpace(sig) == "" {
		return errors.New("鉴权参数不完整")
	}
	now := time.Now().Unix()
	if exp < now-30 {
		return errors.New("鉴权已过期")
	}
	maxFuture := now + int64(webAppAuthTTL.Seconds()) + 60
	if exp > maxFuture {
		return errors.New("鉴权时间无效")
	}
	expected := a.webAppSign(userID, exp)
	if !hmac.Equal([]byte(expected), []byte(strings.TrimSpace(sig))) {
		return errors.New("鉴权签名无效")
	}
	if err := a.verifyWebAppSession(userID, exp, sig, now); err != nil {
		return err
	}
	return nil
}

func (a *App) currentTime() time.Time {
	if a != nil && a.now != nil {
		return a.now()
	}
	return time.Now()
}

func (a *App) persistWebAppSession(userID, exp int64, sig string, now time.Time) error {
	store := a.webAppSessionStore()
	if !store.Enabled() {
		return nil
	}
	sessionID := buildWebAppSessionID(userID, exp, sig)
	if sessionID == "" {
		return errors.New("会话参数无效")
	}
	session := DistributedSession{
		SessionID: sessionID,
		UserID:    userID,
		LoggedIn:  true,
		Context: map[string]string{
			"scope":    "tg_webapp",
			"timezone": a.timezoneLabel(),
		},
		IssuedAt:   now.Unix(),
		ExpiresAt:  exp,
		LastSeenAt: now.Unix(),
	}
	return store.Save(context.Background(), session)
}

func (a *App) verifyWebAppSession(userID, exp int64, sig string, now int64) error {
	store := a.webAppSessionStore()
	if !store.Enabled() {
		return nil
	}
	sessionID := buildWebAppSessionID(userID, exp, sig)
	session, ok, err := store.Get(context.Background(), sessionID)
	if err != nil {
		return fmt.Errorf("登录会话校验失败: %w", err)
	}
	if !ok {
		return errors.New("登录会话不存在或已过期")
	}
	if !session.LoggedIn || session.UserID != userID {
		return errors.New("登录态无效")
	}
	if session.ExpiresAt > 0 && session.ExpiresAt < now-30 {
		_ = store.Delete(context.Background(), sessionID)
		return errors.New("登录会话已过期")
	}
	if session.Context == nil {
		session.Context = map[string]string{}
	}
	session.Context["scope"] = "tg_webapp"
	session.LastSeenAt = now
	if err := store.Save(context.Background(), session); err != nil {
		return fmt.Errorf("刷新登录会话失败: %w", err)
	}
	return nil
}

func (a *App) webAppSessionStore() DistributedSessionStore {
	if a == nil || a.sessionStore == nil {
		return noopDistributedSessionStore{}
	}
	return a.sessionStore
}

func buildWebAppSessionID(userID, exp int64, sig string) string {
	trimmedSig := strings.TrimSpace(sig)
	if userID == 0 || exp == 0 || trimmedSig == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s", userID, exp, trimmedSig)))
	return hex.EncodeToString(sum[:])
}

func (a *App) webAppSign(userID, exp int64) string {
	secret := strings.TrimSpace(a.webAppSecret())
	if secret == "" {
		return ""
	}
	payload := fmt.Sprintf("%d:%d", userID, exp)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func (a *App) webAppSecret() string {
	return strings.TrimSpace(a.cfg.TGWebAppSecret)
}

func (a *App) handleCallbackQuery(ctx context.Context, cq *tgbotapi.CallbackQuery) error {
	if cq == nil {
		return nil
	}
	data := strings.TrimSpace(cq.Data)
	switch {
	case strings.HasPrefix(data, privateGroupControlCallbackPrefix):
		return a.handlePrivateGroupControlCallback(ctx, cq)
	case strings.HasPrefix(data, privateCallbackPrefix):
		return a.handlePrivateCallback(cq)
	case strings.HasPrefix(data, groupPanelCallbackPrefix):
		return a.handleGroupPanelCallback(ctx, cq)
	case strings.HasPrefix(data, broadcastTagCallbackPrefix):
		return a.handleInteractiveBroadcastTagCallback(ctx, cq)
	case strings.HasPrefix(data, batchTagCallbackPrefix):
		return a.handleBatchTagCallback(ctx, cq)
	case strings.HasPrefix(data, batchTypeCallbackPrefix):
		return a.handleBatchTypeCallback(ctx, cq)
	case strings.HasPrefix(data, ledgerCardCallbackPrefix):
		return a.handleLedgerCardCallback(ctx, cq)
	case strings.HasPrefix(data, trc20QueryCallbackPrefix):
		return a.handleTRC20QueryCallback(ctx, cq)
	case strings.HasPrefix(data, rateQueryCallbackPrefix):
		return a.handleRateQueryCallback(ctx, cq)
	default:
		a.answerCallback(cq.ID, "未知操作", false)
		return nil
	}
}

func (a *App) handlePrivateCallback(cq *tgbotapi.CallbackQuery) error {
	if cq == nil || cq.Message == nil || cq.Message.Chat == nil || !cq.Message.Chat.IsPrivate() {
		a.answerCallback(cq.ID, "请在私聊中使用", true)
		return nil
	}
	userID := cq.Message.Chat.ID
	if cq.From != nil {
		userID = cq.From.ID
	}
	action := strings.TrimPrefix(strings.TrimSpace(cq.Data), privateCallbackPrefix)
	var text string
	markup := a.privateMenuMarkup(userID)
	switch action {
	case "menu":
		text = privateIntroText()
	case "overview":
		text = privateFeatureOverviewText()
	case "group":
		text = privateGroupManageText()
	case "order":
		text = privateOrderManageText()
	case "example":
		text = privateExampleText()
	case "update":
		text = privateUpdateText()
	case "world":
		text = "示例：北京时间、巴西时间、德国时间、美国中部时间、日本时间"
	case "panel":
		return a.editPrivateGroupControlPanel(cq, userID)
	case "buy":
		text = privatePurchaseGuideText()
		markup = a.privatePurchaseMarkup(userID)
	case "buy_30", "buy_90", "buy_365":
		createdText, createErr := a.createPrivatePurchaseOrder(userID, action, cq.From)
		if createErr != nil {
			text = "购买申请失败：" + createErr.Error()
		} else {
			text = createdText
		}
		markup = a.privatePurchaseMarkup(userID)
	case "buy_orders":
		text = a.privatePurchaseOrdersText(userID)
		markup = a.privatePurchaseMarkup(userID)
	case "tutorial":
		text = privateTutorialOverviewText(strings.TrimSpace(a.cfg.TGWebAppURL) != "", a.privateBotUsername())
		markup = a.privateTutorialMarkup(userID)
	case "tutorial_auto":
		text = privateInviteMethodAText()
		markup = a.privateTutorialMarkup(userID)
	case "tutorial_manual":
		text = privateInviteMethodBText(a.privateBotUsername())
		markup = a.privateTutorialMarkup(userID)
	case "tutorial_init":
		text = privateInviteInitText(strings.TrimSpace(a.cfg.TGWebAppURL) != "")
		markup = a.privateTutorialMarkup(userID)
	case "tutorial_faq":
		text = privateInviteFAQText()
		markup = a.privateTutorialMarkup(userID)
	default:
		a.answerCallback(cq.ID, "未知菜单项", false)
		return nil
	}
	edit := tgbotapi.NewEditMessageTextAndMarkup(cq.Message.Chat.ID, cq.Message.MessageID, text, markup)
	if err := a.sendWithPriority(outboundPriorityNormalChat, edit); err != nil && !isMessageNotModified(err) {
		return err
	}
	a.answerCallback(cq.ID, "已更新", false)
	return nil
}

func (a *App) handleGroupPanelCallback(ctx context.Context, cq *tgbotapi.CallbackQuery) error {
	if cq == nil || cq.Message == nil || cq.Message.Chat == nil || cq.From == nil {
		a.answerCallback(cq.ID, "请求无效", true)
		return nil
	}
	chat := cq.Message.Chat
	if chat.IsPrivate() {
		a.answerCallback(cq.ID, "请在群组中使用群控面板", true)
		return nil
	}
	group, err := a.db.EnsureGroup(chat.ID, chat.Title)
	if err != nil {
		a.answerCallback(cq.ID, "群组初始化失败", true)
		return err
	}
	action := strings.TrimPrefix(strings.TrimSpace(cq.Data), groupPanelCallbackPrefix)
	if !a.db.IsPrivileged(chat.ID, cq.From.ID) {
		a.answerCallback(cq.ID, "权限不足：仅权限人/操作人可操作", true)
		return nil
	}
	if active, reason := a.checkGroupEntitlement(group); !active {
		_ = a.reply(chat.ID, cq.Message.MessageID, reason)
		a.answerCallback(cq.ID, "当前群功能已到期，请先续期", true)
		return nil
	}
	command := ""
	ack := "已刷新"
	switch action {
	case "panel":
		command = ""
	case "start":
		command = "开始记账"
		ack = "已执行开始记账"
	case "stop":
		command = "停止记账"
		ack = "已执行停止记账"
	case "rate_onekey":
		command = "一键设置汇率"
		ack = "正在一键设置汇率"
	case "bill":
		command = "一键账单"
		ack = "正在生成账单"
	case "broadcast":
		command = "一键群发"
		ack = "请按提示输入群发内容"
	case "export":
		command = "导出账单"
		ack = "正在导出 Excel/PDF"
	case "order_list":
		command = "查单列表"
		ack = "正在查询查单列表"
	case "tag_set":
		command = "设置群标签"
		ack = "请按提示设置群标签"
	case "type_set":
		command = "设置群类型"
		ack = "请按提示设置群类型"
	case "batch_tag_set":
		command = "批量设置群标签"
		ack = "请按提示批量设置群标签"
	case "batch_type_set":
		command = "批量设置群类型"
		ack = "请按提示批量设置群类型"
	case "closing_enable":
		command = "开启下课提醒"
		ack = "已开启下课提醒"
	case "closing_disable":
		command = "关闭下课提醒"
		ack = "已关闭下课提醒"
	case "closing_time":
		command = "设置下课时间"
		ack = "请发送设置下课时间04:00"
	case "closing_query":
		command = "查询下课提醒"
		ack = "正在查询下课提醒"
	case "type_stats":
		command = "同类型群数据统计"
		ack = "正在统计同类型群数据"
	case "single_stats":
		command = "单独群数据统计"
		ack = "正在统计当前群数据"
	case "schedule_list":
		command = "查看定时群发"
		ack = "正在查询定时任务"
	case "rate":
		command = "汇率对比"
		ack = "正在查询汇率"
	case "convert_toggle":
		command = "开关u显示"
		ack = "已切换换算开关"
	case "auto_toggle":
		if group.AutoReplyEnabled == 1 {
			command = "关闭自动回复"
			ack = "已关闭自动回复"
		} else {
			command = "开启自动回复"
			ack = "已开启自动回复"
		}
	case "help":
		command = "菜单"
		ack = "已推送帮助"
	case "auto_list":
		command = "自动回复列表"
		ack = "正在查询自动回复规则"
	case "forward_list":
		command = "关键词转发列表"
		ack = "正在查询关键词转发规则"
	case "custom_list":
		command = "自定义内容列表"
		ack = "正在查询自定义内容规则"
	default:
		a.answerCallback(cq.ID, "未知操作", false)
		return nil
	}
	if command != "" {
		if err := a.executeAsUserGroupCommand(ctx, chat.ID, cq.Message.MessageID, cq.From, command); err != nil {
			a.answerCallback(cq.ID, "执行失败", true)
			return err
		}
	}
	if refreshed, err := a.db.GetGroup(chat.ID); err == nil {
		edit := tgbotapi.NewEditMessageTextAndMarkup(chat.ID, cq.Message.MessageID, a.groupPanelText(refreshed), a.groupPanelMarkup(refreshed))
		if sendErr := a.sendWithPriority(outboundPriorityNormalChat, edit); sendErr != nil && !isMessageNotModified(sendErr) {
			log.Printf("刷新群控面板失败: %v", sendErr)
		}
	}
	a.answerCallback(cq.ID, ack, false)
	return nil
}

func (a *App) handleLedgerCardCallback(ctx context.Context, cq *tgbotapi.CallbackQuery) error {
	if cq == nil || cq.Message == nil || cq.Message.Chat == nil || cq.From == nil {
		a.answerCallback(cq.ID, "请求无效", true)
		return nil
	}
	chat := cq.Message.Chat
	if chat.IsPrivate() {
		a.answerCallback(cq.ID, "请在群组中使用", true)
		return nil
	}
	if !a.db.IsPrivileged(chat.ID, cq.From.ID) {
		a.answerCallback(cq.ID, "权限不足：仅权限人/操作人可操作", true)
		return nil
	}
	action := strings.TrimPrefix(strings.TrimSpace(cq.Data), ledgerCardCallbackPrefix)
	switch action {
	case ledgerCardActionDetail:
		// 先应答回调，避免客户端长时间转圈。
		a.answerCallback(cq.ID, "正在生成账单链接...", false)
		_ = a.getUserFSM().Clear(context.Background(), chat.ID, cq.From.ID)
		a.clearPendingRateTempData(chat.ID, cq.From.ID)
		_ = a.db.ClearTempData(chat.ID, cq.From.ID, ledgerOpenTempDataKey)
		group, _ := a.db.GetGroup(chat.ID)
		looklookURL := a.BuildWebAppLooklookURL(cq.From.ID, chat.ID, group.DayCutTime)
		if looklookURL == "" {
			_ = a.reply(chat.ID, cq.Message.MessageID, "网页账单入口不可用，请联系管理员检查 TG_WEBAPP_URL。")
			return nil
		}
		if parsedURL, err := url.Parse(looklookURL); err == nil {
			log.Printf(
				"账单链接已生成: chat_id=%d user_id=%d host=%s path=%s",
				chat.ID,
				cq.From.ID,
				strings.TrimSpace(parsedURL.Host),
				strings.TrimSpace(parsedURL.Path),
			)
		}
		markup := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonURL("🌐 打开账单", looklookURL),
				tgbotapi.NewInlineKeyboardButtonData("🔄 刷新链接", ledgerCardCallbackPrefix+ledgerCardActionDetail),
			),
		)
		edit := tgbotapi.NewEditMessageReplyMarkup(chat.ID, cq.Message.MessageID, markup)
		if err := a.sendWithPriority(outboundPriorityBookkeepingReceipt, edit); err != nil && !isMessageNotModified(err) {
			log.Printf("更新账单按钮失败: chat_id=%d message_id=%d err=%v", chat.ID, cq.Message.MessageID, err)
			_ = a.reply(chat.ID, cq.Message.MessageID, "生成打开账单按钮失败，请稍后重试。")
			return nil
		}
		return nil
	case ledgerCardActionOpen:
		payload, ok, err := a.db.PopTempData(chat.ID, cq.From.ID, ledgerOpenTempDataKey)
		if err != nil {
			log.Printf("读取网页账单确认态失败: chat_id=%d user_id=%d key=%s err=%v", chat.ID, cq.From.ID, ledgerOpenTempDataKey, err)
			a.answerCallback(cq.ID, "读取账单链接失败，请重试", true)
			return nil
		}
		looklookURL := strings.TrimSpace(payload["url"])
		if !ok || looklookURL == "" {
			a.answerCallback(cq.ID, "账单链接已失效，请重新点击查看账单", true)
			return nil
		}
		a.answerCallbackWithURL(cq.ID, "正在打开网页账单", looklookURL)
		return nil
	case ledgerCardActionCancel:
		if err := a.db.ClearTempData(chat.ID, cq.From.ID, ledgerOpenTempDataKey); err != nil {
			log.Printf("清理网页账单确认态失败: chat_id=%d user_id=%d key=%s err=%v", chat.ID, cq.From.ID, ledgerOpenTempDataKey, err)
		}
		a.answerCallback(cq.ID, "已取消打开网页账单", false)
		return nil
	default:
		a.answerCallback(cq.ID, "未知账单操作", false)
		return nil
	}
}

func (a *App) handleTRC20QueryCallback(ctx context.Context, cq *tgbotapi.CallbackQuery) error {
	_ = ctx
	if cq == nil || cq.Message == nil || cq.Message.Chat == nil {
		if cq != nil {
			a.answerCallback(cq.ID, "请求无效", true)
		}
		return nil
	}
	chat := cq.Message.Chat
	if chat == nil {
		a.answerCallback(cq.ID, "请求无效", true)
		return nil
	}
	if !chat.IsPrivate() {
		if cq.From == nil || !a.db.IsPrivileged(chat.ID, cq.From.ID) {
			a.answerCallback(cq.ID, "权限不足：仅权限人/操作人可操作", true)
			return nil
		}
		group, err := a.db.EnsureGroup(chat.ID, chat.Title)
		if err != nil {
			a.answerCallback(cq.ID, "群组初始化失败", true)
			return nil
		}
		if active, reason := a.checkGroupEntitlement(group); !active {
			_ = a.reply(chat.ID, cq.Message.MessageID, reason)
			a.answerCallback(cq.ID, "当前群功能已到期，请先续期", true)
			return nil
		}
	}

	action, address, ok := parseTRC20QueryCallbackData(cq.Data)
	if !ok || address == "" {
		a.answerCallback(cq.ID, "参数无效", true)
		return nil
	}

	switch action {
	case trc20ActionRefresh:
		text, markup, err := a.renderTRC20QueryCard(address)
		if err != nil {
			a.answerCallback(cq.ID, "刷新失败，请稍后重试", true)
			return nil
		}
		edit := tgbotapi.NewEditMessageTextAndMarkup(chat.ID, cq.Message.MessageID, text, *markup)
		edit.ParseMode = telegramParseModeMarkdownV
		if err := a.sendWithPriority(outboundPriorityNormalChat, edit); err != nil && !isMessageNotModified(err) {
			a.answerCallback(cq.ID, "刷新失败，请稍后重试", true)
			return nil
		}
		a.answerCallback(cq.ID, "数据已刷新", false)
		return nil
	case trc20ActionRisk:
		val := a.address.Validate(address)
		status, err := a.address.QueryOnChainStatus(address)
		if err != nil {
			_ = a.reply(chat.ID, cq.Message.MessageID, fmt.Sprintf("⚠️ 风险评估\n地址：%s\n结果：链上状态获取失败，请稍后重试。", address))
			a.answerCallback(cq.ID, "风险评估已返回", false)
			return nil
		}
		_ = a.reply(chat.ID, cq.Message.MessageID, buildTRC20RiskAssessmentReply(val, status))
		a.answerCallback(cq.ID, "风险评估已返回", false)
		return nil
	case trc20ActionChart:
		if err := a.replyTRC20AnalysisReport(chat.ID, cq.Message.MessageID, address); err != nil {
			_ = a.reply(chat.ID, cq.Message.MessageID, fmt.Sprintf("链上报告生成失败：%v", err))
			a.answerCallback(cq.ID, "报告生成失败", true)
			return nil
		}
		a.answerCallback(cq.ID, "报告已生成", false)
		return nil
	case trc20ActionWatch:
		_ = a.reply(chat.ID, cq.Message.MessageID, fmt.Sprintf("🔍 地址监控能力建设中。\n当前地址：%s\n你可以先使用【🔄 刷新数据】持续跟踪。", address))
		a.answerCallback(cq.ID, "监控功能建设中", false)
		return nil
	case trc20ActionMenu:
		if chat.IsPrivate() {
			userID := chat.ID
			if cq.From != nil {
				userID = cq.From.ID
			}
			if err := a.replyPrivateMenu(chat.ID, cq.Message.MessageID, userID); err != nil {
				a.answerCallback(cq.ID, "返回主菜单失败", true)
				return nil
			}
			a.answerCallback(cq.ID, "已返回主菜单", false)
			return nil
		}
		group, err := a.db.EnsureGroup(chat.ID, chat.Title)
		if err != nil {
			a.answerCallback(cq.ID, "返回主菜单失败", true)
			return nil
		}
		if err := a.replyGroupPanel(chat.ID, cq.Message.MessageID, group); err != nil {
			a.answerCallback(cq.ID, "返回主菜单失败", true)
			return nil
		}
		a.answerCallback(cq.ID, "已返回主菜单", false)
		return nil
	default:
		a.answerCallback(cq.ID, "未知操作", false)
		return nil
	}
}

func (a *App) handleRateQueryCallback(ctx context.Context, cq *tgbotapi.CallbackQuery) error {
	_ = ctx
	if cq == nil || cq.Message == nil || cq.Message.Chat == nil || cq.From == nil {
		if cq != nil {
			a.answerCallback(cq.ID, "请求无效", true)
		}
		return nil
	}
	chat := cq.Message.Chat
	if chat.IsPrivate() {
		a.answerCallback(cq.ID, "请在群组中使用", true)
		return nil
	}
	if !a.db.IsPrivileged(chat.ID, cq.From.ID) {
		a.answerCallback(cq.ID, "权限不足：仅权限人/操作人可操作", true)
		return nil
	}
	group, err := a.db.EnsureGroup(chat.ID, chat.Title)
	if err != nil {
		a.answerCallback(cq.ID, "群组初始化失败", true)
		return nil
	}
	if active, reason := a.checkGroupEntitlement(group); !active {
		_ = a.reply(chat.ID, cq.Message.MessageID, reason)
		a.answerCallback(cq.ID, "当前群功能已到期，请先续期", true)
		return nil
	}
	baseCommand, fiat, providerOverride, ok := parseRateQueryCallbackData(cq.Data)
	if !ok {
		a.answerCallback(cq.ID, "参数无效", true)
		return nil
	}
	text, markup, err := a.renderRateQueryResponse(chat.ID, group, baseCommand, fiat, providerOverride)
	if err != nil {
		a.answerCallback(cq.ID, mapExchangeErrToReply(err), true)
		return nil
	}
	if markup == nil {
		a.answerCallback(cq.ID, "当前命令不支持切换法币", true)
		return nil
	}
	edit := tgbotapi.NewEditMessageTextAndMarkup(chat.ID, cq.Message.MessageID, text, *markup)
	edit.ParseMode = telegramParseModeHTML
	if err := a.sendWithPriority(outboundPriorityNormalChat, edit); err != nil && !isMessageNotModified(err) {
		a.answerCallback(cq.ID, "切换失败，请稍后重试", true)
		return nil
	}
	switchLabels := make([]string, 0, 2)
	if providerLabel := rateQueryProviderButtonLabel(currentRateQueryProvider(baseCommand, providerOverride)); providerLabel != "" {
		switchLabels = append(switchLabels, providerLabel)
	}
	if fiat.Label != "" {
		switchLabels = append(switchLabels, fiat.Label)
	}
	if len(switchLabels) == 0 {
		a.answerCallback(cq.ID, "已更新汇率", false)
		return nil
	}
	a.answerCallback(cq.ID, "已切换为"+strings.Join(switchLabels, " / "), false)
	return nil
}

func parseTRC20QueryCallbackData(raw string) (action string, address string, ok bool) {
	data := strings.TrimSpace(raw)
	if !strings.HasPrefix(data, trc20QueryCallbackPrefix) {
		return "", "", false
	}
	payload := strings.TrimPrefix(data, trc20QueryCallbackPrefix)
	parts := strings.SplitN(payload, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	action = strings.TrimSpace(parts[0])
	address = strings.TrimSpace(parts[1])
	if action == "" || address == "" {
		return "", "", false
	}
	return action, address, true
}

func parseRateQueryCallbackData(raw string) (baseCommand string, fiat rateQueryFiat, providerOverride string, ok bool) {
	data := strings.TrimSpace(raw)
	if !strings.HasPrefix(data, rateQueryCallbackPrefix) {
		return "", rateQueryFiat{}, "", false
	}
	payload := strings.TrimPrefix(data, rateQueryCallbackPrefix)
	parts := strings.Split(payload, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return "", rateQueryFiat{}, "", false
	}
	baseCommand = rateQueryCommandFromToken(parts[0])
	if baseCommand == "" {
		return "", rateQueryFiat{}, "", false
	}
	fiat, ok = parseRateQueryFiat(parts[1])
	if !ok {
		return "", rateQueryFiat{}, "", false
	}
	if len(parts) == 3 {
		providerToken := strings.TrimSpace(parts[2])
		if providerToken != "" && !strings.EqualFold(providerToken, "auto") {
			providerOverride = normalizeRateQueryProviderSource(providerToken)
			if providerOverride == "" {
				return "", rateQueryFiat{}, "", false
			}
		}
	}
	return baseCommand, fiat, providerOverride, true
}

func (a *App) answerCallback(queryID, text string, alert bool) {
	if strings.TrimSpace(queryID) == "" {
		return
	}
	var cfg tgbotapi.CallbackConfig
	if alert {
		cfg = tgbotapi.NewCallbackWithAlert(queryID, text)
	} else {
		cfg = tgbotapi.NewCallback(queryID, text)
	}
	_, _ = a.api.Request(cfg)
}

func (a *App) answerCallbackWithURL(queryID, text, targetURL string) {
	if strings.TrimSpace(queryID) == "" || strings.TrimSpace(targetURL) == "" {
		return
	}
	cfg := tgbotapi.NewCallback(queryID, text)
	cfg.URL = strings.TrimSpace(targetURL)
	_, _ = a.api.Request(cfg)
}

func isMessageNotModified(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "message is not modified")
}

func (a *App) requireOwner(msg *tgbotapi.Message, g db.GroupConfig) bool {
	if msg.From == nil {
		_ = a.reply(msg.Chat.ID, msg.MessageID, "无法识别操作用户。")
		return false
	}
	if a.db.IsOwner(msg.Chat.ID, msg.From.ID) {
		return true
	}
	return false
}

func (a *App) requirePrivileged(msg *tgbotapi.Message, g db.GroupConfig) bool {
	if msg.From == nil {
		_ = a.reply(msg.Chat.ID, msg.MessageID, "无法识别操作用户。")
		return false
	}
	if a.db.IsPrivileged(msg.Chat.ID, msg.From.ID) {
		return true
	}
	return false
}

func normalizeDayCutTimeCommandValue(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("empty day cut time")
	}
	if !strings.Contains(trimmed, ":") {
		hour, err := strconv.Atoi(trimmed)
		if err != nil || hour < 0 || hour > 23 {
			return "", fmt.Errorf("invalid day cut hour")
		}
		return fmt.Sprintf("%02d:00:00", hour), nil
	}
	hour, minute, second, err := utils.ParseHMS(trimmed)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%02d:%02d:%02d", hour, minute, second), nil
}

func (a *App) checkGroupEntitlement(g db.GroupConfig) (bool, string) {
	if g.ChatID == 0 || g.OwnerUserID == 0 {
		return true, ""
	}
	profile, err := a.db.GetPrivateUserProfile(g.OwnerUserID)
	if err != nil {
		log.Printf("读取私聊用户权限失败(chat=%d owner=%d): %v", g.ChatID, g.OwnerUserID, err)
		return true, ""
	}
	expireRaw := strings.TrimSpace(profile.ExpireAt)
	if expireRaw == "" {
		return true, ""
	}
	expireAt, ok := parseRFC3339(expireRaw)
	if !ok {
		log.Printf("私聊用户到期时间格式异常(chat=%d owner=%d expire=%q)", g.ChatID, g.OwnerUserID, expireRaw)
		return true, ""
	}
	now := time.Now().UTC()
	if expireAt.After(now) {
		return true, ""
	}
	if strings.TrimSpace(profile.TrialStatus) != "expired" {
		profile.TrialStatus = "expired"
		_ = a.db.UpsertPrivateUserProfile(profile)
	}
	_ = a.db.UpdateGroupFields(g.ChatID, map[string]any{
		"bookkeeping_enabled":   0,
		"bookkeeping_cycle_key": "",
	})
	loc := a.loc
	if loc == nil {
		loc = time.Local
	}
	return false, fmt.Sprintf(
		"当前群功能已禁用：权限人使用权限已于 %s 到期。\n请权限人私聊机器人发送【购买使用】续期后恢复。",
		expireAt.In(loc).Format("2006-01-02 15:04:05"),
	)
}

func (a *App) currentBookkeepingCycleKey(dayCutTime string) string {
	return strings.TrimSpace(a.dailyLedgerPeriodKey(dayCutTime))
}

func (a *App) isGroupBookkeepingActiveForCurrentCycle(g db.GroupConfig) bool {
	if g.BookkeepingEnabled != 1 {
		return false
	}
	currentCycle := a.currentBookkeepingCycleKey(g.DayCutTime)
	if currentCycle == "" {
		return false
	}
	return strings.TrimSpace(g.BookkeepingCycleKey) == currentCycle
}

func (a *App) syncGroupBookkeepingCycleState(g db.GroupConfig) {
	if a == nil || a.db == nil || g.ChatID == 0 {
		return
	}
	cycleKey := strings.TrimSpace(g.BookkeepingCycleKey)
	if g.BookkeepingEnabled != 1 {
		if cycleKey == "" {
			return
		}
		_ = a.db.UpdateGroupFields(g.ChatID, map[string]any{"bookkeeping_cycle_key": ""})
		return
	}
	if a.isGroupBookkeepingActiveForCurrentCycle(g) {
		return
	}
	_ = a.db.UpdateGroupFields(g.ChatID, map[string]any{
		"bookkeeping_enabled":   0,
		"bookkeeping_cycle_key": "",
	})
}

func (a *App) ensureGroupBookkeepingActiveForCurrentCycle(msg *tgbotapi.Message, g db.GroupConfig) (bool, error) {
	if a.isGroupBookkeepingActiveForCurrentCycle(g) {
		return true, nil
	}
	a.syncGroupBookkeepingCycleState(g)
	if err := a.reply(msg.Chat.ID, msg.MessageID, bookkeepingNotStartedTip); err != nil {
		return false, err
	}
	return false, nil
}

func (a *App) getSensitiveConfirmer() *sensitiveOperationConfirmer {
	if a.sensitiveConfirmer == nil {
		a.sensitiveConfirmer = newSensitiveOperationConfirmer(sensitiveOperationConfirmTTL)
	}
	return a.sensitiveConfirmer
}

func (a *App) getExportEngine() *services.ExportEngine {
	if a == nil {
		return services.NewExportEngine()
	}
	if a.exportEngine == nil {
		a.exportEngine = services.NewExportEngine()
	}
	return a.exportEngine
}

func (a *App) getCommandVelocityChecker() *userCommandVelocityChecker {
	if a.commandVelocity == nil {
		a.commandVelocity = newUserCommandVelocityChecker(defaultVelocityWindow, defaultVelocityMaxPerWindow)
	}
	return a.commandVelocity
}

func (a *App) dispatchGroupMessage(ctx context.Context, msg *tgbotapi.Message, g db.GroupConfig, text string) error {
	if msg.From == nil {
		return a.reply(msg.Chat.ID, msg.MessageID, "无法识别操作用户。")
	}
	if shouldApplyCommandVelocityLimit(msg, text) {
		if !a.guardCommandVelocity(msg, text) {
			return nil
		}
	}
	if a.shouldDropByDegradation(msg, text) {
		return nil
	}
	if handled, err := a.consumeUserFSMState(msg, g, text); err != nil {
		return err
	} else if handled {
		return nil
	}
	if isShowGroupIDCommand(text) {
		m := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("当前群ID：<code>%d</code>", msg.Chat.ID))
		m.ParseMode = telegramParseModeHTML
		return a.sendWithPriority(outboundPriorityNormalChat, m)
	}
	if active, reason := a.checkGroupEntitlement(g); !active {
		if text == "转移本群权限人" {
			return a.handleOperatorCommand(msg, g, text)
		}
		if a.db.IsPrivileged(msg.Chat.ID, msg.From.ID) {
			return a.reply(msg.Chat.ID, msg.MessageID, reason)
		}
		return nil
	}
	if handled, err := a.tryHandleFeeRateQuoteCompare(msg.Chat.ID, msg.MessageID, text); handled || err != nil {
		return err
	}
	if !a.db.IsPrivileged(msg.Chat.ID, msg.From.ID) {
		if val, ok := utils.ParseMathExpression(text); ok {
			return a.replyCalculatorResult(msg.Chat.ID, msg.MessageID, val)
		}
		if handled, err := a.tryHandleExclusiveRemarkQuery(msg, g, text); err != nil {
			return err
		} else if handled {
			return nil
		}
		if addr, ok := parseMemberAddressVerificationRequest(text); ok {
			return a.handleTRC20AddressVerification(msg, g, addr)
		}
		if isMemberAddressQueryCommand(text) {
			queryText := text
			if text == "地址" {
				queryText = "查询地址"
			}
			return a.queryAddress(msg, g, queryText)
		}
		if shouldSilenceMemberPlainMessage(msg, text) {
			return nil
		}
		return nil
	}

	if handled, err := a.handleClosingReminderCommand(msg, g, text); handled || err != nil {
		return err
	}

	if text == "世界时间" || text == "worldtime" {
		return a.reply(msg.Chat.ID, msg.MessageID, "示例：北京时间、巴西时间、德国时间、美国中部时间、日本时间")
	}
	if text == "工作计时" {
		return a.reply(msg.Chat.ID, msg.MessageID, "计时命令：开始计时 / 结束计时")
	}
	if text == "菜单" || text == "帮助" || text == "功能总览" {
		if a.db.IsPrivileged(msg.Chat.ID, msg.From.ID) {
			if err := a.replyGroupPanel(msg.Chat.ID, msg.MessageID, g); err == nil {
				return nil
			}
		}
		return a.reply(msg.Chat.ID, msg.MessageID, groupHelpText()+"\n提示：发送【群控面板】可打开按钮界面。")
	}
	if text == "群组管理" {
		return a.reply(msg.Chat.ID, msg.MessageID, privateGroupManageText())
	}
	if text == "群控面板" || text == "控制面板" {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		return a.replyGroupPanel(msg.Chat.ID, msg.MessageID, g)
	}
	if text == "查单管理" {
		return a.reply(msg.Chat.ID, msg.MessageID, privateOrderManageText())
	}

	if text == "开始计时" || text == "结束计时" {
		return a.handleTimer(msg, text)
	}

	if text == "查看群评价" || text == "查看业务员评价" {
		target := "group"
		if text == "查看业务员评价" {
			target = "sales"
		}
		return a.showEvaluations(msg, target)
	}
	if strings.HasPrefix(text, "评价服务") {
		return a.handleEvaluation(msg, text)
	}

	if text == "上课" || text == "下课" {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		return a.handleClassMode(msg, g, text == "下课")
	}
	if strings.HasPrefix(text, "设置上课广告") {
		if !a.requireOwner(msg, g) {
			return nil
		}
		ad := strings.TrimSpace(strings.TrimPrefix(text, "设置上课广告"))
		if ad == "" {
			return a.reply(msg.Chat.ID, msg.MessageID, "格式：设置上课广告 你的广告内容")
		}
		_ = a.db.UpdateGroupFields(msg.Chat.ID, map[string]any{"class_start_ad": ad})
		return a.reply(msg.Chat.ID, msg.MessageID, "上课广告已更新。")
	}
	if strings.HasPrefix(text, "设置下课广告") {
		if !a.requireOwner(msg, g) {
			return nil
		}
		ad := strings.TrimSpace(strings.TrimPrefix(text, "设置下课广告"))
		if ad == "" {
			return a.reply(msg.Chat.ID, msg.MessageID, "格式：设置下课广告 你的广告内容")
		}
		_ = a.db.UpdateGroupFields(msg.Chat.ID, map[string]any{"class_end_ad": ad})
		return a.reply(msg.Chat.ID, msg.MessageID, "下课广告已更新。")
	}
	if strings.HasPrefix(text, "设置欢迎语") || strings.HasPrefix(text, "设置进群欢迎") {
		if !a.requireOwner(msg, g) {
			return nil
		}
		welcome := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(text, "设置欢迎语"), "设置进群欢迎"))
		if welcome == "" {
			return a.reply(msg.Chat.ID, msg.MessageID, "格式：设置欢迎语 欢迎内容")
		}
		_ = a.db.UpdateGroupFields(msg.Chat.ID, map[string]any{"welcome_text": welcome})
		return a.reply(msg.Chat.ID, msg.MessageID, "进群欢迎语已更新。")
	}

	if text == "开始记账" || text == "一键开始" || text == "一键开始机器人" || text == "开始" {
		if !a.requirePrivileged(msg, g) && !a.db.IsOwner(msg.Chat.ID, msg.From.ID) {
			return nil
		}
		_ = a.db.UpdateGroupFields(msg.Chat.ID, map[string]any{
			"bookkeeping_enabled":   1,
			"bookkeeping_cycle_key": a.currentBookkeepingCycleKey(g.DayCutTime),
		})
		return a.replyAccountingDataCardWithStatus(
			msg,
			g,
			"已开启记账。\n账单已经开始了，请继续记账：",
			"start_bookkeeping",
			false,
		)
	}
	if text == "停止记账" || text == "停止" {
		if !a.requirePrivileged(msg, g) && !a.db.IsOwner(msg.Chat.ID, msg.From.ID) {
			return nil
		}
		_ = a.db.UpdateGroupFields(msg.Chat.ID, map[string]any{
			"bookkeeping_enabled":   0,
			"bookkeeping_cycle_key": "",
		})
		return a.reply(msg.Chat.ID, msg.MessageID, "已停止记账。")
	}

	if text == "一键账单" || text == "一键显示账单" || text == "完整账单" || text == "显示账单" {
		return a.showBill(msg, g)
	}
	if text == "导出账单" || text == "导出报表" || text == "导出Excel" || text == "导出PDF" {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		return a.exportLedgerFiles(msg, g)
	}
	if text == "查单列表" || text == "查单管理" {
		return a.showOrderList(msg, g)
	}
	if strings.HasPrefix(text, "自助查单") {
		return a.searchOrders(msg, g, text)
	}
	if text == "延时查单列表" {
		return a.listDelayedQueries(msg)
	}
	if text == "清理延时查单" || text == "延时查单清理" {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		n, _ := a.db.ClearDelayedQueries(msg.Chat.ID)
		return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("延时查单记录已清理，共 %d 条。", n))
	}
	if strings.HasPrefix(text, "延时查单") {
		return a.addDelayedQuery(msg, text)
	}
	if text == "显示入款信息" {
		return a.showUserSideInfo(msg, g, "deposit")
	}
	if text == "显示下发信息" {
		return a.showUserSideInfo(msg, g, "payout")
	}

	if command, ok := sensitiveOperationByConfirm(text); ok {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		status, pendingCommand := a.getSensitiveConfirmer().Confirm(msg.Chat.ID, msg.From.ID, command)
		switch status {
		case sensitiveOperationConfirmationAccepted:
			return a.clearData(msg, g, command)
		case sensitiveOperationConfirmationExpired:
			return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("二次确认已超时，请重新发送【%s】。", pendingCommand))
		case sensitiveOperationConfirmationMismatch:
			confirmCommand, _ := sensitiveOperationConfirmCommand(pendingCommand)
			return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("当前待确认操作为【%s】，请发送【%s】完成确认。", pendingCommand, confirmCommand))
		default:
			return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("未检测到待确认操作，请先发送【%s】。", command))
		}
	}
	if confirmCommand, ok := sensitiveOperationConfirmCommand(text); ok {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		a.getSensitiveConfirmer().Request(msg.Chat.ID, msg.From.ID, text)
		return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("⚠️ 高危操作【%s】需要二次确认，请在%d秒内发送【%s】。", text, int(sensitiveOperationConfirmTTL/time.Second), confirmCommand))
	}
	if text == "清除缓存" || text == "清理缓存" {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		cardN, _ := a.db.ClearCardHistory(msg.Chat.ID)
		duplicateN, _ := a.db.ClearDuplicateAlertHistory(msg.Chat.ID)
		return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("重复缓存已清理：重复卡记录 %d 条，重复预警记录 %d 条。", cardN, duplicateN))
	}

	if strings.HasPrefix(text, "设置日切时间") {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		v := strings.TrimSpace(strings.TrimPrefix(text, "设置日切时间"))
		normalized, err := normalizeDayCutTimeCommandValue(v)
		if err != nil {
			return a.reply(msg.Chat.ID, msg.MessageID, "格式错误：设置日切时间04 或 设置日切时间04:00:00")
		}
		_ = a.db.UpdateGroupFields(msg.Chat.ID, map[string]any{"day_cut_time": normalized})
		return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("日切时间已设置为 %s", normalized))
	}

	if text == "显示昵称" || text == "关闭昵称" {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		flag := 0
		if text == "显示昵称" {
			flag = 1
		}
		_ = a.db.UpdateGroupFields(msg.Chat.ID, map[string]any{"display_nickname": flag})
		return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("记账昵称显示已%s。", map[bool]string{true: "开启", false: "关闭"}[flag == 1]))
	}

	if text == "开启换算" || text == "关闭换算" || text == "开关U显示" || text == "开关u显示" {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		next := 0
		if text == "开启换算" || text == "开关U显示" || text == "开关u显示" && g.ShowUSDTConversion == 0 {
			next = 1
		}
		if strings.HasPrefix(text, "开关") {
			if g.ShowUSDTConversion == 1 {
				next = 0
			} else {
				next = 1
			}
		}
		_ = a.db.UpdateGroupFields(msg.Chat.ID, map[string]any{"show_usdt_conversion": next})
		return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("记账换算已%s。", map[bool]string{true: "开启", false: "关闭"}[next == 1]))
	}
	normalizedBroadcastToggle := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(text), " ", ""), "　", "")
	if normalizedBroadcastToggle == "开启@群发信息" || normalizedBroadcastToggle == "关闭@群发信息" ||
		normalizedBroadcastToggle == "开启群发功能" || normalizedBroadcastToggle == "关闭群发功能" ||
		normalizedBroadcastToggle == "开启群发信息" || normalizedBroadcastToggle == "关闭群发信息" ||
		normalizedBroadcastToggle == "开启群发" || normalizedBroadcastToggle == "关闭群发" ||
		normalizedBroadcastToggle == "开启@群发" || normalizedBroadcastToggle == "关闭@群发" {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		flag := 0
		if strings.HasPrefix(normalizedBroadcastToggle, "开启") {
			flag = 1
		}
		_ = a.db.UpdateGroupFields(msg.Chat.ID, map[string]any{"at_broadcast_enabled": flag})
		if flag == 1 {
			a.setInteractiveBroadcastAwait(msg.Chat.ID, msg.From.ID)
			return a.reply(msg.Chat.ID, msg.MessageID, "开始成功，请输入群发内容。")
		}
		a.clearInteractiveBroadcastSession(msg.Chat.ID)
		return a.reply(msg.Chat.ID, msg.MessageID, "群发信息已关闭。")
	}
	if handled, err := a.handleInteractiveBroadcastDraftInput(msg, g, text); handled {
		return err
	}
	if handled, err := a.handleBatchTagSelectionInput(msg, g, text); handled {
		return err
	}
	if handled, err := a.handleBatchTypeSelectionInput(msg, g, text); handled {
		return err
	}
	if normalizedBroadcastToggle == "开启重复预警" || normalizedBroadcastToggle == "关闭重复预警" ||
		normalizedBroadcastToggle == "开启重复预警功能" || normalizedBroadcastToggle == "关闭重复预警功能" {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		groupType := strings.TrimSpace(g.GroupType)
		if groupType == "" {
			return a.reply(msg.Chat.ID, msg.MessageID, "当前群未设置群类型，请先发送【设置群类型 类型名】。")
		}
		groups, err := a.db.ListGroupsByType(groupType)
		if err != nil {
			return err
		}
		if len(groups) == 0 {
			return a.reply(msg.Chat.ID, msg.MessageID, "未找到同类型群，请先完成群类型设置。")
		}
		flag := 0
		if strings.HasPrefix(normalizedBroadcastToggle, "开启") {
			flag = 1
		}
		updated := 0
		for _, item := range groups {
			if err := a.db.UpdateGroupFields(item.ChatID, map[string]any{"duplicate_alert_enabled": flag}); err != nil {
				log.Printf("同步重复预警开关失败: source_chat=%d target_chat=%d group_type=%s err=%v", msg.Chat.ID, item.ChatID, groupType, err)
				continue
			}
			updated++
		}
		if updated == 0 {
			return a.reply(msg.Chat.ID, msg.MessageID, "重复预警开关同步失败，请稍后重试。")
		}
		actionText := map[bool]string{true: "开启", false: "关闭"}[flag == 1]
		if updated < len(groups) {
			return a.reply(
				msg.Chat.ID,
				msg.MessageID,
				fmt.Sprintf("重复预警已%s（群类型：%s），同类型群已同步 %d/%d。", actionText, groupType, updated, len(groups)),
			)
		}
		return a.reply(
			msg.Chat.ID,
			msg.MessageID,
			fmt.Sprintf("重复预警已%s（群类型：%s），同类型群已同步%s，共 %d 个。", actionText, groupType, actionText, updated),
		)
	}

	if strings.HasPrefix(text, "设置汇率") || strings.HasPrefix(text, "设置美金汇率") || strings.HasPrefix(text, "设置美元汇率") {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		v, ok := parseNumberFromTail(text)
		if !ok || almostZero(v) {
			if isStandaloneCustomRateCommand(text) {
				return a.enterUserFSMState(msg, UserInteractionStateAwaitCustomRate, "正在设置汇率中", "正在设置汇率中，请直接发送数字（例如 7.25），或发送“取消”退出。")
			}
			return a.reply(msg.Chat.ID, msg.MessageID, "格式错误，例如：设置汇率7.25")
		}
		_ = a.getUserFSM().Clear(context.Background(), msg.Chat.ID, msg.From.ID)
		a.clearPendingRateTempData(msg.Chat.ID, msg.From.ID)
		return a.applyRateSetting(msg, g, v, false)
	}

	if text == "设置实时汇率" {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		_ = a.db.UpdateGroupFields(msg.Chat.ID, map[string]any{"use_realtime_rate": 1})
		return a.reply(msg.Chat.ID, msg.MessageID, "已开启实时汇率记账。")
	}

	if strings.HasPrefix(text, "设置下发汇率") {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		v, ok := parseNumberFromTail(text)
		if !ok || almostZero(v) {
			if isStandalonePayoutRateCommand(text) {
				return a.enterUserFSMState(msg, UserInteractionStateAwaitPayoutRate, "正在设置下发汇率中", "正在设置下发汇率中，请直接发送数字（例如 7.20），或发送“取消”退出。")
			}
			return a.reply(msg.Chat.ID, msg.MessageID, "格式错误，例如：设置下发汇率7.20")
		}
		_ = a.getUserFSM().Clear(context.Background(), msg.Chat.ID, msg.From.ID)
		a.clearPendingRateTempData(msg.Chat.ID, msg.From.ID)
		return a.applyRateSetting(msg, g, v, true)
	}

	if strings.HasPrefix(text, "设置费率") {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		v, ok := parseFeeRateFromTail(text)
		if !ok {
			return a.reply(msg.Chat.ID, msg.MessageID, "格式错误，例如：设置费率10 或 设置费率-10")
		}
		_ = a.db.UpdateGroupFields(msg.Chat.ID, map[string]any{"fee_rate": v})
		return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("入款费率已设置为 %s%%", utils.FormatAmount(v)))
	}

	if strings.HasPrefix(text, "设置下发费率") {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		v, ok := parseFeeRateFromTail(text)
		if !ok {
			return a.reply(msg.Chat.ID, msg.MessageID, "格式错误，例如：设置下发费率10 或 设置下发费率-10")
		}
		_ = a.db.UpdateGroupFields(msg.Chat.ID, map[string]any{"payout_fee_rate": v})
		return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("下发费率已设置为 %s%%", utils.FormatAmount(v)))
	}
	if strings.HasPrefix(text, "单笔下发汇率") {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		v, ok := parseNumberFromTail(text)
		if !ok || almostZero(v) {
			return a.reply(msg.Chat.ID, msg.MessageID, "格式：单笔下发汇率 7.20")
		}
		if err := a.db.SetNextPayoutRate(msg.Chat.ID, msg.From.ID, v); err != nil {
			return err
		}
		return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("已设置单笔下发汇率：%s（仅下一笔下发生效）", utils.FormatAmount(v)))
	}

	if text == "一键设置汇率" {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		return a.quickSetRate(msg)
	}

	if looksLikeRateQueryCommand(text) {
		return a.handleRateQuery(msg, g, text)
	}

	if strings.HasPrefix(text, "设置下发地址") || strings.HasPrefix(text, "设置群下发地址") {
		if !a.requireOwner(msg, g) {
			return nil
		}
		return a.setAddress(msg, text, "payout")
	}
	if strings.HasPrefix(text, "设置专属地址") || strings.HasPrefix(text, "设置专属下发地址") {
		if !a.requireOwner(msg, g) {
			return nil
		}
		return a.setAddress(msg, text, "exclusive")
	}
	if text == "地址" {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		return a.queryAddress(msg, g, "查询地址")
	}
	if text == "下发地址" || text == "专属地址" {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		return a.queryAddress(msg, g, text)
	}
	if remark, ok := parseClearExclusiveAddressByRemarkCommand(text); ok {
		if !a.requireOwner(msg, g) {
			return nil
		}
		return a.clearExclusiveAddressByRemark(msg, g, remark)
	}
	if handled, err := a.tryHandleExclusiveRemarkQuery(msg, g, text); err != nil {
		return err
	} else if handled {
		return nil
	}
	if text == "清除下发地址" || text == "清空下发地址" || text == "删除下发地址" {
		if !a.requireOwner(msg, g) {
			return nil
		}
		if err := a.db.UpdateGroupFields(msg.Chat.ID, map[string]any{"payout_address": "", "payout_address_remark": ""}); err != nil {
			log.Printf("清除下发地址写库失败(chat=%d): %v", msg.Chat.ID, err)
			return a.reply(msg.Chat.ID, msg.MessageID, "清除下发地址失败，请稍后重试。")
		}
		return a.reply(msg.Chat.ID, msg.MessageID, "下发地址已清除。")
	}
	if text == "清除专属地址" || text == "清空专属地址" || text == "删除专属地址" {
		if !a.requireOwner(msg, g) {
			return nil
		}
		if err := a.db.UpdateGroupFields(msg.Chat.ID, map[string]any{
			"exclusive_address":        "",
			"exclusive_address_owner":  "",
			"exclusive_address_remark": "",
		}); err != nil {
			log.Printf("清除专属地址写库失败(chat=%d): %v", msg.Chat.ID, err)
			return a.reply(msg.Chat.ID, msg.MessageID, "清除专属地址失败，请稍后重试。")
		}
		if err := a.db.ClearExclusiveAddresses(msg.Chat.ID); err != nil {
			log.Printf("清空专属地址名单失败(chat=%d): %v", msg.Chat.ID, err)
			return a.reply(msg.Chat.ID, msg.MessageID, "清除专属地址失败，请稍后重试。")
		}
		return a.reply(msg.Chat.ID, msg.MessageID, "专属地址已清除。")
	}
	if strings.HasPrefix(text, "验证地址") {
		return a.verifyAddress(msg, text)
	}
	if strings.HasPrefix(text, "查询地址") {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		return a.queryAddress(msg, g, text)
	}

	if strings.HasPrefix(text, "设置担保规则") {
		if !a.requireOwner(msg, g) {
			return nil
		}
		rule := strings.TrimSpace(strings.TrimPrefix(text, "设置担保规则"))
		if rule == "" {
			return a.reply(msg.Chat.ID, msg.MessageID, "格式：设置担保规则 你的规则内容")
		}
		_ = a.db.UpdateGroupFields(msg.Chat.ID, map[string]any{"guarantee_rule": rule})
		return a.reply(msg.Chat.ID, msg.MessageID, "担保规则已更新。")
	}
	if text == "担保" || text == "担保规则" {
		rule := strings.TrimSpace(g.GuaranteeRule)
		if rule == "" {
			return a.reply(msg.Chat.ID, msg.MessageID, "当前未设置担保规则，请先发送【设置担保规则你的规则内容】。")
		}
		return a.reply(msg.Chat.ID, msg.MessageID, rule)
	}

	if strings.HasPrefix(text, "设置群标签") {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		v := strings.TrimSpace(strings.TrimPrefix(text, "设置群标签"))
		if v == "" {
			return a.reply(msg.Chat.ID, msg.MessageID, "格式：设置群标签 标签1,标签2")
		}
		_ = a.db.UpdateGroupFields(msg.Chat.ID, map[string]any{"group_tags": strings.Join(utils.SplitTags(v), ",")})
		return a.reply(msg.Chat.ID, msg.MessageID, "群标签已更新。")
	}
	if strings.HasPrefix(text, "批量设置群标签") {
		return a.handleBatchSetGroupTags(msg, g, text)
	}
	if strings.HasPrefix(text, "批量设置群类型") {
		return a.handleBatchSetGroupTypes(msg, g, text)
	}
	if text == "清除群标签" || text == "恢复默认群标签" {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		_ = a.db.UpdateGroupFields(msg.Chat.ID, map[string]any{"group_tags": ""})
		return a.reply(msg.Chat.ID, msg.MessageID, "群标签已清除（默认）。")
	}
	if text == "显示群标签" || text == "查看群标签" || text == "本群标签" {
		tags := strings.Join(utils.SplitTags(g.GroupTags), "、")
		return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("当前群标签：%s", orDefault(tags, "未设置")))
	}
	if strings.HasPrefix(text, "设置群类型") {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		v := strings.TrimSpace(strings.TrimPrefix(text, "设置群类型"))
		if v == "" {
			return a.reply(msg.Chat.ID, msg.MessageID, "格式：设置群类型 资金盘/担保盘/代收代付")
		}
		_ = a.db.UpdateGroupFields(msg.Chat.ID, map[string]any{"group_type": v})
		return a.reply(
			msg.Chat.ID,
			msg.MessageID,
			fmt.Sprintf(
				"群类型已更新为：%s\n【设置群类型】不仅可以联动【开启重复预警】。\n后台还能看到此类型群的具体入金数据，不需要每个群对数据，节省宝贵时间。\n忘记当前群是什么类型，输入【显示群类型】；也可在后台【类型管理】查看群类型列表。",
				v,
			),
		)
	}
	if text == "显示群类型" {
		groupType := strings.TrimSpace(g.GroupType)
		if groupType == "" {
			return a.reply(msg.Chat.ID, msg.MessageID, "当前群类型：未设置\n可发送【设置群类型CF】设置；也可在后台【类型管理】查看群类型列表。")
		}
		return a.reply(
			msg.Chat.ID,
			msg.MessageID,
			fmt.Sprintf("当前群类型：%s\n可在后台【类型管理】查看该类型群列表与入金数据。", groupType),
		)
	}
	if text == "同类型群数据统计" {
		return a.showSameTypeStats(msg, g)
	}
	if text == "单独群数据统计" {
		return a.showSingleGroupStats(msg, g)
	}

	if strings.HasPrefix(text, "设置操作人") || strings.HasPrefix(text, "删除操作人") ||
		strings.HasPrefix(text, "设置全局操作人") || strings.HasPrefix(text, "删除全局操作人") ||
		strings.HasPrefix(text, "设置全局权限人") || strings.HasPrefix(text, "删除全局权限人") ||
		text == "显示本群操作人" || text == "显示操作人" || text == "本群操作人" ||
		text == "显示全局操作人" || text == "显示全局权限人" || text == "转移本群权限人" {
		return a.handleOperatorCommand(msg, g, text)
	}

	if strings.HasPrefix(text, "设置身份") || strings.HasPrefix(text, "删除身份") || text == "本群身份" ||
		(strings.HasPrefix(text, "显示") && !isShowGroupIDCommand(text) && text != "显示昵称" && text != "显示入款信息" && text != "显示下发信息") {
		ok, err := a.handleIdentityCommand(msg, g, text)
		if ok || err != nil {
			return err
		}
	}

	if strings.HasPrefix(text, "设置自定义内容") || strings.HasPrefix(text, "删除自定义内容") ||
		text == "开启自定义内容时间" || text == "关闭自定义内容时间" {
		return a.handleCustomContentCommand(msg, g, text)
	}

	if text == "开启自动回复" || text == "关闭自动回复" {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		flag := 0
		if text == "开启自动回复" {
			flag = 1
		}
		_ = a.db.UpdateGroupFields(msg.Chat.ID, map[string]any{"auto_reply_enabled": flag})
		return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("自动回复已%s。", map[bool]string{true: "开启", false: "关闭"}[flag == 1]))
	}
	if text == "自动回复列表" {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		return a.showAutoReplyList(msg)
	}
	if text == "关键词转发列表" {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		return a.showForwardRuleList(msg)
	}
	if text == "自定义内容列表" {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		return a.showCustomContentList(msg)
	}

	if strings.HasPrefix(text, "设置自动回复") || strings.HasPrefix(text, "删除自动回复") ||
		strings.HasPrefix(text, "设置关键词转发") || strings.HasPrefix(text, "删除关键词转发") {
		return a.handleAutomationCommand(msg, g, text)
	}

	if strings.HasPrefix(text, "一键群发") || strings.HasPrefix(text, "定时群发") ||
		strings.HasPrefix(text, "查看定时群发") || strings.HasPrefix(text, "编辑定时群发") ||
		strings.HasPrefix(text, "重新发送定时群发") || text == "转发" || strings.HasPrefix(text, "指定@群发") {
		return a.handleBroadcastCommand(ctx, msg, g, text)
	}

	if text == "@所有人" {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		return a.mentionAll(msg)
	}
	if text == "机器人退出群组" {
		if !a.requireOwner(msg, g) {
			return nil
		}
		_ = a.sendWithPriority(outboundPriorityNormalChat, tgbotapi.NewMessage(msg.Chat.ID, "收到，机器人将退出本群。"))
		_, _ = a.api.Request(tgbotapi.LeaveChatConfig{ChatID: msg.Chat.ID})
		return nil
	}

	if text == "记账信息跟踪" {
		return a.showTracking(msg, g)
	}
	if strings.HasPrefix(text, "换算-") {
		return a.handleConvertTemplate(msg, text)
	}

	if text == "记账撤回" || text == "撤回入款" || text == "撤回下发" {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		if !a.guardReplay(msg, text) {
			return nil
		}
		return a.dispatchBookkeepingRequest(ctx, msg, "reversal", func() error {
			return a.reversalLast(msg, g, text)
		})
	}

	if strings.HasPrefix(text, "查卡") {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		no := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(text, "查卡+"), "查卡"))
		return a.handleCardQuery(msg, no)
	}
	if strings.HasPrefix(text, "设置联行号") {
		if !a.requireOwner(msg, g) {
			return nil
		}
		return a.handleSetBranchCodeOverride(msg, text)
	}
	if strings.HasPrefix(text, "删除联行号") {
		if !a.requireOwner(msg, g) {
			return nil
		}
		return a.handleDeleteBranchCodeOverride(msg, text)
	}
	if strings.HasPrefix(text, "查联行号") {
		no := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(text, "查联行号+"), "查联行号"))
		if no == "" {
			return a.reply(msg.Chat.ID, msg.MessageID, "格式：查联行号+银行卡号")
		}
		return a.reply(msg.Chat.ID, msg.MessageID, a.queryBranchCode(msg.Chat.ID, no))
	}
	if strings.HasPrefix(text, "查询USDT-TRC20最近交易记录") ||
		strings.HasPrefix(text, "查询TRC20") ||
		strings.HasPrefix(text, "查询链上") ||
		strings.HasPrefix(text, "链上查询") ||
		strings.HasPrefix(strings.ToLower(text), "trc20") {
		return a.handleTRC20Query(msg, text)
	}

	if strings.HasPrefix(text, "距离") || strings.HasPrefix(strings.ToLower(text), "jl") {
		return a.handleDistanceQuery(msg, text)
	}
	if region, ok := utils.ParseWorldTimeQuery(text); ok {
		return a.handleWorldTime(msg, region)
	}

	if isQuickAccountingDataCardCommand(text) {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		active, err := a.ensureGroupBookkeepingActiveForCurrentCycle(msg, g)
		if err != nil {
			return err
		}
		if !active {
			return nil
		}
		return a.replyAccountingDataCard(msg, g)
	}

	if ledger, ok := utils.ParseLedgerText(text); ok {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		active, err := a.ensureGroupBookkeepingActiveForCurrentCycle(msg, g)
		if err != nil {
			return err
		}
		if !active {
			return nil
		}
		if !a.guardReplay(msg, "记账") {
			return nil
		}
		return a.dispatchBookkeepingRequest(ctx, msg, "ledger", func() error {
			return a.handleLedger(msg, g, ledger)
		})
	}
	if val, ok := utils.ParseMathExpression(text); ok {
		return a.replyCalculatorResult(msg.Chat.ID, msg.MessageID, val)
	}
	if utils.LooksLikeLedgerText(text) {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		active, err := a.ensureGroupBookkeepingActiveForCurrentCycle(msg, g)
		if err != nil {
			return err
		}
		if !active {
			return nil
		}
		return a.reply(
			msg.Chat.ID,
			msg.MessageID,
			"记账格式不正确。示例：+100 / -100 / +100U#备注 / 下发100U#备注",
		)
	}

	if !isAddressManagementCommand(text) {
		if addr, owner, remark, ok := parseCompactExclusiveAddressShortcut(text); ok {
			if !a.requirePrivileged(msg, g) {
				return nil
			}
			return a.setAddressWithSuccessLine(
				msg,
				fmt.Sprintf("设置专属地址 %s %s %s", addr, owner, remark),
				"exclusive",
				compactExclusiveAddressShortcutSuccessLine(text),
			)
		}
		if addr, ok := extractTRC20AddressForVerification(text); ok {
			return a.handleTRC20AddressVerification(msg, g, addr)
		}
	}

	if handled, err := a.handlePlainTextDuplicateAlert(msg, g, text); err != nil {
		return err
	} else if handled {
		return nil
	}

	if val, ok := utils.ParseMathExpression(text); ok {
		return a.replyCalculatorResult(msg.Chat.ID, msg.MessageID, val)
	}

	if custom, ok, _ := a.db.GetCustomContent(msg.Chat.ID, text); ok {
		if g.CustomContentWithTime == 1 {
			custom = fmt.Sprintf("%s\n\n北京时间：%s", custom, utils.BeijingNow(a.cfg.Timezone).Format("2006-01-02 15:04:05"))
		}
		return a.reply(msg.Chat.ID, msg.MessageID, custom)
	}

	return a.handleAutoReplyAndForward(msg, g, text)
}

func (a *App) dispatchBookkeepingRequest(ctx context.Context, msg *tgbotapi.Message, commandKey string, run func() error) error {
	if run == nil {
		return nil
	}
	if hit, err := a.replyCachedBookkeepingResult(msg, commandKey); err != nil {
		return err
	} else if hit {
		return nil
	}
	execute := func(lockCtx context.Context) error {
		if hit, err := a.replyCachedBookkeepingResult(msg, commandKey); err != nil {
			return err
		} else if hit {
			return nil
		}
		if !a.guardDistributedRequestLock(lockCtx, msg, commandKey) {
			if hit, err := a.replyCachedBookkeepingResult(msg, commandKey); err != nil {
				return err
			} else if hit {
				return nil
			}
			return nil
		}
		if hit, err := a.replyCachedBookkeepingResult(msg, commandKey); err != nil {
			return err
		} else if hit {
			return nil
		}
		return run()
	}
	if a.bookkeepingQueue == nil || msg == nil {
		return execute(ctx)
	}
	taskErr := a.bookkeepingQueue.Submit(ctx, msg.Chat.ID, func(taskCtx context.Context) error {
		return execute(taskCtx)
	})
	if taskErr == nil {
		return nil
	}
	switch {
	case errors.Is(taskErr, errSerializedQueueBackpressure):
		return a.replyWithPriority(outboundPriorityErrorAlert, msg.Chat.ID, msg.MessageID, "系统繁忙：当前群记账请求过多，请稍后重试。")
	case errors.Is(taskErr, errSerializedQueueClosed):
		return a.replyWithPriority(outboundPriorityErrorAlert, msg.Chat.ID, msg.MessageID, "系统正在重启，请稍后重试。")
	default:
		return taskErr
	}
}

func (a *App) cachedBookkeepingResult(msg *tgbotapi.Message, commandKey string) (string, bool) {
	if a == nil || msg == nil || msg.Chat == nil || a.requestResultCache == nil {
		return "", false
	}
	cacheKey := buildRequestResultCacheKey(commandKey, msg.Chat.ID, msg.MessageID)
	if cacheKey == "" {
		return "", false
	}
	return a.requestResultCache.Get(cacheKey)
}

// ClearBookkeepingResultCache 在账单展示配置变化后清理指定群的已渲染回执。
func (a *App) ClearBookkeepingResultCache(chatID int64) int {
	if a == nil || a.requestResultCache == nil {
		return 0
	}
	return a.requestResultCache.ClearChat(chatID)
}

func (a *App) replyCachedBookkeepingResult(msg *tgbotapi.Message, commandKey string) (bool, error) {
	cached, ok := a.cachedBookkeepingResult(msg, commandKey)
	if !ok {
		return false, nil
	}
	replyText, parseMode := decodeBookkeepingCachedResponse(cached)
	if parseMode == "" {
		return true, a.replyWithPriority(outboundPriorityBookkeepingReceipt, msg.Chat.ID, msg.MessageID, replyText)
	}
	m := tgbotapi.NewMessage(msg.Chat.ID, replyText)
	m.ReplyToMessageID = msg.MessageID
	m.ParseMode = parseMode
	return true, a.sendWithPriority(outboundPriorityBookkeepingReceipt, m)
}

func (a *App) replyBookkeepingResult(msg *tgbotapi.Message, commandKey, text string, cacheable bool) error {
	return a.replyBookkeepingResultWithMarkupAndParseMode(msg, commandKey, text, cacheable, nil, "")
}

func (a *App) replyBookkeepingResultWithMarkup(msg *tgbotapi.Message, commandKey, text string, cacheable bool, markup *tgbotapi.InlineKeyboardMarkup) error {
	return a.replyBookkeepingResultWithMarkupAndParseMode(msg, commandKey, text, cacheable, markup, "")
}

func (a *App) replyBookkeepingResultWithMarkupAndParseMode(msg *tgbotapi.Message, commandKey, text string, cacheable bool, markup *tgbotapi.InlineKeyboardMarkup, parseMode string) error {
	if msg == nil || msg.Chat == nil {
		return nil
	}
	m := tgbotapi.NewMessage(msg.Chat.ID, text)
	m.ReplyToMessageID = msg.MessageID
	if strings.TrimSpace(parseMode) != "" {
		m.ParseMode = strings.TrimSpace(parseMode)
	}
	if markup != nil {
		m.ReplyMarkup = *markup
	}
	if err := a.sendWithPriority(outboundPriorityBookkeepingReceipt, m); err != nil {
		return err
	}
	if cacheable && a != nil && a.requestResultCache != nil {
		cacheKey := buildRequestResultCacheKey(commandKey, msg.Chat.ID, msg.MessageID)
		if cacheKey != "" {
			a.requestResultCache.Set(cacheKey, msg.Chat.ID, encodeBookkeepingCachedResponse(text, parseMode))
		}
	}
	return nil
}

func encodeBookkeepingCachedResponse(text, parseMode string) string {
	switch strings.TrimSpace(parseMode) {
	case telegramParseModeHTML:
		return templatePrefixHTML + " " + text
	case telegramParseModeMarkdownV:
		return templatePrefixMarkdownV2 + " " + text
	default:
		return text
	}
}

func decodeBookkeepingCachedResponse(cached string) (string, string) {
	parseMode, body, ok := parseTemplateModePrefix(cached)
	if !ok {
		return cached, ""
	}
	return body, parseMode
}

func (a *App) guardDistributedRequestLock(ctx context.Context, msg *tgbotapi.Message, commandKey string) bool {
	if a == nil || msg == nil {
		return true
	}
	locker := a.requestLocker
	if locker == nil {
		return true
	}
	lockKey := buildRequestLockKey(commandKey, msg.Chat.ID, msg.MessageID)
	locked, err := locker.Acquire(ctx, lockKey, buildRequestLockValue(time.Now()), defaultDistributedRequestLockTTL)
	if err != nil {
		log.Printf("分布式锁获取失败，降级到数据库幂等防重: command=%s chat_id=%d message_id=%d err=%v", normalizeRequestCommandKey(commandKey), msg.Chat.ID, msg.MessageID, err)
		return true
	}
	if locked {
		return true
	}
	userID := int64(0)
	if msg.From != nil {
		userID = msg.From.ID
	}
	detail := fmt.Sprintf("command=%s lock_key=%s chat_id=%d message_id=%d", normalizeRequestCommandKey(commandKey), lockKey, msg.Chat.ID, msg.MessageID)
	if a.db != nil {
		_ = a.db.AddRiskEvent(msg.Chat.ID, msg.MessageID, userID, "distributed_lock_blocked", "warn", detail)
	}
	_ = a.reply(msg.Chat.ID, msg.MessageID, "重复请求已拦截，请勿在10秒内重复提交。")
	return false
}

func (a *App) consumeUserFSMState(msg *tgbotapi.Message, g db.GroupConfig, text string) (bool, error) {
	if a == nil || msg == nil || msg.Chat == nil || msg.From == nil {
		return false, nil
	}
	state, ok, err := a.getUserFSM().Get(context.Background(), msg.Chat.ID, msg.From.ID)
	if err != nil {
		log.Printf("读取用户状态机失败: chat_id=%d user_id=%d err=%v", msg.Chat.ID, msg.From.ID, err)
		return false, nil
	}
	if !ok {
		state, ok = a.restoreUserFSMState(msg.Chat.ID, msg.From.ID)
		if !ok {
			return false, nil
		}
	}

	switch state.State {
	case UserInteractionStateAwaitCustomRate:
		return a.handlePendingRateState(msg, g, text, false)
	case UserInteractionStateAwaitPayoutRate:
		return a.handlePendingRateState(msg, g, text, true)
	default:
		_ = a.getUserFSM().Clear(context.Background(), msg.Chat.ID, msg.From.ID)
		a.clearPendingRateTempData(msg.Chat.ID, msg.From.ID)
		return false, nil
	}
}

func (a *App) restoreUserFSMState(chatID, userID int64) (UserInteractionContext, bool) {
	if a == nil || a.db == nil || chatID == 0 || userID == 0 {
		return UserInteractionContext{}, false
	}
	payload, ok, err := a.db.GetTempData(chatID, userID, rateSetupTempDataKey)
	if err != nil {
		log.Printf("读取会话恢复暂存数据失败: chat_id=%d user_id=%d key=%s err=%v", chatID, userID, rateSetupTempDataKey, err)
		return UserInteractionContext{}, false
	}
	if !ok {
		return UserInteractionContext{}, false
	}
	nowFn := time.Now
	if a.now != nil {
		nowFn = a.now
	}
	recovered, mapped := mapRateSetupTempDataToFSMState(payload, nowFn())
	if !mapped {
		a.clearPendingRateTempData(chatID, userID)
		return UserInteractionContext{}, false
	}
	if _, _, err := a.getUserFSM().Transition(context.Background(), chatID, userID, recovered); err != nil {
		log.Printf("恢复用户状态机失败: chat_id=%d user_id=%d state=%s err=%v", chatID, userID, recovered.State, err)
		return recovered, true
	}
	log.Printf("已恢复用户会话状态: chat_id=%d user_id=%d state=%s", chatID, userID, recovered.State)
	return recovered, true
}

func mapRateSetupTempDataToFSMState(payload map[string]string, now time.Time) (UserInteractionContext, bool) {
	if len(payload) == 0 {
		return UserInteractionContext{}, false
	}
	state := normalizeUserInteractionState(UserInteractionState(payload["state"]))
	if state == UserInteractionStateIdle {
		switch strings.ToLower(strings.TrimSpace(payload["mode"])) {
		case "custom":
			state = UserInteractionStateAwaitCustomRate
		case "payout":
			state = UserInteractionStateAwaitPayoutRate
		}
	}
	if state == UserInteractionStateIdle {
		return UserInteractionContext{}, false
	}
	return normalizeUserInteractionContext(
		UserInteractionContext{
			State: state,
			Scene: strings.TrimSpace(payload["scene"]),
		},
		now,
	), true
}

func (a *App) handlePendingRateState(msg *tgbotapi.Message, g db.GroupConfig, text string, payout bool) (bool, error) {
	trimmed := strings.TrimSpace(text)
	if !shouldHandleRateFSMInput(trimmed, payout) {
		return false, nil
	}
	payout, stagedScene := a.resolvePendingRateTempData(msg.Chat.ID, msg.From.ID, payout)
	if isUserFSMCancelCommand(trimmed) {
		_ = a.getUserFSM().Clear(context.Background(), msg.Chat.ID, msg.From.ID)
		a.clearPendingRateTempData(msg.Chat.ID, msg.From.ID)
		if payout {
			return true, a.reply(msg.Chat.ID, msg.MessageID, "已取消设置下发汇率。")
		}
		return true, a.reply(msg.Chat.ID, msg.MessageID, "已取消设置汇率。")
	}
	if !a.requirePrivileged(msg, g) {
		_ = a.getUserFSM().Clear(context.Background(), msg.Chat.ID, msg.From.ID)
		a.clearPendingRateTempData(msg.Chat.ID, msg.From.ID)
		return true, nil
	}
	value, ok := parseRateInputFromFSM(trimmed, payout)
	if !ok || almostZero(value) {
		return true, a.reply(msg.Chat.ID, msg.MessageID, buildPendingRatePrompt(payout, stagedScene))
	}
	_ = a.getUserFSM().Clear(context.Background(), msg.Chat.ID, msg.From.ID)
	a.clearPendingRateTempData(msg.Chat.ID, msg.From.ID)
	return true, a.applyRateSetting(msg, g, value, payout)
}

func (a *App) enterUserFSMState(msg *tgbotapi.Message, state UserInteractionState, scene, prompt string) error {
	if a == nil || msg == nil || msg.Chat == nil || msg.From == nil {
		return nil
	}
	_, _, err := a.getUserFSM().Transition(
		context.Background(),
		msg.Chat.ID,
		msg.From.ID,
		UserInteractionContext{
			State: state,
			Scene: scene,
		},
	)
	if err != nil {
		log.Printf("写入用户状态机失败: chat_id=%d user_id=%d state=%s err=%v", msg.Chat.ID, msg.From.ID, state, err)
	} else {
		a.storePendingRateTempData(msg, state, scene)
	}
	return a.reply(msg.Chat.ID, msg.MessageID, prompt)
}

func buildPendingRatePrompt(payout bool, scene string) string {
	promptScene := strings.TrimSpace(scene)
	if promptScene == "" {
		if payout {
			promptScene = "正在设置下发汇率中"
		} else {
			promptScene = "正在设置汇率中"
		}
	}
	if payout {
		return fmt.Sprintf("%s，请直接发送数字（例如 7.20），或发送“取消”退出。", promptScene)
	}
	return fmt.Sprintf("%s，请直接发送数字（例如 7.25），或发送“取消”退出。", promptScene)
}

func (a *App) resolvePendingRateTempData(chatID, userID int64, fallbackPayout bool) (bool, string) {
	if a == nil || a.db == nil || chatID == 0 || userID == 0 {
		return fallbackPayout, ""
	}
	payload, ok, err := a.db.GetTempData(chatID, userID, rateSetupTempDataKey)
	if err != nil {
		log.Printf("读取临时暂存数据失败: chat_id=%d user_id=%d key=%s err=%v", chatID, userID, rateSetupTempDataKey, err)
		return fallbackPayout, ""
	}
	if !ok {
		return fallbackPayout, ""
	}
	mode := strings.ToLower(strings.TrimSpace(payload["mode"]))
	switch mode {
	case "payout":
		fallbackPayout = true
	case "custom":
		fallbackPayout = false
	}
	return fallbackPayout, strings.TrimSpace(payload["scene"])
}

func (a *App) storePendingRateTempData(msg *tgbotapi.Message, state UserInteractionState, scene string) {
	if a == nil || a.db == nil || msg == nil || msg.Chat == nil || msg.From == nil {
		return
	}
	mode := ""
	switch state {
	case UserInteractionStateAwaitCustomRate:
		mode = "custom"
	case UserInteractionStateAwaitPayoutRate:
		mode = "payout"
	default:
		return
	}
	payload := map[string]string{
		"mode":  mode,
		"scene": strings.TrimSpace(scene),
	}
	if err := a.db.StageTempData(msg.Chat.ID, msg.From.ID, rateSetupTempDataKey, payload, rateSetupTempDataTTL); err != nil {
		log.Printf("写入临时暂存数据失败: chat_id=%d user_id=%d key=%s err=%v", msg.Chat.ID, msg.From.ID, rateSetupTempDataKey, err)
	}
}

func (a *App) clearPendingRateTempData(chatID, userID int64) {
	if a == nil || a.db == nil || chatID == 0 || userID == 0 {
		return
	}
	if err := a.db.ClearTempData(chatID, userID, rateSetupTempDataKey); err != nil {
		log.Printf("清理临时暂存数据失败: chat_id=%d user_id=%d key=%s err=%v", chatID, userID, rateSetupTempDataKey, err)
	}
}

func shouldHandleRateFSMInput(text string, payout bool) bool {
	if text == "" {
		return false
	}
	if isUserFSMCancelCommand(text) || pureNumberRegex.MatchString(text) {
		return true
	}
	if payout {
		return isStandalonePayoutRateCommand(text) || strings.HasPrefix(text, "设置下发汇率")
	}
	return isStandaloneCustomRateCommand(text) || strings.HasPrefix(text, "设置汇率") || strings.HasPrefix(text, "设置美金汇率") || strings.HasPrefix(text, "设置美元汇率")
}

func parseRateInputFromFSM(text string, payout bool) (float64, bool) {
	if pureNumberRegex.MatchString(text) {
		v, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return 0, false
		}
		return v, true
	}
	if payout {
		if !strings.HasPrefix(text, "设置下发汇率") {
			return 0, false
		}
		return parseNumberFromTail(text)
	}
	if strings.HasPrefix(text, "设置汇率") || strings.HasPrefix(text, "设置美金汇率") || strings.HasPrefix(text, "设置美元汇率") {
		return parseNumberFromTail(text)
	}
	return 0, false
}

func isStandaloneCustomRateCommand(text string) bool {
	trimmed := strings.TrimSpace(text)
	for _, prefix := range []string{"设置汇率", "设置美金汇率", "设置美元汇率"} {
		if trimmed == prefix {
			return true
		}
		if strings.HasPrefix(trimmed, prefix) {
			tail := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
			if strings.Trim(tail, "：:") == "" {
				return true
			}
		}
	}
	return false
}

func isStandalonePayoutRateCommand(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "设置下发汇率" {
		return true
	}
	if strings.HasPrefix(trimmed, "设置下发汇率") {
		tail := strings.TrimSpace(strings.TrimPrefix(trimmed, "设置下发汇率"))
		return strings.Trim(tail, "：:") == ""
	}
	return false
}

func isUserFSMCancelCommand(text string) bool {
	trimmed := strings.TrimSpace(text)
	return trimmed == "取消" || trimmed == "退出" || trimmed == "取消设置汇率" || trimmed == "取消设置下发汇率"
}

func parseNumberFromTail(text string) (float64, bool) {
	re := regexp.MustCompile(`(-?\d+(?:\.\d+)?)\s*$`)
	m := re.FindStringSubmatch(text)
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func parseFeeRateFromTail(text string) (float64, bool) {
	trimmed := strings.TrimSpace(text)
	trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed, "%"))
	return parseNumberFromTail(trimmed)
}

type orderQueryDateRange struct {
	StartAt   string
	EndAt     string
	StartDate string
	EndDate   string
}

func parseOrderQueryDateRange(payload string, loc *time.Location) (*orderQueryDateRange, string, error) {
	trimmed := strings.TrimSpace(payload)
	match := orderQueryDateRangeRegex.FindStringSubmatch(trimmed)
	if match == nil {
		return nil, trimmed, nil
	}
	queryLoc := loc
	if queryLoc == nil {
		queryLoc = time.UTC
	}
	startDay, err := time.ParseInLocation(orderQueryDateLayout, match[1], queryLoc)
	if err != nil {
		return nil, "", fmt.Errorf("开始日期格式错误，请使用 YYYY-MM-DD")
	}
	endDay, err := time.ParseInLocation(orderQueryDateLayout, match[2], queryLoc)
	if err != nil {
		return nil, "", fmt.Errorf("结束日期格式错误，请使用 YYYY-MM-DD")
	}
	if endDay.Before(startDay) {
		return nil, "", fmt.Errorf("结束日期不能早于开始日期")
	}
	rest := strings.TrimSpace(match[3])
	if strings.HasPrefix(rest, "+") {
		rest = strings.TrimSpace(strings.TrimPrefix(rest, "+"))
	}
	return &orderQueryDateRange{
		StartAt:   startDay.UTC().Format(orderQueryTimeLayout),
		EndAt:     endDay.AddDate(0, 0, 1).UTC().Format(orderQueryTimeLayout),
		StartDate: match[1],
		EndDate:   match[2],
	}, rest, nil
}

func parseOrderQueryPayload(payload string) (string, *float64) {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return "", nil
	}
	if idx := strings.LastIndex(payload, "+"); idx > 0 && idx < len(payload)-1 {
		keyword := strings.TrimSpace(payload[:idx])
		tail := strings.TrimSpace(payload[idx+1:])
		if v, err := strconv.ParseFloat(tail, 64); err == nil {
			return keyword, &v
		}
	}
	if v, err := strconv.ParseFloat(payload, 64); err == nil {
		return "", &v
	}
	return payload, nil
}

func almostZero(v float64) bool {
	return math.Abs(v) < 1e-12
}

func (a *App) recordRateTick(chatID int64, payType string, row *services.RateResult) {
	if a == nil || a.timeSeries == nil || !a.timeSeries.Enabled() || row == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeSeriesWriteTimeout)
	defer cancel()
	if err := a.timeSeries.RecordRateTick(ctx, services.RateTick{
		OccurredAt: time.Now().UTC(),
		ChatID:     chatID,
		Provider:   row.Provider,
		PayType:    payType,
		Price:      row.Price,
		Source:     row.Source,
	}); err != nil {
		log.Printf("写入汇率时序失败: chat_id=%d provider=%s err=%v", chatID, row.Provider, err)
	}
}

func (a *App) recordCapitalFlowTick(tick services.CapitalFlowTick) {
	if a == nil || a.timeSeries == nil || !a.timeSeries.Enabled() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeSeriesWriteTimeout)
	defer cancel()
	if err := a.timeSeries.RecordCapitalFlowTick(ctx, tick); err != nil {
		log.Printf("写入资金流向时序失败: chat_id=%d user_id=%d entry_type=%s err=%v", tick.ChatID, tick.UserID, tick.EntryType, err)
	}
}

func (a *App) handleLedger(msg *tgbotapi.Message, g db.GroupConfig, parsed *utils.LedgerParsed) error {
	blocked, err := a.enforcePayoutAddressWhitelist(msg, g, parsed)
	if err != nil {
		return err
	}
	if blocked {
		return nil
	}

	blocked, err = a.enforceExclusiveAddressWhitelist(msg, g, parsed)
	if err != nil {
		return err
	}
	if blocked {
		return nil
	}

	rate := a.resolveRate(g, parsed.EntryType, msg.From.ID)
	baseAmount := parsed.Amount
	if parsed.RateOverride != nil {
		rate = *parsed.RateOverride
	}
	baseFeeRate := g.FeeRate
	if parsed.EntryType == "payout" {
		baseFeeRate = g.PayoutFeeRate
	}
	feeRate := baseFeeRate
	if parsed.FeeRateOverride != nil {
		feeRate = *parsed.FeeRateOverride
	}
	compoundDepositGroupFields := compoundDepositGroupSettingFields(parsed, feeRate, rate)
	if len(compoundDepositGroupFields) > 0 {
		g.FeeRate = feeRate
		if parsed.RateOverride != nil {
			g.CustomRate = rate
			g.UseRealtimeRate = 0
		}
	}
	finalAmount := calculateLedgerFinalAmount(parsed.EntryType, baseAmount, feeRate)

	username := strings.ToLower(strings.TrimSpace(msg.From.UserName))
	nickname := composeNickname(msg.From.FirstName, msg.From.LastName)

	cardNo := utils.ExtractCardNo(parsed.Note)
	track := utils.BuildMessageLink(msg.Chat.ID, msg.MessageID)
	ledgerInput := db.LedgerEntryInput{
		ChatID:           msg.Chat.ID,
		MessageID:        msg.MessageID,
		UserID:           msg.From.ID,
		Username:         username,
		Nickname:         nickname,
		EntryType:        parsed.EntryType,
		Amount:           parsed.Amount,
		Currency:         parsed.Currency,
		BaseAmount:       baseAmount,
		FeeRate:          feeRate,
		Rate:             rate,
		FinalAmount:      finalAmount,
		Note:             parsed.Note,
		CardNo:           cardNo,
		TrackMessageLink: track,
	}
	transactionAt := a.currentTimeInLocation()
	workHours, workHoursConfigured := a.configuredWorkHours()
	timeAnomalyWarning := workHoursConfigured && !workHours.contains(transactionAt)
	result, err := a.db.ApplyLedgerEntryWithReceiptOutbox(ledgerInput, func(applyResult db.LedgerApplyResult) (*db.LedgerReceiptOutboxInput, error) {
		return a.buildLedgerReceiptOutboxInput(msg, g, parsed, ledgerInput, applyResult, transactionAt, timeAnomalyWarning)
	})
	if err != nil {
		return err
	}
	if result.Duplicate {
		fingerprint := db.BuildRequestFingerprint(msg.Chat.ID, msg.MessageID)
		detail := fmt.Sprintf("command=ledger chat_id=%d message_id=%d request_fingerprint=%s", msg.Chat.ID, msg.MessageID, fingerprint)
		_ = a.db.AddRiskEvent(msg.Chat.ID, msg.MessageID, msg.From.ID, "duplicate_message", "warn", detail)
		return a.replyBookkeepingResult(msg, "ledger", "重复消息已拦截，本次记账未重复入账。", false)
	}
	if len(compoundDepositGroupFields) > 0 {
		if err := a.db.UpdateGroupFields(msg.Chat.ID, compoundDepositGroupFields); err != nil {
			return err
		}
	}
	id := result.EntryID
	a.syncDailyLedgerIncrement(msg.Chat.ID, g.DayCutTime, parsed.EntryType, finalAmount, rate)
	a.recordCapitalFlowTick(services.CapitalFlowTick{
		OccurredAt:  transactionAt,
		ChatID:      msg.Chat.ID,
		UserID:      msg.From.ID,
		EntryType:   parsed.EntryType,
		Currency:    parsed.Currency,
		Amount:      parsed.Amount,
		BaseAmount:  baseAmount,
		FinalAmount: finalAmount,
		Rate:        rate,
		FeeRate:     feeRate,
	})
	if a.businessRiskControlsEnabled() {
		duplicateAmountWarning := result.RecentAmountSeenCount > 1
		duplicateCardWarning := cardNo != "" && result.RecentCardSeenCount > 1
		duplicateAddressWarning := result.RecentAddressSeenCount > 1
		if duplicateAmountWarning {
			detail := fmt.Sprintf(
				"entry_id=%d amount=%.8f currency=%s seen_count=%d window=%s",
				id,
				parsed.Amount,
				parsed.Currency,
				result.RecentAmountSeenCount,
				duplicateAlertWindowText,
			)
			_ = a.db.AddRiskEvent(msg.Chat.ID, msg.MessageID, msg.From.ID, "duplicate_amount", "warn", detail)
		}
		if duplicateCardWarning {
			scope := "chat"
			groupType := ""
			if g.DuplicateAlertEnabled == 1 && strings.TrimSpace(g.GroupType) != "" {
				scope = "group_type"
				groupType = strings.TrimSpace(g.GroupType)
			}
			detail := fmt.Sprintf(
				"entry_id=%d card_no=%s seen_count=%d window=%s scope=%s group_type=%s",
				id,
				cardNo,
				result.RecentCardSeenCount,
				duplicateAlertWindowText,
				scope,
				groupType,
			)
			_ = a.db.AddRiskEvent(msg.Chat.ID, msg.MessageID, msg.From.ID, "duplicate_card", "warn", detail)
		}
		if duplicateAddressWarning {
			detail := fmt.Sprintf(
				"entry_id=%d address=%s seen_count=%d window=%s",
				id,
				result.RecentAddress,
				result.RecentAddressSeenCount,
				duplicateAlertWindowText,
			)
			_ = a.db.AddRiskEvent(msg.Chat.ID, msg.MessageID, msg.From.ID, "duplicate_address", "warn", detail)
		}
		if timeAnomalyWarning {
			detail := fmt.Sprintf(
				"entry_id=%d local_time=%s timezone=%s work_hours=%s type=%s final_amount=%.8f",
				id,
				transactionAt.Format("2006-01-02 15:04:05"),
				a.timezoneLabel(),
				workHours.label(),
				parsed.EntryType,
				finalAmount,
			)
			_ = a.db.AddRiskEvent(msg.Chat.ID, msg.MessageID, msg.From.ID, "time_anomaly", "warn", detail)
			if err := a.notifyTimeAnomaly(msg, parsed, id, finalAmount, transactionAt, workHours); err != nil {
				log.Printf("发送异常时间管理员通知失败: %v", err)
			}
		}
	}
	if err := a.deliverLedgerReceiptInlineForDirectCall(); err != nil {
		return err
	}
	return nil
}

func calculateLedgerFinalAmount(entryType string, baseAmount, feeRate float64) float64 {
	return baseAmount * (1 - feeRate/100.0)
}

func compoundDepositGroupSettingFields(parsed *utils.LedgerParsed, feeRate, rate float64) map[string]any {
	if parsed == nil || parsed.EntryType != "deposit" || parsed.FeeRateOverride == nil {
		return nil
	}
	fields := map[string]any{"fee_rate": feeRate}
	if parsed.RateOverride != nil {
		fields["custom_rate"] = rate
		fields["use_realtime_rate"] = 0
	}
	return fields
}

func (a *App) resolveTieredFeeForUser(chatID, userID int64, baseRate, currentBaseAmount float64) tieredFeeDecision {
	projectedMonthlyTrade := math.Abs(currentBaseAmount)
	if a != nil && a.db != nil {
		monthlyTrade, err := a.db.GetUserMonthlyTradeVolume(chatID, userID, time.Now().UTC())
		if err == nil {
			projectedMonthlyTrade += monthlyTrade
		}
	}
	return calculateTieredFee(baseRate, projectedMonthlyTrade)
}

func (a *App) resolveRate(g db.GroupConfig, entryType string, userID int64) float64 {
	if entryType == "payout" {
		if oneShot, err := a.db.PopNextPayoutRate(g.ChatID, userID); err == nil && oneShot != nil && !almostZero(*oneShot) {
			return *oneShot
		}
		if !almostZero(g.PayoutRate) {
			return g.PayoutRate
		}
		return 1
	}
	if g.UseRealtimeRate != 1 {
		if !almostZero(g.CustomRate) {
			return g.CustomRate
		}
		return 1
	}
	row, err := a.exchange.GetRate(g.RateSource, "all", "CNY")
	if err != nil || row == nil {
		if !almostZero(g.CustomRate) {
			return g.CustomRate
		}
		return 1
	}
	a.recordRateTick(g.ChatID, "all", row)
	return row.Price
}

func (a *App) quickSetRate(msg *tgbotapi.Message) error {
	row, err := a.exchange.GetRate("okx", "all", "CNY")
	if err != nil || row == nil {
		return a.reply(msg.Chat.ID, msg.MessageID, "实时汇率查询失败，请稍后重试。")
	}
	a.recordRateTick(msg.Chat.ID, "all", row)
	_ = a.db.UpdateGroupFields(msg.Chat.ID, map[string]any{"custom_rate": row.Price, "use_realtime_rate": 0})
	return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("已一键设置汇率：%s", utils.FormatAmount(row.Price)))
}

type dailyLedgerSnapshot struct {
	summary ledgerSummary
	depRate map[string]float64
	payRate map[string]float64
}

func (a *App) dailyLedgerPeriodKey(dayCutTime string) string {
	if a == nil {
		return ""
	}
	return utils.GetDayStartISO(dayCutTime, a.cfg.Timezone)
}

func (a *App) currentDailyLedgerCounter() DailyLedgerCounter {
	if a == nil || a.dailyLedgerCounter == nil {
		return noopDailyLedgerCounter{}
	}
	return a.dailyLedgerCounter
}

func (a *App) activeDailyLedgerCounter() (DailyLedgerCounter, bool) {
	counter := a.currentDailyLedgerCounter()
	_, disabled := counter.(noopDailyLedgerCounter)
	return counter, !disabled
}

func (a *App) syncDailyLedgerIncrement(chatID int64, dayCutTime, entryType string, finalAmount, rate float64) {
	counter, enabled := a.activeDailyLedgerCounter()
	if !enabled {
		return
	}
	periodKey := a.dailyLedgerPeriodKey(dayCutTime)
	if periodKey == "" {
		return
	}
	if err := counter.Incr(context.Background(), chatID, periodKey, entryType, finalAmount, rate); err != nil {
		log.Printf("Redis 原子计数器增量写入失败: chat_id=%d period=%s type=%s err=%v", chatID, periodKey, entryType, err)
	}
}

func (a *App) rebuildDailyLedgerCounter(chatID int64, dayCutTime string) {
	_, enabled := a.activeDailyLedgerCounter()
	if !enabled {
		return
	}
	periodKey := a.dailyLedgerPeriodKey(dayCutTime)
	if periodKey == "" {
		return
	}
	if _, err := a.rebuildDailyLedgerSnapshot(chatID, periodKey); err != nil {
		log.Printf("Redis 原子计数器重建失败: chat_id=%d period=%s err=%v", chatID, periodKey, err)
	}
}

func (a *App) loadDailyLedgerRowsSnapshot(chatID int64, periodKey string) (dailyLedgerSnapshot, error) {
	rows, err := a.db.ListLedgerSince(chatID, periodKey)
	if err != nil {
		return dailyLedgerSnapshot{}, err
	}
	return buildDailyLedgerSnapshotFromRows(rows), nil
}

func (a *App) rebuildDailyLedgerSnapshot(chatID int64, periodKey string) (dailyLedgerSnapshot, error) {
	snapshot, err := a.loadDailyLedgerRowsSnapshot(chatID, periodKey)
	if err != nil {
		return dailyLedgerSnapshot{}, err
	}
	counter, enabled := a.activeDailyLedgerCounter()
	if !enabled {
		return snapshot, nil
	}
	if err := counter.Replace(
		context.Background(),
		chatID,
		periodKey,
		snapshot.summary,
		snapshot.depRate,
		snapshot.payRate,
	); err != nil {
		log.Printf("Redis 原子计数器回填失败: chat_id=%d period=%s err=%v", chatID, periodKey, err)
	}
	return snapshot, nil
}

func buildDailyLedgerSnapshotFromRows(rows []db.LedgerEntry) dailyLedgerSnapshot {
	out := dailyLedgerSnapshot{
		depRate: map[string]float64{},
		payRate: map[string]float64{},
	}
	for _, r := range rows {
		if r.EntryType == "deposit" {
			out.summary.depCount++
			out.summary.depTotal += r.FinalAmount
			rateKey := normalizeDailyLedgerRateField(r.Rate)
			out.depRate[rateKey] += r.FinalAmount
			continue
		}
		out.summary.payCount++
		out.summary.payTotal += r.FinalAmount
		rateKey := normalizeDailyLedgerRateField(r.Rate)
		out.payRate[rateKey] += r.FinalAmount
	}
	return out
}

func (a *App) loadDailyLedgerSummary(chatID int64, dayCutTime string) (ledgerSummary, error) {
	periodKey := a.dailyLedgerPeriodKey(dayCutTime)
	if periodKey == "" {
		return ledgerSummary{}, nil
	}
	counter, enabled := a.activeDailyLedgerCounter()
	if !enabled {
		snapshot, err := a.loadDailyLedgerRowsSnapshot(chatID, periodKey)
		if err != nil {
			return ledgerSummary{}, err
		}
		return snapshot.summary, nil
	}
	summary, ok, err := counter.Snapshot(context.Background(), chatID, periodKey)
	if err == nil && ok {
		return summary, nil
	}
	if err != nil {
		log.Printf("Redis 原子计数器读取失败，回退数据库重建: chat_id=%d period=%s err=%v", chatID, periodKey, err)
	}
	snapshot, rebuildErr := a.rebuildDailyLedgerSnapshot(chatID, periodKey)
	if rebuildErr != nil {
		return ledgerSummary{}, rebuildErr
	}
	return snapshot.summary, nil
}

func (a *App) loadDailyLedgerSnapshot(chatID int64, dayCutTime string) (dailyLedgerSnapshot, error) {
	periodKey := a.dailyLedgerPeriodKey(dayCutTime)
	if periodKey == "" {
		return dailyLedgerSnapshot{}, nil
	}
	counter, enabled := a.activeDailyLedgerCounter()
	if !enabled {
		return a.loadDailyLedgerRowsSnapshot(chatID, periodKey)
	}
	summary, ok, err := counter.Snapshot(context.Background(), chatID, periodKey)
	if err != nil {
		log.Printf("Redis 原子计数器读取失败，回退数据库重建: chat_id=%d period=%s err=%v", chatID, periodKey, err)
		return a.rebuildDailyLedgerSnapshot(chatID, periodKey)
	}
	if !ok {
		return a.rebuildDailyLedgerSnapshot(chatID, periodKey)
	}
	depRate, depOK, depErr := counter.RateStats(context.Background(), chatID, periodKey, "deposit")
	payRate, payOK, payErr := counter.RateStats(context.Background(), chatID, periodKey, "payout")
	if depErr == nil && !depOK && summary.depCount == 0 {
		depRate = map[string]float64{}
		depOK = true
	}
	if payErr == nil && !payOK && summary.payCount == 0 {
		payRate = map[string]float64{}
		payOK = true
	}
	if depErr != nil || payErr != nil || !depOK || !payOK {
		if depErr != nil {
			log.Printf("Redis 原子计数器读取入款汇率失败，回退数据库重建: chat_id=%d period=%s err=%v", chatID, periodKey, depErr)
		}
		if payErr != nil {
			log.Printf("Redis 原子计数器读取下发汇率失败，回退数据库重建: chat_id=%d period=%s err=%v", chatID, periodKey, payErr)
		}
		return a.rebuildDailyLedgerSnapshot(chatID, periodKey)
	}
	return dailyLedgerSnapshot{
		summary: summary,
		depRate: depRate,
		payRate: payRate,
	}, nil
}

func (a *App) showBill(msg *tgbotapi.Message, g db.GroupConfig) error {
	snapshot, err := a.loadDailyLedgerSnapshot(msg.Chat.ID, g.DayCutTime)
	if err != nil {
		return err
	}
	if snapshot.summary.depCount == 0 && snapshot.summary.payCount == 0 {
		return a.reply(msg.Chat.ID, msg.MessageID, "当前账期暂无数据。")
	}
	content := fmt.Sprintf(
		"一键账单（日切 %s）\n入款笔数：%d，总入款：%s\n下发笔数：%d，总下发：%s\n当前利润：%s\n入款汇率统计：%s\n下发汇率统计：%s",
		g.DayCutTime,
		snapshot.summary.depCount,
		utils.FormatAmount(snapshot.summary.depTotal),
		snapshot.summary.payCount,
		utils.FormatAmount(snapshot.summary.payTotal),
		utils.FormatAmount(snapshot.summary.depTotal-snapshot.summary.payTotal),
		joinRateStats(snapshot.depRate),
		joinRateStats(snapshot.payRate),
	)
	return a.reply(msg.Chat.ID, msg.MessageID, content)
}

func (a *App) exportLedgerFiles(msg *tgbotapi.Message, g db.GroupConfig) error {
	if msg == nil || msg.Chat == nil {
		return errors.New("invalid export message")
	}
	startAt := utils.GetDayStartISO(g.DayCutTime, a.cfg.Timezone)
	rows, err := a.db.ListLedgerSince(msg.Chat.ID, startAt)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return a.reply(msg.Chat.ID, msg.MessageID, "当前账期暂无数据，无法导出。")
	}
	files, err := a.getExportEngine().BuildLedgerExports(rows, services.LedgerExportOptions{
		ChatID:      msg.Chat.ID,
		GroupTitle:  g.Title,
		DayCutTime:  g.DayCutTime,
		Timezone:    a.timezoneLabel(),
		GeneratedAt: a.currentTime().UTC(),
	})
	if err != nil {
		return err
	}
	for _, file := range files {
		doc := tgbotapi.NewDocument(msg.Chat.ID, tgbotapi.FileBytes{
			Name:  file.Name,
			Bytes: file.Content,
		})
		doc.ReplyToMessageID = msg.MessageID
		doc.Caption = "账单导出文件"
		if err := a.sendWithPriority(outboundPriorityNormalChat, doc); err != nil {
			return err
		}
	}
	return nil
}

func joinRateStats(stats map[string]float64) string {
	if len(stats) == 0 {
		return "无"
	}
	keys := make([]string, 0, len(stats))
	for k := range stats {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s:%s", k, utils.FormatAmount(stats[k])))
	}
	return strings.Join(parts, "，")
}

type ledgerSummary struct {
	depCount int
	payCount int
	depTotal float64
	payTotal float64
}

type dimensionLedgerSummary struct {
	depCount int
	payCount int
	depTotal float64
	payTotal float64
}

type multiDimensionLedgerStats struct {
	byCurrency map[string]dimensionLedgerSummary
	byOperator map[string]dimensionLedgerSummary
	byPayType  map[string]dimensionLedgerSummary
}

func newMultiDimensionLedgerStats() multiDimensionLedgerStats {
	return multiDimensionLedgerStats{
		byCurrency: map[string]dimensionLedgerSummary{},
		byOperator: map[string]dimensionLedgerSummary{},
		byPayType:  map[string]dimensionLedgerSummary{},
	}
}

func (s *multiDimensionLedgerStats) addRows(rows []db.LedgerEntry) {
	if s == nil {
		return
	}
	for _, row := range rows {
		s.addEntry(row)
	}
}

func (s *multiDimensionLedgerStats) addEntry(row db.LedgerEntry) {
	if s == nil {
		return
	}
	entryType := strings.ToLower(strings.TrimSpace(row.EntryType))
	if entryType != "deposit" && entryType != "payout" {
		return
	}
	amount := row.FinalAmount
	accumulateDimensionLedgerSummary(s.byCurrency, normalizeStatsCurrency(row.Currency), entryType, amount)
	accumulateDimensionLedgerSummary(s.byOperator, formatStatsOperator(row.UserID, row.Username, row.Nickname), entryType, amount)
	accumulateDimensionLedgerSummary(s.byPayType, inferStatsPayType(row.Note, row.CardNo), entryType, amount)
}

func (s *multiDimensionLedgerStats) merge(other multiDimensionLedgerStats) {
	if s == nil {
		return
	}
	mergeDimensionLedgerSummary(s.byCurrency, other.byCurrency)
	mergeDimensionLedgerSummary(s.byOperator, other.byOperator)
	mergeDimensionLedgerSummary(s.byPayType, other.byPayType)
}

func mergeDimensionLedgerSummary(target map[string]dimensionLedgerSummary, source map[string]dimensionLedgerSummary) {
	for key, row := range source {
		current := target[key]
		current.depCount += row.depCount
		current.payCount += row.payCount
		current.depTotal += row.depTotal
		current.payTotal += row.payTotal
		target[key] = current
	}
}

func accumulateDimensionLedgerSummary(target map[string]dimensionLedgerSummary, key, entryType string, amount float64) {
	current := target[key]
	if entryType == "deposit" {
		current.depCount++
		current.depTotal += amount
	} else if entryType == "payout" {
		current.payCount++
		current.payTotal += amount
	}
	target[key] = current
}

func normalizeStatsCurrency(currency string) string {
	normalized := strings.ToUpper(strings.TrimSpace(currency))
	if normalized == "" {
		return "UNKNOWN"
	}
	return normalized
}

func formatStatsOperator(userID int64, username, nickname string) string {
	trimmedUsername := strings.TrimSpace(username)
	if trimmedUsername != "" {
		return fmt.Sprintf("@%s(%d)", trimmedUsername, userID)
	}
	trimmedNickname := strings.TrimSpace(nickname)
	if trimmedNickname != "" {
		return fmt.Sprintf("%s(%d)", trimmedNickname, userID)
	}
	if userID != 0 {
		return strconv.FormatInt(userID, 10)
	}
	return "未知操作人"
}

func inferStatsPayType(note, cardNo string) string {
	if strings.TrimSpace(cardNo) != "" {
		return "银行卡"
	}
	normalized := strings.ToLower(strings.TrimSpace(note))
	switch {
	case strings.Contains(normalized, "alipay"), strings.Contains(normalized, "zfb"), strings.Contains(normalized, "支付宝"):
		return "支付宝"
	case strings.Contains(normalized, "wechat"), strings.Contains(normalized, "微信"), strings.Contains(normalized, "wx"):
		return "微信"
	case strings.Contains(normalized, "bank"), strings.Contains(normalized, "card"), strings.Contains(normalized, "银行卡"), strings.Contains(normalized, "卡号"):
		return "银行卡"
	default:
		return "其他"
	}
}

func sortDimensionLedgerKeys(stats map[string]dimensionLedgerSummary) []string {
	keys := make([]string, 0, len(stats))
	for key := range stats {
		keys = append(keys, key)
	}
	sort.SliceStable(keys, func(i, j int) bool {
		left := stats[keys[i]]
		right := stats[keys[j]]
		leftTotal := left.depTotal + left.payTotal
		rightTotal := right.depTotal + right.payTotal
		if math.Abs(leftTotal-rightTotal) > 1e-9 {
			return leftTotal > rightTotal
		}
		return keys[i] < keys[j]
	})
	return keys
}

func appendDimensionLedgerStats(lines []string, title string, stats map[string]dimensionLedgerSummary) []string {
	lines = append(lines, title)
	if len(stats) == 0 {
		return append(lines, "- 无")
	}
	keys := sortDimensionLedgerKeys(stats)
	for _, key := range keys {
		row := stats[key]
		lines = append(lines, fmt.Sprintf("- %s：入款 %d 笔 / %s，下发 %d 笔 / %s，利润 %s",
			key,
			row.depCount,
			utils.FormatAmount(row.depTotal),
			row.payCount,
			utils.FormatAmount(row.payTotal),
			utils.FormatAmount(row.depTotal-row.payTotal),
		))
	}
	return lines
}

func (a *App) loadGroupLedgerRows(chatID int64, dayCutTime string) ([]db.LedgerEntry, error) {
	periodKey := a.dailyLedgerPeriodKey(dayCutTime)
	if periodKey == "" {
		return []db.LedgerEntry{}, nil
	}
	return a.db.ListLedgerSince(chatID, periodKey)
}

func (a *App) summarizeGroupLedger(chatID int64, dayCutTime string) (ledgerSummary, error) {
	return a.loadDailyLedgerSummary(chatID, dayCutTime)
}

func (a *App) showSingleGroupStats(msg *tgbotapi.Message, g db.GroupConfig) error {
	rows, err := a.loadGroupLedgerRows(msg.Chat.ID, g.DayCutTime)
	if err != nil {
		return err
	}
	snapshot := buildDailyLedgerSnapshotFromRows(rows)
	sum := snapshot.summary
	dimensionStats := newMultiDimensionLedgerStats()
	dimensionStats.addRows(rows)

	lines := []string{
		"单独群数据统计：",
		fmt.Sprintf("群名：%s", orDefault(g.Title, strconv.FormatInt(g.ChatID, 10))),
		fmt.Sprintf("群ID：%d", g.ChatID),
		fmt.Sprintf("日切：%s", g.DayCutTime),
		fmt.Sprintf("入款：%d 笔 / %s", sum.depCount, utils.FormatAmount(sum.depTotal)),
		fmt.Sprintf("下发：%d 笔 / %s", sum.payCount, utils.FormatAmount(sum.payTotal)),
		fmt.Sprintf("利润：%s", utils.FormatAmount(sum.depTotal-sum.payTotal)),
	}
	lines = appendDimensionLedgerStats(lines, "按币种统计：", dimensionStats.byCurrency)
	lines = appendDimensionLedgerStats(lines, "按操作人统计：", dimensionStats.byOperator)
	lines = appendDimensionLedgerStats(lines, "按支付方式统计：", dimensionStats.byPayType)
	return a.reply(msg.Chat.ID, msg.MessageID, strings.Join(lines, "\n"))
}

func (a *App) showSameTypeStats(msg *tgbotapi.Message, g db.GroupConfig) error {
	groupType := strings.TrimSpace(g.GroupType)
	if groupType == "" {
		return a.reply(msg.Chat.ID, msg.MessageID, "当前群未设置群类型，请先发送【设置群类型 类型名】。")
	}
	groups, err := a.db.ListGroupsByType(groupType)
	if err != nil {
		return err
	}
	if len(groups) == 0 {
		return a.reply(msg.Chat.ID, msg.MessageID, "未找到同类型群数据。")
	}
	totalDepCount := 0
	totalPayCount := 0
	totalDepAmount := 0.0
	totalPayAmount := 0.0
	aggregatedDimensions := newMultiDimensionLedgerStats()
	lines := []string{
		fmt.Sprintf("同类型群数据统计（类型：%s）", groupType),
		fmt.Sprintf("群数量：%d", len(groups)),
	}
	preview := 0
	for _, item := range groups {
		rows, err := a.loadGroupLedgerRows(item.ChatID, item.DayCutTime)
		if err != nil {
			continue
		}
		snapshot := buildDailyLedgerSnapshotFromRows(rows)
		sum := snapshot.summary
		totalDepCount += sum.depCount
		totalPayCount += sum.payCount
		totalDepAmount += sum.depTotal
		totalPayAmount += sum.payTotal
		groupDimensions := newMultiDimensionLedgerStats()
		groupDimensions.addRows(rows)
		aggregatedDimensions.merge(groupDimensions)
		if preview < 10 {
			lines = append(lines, fmt.Sprintf("- %s（%d） 入:%s 下:%s 利:%s",
				orDefault(item.Title, strconv.FormatInt(item.ChatID, 10)),
				item.ChatID,
				utils.FormatAmount(sum.depTotal),
				utils.FormatAmount(sum.payTotal),
				utils.FormatAmount(sum.depTotal-sum.payTotal),
			))
			preview++
		}
	}
	lines = append(lines,
		fmt.Sprintf("总入款：%d 笔 / %s", totalDepCount, utils.FormatAmount(totalDepAmount)),
		fmt.Sprintf("总下发：%d 笔 / %s", totalPayCount, utils.FormatAmount(totalPayAmount)),
		fmt.Sprintf("总利润：%s", utils.FormatAmount(totalDepAmount-totalPayAmount)),
	)
	lines = appendDimensionLedgerStats(lines, "按币种统计：", aggregatedDimensions.byCurrency)
	lines = appendDimensionLedgerStats(lines, "按操作人统计：", aggregatedDimensions.byOperator)
	lines = appendDimensionLedgerStats(lines, "按支付方式统计：", aggregatedDimensions.byPayType)
	return a.reply(msg.Chat.ID, msg.MessageID, strings.Join(lines, "\n"))
}

func (a *App) showUserSideInfo(msg *tgbotapi.Message, g db.GroupConfig, entryType string) error {
	startAt := utils.GetDayStartISO(g.DayCutTime, a.cfg.Timezone)
	rows, err := a.db.ListLedgerByTypeSince(msg.Chat.ID, entryType, startAt)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return a.reply(msg.Chat.ID, msg.MessageID, "暂无信息。")
	}
	total := 0.0
	for _, r := range rows {
		total += r.FinalAmount
	}
	label := "入款"
	if entryType == "payout" {
		label = "下发"
	}
	return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("%s信息：笔数 %d，总金额 %s", label, len(rows), utils.FormatAmount(total)))
}

func (a *App) clearData(msg *tgbotapi.Message, g db.GroupConfig, command string) error {
	startAt := utils.GetDayStartISO(g.DayCutTime, a.cfg.Timezone)
	var n int64
	var err error
	if command == "清理今日数据" {
		n, err = a.db.ClearLedgerSince(msg.Chat.ID, startAt)
	} else if command == "清理入款数据" {
		n, err = a.db.ClearLedgerTypeSince(msg.Chat.ID, "deposit", startAt)
	} else {
		n, err = a.db.ClearLedgerTypeSince(msg.Chat.ID, "payout", startAt)
	}
	if err != nil {
		return err
	}
	a.rebuildDailyLedgerCounter(msg.Chat.ID, g.DayCutTime)
	return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("%s完成，已删除 %d 条数据。", command, n))
}

func (a *App) handleClassMode(msg *tgbotapi.Message, g db.GroupConfig, lock bool) error {
	if err := a.applyClassModePermissions(msg.Chat.ID, lock); err != nil {
		return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("设置%s失败：%v", map[bool]string{true: "下课禁言", false: "上课解禁"}[lock], err))
	}
	return a.reply(msg.Chat.ID, msg.MessageID, classModeReplyText(g, lock))
}

func classModeReplyText(g db.GroupConfig, lock bool) string {
	if lock {
		ad := strings.TrimSpace(g.ClassEndAd)
		if ad == "" || ad == legacyDefaultClassEndAd {
			return currentDefaultClassEndAd
		}
		return ad
	}
	ad := strings.TrimSpace(g.ClassStartAd)
	if ad == "" || ad == legacyDefaultClassStartAd {
		return currentDefaultClassStartAd
	}
	return ad
}

type classModePermissionPayload struct {
	CanSendMessages       bool `json:"can_send_messages"`
	CanSendMediaMessages  bool `json:"can_send_media_messages"`
	CanSendPolls          bool `json:"can_send_polls"`
	CanSendOtherMessages  bool `json:"can_send_other_messages"`
	CanAddWebPagePreviews bool `json:"can_add_web_page_previews"`
	CanInviteUsers        bool `json:"can_invite_users"`
	CanPinMessages        bool `json:"can_pin_messages"`
	CanChangeInfo         bool `json:"can_change_info"`
}

func (a *App) applyClassModePermissions(chatID int64, lock bool) error {
	perm := classModePermissionPayload{
		CanSendMessages:       !lock,
		CanSendMediaMessages:  !lock,
		CanSendPolls:          !lock,
		CanSendOtherMessages:  !lock,
		CanAddWebPagePreviews: !lock,
		CanInviteUsers:        !lock,
		CanPinMessages:        !lock,
		CanChangeInfo:         !lock,
	}
	payload, err := json.Marshal(perm)
	if err != nil {
		return err
	}
	params := tgbotapi.Params{
		"chat_id":                          strconv.FormatInt(chatID, 10),
		"permissions":                      string(payload),
		"use_independent_chat_permissions": "true",
	}
	_, err = a.api.MakeRequest("setChatPermissions", params)
	return err
}

func (a *App) showOrderList(msg *tgbotapi.Message, g db.GroupConfig) error {
	startAt := utils.GetDayStartISO(g.DayCutTime, a.cfg.Timezone)
	rows, err := a.db.ListOrderList(msg.Chat.ID, startAt, 50)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return a.reply(msg.Chat.ID, msg.MessageID, "当前账期暂无订单。")
	}
	depTotal := 0.0
	payTotal := 0.0
	lines := []string{"查单列表（最近记录）："}
	for i, r := range rows {
		if i < 20 {
			lines = append(lines, fmt.Sprintf("#%d %s %s 备注:%s",
				r.ID,
				map[bool]string{true: "入款", false: "下发"}[r.EntryType == "deposit"],
				utils.FormatAmount(r.FinalAmount),
				orDefault(r.Note, "无"),
			))
		}
		if r.EntryType == "deposit" {
			depTotal += r.FinalAmount
		} else {
			payTotal += r.FinalAmount
		}
	}
	lines = append(lines, fmt.Sprintf("总入款:%s，总下发:%s，利润:%s", utils.FormatAmount(depTotal), utils.FormatAmount(payTotal), utils.FormatAmount(depTotal-payTotal)))
	return a.reply(msg.Chat.ID, msg.MessageID, strings.Join(lines, "\n"))
}

func (a *App) searchOrders(msg *tgbotapi.Message, g db.GroupConfig, text string) error {
	payload := strings.TrimSpace(strings.TrimPrefix(text, "自助查单"))
	startAt := utils.GetDayStartISO(g.DayCutTime, a.cfg.Timezone)
	rangeQuery, remainPayload, err := parseOrderQueryDateRange(payload, a.loc)
	if err != nil {
		return a.reply(msg.Chat.ID, msg.MessageID, err.Error())
	}
	keyword, amount := parseOrderQueryPayload(remainPayload)
	endAt := ""
	title := "查单结果（最多10条）："
	if rangeQuery != nil {
		startAt = rangeQuery.StartAt
		endAt = rangeQuery.EndAt
		title = fmt.Sprintf("查单结果（%s 至 %s，最多10条）：", rangeQuery.StartDate, rangeQuery.EndDate)
	}
	rows, err := a.db.SearchOrdersInRange(msg.Chat.ID, keyword, amount, startAt, endAt)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return a.reply(msg.Chat.ID, msg.MessageID, "未查询到匹配订单。")
	}
	lines := []string{title}
	for i, r := range rows {
		if i >= 10 {
			break
		}
		lines = append(lines, fmt.Sprintf("#%d %s %s 备注:%s",
			r.ID,
			map[bool]string{true: "入款", false: "下发"}[r.EntryType == "deposit"],
			utils.FormatAmount(r.FinalAmount),
			orDefault(r.Note, "无"),
		))
	}
	return a.reply(msg.Chat.ID, msg.MessageID, strings.Join(lines, "\n"))
}

func (a *App) addDelayedQuery(msg *tgbotapi.Message, text string) error {
	payload := strings.TrimSpace(strings.TrimPrefix(text, "延时查单"))
	if payload == "" && msg.ReplyToMessage != nil {
		payload = strings.TrimSpace(msg.ReplyToMessage.Text)
	}
	keyword, amount := parseOrderQueryPayload(payload)
	id, err := a.db.AddDelayedQuery(msg.Chat.ID, msg.From.ID, keyword, amount, payload)
	if err != nil {
		return err
	}
	return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("延时查单已创建，ID:%d 关键词:%s 金额:%s",
		id,
		orDefault(keyword, "无"),
		formatMaybeAmount(amount),
	))
}

func (a *App) listDelayedQueries(msg *tgbotapi.Message) error {
	rows, err := a.db.ListDelayedQueries(msg.Chat.ID, "pending")
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return a.reply(msg.Chat.ID, msg.MessageID, "暂无延时查单记录。")
	}
	lines := []string{"延时查单列表："}
	for i, r := range rows {
		if i >= 20 {
			break
		}
		lines = append(lines, fmt.Sprintf("#%d 关键词:%s 金额:%s 备注:%s 状态:%s",
			r.ID,
			orDefault(r.Keyword, "无"),
			formatMaybeAmount(r.Amount),
			orDefault(r.Note, "无"),
			r.Status,
		))
	}
	return a.reply(msg.Chat.ID, msg.MessageID, strings.Join(lines, "\n"))
}

func formatMaybeAmount(amount *float64) string {
	if amount == nil {
		return "无"
	}
	return utils.FormatAmount(*amount)
}

func reversalReceiptStatusLine(g db.GroupConfig, result db.LedgerReverseResult) string {
	if g.LedgerCardHideReversed == 1 {
		return "撤回成功。"
	}
	return fmt.Sprintf("撤回成功。原记录 #%d，反向记录 #%d", result.OriginalID, result.ReversalID)
}

func (a *App) reversalLast(msg *tgbotapi.Message, g db.GroupConfig, command string) error {
	targetType := ""
	if command == "撤回入款" {
		targetType = "deposit"
	}
	if command == "撤回下发" {
		targetType = "payout"
	}
	nick := composeNickname(msg.From.FirstName, msg.From.LastName)
	track := utils.BuildMessageLink(msg.Chat.ID, msg.MessageID)
	periodStartAt := utils.GetDayStartISO(g.DayCutTime, a.cfg.Timezone)
	result, err := a.db.ReverseLastLedgerInPeriod(
		msg.Chat.ID,
		msg.MessageID,
		msg.From.ID,
		msg.From.UserName,
		nick,
		targetType,
		track,
		periodStartAt,
	)
	if err != nil {
		return err
	}
	if result.Duplicate {
		fingerprint := db.BuildRequestFingerprint(msg.Chat.ID, msg.MessageID)
		detail := fmt.Sprintf("command=%s chat_id=%d message_id=%d request_fingerprint=%s", command, msg.Chat.ID, msg.MessageID, fingerprint)
		_ = a.db.AddRiskEvent(msg.Chat.ID, msg.MessageID, msg.From.ID, "duplicate_message", "warn", detail)
		return a.replyBookkeepingResult(msg, "reversal", "重复消息已拦截，本次撤回未重复执行。", false)
	}
	if result.NotFound {
		return a.replyBookkeepingResult(msg, "reversal", "没有可撤回的记录。", true)
	}
	if result.Settled {
		return a.replyBookkeepingResult(msg, "reversal", "仅允许撤回当前账期且未结算的记录。", true)
	}
	a.rebuildDailyLedgerCounter(msg.Chat.ID, g.DayCutTime)
	a.emitReversalState(msg, command, result)
	return a.replyAccountingDataCardWithStatus(
		msg,
		g,
		reversalReceiptStatusLine(g, result),
		"reversal",
		true,
	)
}

func (a *App) emitReversalState(msg *tgbotapi.Message, command string, result db.LedgerReverseResult) {
	if a == nil || a.stateBroadcaster == nil || msg == nil || msg.From == nil {
		return
	}
	event := LedgerReversalStateEvent{
		Event:          "ledger.reversal",
		ChatID:         msg.Chat.ID,
		MessageID:      msg.MessageID,
		OperatorUserID: msg.From.ID,
		Command:        strings.TrimSpace(command),
		OriginalID:     result.OriginalID,
		ReversalID:     result.ReversalID,
		OccurredAt:     time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := a.stateBroadcaster.BroadcastReversal(context.Background(), event); err != nil {
		log.Printf("撤回状态广播失败: chat=%d original=%d reversal=%d err=%v", msg.Chat.ID, result.OriginalID, result.ReversalID, err)
	}
}

func isReplayExpired(messageDate int, now time.Time, windowSeconds int) (bool, time.Duration) {
	if messageDate <= 0 || windowSeconds <= 0 {
		return false, 0
	}
	msgAt := time.Unix(int64(messageDate), 0).UTC()
	age := now.UTC().Sub(msgAt)
	if age <= 0 {
		return false, age
	}
	window := time.Duration(windowSeconds) * time.Second
	return age > window, age
}

func (a *App) guardCommandVelocity(msg *tgbotapi.Message, text string) bool {
	if a == nil || msg == nil || msg.From == nil {
		return true
	}
	checker := a.getCommandVelocityChecker()
	if checker == nil {
		return true
	}
	allowed, count := checker.Allow(msg.From.ID)
	if allowed {
		return true
	}
	detail := fmt.Sprintf(
		"user_id=%d count=%d threshold=%d window=%s text=%s",
		msg.From.ID,
		count,
		checker.MaxPerWindow(),
		checker.Window(),
		trimForAudit(text, 128),
	)
	if a.db != nil {
		_ = a.db.AddRiskEvent(msg.Chat.ID, msg.MessageID, msg.From.ID, "velocity_check_blocked", "warn", detail)
	}
	log.Printf(
		"高频指令已拦截: chat_id=%d user_id=%d message_id=%d count=%d threshold=%d",
		msg.Chat.ID,
		msg.From.ID,
		msg.MessageID,
		count,
		checker.MaxPerWindow(),
	)
	_ = a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("检测到指令发送频率过高（>%d次/秒），请稍后再试。", checker.MaxPerWindow()))
	return false
}

func (a *App) guardReplay(msg *tgbotapi.Message, action string) bool {
	if msg == nil {
		return true
	}
	expired, age := isReplayExpired(msg.Date, time.Now(), a.cfg.ReplayWindowSeconds)
	if !expired {
		return true
	}
	userID := int64(0)
	if msg.From != nil {
		userID = msg.From.ID
	}
	detail := fmt.Sprintf("action=%s age_sec=%d text=%s", strings.TrimSpace(action), int(age.Seconds()), trimForAudit(msg.Text, 128))
	_ = a.db.AddRiskEvent(msg.Chat.ID, msg.MessageID, userID, "replay_blocked", "warn", detail)
	_ = a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("消息已过期（>%d秒），为防重放已拒绝处理。", a.cfg.ReplayWindowSeconds))
	return false
}

func trimForAudit(text string, max int) string {
	out := strings.TrimSpace(text)
	if max <= 0 || len(out) <= max {
		return out
	}
	return out[:max]
}

func mapExchangeErrToReply(err error) string {
	if err == nil {
		return "汇率服务暂不可用，请稍后重试。"
	}
	switch services.ServiceErrorCodeOf(err) {
	case services.ErrNoData:
		return "当前报价不足，请稍后再试。"
	case services.ErrInvalidInput:
		return "汇率查询参数错误，请检查法币或交易所后重试。"
	case services.ErrTimeout, services.ErrUpstream:
		return "汇率服务暂不可用，请稍后重试。"
	default:
		return "汇率查询失败，请稍后重试。"
	}
}

type rateQueryFiat struct {
	Code  string
	Label string
}

func defaultRateQueryFiat() rateQueryFiat {
	return rateQueryFiat{Code: "CNY", Label: "人民币"}
}

func parseRateQueryFiat(raw string) (rateQueryFiat, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultRateQueryFiat(), true
	}
	normalized := strings.ToUpper(strings.ReplaceAll(trimmed, " ", ""))
	switch normalized {
	case "CNY", "CNH", "RMB", "人民币", "中国":
		return rateQueryFiat{Code: "CNY", Label: "人民币"}, true
	case "JPY", "日元", "日本":
		return rateQueryFiat{Code: "JPY", Label: "日元"}, true
	case "KRW", "韩元", "韩国":
		return rateQueryFiat{Code: "KRW", Label: "韩元"}, true
	case "VND", "越南", "越南盾":
		return rateQueryFiat{Code: "VND", Label: "越南盾"}, true
	case "INR", "印度", "印度卢比":
		return rateQueryFiat{Code: "INR", Label: "印度卢比"}, true
	case "EUR", "欧元":
		return rateQueryFiat{Code: "EUR", Label: "欧元"}, true
	case "USD", "美元", "美金":
		return rateQueryFiat{Code: "USD", Label: "美元"}, true
	default:
		return rateQueryFiat{}, false
	}
}

func formatRateQueryFiatLabel(fiat rateQueryFiat) string {
	label := strings.TrimSpace(fiat.Label)
	code := strings.TrimSpace(strings.ToUpper(fiat.Code))
	if label == "" {
		return code
	}
	if code == "" {
		return label
	}
	return fmt.Sprintf("%s (%s)", label, code)
}

func formatRateQueryCurrencyPair(fiat rateQueryFiat) string {
	code := strings.TrimSpace(strings.ToUpper(fiat.Code))
	if code == "" {
		code = defaultRateQueryFiat().Code
	}
	return code + "/USDT"
}

func supportedRateQueryFiatCodes() string {
	return "CNY / JPY / KRW / VND / INR / EUR / USD"
}

func supportedRateQueryFiats() []rateQueryFiat {
	return []rateQueryFiat{
		{Code: "CNY", Label: "人民币"},
		{Code: "USD", Label: "美元"},
		{Code: "EUR", Label: "欧元"},
		{Code: "JPY", Label: "日元"},
		{Code: "KRW", Label: "韩元"},
		{Code: "VND", Label: "越南盾"},
		{Code: "INR", Label: "印度卢比"},
	}
}

func parseRateQueryCommand(text string) (string, rateQueryFiat, bool) {
	trimmed := strings.TrimSpace(text)
	switch trimmed {
	case "设置欧易交易所", "设置欧意交易所", "设置火币交易所", "设置币安交易所":
		return trimmed, defaultRateQueryFiat(), true
	}

	bases := []string{"汇率对比", "z0", "lk", "lz", "lw", "欧易交易所", "欧意交易所", "火币交易所", "币安交易所"}
	for _, base := range bases {
		matched, tail := matchRateQueryBase(trimmed, base)
		if !matched {
			continue
		}
		if tail == "" {
			return base, defaultRateQueryFiat(), true
		}
		fiat, ok := parseRateQueryFiat(tail)
		if ok {
			return base, fiat, true
		}
		return base, rateQueryFiat{}, false
	}
	return "", rateQueryFiat{}, false
}

func matchRateQueryBase(trimmed, base string) (bool, string) {
	candidate := trimmed
	if isLatinRateQueryBase(base) {
		candidate = strings.ToLower(trimmed)
	}
	if candidate == base {
		return true, ""
	}
	if strings.HasPrefix(candidate, base) {
		return true, strings.TrimSpace(trimmed[len(base):])
	}
	return false, ""
}

func isLatinRateQueryBase(base string) bool {
	switch base {
	case "z0", "lk", "lz", "lw":
		return true
	default:
		return false
	}
}

func looksLikeRateQueryCommand(text string) bool {
	trimmed := strings.TrimSpace(text)
	switch trimmed {
	case "设置欧易交易所", "设置欧意交易所", "设置火币交易所", "设置币安交易所":
		return true
	}
	bases := []string{"汇率对比", "z0", "lk", "lz", "lw", "欧易交易所", "欧意交易所", "火币交易所", "币安交易所"}
	for _, base := range bases {
		if matched, _ := matchRateQueryBase(trimmed, base); matched {
			return true
		}
	}
	return false
}

func rateQueryCommandToken(baseCommand string) string {
	switch strings.TrimSpace(baseCommand) {
	case "汇率对比":
		return "cmp"
	case "z0":
		return "z0"
	case "lk":
		return "lk"
	case "lz":
		return "lz"
	case "lw":
		return "lw"
	case "欧易交易所", "欧意交易所":
		return "okx"
	case "火币交易所":
		return "huobi"
	case "币安交易所":
		return "binance"
	default:
		return ""
	}
}

func rateQueryCommandFromToken(token string) string {
	switch strings.TrimSpace(strings.ToLower(token)) {
	case "cmp":
		return "汇率对比"
	case "z0":
		return "z0"
	case "lk":
		return "lk"
	case "lz":
		return "lz"
	case "lw":
		return "lw"
	case "okx":
		return "欧易交易所"
	case "huobi":
		return "火币交易所"
	case "binance":
		return "币安交易所"
	default:
		return ""
	}
}

type rateQueryProviderOption struct {
	Source      string
	ButtonLabel string
	Command     string
}

func supportedRateQueryProviders() []rateQueryProviderOption {
	return []rateQueryProviderOption{
		{Source: "okx", ButtonLabel: "欧易", Command: "欧易交易所"},
		{Source: "huobi", ButtonLabel: "火币", Command: "火币交易所"},
		{Source: "binance", ButtonLabel: "币安", Command: "币安交易所"},
	}
}

func normalizeRateQueryProviderSource(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "okx", "欧易交易所", "欧意交易所":
		return "okx"
	case "huobi", "htx", "火币交易所":
		return "huobi"
	case "binance", "币安交易所":
		return "binance"
	default:
		return ""
	}
}

func rateQueryProviderSourceFromCommand(baseCommand string) string {
	switch strings.TrimSpace(baseCommand) {
	case "z0":
		return "binance"
	case "欧易交易所", "欧意交易所":
		return "okx"
	case "火币交易所":
		return "huobi"
	case "币安交易所":
		return "binance"
	default:
		return ""
	}
}

func rateQueryProviderButtonLabel(source string) string {
	switch normalizeRateQueryProviderSource(source) {
	case "okx":
		return "欧易"
	case "huobi":
		return "火币"
	case "binance":
		return "币安"
	default:
		return ""
	}
}

func defaultRateQueryProviderSource() string {
	return "binance"
}

func currentRateQueryProvider(baseCommand, providerOverride string) string {
	if source := normalizeRateQueryProviderSource(providerOverride); source != "" {
		return source
	}
	return normalizeRateQueryProviderSource(rateQueryProviderSourceFromCommand(baseCommand))
}

func buildRateQueryCallbackData(baseCommand, fiatCode string, providerOverride ...string) string {
	token := rateQueryCommandToken(baseCommand)
	if token == "" {
		return rateQueryCallbackPrefix + "noop"
	}
	fiat, ok := parseRateQueryFiat(fiatCode)
	if !ok {
		fiat = defaultRateQueryFiat()
	}
	provider := ""
	if len(providerOverride) > 0 {
		provider = normalizeRateQueryProviderSource(providerOverride[0])
	}
	if provider == "" {
		provider = "auto"
	}
	return rateQueryCallbackPrefix + token + ":" + fiat.Code + ":" + provider
}

func buildRateQuerySwitchMarkup(baseCommand string, currentFiat rateQueryFiat, currentProvider string) tgbotapi.InlineKeyboardMarkup {
	supported := supportedRateQueryFiats()
	rows := make([][]tgbotapi.InlineKeyboardButton, 0, 1+(len(supported)+2)/3)
	selectedProvider := normalizeRateQueryProviderSource(currentProvider)
	providerRow := make([]tgbotapi.InlineKeyboardButton, 0, len(supportedRateQueryProviders()))
	for _, provider := range supportedRateQueryProviders() {
		label := provider.ButtonLabel
		if provider.Source == selectedProvider {
			label = "✅ " + label
		}
		targetBaseCommand := baseCommand
		targetProvider := provider.Source
		if strings.TrimSpace(baseCommand) == "汇率对比" {
			targetBaseCommand = provider.Command
		}
		providerRow = append(
			providerRow,
			tgbotapi.NewInlineKeyboardButtonData(
				label,
				buildRateQueryCallbackData(targetBaseCommand, currentFiat.Code, targetProvider),
			),
		)
	}
	if len(providerRow) > 0 {
		rows = append(rows, providerRow)
	}
	row := make([]tgbotapi.InlineKeyboardButton, 0, 3)
	currentCode := strings.ToUpper(strings.TrimSpace(currentFiat.Code))
	for _, fiat := range supported {
		label := fiat.Label
		if strings.EqualFold(currentCode, fiat.Code) {
			label = "✅ " + label
		}
		row = append(
			row,
			tgbotapi.NewInlineKeyboardButtonData(
				label,
				buildRateQueryCallbackData(baseCommand, fiat.Code, selectedProvider),
			),
		)
		if len(row) == 3 {
			rows = append(rows, row)
			row = make([]tgbotapi.InlineKeyboardButton, 0, 3)
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func fallbackRateFromGroupConfig(g db.GroupConfig, payType, fiat string) (*services.RateResult, bool) {
	if strings.TrimSpace(strings.ToUpper(fiat)) != "" && !strings.EqualFold(strings.TrimSpace(fiat), "CNY") {
		return nil, false
	}
	rate := g.CustomRate
	if rate <= 0 {
		rate = g.PayoutRate
	}
	if rate <= 0 {
		return nil, false
	}
	return &services.RateResult{
		Provider:     "local",
		PayType:      payType,
		Price:        rate,
		Top10:        []float64{rate},
		Top10Quotes:  []services.RateQuote{{Merchant: "本群汇率", Price: rate}},
		Source:       "group_config",
		AvgPrice:     rate,
		SampleCount:  1,
		FetchedAt:    time.Now(),
		FallbackUsed: true,
	}, true
}

func mapGeoErrToReply(err *services.ServiceError) string {
	if err == nil {
		return "距离查询失败，请确认地址后重试。"
	}
	switch err.Code {
	case services.ErrInvalidInput:
		return "格式：距离上海浦东，或 距离A到B"
	case services.ErrNoData:
		return "地址无法识别，请简化后重试。"
	default:
		return "路线服务暂不可用，请稍后重试或简化地址。"
	}
}

func formatRateProviderDisplayName(provider string) string {
	switch strings.ToUpper(strings.TrimSpace(provider)) {
	case "OKX":
		return "Okx"
	case "HUOBI":
		return "Huobi"
	case "BINANCE":
		return "Binance"
	default:
		trimmed := strings.TrimSpace(provider)
		if trimmed == "" {
			return "Unknown"
		}
		lowered := strings.ToLower(trimmed)
		return strings.ToUpper(lowered[:1]) + lowered[1:]
	}
}

func formatRateTop10Details(row *services.RateResult, currencyPair string) string {
	if row == nil {
		return "暂无有效报价"
	}
	currencyPair = strings.TrimSpace(currencyPair)
	if len(row.Top10Quotes) > 0 {
		lines := make([]string, 0, len(row.Top10Quotes))
		for i, quote := range row.Top10Quotes {
			name := strings.TrimSpace(quote.Merchant)
			if name == "" {
				name = fmt.Sprintf("商家%d", i+1)
			}
			name = strings.ReplaceAll(name, "\n", " ")
			line := fmt.Sprintf("%d) %s %s  %s", i+1, utils.FormatAmount(quote.Price), currencyPair, name)
			lines = append(lines, html.EscapeString(line))
		}
		return strings.Join(lines, "\n")
	}
	if len(row.Top10) > 0 {
		top := make([]string, 0, len(row.Top10))
		for i, price := range row.Top10 {
			line := fmt.Sprintf("%d) %s %s  商家%d", i+1, utils.FormatAmount(price), currencyPair, i+1)
			top = append(top, html.EscapeString(line))
		}
		return strings.Join(top, "\n")
	}
	return "暂无有效报价"
}

func isKoreanSpotRateResult(row *services.RateResult, fiat rateQueryFiat) bool {
	return row != nil &&
		strings.EqualFold(strings.TrimSpace(fiat.Code), "KRW") &&
		strings.EqualFold(strings.TrimSpace(row.Provider), "korea_spot")
}

func formatKoreanSpotRateReplyCard(row *services.RateResult, requestedProvider string, fiat rateQueryFiat) string {
	if row == nil {
		return "汇率服务暂不可用，请稍后重试。"
	}
	sampleCount := row.SampleCount
	if sampleCount <= 0 {
		if len(row.Top10Quotes) > 0 {
			sampleCount = len(row.Top10Quotes)
		} else {
			sampleCount = len(row.Top10)
		}
	}
	requested := strings.ToUpper(strings.TrimSpace(requestedProvider))
	fiatLabelText := html.EscapeString(formatRateQueryFiatLabel(fiat))
	priceText := html.EscapeString(utils.FormatAmount(row.Price))
	currencyPairText := html.EscapeString(formatRateQueryCurrencyPair(fiat))
	lines := []string{
		"韩国交易所 实时汇率",
		"------------------------------",
		"市场类型：现货盘口",
		fmt.Sprintf("法币市场：%s", fiatLabelText),
		fmt.Sprintf("均价：%s %s", priceText, currencyPairText),
		fmt.Sprintf("样本数量：%d", sampleCount),
	}
	if requested != "" {
		lines = append(lines, fmt.Sprintf("请求交易所：%s", html.EscapeString(requested)))
	}
	lines = append(lines,
		"实际来源：韩国现货源",
		"------------------------------",
		"<b>今日韩国交易所实时交易汇率:</b>",
		fmt.Sprintf("<pre>%s</pre>", formatRateTop10Details(row, currencyPairText)),
	)
	return strings.Join(lines, "\n")
}

func formatRateReplyCard(provider, payLabel string, row *services.RateResult, requestedProvider string, fiat rateQueryFiat) string {
	if row == nil {
		return "汇率服务暂不可用，请稍后重试。"
	}
	if isKoreanSpotRateResult(row, fiat) {
		return formatKoreanSpotRateReplyCard(row, requestedProvider, fiat)
	}
	sampleCount := row.SampleCount
	if sampleCount <= 0 {
		if len(row.Top10Quotes) > 0 {
			sampleCount = len(row.Top10Quotes)
		} else {
			sampleCount = len(row.Top10)
		}
	}
	requested := strings.ToUpper(strings.TrimSpace(requestedProvider))
	actual := strings.ToUpper(strings.TrimSpace(provider))
	if actual == "" {
		actual = strings.ToUpper(strings.TrimSpace(row.Provider))
	}
	actualText := html.EscapeString(actual)
	payLabelText := html.EscapeString(payLabel)
	fiatLabelText := html.EscapeString(formatRateQueryFiatLabel(fiat))
	priceText := html.EscapeString(utils.FormatAmount(row.Price))
	currencyPairText := html.EscapeString(formatRateQueryCurrencyPair(fiat))
	lines := []string{
		fmt.Sprintf("%s 实时汇率", actualText),
		"------------------------------",
		fmt.Sprintf("支付方式：%s", payLabelText),
		fmt.Sprintf("法币市场：%s", fiatLabelText),
		fmt.Sprintf("Top10均价：%s %s", priceText, currencyPairText),
		fmt.Sprintf("样本数量：%d", sampleCount),
	}
	if requested != "" {
		lines = append(lines, fmt.Sprintf("请求交易所：%s", html.EscapeString(requested)))
	}
	if actual != "" {
		lines = append(lines, fmt.Sprintf("实际来源：%s", actualText))
	}
	if row.FallbackUsed && requested != "" && actual != "" && requested != actual {
		lines = append(lines, "状态：请求上游暂不可用，已自动回退可用交易所")
	}
	lines = append(lines,
		"------------------------------",
		fmt.Sprintf("<b>今日 %s 商家实时交易汇率 Top10:</b>", html.EscapeString(formatRateProviderDisplayName(actual))),
		fmt.Sprintf("<pre>%s</pre>", formatRateTop10Details(row, currencyPairText)),
	)
	return strings.Join(lines, "\n")
}

func (a *App) renderRateQueryResponse(chatID int64, g db.GroupConfig, normalized string, fiat rateQueryFiat, providerOverride string) (string, *tgbotapi.InlineKeyboardMarkup, error) {
	switch normalized {
	case "设置欧易交易所", "设置欧意交易所":
		normalized = "欧易交易所"
	case "设置火币交易所":
		normalized = "火币交易所"
	case "设置币安交易所":
		normalized = "币安交易所"
	}
	if normalized == "汇率对比" {
		rows, err := a.exchange.CompareRates("all", fiat.Code)
		if err != nil {
			return "", nil, err
		}
		header := "汇率对比（Top10均价）"
		if fiat.Code != "CNY" {
			header = fmt.Sprintf("汇率对比（Top10均价 / %s）", fiat.Code)
		}
		lines := []string{header}
		for _, r := range rows {
			rateRow := r
			if fiat.Code == "CNY" {
				a.recordRateTick(chatID, "all", &rateRow)
			}
			lines = append(lines, fmt.Sprintf("%s：%s", strings.ToUpper(r.Provider), utils.FormatAmount(r.Price)))
		}
		if len(rows) < 3 {
			lines = append(lines, "提示：部分交易所暂不可用，当前仅展示可用结果。")
		}
		markup := buildRateQuerySwitchMarkup(normalized, fiat, "")
		return strings.Join(lines, "\n"), &markup, nil
	}
	if normalized == "欧易交易所" || normalized == "欧意交易所" || normalized == "火币交易所" || normalized == "币安交易所" {
		source := currentRateQueryProvider(normalized, providerOverride)
		if source == "" {
			source = rateQueryProviderSourceFromCommand(normalized)
		}
		row, err := a.exchange.GetRate(source, "all", fiat.Code)
		if err != nil || row == nil {
			if err != nil {
				return "", nil, err
			}
			return "", nil, errors.New("empty rate result")
		}
		if fiat.Code == "CNY" {
			a.recordRateTick(chatID, "all", row)
		}
		markup := buildRateQuerySwitchMarkup(normalized, fiat, source)
		return formatRateReplyCard(row.Provider, "全部", row, source, fiat), &markup, nil
	}
	payMap := map[string]string{"z0": "all", "lk": "bank", "lz": "alipay", "lw": "wechat"}
	payType := payMap[normalized]
	if payType == "" {
		payType = "all"
	}
	payLabel := map[string]string{"all": "全部", "bank": "银行卡", "alipay": "支付宝", "wechat": "微信"}[payType]
	source := currentRateQueryProvider(normalized, providerOverride)
	if source == "" {
		source = normalizeRateQueryProviderSource(g.RateSource)
	}
	if source == "" {
		source = defaultRateQueryProviderSource()
	}
	row, err := a.exchange.GetRate(source, payType, fiat.Code)
	if err != nil || row == nil {
		if fallbackRow, ok := fallbackRateFromGroupConfig(g, payType, fiat.Code); ok {
			markup := buildRateQuerySwitchMarkup(normalized, fiat, source)
			return fmt.Sprintf(
				"实时汇率暂不可用，已回退群内汇率\n支付方式：%s\n法币市场：%s\n当前汇率：%s",
				payLabel,
				formatRateQueryFiatLabel(fiat),
				utils.FormatAmount(fallbackRow.Price),
			), &markup, nil
		}
		if err != nil {
			return "", nil, err
		}
		return "", nil, errors.New("empty rate result")
	}
	if fiat.Code == "CNY" {
		a.recordRateTick(chatID, payType, row)
	}
	markup := buildRateQuerySwitchMarkup(normalized, fiat, source)
	return formatRateReplyCard(row.Provider, payLabel, row, source, fiat), &markup, nil
}

func (a *App) handleRateQuery(msg *tgbotapi.Message, g db.GroupConfig, text string) error {
	normalized, fiat, ok := parseRateQueryCommand(text)
	if !ok {
		return a.reply(
			msg.Chat.ID,
			msg.MessageID,
			fmt.Sprintf("不支持的法币参数。支持法币：%s", supportedRateQueryFiatCodes()),
		)
	}
	switch normalized {
	case "设置欧易交易所", "设置欧意交易所":
		normalized = "欧易交易所"
	case "设置火币交易所":
		normalized = "火币交易所"
	case "设置币安交易所":
		normalized = "币安交易所"
	}
	if normalized == "欧易交易所" || normalized == "火币交易所" || normalized == "币安交易所" {
		source := map[string]string{"欧易交易所": "okx", "火币交易所": "huobi", "币安交易所": "binance"}[normalized]
		if strings.HasPrefix(strings.TrimSpace(text), "设置") {
			if !a.requirePrivileged(msg, g) {
				return nil
			}
			_ = a.db.UpdateGroupFields(msg.Chat.ID, map[string]any{"rate_source": source})
			return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("已切换交易所为 %s", strings.ToUpper(source)))
		}
	}
	replyText, markup, err := a.renderRateQueryResponse(msg.Chat.ID, g, normalized, fiat, "")
	if err != nil {
		return a.reply(msg.Chat.ID, msg.MessageID, mapExchangeErrToReply(err))
	}
	if markup != nil {
		return a.replyWithMarkupAndParseMode(msg.Chat.ID, msg.MessageID, replyText, *markup, telegramParseModeHTML)
	}
	return a.reply(msg.Chat.ID, msg.MessageID, replyText)
}

func (a *App) handleCardQuery(msg *tgbotapi.Message, no string) error {
	info, serr := a.card.QueryCardWithStatus(no)
	if serr != nil {
		switch serr.Code {
		case services.ErrInvalidInput:
			return a.reply(msg.Chat.ID, msg.MessageID, "请输入正确银行卡号，例如：查卡 6222xxxx")
		case services.ErrNoData:
			return a.reply(msg.Chat.ID, msg.MessageID, "未查询到该卡信息")
		default:
			return a.reply(msg.Chat.ID, msg.MessageID, "查卡服务暂不可用，请稍后重试")
		}
	}

	if strings.TrimSpace(info.BankPhone) == "" {
		info.BankPhone = services.BankCustomerServicePhone(info.BankCode, info.BankName)
	}

	result := services.BankCardTXTResult{
		CardNo:     info.CardNo,
		BankName:   info.BankName,
		CardType:   normalizeBankCardType(info.CardType),
		CardName:   info.CardName,
		CreditType: services.NormalizeCardCreditType(info.CardType),
		BankCode:   info.BankCode,
		BankPhone:  info.BankPhone,
		Province:   info.Province,
		City:       info.City,
	}
	content, formatErr := services.FormatBankCardNineFields(result)
	if formatErr != nil {
		return a.reply(msg.Chat.ID, msg.MessageID, formatErr.Error())
	}
	return a.reply(msg.Chat.ID, msg.MessageID, content)
}

func (a *App) lookupExactBranchForCard(chatID int64, info services.CardInfo) (*services.BranchLookupResult, error) {
	if a.db != nil {
		override, err := a.db.GetBranchCodeOverride(chatID, info.CardNo)
		if err != nil {
			return nil, err
		}
		if override != nil {
			return &services.BranchLookupResult{
				CNAPSCode:  override.CNAPSCode,
				BankName:   info.BankName,
				BranchName: override.BranchName,
				Province:   info.Province,
				City:       info.City,
				Source:     "群内手工映射",
			}, nil
		}
	}
	if a.card == nil {
		return nil, services.ErrBranchLookupNotConfigured
	}
	return a.card.LookupBranchCodeForCard(info)
}

func normalizeBankCardType(cardType string) string {
	switch strings.ToLower(strings.TrimSpace(cardType)) {
	case "debit", "debit card", "dc", "储蓄卡", "借记卡":
		return "借记卡"
	case "credit", "credit card", "cc":
		return "贷记卡"
	case "贷记卡", "信用卡", "准贷记卡":
		return strings.TrimSpace(cardType)
	default:
		return strings.TrimSpace(cardType)
	}
}

func (a *App) handleSetBranchCodeOverride(msg *tgbotapi.Message, text string) error {
	cardNo, cnaps, branchName, ok := parseBranchOverrideInput(text)
	if !ok {
		return a.reply(msg.Chat.ID, msg.MessageID, "格式：设置联行号+银行卡号+联行号+支行名\n示例：设置联行号+6222001234567890+102100099996+中国工商银行深圳南山支行")
	}
	if err := a.db.SetBranchCodeOverride(msg.Chat.ID, cardNo, cnaps, branchName, "", msg.From.ID); err != nil {
		return err
	}
	info := a.card.QueryCard(cardNo)
	lines := []string{
		"联行号映射已保存。",
		fmt.Sprintf("银行卡号：%s", cardNo),
		fmt.Sprintf("联行号：%s", cnaps),
		fmt.Sprintf("支行：%s", orDefault(branchName, "未填写")),
		fmt.Sprintf("银行：%s", orDefault(info.BankName, "未知")),
		"后续查联行号将优先返回该映射结果。",
	}
	return a.reply(msg.Chat.ID, msg.MessageID, strings.Join(lines, "\n"))
}

func (a *App) handleDeleteBranchCodeOverride(msg *tgbotapi.Message, text string) error {
	cardNo := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(text, "删除联行号+"), "删除联行号"))
	cardNo = digitsOnly(cardNo)
	if cardNo == "" {
		return a.reply(msg.Chat.ID, msg.MessageID, "格式：删除联行号+银行卡号")
	}
	affected, err := a.db.DeleteBranchCodeOverride(msg.Chat.ID, cardNo)
	if err != nil {
		return err
	}
	if affected == 0 {
		return a.reply(msg.Chat.ID, msg.MessageID, "未找到该银行卡号的联行号映射。")
	}
	return a.reply(msg.Chat.ID, msg.MessageID, "联行号映射已删除。")
}

func (a *App) queryBranchCode(chatID int64, cardNo string) string {
	cardNo = digitsOnly(cardNo)
	if cardNo == "" {
		return "银行卡号格式不正确。"
	}
	override, err := a.db.GetBranchCodeOverride(chatID, cardNo)
	if err == nil && override != nil {
		info := a.card.QueryCard(cardNo)
		lines := []string{
			"联行号查询结果：",
			fmt.Sprintf("银行卡号：%s", cardNo),
			fmt.Sprintf("银行名称：%s", orDefault(info.BankName, "未知")),
			fmt.Sprintf("银行代码：%s", orDefault(info.BankCode, "未知")),
			"来源：群内手工映射",
			fmt.Sprintf("联行号：%s", override.CNAPSCode),
			fmt.Sprintf("支行：%s", orDefault(override.BranchName, "未填写")),
		}
		return strings.Join(lines, "\n")
	}
	return a.card.QueryBranchCode(cardNo)
}

func parseBranchOverrideInput(text string) (cardNo string, cnaps string, branchName string, ok bool) {
	payload := strings.TrimSpace(strings.TrimPrefix(text, "设置联行号"))
	payload = strings.TrimSpace(strings.TrimPrefix(payload, "+"))
	if payload == "" {
		return "", "", "", false
	}
	parts := make([]string, 0, 4)
	for _, p := range strings.Split(payload, "+") {
		p = strings.TrimSpace(p)
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) < 2 {
		fields := strings.Fields(payload)
		if len(fields) < 2 {
			return "", "", "", false
		}
		parts = fields
	}
	cardNo = digitsOnly(parts[0])
	cnaps = digitsOnly(parts[1])
	if len(parts) >= 3 {
		branchName = strings.TrimSpace(strings.Join(parts[2:], " "))
	}
	if cardNo == "" || cnaps == "" {
		return "", "", "", false
	}
	return cardNo, cnaps, branchName, true
}

func digitsOnly(v string) string {
	var b strings.Builder
	b.Grow(len(v))
	for _, r := range v {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (a *App) handleTRC20Query(msg *tgbotapi.Message, text string) error {
	address := strings.TrimSpace(text)
	for _, prefix := range []string{"查询USDT-TRC20最近交易记录", "查询TRC20", "查询链上", "链上查询", "trc20", "TRC20"} {
		if strings.HasPrefix(address, prefix) {
			address = strings.TrimSpace(strings.TrimPrefix(address, prefix))
			break
		}
	}
	if address == "" && msg.ReplyToMessage != nil {
		address = strings.TrimSpace(msg.ReplyToMessage.Text)
	}
	if address == "" {
		return a.reply(msg.Chat.ID, msg.MessageID, "格式：查询链上 地址（或 查询TRC20 地址）")
	}

	cardText, markup, err := a.renderTRC20QueryCard(address)
	if err != nil {
		return a.reply(msg.Chat.ID, msg.MessageID, "链上核验失败：请确认地址格式或稍后重试。")
	}
	m := tgbotapi.NewMessage(msg.Chat.ID, cardText)
	m.ReplyToMessageID = msg.MessageID
	m.ParseMode = telegramParseModeMarkdownV
	m.DisableWebPagePreview = true
	if markup != nil {
		m.ReplyMarkup = *markup
	}
	return a.sendWithPriority(outboundPriorityNormalChat, m)
}

func (a *App) renderTRC20QueryCard(address string) (string, *tgbotapi.InlineKeyboardMarkup, error) {
	status, err := a.address.QueryOnChainStatus(address)
	if err != nil {
		return "", nil, err
	}
	var (
		rows      []map[string]any
		recentErr error
	)
	if strings.EqualFold(strings.TrimSpace(status.Network), "TRC20") {
		rows, recentErr = a.address.FetchTRC20Recent(address, 5)
	}
	text := buildTRC20QueryReply(status, rows, recentErr, a.loc)
	markup := trc20QueryActionMarkup(address)
	return text, &markup, nil
}

func (a *App) replyTRC20AnalysisReport(chatID int64, replyTo int, address string) error {
	if a == nil || a.address == nil {
		return errors.New("链上查询服务未初始化")
	}
	validation := a.address.Validate(address)
	if !validation.Valid && !validation.Blacklisted {
		return errors.New(validation.Reason)
	}
	status, err := a.address.QueryOnChainStatus(address)
	if err != nil {
		return err
	}
	rows, recentErr := a.address.FetchTRC20Recent(address, 50)
	report := buildTRC20AnalysisReport(status, validation, rows, recentErr, a.loc, a.currentTime())
	for idx, chunk := range splitTRC20PlainTextReport(report, trc20PlainTextReportChunkRunes) {
		targetReplyTo := replyTo
		if idx > 0 {
			targetReplyTo = 0
		}
		if err := a.reply(chatID, targetReplyTo, chunk); err != nil {
			return err
		}
	}
	return nil
}

func buildTRC20QueryReply(status services.OnChainInquiry, rows []map[string]any, recentErr error, loc *time.Location) string {
	network := strings.ToUpper(strings.TrimSpace(status.Network))
	if network == "" {
		network = "TRC20"
	}
	stateIcon := "⚪"
	stateText := "不活跃"
	if status.Active {
		stateIcon = "🟢"
		stateText = "活跃"
	}
	txCountText := "0笔"
	if status.TransactionCount > 0 {
		txCountText = fmt.Sprintf("%d笔", status.TransactionCount)
	}
	registeredAt := "未知"
	if t := formatTRC20Time(status.CreatedAtUnix, loc); t != "" {
		registeredAt = t
	}
	usdtBalance := normalizeTRC20Amount(status.Balance)
	trxBalance := normalizeTRC20Amount(status.NativeBalance)
	lines := []string{
		fmt.Sprintf("🏦 *地址档案 ｜ %s*", escapeTelegramMarkdownV2(network)),
		fmt.Sprintf("%s 状态：%s  ·  📊 交易数：%s", stateIcon, escapeTelegramMarkdownV2(stateText), escapeTelegramMarkdownV2(txCountText)),
		fmt.Sprintf("⏳ 注册时间：%s", markdownInlineCode(registeredAt)),
		"",
		"🔗 *钱包地址*（点击复制）",
		markdownInlineCode(orDefault(status.Address, "未知")),
		"",
		"💳 *当前余额*",
		fmt.Sprintf("💵 *%s* `USDT`", escapeTelegramMarkdownV2(usdtBalance)),
		fmt.Sprintf("💎 *%s* `TRX`", escapeTelegramMarkdownV2(orDefault(trxBalance, "0"))),
		"━━━━━━━━━━━━━━━━━━━━",
	}
	if network != "TRC20" {
		lines = append(lines, "🧾 *最近流水*")
		lines = append(lines, "该网络暂未接入近期流水明细。")
		return strings.Join(lines, "\n")
	}

	lines = append(lines, "🧾 *最近 5 笔 USDT 流水*")
	if len(rows) == 0 {
		if recentErr != nil {
			lines = append(lines, "暂无法获取最近流水，请稍后重试。")
		} else {
			lines = append(lines, "暂无 USDT 流水。")
		}
		return strings.Join(lines, "\n")
	}
	for idx, row := range rows {
		if idx >= 5 {
			break
		}
		from := strings.TrimSpace(fmt.Sprintf("%v", row["from"]))
		to := strings.TrimSpace(fmt.Sprintf("%v", row["to"]))
		directionText, sign := inferTRC20Direction(status.Address, from, to)
		directionIcon := "⚪"
		switch directionText {
		case "转入":
			directionIcon = "🟢"
		case "转出":
			directionIcon = "🔴"
		}
		amount := normalizeTRC20Amount(fmt.Sprintf("%v", row["amount"]))
		displayAmount := amount
		if sign != "" && amount != "0" {
			displayAmount = sign + amount + " U"
		} else {
			displayAmount = amount + " U"
		}
		occurredAt := formatTRC20AnyTimestampShort(row["timestamp"], loc)
		if occurredAt == "" {
			occurredAt = "未知"
		}
		lines = append(lines, "")
		lines = append(lines, fmt.Sprintf("%d\\. %s *%s* ｜ 🕒 %s", idx+1, directionIcon, escapeTelegramMarkdownV2(displayAmount), markdownInlineCode(occurredAt)))
		lines = append(lines, fmt.Sprintf("📤 从：%s", markdownInlineCode(shortenTRC20Address(from))))
		lines = append(lines, fmt.Sprintf("📥 至：%s", markdownInlineCode(shortenTRC20Address(to))))
		txid := strings.TrimSpace(fmt.Sprintf("%v", row["txid"]))
		lines = append(lines, fmt.Sprintf("🔗 哈希：%s", markdownInlineCode(shortenTRC20TxID(txid))))
		if idx < len(rows)-1 && idx < 4 {
			lines = append(lines, "")
		}
	}
	return strings.Join(lines, "\n")
}

func trc20QueryActionMarkup(address string) tgbotapi.InlineKeyboardMarkup {
	addr := strings.TrimSpace(address)
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 刷新数据", buildTRC20QueryCallbackData(trc20ActionRefresh, addr)),
			tgbotapi.NewInlineKeyboardButtonData("📈 生成图表报告", buildTRC20QueryCallbackData(trc20ActionChart, addr)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬅️ 返回主菜单", buildTRC20QueryCallbackData(trc20ActionMenu, addr)),
		),
	)
}

func buildTRC20QueryCallbackData(action, address string) string {
	return trc20QueryCallbackPrefix + strings.TrimSpace(action) + ":" + strings.TrimSpace(address)
}

func markdownInlineCode(raw string) string {
	text := strings.TrimSpace(raw)
	text = strings.ReplaceAll(text, "`", "")
	if text == "" {
		text = "未知"
	}
	return "`" + text + "`"
}

func buildTronScanTransactionURL(txid string) string {
	id := strings.TrimSpace(txid)
	if id == "" || id == "<nil>" {
		return ""
	}
	return "https://tronscan.org/#/transaction/" + url.PathEscape(id)
}

func buildTRC20RiskAssessmentReply(validation services.AddressValidation, status services.OnChainInquiry) string {
	level := "低"
	reason := "未命中已知风险规则。"
	if validation.Blacklisted {
		level = "高"
		reason = validation.Reason
	} else if !status.Active && !status.HasBalance {
		level = "中"
		reason = "地址不活跃且余额为 0，建议二次核验收款方。"
	}
	return strings.Join([]string{
		"⚠️ 风险评估",
		fmt.Sprintf("地址：%s", status.Address),
		fmt.Sprintf("风险等级：%s", level),
		fmt.Sprintf("黑名单命中：%s", map[bool]string{true: "是", false: "否"}[validation.Blacklisted]),
		fmt.Sprintf("链上活跃：%s", map[bool]string{true: "是", false: "否"}[status.Active]),
		fmt.Sprintf("USDT余额：%s U", normalizeTRC20Amount(status.Balance)),
		fmt.Sprintf("TRX余额：%s TRX", normalizeTRC20Amount(status.NativeBalance)),
		fmt.Sprintf("说明：%s", reason),
	}, "\n")
}

func normalizeTRC20Amount(raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" || text == "<nil>" {
		return "0"
	}
	if strings.HasPrefix(text, "+") {
		text = strings.TrimSpace(strings.TrimPrefix(text, "+"))
	}
	if strings.HasPrefix(text, "-") {
		text = strings.TrimSpace(strings.TrimPrefix(text, "-"))
	}
	if text == "" {
		return "0"
	}
	if strings.ContainsAny(text, "eE") {
		if f, err := strconv.ParseFloat(text, 64); err == nil {
			return utils.FormatAmount(math.Abs(f))
		}
	}
	if !strings.Contains(text, ".") {
		return text
	}
	parts := strings.SplitN(text, ".", 2)
	frac := strings.TrimRight(parts[1], "0")
	if frac == "" {
		return parts[0]
	}
	return parts[0] + "." + frac
}

func inferTRC20Direction(address, from, to string) (string, string) {
	switch {
	case strings.EqualFold(strings.TrimSpace(to), strings.TrimSpace(address)) &&
		!strings.EqualFold(strings.TrimSpace(from), strings.TrimSpace(address)):
		return "转入", "+"
	case strings.EqualFold(strings.TrimSpace(from), strings.TrimSpace(address)) &&
		!strings.EqualFold(strings.TrimSpace(to), strings.TrimSpace(address)):
		return "转出", "-"
	default:
		return "相关", ""
	}
}

func formatTRC20Time(unix int64, loc *time.Location) string {
	if unix <= 0 {
		return ""
	}
	if unix >= 1_000_000_000_000 {
		unix /= 1000
	}
	if loc == nil {
		loc = time.Local
	}
	return time.Unix(unix, 0).In(loc).Format("2006-01-02 15:04:05")
}

func formatTRC20AnyTimestamp(raw any, loc *time.Location) string {
	if unix := parseTRC20AnyTimestamp(raw); unix > 0 {
		return formatTRC20Time(unix, loc)
	}
	return ""
}

func formatTRC20AnyTimestampShort(raw any, loc *time.Location) string {
	unix := parseTRC20AnyTimestamp(raw)
	if unix <= 0 {
		return ""
	}
	if loc == nil {
		loc = time.Local
	}
	return time.Unix(unix, 0).In(loc).Format("01-02 15:04")
}

func parseTRC20AnyTimestamp(raw any) int64 {
	text := strings.TrimSpace(fmt.Sprintf("%v", raw))
	if text == "" || text == "<nil>" {
		return 0
	}
	if i, err := strconv.ParseInt(text, 10, 64); err == nil {
		if i >= 1_000_000_000_000 {
			return i / 1000
		}
		return i
	}
	if f, err := strconv.ParseFloat(text, 64); err == nil {
		i := int64(f)
		if i >= 1_000_000_000_000 {
			return i / 1000
		}
		return i
	}
	if parsed, err := time.Parse(time.RFC3339, text); err == nil {
		return parsed.Unix()
	}
	if parsed, err := time.Parse("2006-01-02 15:04:05", text); err == nil {
		return parsed.Unix()
	}
	return 0
}

func shortenTRC20Address(address string) string {
	addr := strings.TrimSpace(address)
	if addr == "" || addr == "<nil>" {
		return "未知"
	}
	return addr
}

func shortenTRC20TxID(txid string) string {
	id := strings.TrimSpace(txid)
	if id == "" || id == "<nil>" {
		return "未知"
	}
	if len(id) <= 18 {
		return id
	}
	return id[:10] + "..." + id[len(id)-6:]
}

func (a *App) handleDistanceQuery(msg *tgbotapi.Message, text string) error {
	payload := ""
	lower := strings.ToLower(text)
	if strings.HasPrefix(lower, "jl") {
		payload = strings.TrimSpace(text[2:])
	} else {
		payload = strings.TrimSpace(strings.TrimPrefix(text, "距离"))
	}
	source := "上海浦东"
	target := payload
	if msg.ReplyToMessage != nil && strings.TrimSpace(msg.ReplyToMessage.Text) != "" {
		source = strings.TrimSpace(msg.ReplyToMessage.Text)
	}
	if strings.Contains(payload, "到") {
		parts := strings.SplitN(payload, "到", 2)
		source = strings.TrimSpace(parts[0])
		target = strings.TrimSpace(parts[1])
	}
	if strings.TrimSpace(target) == "" {
		return a.reply(msg.Chat.ID, msg.MessageID, "格式：距离上海浦东，或 距离A到B")
	}
	if strings.TrimSpace(source) == "" {
		return a.reply(msg.Chat.ID, msg.MessageID, "格式：距离上海浦东，或 距离A到B")
	}
	row, serr := a.geo.RouteDistance(source, target)
	if serr != nil {
		return a.reply(msg.Chat.ID, msg.MessageID, mapGeoErrToReply(serr))
	}
	if row == nil {
		return a.reply(msg.Chat.ID, msg.MessageID, "距离查询失败，请确认地址后重试。")
	}
	durationText := fmt.Sprintf("%d 分钟", row.DurationMin)
	if row.DurationMin >= 60 {
		h := row.DurationMin / 60
		m := row.DurationMin % 60
		durationText = fmt.Sprintf("%d小时%d分钟", h, m)
	}
	return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("距离结果：%s -> %s\n路线距离：%.1f 公里\n预计时长：%s", row.ResolvedFrom, row.ResolvedTo, row.DistanceKM, durationText))
}

func (a *App) handleWorldTime(msg *tgbotapi.Message, region string) error {
	row, err := a.worldTime.Query(region)
	if err != nil {
		return a.reply(msg.Chat.ID, msg.MessageID, "暂不支持该地区，请尝试：北京时间、巴西时间、德国时间、美国中部时间。")
	}
	timezoneLabel := strings.TrimSpace(row.TimezoneLabel)
	if timezoneLabel == "" {
		timezoneLabel = strings.TrimSpace(row.Timezone)
	}
	return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("%s时间：%s\n星期：%s\n时区：%s", row.Region, row.TimeText, row.Weekday, timezoneLabel))
}

func (a *App) handleTimer(msg *tgbotapi.Message, text string) error {
	if text == "开始计时" {
		_ = a.db.StartTimer(msg.Chat.ID, msg.From.ID, utils.UTCNowISO())
		return a.reply(msg.Chat.ID, msg.MessageID, "已开始计时。")
	}
	started, ok, err := a.db.EndTimer(msg.Chat.ID, msg.From.ID)
	if err != nil {
		return err
	}
	if !ok {
		return a.reply(msg.Chat.ID, msg.MessageID, "你还没有开始计时。")
	}
	st, err := time.Parse("2006-01-02T15:04:05", started)
	if err != nil {
		return a.reply(msg.Chat.ID, msg.MessageID, "计时结束。")
	}
	d := time.Since(st.UTC())
	return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("计时结束，用时：%s", formatDuration(d)))
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

func (a *App) showTracking(msg *tgbotapi.Message, g db.GroupConfig) error {
	startAt := utils.GetDayStartISO(g.DayCutTime, a.cfg.Timezone)
	rows, err := a.db.ListLedgerSince(msg.Chat.ID, startAt)
	if err != nil {
		return err
	}
	out := []string{"记账信息跟踪："}
	for _, r := range rows {
		if strings.TrimSpace(r.TrackLink) != "" {
			out = append(out, fmt.Sprintf("#%d %s", r.ID, r.TrackLink))
		}
	}
	if len(out) == 1 {
		return a.reply(msg.Chat.ID, msg.MessageID, "暂无跟踪记录。")
	}
	if len(out) > 11 {
		out = append([]string{out[0]}, out[len(out)-10:]...)
	}
	return a.reply(msg.Chat.ID, msg.MessageID, strings.Join(out, "\n"))
}

func (a *App) handleConvertTemplate(msg *tgbotapi.Message, text string) error {
	parts := strings.Split(strings.TrimPrefix(text, "换算-"), "-")
	if len(parts) != 3 {
		return a.reply(msg.Chat.ID, msg.MessageID, "格式：换算-币价-汇率-下发，例如：换算-7.2-7.15-10000")
	}
	coinPrice, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	rate, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	payout, err3 := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
	if err1 != nil || err2 != nil || err3 != nil || almostZero(rate) {
		return a.reply(msg.Chat.ID, msg.MessageID, "换算参数必须为有效数字且汇率不能为 0。")
	}
	usdt := payout / rate
	revenue := (coinPrice - rate) * usdt
	profitPct := 0.0
	if !almostZero(payout) {
		profitPct = revenue / payout * 100
	}
	return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("换算结果：\nUSDT数量：%su\n利润：%s\n利润率：%s%%", utils.FormatAmount(usdt), utils.FormatAmount(revenue), utils.FormatAmount(profitPct)))
}

func (a *App) setAddress(msg *tgbotapi.Message, text, kind string) error {
	return a.setAddressWithSuccessLine(msg, text, kind, "")
}

func (a *App) setAddressWithSuccessLine(msg *tgbotapi.Message, text, kind, exclusiveSuccessLine string) error {
	if kind == "exclusive" {
		addr, owner, remark, ok := parseExclusiveAddressOwnerAndRemarkFromSetAddressCommand(text)
		if !ok {
			return a.reply(msg.Chat.ID, msg.MessageID, "格式：设置专属地址 地址 @所属人 备注信息")
		}
		val := a.address.Validate(addr)
		if !val.Valid {
			return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("地址无效：%s", val.Reason))
		}
		if err := a.db.UpdateGroupFields(msg.Chat.ID, map[string]any{
			"exclusive_address":        addr,
			"exclusive_address_owner":  owner,
			"exclusive_address_remark": remark,
		}); err != nil {
			log.Printf("设置专属地址写库失败(chat=%d): %v", msg.Chat.ID, err)
			return a.reply(msg.Chat.ID, msg.MessageID, "专属地址设置失败，请稍后重试。")
		}
		if err := a.db.UpsertExclusiveAddress(msg.Chat.ID, addr, owner, remark); err != nil {
			log.Printf("写入专属地址名单失败(chat=%d): %v", msg.Chat.ID, err)
			return a.reply(msg.Chat.ID, msg.MessageID, "专属地址设置失败，请稍后重试。")
		}
		successLine := strings.TrimSpace(exclusiveSuccessLine)
		if successLine == "" {
			successLine = "设置专属地址成功。"
		}
		return a.reply(
			msg.Chat.ID,
			msg.MessageID,
			fmt.Sprintf(
				"%s\n专属地址：%s\n所属人：%s\n备注信息：%s\n网络：%s",
				successLine,
				addr,
				owner,
				remark,
				orDefault(val.Network, "未知"),
			),
		)
	}

	addr, remark, ok := parseAddressAndRemarkFromSetAddressCommand(text)
	if !ok {
		return a.reply(msg.Chat.ID, msg.MessageID, "格式：设置下发地址 地址 或 设置群下发地址 地址")
	}
	val := a.address.Validate(addr)
	if !val.Valid {
		return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("地址无效：%s", val.Reason))
	}
	if kind == "payout" {
		if err := a.db.UpdateGroupFields(msg.Chat.ID, map[string]any{
			"payout_address":        addr,
			"payout_address_remark": remark,
		}); err != nil {
			log.Printf("设置下发地址写库失败(chat=%d): %v", msg.Chat.ID, err)
			return a.reply(msg.Chat.ID, msg.MessageID, "下发地址设置失败，请稍后重试。")
		}
		return a.reply(
			msg.Chat.ID,
			msg.MessageID,
			fmt.Sprintf(
				"设置下发地址成功。\n下发地址：%s\n网络：%s",
				addr,
				orDefault(val.Network, "未知"),
			),
		)
	}
	return nil
}

func parseAddressFromSetAddressCommand(text string) (string, bool) {
	addr, _, ok := parseAddressAndRemarkFromSetAddressCommand(text)
	return addr, ok
}

func compactExclusiveAddressShortcutSuccessLine(text string) string {
	normalized := strings.TrimSpace(text)
	if normalized == "" {
		return ""
	}
	separatorIdx := strings.Index(normalized, "地址")
	if separatorIdx <= 0 {
		return ""
	}
	name := normalizeAddressSetRemark(normalized[:separatorIdx])
	if name == "" {
		return ""
	}
	return name + "地址设置成功。"
}

func parseAddressAndRemarkFromSetAddressCommand(text string) (string, string, bool) {
	payload, ok := parseSetAddressCommandPayload(text, []string{"设置下发地址", "设置群下发地址", "设置专属地址", "设置专属下发地址"})
	if !ok {
		return "", "", false
	}
	if addr, found := extractTRC20AddressForVerification(payload); found {
		remarkRaw := ""
		if idx := strings.Index(strings.ToLower(payload), strings.ToLower(addr)); idx >= 0 {
			remarkRaw = strings.TrimSpace(payload[:idx] + " " + payload[idx+len(addr):])
		}
		return addr, normalizeAddressSetRemark(remarkRaw), true
	}
	return payload, "", true
}

func parseExclusiveAddressOwnerAndRemarkFromSetAddressCommand(text string) (string, string, string, bool) {
	payload, ok := parseSetAddressCommandPayload(text, []string{"设置专属地址", "设置专属下发地址"})
	if !ok {
		return "", "", "", false
	}
	fields := strings.Fields(payload)
	if len(fields) < 3 {
		return "", "", "", false
	}
	addr := strings.TrimSpace(strings.Trim(fields[0], "\"'“”‘’"))
	if extracted, found := extractTRC20AddressForVerification(addr); found {
		addr = extracted
	}
	owner := strings.TrimSpace(strings.Trim(fields[1], "\"'“”‘’"))
	owner = strings.TrimSpace(strings.TrimLeft(owner, ":：,，=＝ \t\r\n"))
	if owner == "" {
		return "", "", "", false
	}
	if !strings.HasPrefix(owner, "@") {
		owner = "@" + owner
	}
	if owner == "@" {
		return "", "", "", false
	}
	remark := normalizeAddressSetRemark(strings.Join(fields[2:], " "))
	if remark == "" {
		return "", "", "", false
	}
	return addr, owner, remark, true
}

func parseCompactExclusiveAddressShortcut(text string) (string, string, string, bool) {
	normalized := strings.TrimSpace(text)
	if normalized == "" {
		return "", "", "", false
	}
	separatorIdx := strings.Index(normalized, "地址")
	if separatorIdx <= 0 {
		return "", "", "", false
	}
	prefixRemark := normalizeAddressSetRemark(normalized[:separatorIdx])
	if prefixRemark == "" {
		return "", "", "", false
	}
	payload := strings.TrimSpace(normalized[separatorIdx+len("地址"):])
	if payload == "" {
		return "", "", "", false
	}
	addr, found := extractTRC20AddressForVerification(payload)
	if !found {
		return "", "", "", false
	}
	addrIdx := strings.Index(strings.ToLower(payload), strings.ToLower(addr))
	if addrIdx < 0 {
		return "", "", "", false
	}
	tail := strings.TrimSpace(payload[addrIdx+len(addr):])
	owner, suffixRemark, ok := parseCompactExclusiveAddressOwner(tail)
	if !ok {
		return "", "", "", false
	}
	remark := normalizeAddressSetRemark(prefixRemark + " " + suffixRemark)
	if remark == "" {
		return "", "", "", false
	}
	return addr, owner, remark, true
}

func parseCompactExclusiveAddressOwner(raw string) (string, string, bool) {
	payload := strings.TrimSpace(raw)
	payload = strings.TrimSpace(strings.TrimLeft(payload, ":：,，=＝ \t\r\n"))
	if payload == "" || payload[0] != '@' {
		return "", "", false
	}
	end := 1
	for end < len(payload) && isCompactExclusiveAddressOwnerChar(payload[end]) {
		end++
	}
	owner := strings.TrimSpace(payload[:end])
	if owner == "@" {
		return "", "", false
	}
	remark := normalizeAddressSetRemark(payload[end:])
	if remark == "" {
		return "", "", false
	}
	return owner, remark, true
}

func isCompactExclusiveAddressOwnerChar(ch byte) bool {
	return ch == '_' ||
		(ch >= '0' && ch <= '9') ||
		(ch >= 'a' && ch <= 'z') ||
		(ch >= 'A' && ch <= 'Z')
}

func parseSetAddressCommandPayload(text string, prefixes []string) (string, bool) {
	normalized := strings.TrimSpace(text)
	if normalized == "" {
		return "", false
	}
	payload := ""
	for _, p := range prefixes {
		if strings.HasPrefix(normalized, p) {
			payload = strings.TrimSpace(strings.TrimPrefix(normalized, p))
			break
		}
	}
	if payload == "" {
		return "", false
	}
	payload = trimSetAddressCommandDecorators(payload)
	payload = strings.TrimSpace(strings.Trim(payload, "\"'“”‘’"))
	if payload == "" {
		return "", false
	}
	return payload, true
}

func trimSetAddressCommandDecorators(raw string) string {
	out := strings.TrimSpace(raw)
	out = strings.TrimSpace(strings.TrimLeft(out, ":：,，=＝ \t\r\n"))
	for {
		switch {
		case strings.HasPrefix(out, "为"):
			out = strings.TrimSpace(strings.TrimPrefix(out, "为"))
		case strings.HasPrefix(out, "是"):
			out = strings.TrimSpace(strings.TrimPrefix(out, "是"))
		default:
			return strings.TrimSpace(strings.TrimLeft(out, ":：,，=＝ \t\r\n"))
		}
		out = strings.TrimSpace(strings.TrimLeft(out, ":：,，=＝ \t\r\n"))
	}
}

func normalizeAddressSetRemark(raw string) string {
	remark := strings.TrimSpace(raw)
	remark = strings.TrimSpace(strings.TrimLeft(remark, ":：,，=＝ \t\r\n"))
	remark = strings.TrimSpace(strings.Trim(remark, "\"'“”‘’"))
	return remark
}

func parseAddressFromQueryAddressCommand(text string) (string, bool) {
	normalized := strings.TrimSpace(text)
	if !strings.HasPrefix(normalized, "查询地址") {
		return "", false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(normalized, "查询地址"))
	payload = strings.TrimSpace(strings.Trim(payload, "\"'“”‘’"))
	if payload == "" {
		return "", false
	}
	if addr, found := extractTRC20AddressForVerification(payload); found {
		return addr, true
	}
	return payload, true
}

func (a *App) verifyAddress(msg *tgbotapi.Message, text string) error {
	addr := strings.TrimSpace(strings.TrimPrefix(text, "验证地址"))
	if addr == "" {
		return a.reply(msg.Chat.ID, msg.MessageID, "格式：验证地址 Txxxx 或 0x...")
	}
	val := a.address.Validate(addr)
	return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("验证结果：%s\n网络：%s\n说明：%s",
		map[bool]string{true: "通过", false: "失败"}[val.Valid], orDefault(val.Network, "未知"), val.Reason))
}

func parseMemberAddressVerificationRequest(text string) (string, bool) {
	normalized := strings.TrimSpace(text)
	if normalized == "" {
		return "", false
	}
	if strings.HasPrefix(normalized, "验证地址") {
		payload := strings.TrimSpace(strings.TrimPrefix(normalized, "验证地址"))
		if payload == "" {
			return "", false
		}
		return extractTRC20AddressForVerification(payload)
	}
	if isAddressManagementCommand(normalized) {
		return "", false
	}
	return extractTRC20AddressForVerification(normalized)
}

func extractTRC20AddressForVerification(text string) (string, bool) {
	normalized := strings.TrimSpace(text)
	if normalized == "" {
		return "", false
	}
	addr := strings.TrimSpace(trc20AddressFinderRegex.FindString(normalized))
	if addr == "" {
		addr = strings.TrimSpace(trc20AddressLooseFinderRegex.FindString(normalized))
	}
	if addr == "" {
		addr = strings.TrimSpace(erc20AddressFinderRegex.FindString(normalized))
	}
	if addr == "" {
		return "", false
	}
	if strings.HasPrefix(addr, "0X") {
		addr = "0x" + addr[2:]
	}
	return addr, true
}

func extractDuplicateAlertAccount(text string) string {
	normalized := strings.TrimSpace(text)
	if normalized == "" {
		return ""
	}
	lowerText := strings.ToLower(normalized)
	keywords := []string{
		"account", "a/c", "bank", "ifsc", "iban", "swift",
		"卡号", "账号", "账户", "银行",
	}
	hasKeyword := false
	for _, keyword := range keywords {
		if strings.Contains(lowerText, keyword) {
			hasKeyword = true
			break
		}
	}
	if !hasKeyword {
		return ""
	}
	for _, candidate := range accountNumberFinderRegex.FindAllString(normalized, -1) {
		accountNo := strings.TrimSpace(candidate)
		if accountNo != "" {
			return accountNo
		}
	}
	return ""
}

func isMemberAddressQueryCommand(text string) bool {
	switch strings.TrimSpace(text) {
	case "地址", "下发地址", "专属地址":
		return true
	default:
		return false
	}
}

func parseExclusiveAddressRemarkQuery(text string) (string, bool) {
	normalized := strings.TrimSpace(text)
	if normalized == "" {
		return "", false
	}
	if !strings.HasSuffix(normalized, "地址") {
		return "", false
	}
	if strings.ContainsAny(normalized, "\r\n\t") {
		return "", false
	}
	remark := strings.TrimSpace(strings.TrimSuffix(normalized, "地址"))
	if remark == "" {
		return "", false
	}
	reserved := map[string]struct{}{
		"设置下发": {}, "设置专属": {}, "设置群下发": {}, "设置专属下发": {},
		"验证": {}, "查询": {}, "下发": {}, "专属": {},
		"查询链上": {}, "链上查询": {}, "查询TRC20": {}, "查询trc20": {}, "TRC20": {}, "trc20": {},
		"查询USDT-TRC20最近交易记录": {}, "查询usdt-trc20最近交易记录": {},
		"清除下发": {}, "清空下发": {}, "删除下发": {},
		"清除专属": {}, "清空专属": {}, "删除专属": {},
	}
	if _, blocked := reserved[remark]; blocked {
		return "", false
	}
	return remark, true
}

func parseClearExclusiveAddressByRemarkCommand(text string) (string, bool) {
	normalized := strings.TrimSpace(text)
	if normalized == "" || strings.ContainsAny(normalized, "\r\n\t") || !strings.HasSuffix(normalized, "地址") {
		return "", false
	}
	prefixes := []string{"清除", "清空", "删除"}
	for _, prefix := range prefixes {
		if !strings.HasPrefix(normalized, prefix) {
			continue
		}
		remark := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(normalized, prefix), "地址"))
		if remark == "" {
			return "", false
		}
		if remark == "下发" || remark == "专属" {
			return "", false
		}
		return remark, true
	}
	return "", false
}

func (a *App) clearExclusiveAddressByRemark(msg *tgbotapi.Message, g db.GroupConfig, remark string) error {
	note := strings.TrimSpace(remark)
	if note == "" {
		return a.reply(msg.Chat.ID, msg.MessageID, "格式：清除备注地址")
	}
	removed := false
	legacyAddr := strings.TrimSpace(g.ExclusiveAddress)
	legacyRemark := strings.TrimSpace(g.ExclusiveAddressRemark)

	var err error
	removed, err = a.db.DeleteExclusiveAddressesByRemark(msg.Chat.ID, note)
	if err != nil {
		log.Printf("按备注清除专属地址失败(chat=%d remark=%s): %v", msg.Chat.ID, note, err)
		return a.reply(msg.Chat.ID, msg.MessageID, "清除专属地址失败，请稍后重试。")
	}
	if !removed {
		if entry, _, ok, err := a.findExclusiveAddressByRemarkQuery(msg.Chat.ID, g, note); err != nil {
			return err
		} else if ok && strings.TrimSpace(entry.Remark) != "" && entry.Remark != note {
			removed, err = a.db.DeleteExclusiveAddressesByRemark(msg.Chat.ID, entry.Remark)
			if err != nil {
				log.Printf("按备注前缀清除专属地址失败(chat=%d remark=%s stored_remark=%s): %v", msg.Chat.ID, note, entry.Remark, err)
				return a.reply(msg.Chat.ID, msg.MessageID, "清除专属地址失败，请稍后重试。")
			}
		}
	}
	if legacyAddr != "" {
		_, legacyPrefixMatched := exclusiveAddressDisplayRemarkForQuery(legacyRemark, note)
		if legacyRemark == note || legacyPrefixMatched {
			if err := a.db.UpdateGroupFields(msg.Chat.ID, map[string]any{
				"exclusive_address":        "",
				"exclusive_address_owner":  "",
				"exclusive_address_remark": "",
			}); err != nil {
				log.Printf("按备注清除专属地址同步群配置失败(chat=%d remark=%s): %v", msg.Chat.ID, note, err)
				return a.reply(msg.Chat.ID, msg.MessageID, "清除专属地址失败，请稍后重试。")
			}
			removed = true
		}
	}
	if removed {
		rows, err := a.db.ListExclusiveAddresses(msg.Chat.ID)
		if err != nil {
			log.Printf("按备注清除专属地址后查询列表失败(chat=%d remark=%s): %v", msg.Chat.ID, note, err)
		} else if len(rows) == 0 {
			_ = a.db.UpdateGroupFields(msg.Chat.ID, map[string]any{
				"exclusive_address":        "",
				"exclusive_address_owner":  "",
				"exclusive_address_remark": "",
			})
		}
	}
	if !removed {
		return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("未找到备注信息为「%s」的专属地址。", note))
	}
	return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("清除%s专属地址成功", note))
}

func (a *App) tryHandleExclusiveRemarkQuery(msg *tgbotapi.Message, g db.GroupConfig, text string) (bool, error) {
	remark, ok := parseExclusiveAddressRemarkQuery(text)
	if !ok {
		return false, nil
	}
	_, _, found, err := a.findExclusiveAddressByRemarkQuery(msg.Chat.ID, g, remark)
	if err != nil {
		return true, err
	}
	if !found {
		return false, nil
	}
	return true, a.queryAddress(msg, g, text)
}

func (a *App) handlePlainTextDuplicateAlert(msg *tgbotapi.Message, g db.GroupConfig, text string) (bool, error) {
	if a == nil || a.db == nil || msg == nil || msg.Chat == nil || msg.From == nil {
		return false, nil
	}
	if !a.businessRiskControlsEnabled() {
		return false, nil
	}
	if g.DuplicateAlertEnabled != 1 {
		return false, nil
	}
	accountNo := extractDuplicateAlertAccount(text)
	if accountNo == "" {
		return false, nil
	}
	seenCount, groupTypeScope, err := a.db.RecordTextDuplicateAccount(msg.Chat.ID, msg.MessageID, accountNo)
	if err != nil {
		return false, err
	}
	if seenCount <= 1 {
		return false, nil
	}
	scopeText := "本群"
	if strings.TrimSpace(groupTypeScope) != "" {
		scopeText = fmt.Sprintf("同类型群（%s）", strings.TrimSpace(groupTypeScope))
	}
	duplicateTimes := seenCount - 1
	detail := fmt.Sprintf(
		"message_id=%d account_no=%s seen_count=%d window=%s scope=%s group_type=%s",
		msg.MessageID,
		accountNo,
		seenCount,
		duplicateAlertWindowText,
		map[bool]string{true: "group_type", false: "chat"}[strings.TrimSpace(groupTypeScope) != ""],
		strings.TrimSpace(groupTypeScope),
	)
	_ = a.db.AddRiskEvent(msg.Chat.ID, msg.MessageID, msg.From.ID, "duplicate_account", "warn", detail)
	return true, a.reply(
		msg.Chat.ID,
		msg.MessageID,
		fmt.Sprintf("⚠️ 重复预警：当前信息重复发送%d次（%s，%s）。", duplicateTimes, scopeText, duplicateAlertWindowText),
	)
}

func isAddressManagementCommand(text string) bool {
	normalized := strings.TrimSpace(text)
	if normalized == "" {
		return false
	}
	switch normalized {
	case "地址", "下发地址", "专属地址",
		"清除下发地址", "清空下发地址", "删除下发地址",
		"清除专属地址", "清空专属地址", "删除专属地址":
		return true
	}
	prefixes := []string{
		"设置下发地址", "设置群下发地址",
		"设置专属地址", "设置专属下发地址",
		"验证地址", "查询地址",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	return false
}

func shouldSilenceMemberPlainMessage(msg *tgbotapi.Message, text string) bool {
	normalized := strings.TrimSpace(text)
	if normalized == "" {
		return true
	}
	if msg != nil && msg.IsCommand() {
		return false
	}
	return !isLikelyGroupCommandText(normalized)
}

func isLikelyGroupCommandText(text string) bool {
	normalized := strings.TrimSpace(text)
	if normalized == "" {
		return false
	}
	if strings.HasPrefix(normalized, "/") {
		return true
	}
	prefixes := []string{
		"@所有人",
		"菜单", "帮助", "群控", "控制面板", "一键账单", "汇率", "担保", "工作计时",
		"设置", "清除", "清空", "删除", "显示", "查询", "查", "导出",
		"开始", "结束", "开启", "关闭", "添加", "移除", "转移", "切换", "同步", "绑定", "解除",
		"自动回复", "记账", "下发", "入款",
		"+", "-",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	return false
}

type addressVerificationTiming struct {
	Total                time.Duration
	TrackMessage         time.Duration
	TrackStats           time.Duration
	ExclusiveLookup      time.Duration
	ImageRender          time.Duration
	ImageBytes           int
	MediaMode            string
	MediaPublish         time.Duration
	MediaURLReady        bool
	MediaPublishError    string
	SendAttemptCount     int
	TelegramResultClass  string
	OutboundSend         time.Duration
	GroupLimiterWait     time.Duration
	HTTPDNSLookup        time.Duration
	HTTPConnect          time.Duration
	HTTPTLSHandshake     time.Duration
	HTTPConnectionWait   time.Duration
	HTTPConnectionReused bool
	HTTPRemoteIP         string
	HTTPRequestWrite     time.Duration
	HTTPResponseWait     time.Duration
	HTTPResponseParse    time.Duration
}

func formatAddressVerificationTimingLog(chatID int64, messageID int, result string, timing addressVerificationTiming) string {
	return fmt.Sprintf(
		"地址验证耗时: chat_id=%d message_id=%d result=%s total_ms=%d track_message_ms=%d track_stats_ms=%d exclusive_lookup_ms=%d image_render_ms=%d image_bytes=%d media_mode=%s media_publish_ms=%d media_url_ready=%d media_publish_error=%q send_attempt_count=%d telegram_result_class=%s outbound_send_ms=%d group_limiter_wait_ms=%d dns_lookup_ms=%d tcp_connect_ms=%d tls_handshake_ms=%d connection_wait_ms=%d connection_reused=%d remote_ip=%s http_request_write_ms=%d http_response_wait_ms=%d http_response_parse_ms=%d",
		chatID,
		messageID,
		strings.TrimSpace(result),
		timing.Total.Milliseconds(),
		timing.TrackMessage.Milliseconds(),
		timing.TrackStats.Milliseconds(),
		timing.ExclusiveLookup.Milliseconds(),
		timing.ImageRender.Milliseconds(),
		timing.ImageBytes,
		strings.TrimSpace(timing.MediaMode),
		timing.MediaPublish.Milliseconds(),
		boolToInt(timing.MediaURLReady),
		strings.TrimSpace(timing.MediaPublishError),
		timing.SendAttemptCount,
		strings.TrimSpace(timing.TelegramResultClass),
		timing.OutboundSend.Milliseconds(),
		timing.GroupLimiterWait.Milliseconds(),
		timing.HTTPDNSLookup.Milliseconds(),
		timing.HTTPConnect.Milliseconds(),
		timing.HTTPTLSHandshake.Milliseconds(),
		timing.HTTPConnectionWait.Milliseconds(),
		boolToInt(timing.HTTPConnectionReused),
		strings.TrimSpace(timing.HTTPRemoteIP),
		timing.HTTPRequestWrite.Milliseconds(),
		timing.HTTPResponseWait.Milliseconds(),
		timing.HTTPResponseParse.Milliseconds(),
	)
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (a *App) handleTRC20AddressVerification(msg *tgbotapi.Message, g db.GroupConfig, address string) (err error) {
	if a == nil || a.db == nil || a.address == nil || msg == nil || msg.Chat == nil || msg.From == nil {
		return nil
	}
	addr := strings.TrimSpace(address)
	if addr == "" {
		return nil
	}
	startedAt := time.Now()
	timing := addressVerificationTiming{
		MediaMode:           "multipart",
		TelegramResultClass: "unknown",
	}
	defer func() {
		timing.Total = time.Since(startedAt)
		result := "success"
		if err != nil {
			result = "error"
		}
		log.Printf("%s", formatAddressVerificationTimingLog(msg.Chat.ID, msg.MessageID, result, timing))
	}()
	log.Printf("地址验证命中: chat_id=%d message_id=%d user_id=%d address=%s", msg.Chat.ID, msg.MessageID, msg.From.ID, addr)
	stageStartedAt := time.Now()
	trackMessageErr := a.db.TrackAddressVerificationMessage(
		msg.Chat.ID,
		msg.MessageID,
		msg.From.ID,
		msg.From.UserName,
		addr,
		messageSentAtUTC(msg),
	)
	timing.TrackMessage = time.Since(stageStartedAt)
	if trackMessageErr != nil {
		return trackMessageErr
	}

	now := a.currentTimeInLocation()
	verifyCode := generateAddressVerificationCode()
	verifyTime := now.Format("2006-01-02 15:04:05")
	currentUserID := msg.From.ID
	currentUsername := strings.TrimSpace(msg.From.UserName)
	stageStartedAt = time.Now()
	snapshot, trackStatsErr := a.db.TrackAddressVerification(msg.Chat.ID, addr, currentUserID, currentUsername, now.UTC())
	timing.TrackStats = time.Since(stageStartedAt)
	if trackStatsErr != nil {
		return trackStatsErr
	}

	currentMention := buildTelegramUserMentionHTML(currentUserID, currentUsername)
	previousMention := "无"
	if snapshot.PreviousSenderUserID != 0 {
		previousMention = a.resolveAddressVerificationPreviousSenderMention(
			msg.Chat.ID,
			snapshot.PreviousSenderUserID,
		)
	}

	imageRemark := ""
	caption := buildAddressVerificationCaption(
		addr,
		verifyCode,
		verifyTime,
		snapshot.VerificationCount,
		previousMention,
		currentMention,
	)
	stageStartedAt = time.Now()
	rows, exclusiveLookupErr := a.listGroupExclusiveAddresses(msg.Chat.ID, g)
	timing.ExclusiveLookup = time.Since(stageStartedAt)
	if exclusiveLookupErr != nil {
		return exclusiveLookupErr
	}
	if entry, ok := matchExclusiveAddressEntryByAddress(rows, addr); ok {
		imageRemark = entry.Remark
		caption = buildExclusiveAddressVerificationCaption(
			addr,
			snapshot.VerificationCount,
			previousMention,
			currentMention,
			entry.Owner,
			entry.Remark,
			"TRC20",
		)
	}
	stageStartedAt = time.Now()
	imageBytes, err := generateAddressVerificationImageWithRemark(addr, imageRemark, verifyCode, verifyTime)
	timing.ImageRender = time.Since(stageStartedAt)
	if err != nil {
		return err
	}
	timing.ImageBytes = len(imageBytes)

	stageStartedAt = time.Now()
	sendTiming, sendResult, sendErr := a.sendAddressVerificationPhoto(
		msg.Chat.ID,
		msg.MessageID,
		caption,
		imageBytes,
	)
	err = sendErr
	timing.OutboundSend = time.Since(stageStartedAt)
	timing.MediaMode = sendResult.mediaMode
	timing.MediaPublish = sendResult.mediaPublishDuration
	timing.MediaURLReady = sendResult.mediaURLReady
	timing.MediaPublishError = sendResult.mediaPublishErrorText
	timing.SendAttemptCount = sendResult.sendAttemptCount
	timing.TelegramResultClass = sendResult.telegramResultClass
	timing.GroupLimiterWait = sendTiming.GroupLimiterWait
	timing.HTTPDNSLookup = sendTiming.HTTPDNSLookup
	timing.HTTPConnect = sendTiming.HTTPConnect
	timing.HTTPTLSHandshake = sendTiming.HTTPTLSHandshake
	timing.HTTPConnectionWait = sendTiming.HTTPConnectionWait
	timing.HTTPConnectionReused = sendTiming.HTTPConnectionReused
	timing.HTTPRemoteIP = sendTiming.HTTPRemoteIP
	timing.HTTPRequestWrite = sendTiming.HTTPRequestWrite
	timing.HTTPResponseWait = sendTiming.HTTPResponseWait
	timing.HTTPResponseParse = sendTiming.HTTPResponseParse
	return err
}

func (a *App) resolveAddressVerificationPreviousSenderMention(chatID, userID int64) string {
	if a == nil || a.db == nil || chatID == 0 || userID == 0 {
		return "无"
	}
	user, err := a.db.FindUserByID(chatID, userID)
	if err != nil {
		log.Printf("读取本群地址验证发送人失败(chat=%d user=%d): %v", chatID, userID, err)
		return "无"
	}
	if user == nil {
		return "无"
	}
	return buildTelegramUserMentionHTML(userID, strings.TrimSpace(user.Username))
}

func buildAddressVerificationCaption(address, verifyCode, verifyTime string, verifyCount int, previousSenderMention, currentSenderMention string) string {
	previous := normalizeAddressVerificationSenderMention(previousSenderMention)
	current := normalizeAddressVerificationSenderMention(currentSenderMention)
	lines := []string{
		fmt.Sprintf("验证地址：<code>%s</code>", html.EscapeString(strings.TrimSpace(address))),
		fmt.Sprintf("验  证  码：%s", html.EscapeString(strings.TrimSpace(verifyCode))),
		fmt.Sprintf("验证时间：%s", html.EscapeString(strings.TrimSpace(verifyTime))),
		fmt.Sprintf("验证次数：%d", verifyCount),
		"",
		fmt.Sprintf("上次发送人：%s", previous),
		fmt.Sprintf("本次发送人：%s", current),
	}
	if verifyCount == 1 {
		lines = append(lines,
			"‼️此地址第一次验证，请核实后进行转账。",
			"‼️此地址第一次验证，请核实后进行转账。",
			"‼️此地址第一次验证，请核实后进行转账。",
		)
	}
	lines = append(lines,
		"",
		"温馨提示：避免损失，请转账方与收款方多人确认，四位一体，是否一致。",
		"图片中 ✅地址 ✅验证码 ✅验证时间 ✅文字地址 ",
		"",
		"点击上方地址，👆🏻即可复制地址",
	)
	return strings.Join(lines, "\n")
}

func normalizeAddressVerificationSenderMention(mention string) string {
	v := strings.TrimSpace(mention)
	if v == "" || v == "未知" || v == "@未知" {
		return "无"
	}
	return v
}

func buildExclusiveAddressVerificationCaption(address string, verifyCount int, previousSenderMention, currentSenderMention, owner, remark, network string) string {
	ownerText := strings.TrimSpace(owner)
	if ownerText == "" {
		ownerText = "@所属人"
	}
	if !strings.HasPrefix(ownerText, "@") {
		ownerText = "@" + ownerText
	}
	note := strings.TrimSpace(remark)
	if note == "" {
		note = "（设置时候的备注）"
	}
	return strings.Join([]string{
		"【专属验证】专属地址验证通过",
		fmt.Sprintf("验证地址: <code>%s</code>", html.EscapeString(strings.TrimSpace(address))),
		fmt.Sprintf("验证次数: %d", verifyCount),
		fmt.Sprintf("上次发送人: %s", strings.TrimSpace(previousSenderMention)),
		fmt.Sprintf("本次发送人: %s", strings.TrimSpace(currentSenderMention)),
		fmt.Sprintf("地址归属: %s", html.EscapeString(ownerText)),
		fmt.Sprintf("备注信息: %s", html.EscapeString(note)),
		fmt.Sprintf("网络: %s", html.EscapeString(strings.TrimSpace(network))),
		"点击上方地址即可复制地址",
	}, "\n")
}

func matchExclusiveAddressEntryByAddress(rows []db.ExclusiveAddressEntry, address string) (db.ExclusiveAddressEntry, bool) {
	target := strings.TrimSpace(address)
	if target == "" {
		return db.ExclusiveAddressEntry{}, false
	}
	for _, item := range rows {
		if sameAddressForVerification(item.Address, target) {
			return item, true
		}
	}
	return db.ExclusiveAddressEntry{}, false
}

func sameAddressForVerification(left, right string) bool {
	l := strings.TrimSpace(left)
	r := strings.TrimSpace(right)
	if l == "" || r == "" {
		return false
	}
	if strings.HasPrefix(strings.ToLower(l), "0x") && strings.HasPrefix(strings.ToLower(r), "0x") {
		return strings.EqualFold(l, r)
	}
	return l == r
}

func buildTelegramUserMentionHTML(userID int64, username string) string {
	if userID == 0 {
		return "未知"
	}
	tag := strings.TrimSpace(strings.TrimPrefix(username, "@"))
	if tag == "" {
		tag = "用户" + strconv.FormatInt(userID, 10)
	}
	return fmt.Sprintf(`<a href="tg://user?id=%d">@%s</a>`, userID, html.EscapeString(tag))
}

func generateAddressVerificationCode() string {
	var seed [2]byte
	if _, err := cryptorand.Read(seed[:]); err != nil {
		value := int(time.Now().UnixNano()%9000) + 1000
		return fmt.Sprintf("%04d", value)
	}
	value := int(binary.BigEndian.Uint16(seed[:]))%9000 + 1000
	return fmt.Sprintf("%04d", value)
}

func generateAddressVerificationImage(address, verifyCode, verifyTime string) ([]byte, error) {
	return generateAddressVerificationImageWithRemark(address, "", verifyCode, verifyTime)
}

type addressVerificationImageLayout struct {
	Bounds   image.Rectangle
	Sections []addressVerificationImageSection
}

type addressVerificationImageSection struct {
	Label              string
	Value              string
	Rect               image.Rectangle
	DarkBackground     bool
	PreferredValueSize float64
	MinValueSize       float64
}

func generateAddressVerificationImageWithRemark(address, remark, verifyCode, verifyTime string) ([]byte, error) {
	layout := buildAddressVerificationImageLayout(address, remark, verifyCode, verifyTime)
	canvas := newAddressVerificationCanvas(layout.Bounds)
	for _, section := range layout.Sections {
		if err := drawAddressVerificationSection(canvas, section); err != nil {
			return nil, err
		}
	}

	var out bytes.Buffer
	if err := jpeg.Encode(&out, canvas, &jpeg.Options{Quality: 92}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

var (
	addressVerificationBackgroundOnce  sync.Once
	addressVerificationBackgroundCache *image.RGBA
)

func addressVerificationBackgroundTemplate() *image.RGBA {
	addressVerificationBackgroundOnce.Do(func() {
		background := image.NewRGBA(image.Rect(0, 0, 1280, 720))
		drawAddressVerificationBlueWhiteBackground(background)
		addressVerificationBackgroundCache = background
	})
	return addressVerificationBackgroundCache
}

func newAddressVerificationCanvas(bounds image.Rectangle) *image.RGBA {
	template := addressVerificationBackgroundTemplate()
	canvas := image.NewRGBA(bounds)
	if canvas.Bounds() == template.Bounds() {
		copy(canvas.Pix, template.Pix)
		return canvas
	}
	draw.Draw(canvas, canvas.Bounds(), template, template.Bounds().Min, draw.Src)
	return canvas
}

func buildAddressVerificationImageLayout(address, remark, verifyCode, verifyTime string) addressVerificationImageLayout {
	bounds := image.Rect(0, 0, 1280, 720)
	sectionHeights := []int{270, 135, 135, 180}
	addr := strings.Join(strings.Fields(address), "")
	note := strings.TrimSpace(remark)
	if note == "" {
		note = "未设置"
	}
	items := []struct {
		label         string
		value         string
		dark          bool
		preferredSize float64
		minSize       float64
	}{
		{"本次验证地址", addr, true, 60, 24},
		{"备注信息", note, false, 44, 24},
		{"本次验证码", strings.TrimSpace(verifyCode), false, 58, 34},
		{"本次验证时间", strings.TrimSpace(verifyTime), false, 46, 24},
	}
	sections := make([]addressVerificationImageSection, 0, len(items))
	currentY := bounds.Min.Y
	for i, item := range items {
		height := sectionHeights[i]
		sections = append(sections, addressVerificationImageSection{
			Label:              item.label,
			Value:              item.value,
			Rect:               image.Rect(bounds.Min.X, currentY, bounds.Max.X, currentY+height),
			DarkBackground:     item.dark,
			PreferredValueSize: item.preferredSize,
			MinValueSize:       item.minSize,
		})
		currentY += height
	}
	return addressVerificationImageLayout{Bounds: bounds, Sections: sections}
}

func drawAddressVerificationBlueWhiteBackground(canvas *image.RGBA) {
	if canvas == nil {
		return
	}
	bounds := canvas.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		t := float64(y-bounds.Min.Y) / float64(maxInt(1, bounds.Dy()-1))
		base := blendRGBA(color.RGBA{R: 248, G: 251, B: 255, A: 255}, color.RGBA{R: 234, G: 244, B: 255, A: 255}, t)
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			canvas.SetRGBA(x, y, base)
		}
	}
}

func drawAddressVerificationSection(canvas *image.RGBA, section addressVerificationImageSection) error {
	if canvas == nil {
		return nil
	}
	if section.DarkBackground {
		drawHorizontalGradient(canvas, section.Rect, color.RGBA{R: 18, G: 75, B: 132, A: 255}, color.RGBA{R: 18, G: 75, B: 132, A: 255})
	}
	if section.Rect.Min.Y > canvas.Bounds().Min.Y {
		draw.Draw(canvas, image.Rect(section.Rect.Min.X, section.Rect.Min.Y, section.Rect.Max.X, section.Rect.Min.Y+1), &image.Uniform{C: color.RGBA{R: 36, G: 101, B: 172, A: 33}}, image.Point{}, draw.Over)
	}

	horizontalPadding := 54
	if section.Label == "本次验证地址" {
		horizontalPadding = 36
	}
	const labelSize = 28
	const labelGap = 10
	availableWidth := section.Rect.Dx() - horizontalPadding*2
	valueSize, err := fitAddressVerifyTextSize(section.Value, availableWidth, section.PreferredValueSize, section.MinValueSize)
	if err != nil {
		return err
	}
	labelFace, err := newAddressVerifyFace(labelSize)
	if err != nil {
		return err
	}
	defer closeFontFace(labelFace)
	valueFace, err := newAddressVerifyFace(valueSize)
	if err != nil {
		return err
	}
	defer closeFontFace(valueFace)

	labelColor := color.RGBA{R: 15, G: 45, B: 82, A: 235}
	valueColor := color.RGBA{R: 9, G: 40, B: 74, A: 255}
	if section.DarkBackground {
		labelColor = color.RGBA{R: 242, G: 248, B: 255, A: 235}
		valueColor = color.RGBA{R: 255, G: 255, B: 255, A: 255}
	}
	if section.Label == "本次验证码" {
		valueColor = color.RGBA{R: 6, G: 79, B: 157, A: 255}
	}

	labelMetrics := labelFace.Metrics()
	valueMetrics := valueFace.Metrics()
	totalHeight := labelMetrics.Height.Ceil() + labelGap + valueMetrics.Height.Ceil()
	top := section.Rect.Min.Y + (section.Rect.Dy()-totalHeight)/2
	x := section.Rect.Min.X + horizontalPadding

	drawer := &font.Drawer{Dst: canvas, Face: labelFace, Src: image.NewUniform(labelColor)}
	drawTextLine(drawer, x, top+labelMetrics.Ascent.Ceil(), section.Label)
	drawer.Face = valueFace
	drawer.Src = image.NewUniform(valueColor)
	drawTextLine(drawer, x, top+labelMetrics.Height.Ceil()+labelGap+valueMetrics.Ascent.Ceil(), section.Value)
	return nil
}

func fitAddressVerifyTextSize(text string, availableWidth int, preferredSize, minSize float64) (float64, error) {
	if availableWidth <= 0 {
		return minSize, nil
	}
	if strings.TrimSpace(text) == "" {
		return preferredSize, nil
	}
	for size := preferredSize; size >= minSize; size -= 1 {
		face, err := newAddressVerifyFace(size)
		if err != nil {
			return 0, err
		}
		width := measureTextWidth(face, text)
		closeFontFace(face)
		if width <= availableWidth {
			return size, nil
		}
	}
	return minSize, nil
}

func measureTextWidth(face font.Face, text string) int {
	if face == nil || text == "" {
		return 0
	}
	return font.MeasureString(face, text).Ceil()
}

func drawHorizontalGradient(canvas *image.RGBA, rect image.Rectangle, left, right color.RGBA) {
	if canvas == nil {
		return
	}
	rect = rect.Intersect(canvas.Bounds())
	width := maxInt(1, rect.Dx()-1)
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			t := float64(x-rect.Min.X) / float64(width)
			canvas.SetRGBA(x, y, blendRGBA(left, right, t))
		}
	}
}

func blendRGBA(a, b color.RGBA, t float64) color.RGBA {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	return color.RGBA{
		R: uint8(float64(a.R)*(1-t) + float64(b.R)*t),
		G: uint8(float64(a.G)*(1-t) + float64(b.G)*t),
		B: uint8(float64(a.B)*(1-t) + float64(b.B)*t),
		A: uint8(float64(a.A)*(1-t) + float64(b.A)*t),
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func buildAddressVerificationImageLines(address, remark, verifyCode, verifyTime string) []string {
	lines := []string{"本次验证地址："}
	lines = append(lines, wrapTextByRune(address, 18)...)
	if note := strings.TrimSpace(remark); note != "" {
		lines = append(lines, "备注信息："+note)
	}
	lines = append(lines,
		"本次验证码："+strings.TrimSpace(verifyCode),
		"本次验证时间："+strings.TrimSpace(verifyTime),
	)
	return lines
}

func drawTextLine(drawer *font.Drawer, x, y int, text string) {
	if drawer == nil {
		return
	}
	drawer.Dot = fixed.P(x, y)
	drawer.DrawString(text)
}

func newAddressVerifyFace(size float64) (font.Face, error) {
	tt, err := loadAddressVerifyFont()
	if err != nil {
		return nil, err
	}
	return opentype.NewFace(tt, &opentype.FaceOptions{
		Size:    size,
		DPI:     72,
		Hinting: font.HintingFull,
	})
}

func closeFontFace(face font.Face) {
	if closer, ok := face.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
}

func loadAddressVerificationBackground() image.Image {
	candidates := []string{
		"bg_template.jpg",
		"assets/bg_template.jpg",
		"go-bot-work/bg_template.jpg",
		"go-bot-work/assets/bg_template.jpg",
	}
	for _, path := range candidates {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		img, _, decodeErr := image.Decode(f)
		_ = f.Close()
		if decodeErr == nil && img != nil {
			return img
		}
	}

	fallback := image.NewRGBA(image.Rect(0, 0, 1280, 720))
	draw.Draw(fallback, fallback.Bounds(), &image.Uniform{C: color.RGBA{R: 172, G: 24, B: 24, A: 255}}, image.Point{}, draw.Src)
	return fallback
}

func wrapTextByRune(raw string, width int) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return []string{""}
	}
	if width <= 0 {
		width = 18
	}
	rs := []rune(trimmed)
	if len(rs) <= width {
		return []string{trimmed}
	}
	out := make([]string, 0, (len(rs)/width)+1)
	for len(rs) > 0 {
		take := width
		if len(rs) < take {
			take = len(rs)
		}
		out = append(out, string(rs[:take]))
		rs = rs[take:]
	}
	return out
}

func (a *App) queryAddress(msg *tgbotapi.Message, g db.GroupConfig, text string) error {
	if latest, err := a.db.GetGroup(msg.Chat.ID); err == nil {
		g = latest
	}
	if text == "下发地址" {
		addr := strings.TrimSpace(g.PayoutAddress)
		if addr == "" {
			return a.reply(msg.Chat.ID, msg.MessageID, "未设置下发地址。")
		}
		return a.replyWithParseMode(
			msg.Chat.ID,
			msg.MessageID,
			buildPayoutAddressTemplate(addr, g.PayoutAddressRemark),
			telegramParseModeHTML,
		)
	}
	if text == "专属地址" {
		rows, err := a.listGroupExclusiveAddresses(msg.Chat.ID, g)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return a.reply(msg.Chat.ID, msg.MessageID, "未设置专属地址。")
		}
		return a.reply(msg.Chat.ID, msg.MessageID, a.buildExclusiveAddressListTemplate(rows))
	}
	if remark, ok := parseExclusiveAddressRemarkQuery(text); ok {
		return a.replyExclusiveAddressDetailByRemark(msg, g, remark)
	}

	addr, hasInlineAddress := parseAddressFromQueryAddressCommand(text)
	if hasInlineAddress {
		val := a.address.Validate(addr)
		if val.Valid {
			return a.handleTRC20Query(msg, "查询链上 "+addr)
		}
		return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("地址：%s\n验证：%s\n网络：%s", addr, map[bool]string{true: "通过", false: "失败"}[val.Valid], orDefault(val.Network, "未知")))
	}

	payoutAddr := strings.TrimSpace(g.PayoutAddress)
	if payoutAddr != "" {
		return a.replyWithParseMode(
			msg.Chat.ID,
			msg.MessageID,
			buildPayoutAddressTemplate(payoutAddr, g.PayoutAddressRemark),
			telegramParseModeHTML,
		)
	}
	return a.reply(msg.Chat.ID, msg.MessageID, "下发地址未设置。")
}

func (a *App) listGroupExclusiveAddresses(chatID int64, g db.GroupConfig) ([]db.ExclusiveAddressEntry, error) {
	rows, err := a.db.ListExclusiveAddresses(chatID)
	if err != nil {
		return nil, err
	}
	if len(rows) > 0 {
		return rows, nil
	}
	addr := strings.TrimSpace(g.ExclusiveAddress)
	if addr == "" {
		return nil, nil
	}
	return []db.ExclusiveAddressEntry{{
		ChatID:  chatID,
		Address: addr,
		Owner:   strings.TrimSpace(g.ExclusiveAddressOwner),
		Remark:  strings.TrimSpace(g.ExclusiveAddressRemark),
	}}, nil
}

func (a *App) replyExclusiveAddressDetailByRemark(msg *tgbotapi.Message, g db.GroupConfig, remark string) error {
	note := strings.TrimSpace(remark)
	if note == "" {
		return a.reply(msg.Chat.ID, msg.MessageID, "未找到对应专属地址。")
	}
	entry, displayRemark, ok, err := a.findExclusiveAddressByRemarkQuery(msg.Chat.ID, g, note)
	if err != nil {
		return err
	}
	if !ok {
		return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("未找到备注信息为「%s」的专属地址。", note))
	}
	return a.replyExclusiveAddressVerificationPhoto(msg, entry, displayRemark)
}

func (a *App) findExclusiveAddressByRemarkQuery(chatID int64, g db.GroupConfig, remark string) (db.ExclusiveAddressEntry, string, bool, error) {
	note := strings.TrimSpace(remark)
	if note == "" {
		return db.ExclusiveAddressEntry{}, "", false, nil
	}

	entry, ok, err := a.db.GetExclusiveAddressByRemark(chatID, note)
	if err != nil {
		return db.ExclusiveAddressEntry{}, "", false, err
	}
	if ok {
		return entry, strings.TrimSpace(entry.Remark), true, nil
	}

	rows, err := a.listGroupExclusiveAddresses(chatID, g)
	if err != nil {
		return db.ExclusiveAddressEntry{}, "", false, err
	}
	for _, item := range rows {
		displayRemark, matched := exclusiveAddressDisplayRemarkForQuery(item.Remark, note)
		if matched {
			return item, displayRemark, true, nil
		}
	}
	return db.ExclusiveAddressEntry{}, "", false, nil
}

func exclusiveAddressDisplayRemarkForQuery(storedRemark, queryRemark string) (string, bool) {
	stored := strings.TrimSpace(storedRemark)
	query := strings.TrimSpace(queryRemark)
	if stored == "" || query == "" {
		return "", false
	}
	if stored == query {
		return stored, true
	}
	if !strings.HasPrefix(stored, query) {
		return "", false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(stored, query))
	if rest == "" || rest == stored {
		return "", false
	}
	return rest, true
}

func (a *App) replyExclusiveAddressVerificationPhoto(msg *tgbotapi.Message, entry db.ExclusiveAddressEntry, displayRemark string) error {
	if a == nil || msg == nil || msg.Chat == nil {
		return nil
	}
	addr := strings.TrimSpace(entry.Address)
	if addr == "" {
		return a.reply(msg.Chat.ID, msg.MessageID, "未找到对应专属地址。")
	}
	now := a.currentTimeInLocation()
	verifyCode := generateAddressVerificationCode()
	verifyTime := now.Format("2006-01-02 15:04:05")
	imageBytes, err := generateAddressVerificationImageWithRemark(addr, displayRemark, verifyCode, verifyTime)
	if err != nil {
		return err
	}
	caption := buildExclusiveAddressKeywordVerificationCaption(
		addr,
		verifyCode,
		verifyTime,
		entry.Owner,
		displayRemark,
	)
	_, _, err = a.sendAddressVerificationPhoto(msg.Chat.ID, msg.MessageID, caption, imageBytes)
	return err
}

func buildExclusiveAddressKeywordVerificationCaption(address, verifyCode, verifyTime, owner, remark string) string {
	addr := strings.TrimSpace(address)
	ownerText := strings.TrimSpace(owner)
	if ownerText == "" {
		ownerText = "@所属人"
	}
	if !strings.HasPrefix(ownerText, "@") {
		ownerText = "@" + ownerText
	}
	note := strings.TrimSpace(remark)
	if note == "" {
		note = "（设置时候的备注）"
	}
	return strings.Join([]string{
		"下发地址：<code>" + html.EscapeString(addr) + "</code>",
		"验  证  码：" + html.EscapeString(strings.TrimSpace(verifyCode)),
		"验证时间：" + html.EscapeString(strings.TrimSpace(verifyTime)),
		"",
		"地址归属：" + html.EscapeString(ownerText),
		"",
		"备注信息：" + html.EscapeString(note),
		"",
		"点击上方地址，👆🏻即可复制地址",
		"",
		"温馨提示：避免损失，请转账方与收款方多人确认，四位一体，是否一致。",
		"图片中 ✅地址 ✅验证码 ✅验证时间 ✅文字地址",
	}, "\n")
}

func (a *App) buildExclusiveAddressListTemplate(rows []db.ExclusiveAddressEntry) string {
	if len(rows) == 0 {
		return "本群暂无专属地址。"
	}
	lines := []string{fmt.Sprintf("本群专属地址共 %d 个：", len(rows))}
	for i, item := range rows {
		note := strings.TrimSpace(item.Remark)
		if note == "" {
			note = fmt.Sprintf("未命名%d", i+1)
		}
		addr := strings.TrimSpace(item.Address)
		owner := strings.TrimSpace(item.Owner)
		if owner == "" {
			owner = "@所属人"
		}
		if !strings.HasPrefix(owner, "@") {
			owner = "@" + owner
		}
		network := "未知"
		if a != nil && a.address != nil {
			val := a.address.Validate(addr)
			network = orDefault(strings.TrimSpace(val.Network), "未知")
		}
		lines = append(lines,
			fmt.Sprintf("%d. 备注信息：%s", i+1, note),
			"专属地址："+addr,
			"所属人："+owner,
			"网络："+network,
		)
	}
	return strings.Join(lines, "\n")
}

func buildPayoutAddressTemplate(address, remark string) string {
	addr := strings.TrimSpace(address)
	note := strings.TrimSpace(remark)
	if note == "" {
		note = "（设置时候的备注）"
	}
	escapedAddr := html.EscapeString(addr)
	escapedNote := html.EscapeString(note)
	return strings.Join([]string{
		"下发地址：<code>" + escapedAddr + "</code>",
		"备注信息：" + escapedNote,
		"点击上方地址即可复制地址",
	}, "\n")
}

func buildExclusiveAddressTemplate(address, owner, remark, verification, network string) string {
	addr := strings.TrimSpace(address)
	ownerText := strings.TrimSpace(owner)
	if ownerText == "" {
		ownerText = "@所属人"
	}
	if !strings.HasPrefix(ownerText, "@") {
		ownerText = "@" + ownerText
	}
	note := strings.TrimSpace(remark)
	if note == "" {
		note = "（设置时候的备注）"
	}
	return strings.Join([]string{
		"专属地址：<code>" + html.EscapeString(addr) + "</code>",
		"地址归属：" + html.EscapeString(ownerText),
		"备注信息：" + html.EscapeString(note),
		"",
		"点击上方地址，即可复制地址",
		"温馨提示：请地址归属方人员确认地址。",
	}, "\n")
}

func (a *App) handleOperatorCommand(msg *tgbotapi.Message, g db.GroupConfig, text string) error {
	if text == "显示本群操作人" || text == "显示操作人" || text == "本群操作人" {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		rows, err := a.db.ListOperators(msg.Chat.ID, false)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return a.reply(msg.Chat.ID, msg.MessageID, "暂无本群操作人。")
		}
		lines := []string{"本群操作人："}
		for _, r := range rows {
			lines = append(lines, fmt.Sprintf("- @%s", orDefault(r.Username, strconv.FormatInt(r.UserID, 10))))
		}
		return a.reply(msg.Chat.ID, msg.MessageID, strings.Join(lines, "\n"))
	}

	if text == "显示全局权限人" {
		if !a.requireOwner(msg, g) {
			return nil
		}
		rows, err := a.db.ListGlobalOwners()
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return a.reply(msg.Chat.ID, msg.MessageID, "暂无全局权限人。")
		}
		lines := []string{"全局权限人："}
		for _, r := range rows {
			lines = append(lines, fmt.Sprintf("- @%s", orDefault(r.Username, strconv.FormatInt(r.UserID, 10))))
		}
		return a.reply(msg.Chat.ID, msg.MessageID, strings.Join(lines, "\n"))
	}

	if strings.HasPrefix(text, "设置全局权限人") || strings.HasPrefix(text, "删除全局权限人") {
		if msg.From != nil && a.db.IsGlobalOwner(msg.From.ID) {
			return nil
		}
		if !a.requireOwner(msg, g) {
			return nil
		}
		isAdd := strings.HasPrefix(text, "设置全局权限人")
		targets, err := a.extractTargetUsers(msg, text)
		if err != nil {
			return err
		}
		if len(targets) == 0 {
			return a.reply(msg.Chat.ID, msg.MessageID, "请通过回复消息或 @用户名 指定用户。")
		}
		for _, t := range targets {
			if isAdd {
				_ = a.db.AddGlobalOwner(t.UserID, t.Username)
			} else {
				_ = a.db.RemoveGlobalOwner(t.UserID)
			}
		}
		action := "设置"
		if !isAdd {
			action = "删除"
		}
		return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("%s全局权限人完成，共 %d 人。", action, len(targets)))
	}

	if text == "显示全局操作人" {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		rows, err := a.db.ListOperators(msg.Chat.ID, true)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return a.reply(msg.Chat.ID, msg.MessageID, "暂无全局操作人。")
		}
		lines := []string{"全局操作人："}
		for _, r := range rows {
			lines = append(lines, fmt.Sprintf("- @%s", orDefault(r.Username, strconv.FormatInt(r.UserID, 10))))
		}
		return a.reply(msg.Chat.ID, msg.MessageID, strings.Join(lines, "\n"))
	}

	if text == "转移本群权限人" {
		expired := false
		if active, reason := a.checkGroupEntitlement(g); !active {
			expired = true
			_ = a.reply(msg.Chat.ID, msg.MessageID, reason)
		}
		allowTransfer := false
		if msg.From != nil && a.db.IsOwner(msg.Chat.ID, msg.From.ID) {
			allowTransfer = true
		}
		if !allowTransfer {
			if !a.requirePrivileged(msg, g) {
				return nil
			}
			if !expired {
				return nil
			}
			allowTransfer = true
		}
		if !allowTransfer {
			return nil
		}
		targets, err := a.extractTargetUsers(msg, text)
		if err != nil {
			return err
		}
		if len(targets) == 0 {
			return a.reply(msg.Chat.ID, msg.MessageID, "请通过回复消息或 @用户名 指定新的权限人。")
		}
		_ = a.db.SetOwner(msg.Chat.ID, targets[0].UserID)
		return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("权限人已转移为 @%s", orDefault(targets[0].Username, strconv.FormatInt(targets[0].UserID, 10))))
	}

	if !a.requireOwner(msg, g) {
		return nil
	}
	isAdd := strings.HasPrefix(text, "设置操作人") || strings.HasPrefix(text, "设置全局操作人")
	isGlobal := strings.HasPrefix(text, "设置全局操作人") || strings.HasPrefix(text, "删除全局操作人")
	targets, err := a.extractTargetUsers(msg, text)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return a.reply(msg.Chat.ID, msg.MessageID, "没有缓存该用户，请直接回复对方消息后发送【设置操作人】或使用 @用户名 指定用户。")
	}
	for _, t := range targets {
		if isAdd {
			_ = a.db.AddOperator(msg.Chat.ID, t.UserID, t.Username, isGlobal)
		} else {
			_ = a.db.RemoveOperator(msg.Chat.ID, t.UserID, isGlobal)
		}
	}
	if err := a.notifyOwnerPermissionChange(g.OwnerUserID, msg, isAdd, isGlobal, targets); err != nil {
		log.Printf("发送权限变更私密通知失败: %v", err)
	}
	action := "设置"
	if !isAdd {
		action = "删除"
	}
	scope := "本群"
	if isGlobal {
		scope = "全局"
	}
	return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("%s%s操作人完成，共 %d 人。", action, scope, len(targets)))
}

func (a *App) notifyOwnerPermissionChange(ownerUserID int64, msg *tgbotapi.Message, isAdd, isGlobal bool, targets []targetUser) error {
	if ownerUserID == 0 || msg == nil || msg.Chat == nil || len(targets) == 0 {
		return nil
	}

	action := "新增"
	if !isAdd {
		action = "删除"
	}
	scope := "本群"
	if isGlobal {
		scope = "全局"
	}
	lines := []string{
		"【权限变更通知】",
		fmt.Sprintf("操作：%s%s操作人", action, scope),
		fmt.Sprintf("群组ID：%d", msg.Chat.ID),
		fmt.Sprintf("群标题：%s", orDefault(strings.TrimSpace(msg.Chat.Title), "未知")),
		fmt.Sprintf("执行人：%s", permissionAlertOperatorLabel(msg.From)),
		fmt.Sprintf("目标：%s", permissionAlertTargetsLabel(targets)),
		fmt.Sprintf("时间：%s", a.currentTimeInLocation().Format("2006-01-02 15:04:05")),
	}

	alert := tgbotapi.NewMessage(ownerUserID, strings.Join(lines, "\n"))
	return a.sendWithPriority(outboundPriorityErrorAlert, alert)
}

func permissionAlertOperatorLabel(user *tgbotapi.User) string {
	if user == nil {
		return "未知用户"
	}
	label := composeNickname(user.FirstName, user.LastName)
	username := strings.TrimSpace(strings.TrimPrefix(user.UserName, "@"))
	if username != "" {
		label = "@" + strings.ToLower(username)
	}
	return fmt.Sprintf("%s(%d)", label, user.ID)
}

func permissionAlertTargetsLabel(targets []targetUser) string {
	if len(targets) == 0 {
		return "无"
	}
	labels := make([]string, 0, len(targets))
	for _, target := range targets {
		labels = append(labels, permissionAlertTargetLabel(target))
	}
	return strings.Join(labels, "、")
}

func permissionAlertTargetLabel(target targetUser) string {
	username := strings.TrimSpace(strings.TrimPrefix(target.Username, "@"))
	if username == "" {
		return strconv.FormatInt(target.UserID, 10)
	}
	return fmt.Sprintf("@%s(%d)", strings.ToLower(username), target.UserID)
}

type targetUser struct {
	UserID   int64
	Username string
}

func normalizeTelegramUsername(username string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(username), "@"))
}

func normalizeTargetUsername(username string) string {
	return normalizeTelegramUsername(username)
}

func (a *App) validateAndBackfillTargetUser(chatID int64, row *db.GroupUser) (*targetUser, error) {
	if row == nil || !a.IsGroupMember(chatID, row.UserID) {
		return nil, nil
	}
	if _, _, err := a.db.UpsertGroupUser(chatID, row.UserID, row.Username, row.Nickname); err != nil {
		return nil, err
	}
	return &targetUser{
		UserID:   row.UserID,
		Username: normalizeTargetUsername(row.Username),
	}, nil
}

func (a *App) resolveTargetUserFromCaches(chatID int64, username string) (*targetUser, error) {
	normalized := normalizeTargetUsername(username)
	if normalized == "" {
		return nil, nil
	}

	row, err := a.db.FindUserByUsername(chatID, normalized)
	if err != nil {
		return nil, err
	}
	if row != nil {
		return &targetUser{
			UserID:   row.UserID,
			Username: normalizeTargetUsername(row.Username),
		}, nil
	}

	row, err = a.db.FindUserByAlias(chatID, normalized)
	if err != nil {
		return nil, err
	}
	if row != nil {
		return a.validateAndBackfillTargetUser(chatID, row)
	}

	row, err = a.db.FindAnyUserByUsername(normalized)
	if err != nil {
		return nil, err
	}
	if row != nil {
		return a.validateAndBackfillTargetUser(chatID, row)
	}

	row, err = a.db.FindAnyUserByAlias(normalized)
	if err != nil {
		return nil, err
	}
	if row != nil {
		return a.validateAndBackfillTargetUser(chatID, row)
	}

	return nil, nil
}

func (a *App) extractTargetUsers(msg *tgbotapi.Message, text string) ([]targetUser, error) {
	seen := map[int64]struct{}{}
	out := []targetUser{}
	appendUser := func(uid int64, uname string) {
		if _, ok := seen[uid]; ok {
			return
		}
		seen[uid] = struct{}{}
		out = append(out, targetUser{UserID: uid, Username: normalizeTargetUsername(uname)})
	}
	if msg.ReplyToMessage != nil && msg.ReplyToMessage.From != nil {
		appendUser(msg.ReplyToMessage.From.ID, msg.ReplyToMessage.From.UserName)
	}
	re := regexp.MustCompile(`@([A-Za-z0-9_]{3,})`)
	for _, m := range re.FindAllStringSubmatch(text, -1) {
		target, err := a.resolveTargetUserFromCaches(msg.Chat.ID, m[1])
		if err != nil {
			return nil, err
		}
		if target == nil {
			continue
		}
		appendUser(target.UserID, target.Username)
	}
	return out, nil
}

func (a *App) handleIdentityCommand(msg *tgbotapi.Message, g db.GroupConfig, text string) (bool, error) {
	if text == "本群身份" {
		names, err := a.db.ListIdentityNames(msg.Chat.ID)
		if err != nil {
			return true, err
		}
		if len(names) == 0 {
			return true, a.reply(msg.Chat.ID, msg.MessageID, "本群身份：暂无")
		}
		return true, a.reply(msg.Chat.ID, msg.MessageID, "本群身份："+strings.Join(names, "、"))
	}
	if strings.HasPrefix(text, "显示") && !isShowGroupIDCommand(text) && text != "显示昵称" && text != "显示入款信息" && text != "显示下发信息" {
		identity := strings.TrimSpace(strings.TrimPrefix(text, "显示"))
		if identity == "" {
			return false, nil
		}
		rows, err := a.db.ListIdentityUsers(msg.Chat.ID, identity)
		if err != nil {
			return true, err
		}
		if len(rows) == 0 {
			return true, a.reply(msg.Chat.ID, msg.MessageID, "该身份暂无成员。")
		}
		lines := []string{identity + " 成员："}
		for _, r := range rows {
			lines = append(lines, fmt.Sprintf("- @%s", orDefault(r.Username, strconv.FormatInt(r.UserID, 10))))
		}
		return true, a.reply(msg.Chat.ID, msg.MessageID, strings.Join(lines, "\n"))
	}
	if strings.HasPrefix(text, "设置身份") {
		if !a.requirePrivileged(msg, g) {
			return true, nil
		}
		raw := strings.TrimSpace(strings.TrimPrefix(text, "设置身份"))
		if raw == "" {
			return true, a.reply(msg.Chat.ID, msg.MessageID, "格式：设置身份合作伙伴 @xxx @xxx")
		}
		identity := raw
		if strings.Contains(raw, " ") {
			identity = strings.TrimSpace(strings.SplitN(raw, " ", 2)[0])
		}
		targets, err := a.extractTargetUsers(msg, text)
		if err != nil {
			return true, err
		}
		if len(targets) == 0 {
			return true, a.reply(msg.Chat.ID, msg.MessageID, "请通过回复消息或 @用户名 指定成员。")
		}
		for _, t := range targets {
			_ = a.db.SetIdentity(msg.Chat.ID, identity, t.UserID, t.Username)
		}
		return true, a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("身份【%s】设置完成，共 %d 人。", identity, len(targets)))
	}
	if strings.HasPrefix(text, "删除身份") {
		if !a.requirePrivileged(msg, g) {
			return true, nil
		}
		targets, err := a.extractTargetUsers(msg, text)
		if err != nil {
			return true, err
		}
		if len(targets) == 0 {
			return true, a.reply(msg.Chat.ID, msg.MessageID, "请通过回复消息或 @用户名 指定成员。")
		}
		for _, t := range targets {
			_ = a.db.RemoveIdentity(msg.Chat.ID, t.UserID)
		}
		return true, a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("身份删除完成，共 %d 人。", len(targets)))
	}
	return false, nil
}

func (a *App) handleCustomContentCommand(msg *tgbotapi.Message, g db.GroupConfig, text string) error {
	if text == "开启自定义内容时间" || text == "关闭自定义内容时间" {
		if !a.requirePrivileged(msg, g) {
			return nil
		}
		flag := 0
		if text == "开启自定义内容时间" {
			flag = 1
		}
		_ = a.db.UpdateGroupFields(msg.Chat.ID, map[string]any{"custom_content_with_time": flag})
		return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("自定义内容时间显示已%s。", map[bool]string{true: "开启", false: "关闭"}[flag == 1]))
	}
	if !a.requirePrivileged(msg, g) {
		return nil
	}
	if strings.HasPrefix(text, "删除自定义内容") {
		key, err := utils.SanitizeAutomationTriggerKey(strings.TrimSpace(strings.TrimPrefix(text, "删除自定义内容")))
		if err != nil {
			return a.reply(msg.Chat.ID, msg.MessageID, err.Error())
		}
		n, _ := a.db.DeleteCustomContent(msg.Chat.ID, key)
		return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("删除完成，影响 %d 条。", n))
	}
	payload := strings.TrimSpace(strings.TrimPrefix(text, "设置自定义内容"))
	parts := strings.SplitN(payload, "=", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return a.reply(msg.Chat.ID, msg.MessageID, "格式：设置自定义内容 key=value")
	}
	key, err := utils.SanitizeAutomationTriggerKey(parts[0])
	if err != nil {
		return a.reply(msg.Chat.ID, msg.MessageID, err.Error())
	}
	content, err := utils.SanitizeAutomationContent(parts[1])
	if err != nil {
		return a.reply(msg.Chat.ID, msg.MessageID, err.Error())
	}
	if err := a.db.SetCustomContent(msg.Chat.ID, key, content, msg.From.ID); err != nil {
		return err
	}
	return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("自定义内容已设置：%s", key))
}

func (a *App) handleAutomationCommand(msg *tgbotapi.Message, g db.GroupConfig, text string) error {
	if !a.requirePrivileged(msg, g) {
		return nil
	}
	if strings.HasPrefix(text, "设置自动回复") {
		payload := strings.TrimSpace(strings.TrimPrefix(text, "设置自动回复"))
		parts := strings.SplitN(payload, "=", 2)
		if len(parts) != 2 {
			return a.reply(msg.Chat.ID, msg.MessageID, "格式：设置自动回复 关键词=内容")
		}
		keyword, err := utils.SanitizeAutomationKeyword(parts[0], true)
		if err != nil {
			return a.reply(msg.Chat.ID, msg.MessageID, err.Error())
		}
		content, err := utils.SanitizeAutomationContent(parts[1])
		if err != nil {
			return a.reply(msg.Chat.ID, msg.MessageID, err.Error())
		}
		if err := a.db.SetAutoReply(msg.Chat.ID, keyword, content); err != nil {
			return err
		}
		return a.reply(msg.Chat.ID, msg.MessageID, "自动回复已设置。")
	}
	if strings.HasPrefix(text, "删除自动回复") {
		key, err := utils.SanitizeAutomationKeyword(strings.TrimSpace(strings.TrimPrefix(text, "删除自动回复")), false)
		if err != nil {
			return a.reply(msg.Chat.ID, msg.MessageID, err.Error())
		}
		n, _ := a.db.DeleteAutoReply(msg.Chat.ID, key)
		return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("自动回复已删除，影响 %d 条。", n))
	}
	if strings.HasPrefix(text, "设置关键词转发") {
		payload := strings.TrimSpace(strings.TrimPrefix(text, "设置关键词转发"))
		parts := strings.SplitN(payload, "=", 2)
		if len(parts) != 2 {
			return a.reply(msg.Chat.ID, msg.MessageID, "格式：设置关键词转发 关键词=chatId1,chatId2")
		}
		keyword, err := utils.SanitizeAutomationKeyword(parts[0], false)
		if err != nil {
			return a.reply(msg.Chat.ID, msg.MessageID, err.Error())
		}
		targets, err := utils.SanitizeForwardTargets(parts[1])
		if err != nil {
			return a.reply(msg.Chat.ID, msg.MessageID, err.Error())
		}
		if err := a.db.SetForwardRule(msg.Chat.ID, keyword, targets); err != nil {
			return err
		}
		return a.reply(msg.Chat.ID, msg.MessageID, "关键词转发已设置。")
	}
	key, err := utils.SanitizeAutomationKeyword(strings.TrimSpace(strings.TrimPrefix(text, "删除关键词转发")), false)
	if err != nil {
		return a.reply(msg.Chat.ID, msg.MessageID, err.Error())
	}
	n, _ := a.db.DeleteForwardRule(msg.Chat.ID, key)
	return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("关键词转发已删除，影响 %d 条。", n))
}

func (a *App) showAutoReplyList(msg *tgbotapi.Message) error {
	rows, err := a.db.ListAutoReplies(msg.Chat.ID)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return a.reply(msg.Chat.ID, msg.MessageID, "当前无自动回复规则。")
	}
	lines := []string{"自动回复列表："}
	for i, row := range rows {
		if i >= 20 {
			break
		}
		lines = append(lines, fmt.Sprintf("%d) %s => %s", i+1, orDefault(row.Keyword, "（无关键词兜底）"), trimForPreview(row.Content, 40)))
	}
	return a.reply(msg.Chat.ID, msg.MessageID, strings.Join(lines, "\n"))
}

func (a *App) showForwardRuleList(msg *tgbotapi.Message) error {
	rows, err := a.db.ListForwardRules(msg.Chat.ID)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return a.reply(msg.Chat.ID, msg.MessageID, "当前无关键词转发规则。")
	}
	lines := []string{"关键词转发列表："}
	for i, row := range rows {
		if i >= 20 {
			break
		}
		lines = append(lines, fmt.Sprintf("%d) %s => %s", i+1, row.Keyword, row.TargetChatIDs))
	}
	return a.reply(msg.Chat.ID, msg.MessageID, strings.Join(lines, "\n"))
}

func (a *App) showCustomContentList(msg *tgbotapi.Message) error {
	rows, err := a.db.ListCustomContents(msg.Chat.ID)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return a.reply(msg.Chat.ID, msg.MessageID, "当前无自定义内容。")
	}
	lines := []string{"自定义内容列表："}
	for i, row := range rows {
		if i >= 20 {
			break
		}
		lines = append(lines, fmt.Sprintf("%d) %s => %s", i+1, row.TriggerKey, trimForPreview(row.Content, 40)))
	}
	return a.reply(msg.Chat.ID, msg.MessageID, strings.Join(lines, "\n"))
}

func (a *App) replyGroupPanel(chatID int64, replyTo int, g db.GroupConfig) error {
	m := tgbotapi.NewMessage(chatID, a.groupPanelText(g))
	m.ReplyToMessageID = replyTo
	m.ReplyMarkup = a.groupPanelMarkup(g)
	return a.sendWithPriority(outboundPriorityNormalChat, m)
}

func (a *App) groupPanelText(g db.GroupConfig) string {
	bookkeepingStatus := "停止"
	if a.isGroupBookkeepingActiveForCurrentCycle(g) {
		bookkeepingStatus = "开启"
	}
	return strings.Join([]string{
		"群控面板",
		fmt.Sprintf("记账：%s", bookkeepingStatus),
		fmt.Sprintf("换算：%s", map[bool]string{true: "开启", false: "关闭"}[g.ShowUSDTConversion == 1]),
		fmt.Sprintf("自动回复：%s", map[bool]string{true: "开启", false: "关闭"}[g.AutoReplyEnabled == 1]),
		"已纳入：一键开始/汇率/账单/群发/标签/群类型/统计/自动化。",
		"权限：仅机器人系统权限人/操作人可设置，不要求 Telegram 管理员。",
		"条件：机器人必须在目标群内；私聊群控只显示可管理群。",
	}, "\n")
}

func (a *App) groupPanelMarkup(g db.GroupConfig) tgbotapi.InlineKeyboardMarkup {
	convertLabel := "开启换算"
	if g.ShowUSDTConversion == 1 {
		convertLabel = "关闭换算"
	}
	autoLabel := "开启自动回复"
	if g.AutoReplyEnabled == 1 {
		autoLabel = "关闭自动回复"
	}
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("开始记账", groupPanelCallbackPrefix+"start"),
			tgbotapi.NewInlineKeyboardButtonData("停止记账", groupPanelCallbackPrefix+"stop"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("一键设置汇率", groupPanelCallbackPrefix+"rate_onekey"),
			tgbotapi.NewInlineKeyboardButtonData("一键账单", groupPanelCallbackPrefix+"bill"),
			tgbotapi.NewInlineKeyboardButtonData("一键群发信息", groupPanelCallbackPrefix+"broadcast"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("设置群标签", groupPanelCallbackPrefix+"tag_set"),
			tgbotapi.NewInlineKeyboardButtonData("设置群类型", groupPanelCallbackPrefix+"type_set"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("批量设置群标签", groupPanelCallbackPrefix+"batch_tag_set"),
			tgbotapi.NewInlineKeyboardButtonData("批量设置群类型", groupPanelCallbackPrefix+"batch_type_set"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("开启下课提醒", groupPanelCallbackPrefix+"closing_enable"),
			tgbotapi.NewInlineKeyboardButtonData("关闭下课提醒", groupPanelCallbackPrefix+"closing_disable"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("设置下课时间", groupPanelCallbackPrefix+"closing_time"),
			tgbotapi.NewInlineKeyboardButtonData("查询下课提醒", groupPanelCallbackPrefix+"closing_query"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("同类型群数据统计", groupPanelCallbackPrefix+"type_stats"),
			tgbotapi.NewInlineKeyboardButtonData("单独群数据统计", groupPanelCallbackPrefix+"single_stats"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("查单列表", groupPanelCallbackPrefix+"order_list"),
			tgbotapi.NewInlineKeyboardButtonData("导出账单", groupPanelCallbackPrefix+"export"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("定时任务", groupPanelCallbackPrefix+"schedule_list"),
			tgbotapi.NewInlineKeyboardButtonData("汇率对比", groupPanelCallbackPrefix+"rate"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("自动回复列表", groupPanelCallbackPrefix+"auto_list"),
			tgbotapi.NewInlineKeyboardButtonData("关键词转发列表", groupPanelCallbackPrefix+"forward_list"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("自定义内容列表", groupPanelCallbackPrefix+"custom_list"),
			tgbotapi.NewInlineKeyboardButtonData("菜单帮助", groupPanelCallbackPrefix+"help"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(convertLabel, groupPanelCallbackPrefix+"convert_toggle"),
			tgbotapi.NewInlineKeyboardButtonData(autoLabel, groupPanelCallbackPrefix+"auto_toggle"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("群组管理", groupPanelCallbackPrefix+"help"),
			tgbotapi.NewInlineKeyboardButtonData("刷新面板", groupPanelCallbackPrefix+"panel"),
		),
	)
}

func (a *App) executeAsUserGroupCommand(ctx context.Context, chatID int64, replyTo int, user *tgbotapi.User, command string) error {
	if user == nil || chatID == 0 {
		return errors.New("执行参数不完整")
	}
	group, err := a.db.EnsureGroup(chatID, "")
	if err != nil {
		return err
	}
	text := strings.TrimSpace(command)
	if active, reason := a.checkGroupEntitlement(group); !active {
		return errors.New(reason)
	}
	if !a.db.IsPrivileged(chatID, user.ID) {
		return errors.New("权限不足")
	}
	username := strings.ToLower(strings.TrimSpace(user.UserName))
	nickname := composeNickname(user.FirstName, user.LastName)
	_, _, _ = a.db.UpsertGroupUser(chatID, user.ID, username, nickname)
	msg := &tgbotapi.Message{
		MessageID: replyTo,
		Chat:      &tgbotapi.Chat{ID: chatID, Type: "supergroup", Title: group.Title},
		From:      user,
		Text:      text,
	}
	return a.dispatchGroupMessage(ctx, msg, group, text)
}

type WebAppPrivateActionResult struct {
	Message     string
	DisplayText string
	Mono        bool
	OpenURL     string
}

func (a *App) ExecuteWebAppPrivateAction(userID, targetChatID int64, action string) (WebAppPrivateActionResult, error) {
	cmd := strings.TrimSpace(action)
	if userID == 0 {
		return WebAppPrivateActionResult{}, errors.New("用户ID不能为空")
	}
	if cmd == "" {
		return WebAppPrivateActionResult{}, errors.New("操作不能为空")
	}
	if a == nil || a.db == nil {
		return WebAppPrivateActionResult{}, errors.New("服务未初始化")
	}
	now := a.currentTime().UTC()
	formatTime := func(t time.Time) string {
		if a != nil && a.loc != nil {
			return t.In(a.loc).Format("2006-01-02 15:04:05")
		}
		return t.Format("2006-01-02 15:04:05")
	}

	switch cmd {
	case "申请试用":
		return WebAppPrivateActionResult{
			Message:     "使用申请已关闭",
			DisplayText: privateTrialApplyClosedText(),
		}, nil
	case "获取ID":
		groupIDText := "未选择"
		if targetChatID != 0 {
			groupIDText = strconv.FormatInt(targetChatID, 10)
		}
		lines := []string{
			fmt.Sprintf("当前用户ID：%d", userID),
			fmt.Sprintf("当前群ID：%s", groupIDText),
			"如需群ID，请在目标群发送【显示群ID】。",
		}
		return WebAppPrivateActionResult{
			Message:     "ID 信息已返回",
			DisplayText: strings.Join(lines, "\n"),
			Mono:        true,
		}, nil
	case "邀请进群":
		botUsername := a.privateBotUsername()
		result := WebAppPrivateActionResult{
			Message:     "入群教程已返回",
			DisplayText: privateTutorialOverviewText(strings.TrimSpace(a.cfg.TGWebAppURL) != "", botUsername),
			Mono:        true,
		}
		if strings.TrimSpace(botUsername) != "" {
			result.OpenURL = fmt.Sprintf("https://t.me/%s?startgroup=true", strings.TrimSpace(botUsername))
		}
		return result, nil
	case "使用说明":
		return WebAppPrivateActionResult{
			Message:     "使用说明已返回",
			DisplayText: privateUsageGuideText(),
			Mono:        true,
		}, nil
	case "到期时间":
		profile, err := a.db.GetPrivateUserProfile(userID)
		if err != nil {
			return WebAppPrivateActionResult{}, err
		}
		if strings.TrimSpace(profile.ExpireAt) == "" {
			return WebAppPrivateActionResult{
				Message:     "到期时间已返回",
				DisplayText: "当前无有效使用权限，请联系管理员开通。",
			}, nil
		}
		expireAt, ok := parseRFC3339(profile.ExpireAt)
		if !ok {
			return WebAppPrivateActionResult{
				Message:     "到期时间已返回",
				DisplayText: "到期时间数据异常，请联系人工客服。",
			}, nil
		}
		if !expireAt.After(now) {
			profile.TrialStatus = "expired"
			_ = a.db.UpsertPrivateUserProfile(profile)
			return WebAppPrivateActionResult{
				Message:     "到期时间已返回",
				DisplayText: fmt.Sprintf("你的使用权限已到期。\n到期时间：%s\n请联系人工客服续期。", formatTime(expireAt)),
			}, nil
		}
		return WebAppPrivateActionResult{
			Message: "到期时间已返回",
			DisplayText: strings.Join([]string{
				fmt.Sprintf("到期时间：%s", formatTime(expireAt)),
				fmt.Sprintf("剩余时长：%s", formatDuration(expireAt.Sub(now))),
			}, "\n"),
		}, nil
	case "人工客服", "联系管理员":
		supportURL := normalizeSupportURL(a.cfg.SupportURL)
		hasSupportInbox := a.cfg.SupportChatID != 0
		if hasSupportInbox {
			if err := a.notifySupportWithUser("人工客服", userID, nil, "人工客服请求"); err != nil {
				log.Printf("WebApp 转发人工客服请求失败: %v", err)
			}
		}
		lines := []string{}
		if hasSupportInbox {
			lines = append(lines, "已提交人工客服请求。")
		}
		if supportURL != "" {
			if !hasSupportInbox {
				lines = append(lines, "当前未配置自动转发，已为你提供人工客服直达入口。")
			}
			lines = append(lines, "你也可以点击下方按钮直接联系人工客服。")
		}
		if len(lines) == 0 {
			lines = append(lines, "当前未配置客服通道，请联系管理员。")
		}
		return WebAppPrivateActionResult{
			Message:     "人工客服请求已处理",
			DisplayText: strings.Join(lines, "\n"),
			OpenURL:     supportURL,
		}, nil
	default:
		return WebAppPrivateActionResult{}, ErrUnsupportedWebAppPrivateAction
	}
}

func (a *App) ExecuteWebAppCommand(userID, targetChatID int64, command string) error {
	cmd := strings.TrimSpace(command)
	if targetChatID == 0 || userID == 0 {
		return errors.New("目标群ID与用户ID不能为空")
	}
	if cmd == "" {
		return errors.New("命令不能为空")
	}
	user := &tgbotapi.User{
		ID:        userID,
		UserName:  strconv.FormatInt(userID, 10),
		FirstName: "WebAppOperator",
	}
	return a.executeAsUserGroupCommand(context.Background(), targetChatID, 0, user, cmd)
}

func (a *App) ListPrivilegedGroups(userID int64) ([]db.GroupConfig, error) {
	if a == nil || a.db == nil || userID == 0 {
		return nil, nil
	}
	return a.db.ListPrivilegedGroups(userID)
}

func (a *App) CanAccessWebAppGroup(chatID, userID int64) bool {
	if a == nil || a.db == nil || chatID == 0 || userID == 0 {
		return false
	}
	return a.db.IsPrivileged(chatID, userID) || a.IsGroupMember(chatID, userID)
}

func (a *App) ListWebAppAccessibleGroups(userID int64) ([]db.GroupConfig, error) {
	if a == nil || a.db == nil || userID == 0 {
		return nil, nil
	}
	rows, err := a.db.ListAllGroups()
	if err != nil {
		return nil, err
	}
	out := make([]db.GroupConfig, 0, len(rows))
	for _, row := range rows {
		if a.CanAccessWebAppGroup(row.ChatID, userID) {
			out = append(out, row)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ChatID < out[j].ChatID })
	return out, nil
}

func (a *App) IsGroupMember(chatID, userID int64) bool {
	if a != nil && a.isGroupMemberFn != nil {
		return a.isGroupMemberFn(chatID, userID)
	}
	if a == nil || a.api == nil || chatID == 0 || userID == 0 {
		return false
	}
	member, err := a.api.GetChatMember(tgbotapi.GetChatMemberConfig{
		ChatConfigWithUser: tgbotapi.ChatConfigWithUser{
			ChatID: chatID,
			UserID: userID,
		},
	})
	if err != nil {
		return false
	}
	return isActiveGroupMemberStatus(member.Status)
}

func (a *App) handleAutoReplyAndForward(msg *tgbotapi.Message, g db.GroupConfig, text string) error {
	templateData := messageTemplateData{
		ChatID: msg.Chat.ID,
		Text:   text,
		Now:    utils.BeijingNow(a.cfg.Timezone).Format("2006-01-02 15:04:05"),
	}
	if msg.From != nil {
		templateData.UserID = msg.From.ID
		templateData.Username = strings.TrimSpace(msg.From.UserName)
		templateData.Nickname = composeNickname(msg.From.FirstName, msg.From.LastName)
	}

	if g.AutoReplyEnabled == 1 {
		if replies, err := a.db.ListAutoReplies(msg.Chat.ID); err == nil {
			var fallback string
			for _, r := range replies {
				if strings.TrimSpace(r.Keyword) == "" && fallback == "" {
					fallback = r.Content
				}
				if strings.TrimSpace(r.Keyword) != "" && strings.Contains(text, r.Keyword) {
					rendered := a.renderTemplateMessage(r.Content, templateData)
					_ = a.replyWithParseMode(msg.Chat.ID, msg.MessageID, rendered.Text, rendered.ParseMode)
					break
				}
			}
			if fallback != "" {
				rendered := a.renderTemplateMessage(fallback, templateData)
				_ = a.replyWithParseMode(msg.Chat.ID, msg.MessageID, rendered.Text, rendered.ParseMode)
			}
		}
	}
	if rules, err := a.db.ListForwardRules(msg.Chat.ID); err == nil {
		for _, rule := range rules {
			if strings.TrimSpace(rule.Keyword) != "" && !strings.Contains(text, rule.Keyword) {
				continue
			}
			targets := strings.Split(rule.TargetChatIDs, ",")
			for _, raw := range targets {
				raw = strings.TrimSpace(raw)
				if raw == "" {
					continue
				}
				cid, err := strconv.ParseInt(raw, 10, 64)
				if err != nil {
					continue
				}
				fwd := tgbotapi.NewForward(cid, msg.Chat.ID, msg.MessageID)
				if err := a.sendWithPriority(outboundPriorityNormalChat, fwd); err != nil {
					log.Printf("关键词转发失败: %d -> %d", msg.Chat.ID, cid)
				}
			}
		}
	}
	return nil
}

func (a *App) resolveBroadcastGroups(sourceChatID int64, rawTags string) ([]db.GroupConfig, error) {
	tags := utils.SplitTags(rawTags)
	if len(tags) == 0 {
		return a.listTargetGroups(sourceChatID)
	}

	uniq := make(map[int64]db.GroupConfig)
	tagRows, err := a.db.ListGroupsByTags(tags)
	if err != nil {
		return nil, err
	}
	for _, row := range tagRows {
		uniq[row.ChatID] = row
	}
	for _, tag := range tags {
		typeRows, err := a.db.ListGroupsByType(tag)
		if err != nil {
			return nil, err
		}
		for _, row := range typeRows {
			uniq[row.ChatID] = row
		}
	}

	rows := make([]db.GroupConfig, 0, len(uniq))
	for _, row := range uniq {
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].ChatID < rows[j].ChatID })
	return rows, nil
}

func (a *App) handleBatchSetGroupTags(msg *tgbotapi.Message, g db.GroupConfig, text string) error {
	if msg == nil || msg.Chat == nil || msg.From == nil {
		return nil
	}
	if !strings.HasPrefix(strings.TrimSpace(text), "批量设置群标签") {
		return nil
	}
	if !a.requirePrivileged(msg, g) {
		return nil
	}
	botID := int64(0)
	if a != nil && a.api != nil {
		botID = a.api.Self.ID
	}
	if botID == 0 {
		return a.reply(msg.Chat.ID, msg.MessageID, "机器人身份异常，请稍后重试。")
	}
	rows, err := a.ListPrivilegedGroups(msg.From.ID)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return a.reply(msg.Chat.ID, msg.MessageID, "未找到你有权限的群，无法批量设置。")
	}

	eligible := make([]db.GroupConfig, 0, len(rows))
	for _, row := range rows {
		if !a.IsGroupMember(row.ChatID, botID) {
			continue
		}
		eligible = append(eligible, row)
	}
	if len(eligible) == 0 {
		return a.reply(msg.Chat.ID, msg.MessageID, "未找到机器人在群内的可管理群，无法批量设置。")
	}
	session := batchTagSelectionSession{
		InitiatorUserID: msg.From.ID,
		Groups:          eligible,
		CurrentPage:     0,
		UpdatedAt:       a.currentTime(),
	}
	a.setBatchTagSession(msg.Chat.ID, session)
	a.clearBatchTypeSession(msg.Chat.ID)

	pageText, markup := a.renderBatchTagPage(session)
	m := tgbotapi.NewMessage(msg.Chat.ID, pageText)
	m.ReplyToMessageID = msg.MessageID
	if len(markup.InlineKeyboard) > 0 {
		m.ReplyMarkup = markup
	}
	return a.sendWithPriority(outboundPriorityNormalChat, m)
}

func (a *App) setBatchTagSession(chatID int64, session batchTagSelectionSession) {
	if a == nil || chatID == 0 {
		return
	}
	a.batchTagMu.Lock()
	defer a.batchTagMu.Unlock()
	if a.batchTagSessions == nil {
		a.batchTagSessions = map[int64]batchTagSelectionSession{}
	}
	a.batchTagSessions[chatID] = session
}

func (a *App) getBatchTagSession(chatID int64) (batchTagSelectionSession, bool) {
	if a == nil || chatID == 0 {
		return batchTagSelectionSession{}, false
	}
	a.batchTagMu.Lock()
	defer a.batchTagMu.Unlock()
	if a.batchTagSessions == nil {
		return batchTagSelectionSession{}, false
	}
	session, ok := a.batchTagSessions[chatID]
	if !ok {
		return batchTagSelectionSession{}, false
	}
	if !session.UpdatedAt.IsZero() && a.currentTime().Sub(session.UpdatedAt) > batchTagSessionTTL {
		delete(a.batchTagSessions, chatID)
		return batchTagSelectionSession{}, false
	}
	return session, true
}

func (a *App) clearBatchTagSession(chatID int64) {
	if a == nil || chatID == 0 {
		return
	}
	a.batchTagMu.Lock()
	defer a.batchTagMu.Unlock()
	if a.batchTagSessions == nil {
		return
	}
	delete(a.batchTagSessions, chatID)
}

func (a *App) renderBatchTagPage(session batchTagSelectionSession) (string, tgbotapi.InlineKeyboardMarkup) {
	if len(session.Groups) == 0 {
		return "未找到可设置群。", tgbotapi.NewInlineKeyboardMarkup()
	}
	totalPages := (len(session.Groups) + batchTagPageSize - 1) / batchTagPageSize
	page := session.CurrentPage
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}
	start := page * batchTagPageSize
	end := start + batchTagPageSize
	if end > len(session.Groups) {
		end = len(session.Groups)
	}

	lines := []string{
		"批量设置群标签（可设置群列表）",
		fmt.Sprintf("页码：%d/%d（每页%d群）", page+1, totalPages, batchTagPageSize),
		"--------------------------------",
	}
	for i := start; i < end; i++ {
		row := session.Groups[i]
		title := strings.TrimSpace(row.Title)
		if title == "" {
			title = "未命名群"
		}
		currentTags := strings.Join(utils.SplitTags(row.GroupTags), "、")
		if currentTags == "" {
			currentTags = "未设置"
		}
		lines = append(lines, fmt.Sprintf("%02d. %s", i+1, title))
		lines = append(lines, fmt.Sprintf("    标签：%s", currentTags))
	}
	lines = append(lines, "--------------------------------")
	lines = append(lines, "发送格式：序号 空格 序号 空格 标签")
	lines = append(lines, "示例：12 38 52 cs2")

	return strings.Join(lines, "\n"), buildBatchTagPageMarkup(page, totalPages)
}

func buildBatchTagPageMarkup(page, totalPages int) tgbotapi.InlineKeyboardMarkup {
	if totalPages <= 0 {
		totalPages = 1
	}
	buttons := make([]tgbotapi.InlineKeyboardButton, 0, 3)
	prevData := buildBatchTagPageCallbackData(page - 1)
	nextData := buildBatchTagPageCallbackData(page + 1)
	if page > 0 {
		prevData = buildBatchTagPageCallbackData(page - 1)
	} else {
		prevData = batchTagCallbackPrefix + "edge_prev"
	}
	if page < totalPages-1 {
		nextData = buildBatchTagPageCallbackData(page + 1)
	} else {
		nextData = batchTagCallbackPrefix + "edge_next"
	}
	buttons = append(buttons, tgbotapi.NewInlineKeyboardButtonData("⬅️ 上一页", prevData))
	buttons = append(buttons, tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("%d/%d", page+1, totalPages), batchTagCallbackPrefix+"noop"))
	buttons = append(buttons, tgbotapi.NewInlineKeyboardButtonData("下一页 ➡️", nextData))
	if page < totalPages-1 {
		buttons[2] = tgbotapi.NewInlineKeyboardButtonData("下一页 ➡️", buildBatchTagPageCallbackData(page+1))
	}
	return tgbotapi.NewInlineKeyboardMarkup(buttons)
}

func buildBatchTagPageCallbackData(page int) string {
	return fmt.Sprintf("%sp:%d", batchTagCallbackPrefix, page)
}

func (a *App) handleBatchTagCallback(ctx context.Context, cq *tgbotapi.CallbackQuery) error {
	_ = ctx
	if cq == nil || cq.Message == nil || cq.Message.Chat == nil || cq.From == nil {
		return nil
	}
	session, ok := a.getBatchTagSession(cq.Message.Chat.ID)
	if !ok {
		a.answerCallback(cq.ID, "会话已过期，请重新发送【批量设置群标签】。", true)
		return nil
	}
	if session.InitiatorUserID != cq.From.ID {
		a.answerCallback(cq.ID, "仅发起人可翻页。", true)
		return nil
	}
	action := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(cq.Data), batchTagCallbackPrefix))
	if action == "noop" {
		a.answerCallback(cq.ID, "请选择上下页按钮。", false)
		return nil
	}
	if action == "edge_prev" {
		a.answerCallback(cq.ID, "已是第一页。", false)
		return nil
	}
	if action == "edge_next" {
		a.answerCallback(cq.ID, "已是最后一页。", false)
		return nil
	}
	if !strings.HasPrefix(action, "p:") {
		a.answerCallback(cq.ID, "未知翻页操作。", true)
		return nil
	}
	page, err := strconv.Atoi(strings.TrimPrefix(action, "p:"))
	if err != nil {
		a.answerCallback(cq.ID, "页码无效。", true)
		return nil
	}
	totalPages := (len(session.Groups) + batchTagPageSize - 1) / batchTagPageSize
	if totalPages <= 0 {
		a.answerCallback(cq.ID, "暂无可设置群。", true)
		return nil
	}
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}
	session.CurrentPage = page
	session.UpdatedAt = a.currentTime()
	a.setBatchTagSession(cq.Message.Chat.ID, session)

	text, markup := a.renderBatchTagPage(session)
	edit := tgbotapi.NewEditMessageTextAndMarkup(cq.Message.Chat.ID, cq.Message.MessageID, text, markup)
	if err := a.sendWithPriority(outboundPriorityNormalChat, edit); err != nil && !isMessageNotModified(err) {
		a.answerCallback(cq.ID, "翻页失败，请稍后重试。", true)
		return err
	}
	a.answerCallback(cq.ID, fmt.Sprintf("已切换到第%d页", page+1), false)
	return nil
}

func (a *App) handleBatchTagSelectionInput(msg *tgbotapi.Message, g db.GroupConfig, text string) (bool, error) {
	if msg == nil || msg.Chat == nil || msg.From == nil {
		return false, nil
	}
	session, ok := a.getBatchTagSession(msg.Chat.ID)
	if !ok {
		return false, nil
	}
	if session.InitiatorUserID != msg.From.ID {
		return false, nil
	}
	if !a.requirePrivileged(msg, g) {
		return true, nil
	}
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) < 2 {
		return true, a.reply(msg.Chat.ID, msg.MessageID, "格式：序号 空格 序号 空格 标签\n示例：12 38 52 cs2")
	}
	tagTokens := utils.SplitTags(strings.TrimSpace(fields[len(fields)-1]))
	if len(tagTokens) == 0 {
		return true, a.reply(msg.Chat.ID, msg.MessageID, "标签不能为空。\n示例：12 38 52 cs2")
	}
	finalTags := strings.Join(tagTokens, ",")
	validIndices := make([]int, 0, len(fields)-1)
	seen := make(map[int]struct{}, len(fields)-1)
	invalidInputs := make([]string, 0, len(fields)-1)
	for _, raw := range fields[:len(fields)-1] {
		index, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || index < 1 || index > len(session.Groups) {
			invalidInputs = append(invalidInputs, raw)
			continue
		}
		if _, exists := seen[index]; exists {
			continue
		}
		seen[index] = struct{}{}
		validIndices = append(validIndices, index)
	}
	if len(validIndices) == 0 {
		return true, a.reply(msg.Chat.ID, msg.MessageID, "没有可用序号，请先翻页查看可设置群后重试。")
	}
	sort.Ints(validIndices)
	successCount := 0
	failedCount := 0
	failedGroups := make([]string, 0, 5)
	for _, index := range validIndices {
		groupRow := session.Groups[index-1]
		if err := a.db.UpdateGroupFields(groupRow.ChatID, map[string]any{"group_tags": finalTags}); err != nil {
			failedCount++
			if len(failedGroups) < 5 {
				failedGroups = append(failedGroups, fmt.Sprintf("%02d.%s(%d)", index, orDefault(strings.TrimSpace(groupRow.Title), "未命名群"), groupRow.ChatID))
			}
			continue
		}
		successCount++
	}
	a.clearBatchTagSession(msg.Chat.ID)
	lines := []string{
		"批量设置群标签结果：",
		fmt.Sprintf("目标标签：%s", strings.Join(tagTokens, "、")),
		fmt.Sprintf("输入序号：%d", len(fields)-1),
		fmt.Sprintf("有效序号：%d", len(validIndices)),
		fmt.Sprintf("无效序号：%d", len(invalidInputs)),
		fmt.Sprintf("设置成功：%d", successCount),
		fmt.Sprintf("设置失败：%d", failedCount),
		"会话状态：已结束（如需继续请重新发送【批量设置群标签】）",
	}
	if len(invalidInputs) > 0 {
		lines = append(lines, "无效项："+strings.Join(invalidInputs, "、"))
	}
	if len(failedGroups) > 0 {
		lines = append(lines, "失败群："+strings.Join(failedGroups, "；"))
	}
	return true, a.reply(msg.Chat.ID, msg.MessageID, strings.Join(lines, "\n"))
}

func (a *App) handleBatchSetGroupTypes(msg *tgbotapi.Message, g db.GroupConfig, text string) error {
	if msg == nil || msg.Chat == nil || msg.From == nil {
		return nil
	}
	if !strings.HasPrefix(strings.TrimSpace(text), "批量设置群类型") {
		return nil
	}
	if !a.requirePrivileged(msg, g) {
		return nil
	}
	botID := int64(0)
	if a != nil && a.api != nil {
		botID = a.api.Self.ID
	}
	if botID == 0 {
		return a.reply(msg.Chat.ID, msg.MessageID, "机器人身份异常，请稍后重试。")
	}
	rows, err := a.ListPrivilegedGroups(msg.From.ID)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return a.reply(msg.Chat.ID, msg.MessageID, "未找到你有权限的群，无法批量设置。")
	}

	eligible := make([]db.GroupConfig, 0, len(rows))
	for _, row := range rows {
		if !a.IsGroupMember(row.ChatID, botID) {
			continue
		}
		eligible = append(eligible, row)
	}
	if len(eligible) == 0 {
		return a.reply(msg.Chat.ID, msg.MessageID, "未找到机器人在群内的可管理群，无法批量设置。")
	}
	session := batchTypeSelectionSession{
		InitiatorUserID: msg.From.ID,
		Groups:          eligible,
		CurrentPage:     0,
		UpdatedAt:       a.currentTime(),
	}
	a.setBatchTypeSession(msg.Chat.ID, session)
	a.clearBatchTagSession(msg.Chat.ID)

	pageText, markup := a.renderBatchTypePage(session)
	m := tgbotapi.NewMessage(msg.Chat.ID, pageText)
	m.ReplyToMessageID = msg.MessageID
	if len(markup.InlineKeyboard) > 0 {
		m.ReplyMarkup = markup
	}
	return a.sendWithPriority(outboundPriorityNormalChat, m)
}

func (a *App) setBatchTypeSession(chatID int64, session batchTypeSelectionSession) {
	if a == nil || chatID == 0 {
		return
	}
	a.batchTypeMu.Lock()
	defer a.batchTypeMu.Unlock()
	if a.batchTypeSessions == nil {
		a.batchTypeSessions = map[int64]batchTypeSelectionSession{}
	}
	a.batchTypeSessions[chatID] = session
}

func (a *App) getBatchTypeSession(chatID int64) (batchTypeSelectionSession, bool) {
	if a == nil || chatID == 0 {
		return batchTypeSelectionSession{}, false
	}
	a.batchTypeMu.Lock()
	defer a.batchTypeMu.Unlock()
	if a.batchTypeSessions == nil {
		return batchTypeSelectionSession{}, false
	}
	session, ok := a.batchTypeSessions[chatID]
	if !ok {
		return batchTypeSelectionSession{}, false
	}
	if !session.UpdatedAt.IsZero() && a.currentTime().Sub(session.UpdatedAt) > batchTagSessionTTL {
		delete(a.batchTypeSessions, chatID)
		return batchTypeSelectionSession{}, false
	}
	return session, true
}

func (a *App) clearBatchTypeSession(chatID int64) {
	if a == nil || chatID == 0 {
		return
	}
	a.batchTypeMu.Lock()
	defer a.batchTypeMu.Unlock()
	if a.batchTypeSessions == nil {
		return
	}
	delete(a.batchTypeSessions, chatID)
}

func (a *App) renderBatchTypePage(session batchTypeSelectionSession) (string, tgbotapi.InlineKeyboardMarkup) {
	if len(session.Groups) == 0 {
		return "未找到可设置群。", tgbotapi.NewInlineKeyboardMarkup()
	}
	totalPages := (len(session.Groups) + batchTagPageSize - 1) / batchTagPageSize
	page := session.CurrentPage
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}
	start := page * batchTagPageSize
	end := start + batchTagPageSize
	if end > len(session.Groups) {
		end = len(session.Groups)
	}

	lines := []string{
		"批量设置群类型（可设置群列表）",
		fmt.Sprintf("页码：%d/%d（每页%d群）", page+1, totalPages, batchTagPageSize),
		"--------------------------------",
	}
	for i := start; i < end; i++ {
		row := session.Groups[i]
		title := strings.TrimSpace(row.Title)
		if title == "" {
			title = "未命名群"
		}
		currentType := strings.TrimSpace(row.GroupType)
		if currentType == "" {
			currentType = "未设置"
		}
		lines = append(lines, fmt.Sprintf("%02d. %s", i+1, title))
		lines = append(lines, fmt.Sprintf("    类型：%s", currentType))
	}
	lines = append(lines, "--------------------------------")
	lines = append(lines, "发送格式：序号 空格 序号 空格 类型")
	lines = append(lines, "示例：12 38 52 代收")

	return strings.Join(lines, "\n"), buildBatchTypePageMarkup(page, totalPages)
}

func buildBatchTypePageMarkup(page, totalPages int) tgbotapi.InlineKeyboardMarkup {
	if totalPages <= 0 {
		totalPages = 1
	}
	buttons := make([]tgbotapi.InlineKeyboardButton, 0, 3)
	prevData := buildBatchTypePageCallbackData(page - 1)
	nextData := buildBatchTypePageCallbackData(page + 1)
	if page > 0 {
		prevData = buildBatchTypePageCallbackData(page - 1)
	} else {
		prevData = batchTypeCallbackPrefix + "edge_prev"
	}
	if page < totalPages-1 {
		nextData = buildBatchTypePageCallbackData(page + 1)
	} else {
		nextData = batchTypeCallbackPrefix + "edge_next"
	}
	buttons = append(buttons, tgbotapi.NewInlineKeyboardButtonData("⬅️ 上一页", prevData))
	buttons = append(buttons, tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("%d/%d", page+1, totalPages), batchTypeCallbackPrefix+"noop"))
	buttons = append(buttons, tgbotapi.NewInlineKeyboardButtonData("下一页 ➡️", nextData))
	if page < totalPages-1 {
		buttons[2] = tgbotapi.NewInlineKeyboardButtonData("下一页 ➡️", buildBatchTypePageCallbackData(page+1))
	}
	return tgbotapi.NewInlineKeyboardMarkup(buttons)
}

func buildBatchTypePageCallbackData(page int) string {
	return fmt.Sprintf("%sp:%d", batchTypeCallbackPrefix, page)
}

func (a *App) handleBatchTypeCallback(ctx context.Context, cq *tgbotapi.CallbackQuery) error {
	_ = ctx
	if cq == nil || cq.Message == nil || cq.Message.Chat == nil || cq.From == nil {
		return nil
	}
	session, ok := a.getBatchTypeSession(cq.Message.Chat.ID)
	if !ok {
		a.answerCallback(cq.ID, "会话已过期，请重新发送【批量设置群类型】。", true)
		return nil
	}
	if session.InitiatorUserID != cq.From.ID {
		a.answerCallback(cq.ID, "仅发起人可翻页。", true)
		return nil
	}
	action := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(cq.Data), batchTypeCallbackPrefix))
	if action == "noop" {
		a.answerCallback(cq.ID, "请选择上下页按钮。", false)
		return nil
	}
	if action == "edge_prev" {
		a.answerCallback(cq.ID, "已是第一页。", false)
		return nil
	}
	if action == "edge_next" {
		a.answerCallback(cq.ID, "已是最后一页。", false)
		return nil
	}
	if !strings.HasPrefix(action, "p:") {
		a.answerCallback(cq.ID, "未知翻页操作。", true)
		return nil
	}
	page, err := strconv.Atoi(strings.TrimPrefix(action, "p:"))
	if err != nil {
		a.answerCallback(cq.ID, "页码无效。", true)
		return nil
	}
	totalPages := (len(session.Groups) + batchTagPageSize - 1) / batchTagPageSize
	if totalPages <= 0 {
		a.answerCallback(cq.ID, "暂无可设置群。", true)
		return nil
	}
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}
	session.CurrentPage = page
	session.UpdatedAt = a.currentTime()
	a.setBatchTypeSession(cq.Message.Chat.ID, session)

	text, markup := a.renderBatchTypePage(session)
	edit := tgbotapi.NewEditMessageTextAndMarkup(cq.Message.Chat.ID, cq.Message.MessageID, text, markup)
	if err := a.sendWithPriority(outboundPriorityNormalChat, edit); err != nil && !isMessageNotModified(err) {
		a.answerCallback(cq.ID, "翻页失败，请稍后重试。", true)
		return err
	}
	a.answerCallback(cq.ID, fmt.Sprintf("已切换到第%d页", page+1), false)
	return nil
}

func (a *App) handleBatchTypeSelectionInput(msg *tgbotapi.Message, g db.GroupConfig, text string) (bool, error) {
	if msg == nil || msg.Chat == nil || msg.From == nil {
		return false, nil
	}
	session, ok := a.getBatchTypeSession(msg.Chat.ID)
	if !ok {
		return false, nil
	}
	if session.InitiatorUserID != msg.From.ID {
		return false, nil
	}
	if !a.requirePrivileged(msg, g) {
		return true, nil
	}
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) < 2 {
		return true, a.reply(msg.Chat.ID, msg.MessageID, "格式：序号 空格 序号 空格 类型\n示例：12 38 52 代收")
	}
	finalType := strings.TrimSpace(fields[len(fields)-1])
	if finalType == "" {
		return true, a.reply(msg.Chat.ID, msg.MessageID, "类型不能为空。\n示例：12 38 52 代收")
	}
	validIndices := make([]int, 0, len(fields)-1)
	seen := make(map[int]struct{}, len(fields)-1)
	invalidInputs := make([]string, 0, len(fields)-1)
	for _, raw := range fields[:len(fields)-1] {
		index, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || index < 1 || index > len(session.Groups) {
			invalidInputs = append(invalidInputs, raw)
			continue
		}
		if _, exists := seen[index]; exists {
			continue
		}
		seen[index] = struct{}{}
		validIndices = append(validIndices, index)
	}
	if len(validIndices) == 0 {
		return true, a.reply(msg.Chat.ID, msg.MessageID, "没有可用序号，请先翻页查看可设置群后重试。")
	}
	sort.Ints(validIndices)
	successCount := 0
	failedCount := 0
	failedGroups := make([]string, 0, 5)
	for _, index := range validIndices {
		groupRow := session.Groups[index-1]
		if err := a.db.UpdateGroupFields(groupRow.ChatID, map[string]any{"group_type": finalType}); err != nil {
			failedCount++
			if len(failedGroups) < 5 {
				failedGroups = append(failedGroups, fmt.Sprintf("%02d.%s(%d)", index, orDefault(strings.TrimSpace(groupRow.Title), "未命名群"), groupRow.ChatID))
			}
			continue
		}
		successCount++
	}
	a.clearBatchTypeSession(msg.Chat.ID)
	lines := []string{
		"批量设置群类型结果：",
		fmt.Sprintf("目标类型：%s", finalType),
		fmt.Sprintf("输入序号：%d", len(fields)-1),
		fmt.Sprintf("有效序号：%d", len(validIndices)),
		fmt.Sprintf("无效序号：%d", len(invalidInputs)),
		fmt.Sprintf("设置成功：%d", successCount),
		fmt.Sprintf("设置失败：%d", failedCount),
		"会话状态：已结束（如需继续请重新发送【批量设置群类型】）",
	}
	if len(invalidInputs) > 0 {
		lines = append(lines, "无效项："+strings.Join(invalidInputs, "、"))
	}
	if len(failedGroups) > 0 {
		lines = append(lines, "失败群："+strings.Join(failedGroups, "；"))
	}
	return true, a.reply(msg.Chat.ID, msg.MessageID, strings.Join(lines, "\n"))
}

func (a *App) setInteractiveBroadcastAwait(chatID, userID int64) {
	if a == nil || chatID == 0 || userID == 0 {
		return
	}
	a.interactiveBroadcastMu.Lock()
	defer a.interactiveBroadcastMu.Unlock()
	if a.interactiveBroadcastState == nil {
		a.interactiveBroadcastState = map[int64]interactiveBroadcastSession{}
	}
	a.interactiveBroadcastState[chatID] = interactiveBroadcastSession{
		InitiatorUserID: userID,
		AwaitingContent: true,
		Content:         "",
		ContentMode:     "",
		ContentPreview:  "",
		SourceChatID:    0,
		SourceMessageID: 0,
		Tags:            nil,
		UpdatedAt:       a.currentTime(),
	}
}

func (a *App) setInteractiveBroadcastSession(chatID int64, session interactiveBroadcastSession) {
	if a == nil || chatID == 0 {
		return
	}
	a.interactiveBroadcastMu.Lock()
	defer a.interactiveBroadcastMu.Unlock()
	if a.interactiveBroadcastState == nil {
		a.interactiveBroadcastState = map[int64]interactiveBroadcastSession{}
	}
	a.interactiveBroadcastState[chatID] = session
}

func (a *App) getInteractiveBroadcastSession(chatID int64) (interactiveBroadcastSession, bool) {
	if a == nil || chatID == 0 {
		return interactiveBroadcastSession{}, false
	}
	a.interactiveBroadcastMu.Lock()
	defer a.interactiveBroadcastMu.Unlock()
	if a.interactiveBroadcastState == nil {
		return interactiveBroadcastSession{}, false
	}
	session, ok := a.interactiveBroadcastState[chatID]
	return session, ok
}

func (a *App) clearInteractiveBroadcastSession(chatID int64) {
	if a == nil || chatID == 0 {
		return
	}
	a.interactiveBroadcastMu.Lock()
	defer a.interactiveBroadcastMu.Unlock()
	if a.interactiveBroadcastState == nil {
		return
	}
	delete(a.interactiveBroadcastState, chatID)
}

func (a *App) listInteractiveBroadcastTags() ([]string, error) {
	if a == nil || a.db == nil {
		return nil, nil
	}
	rows, err := a.db.ListAllGroups()
	if err != nil {
		return nil, err
	}
	uniq := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if groupType := strings.TrimSpace(row.GroupType); groupType != "" {
			uniq[groupType] = struct{}{}
		}
		for _, tag := range utils.SplitTags(row.GroupTags) {
			tag = strings.TrimSpace(tag)
			if tag == "" {
				continue
			}
			uniq[tag] = struct{}{}
		}
	}
	tags := make([]string, 0, len(uniq))
	for tag := range uniq {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	if len(tags) > interactiveBroadcastMaxTags {
		tags = tags[:interactiveBroadcastMaxTags]
	}
	return tags, nil
}

func buildInteractiveBroadcastTagCallbackData(index int) string {
	return fmt.Sprintf("%s%d", broadcastTagCallbackPrefix, index)
}

func buildInteractiveBroadcastTagMarkup(tags []string) tgbotapi.InlineKeyboardMarkup {
	rows := make([][]tgbotapi.InlineKeyboardButton, 0, (len(tags)+1)/2)
	current := make([]tgbotapi.InlineKeyboardButton, 0, 2)
	for i, tag := range tags {
		current = append(current, tgbotapi.NewInlineKeyboardButtonData(
			fmt.Sprintf("%02d.%s", i+1, tag),
			buildInteractiveBroadcastTagCallbackData(i),
		))
		if len(current) == 2 {
			rows = append(rows, current)
			current = make([]tgbotapi.InlineKeyboardButton, 0, 2)
		}
	}
	if len(current) > 0 {
		rows = append(rows, current)
	}
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func isInteractiveBroadcastMediaMessage(msg *tgbotapi.Message) bool {
	if msg == nil {
		return false
	}
	return len(msg.Photo) > 0 || msg.Video != nil || msg.Voice != nil
}

func interactiveBroadcastContentPreview(msg *tgbotapi.Message, text string) string {
	content := strings.TrimSpace(text)
	switch {
	case msg != nil && len(msg.Photo) > 0:
		if content != "" {
			return fmt.Sprintf("图片+文字：%s", trimForPreview(content, 24))
		}
		return "图片"
	case msg != nil && msg.Video != nil:
		if content != "" {
			return fmt.Sprintf("视频+文字：%s", trimForPreview(content, 24))
		}
		return "视频"
	case msg != nil && msg.Voice != nil:
		if content != "" {
			return fmt.Sprintf("语音+文字：%s", trimForPreview(content, 24))
		}
		return "语音"
	default:
		if content == "" {
			return "文本"
		}
		return content
	}
}

func (a *App) handleInteractiveBroadcastDraftInput(msg *tgbotapi.Message, g db.GroupConfig, text string) (bool, error) {
	if msg == nil || msg.Chat == nil || msg.From == nil {
		return false, nil
	}
	if g.AtBroadcastEnabled != 1 {
		return false, nil
	}
	session, ok := a.getInteractiveBroadcastSession(msg.Chat.ID)
	if !ok || !session.AwaitingContent {
		return false, nil
	}
	if session.InitiatorUserID != msg.From.ID {
		return false, nil
	}
	content := strings.TrimSpace(text)
	hasMedia := isInteractiveBroadcastMediaMessage(msg)
	if !hasMedia && content == "" {
		return true, a.reply(msg.Chat.ID, msg.MessageID, "群发内容不能为空，请重新输入（支持文字/图片/视频/语音）。")
	}
	tags, err := a.listInteractiveBroadcastTags()
	if err != nil {
		return true, err
	}
	if len(tags) == 0 {
		return true, a.reply(msg.Chat.ID, msg.MessageID, "后台暂未配置群标签，请先设置群标签后再群发。")
	}

	session.AwaitingContent = false
	session.ContentPreview = interactiveBroadcastContentPreview(msg, content)
	if hasMedia {
		session.ContentMode = "copy"
		session.Content = ""
		session.SourceChatID = msg.Chat.ID
		session.SourceMessageID = msg.MessageID
	} else {
		session.ContentMode = "text"
		session.Content = content
		session.SourceChatID = 0
		session.SourceMessageID = 0
	}
	session.Tags = tags
	session.UpdatedAt = a.currentTime()
	a.setInteractiveBroadcastSession(msg.Chat.ID, session)

	lines := []string{
		"已接收群发内容。",
		"请点击下方标签按钮执行群发（序号对应标签，可重复点击重复群发）。",
		fmt.Sprintf("本次内容：%s", session.ContentPreview),
	}
	m := tgbotapi.NewMessage(msg.Chat.ID, strings.Join(lines, "\n"))
	m.ReplyToMessageID = msg.MessageID
	m.ReplyMarkup = buildInteractiveBroadcastTagMarkup(tags)
	return true, a.sendWithPriority(outboundPriorityNormalChat, m)
}

func (a *App) handleInteractiveBroadcastTagCallback(ctx context.Context, cq *tgbotapi.CallbackQuery) error {
	_ = ctx
	if cq == nil || cq.Message == nil || cq.Message.Chat == nil {
		return nil
	}
	chat := cq.Message.Chat
	if chat.IsPrivate() {
		a.answerCallback(cq.ID, "请在群组中点击标签按钮", true)
		return nil
	}
	group, err := a.db.EnsureGroup(chat.ID, chat.Title)
	if err != nil {
		a.answerCallback(cq.ID, "群组初始化失败", true)
		return err
	}
	if active, reason := a.checkGroupEntitlement(group); !active {
		_ = a.reply(chat.ID, cq.Message.MessageID, reason)
		a.answerCallback(cq.ID, "当前群功能已到期，请先续期", true)
		return nil
	}
	if group.AtBroadcastEnabled != 1 {
		a.answerCallback(cq.ID, "群发信息已关闭，点击无效。", true)
		return nil
	}
	session, ok := a.getInteractiveBroadcastSession(chat.ID)
	if !ok || len(session.Tags) == 0 {
		a.answerCallback(cq.ID, "暂无可群发内容，请先开启并输入内容。", true)
		return nil
	}
	hasTextPayload := strings.TrimSpace(session.Content) != ""
	hasCopyPayload := session.SourceMessageID > 0
	if !hasTextPayload && !hasCopyPayload {
		a.answerCallback(cq.ID, "暂无可群发内容，请先开启并输入内容。", true)
		return nil
	}

	rawIndex := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(cq.Data), broadcastTagCallbackPrefix))
	index, parseErr := strconv.Atoi(rawIndex)
	if parseErr != nil || index < 0 || index >= len(session.Tags) {
		a.answerCallback(cq.ID, "标签序号无效。", true)
		return nil
	}
	tag := session.Tags[index]

	a.answerCallback(cq.ID, "群发任务已提交，后台发送中。", false)
	go a.runInteractiveBroadcastTagJob(chat.ID, cq.Message.MessageID, tag, session)
	return nil
}

func (a *App) runInteractiveBroadcastTagJob(chatID int64, replyTo int, tag string, session interactiveBroadcastSession) {
	startAt := time.Now()
	targetGroups, err := a.resolveBroadcastGroups(chatID, tag)
	if err != nil {
		_ = a.reply(chatID, replyTo, "标签群发执行失败：读取群组失败，请稍后重试。")
		return
	}
	expected := len(targetGroups)
	sent := 0
	if session.ContentMode == "copy" && session.SourceMessageID > 0 {
		sourceChatID := orDefaultInt64(session.SourceChatID, chatID)
		sent, err = a.broadcastForwardToGroups(targetGroups, sourceChatID, session.SourceMessageID)
	} else {
		sent, err = a.broadcastTextToGroups(targetGroups, session.Content, "")
	}
	if err != nil {
		_ = a.reply(chatID, replyTo, "标签群发执行失败，请稍后重试。")
		return
	}
	failed := expected - sent
	if failed < 0 {
		failed = 0
	}
	elapsed := time.Since(startAt).Round(time.Millisecond)

	status := "成功"
	if failed > 0 {
		status = "部分失败"
	}
	preview := strings.TrimSpace(session.ContentPreview)
	if preview == "" {
		preview = strings.TrimSpace(session.Content)
	}
	if preview == "" {
		preview = "媒体消息"
	}
	feedback := []string{
		"标签群发执行结果：",
		fmt.Sprintf("标签：%s", tag),
		fmt.Sprintf("本次发送内容：%s", preview),
		fmt.Sprintf("发送状态：%s", status),
		fmt.Sprintf("发送成功：%d", sent),
		fmt.Sprintf("发送失败：%d", failed),
		fmt.Sprintf("应发数量：%d", expected),
		fmt.Sprintf("总发送群数：%d", sent),
		fmt.Sprintf("耗时：%s", elapsed),
	}
	_ = a.reply(chatID, replyTo, strings.Join(feedback, "\n"))
}

func (a *App) broadcastTextToGroups(groups []db.GroupConfig, content, identity string) (int, error) {
	result, err := a.broadcastTextToGroupsDetailed(groups, content, identity)
	return result.Succeeded, err
}

func (a *App) broadcastForwardToGroups(groups []db.GroupConfig, sourceChatID int64, sourceMessageID int) (int, error) {
	result, err := a.broadcastForwardToGroupsDetailed(groups, sourceChatID, sourceMessageID)
	return result.Succeeded, err
}

func (a *App) broadcastText(sourceChatID int64, content, identity string) (int, error) {
	groups, err := a.listTargetGroups(sourceChatID)
	if err != nil {
		return 0, err
	}
	return a.broadcastTextToGroups(groups, content, identity)
}

func (a *App) broadcastForward(sourceChatID int64, sourceMessageID int) (int, error) {
	groups, err := a.listTargetGroups(sourceChatID)
	if err != nil {
		return 0, err
	}
	return a.broadcastForwardToGroups(groups, sourceChatID, sourceMessageID)
}

func (a *App) mentionAll(msg *tgbotapi.Message) error {
	rows, err := a.db.ListRecentUsers(msg.Chat.ID, 30)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return a.reply(msg.Chat.ID, msg.MessageID, "暂无可@用户。")
	}
	parts := make([]string, 0, len(rows))
	for _, r := range rows {
		if shouldSkipMentionAllUser(r) {
			continue
		}
		name := r.Username
		if strings.TrimSpace(name) == "" {
			name = strconv.FormatInt(r.UserID, 10)
		}
		mention, renderErr := a.renderHTMLMention(r.UserID, name)
		if renderErr != nil {
			return renderErr
		}
		parts = append(parts, mention)
	}
	if len(parts) == 0 {
		return a.reply(msg.Chat.ID, msg.MessageID, "暂无可@用户。")
	}
	return a.replyHTML(msg.Chat.ID, msg.MessageID, strings.Join(parts, " "))
}

func shouldSkipMentionAllUser(user db.GroupUser) bool {
	username := normalizeTelegramUsername(user.Username)
	if username == "" {
		return false
	}
	if username == "groupanonymousbot" {
		return true
	}
	return strings.HasSuffix(username, "bot")
}

func (a *App) handleEvaluation(msg *tgbotapi.Message, text string) error {
	re := regexp.MustCompile(`^评价服务\s+([1-5])(?:\s+(.+))?$`)
	m := re.FindStringSubmatch(text)
	if m == nil {
		return a.reply(msg.Chat.ID, msg.MessageID, "格式：评价服务 1-5 评价内容")
	}
	score, _ := strconv.Atoi(m[1])
	comment := ""
	if len(m) > 2 {
		comment = strings.TrimSpace(m[2])
	}
	_ = a.db.AddEvaluation(msg.Chat.ID, msg.From.ID, strings.ToLower(strings.TrimSpace(msg.From.UserName)), score, comment, "group", 0)
	return a.reply(msg.Chat.ID, msg.MessageID, "感谢评价，已记录。")
}

func (a *App) showEvaluations(msg *tgbotapi.Message, target string) error {
	rows, err := a.db.ListEvaluations(msg.Chat.ID, target)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return a.reply(msg.Chat.ID, msg.MessageID, "暂无评价数据。")
	}
	sum := 0.0
	lines := []string{}
	for i, r := range rows {
		score, _ := r["score"].(int)
		sum += float64(score)
		if i < 10 {
			uname := fmt.Sprintf("%v", r["username"])
			if strings.TrimSpace(uname) == "" || uname == "<nil>" {
				uname = fmt.Sprintf("%v", r["user_id"])
			}
			lines = append(lines, fmt.Sprintf("%s：%d分 %v", uname, score, r["comment"]))
		}
	}
	avg := sum / float64(len(rows))
	content := fmt.Sprintf("最近评价（均分 %.2f）：\n%s", avg, strings.Join(lines, "\n"))
	return a.reply(msg.Chat.ID, msg.MessageID, content)
}

func (a *App) handleBroadcastCommand(ctx context.Context, msg *tgbotapi.Message, g db.GroupConfig, text string) error {
	if !a.requirePrivileged(msg, g) {
		return nil
	}
	switch {
	case strings.HasPrefix(text, "一键群发"):
		content := strings.TrimSpace(strings.TrimPrefix(text, "一键群发"))
		if content == "" {
			return a.reply(msg.Chat.ID, msg.MessageID, "格式：一键群发 内容")
		}
		sent, err := a.broadcastText(msg.Chat.ID, content, "")
		if err != nil {
			return err
		}
		return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("群发完成，发送到 %d 个群。", sent))
	case text == "转发":
		if msg.ReplyToMessage == nil {
			return a.reply(msg.Chat.ID, msg.MessageID, "请回复一条消息后发送【转发】。")
		}
		sent, err := a.broadcastForward(msg.Chat.ID, msg.ReplyToMessage.MessageID)
		if err != nil {
			return err
		}
		return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("转发群发完成，发送到 %d 个群。", sent))
	case strings.HasPrefix(text, "指定@群发"):
		if g.AtBroadcastEnabled != 1 {
			return a.reply(msg.Chat.ID, msg.MessageID, "指定@群发未开启，请先发送【开启@群发信息】。")
		}
		payload := strings.TrimSpace(strings.TrimPrefix(text, "指定@群发"))
		parts := strings.SplitN(payload, " ", 2)
		if len(parts) != 2 {
			return a.reply(msg.Chat.ID, msg.MessageID, "格式：指定@群发 身份 内容")
		}
		sent, err := a.broadcastText(msg.Chat.ID, strings.TrimSpace(parts[1]), strings.TrimSpace(parts[0]))
		if err != nil {
			return err
		}
		return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("指定@群发完成，发送到 %d 个群。", sent))
	case strings.HasPrefix(text, "定时群发"):
		return a.createSchedule(msg, text)
	case text == "查看定时群发":
		return a.listSchedules(msg)
	case strings.HasPrefix(text, "编辑定时群发"):
		return a.editSchedule(msg, text)
	case strings.HasPrefix(text, "重新发送定时群发"):
		return a.resendSchedule(msg, text)
	default:
		return nil
	}
}

func (a *App) listTargetGroups(sourceChatID int64) ([]db.GroupConfig, error) {
	source, err := a.db.GetGroup(sourceChatID)
	if err != nil {
		return nil, err
	}
	tags := utils.SplitTags(source.GroupTags)
	if len(tags) == 0 {
		return []db.GroupConfig{source}, nil
	}
	rows, err := a.db.ListGroupsByTags(tags)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return []db.GroupConfig{source}, nil
	}
	return rows, nil
}

func (a *App) buildIdentityMentions(chatID int64, identity string) (string, error) {
	rows, err := a.db.ListIdentityUsers(chatID, identity)
	if err != nil {
		return "", err
	}
	parts := []string{}
	for i, r := range rows {
		if i >= 20 {
			break
		}
		name := r.Username
		if strings.TrimSpace(name) == "" {
			name = strconv.FormatInt(r.UserID, 10)
		}
		mention, renderErr := a.renderHTMLMention(r.UserID, name)
		if renderErr != nil {
			return "", renderErr
		}
		parts = append(parts, mention)
	}
	return strings.Join(parts, " "), nil
}

func (a *App) renderHTMLMention(userID int64, username string) (string, error) {
	renderer := a.templateRenderer
	if renderer == nil {
		renderer = newTemplateRenderer()
	}
	return renderer.RenderHTMLTemplate(
		`<a href="tg://user?id={{.UserID}}">@{{.Username}}</a>`,
		map[string]any{
			"UserID":   userID,
			"Username": username,
		},
	)
}

func (a *App) createSchedule(msg *tgbotapi.Message, text string) error {
	dt, content, ok := utils.ParseScheduleCommand(text, a.loc)
	if !ok {
		return a.reply(msg.Chat.ID, msg.MessageID, "格式：定时群发 2026-02-16 09:00 内容")
	}
	if dt.Before(time.Now().In(a.loc)) {
		return a.reply(msg.Chat.ID, msg.MessageID, "定时发送时间必须晚于当前时间。")
	}
	sourceMid := 0
	sourceChat := int64(0)
	if content == "" {
		if msg.ReplyToMessage == nil {
			return a.reply(msg.Chat.ID, msg.MessageID, "没有文字内容时，请回复一条消息作为群发内容。")
		}
		content = "[媒体群发]"
		sourceMid = msg.ReplyToMessage.MessageID
		sourceChat = msg.Chat.ID
	}
	sid, err := a.db.AddSchedule(msg.Chat.ID, dt.Format(time.RFC3339), content, sourceChat, sourceMid, "", "", msg.From.ID)
	if err != nil {
		return err
	}
	a.scheduleJob(sid, dt)
	return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("定时群发已创建，任务ID：%d", sid))
}

func (a *App) listSchedules(msg *tgbotapi.Message) error {
	rows, err := a.db.ListSchedulesByChat(msg.Chat.ID, 30)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return a.reply(msg.Chat.ID, msg.MessageID, "暂无定时群发任务。")
	}
	lines := []string{"定时群发任务列表："}
	for i, r := range rows {
		if i >= 20 {
			break
		}
		kind := "文本"
		if r.SourceMessageID > 0 {
			kind = "媒体"
		}
		lines = append(lines, fmt.Sprintf("#%d [%s] %s (%s) %s", r.ID, r.Status, r.SendAt, kind, trimForPreview(r.Content, 20)))
	}
	return a.reply(msg.Chat.ID, msg.MessageID, strings.Join(lines, "\n"))
}

func trimForPreview(v string, n int) string {
	runes := []rune(v)
	if len(runes) <= n {
		return v
	}
	return string(runes[:n]) + "..."
}

func (a *App) editSchedule(msg *tgbotapi.Message, text string) error {
	m := scheduleEditRegex.FindStringSubmatch(text)
	if m == nil {
		return a.reply(msg.Chat.ID, msg.MessageID, "格式：编辑定时群发 任务ID 2026-02-16 09:30 新内容")
	}
	sid, _ := strconv.ParseInt(m[1], 10, 64)
	row, err := a.db.GetSchedule(sid)
	if err != nil || row == nil || row.ChatID != msg.Chat.ID {
		return a.reply(msg.Chat.ID, msg.MessageID, "任务不存在。")
	}
	sec := m[4]
	if sec == "" {
		sec = "00"
	}
	dt, err := time.ParseInLocation("2006-01-02 15:04:05", fmt.Sprintf("%s %s:%s", m[2], m[3], sec), a.loc)
	if err != nil || dt.Before(time.Now().In(a.loc)) {
		return a.reply(msg.Chat.ID, msg.MessageID, "新时间必须晚于当前时间。")
	}
	newContent := strings.TrimSpace(m[5])
	sourceChat := row.SourceChatID
	sourceMid := row.SourceMessageID
	if newContent != "" {
		sourceChat = 0
		sourceMid = 0
	} else if msg.ReplyToMessage != nil {
		sourceChat = msg.Chat.ID
		sourceMid = msg.ReplyToMessage.MessageID
		newContent = row.Content
	}
	if newContent == "" {
		return a.reply(msg.Chat.ID, msg.MessageID, "请提供新内容，或回复一条消息作为媒体群发内容。")
	}
	_ = a.db.UpdateSchedule(sid, map[string]any{
		"send_at":           dt.Format(time.RFC3339),
		"content":           newContent,
		"source_chat_id":    sourceChat,
		"source_message_id": sourceMid,
		"status":            "pending",
	})
	a.scheduleJob(sid, dt)
	return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("任务 #%d 已更新。", sid))
}

func (a *App) resendSchedule(msg *tgbotapi.Message, text string) error {
	re := regexp.MustCompile(`^重新发送定时群发\s+(\d+)$`)
	m := re.FindStringSubmatch(text)
	if m == nil {
		return a.reply(msg.Chat.ID, msg.MessageID, "格式：重新发送定时群发 任务ID")
	}
	sid, _ := strconv.ParseInt(m[1], 10, 64)
	row, err := a.db.GetSchedule(sid)
	if err != nil || row == nil || row.ChatID != msg.Chat.ID {
		return a.reply(msg.Chat.ID, msg.MessageID, "任务不存在。")
	}
	dt := time.Now().In(a.loc).Add(2 * time.Second)
	_ = a.db.UpdateSchedule(sid, map[string]any{"send_at": dt.Format(time.RFC3339), "status": "pending"})
	a.scheduleJob(sid, dt)
	return a.reply(msg.Chat.ID, msg.MessageID, fmt.Sprintf("任务 #%d 已加入重发队列。", sid))
}

func (a *App) restoreSchedules() error {
	rows, err := a.db.ListPendingSchedules()
	if err != nil {
		return err
	}
	for _, row := range rows {
		dt, err := time.Parse(time.RFC3339, row.SendAt)
		if err != nil {
			continue
		}
		a.scheduleJob(row.ID, dt.In(a.loc))
	}
	return nil
}

func (a *App) scheduleJob(scheduleID int64, runAt time.Time) {
	a.schedMu.Lock()
	defer a.schedMu.Unlock()
	if old, ok := a.timers[scheduleID]; ok {
		old.Stop()
	}
	delay := time.Until(runAt)
	if delay <= 0 {
		_ = a.db.SetScheduleStatus(scheduleID, "expired")
		return
	}
	a.timers[scheduleID] = time.AfterFunc(delay, func() {
		_ = a.onScheduleJob(scheduleID)
	})
}

func (a *App) onScheduleJob(scheduleID int64) error {
	row, err := a.db.GetSchedule(scheduleID)
	if err != nil || row == nil || row.Status != "pending" {
		return nil
	}
	groups, err := a.resolveBroadcastGroups(row.ChatID, row.TargetTags)
	if err != nil {
		return err
	}
	sent := 0
	if row.SourceMessageID > 0 {
		sent, err = a.broadcastForwardToGroups(groups, orDefaultInt64(row.SourceChatID, row.ChatID), row.SourceMessageID)
	} else {
		sent, err = a.broadcastTextToGroups(groups, row.Content, row.IdentityName)
	}
	if err != nil {
		return err
	}
	_ = a.db.SetScheduleStatus(scheduleID, "done")
	_ = a.sendWithPriority(outboundPriorityNormalChat, tgbotapi.NewMessage(row.ChatID, fmt.Sprintf("定时群发任务 %d 已完成，发送到 %d 个群。", scheduleID, sent)))
	return nil
}

func orDefaultInt64(v, def int64) int64 {
	if v == 0 {
		return def
	}
	return v
}
