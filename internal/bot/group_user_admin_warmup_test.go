package bot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/db"
)

func TestWarmupGroupAdministratorsCachesAdmins(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "group-user-warmup.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("关闭数据库失败: %v", closeErr)
		}
	}()

	chatID := int64(-1004001)
	if _, err := database.EnsureGroup(chatID, "预热群"); err != nil {
		t.Fatalf("初始化群失败: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{"bookkeeping_enabled": 1}); err != nil {
		t.Fatalf("开启记账失败: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/getChatAdministrators") {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":[{"status":"administrator","user":{"id":7001,"is_bot":false,"first_name":"Nova","username":"admin_a"}}]}`))
	}))
	defer server.Close()

	api := &tgbotapi.BotAPI{
		Token:  "warmup-test-token",
		Client: server.Client(),
	}
	api.SetAPIEndpoint(server.URL + "/bot%s/%s")

	app := &App{db: database, api: api}
	if err := app.warmupGroupAdministrators(context.Background()); err != nil {
		t.Fatalf("管理员预热失败: %v", err)
	}

	row, err := database.FindUserByUsername(chatID, "admin_a")
	if err != nil {
		t.Fatalf("查询快照失败: %v", err)
	}
	if row == nil || row.UserID != 7001 {
		t.Fatalf("管理员快照未写入: %+v", row)
	}

	aliasRow, err := database.FindUserByAlias(chatID, "admin_a")
	if err != nil {
		t.Fatalf("查询 alias 失败: %v", err)
	}
	if aliasRow == nil || aliasRow.UserID != 7001 {
		t.Fatalf("管理员 alias 未写入: %+v", aliasRow)
	}
}

func TestWarmupGroupAdministratorsGracefullyDegradesOnForbidden(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "group-user-warmup-forbidden.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("关闭数据库失败: %v", closeErr)
		}
	}()

	chatID := int64(-1004002)
	if _, err := database.EnsureGroup(chatID, "降级群"); err != nil {
		t.Fatalf("初始化群失败: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{"bookkeeping_enabled": 1}); err != nil {
		t.Fatalf("开启记账失败: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":403,"description":"Forbidden"}`))
	}))
	defer server.Close()

	api := &tgbotapi.BotAPI{
		Token:  "warmup-test-token",
		Client: server.Client(),
	}
	api.SetAPIEndpoint(server.URL + "/bot%s/%s")

	app := &App{db: database, api: api}
	if err := app.warmupGroupAdministrators(context.Background()); err != nil {
		t.Fatalf("预热应自动降级，不应把 403 向上抛出: %v", err)
	}
}

func TestWarmupGroupAdministratorsSkipsDisabledGroups(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "group-user-warmup-skip-disabled.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("关闭数据库失败: %v", closeErr)
		}
	}()

	enabledChatID := int64(-1004003)
	disabledChatID := int64(-1004004)
	if _, err := database.EnsureGroup(enabledChatID, "启用群"); err != nil {
		t.Fatalf("初始化启用群失败: %v", err)
	}
	if _, err := database.EnsureGroup(disabledChatID, "停用群"); err != nil {
		t.Fatalf("初始化停用群失败: %v", err)
	}
	if err := database.UpdateGroupFields(enabledChatID, map[string]any{"bookkeeping_enabled": 1}); err != nil {
		t.Fatalf("开启启用群记账失败: %v", err)
	}

	requestedChatIDs := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/getChatAdministrators") {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form failed: %v", err)
		}
		requestedChatIDs[strings.TrimSpace(r.Form.Get("chat_id"))]++
		_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
	}))
	defer server.Close()

	api := &tgbotapi.BotAPI{
		Token:  "warmup-test-token",
		Client: server.Client(),
	}
	api.SetAPIEndpoint(server.URL + "/bot%s/%s")

	app := &App{db: database, api: api}
	if err := app.warmupGroupAdministrators(context.Background()); err != nil {
		t.Fatalf("管理员预热失败: %v", err)
	}

	if requestedChatIDs["-1004003"] != 1 {
		t.Fatalf("启用群应被预热一次，got=%v", requestedChatIDs)
	}
	if requestedChatIDs["-1004004"] != 0 {
		t.Fatalf("停用群不应参与预热，got=%v", requestedChatIDs)
	}
}

func TestWarmupGroupAdministratorsDisablesGroupOnChatNotFound(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "group-user-warmup-chat-not-found.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("关闭数据库失败: %v", closeErr)
		}
	}()

	chatID := int64(-1004005)
	if _, err := database.EnsureGroup(chatID, "失效群"); err != nil {
		t.Fatalf("初始化群失败: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{
		"bookkeeping_enabled":   1,
		"bookkeeping_cycle_key": "2026-07-08",
	}); err != nil {
		t.Fatalf("初始化记账状态失败: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`))
	}))
	defer server.Close()

	api := &tgbotapi.BotAPI{
		Token:  "warmup-test-token",
		Client: server.Client(),
	}
	api.SetAPIEndpoint(server.URL + "/bot%s/%s")

	app := &App{db: database, api: api}
	if err := app.warmupGroupAdministrators(context.Background()); err != nil {
		t.Fatalf("预热应自动降级，不应把 chat not found 向上抛出: %v", err)
	}

	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	if group.BookkeepingEnabled != 0 {
		t.Fatalf("失效群应被自动停用: %+v", group)
	}
	if strings.TrimSpace(group.BookkeepingCycleKey) != "" {
		t.Fatalf("失效群日切状态应被清空: %+v", group)
	}
}

func TestScanGroupUserProfilesDisablesGroupOnChatNotFound(t *testing.T) {
	database, err := db.NewDatabase(filepath.Join(t.TempDir(), "group-user-scan-chat-not-found.db"))
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("关闭数据库失败: %v", closeErr)
		}
	}()

	chatID := int64(-1004006)
	userID := int64(7006)
	if _, err := database.EnsureGroup(chatID, "巡检失效群"); err != nil {
		t.Fatalf("初始化群失败: %v", err)
	}
	if err := database.UpdateGroupFields(chatID, map[string]any{
		"bookkeeping_enabled":   1,
		"bookkeeping_cycle_key": "2026-07-08",
	}); err != nil {
		t.Fatalf("初始化记账状态失败: %v", err)
	}
	if _, _, err := database.UpsertGroupUser(chatID, userID, "legacy_admin", "Legacy"); err != nil {
		t.Fatalf("写入群成员快照失败: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/getChatMember") {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`))
	}))
	defer server.Close()

	api := &tgbotapi.BotAPI{
		Token:  "scan-test-token",
		Client: server.Client(),
	}
	api.SetAPIEndpoint(server.URL + "/bot%s/%s")

	app := &App{
		db:                  database,
		api:                 api,
		groupUserScanOffset: map[int64]int{},
	}
	app.scanGroupUserProfiles(context.Background())

	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群配置失败: %v", err)
	}
	if group.BookkeepingEnabled != 0 {
		t.Fatalf("巡检命中失效群后应自动停用: %+v", group)
	}
	if strings.TrimSpace(group.BookkeepingCycleKey) != "" {
		t.Fatalf("巡检命中失效群后应清空日切状态: %+v", group)
	}
}
