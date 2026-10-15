package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/db"
)

type privateGroupControlTestButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

type privateGroupControlTestMarkup struct {
	InlineKeyboard [][]privateGroupControlTestButton `json:"inline_keyboard"`
}

const privateGroupControlClosingReminderEntryLabel = "下课提醒管理"
const privateGroupControlExpectedMemberCheckConcurrency = 8

func TestPrivateGroupControlPanelEntriesOpenOperablePanel(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-entries.db")
	ownerID := int64(880001)
	setupPrivateGroupControlGroups(t, database, ownerID, []int64{-100880001})
	app.isGroupMemberFn = privateGroupControlBotMemberFn(app, nil)

	baseDate := int(time.Now().Unix())
	entries := []struct {
		name   string
		update tgbotapi.Update
	}{
		{name: "command panel", update: fourLayerPrivateCommandUpdate(880101, 880101, ownerID, "owner_private", "/panel", baseDate)},
		{name: "text group panel", update: fourLayerPrivateTextUpdate(880102, 880102, ownerID, "owner_private", "群控面板", baseDate+1)},
		{name: "text control panel", update: fourLayerPrivateTextUpdate(880103, 880103, ownerID, "owner_private", "控制面板", baseDate+2)},
	}
	for _, tc := range entries {
		t.Run(tc.name, func(t *testing.T) {
			app.dispatchUpdate(context.Background(), tc.update)
			msg := privateGroupControlLatestMessageContaining(t, capture, ownerID, "私聊群控面板")
			privateGroupControlRequireButton(t, msg, "批量设置群标签")
			privateGroupControlRequireButton(t, msg, "批量设置群类型")
			privateGroupControlRequireExactButton(t, msg, privateGroupControlClosingReminderEntryLabel)
			privateGroupControlRequireNoButton(t, msg, "批量设置下课提醒")
			privateGroupControlRequireButton(t, msg, "选择群群发")
			privateGroupControlRequireButton(t, msg, "刷新群列表")
		})
	}

	callback := privateGroupControlCallback("private-panel-entry", privateCallbackPrefix+"panel", ownerID, ownerID, 990101)
	if err := app.handleCallbackQuery(context.Background(), callback); err != nil {
		t.Fatalf("私聊菜单群控入口回调失败: %v", err)
	}
	msg := privateGroupControlLatestMessageContaining(t, capture, ownerID, "私聊群控面板")
	privateGroupControlRequireButton(t, msg, "批量设置群标签")
	privateGroupControlRequireExactButton(t, msg, privateGroupControlClosingReminderEntryLabel)
	privateGroupControlRequireNoButton(t, msg, "批量设置下课提醒")
}

func TestPrivateGroupControlSelectionShowsLayeredGroupMetadata(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-selection-metadata.db")
	ownerID := int64(882001)
	groupIDs := setupPrivateGroupControlGroups(t, database, ownerID, privateGroupControlChatIDs(-100882001, 3))
	app.isGroupMemberFn = privateGroupControlBotMemberFn(app, nil)

	fieldsByGroup := map[int64]map[string]any{
		groupIDs[0]: {"group_type": "泰国", "group_tags": "VIP,泰国"},
		groupIDs[1]: {"group_type": "", "group_tags": ""},
		groupIDs[2]: {"group_type": "新加坡", "group_tags": "VIP"},
	}
	for chatID, fields := range fieldsByGroup {
		if err := database.UpdateGroupFields(chatID, fields); err != nil {
			t.Fatalf("设置群字段失败(chat=%d): %v", chatID, err)
		}
	}

	main := privateGroupControlOpenPanel(t, app, capture, ownerID, "owner_private", 882101, int(time.Now().Unix()))
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
		"selection-metadata-open",
		privateGroupControlButtonData(t, main, "批量设置群类型"),
		ownerID,
		ownerID,
		992101,
	)); err != nil {
		t.Fatalf("打开分层信息选群页失败: %v", err)
	}

	page := privateGroupControlLatestMessageContaining(t, capture, ownerID, "选择群类型目标")
	for _, want := range []string{
		"☐ 私聊群控-01",
		"现有类型：泰国",
		"现有标签：VIP、泰国",
		"☐ 私聊群控-02",
		"现有类型：未设置",
		"现有标签：未设置",
		"☐ 私聊群控-03",
		"现有类型：新加坡",
		"现有标签：VIP",
	} {
		if !strings.Contains(page.text, want) {
			t.Fatalf("选群页缺少分层信息 %q: %q", want, page.text)
		}
	}
	privateGroupControlRequireExactButton(t, page, "☐ 私聊群控-01")
	privateGroupControlRequireExactButton(t, page, "☐ 私聊群控-02")
	privateGroupControlRequireExactButton(t, page, "☐ 私聊群控-03")
	privateGroupControlRequireNoButton(t, page, "现有类型：")
	privateGroupControlRequireNoButton(t, page, "现有标签：")

	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
		"selection-metadata-select",
		privateGroupControlButtonData(t, page, "私聊群控-02"),
		ownerID,
		ownerID,
		992101,
	)); err != nil {
		t.Fatalf("仍按群名按钮选择群失败: %v", err)
	}
	page = privateGroupControlLatestMessageContaining(t, capture, ownerID, "已选：1 个群")
	privateGroupControlRequireExactButton(t, page, "☑ 私聊群控-02")
}

func TestPrivateGroupControlPanelOpensTypeTagListManagement(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-category-entry.db")
	ownerID := int64(882101)
	groupIDs := setupPrivateGroupControlGroups(t, database, ownerID, privateGroupControlChatIDs(-100882101, 3))
	app.isGroupMemberFn = privateGroupControlBotMemberFn(app, nil)
	for _, fields := range []struct {
		chatID int64
		values map[string]any
	}{
		{groupIDs[0], map[string]any{"group_type": "泰国", "group_tags": "VIP,泰国"}},
		{groupIDs[1], map[string]any{"group_type": "泰国", "group_tags": "泰国"}},
		{groupIDs[2], map[string]any{"group_type": "新加坡", "group_tags": "VIP"}},
	} {
		if err := database.UpdateGroupFields(fields.chatID, fields.values); err != nil {
			t.Fatalf("设置群字段失败(chat=%d): %v", fields.chatID, err)
		}
	}

	main := privateGroupControlOpenPanel(t, app, capture, ownerID, "owner_private", 882201, int(time.Now().Unix()))
	privateGroupControlRequireButton(t, main, "类型/标签列表管理")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
		"category-entry",
		privateGroupControlButtonData(t, main, "类型/标签列表管理"),
		ownerID,
		ownerID,
		992201,
	)); err != nil {
		t.Fatalf("打开类型/标签列表管理失败: %v", err)
	}

	menu := privateGroupControlLatestMessageContaining(t, capture, ownerID, "类型/标签列表管理")
	privateGroupControlRequireExactButton(t, menu, "类型列表")
	privateGroupControlRequireExactButton(t, menu, "标签列表")

	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
		"type-list",
		privateGroupControlExactButtonData(t, menu, "类型列表"),
		ownerID,
		ownerID,
		992201,
	)); err != nil {
		t.Fatalf("打开类型列表失败: %v", err)
	}
	typeList := privateGroupControlLatestMessageContaining(t, capture, ownerID, "类型列表")
	if !strings.Contains(typeList.text, "泰国：2 个群") {
		t.Fatalf("类型列表应显示类型及群数: %q", typeList.text)
	}
	privateGroupControlRequireButton(t, typeList, "泰国")
}

func TestPrivateGroupControlTypeDetailUsesRenderedListSnapshot(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-category-index-snapshot.db")
	ownerID := int64(882151)
	groupIDs := setupPrivateGroupControlGroups(t, database, ownerID, privateGroupControlChatIDs(-100882151, 2))
	app.isGroupMemberFn = privateGroupControlBotMemberFn(app, nil)
	if err := database.UpdateGroupFields(groupIDs[0], map[string]any{"group_type": "泰国"}); err != nil {
		t.Fatalf("设置泰国类型失败: %v", err)
	}
	if err := database.UpdateGroupFields(groupIDs[1], map[string]any{"group_type": "越南"}); err != nil {
		t.Fatalf("设置越南类型失败: %v", err)
	}

	main := privateGroupControlOpenPanel(t, app, capture, ownerID, "owner_private", 882251, int(time.Now().Unix()))
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
		"category-index-entry",
		privateGroupControlButtonData(t, main, "类型/标签列表管理"),
		ownerID,
		ownerID,
		992251,
	)); err != nil {
		t.Fatalf("打开类型/标签列表管理失败: %v", err)
	}
	menu := privateGroupControlLatestMessageContaining(t, capture, ownerID, "类型/标签列表管理")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
		"category-index-type-list",
		privateGroupControlExactButtonData(t, menu, "类型列表"),
		ownerID,
		ownerID,
		992251,
	)); err != nil {
		t.Fatalf("打开类型列表失败: %v", err)
	}
	typeList := privateGroupControlLatestMessageContaining(t, capture, ownerID, "类型列表")
	thailandData := privateGroupControlButtonData(t, typeList, "泰国")

	if err := database.UpdateGroupFields(groupIDs[1], map[string]any{"group_type": "A前置类型"}); err != nil {
		t.Fatalf("模拟列表渲染后类型变化失败: %v", err)
	}
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
		"category-index-type-detail",
		thailandData,
		ownerID,
		ownerID,
		992251,
	)); err != nil {
		t.Fatalf("打开类型详情失败: %v", err)
	}

	detail := privateGroupControlLatestMessageContaining(t, capture, ownerID, "类型：")
	if !strings.Contains(detail.text, "类型：泰国") {
		t.Fatalf("类型详情应使用列表渲染时的按钮快照，而不是刷新后漂移到其它类型: %q", detail.text)
	}
	if strings.Contains(detail.text, "类型：A前置类型") {
		t.Fatalf("旧按钮不应打开刷新后同 index 的其它类型: %q", detail.text)
	}
}

func TestPrivateGroupControlCategoryCallbacksStayShortForLongValues(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-category-callback-length.db")
	ownerID := int64(882161)
	groupIDs := setupPrivateGroupControlGroups(t, database, ownerID, privateGroupControlChatIDs(-100882161, 2))
	app.isGroupMemberFn = privateGroupControlBotMemberFn(app, nil)
	longType := "泰国超长类型:一级:二级:三级:四级:五级:六级:七级"
	longTag := "泰国超长标签:一级:二级:三级:四级:五级:六级:七级"
	if err := database.UpdateGroupFields(groupIDs[0], map[string]any{"group_type": longType, "group_tags": longTag}); err != nil {
		t.Fatalf("设置长类型标签失败: %v", err)
	}
	if err := database.UpdateGroupFields(groupIDs[1], map[string]any{"group_type": "普通类型", "group_tags": "普通标签"}); err != nil {
		t.Fatalf("设置普通类型标签失败: %v", err)
	}

	main := privateGroupControlOpenPanel(t, app, capture, ownerID, "owner_private", 882261, int(time.Now().Unix()))
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
		"category-long-entry",
		privateGroupControlButtonData(t, main, "类型/标签列表管理"),
		ownerID,
		ownerID,
		992261,
	)); err != nil {
		t.Fatalf("打开类型/标签列表管理失败: %v", err)
	}
	menu := privateGroupControlLatestMessageContaining(t, capture, ownerID, "类型/标签列表管理")
	privateGroupControlAssertCallbacksValid(t, menu)
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
		"category-long-type-list",
		privateGroupControlExactButtonData(t, menu, "类型列表"),
		ownerID,
		ownerID,
		992261,
	)); err != nil {
		t.Fatalf("打开类型列表失败: %v", err)
	}
	typeList := privateGroupControlLatestMessageContaining(t, capture, ownerID, "类型列表")
	privateGroupControlAssertCallbacksValid(t, typeList)
	if strings.Contains(typeList.replyMarkup, longType) {
		t.Fatalf("长类型原文不应进入 callback/reply_markup: %s", typeList.replyMarkup)
	}

	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
		"category-long-back-menu",
		privateGroupControlButtonData(t, typeList, "返回管理"),
		ownerID,
		ownerID,
		992261,
	)); err != nil {
		t.Fatalf("返回管理失败: %v", err)
	}
	menu = privateGroupControlLatestMessageContaining(t, capture, ownerID, "类型/标签列表管理")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
		"category-long-tag-list",
		privateGroupControlExactButtonData(t, menu, "标签列表"),
		ownerID,
		ownerID,
		992261,
	)); err != nil {
		t.Fatalf("打开标签列表失败: %v", err)
	}
	tagList := privateGroupControlLatestMessageContaining(t, capture, ownerID, "标签列表")
	privateGroupControlAssertCallbacksValid(t, tagList)
	if strings.Contains(tagList.replyMarkup, longTag) {
		t.Fatalf("长标签原文不应进入 callback/reply_markup: %s", tagList.replyMarkup)
	}
}

func TestPrivateGroupControlTypeDetailsShowGroupsAndMembershipActions(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-type-detail.db")
	ownerID := int64(882201)
	groupIDs := setupPrivateGroupControlGroups(t, database, ownerID, privateGroupControlChatIDs(-100882201, 3))
	app.isGroupMemberFn = privateGroupControlBotMemberFn(app, nil)
	for _, fields := range []struct {
		chatID int64
		values map[string]any
	}{
		{groupIDs[0], map[string]any{"group_type": "泰国", "group_tags": "VIP,泰国"}},
		{groupIDs[1], map[string]any{"group_type": "泰国", "group_tags": ""}},
		{groupIDs[2], map[string]any{"group_type": "", "group_tags": "VIP"}},
	} {
		if err := database.UpdateGroupFields(fields.chatID, fields.values); err != nil {
			t.Fatalf("设置群字段失败(chat=%d): %v", fields.chatID, err)
		}
	}

	detail := privateGroupControlOpenTypeDetail(t, app, capture, ownerID, 882301, 992301, "泰国")
	for _, want := range []string{
		"类型：泰国",
		"群数：2",
		"私聊群控-01",
		"现有类型：泰国",
		"现有标签：VIP、泰国",
		"私聊群控-02",
		"现有标签：未设置",
	} {
		if !strings.Contains(detail.text, want) {
			t.Fatalf("类型详情缺少 %q: %q", want, detail.text)
		}
	}
	privateGroupControlRequireExactButton(t, detail, "添加群到泰国")
	privateGroupControlRequireExactButton(t, detail, "从泰国移出群")
}

func TestPrivateGroupControlTypeManagementAddsSelectedGroup(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-type-add.db")
	ownerID := int64(882301)
	groupIDs := setupPrivateGroupControlGroups(t, database, ownerID, privateGroupControlChatIDs(-100882301, 3))
	app.isGroupMemberFn = privateGroupControlBotMemberFn(app, nil)
	if err := database.UpdateGroupFields(groupIDs[0], map[string]any{"group_type": "泰国"}); err != nil {
		t.Fatalf("设置已有类型失败: %v", err)
	}
	if err := database.UpdateGroupFields(groupIDs[1], map[string]any{"group_type": "泰国"}); err != nil {
		t.Fatalf("设置已有类型失败: %v", err)
	}

	detail := privateGroupControlOpenTypeDetail(t, app, capture, ownerID, 882401, 992401, "泰国")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
		"type-add-open",
		privateGroupControlExactButtonData(t, detail, "添加群到泰国"),
		ownerID,
		ownerID,
		992401,
	)); err != nil {
		t.Fatalf("打开添加群到类型选择页失败: %v", err)
	}

	page := privateGroupControlLatestMessageContaining(t, capture, ownerID, "添加群到泰国")
	privateGroupControlRequireButton(t, page, "私聊群控-03")
	privateGroupControlRequireNoButton(t, page, "私聊群控-01")
	privateGroupControlRequireNoButton(t, page, "私聊群控-02")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
		"type-add-select",
		privateGroupControlButtonData(t, page, "私聊群控-03"),
		ownerID,
		ownerID,
		992401,
	)); err != nil {
		t.Fatalf("选择待添加类型的群失败: %v", err)
	}
	page = privateGroupControlLatestMessageContaining(t, capture, ownerID, "已选：1 个群")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
		"type-add-confirm",
		privateGroupControlButtonData(t, page, "确认添加"),
		ownerID,
		ownerID,
		992401,
	)); err != nil {
		t.Fatalf("确认添加群到类型失败: %v", err)
	}

	privateGroupControlAssertGroupType(t, database, groupIDs[2], "泰国")
	privateGroupControlAssertGroupType(t, database, groupIDs[0], "泰国")
}

func TestPrivateGroupControlTypeManagementRemovesSelectedGroup(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-type-remove.db")
	ownerID := int64(882401)
	groupIDs := setupPrivateGroupControlGroups(t, database, ownerID, privateGroupControlChatIDs(-100882401, 3))
	app.isGroupMemberFn = privateGroupControlBotMemberFn(app, nil)
	if err := database.UpdateGroupFields(groupIDs[0], map[string]any{"group_type": "泰国"}); err != nil {
		t.Fatalf("设置已有类型失败: %v", err)
	}
	if err := database.UpdateGroupFields(groupIDs[1], map[string]any{"group_type": "泰国"}); err != nil {
		t.Fatalf("设置已有类型失败: %v", err)
	}

	detail := privateGroupControlOpenTypeDetail(t, app, capture, ownerID, 882501, 992501, "泰国")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
		"type-remove-open",
		privateGroupControlExactButtonData(t, detail, "从泰国移出群"),
		ownerID,
		ownerID,
		992501,
	)); err != nil {
		t.Fatalf("打开从类型移出群选择页失败: %v", err)
	}

	page := privateGroupControlLatestMessageContaining(t, capture, ownerID, "从泰国移出群")
	privateGroupControlRequireButton(t, page, "私聊群控-01")
	privateGroupControlRequireButton(t, page, "私聊群控-02")
	privateGroupControlRequireNoButton(t, page, "私聊群控-03")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
		"type-remove-select",
		privateGroupControlButtonData(t, page, "私聊群控-02"),
		ownerID,
		ownerID,
		992501,
	)); err != nil {
		t.Fatalf("选择待移出类型的群失败: %v", err)
	}
	page = privateGroupControlLatestMessageContaining(t, capture, ownerID, "已选：1 个群")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
		"type-remove-confirm",
		privateGroupControlButtonData(t, page, "确认移出"),
		ownerID,
		ownerID,
		992501,
	)); err != nil {
		t.Fatalf("确认从类型移出群失败: %v", err)
	}

	privateGroupControlAssertGroupType(t, database, groupIDs[1], "")
	privateGroupControlAssertGroupType(t, database, groupIDs[0], "泰国")
}

func TestPrivateGroupControlTypeRemovalDoesNotClearChangedType(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-type-remove-changed.db")
	ownerID := int64(882451)
	groupIDs := setupPrivateGroupControlGroups(t, database, ownerID, privateGroupControlChatIDs(-100882451, 2))
	app.isGroupMemberFn = privateGroupControlBotMemberFn(app, nil)
	if err := database.UpdateGroupFields(groupIDs[0], map[string]any{"group_type": "泰国"}); err != nil {
		t.Fatalf("设置已有类型失败: %v", err)
	}
	if err := database.UpdateGroupFields(groupIDs[1], map[string]any{"group_type": "泰国"}); err != nil {
		t.Fatalf("设置已有类型失败: %v", err)
	}

	detail := privateGroupControlOpenTypeDetail(t, app, capture, ownerID, 882551, 992551, "泰国")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
		"type-remove-changed-open",
		privateGroupControlExactButtonData(t, detail, "从泰国移出群"),
		ownerID,
		ownerID,
		992551,
	)); err != nil {
		t.Fatalf("打开从类型移出群选择页失败: %v", err)
	}
	page := privateGroupControlLatestMessageContaining(t, capture, ownerID, "从泰国移出群")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
		"type-remove-changed-select",
		privateGroupControlButtonData(t, page, "私聊群控-01"),
		ownerID,
		ownerID,
		992551,
	)); err != nil {
		t.Fatalf("选择待移出类型的群失败: %v", err)
	}
	page = privateGroupControlLatestMessageContaining(t, capture, ownerID, "已选：1 个群")
	if err := database.UpdateGroupFields(groupIDs[0], map[string]any{"group_type": "A前置类型"}); err != nil {
		t.Fatalf("模拟外部变更群类型失败: %v", err)
	}
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
		"type-remove-changed-confirm",
		privateGroupControlButtonData(t, page, "确认移出"),
		ownerID,
		ownerID,
		992551,
	)); err != nil {
		t.Fatalf("确认从类型移出群失败: %v", err)
	}

	privateGroupControlAssertGroupType(t, database, groupIDs[0], "A前置类型")
	privateGroupControlAssertGroupType(t, database, groupIDs[1], "泰国")
}

func TestPrivateGroupControlTagDetailsShowGroupsAndMembershipActions(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-tag-detail.db")
	ownerID := int64(882501)
	groupIDs := setupPrivateGroupControlGroups(t, database, ownerID, privateGroupControlChatIDs(-100882501, 3))
	app.isGroupMemberFn = privateGroupControlBotMemberFn(app, nil)
	for _, fields := range []struct {
		chatID int64
		values map[string]any
	}{
		{groupIDs[0], map[string]any{"group_tags": "VIP,泰国"}},
		{groupIDs[1], map[string]any{"group_tags": "泰国"}},
		{groupIDs[2], map[string]any{"group_tags": "VIP"}},
	} {
		if err := database.UpdateGroupFields(fields.chatID, fields.values); err != nil {
			t.Fatalf("设置群字段失败(chat=%d): %v", fields.chatID, err)
		}
	}

	detail := privateGroupControlOpenTagDetail(t, app, capture, ownerID, 882601, 992601, "泰国")
	for _, want := range []string{
		"标签：泰国",
		"群数：2",
		"私聊群控-01",
		"现有标签：VIP、泰国",
		"私聊群控-02",
	} {
		if !strings.Contains(detail.text, want) {
			t.Fatalf("标签详情缺少 %q: %q", want, detail.text)
		}
	}
	privateGroupControlRequireExactButton(t, detail, "添加群到泰国")
	privateGroupControlRequireExactButton(t, detail, "从泰国移出群")
}

func TestPrivateGroupControlTagManagementAddsAndDeduplicatesSelectedGroup(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-tag-add.db")
	ownerID := int64(882601)
	groupIDs := setupPrivateGroupControlGroups(t, database, ownerID, privateGroupControlChatIDs(-100882601, 3))
	app.isGroupMemberFn = privateGroupControlBotMemberFn(app, nil)
	if err := database.UpdateGroupFields(groupIDs[0], map[string]any{"group_tags": "泰国,VIP"}); err != nil {
		t.Fatalf("设置已有标签失败: %v", err)
	}
	if err := database.UpdateGroupFields(groupIDs[1], map[string]any{"group_tags": "泰国"}); err != nil {
		t.Fatalf("设置已有标签失败: %v", err)
	}
	if err := database.UpdateGroupFields(groupIDs[2], map[string]any{"group_tags": "VIP,VIP"}); err != nil {
		t.Fatalf("设置待添加群标签失败: %v", err)
	}

	detail := privateGroupControlOpenTagDetail(t, app, capture, ownerID, 882701, 992701, "泰国")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
		"tag-add-open",
		privateGroupControlExactButtonData(t, detail, "添加群到泰国"),
		ownerID,
		ownerID,
		992701,
	)); err != nil {
		t.Fatalf("打开添加群到标签选择页失败: %v", err)
	}

	page := privateGroupControlLatestMessageContaining(t, capture, ownerID, "添加群到泰国")
	privateGroupControlRequireButton(t, page, "私聊群控-03")
	privateGroupControlRequireNoButton(t, page, "私聊群控-01")
	privateGroupControlRequireNoButton(t, page, "私聊群控-02")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
		"tag-add-select",
		privateGroupControlButtonData(t, page, "私聊群控-03"),
		ownerID,
		ownerID,
		992701,
	)); err != nil {
		t.Fatalf("选择待添加标签的群失败: %v", err)
	}
	page = privateGroupControlLatestMessageContaining(t, capture, ownerID, "已选：1 个群")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
		"tag-add-confirm",
		privateGroupControlButtonData(t, page, "确认添加"),
		ownerID,
		ownerID,
		992701,
	)); err != nil {
		t.Fatalf("确认添加群到标签失败: %v", err)
	}

	privateGroupControlAssertGroupTags(t, database, groupIDs[2], "VIP,泰国")
}

func TestPrivateGroupControlTagManagementRemovesOnlySelectedTag(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-tag-remove.db")
	ownerID := int64(882701)
	groupIDs := setupPrivateGroupControlGroups(t, database, ownerID, privateGroupControlChatIDs(-100882701, 3))
	app.isGroupMemberFn = privateGroupControlBotMemberFn(app, nil)
	if err := database.UpdateGroupFields(groupIDs[0], map[string]any{"group_tags": "VIP,泰国,CS2"}); err != nil {
		t.Fatalf("设置待移出群标签失败: %v", err)
	}
	if err := database.UpdateGroupFields(groupIDs[1], map[string]any{"group_tags": "泰国"}); err != nil {
		t.Fatalf("设置已有标签失败: %v", err)
	}
	if err := database.UpdateGroupFields(groupIDs[2], map[string]any{"group_tags": "VIP"}); err != nil {
		t.Fatalf("设置其它群标签失败: %v", err)
	}

	detail := privateGroupControlOpenTagDetail(t, app, capture, ownerID, 882801, 992801, "泰国")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
		"tag-remove-open",
		privateGroupControlExactButtonData(t, detail, "从泰国移出群"),
		ownerID,
		ownerID,
		992801,
	)); err != nil {
		t.Fatalf("打开从标签移出群选择页失败: %v", err)
	}

	page := privateGroupControlLatestMessageContaining(t, capture, ownerID, "从泰国移出群")
	privateGroupControlRequireButton(t, page, "私聊群控-01")
	privateGroupControlRequireButton(t, page, "私聊群控-02")
	privateGroupControlRequireNoButton(t, page, "私聊群控-03")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
		"tag-remove-select",
		privateGroupControlButtonData(t, page, "私聊群控-01"),
		ownerID,
		ownerID,
		992801,
	)); err != nil {
		t.Fatalf("选择待移出标签的群失败: %v", err)
	}
	page = privateGroupControlLatestMessageContaining(t, capture, ownerID, "已选：1 个群")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
		"tag-remove-confirm",
		privateGroupControlButtonData(t, page, "确认移出"),
		ownerID,
		ownerID,
		992801,
	)); err != nil {
		t.Fatalf("确认从标签移出群失败: %v", err)
	}

	privateGroupControlAssertGroupTags(t, database, groupIDs[0], "VIP,CS2")
	privateGroupControlAssertGroupTags(t, database, groupIDs[1], "泰国")
}

func TestPrivateGroupControlPanelListsOnlyEligibleGroups(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-eligible.db")
	ownerID := int64(880101)
	eligibleID := int64(-100880102)
	botLeftID := int64(-100880101)
	outsiderID := int64(-100880103)
	setupPrivateGroupControlGroups(t, database, ownerID, []int64{eligibleID, botLeftID})
	if _, err := database.EnsureGroup(outsiderID, "私聊群控-无权限"); err != nil {
		t.Fatalf("初始化无权限群失败: %v", err)
	}
	app.isGroupMemberFn = privateGroupControlBotMemberFn(app, map[int64]bool{botLeftID: true})

	baseDate := int(time.Now().Unix())
	main := privateGroupControlOpenPanel(t, app, capture, ownerID, "owner_private", 880201, baseDate)
	actionData := privateGroupControlButtonData(t, main, "批量设置群标签")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("eligible-action", actionData, ownerID, ownerID, 990201)); err != nil {
		t.Fatalf("打开私聊批量标签选群失败: %v", err)
	}

	page := privateGroupControlLatestMessageContaining(t, capture, ownerID, "选择群标签目标")
	if !strings.Contains(page.text, "私聊群控-01") {
		t.Fatalf("选群页应展示有权限且机器人在群内的群: %q", page.text)
	}
	if strings.Contains(page.text, "私聊群控-02") || strings.Contains(page.text, "私聊群控-无权限") {
		t.Fatalf("选群页不应展示机器人已退出或无权限群: %q", page.text)
	}
}

func TestPrivateGroupControlActionAnswersCallbackBeforeEditingSelectionPanel(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-callback-order.db")
	ownerID := int64(880105)
	setupPrivateGroupControlGroups(t, database, ownerID, privateGroupControlChatIDs(-100880105, 2))
	app.isGroupMemberFn = privateGroupControlBotMemberFn(app, nil)

	main := privateGroupControlOpenPanel(t, app, capture, ownerID, "owner_private", 880205, int(time.Now().Unix()))
	queryID := "selection-order"
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
		queryID,
		privateGroupControlButtonData(t, main, "批量设置群标签"),
		ownerID,
		ownerID,
		990205,
	)); err != nil {
		t.Fatalf("打开私聊批量标签选群失败: %v", err)
	}

	answerIndex := -1
	editIndex := -1
	events := capture.eventSnapshot()
	for i, event := range events {
		if event.kind == "answer" && event.queryID == queryID && strings.Contains(event.text, "正在读取群列表") && answerIndex < 0 {
			answerIndex = i
		}
		if event.kind == "message" && event.source == "edit_message_text" && event.replyTo == 990205 && strings.Contains(event.text, "选择群标签目标") && editIndex < 0 {
			editIndex = i
		}
	}
	if answerIndex < 0 {
		t.Fatalf("缺少 callback 回执事件: %+v", events)
	}
	if editIndex < 0 {
		t.Fatalf("缺少编辑选群面板事件: %+v", events)
	}
	if answerIndex > editIndex {
		t.Fatalf("callback 应先回执再编辑面板: answer_index=%d edit_index=%d events=%+v", answerIndex, editIndex, events)
	}
}

func TestPrivateGroupControlActionAcknowledgesBeforeSlowMembershipChecks(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-callback-early-ack.db")
	ownerID := int64(880107)
	setupPrivateGroupControlGroups(t, database, ownerID, privateGroupControlChatIDs(-100880107, 2))

	main := privateGroupControlOpenPanel(t, app, capture, ownerID, "owner_private", 880207, int(time.Now().Unix()))
	actionData := privateGroupControlButtonData(t, main, "批量设置群标签")
	memberStarted := make(chan struct{}, 1)
	releaseMembershipChecks := make(chan struct{})
	app.isGroupMemberFn = func(chatID, userID int64) bool {
		select {
		case memberStarted <- struct{}{}:
		default:
		}
		<-releaseMembershipChecks
		return true
	}

	queryID := "early-ack"
	done := make(chan error, 1)
	go func() {
		done <- app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
			queryID,
			actionData,
			ownerID,
			ownerID,
			990207,
		))
	}()

	select {
	case <-memberStarted:
	case <-time.After(2 * time.Second):
		close(releaseMembershipChecks)
		<-done
		t.Fatal("群成员检查未启动，无法验证慢路径回执")
	}

	answered := false
	for _, answer := range capture.answerSnapshot() {
		if answer.queryID == queryID && strings.Contains(answer.text, "读取群列表") {
			answered = true
			break
		}
	}
	close(releaseMembershipChecks)
	if err := <-done; err != nil {
		t.Fatalf("慢速群成员检查回调失败: %v", err)
	}
	if !answered {
		t.Fatalf("群成员检查阻塞时应先发送 callback 回执: %+v", capture.answerSnapshot())
	}
}

func TestPrivateGroupControlEligibleGroupsUseBoundedMembershipConcurrency(t *testing.T) {
	app, database, _ := newFourLayerTestApp(t, "private-group-control-membership-concurrency.db")
	ownerID := int64(880108)
	groupIDs := setupPrivateGroupControlGroups(t, database, ownerID, privateGroupControlChatIDs(
		-100880108,
		privateGroupControlExpectedMemberCheckConcurrency+2,
	))

	memberStarted := make(chan struct{}, len(groupIDs))
	releaseMembershipChecks := make(chan struct{})
	var active int64
	var maxActive int64
	app.isGroupMemberFn = func(chatID, userID int64) bool {
		current := atomic.AddInt64(&active, 1)
		for {
			previous := atomic.LoadInt64(&maxActive)
			if current <= previous || atomic.CompareAndSwapInt64(&maxActive, previous, current) {
				break
			}
		}
		memberStarted <- struct{}{}
		<-releaseMembershipChecks
		atomic.AddInt64(&active, -1)
		return true
	}

	result := make(chan struct {
		groups []db.GroupConfig
		err    error
	}, 1)
	go func() {
		groups, err := app.listPrivateGroupControlEligibleGroups(ownerID)
		result <- struct {
			groups []db.GroupConfig
			err    error
		}{groups: groups, err: err}
	}()

	select {
	case <-memberStarted:
	case <-time.After(2 * time.Second):
		close(releaseMembershipChecks)
		<-result
		t.Fatal("群成员检查未启动")
	}
	select {
	case <-memberStarted:
	case <-time.After(2 * time.Second):
		close(releaseMembershipChecks)
		<-result
		t.Fatal("群成员检查仍为串行执行")
	}

	time.Sleep(100 * time.Millisecond)
	close(releaseMembershipChecks)
	out := <-result
	if out.err != nil {
		t.Fatalf("读取可管理群失败: %v", out.err)
	}
	if len(out.groups) != len(groupIDs) {
		t.Fatalf("可管理群数量异常: got=%d want=%d", len(out.groups), len(groupIDs))
	}
	if got := atomic.LoadInt64(&maxActive); got <= 1 {
		t.Fatalf("群成员检查应并发执行: max_active=%d", got)
	}
	if atomic.LoadInt64(&maxActive) > privateGroupControlExpectedMemberCheckConcurrency {
		t.Fatalf("群成员检查超过并发上限: max_active=%d limit=%d", atomic.LoadInt64(&maxActive), privateGroupControlExpectedMemberCheckConcurrency)
	}
}

func TestPrivateGroupControlPanelReusesBotMembershipChecksAcrossReopenWithinTTL(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-membership-cache-reopen.db")
	ownerID := int64(880106)
	groupIDs := setupPrivateGroupControlGroups(t, database, ownerID, privateGroupControlChatIDs(-100880106, 3))
	var memberChecks int64
	app.isGroupMemberFn = func(chatID, userID int64) bool {
		atomic.AddInt64(&memberChecks, 1)
		return privateGroupControlBotMemberFn(app, nil)(chatID, userID)
	}

	main := privateGroupControlOpenPanel(t, app, capture, ownerID, "owner_private", 880206, int(time.Now().Unix()))
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
		"membership-cache-open-1",
		privateGroupControlButtonData(t, main, "批量设置群标签"),
		ownerID,
		ownerID,
		990206,
	)); err != nil {
		t.Fatalf("首次打开私聊批量标签选群失败: %v", err)
	}
	if got, want := atomic.LoadInt64(&memberChecks), int64(len(groupIDs)); got != want {
		t.Fatalf("首次打开应只校验一次每个群的机器人在群状态: got=%d want=%d", got, want)
	}

	main = privateGroupControlOpenPanel(t, app, capture, ownerID, "owner_private", 880207, int(time.Now().Unix())+1)
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
		"membership-cache-open-2",
		privateGroupControlButtonData(t, main, "批量设置群标签"),
		ownerID,
		ownerID,
		990207,
	)); err != nil {
		t.Fatalf("二次打开私聊批量标签选群失败: %v", err)
	}
	if got, want := atomic.LoadInt64(&memberChecks), int64(len(groupIDs)); got != want {
		t.Fatalf("短时间内重复打开群列表不应重复逐群校验机器人成员关系: got=%d want=%d", got, want)
	}
}

func TestPrivateGroupControlPanelListsGroupsForGlobalOwner(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-global-owner.db")
	globalOwnerID := int64(880111)
	chatID := int64(-100880111)
	if _, err := database.EnsureGroup(chatID, "全局权限可管理群"); err != nil {
		t.Fatalf("初始化群失败: %v", err)
	}
	if err := database.AddGlobalOwner(globalOwnerID, "global_owner"); err != nil {
		t.Fatalf("设置全局权限人失败: %v", err)
	}
	app.isGroupMemberFn = privateGroupControlBotMemberFn(app, nil)

	main := privateGroupControlOpenPanel(t, app, capture, globalOwnerID, "global_owner", 880211, int(time.Now().Unix()))
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("global-owner-closing-time", privateGroupControlExactButtonData(t, main, privateGroupControlClosingReminderEntryLabel), globalOwnerID, globalOwnerID, 990211)); err != nil {
		t.Fatalf("全局权限人打开下课时间选群失败: %v", err)
	}

	page := privateGroupControlLatestMessageContaining(t, capture, globalOwnerID, "第1步：先选择目标群")
	if !strings.Contains(page.text, "全局权限可管理群") {
		t.Fatalf("全局权限人应看到可管理群: %q", page.text)
	}
	if strings.Contains(page.text, "未找到可管理群") {
		t.Fatalf("全局权限人不应被误判为无可管理群: %q", page.text)
	}
}

func TestPrivateGroupControlPanelListsRecentActiveGroupsFirst(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-recent-active.db")
	ownerID := int64(880151)
	groupIDs := setupPrivateGroupControlGroups(t, database, ownerID, privateGroupControlChatIDs(-100880151, 3))
	app.isGroupMemberFn = privateGroupControlBotMemberFn(app, nil)

	if _, _, err := database.UpsertGroupUser(groupIDs[0], 88015101, "active_old", "Active Old"); err != nil {
		t.Fatalf("初始化旧活跃群成员失败: %v", err)
	}
	if _, _, err := database.UpsertGroupUser(groupIDs[1], 88015102, "active_mid", "Active Mid"); err != nil {
		t.Fatalf("初始化中间活跃群成员失败: %v", err)
	}
	time.Sleep(1100 * time.Millisecond)
	if _, _, err := database.UpsertGroupUser(groupIDs[2], 88015103, "active_new", "Active New"); err != nil {
		t.Fatalf("初始化最新活跃群成员失败: %v", err)
	}

	main := privateGroupControlOpenPanel(t, app, capture, ownerID, "owner_private", 880251, int(time.Now().Unix()))
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("recent-active-action", privateGroupControlButtonData(t, main, "批量设置群标签"), ownerID, ownerID, 990251)); err != nil {
		t.Fatalf("打开私聊批量标签选群失败: %v", err)
	}

	page := privateGroupControlLatestMessageContaining(t, capture, ownerID, "选择群标签目标")
	got := privateGroupControlGroupButtonLabels(t, page)
	want := []string{"私聊群控-03", "私聊群控-01", "私聊群控-02"}
	if len(got) != len(want) {
		t.Fatalf("选群按钮数量异常: got=%v want=%v text=%q", got, want, page.text)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("最近活跃群应优先展示并保持稳定排序: got=%v want=%v text=%q", got, want, page.text)
		}
	}
}

func TestPrivateGroupControlPanelSelectionShowsTwentyGroupsPerPage(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-20-per-page.db")
	ownerID := int64(880181)
	setupPrivateGroupControlGroups(t, database, ownerID, privateGroupControlChatIDs(-100880181, 21))
	app.isGroupMemberFn = privateGroupControlBotMemberFn(app, nil)

	main := privateGroupControlOpenPanel(t, app, capture, ownerID, "owner_private", 880281, int(time.Now().Unix()))
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("twenty-page-action", privateGroupControlButtonData(t, main, "选择群群发"), ownerID, ownerID, 990281)); err != nil {
		t.Fatalf("打开群发选群失败: %v", err)
	}

	page := privateGroupControlLatestMessageContaining(t, capture, ownerID, "选择群发目标")
	if got := len(privateGroupControlGroupButtonLabels(t, page)); got != 20 {
		t.Errorf("第一页应展示 20 个可选群: got=%d text=%q", got, page.text)
	}
	if !strings.Contains(page.text, "页码：1/2") {
		t.Errorf("21 个群按每页 20 个应分为 2 页: text=%q", page.text)
	}

	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("twenty-page-next", privateGroupControlButtonData(t, page, "下一页"), ownerID, ownerID, 990281)); err != nil {
		t.Fatalf("翻到第二页失败: %v", err)
	}
	page = privateGroupControlLatestMessageContaining(t, capture, ownerID, "页码：2/")
	if got := len(privateGroupControlGroupButtonLabels(t, page)); got != 1 {
		t.Fatalf("第二页应只展示剩余 1 个可选群: got=%d text=%q", got, page.text)
	}
	if !strings.Contains(page.text, "页码：2/2") {
		t.Fatalf("第二页页码异常: text=%q", page.text)
	}
}

func TestPrivateGroupControlPanelSelectsGroupsByButtonsAcrossPages(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-select-pages.db")
	ownerID := int64(880201)
	groupIDs := setupPrivateGroupControlGroups(t, database, ownerID, privateGroupControlChatIDs(-100880201, 21))
	app.isGroupMemberFn = privateGroupControlBotMemberFn(app, nil)

	baseDate := int(time.Now().Unix())
	main := privateGroupControlOpenPanel(t, app, capture, ownerID, "owner_private", 880301, baseDate)
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("select-action", privateGroupControlButtonData(t, main, "选择群群发"), ownerID, ownerID, 990301)); err != nil {
		t.Fatalf("打开群发选群失败: %v", err)
	}

	page := privateGroupControlLatestMessageContaining(t, capture, ownerID, "选择群发目标")
	firstGroupData := privateGroupControlButtonData(t, page, "私聊群控-01")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("select-g1", firstGroupData, ownerID, ownerID, 990301)); err != nil {
		t.Fatalf("选择第一页群失败: %v", err)
	}
	page = privateGroupControlLatestMessageContaining(t, capture, ownerID, "已选：1 个群")

	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("select-page2", privateGroupControlButtonData(t, page, "下一页"), ownerID, ownerID, 990301)); err != nil {
		t.Fatalf("翻到第二页失败: %v", err)
	}
	page = privateGroupControlLatestMessageContaining(t, capture, ownerID, "页码：2/2")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("select-g21", privateGroupControlButtonData(t, page, "私聊群控-21"), ownerID, ownerID, 990301)); err != nil {
		t.Fatalf("选择第二页群失败: %v", err)
	}
	page = privateGroupControlLatestMessageContaining(t, capture, ownerID, "已选：2 个群")

	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("select-back-page1", privateGroupControlButtonData(t, page, "上一页"), ownerID, ownerID, 990301)); err != nil {
		t.Fatalf("返回第一页失败: %v", err)
	}
	page = privateGroupControlLatestMessageContaining(t, capture, ownerID, "页码：1/2")
	privateGroupControlRequireButton(t, page, "☑ 私聊群控-01")
	if !strings.Contains(page.text, "已选：2 个群") {
		t.Fatalf("跨页选择数量异常: text=%q groupIDs=%v", page.text, groupIDs)
	}
}

func TestPrivateGroupControlPanelSelectsAllGroupsAcrossPages(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-select-all-groups.db")
	ownerID := int64(880211)
	groupIDs := setupPrivateGroupControlGroups(t, database, ownerID, privateGroupControlChatIDs(-100880211, 21))
	app.isGroupMemberFn = privateGroupControlBotMemberFn(app, nil)

	main := privateGroupControlOpenPanel(t, app, capture, ownerID, "owner_private", 880311, int(time.Now().Unix()))
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("select-all-action", privateGroupControlButtonData(t, main, "批量设置群类型"), ownerID, ownerID, 990311)); err != nil {
		t.Fatalf("打开群类型选群失败: %v", err)
	}
	page := privateGroupControlLatestMessageContaining(t, capture, ownerID, "选择群类型目标")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("select-all-groups", privateGroupControlButtonData(t, page, "全选全部群"), ownerID, ownerID, 990311)); err != nil {
		t.Fatalf("全选全部群失败: %v", err)
	}
	page = privateGroupControlLatestMessageContaining(t, capture, ownerID, "已选：21 个群")
	privateGroupControlRequireButton(t, page, "☑ 私聊群控-01")

	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("select-all-page2", privateGroupControlButtonData(t, page, "下一页"), ownerID, ownerID, 990311)); err != nil {
		t.Fatalf("翻到第二页失败: %v", err)
	}
	page = privateGroupControlLatestMessageContaining(t, capture, ownerID, "页码：2/2")
	privateGroupControlRequireButton(t, page, "☑ 私聊群控-21")
	if !strings.Contains(page.text, "已选：21 个群") {
		t.Fatalf("全选全部群后跨页数量异常: text=%q", page.text)
	}
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("select-all-choose", privateGroupControlButtonData(t, page, "确认选择"), ownerID, ownerID, 990311)); err != nil {
		t.Fatalf("确认全选全部群失败: %v", err)
	}
	app.dispatchUpdate(context.Background(), fourLayerPrivateTextUpdate(880312, 880312, ownerID, "owner_private", "全部群类型", int(time.Now().Unix())))
	preview := privateGroupControlLatestMessageContaining(t, capture, ownerID, "目标类型：全部群类型")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("select-all-confirm", privateGroupControlButtonData(t, preview, "确认设置"), ownerID, ownerID, 990311)); err != nil {
		t.Fatalf("确认设置全部群类型失败: %v", err)
	}
	for _, chatID := range groupIDs {
		privateGroupControlAssertGroupType(t, database, chatID, "全部群类型")
	}
}

func TestPrivateGroupControlPanelSelectsGroupsByExistingTypeAndTag(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-select-by-existing.db")
	ownerID := int64(880221)
	groupIDs := setupPrivateGroupControlGroups(t, database, ownerID, privateGroupControlChatIDs(-100880221, 4))
	app.isGroupMemberFn = privateGroupControlBotMemberFn(app, nil)

	fieldsByGroup := map[int64]map[string]any{
		groupIDs[0]: {"group_type": "代收", "group_tags": "印度,VIP"},
		groupIDs[1]: {"group_type": "代收", "group_tags": "印度"},
		groupIDs[2]: {"group_type": "代付", "group_tags": "越南"},
		groupIDs[3]: {"group_type": "", "group_tags": "VIP"},
	}
	for chatID, fields := range fieldsByGroup {
		if err := database.UpdateGroupFields(chatID, fields); err != nil {
			t.Fatalf("设置群字段失败(chat=%d): %v", chatID, err)
		}
	}

	main := privateGroupControlOpenPanel(t, app, capture, ownerID, "owner_private", 880321, int(time.Now().Unix()))
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("select-existing-action", privateGroupControlButtonData(t, main, "批量设置群类型"), ownerID, ownerID, 990321)); err != nil {
		t.Fatalf("打开群类型选群失败: %v", err)
	}
	page := privateGroupControlLatestMessageContaining(t, capture, ownerID, "选择群类型目标")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("select-existing-type-menu", privateGroupControlButtonData(t, page, "按类型选择"), ownerID, ownerID, 990321)); err != nil {
		t.Fatalf("打开按类型选择失败: %v", err)
	}
	typePage := privateGroupControlLatestMessageContaining(t, capture, ownerID, "按已有群类型选择")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("select-existing-type", privateGroupControlButtonData(t, typePage, "代收 (2)"), ownerID, ownerID, 990321)); err != nil {
		t.Fatalf("按已有类型选择失败: %v", err)
	}
	page = privateGroupControlLatestMessageContaining(t, capture, ownerID, "已选：2 个群")
	privateGroupControlRequireButton(t, page, "☑ 私聊群控-01")
	privateGroupControlRequireButton(t, page, "☑ 私聊群控-02")

	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("select-existing-tag-menu", privateGroupControlButtonData(t, page, "按标签选择"), ownerID, ownerID, 990321)); err != nil {
		t.Fatalf("打开按标签选择失败: %v", err)
	}
	tagPage := privateGroupControlLatestMessageContaining(t, capture, ownerID, "按已有群标签选择")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("select-existing-tag", privateGroupControlButtonData(t, tagPage, "VIP (2)"), ownerID, ownerID, 990321)); err != nil {
		t.Fatalf("按已有标签选择失败: %v", err)
	}
	page = privateGroupControlLatestMessageContaining(t, capture, ownerID, "已选：3 个群")
	privateGroupControlRequireButton(t, page, "☑ 私聊群控-01")
	privateGroupControlRequireButton(t, page, "☑ 私聊群控-02")
	privateGroupControlRequireButton(t, page, "☑ 私聊群控-04")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("select-existing-choose", privateGroupControlButtonData(t, page, "确认选择"), ownerID, ownerID, 990321)); err != nil {
		t.Fatalf("确认按已有条件选择失败: %v", err)
	}
	app.dispatchUpdate(context.Background(), fourLayerPrivateTextUpdate(880322, 880322, ownerID, "owner_private", "筛选后类型", int(time.Now().Unix())))
	preview := privateGroupControlLatestMessageContaining(t, capture, ownerID, "目标类型：筛选后类型")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("select-existing-confirm", privateGroupControlButtonData(t, preview, "确认设置"), ownerID, ownerID, 990321)); err != nil {
		t.Fatalf("确认按已有条件设置类型失败: %v", err)
	}
	privateGroupControlAssertGroupType(t, database, groupIDs[0], "筛选后类型")
	privateGroupControlAssertGroupType(t, database, groupIDs[1], "筛选后类型")
	privateGroupControlAssertGroupType(t, database, groupIDs[2], "代付")
	privateGroupControlAssertGroupType(t, database, groupIDs[3], "筛选后类型")
}

func TestPrivateGroupControlSelectionFilterPaginatesManyExistingTags(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-filter-pagination.db")
	ownerID := int64(880231)
	groupIDs := setupPrivateGroupControlGroups(t, database, ownerID, privateGroupControlChatIDs(-100880231, 60))
	app.isGroupMemberFn = privateGroupControlBotMemberFn(app, nil)
	for i, chatID := range groupIDs {
		tag := fmt.Sprintf("tag-%02d", i+1)
		if err := database.UpdateGroupFields(chatID, map[string]any{"group_tags": tag}); err != nil {
			t.Fatalf("设置唯一标签失败(chat=%d): %v", chatID, err)
		}
	}

	main := privateGroupControlOpenPanel(t, app, capture, ownerID, "owner_private", 880331, int(time.Now().Unix()))
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("filter-page-action", privateGroupControlButtonData(t, main, "选择群群发"), ownerID, ownerID, 990331)); err != nil {
		t.Fatalf("打开群发选群失败: %v", err)
	}
	page := privateGroupControlLatestMessageContaining(t, capture, ownerID, "选择群发目标")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("filter-page-tag-menu", privateGroupControlButtonData(t, page, "按标签选择"), ownerID, ownerID, 990331)); err != nil {
		t.Fatalf("打开按标签选择失败: %v", err)
	}
	tagPage := privateGroupControlLatestMessageContaining(t, capture, ownerID, "按已有群标签选择")
	if !strings.Contains(tagPage.text, "条件页码：1/3") {
		t.Fatalf("大量标签应分页展示: text=%q", tagPage.text)
	}
	privateGroupControlRequireButton(t, tagPage, "tag-20 (1)")
	privateGroupControlRequireNoButton(t, tagPage, "tag-21 (1)")

	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("filter-page-next", privateGroupControlButtonData(t, tagPage, "下一页"), ownerID, ownerID, 990331)); err != nil {
		t.Fatalf("翻到第二页标签失败: %v", err)
	}
	tagPage = privateGroupControlLatestMessageContaining(t, capture, ownerID, "条件页码：2/3")
	privateGroupControlRequireButton(t, tagPage, "tag-21 (1)")
	privateGroupControlRequireNoButton(t, tagPage, "tag-20 (1)")
}

func TestPrivateGroupControlPanelRejectsStaleCallback(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-stale.db")
	ownerID := int64(880301)
	setupPrivateGroupControlGroups(t, database, ownerID, []int64{-100880301})
	app.isGroupMemberFn = privateGroupControlBotMemberFn(app, nil)

	main := privateGroupControlOpenPanel(t, app, capture, ownerID, "owner_private", 880401, int(time.Now().Unix()))
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("stale-action", privateGroupControlButtonData(t, main, "批量设置群标签"), ownerID, ownerID, 990401)); err != nil {
		t.Fatalf("打开选群失败: %v", err)
	}
	page := privateGroupControlLatestMessageContaining(t, capture, ownerID, "选择群标签目标")
	staleGroupData := privateGroupControlButtonData(t, page, "私聊群控-01")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("stale-select", staleGroupData, ownerID, ownerID, 990401)); err != nil {
		t.Fatalf("首次选择群失败: %v", err)
	}
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("stale-replay", staleGroupData, ownerID, ownerID, 990401)); err != nil {
		t.Fatalf("旧版本 callback 不应报错: %v", err)
	}
	if !fourLayerHasCallbackAnswer(capture.answerSnapshot(), "stale-replay", "面板已刷新") {
		t.Fatalf("旧版本 callback 应提示面板已刷新: %+v", capture.answerSnapshot())
	}
	page = privateGroupControlLatestMessageContaining(t, capture, ownerID, "已选：1 个群")
	privateGroupControlRequireButton(t, page, "☑ 私聊群控-01")
}

func TestPrivateGroupControlSelectionActionsAndSecurity(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-selection-actions.db")
	ownerID := int64(880401)
	otherUserID := int64(880499)
	setupPrivateGroupControlGroups(t, database, ownerID, privateGroupControlChatIDs(-100880401, 3))
	app.isGroupMemberFn = privateGroupControlBotMemberFn(app, nil)

	main := privateGroupControlOpenPanel(t, app, capture, ownerID, "owner_private", 880501, int(time.Now().Unix()))
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("selection-action", privateGroupControlButtonData(t, main, "批量设置群标签"), ownerID, ownerID, 990501)); err != nil {
		t.Fatalf("打开选群失败: %v", err)
	}
	page := privateGroupControlLatestMessageContaining(t, capture, ownerID, "选择群标签目标")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("selection-choose-empty", privateGroupControlButtonData(t, page, "确认选择"), ownerID, ownerID, 990501)); err != nil {
		t.Fatalf("未选群确认不应报错: %v", err)
	}
	if !fourLayerHasCallbackAnswer(capture.answerSnapshot(), "selection-choose-empty", "至少选择一个群") {
		t.Fatalf("未选群确认应提示至少选择一个群: %+v", capture.answerSnapshot())
	}

	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("selection-select-all", privateGroupControlButtonData(t, page, "全选本页"), ownerID, ownerID, 990501)); err != nil {
		t.Fatalf("全选本页失败: %v", err)
	}
	page = privateGroupControlLatestMessageContaining(t, capture, ownerID, "已选：3 个群")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("selection-clear-wrong-user", privateGroupControlButtonData(t, page, "清空选择"), otherUserID, ownerID, 990501)); err != nil {
		t.Fatalf("其他用户点击不应报错: %v", err)
	}
	if !fourLayerHasCallbackAnswer(capture.answerSnapshot(), "selection-clear-wrong-user", "无权") {
		t.Fatalf("其他用户点击应被拒绝: %+v", capture.answerSnapshot())
	}
	page = privateGroupControlLatestMessageContaining(t, capture, ownerID, "已选：3 个群")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("selection-clear", privateGroupControlButtonData(t, page, "清空选择"), ownerID, ownerID, 990501)); err != nil {
		t.Fatalf("清空选择失败: %v", err)
	}
	page = privateGroupControlLatestMessageContaining(t, capture, ownerID, "已选：0 个群")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("selection-invalid", "pc:bad", ownerID, ownerID, 990501)); err != nil {
		t.Fatalf("非法 callback 不应报错: %v", err)
	}
	if !fourLayerHasCallbackAnswer(capture.answerSnapshot(), "selection-invalid", "无效") {
		t.Fatalf("非法 callback 应提示无效: %+v", capture.answerSnapshot())
	}
}

func TestPrivateGroupControlBatchSetTagsByButtonSelection(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-tags.db")
	ownerID := int64(880501)
	groupIDs := setupPrivateGroupControlGroups(t, database, ownerID, privateGroupControlChatIDs(-100880501, 3))
	app.isGroupMemberFn = privateGroupControlBotMemberFn(app, nil)

	preview := privateGroupControlSelectGroupsAndInput(t, app, capture, ownerID, "批量设置群标签", []string{"私聊群控-01", "私聊群控-02"}, "cs2,印度")
	if !strings.Contains(preview.text, "目标标签：cs2、印度") {
		t.Fatalf("标签预览异常: %q", preview.text)
	}
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("tags-confirm", privateGroupControlButtonData(t, preview, "确认设置"), ownerID, ownerID, 990601)); err != nil {
		t.Fatalf("确认设置标签失败: %v", err)
	}

	privateGroupControlAssertGroupTags(t, database, groupIDs[0], "cs2,印度")
	privateGroupControlAssertGroupTags(t, database, groupIDs[1], "cs2,印度")
	privateGroupControlAssertGroupTags(t, database, groupIDs[2], "")
	if !fourLayerHasReply(capture.snapshot(), ownerID, 990601, "设置成功：2") {
		t.Fatalf("缺少标签设置成功回执: %+v", capture.snapshot())
	}
}

func TestPrivateGroupControlBatchSetTypesByButtonSelection(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-types.db")
	ownerID := int64(880601)
	groupIDs := setupPrivateGroupControlGroups(t, database, ownerID, privateGroupControlChatIDs(-100880601, 3))
	app.isGroupMemberFn = privateGroupControlBotMemberFn(app, nil)

	preview := privateGroupControlSelectGroupsAndInput(t, app, capture, ownerID, "批量设置群类型", []string{"私聊群控-01", "私聊群控-03"}, "代收")
	if !strings.Contains(preview.text, "目标类型：代收") {
		t.Fatalf("类型预览异常: %q", preview.text)
	}
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("types-confirm", privateGroupControlButtonData(t, preview, "确认设置"), ownerID, ownerID, 990701)); err != nil {
		t.Fatalf("确认设置类型失败: %v", err)
	}

	privateGroupControlAssertGroupType(t, database, groupIDs[0], "代收")
	privateGroupControlAssertGroupType(t, database, groupIDs[1], "")
	privateGroupControlAssertGroupType(t, database, groupIDs[2], "代收")
}

func TestPrivateGroupControlBatchSetClosingReminderTimeBySelectAll(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-closing-time.db")
	ownerID := int64(880651)
	groupIDs := setupPrivateGroupControlGroups(t, database, ownerID, privateGroupControlChatIDs(-100880651, 3))
	app.isGroupMemberFn = privateGroupControlBotMemberFn(app, nil)
	thresholds := map[int64]float64{
		groupIDs[0]: 1.5,
		groupIDs[1]: 2.5,
		groupIDs[2]: 2.5,
	}
	for i, chatID := range groupIDs {
		config := db.GroupClosingReminderConfig{
			ChatID:       chatID,
			Enabled:      1,
			ReminderTime: "21:30",
			ThresholdU:   thresholds[chatID],
			UpdatedBy:    ownerID,
		}
		if i == 0 {
			config.Enabled = 0
			config.ReminderTime = "04:00"
			config.TextNormal = "自定义正常文案"
		}
		if err := database.UpsertClosingReminderConfig(config); err != nil {
			t.Fatalf("初始化下课提醒配置失败(chat=%d): %v", chatID, err)
		}
	}

	main := privateGroupControlOpenPanel(t, app, capture, ownerID, "owner_private", 880651, int(time.Now().Unix()))
	if !strings.Contains(main.text, "仅机器人系统权限人/操作人可操作") {
		t.Fatalf("私聊群控面板应说明权限边界: %q", main.text)
	}
	if !strings.Contains(main.text, "机器人仍在群内") {
		t.Fatalf("私聊群控面板应说明机器人在群条件: %q", main.text)
	}
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("closing-time-action", privateGroupControlExactButtonData(t, main, privateGroupControlClosingReminderEntryLabel), ownerID, ownerID, 990651)); err != nil {
		t.Fatalf("打开下课提醒管理选群失败: %v", err)
	}

	page := privateGroupControlLatestMessageContaining(t, capture, ownerID, "第1步：先选择目标群")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("closing-time-select-all", privateGroupControlButtonData(t, page, "全选本页"), ownerID, ownerID, 990652)); err != nil {
		t.Fatalf("全选下课时间目标失败: %v", err)
	}
	page = privateGroupControlLatestMessageContaining(t, capture, ownerID, "已选：3 个群")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("closing-time-choose", privateGroupControlButtonData(t, page, "确认选择"), ownerID, ownerID, 990653)); err != nil {
		t.Fatalf("确认下课时间目标失败: %v", err)
	}
	if !fourLayerHasCallbackAnswer(capture.answerSnapshot(), "closing-time-choose", "请选择下课提醒设置项") {
		t.Fatalf("确认选择后应进入下课提醒设置层: %+v", capture.answerSnapshot())
	}
	menu := privateGroupControlLatestMessageContaining(t, capture, ownerID, "下课提醒设置")
	for _, want := range []string{
		"已选：3 个群",
		"第2步：选择要批量修改的内容",
		"权限说明",
		"全局/本群权限人、操作人可设置",
		"不要求 Telegram 群管理员",
		"机器人必须在目标群内",
		"当前配置",
		"提醒状态",
		"开启：",
		"关闭：",
		"下课时间",
		"U差额阈值",
		"文案状态",
		"正常：1 个群自定义，其余默认",
		"未列状态：全部默认",
		"状态规则",
		"正常：收支差额在阈值内",
		"待确认：入款大于下发，且超过阈值",
		"无账单：本账期无入款/下发",
		"复核：只有下发，或下发大于入款且超过阈值",
		"失败：统计或发送异常",
		"U阈值只影响“待确认/复核”的判断，不影响下课时间。",
	} {
		if !strings.Contains(menu.text, want) {
			t.Fatalf("下课提醒设置层缺少说明 %q: %q", want, menu.text)
		}
	}
	if strings.Count(menu.text, "--------------------------------") < 2 {
		t.Fatalf("下课提醒设置层应使用分割线分块: %q", menu.text)
	}
	for _, old := range []string{
		"权限：全局/本群权限人或操作人可设置",
		"当前摘要：",
		"状态说明：正常=",
		"个自定义 /",
	} {
		if strings.Contains(menu.text, old) {
			t.Fatalf("下课提醒设置层不应继续使用旧长文案 %q: %q", old, menu.text)
		}
	}
	privateGroupControlRequireButton(t, menu, "设置下课时间")
	privateGroupControlRequireButton(t, menu, "设置U差额阈值")
	privateGroupControlRequireButton(t, menu, "查询下课提醒")
	privateGroupControlRequireButton(t, menu, "文案管理")
	for _, label := range []string{"设置正常文案", "设置待确认文案", "设置无账单文案", "设置复核文案", "设置失败文案"} {
		privateGroupControlRequireNoButton(t, menu, label)
	}

	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("closing-time-open-input", privateGroupControlButtonData(t, menu, "设置下课时间"), ownerID, ownerID, 990655)); err != nil {
		t.Fatalf("打开下课时间输入失败: %v", err)
	}
	prompt := privateGroupControlLatestMessageContaining(t, capture, ownerID, "请发送下课提醒时间")
	if strings.Contains(prompt.text, "21:49 2.5") {
		t.Fatalf("下课时间输入页不应把U阈值作为同一行参数: %q", prompt.text)
	}
	app.dispatchUpdate(context.Background(), fourLayerPrivateTextUpdate(880652, 880652, ownerID, "owner_private", "21:49 2.5", int(time.Now().Unix())+1))
	if !fourLayerHasReply(capture.snapshot(), ownerID, 880652, "格式错误，请发送 21:49") {
		t.Fatalf("下课时间输入应拒绝附带U阈值: %+v", capture.snapshot())
	}

	app.dispatchUpdate(context.Background(), fourLayerPrivateTextUpdate(880653, 880653, ownerID, "owner_private", "21:49", int(time.Now().Unix())+2))
	preview := privateGroupControlLatestMessageContaining(t, capture, ownerID, "新时间：21:49")
	if strings.Contains(preview.text, "差额阈值：") {
		t.Fatalf("设置下课时间确认页不应展示U阈值: %q", preview.text)
	}
	for _, want := range []string{"当前时间分布", "04:00", "21:30", "新时间：21:49", "不改变U阈值和文案"} {
		if !strings.Contains(preview.text, want) {
			t.Fatalf("设置下课时间确认页缺少说明 %q: %q", want, preview.text)
		}
	}
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("closing-time-confirm", privateGroupControlButtonData(t, preview, "确认设置"), ownerID, ownerID, 990654)); err != nil {
		t.Fatalf("确认设置下课时间失败: %v", err)
	}

	for _, chatID := range groupIDs {
		privateGroupControlAssertClosingReminderTime(t, database, chatID, "21:49")
		privateGroupControlAssertClosingReminderThreshold(t, database, chatID, thresholds[chatID])
	}
	if !fourLayerHasReply(capture.snapshot(), ownerID, 990654, "批量设置下课时间结果：") {
		t.Fatalf("缺少下课时间批量设置结果: %+v", capture.snapshot())
	}
	if fourLayerHasReply(capture.snapshot(), ownerID, 990654, "差额阈值：") {
		t.Fatalf("设置下课时间结果不应包含U阈值: %+v", capture.snapshot())
	}
	if !fourLayerHasReply(capture.snapshot(), ownerID, 990654, "设置成功：3") {
		t.Fatalf("下课时间批量设置成功数量异常: %+v", capture.snapshot())
	}
	result := privateGroupControlLatestMessageContaining(t, capture, ownerID, "批量设置下课时间结果：")
	privateGroupControlRequireButton(t, result, "文案管理")
	privateGroupControlRequireButton(t, result, "设置U差额阈值")
	privateGroupControlRequireButton(t, result, "返回群控面板")

	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("closing-time-continue-text-management", privateGroupControlButtonData(t, result, "文案管理"), ownerID, ownerID, 990656)); err != nil {
		t.Fatalf("下课时间设置后应能继续进入文案管理: %v", err)
	}
	textMenu := privateGroupControlLatestMessageContaining(t, capture, ownerID, "文案管理")
	if !strings.Contains(textMenu.text, "已选：3 个群") {
		t.Fatalf("继续进入文案管理应保留已选群: %q", textMenu.text)
	}
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("closing-time-text-management-back", privateGroupControlButtonData(t, textMenu, "返回提醒设置"), ownerID, ownerID, 990656)); err != nil {
		t.Fatalf("文案管理应能返回提醒设置: %v", err)
	}
	menu = privateGroupControlLatestMessageContaining(t, capture, ownerID, "下课提醒设置")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("closing-time-continue-threshold", privateGroupControlButtonData(t, menu, "设置U差额阈值"), ownerID, ownerID, 990656)); err != nil {
		t.Fatalf("下课时间设置后应能继续设置U阈值: %v", err)
	}
	thresholdPrompt := privateGroupControlLatestMessageContaining(t, capture, ownerID, "请发送U差额提醒阈值")
	if !strings.Contains(thresholdPrompt.text, "已选：3 个群") {
		t.Fatalf("继续设置应保留已选群: %q", thresholdPrompt.text)
	}
	app.dispatchUpdate(context.Background(), fourLayerPrivateTextUpdate(880654, 880654, ownerID, "owner_private", "2.5", int(time.Now().Unix())+3))
	thresholdPreview := privateGroupControlLatestMessageContaining(t, capture, ownerID, "目标阈值：2.50 U")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("closing-threshold-confirm-after-time", privateGroupControlButtonData(t, thresholdPreview, "确认设置"), ownerID, ownerID, 990657)); err != nil {
		t.Fatalf("继续确认设置U阈值失败: %v", err)
	}
	for _, chatID := range groupIDs {
		privateGroupControlAssertClosingReminderTime(t, database, chatID, "21:49")
		privateGroupControlAssertClosingReminderThreshold(t, database, chatID, 2.5)
	}
}

func TestPrivateGroupControlClosingReminderQueryShowsGroupsByValue(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-closing-reminder-query.db")
	ownerID := int64(880661)
	groupIDs := setupPrivateGroupControlGroups(t, database, ownerID, privateGroupControlChatIDs(-100880661, 3))
	app.isGroupMemberFn = privateGroupControlBotMemberFn(app, nil)

	configs := []db.GroupClosingReminderConfig{
		{ChatID: groupIDs[0], Enabled: 1, ReminderTime: "04:00", ThresholdU: 1.5, UpdatedBy: ownerID},
		{ChatID: groupIDs[1], Enabled: 1, ReminderTime: "21:49", ThresholdU: 2.5, UpdatedBy: ownerID},
		{ChatID: groupIDs[2], Enabled: 1, ReminderTime: "04:00", ThresholdU: 1.5, UpdatedBy: ownerID},
	}
	for _, config := range configs {
		if err := database.UpsertClosingReminderConfig(config); err != nil {
			t.Fatalf("初始化下课提醒配置失败(chat=%d): %v", config.ChatID, err)
		}
	}

	menu := privateGroupControlOpenClosingReminderMenu(t, app, capture, ownerID)
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("closing-query", privateGroupControlButtonData(t, menu, "查询下课提醒"), ownerID, ownerID, 990661)); err != nil {
		t.Fatalf("查询下课提醒失败: %v", err)
	}
	for _, want := range []string{
		"下课提醒批量查询：",
		"21:49：1 个",
		"群：私聊群控-02",
		"04:00：2 个",
		"群：私聊群控-01、私聊群控-03",
		"2.50 U：1 个",
	} {
		if !fourLayerHasReply(capture.snapshot(), ownerID, 990661, want) {
			t.Fatalf("下课提醒查询结果缺少 %q: %+v", want, capture.snapshot())
		}
	}
}

func TestPrivateGroupControlClosingReminderThresholdAndTextCRUD(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-closing-reminder-crud.db")
	ownerID := int64(880681)
	groupIDs := setupPrivateGroupControlGroups(t, database, ownerID, privateGroupControlChatIDs(-100880681, 2))
	app.isGroupMemberFn = privateGroupControlBotMemberFn(app, nil)

	menu := privateGroupControlOpenClosingReminderMenu(t, app, capture, ownerID)
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("threshold-open", privateGroupControlButtonData(t, menu, "设置U差额阈值"), ownerID, ownerID, 990681)); err != nil {
		t.Fatalf("打开U阈值输入失败: %v", err)
	}
	prompt := privateGroupControlLatestMessageContaining(t, capture, ownerID, "请发送U差额提醒阈值")
	if !strings.Contains(prompt.text, "例如 2.5") {
		t.Fatalf("U阈值输入页说明异常: %q", prompt.text)
	}
	app.dispatchUpdate(context.Background(), fourLayerPrivateTextUpdate(880681, 880681, ownerID, "owner_private", "2.5U", int(time.Now().Unix())+1))
	preview := privateGroupControlLatestMessageContaining(t, capture, ownerID, "目标阈值：2.50 U")
	if !strings.Contains(preview.text, "子操作：设置U差额阈值") {
		t.Fatalf("U阈值确认页应展示子操作: %q", preview.text)
	}
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("threshold-confirm", privateGroupControlButtonData(t, preview, "确认设置"), ownerID, ownerID, 990682)); err != nil {
		t.Fatalf("确认设置U阈值失败: %v", err)
	}
	for _, chatID := range groupIDs {
		privateGroupControlAssertClosingReminderThreshold(t, database, chatID, 2.5)
	}

	textMenu := privateGroupControlOpenClosingReminderTextManagement(t, app, capture, ownerID)
	for _, label := range []string{"查询全部文案", "预览全部文案", "设置正常文案", "恢复全部文案", "返回提醒设置"} {
		privateGroupControlRequireButton(t, textMenu, label)
	}
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("text-open", privateGroupControlButtonData(t, textMenu, "设置正常文案"), ownerID, ownerID, 990683)); err != nil {
		t.Fatalf("打开正常文案输入失败: %v", err)
	}
	prompt = privateGroupControlLatestMessageContaining(t, capture, ownerID, "请发送正常状态下课文案")
	app.dispatchUpdate(context.Background(), fourLayerPrivateTextUpdate(880682, 880682, ownerID, "owner_private", "下课啦，今天辛苦大家。\n今日收尾完成。", int(time.Now().Unix())+2))
	preview = privateGroupControlLatestMessageContaining(t, capture, ownerID, "目标文案：下课啦，今天辛苦大家。")
	if !strings.Contains(preview.text, "子操作：设置正常文案") {
		t.Fatalf("文案确认页应展示子操作: %q", preview.text)
	}
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("text-confirm", privateGroupControlButtonData(t, preview, "确认设置"), ownerID, ownerID, 990684)); err != nil {
		t.Fatalf("确认设置正常文案失败: %v", err)
	}
	for _, chatID := range groupIDs {
		privateGroupControlAssertClosingReminderText(t, database, chatID, db.ClosingReminderStatusNormal, "下课啦，今天辛苦大家。\n今日收尾完成。")
	}

	textMenu = privateGroupControlOpenClosingReminderTextManagement(t, app, capture, ownerID)
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("text-query", privateGroupControlButtonData(t, textMenu, "查询全部文案"), ownerID, ownerID, 990685)); err != nil {
		t.Fatalf("查询全部文案失败: %v", err)
	}
	if !fourLayerHasReply(capture.snapshot(), ownerID, 990685, "下课文案批量查询：") {
		t.Fatalf("缺少下课文案查询结果: %+v", capture.snapshot())
	}
	if !fourLayerHasReply(capture.snapshot(), ownerID, 990685, "正常：2 个自定义") {
		t.Fatalf("下课文案查询应展示自定义数量: %+v", capture.snapshot())
	}

	textMenu = privateGroupControlOpenClosingReminderTextManagement(t, app, capture, ownerID)
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("restore-all-open", privateGroupControlButtonData(t, textMenu, "恢复全部文案"), ownerID, ownerID, 990686)); err != nil {
		t.Fatalf("打开恢复全部文案确认失败: %v", err)
	}
	preview = privateGroupControlLatestMessageContaining(t, capture, ownerID, "子操作：恢复全部文案")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("restore-all-confirm", privateGroupControlButtonData(t, preview, "确认设置"), ownerID, ownerID, 990687)); err != nil {
		t.Fatalf("确认恢复全部文案失败: %v", err)
	}
	for _, chatID := range groupIDs {
		privateGroupControlAssertClosingReminderText(t, database, chatID, db.ClosingReminderStatusNormal, "")
	}
}

func TestPrivateGroupControlRejectsEmptyTagOrType(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-empty-input.db")
	ownerID := int64(880701)
	groupIDs := setupPrivateGroupControlGroups(t, database, ownerID, []int64{-100880701})
	app.isGroupMemberFn = privateGroupControlBotMemberFn(app, nil)

	privateGroupControlSelectGroupsAndInput(t, app, capture, ownerID, "批量设置群标签", []string{"私聊群控-01"}, ",,,")
	if !fourLayerHasReply(capture.snapshot(), ownerID, 880903, "标签不能为空") {
		t.Fatalf("空标签应被拒绝: %+v", capture.snapshot())
	}
	privateGroupControlAssertGroupTags(t, database, groupIDs[0], "")

	app.dispatchUpdate(context.Background(), fourLayerPrivateTextUpdate(880904, 880904, ownerID, "owner_private", "有效标签", int(time.Now().Unix())+1))
	preview := privateGroupControlLatestMessageContaining(t, capture, ownerID, "目标标签：有效标签")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("empty-retry-confirm", privateGroupControlButtonData(t, preview, "确认设置"), ownerID, ownerID, 990801)); err != nil {
		t.Fatalf("重输有效标签后确认失败: %v", err)
	}
	privateGroupControlAssertGroupTags(t, database, groupIDs[0], "有效标签")
}

func TestPrivateGroupControlConfirmFiltersExpiredOrUnauthorizedTargets(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-final-filter.db")
	ownerID := int64(880801)
	groupIDs := setupPrivateGroupControlGroups(t, database, ownerID, privateGroupControlChatIDs(-100880801, 2))
	botLeft := map[int64]bool{}
	app.isGroupMemberFn = privateGroupControlBotMemberFn(app, botLeft)

	preview := privateGroupControlSelectGroupsAndInput(t, app, capture, ownerID, "批量设置群类型", []string{"私聊群控-01", "私聊群控-02"}, "过滤后类型")
	botLeft[groupIDs[1]] = true
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("filter-confirm", privateGroupControlButtonData(t, preview, "确认设置"), ownerID, ownerID, 990901)); err != nil {
		t.Fatalf("确认过滤目标失败: %v", err)
	}

	privateGroupControlAssertGroupType(t, database, groupIDs[0], "过滤后类型")
	privateGroupControlAssertGroupType(t, database, groupIDs[1], "")
	if !fourLayerHasReply(capture.snapshot(), ownerID, 990901, "失效目标：1") {
		t.Fatalf("应反馈失效目标数量: %+v", capture.snapshot())
	}
}

func TestPrivateGroupControlResultShowsRecoveryActionsForFailedTargets(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-recovery-actions.db")
	ownerID := int64(880811)
	groupIDs := setupPrivateGroupControlGroups(t, database, ownerID, privateGroupControlChatIDs(-100880811, 2))
	botLeft := map[int64]bool{}
	app.isGroupMemberFn = privateGroupControlBotMemberFn(app, botLeft)

	preview := privateGroupControlSelectGroupsAndInput(t, app, capture, ownerID, "批量设置群类型", []string{"私聊群控-01", "私聊群控-02"}, "恢复测试")
	botLeft[groupIDs[1]] = true
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("recovery-actions-confirm", privateGroupControlButtonData(t, preview, "确认设置"), ownerID, ownerID, 990911)); err != nil {
		t.Fatalf("确认过滤目标失败: %v", err)
	}

	result := privateGroupControlLatestMessageContaining(t, capture, ownerID, "失败/失效明细：")
	if !strings.Contains(result.text, "私聊群控-02") || !strings.Contains(result.text, "机器人不在群内") {
		t.Fatalf("失败结果应展示可恢复原因: %q", result.text)
	}
	privateGroupControlRequireButton(t, result, "重试失败群")
	privateGroupControlRequireButton(t, result, "重新选择")
	privateGroupControlRequireButton(t, result, "导出失败列表")

	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("recovery-export", privateGroupControlButtonData(t, result, "导出失败列表"), ownerID, ownerID, 990911)); err != nil {
		t.Fatalf("导出失败列表不应报错: %v", err)
	}
	exported := privateGroupControlLatestMessageContaining(t, capture, ownerID, "失败/失效列表")
	if !strings.Contains(exported.text, "私聊群控-02") || !strings.Contains(exported.text, "机器人不在群内") {
		t.Fatalf("导出失败列表应包含失败群和原因: %q", exported.text)
	}
}

func TestPrivateGroupControlRetryFailedTargetsAfterRecovery(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-retry-failed.db")
	ownerID := int64(880812)
	groupIDs := setupPrivateGroupControlGroups(t, database, ownerID, privateGroupControlChatIDs(-100880812, 2))
	botLeft := map[int64]bool{}
	app.isGroupMemberFn = privateGroupControlBotMemberFn(app, botLeft)

	preview := privateGroupControlSelectGroupsAndInput(t, app, capture, ownerID, "批量设置群类型", []string{"私聊群控-01", "私聊群控-02"}, "恢复后类型")
	botLeft[groupIDs[1]] = true
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("retry-failed-confirm", privateGroupControlButtonData(t, preview, "确认设置"), ownerID, ownerID, 990912)); err != nil {
		t.Fatalf("确认过滤目标失败: %v", err)
	}
	privateGroupControlAssertGroupType(t, database, groupIDs[0], "恢复后类型")
	privateGroupControlAssertGroupType(t, database, groupIDs[1], "")

	result := privateGroupControlLatestMessageContaining(t, capture, ownerID, "失败/失效明细：")
	botLeft[groupIDs[1]] = false
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("retry-failed-click", privateGroupControlButtonData(t, result, "重试失败群"), ownerID, ownerID, 990912)); err != nil {
		t.Fatalf("重试失败群失败: %v", err)
	}

	privateGroupControlAssertGroupType(t, database, groupIDs[0], "恢复后类型")
	privateGroupControlAssertGroupType(t, database, groupIDs[1], "恢复后类型")
	retryResult := privateGroupControlLatestMessageContaining(t, capture, ownerID, "应执行目标：1")
	if !strings.Contains(retryResult.text, "设置成功：1") || strings.Contains(retryResult.text, "失败/失效明细：") {
		t.Fatalf("重试结果应只覆盖恢复后的失败目标: %q", retryResult.text)
	}
}

func TestPrivateGroupControlDuplicateConfirmDoesNotRepeatBroadcast(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-duplicate-confirm.db")
	ownerID := int64(880901)
	setupPrivateGroupControlGroups(t, database, ownerID, []int64{-100880901})
	app.isGroupMemberFn = privateGroupControlBotMemberFn(app, nil)

	preview := privateGroupControlSelectGroupsAndInput(t, app, capture, ownerID, "选择群群发", []string{"私聊群控-01"}, "重复确认内容")
	confirmData := privateGroupControlButtonData(t, preview, "确认群发")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("dup-confirm-1", confirmData, ownerID, ownerID, 991001)); err != nil {
		t.Fatalf("首次确认群发失败: %v", err)
	}
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("dup-confirm-2", confirmData, ownerID, ownerID, 991001)); err != nil {
		t.Fatalf("重复确认不应报错: %v", err)
	}
	if got := privateGroupControlCountExactText(capture.snapshot(), "重复确认内容"); got != 1 {
		t.Fatalf("重复确认不应重复群发: got=%d snapshot=%+v", got, capture.snapshot())
	}
	if !fourLayerHasCallbackAnswer(capture.answerSnapshot(), "dup-confirm-2", "已完成") {
		t.Fatalf("重复确认应提示已完成或执行中: %+v", capture.answerSnapshot())
	}
}

func TestPrivateGroupControlBroadcastSendsOnlySelectedGroups(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-broadcast-text.db")
	ownerID := int64(881001)
	groupIDs := setupPrivateGroupControlGroups(t, database, ownerID, privateGroupControlChatIDs(-100881001, 3))
	app.isGroupMemberFn = privateGroupControlBotMemberFn(app, nil)

	preview := privateGroupControlSelectGroupsAndInput(t, app, capture, ownerID, "选择群群发", []string{"私聊群控-01", "私聊群控-03"}, "菜单")
	if strings.Contains(preview.text, privateIntroText()) {
		t.Fatalf("等待群发内容时不应落入私聊菜单: %q", preview.text)
	}
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("broadcast-confirm", privateGroupControlButtonData(t, preview, "确认群发"), ownerID, ownerID, 991101)); err != nil {
		t.Fatalf("确认文字群发失败: %v", err)
	}

	snapshot := capture.snapshot()
	if !privateGroupControlHasExactMessage(snapshot, groupIDs[0], "菜单") || !privateGroupControlHasExactMessage(snapshot, groupIDs[2], "菜单") {
		t.Fatalf("应发送到选中群: %+v", snapshot)
	}
	if privateGroupControlHasExactMessage(snapshot, groupIDs[1], "菜单") {
		t.Fatalf("不应发送到未选中群: %+v", snapshot)
	}
	if !fourLayerHasReply(snapshot, ownerID, 991101, "发送成功：2") {
		t.Fatalf("群发结果统计异常: %+v", snapshot)
	}
}

func TestPrivateGroupControlBroadcastSupportsPhotoVideoAndVoice(t *testing.T) {
	for _, tc := range []struct {
		name        string
		update      func(updateID, messageID int, userID int64, username string, date int) tgbotapi.Update
		wantPreview string
	}{
		{
			name: "photo",
			update: func(updateID, messageID int, userID int64, username string, date int) tgbotapi.Update {
				return fourLayerPrivatePhotoUpdate(updateID, messageID, userID, username, "图片说明", date)
			},
			wantPreview: "图片+文字：图片说明",
		},
		{
			name: "video",
			update: func(updateID, messageID int, userID int64, username string, date int) tgbotapi.Update {
				return fourLayerPrivateVideoUpdate(updateID, messageID, userID, username, "视频说明", date)
			},
			wantPreview: "视频+文字：视频说明",
		},
		{
			name: "voice",
			update: func(updateID, messageID int, userID int64, username string, date int) tgbotapi.Update {
				return fourLayerPrivateVoiceUpdate(updateID, messageID, userID, username, date)
			},
			wantPreview: "语音",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, database, capture := newFourLayerTestApp(t, "private-group-control-broadcast-"+tc.name+".db")
			ownerID := int64(881101)
			groupIDs := setupPrivateGroupControlGroups(t, database, ownerID, []int64{-100881101})
			app.isGroupMemberFn = privateGroupControlBotMemberFn(app, nil)

			privateGroupControlSelectGroups(t, app, capture, ownerID, "选择群群发", []string{"私聊群控-01"})
			app.dispatchUpdate(context.Background(), tc.update(881301, 881301, ownerID, "owner_private", int(time.Now().Unix())))
			preview := privateGroupControlLatestMessageContaining(t, capture, ownerID, tc.wantPreview)
			if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("media-confirm-"+tc.name, privateGroupControlButtonData(t, preview, "确认群发"), ownerID, ownerID, 991201)); err != nil {
				t.Fatalf("确认媒体群发失败: %v", err)
			}
			copies := capture.copyMessageSnapshot()
			if len(copies) != 1 || copies[0].chatID != groupIDs[0] || copies[0].fromChatID != ownerID || copies[0].sourceMessageID != 881301 {
				t.Fatalf("媒体群发应使用 copyMessage 到选中群: %+v", copies)
			}
		})
	}
}

func TestPrivateGroupControlBroadcastRejectsUnsupportedMedia(t *testing.T) {
	app, database, capture := newFourLayerTestApp(t, "private-group-control-broadcast-unsupported.db")
	ownerID := int64(881201)
	setupPrivateGroupControlGroups(t, database, ownerID, []int64{-100881201})
	app.isGroupMemberFn = privateGroupControlBotMemberFn(app, nil)

	privateGroupControlSelectGroups(t, app, capture, ownerID, "选择群群发", []string{"私聊群控-01"})
	app.dispatchUpdate(context.Background(), fourLayerPrivateDocumentUpdate(881401, 881401, ownerID, "owner_private", int(time.Now().Unix())))
	if !fourLayerHasReply(capture.snapshot(), ownerID, 881401, "不支持") {
		t.Fatalf("文档媒体应被拒绝: %+v", capture.snapshot())
	}
	app.dispatchUpdate(context.Background(), fourLayerPrivateMediaGroupPhotoUpdate(881402, 881402, ownerID, "owner_private", int(time.Now().Unix())+1))
	if !fourLayerHasReply(capture.snapshot(), ownerID, 881402, "不支持相册") {
		t.Fatalf("相册消息应被拒绝: %+v", capture.snapshot())
	}
	if len(capture.copyMessageSnapshot()) != 0 {
		t.Fatalf("拒绝的媒体不应触发 copyMessage: %+v", capture.copyMessageSnapshot())
	}
}

func TestPrivateGroupControlRegressionGroupPanelStillRejectsPrivateCallback(t *testing.T) {
	app, _, capture := newFourLayerTestApp(t, "private-group-control-regression.db")
	userID := int64(881301)
	callback := privateGroupControlCallback("gp-private", groupPanelCallbackPrefix+"panel", userID, userID, 991301)
	if err := app.handleCallbackQuery(context.Background(), callback); err != nil {
		t.Fatalf("群内面板私聊回调拒绝不应报错: %v", err)
	}
	if !fourLayerHasCallbackAnswer(capture.answerSnapshot(), "gp-private", "请在群组中使用群控面板") {
		t.Fatalf("群内面板私聊回调仍应被拒绝: %+v", capture.answerSnapshot())
	}
}

func fourLayerPrivateCommandUpdate(updateID, messageID int, userID int64, username, text string, date int) tgbotapi.Update {
	upd := fourLayerPrivateTextUpdate(updateID, messageID, userID, username, text, date)
	upd.Message.Entities = []tgbotapi.MessageEntity{
		{Type: "bot_command", Offset: 0, Length: len(text)},
	}
	return upd
}

func setupPrivateGroupControlGroups(t testing.TB, database *db.Database, ownerID int64, chatIDs []int64) []int64 {
	t.Helper()
	out := append([]int64(nil), chatIDs...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	for i, chatID := range out {
		title := fmt.Sprintf("私聊群控-%02d", i+1)
		if _, err := database.EnsureGroup(chatID, title); err != nil {
			t.Fatalf("初始化群失败(chat=%d): %v", chatID, err)
		}
		if err := database.SetOwner(chatID, ownerID); err != nil {
			t.Fatalf("设置群权限人失败(chat=%d): %v", chatID, err)
		}
		if err := database.UpdateGroupFields(chatID, map[string]any{"group_tags": "", "group_type": ""}); err != nil {
			t.Fatalf("初始化群字段失败(chat=%d): %v", chatID, err)
		}
	}
	return out
}

func privateGroupControlChatIDs(start int64, count int) []int64 {
	out := make([]int64, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, start-int64(i))
	}
	return out
}

func privateGroupControlBotMemberFn(app *App, botLeft map[int64]bool) func(chatID, userID int64) bool {
	return func(chatID, userID int64) bool {
		if app == nil || app.api == nil || userID != app.api.Self.ID {
			return false
		}
		return !botLeft[chatID]
	}
}

func privateGroupControlOpenPanel(t *testing.T, app *App, capture *fourLayerCapture, userID int64, username string, messageID int, date int) fourLayerCapturedMessage {
	t.Helper()
	app.dispatchUpdate(context.Background(), fourLayerPrivateTextUpdate(messageID, messageID, userID, username, "群控面板", date))
	return privateGroupControlLatestMessageContaining(t, capture, userID, "私聊群控面板")
}

func privateGroupControlOpenTypeDetail(t *testing.T, app *App, capture *fourLayerCapture, ownerID int64, messageID, callbackMessageID int, typeName string) fourLayerCapturedMessage {
	t.Helper()
	main := privateGroupControlOpenPanel(t, app, capture, ownerID, "owner_private", messageID, int(time.Now().Unix()))
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
		"category-management-"+strconv.Itoa(messageID),
		privateGroupControlButtonData(t, main, "类型/标签列表管理"),
		ownerID,
		ownerID,
		callbackMessageID,
	)); err != nil {
		t.Fatalf("打开类型/标签列表管理失败: %v", err)
	}
	menu := privateGroupControlLatestMessageContaining(t, capture, ownerID, "类型/标签列表管理")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
		"type-list-"+strconv.Itoa(messageID),
		privateGroupControlExactButtonData(t, menu, "类型列表"),
		ownerID,
		ownerID,
		callbackMessageID,
	)); err != nil {
		t.Fatalf("打开类型列表失败: %v", err)
	}
	typeList := privateGroupControlLatestMessageContaining(t, capture, ownerID, "类型列表")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
		"type-detail-"+strconv.Itoa(messageID),
		privateGroupControlButtonData(t, typeList, typeName),
		ownerID,
		ownerID,
		callbackMessageID,
	)); err != nil {
		t.Fatalf("打开类型详情失败(%s): %v", typeName, err)
	}
	return privateGroupControlLatestMessageContaining(t, capture, ownerID, "类型："+typeName)
}

func privateGroupControlOpenTagDetail(t *testing.T, app *App, capture *fourLayerCapture, ownerID int64, messageID, callbackMessageID int, tagName string) fourLayerCapturedMessage {
	t.Helper()
	main := privateGroupControlOpenPanel(t, app, capture, ownerID, "owner_private", messageID, int(time.Now().Unix()))
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
		"category-management-"+strconv.Itoa(messageID),
		privateGroupControlButtonData(t, main, "类型/标签列表管理"),
		ownerID,
		ownerID,
		callbackMessageID,
	)); err != nil {
		t.Fatalf("打开类型/标签列表管理失败: %v", err)
	}
	menu := privateGroupControlLatestMessageContaining(t, capture, ownerID, "类型/标签列表管理")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
		"tag-list-"+strconv.Itoa(messageID),
		privateGroupControlExactButtonData(t, menu, "标签列表"),
		ownerID,
		ownerID,
		callbackMessageID,
	)); err != nil {
		t.Fatalf("打开标签列表失败: %v", err)
	}
	tagList := privateGroupControlLatestMessageContaining(t, capture, ownerID, "标签列表")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback(
		"tag-detail-"+strconv.Itoa(messageID),
		privateGroupControlButtonData(t, tagList, tagName),
		ownerID,
		ownerID,
		callbackMessageID,
	)); err != nil {
		t.Fatalf("打开标签详情失败(%s): %v", tagName, err)
	}
	return privateGroupControlLatestMessageContaining(t, capture, ownerID, "标签："+tagName)
}

func privateGroupControlSelectGroups(t *testing.T, app *App, capture *fourLayerCapture, ownerID int64, actionLabel string, groupLabels []string) fourLayerCapturedMessage {
	t.Helper()
	main := privateGroupControlOpenPanel(t, app, capture, ownerID, "owner_private", 880901, int(time.Now().Unix()))
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("select-action-"+actionLabel, privateGroupControlButtonData(t, main, actionLabel), ownerID, ownerID, 990801)); err != nil {
		t.Fatalf("打开%s选群失败: %v", actionLabel, err)
	}
	page := privateGroupControlLatestMessageContaining(t, capture, ownerID, "选择群")
	for i, label := range groupLabels {
		if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("select-group-"+strconv.Itoa(i), privateGroupControlButtonData(t, page, label), ownerID, ownerID, 990801)); err != nil {
			t.Fatalf("选择群%s失败: %v", label, err)
		}
		page = privateGroupControlLatestMessageContaining(t, capture, ownerID, "已选：")
	}
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("select-choose-"+actionLabel, privateGroupControlButtonData(t, page, "确认选择"), ownerID, ownerID, 990801)); err != nil {
		t.Fatalf("确认选择%s失败: %v", actionLabel, err)
	}
	return privateGroupControlLatestMessageContaining(t, capture, ownerID, "请发送")
}

func privateGroupControlSelectGroupsAndInput(t *testing.T, app *App, capture *fourLayerCapture, ownerID int64, actionLabel string, groupLabels []string, input string) fourLayerCapturedMessage {
	t.Helper()
	privateGroupControlSelectGroups(t, app, capture, ownerID, actionLabel, groupLabels)
	app.dispatchUpdate(context.Background(), fourLayerPrivateTextUpdate(880903, 880903, ownerID, "owner_private", input, int(time.Now().Unix())))
	switch actionLabel {
	case "批量设置群标签":
		if strings.Trim(input, " ,，") == "" {
			return privateGroupControlLatestMessageContaining(t, capture, ownerID, "标签不能为空")
		}
		return privateGroupControlLatestMessageContaining(t, capture, ownerID, "目标标签：")
	case "批量设置群类型":
		if strings.TrimSpace(input) == "" {
			return privateGroupControlLatestMessageContaining(t, capture, ownerID, "类型不能为空")
		}
		return privateGroupControlLatestMessageContaining(t, capture, ownerID, "目标类型：")
	default:
		return privateGroupControlLatestMessageContaining(t, capture, ownerID, "确认群发")
	}
}

func privateGroupControlOpenClosingReminderMenu(t *testing.T, app *App, capture *fourLayerCapture, ownerID int64) fourLayerCapturedMessage {
	t.Helper()
	main := privateGroupControlOpenPanel(t, app, capture, ownerID, "owner_private", 880951, int(time.Now().Unix()))
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("closing-menu-action", privateGroupControlExactButtonData(t, main, privateGroupControlClosingReminderEntryLabel), ownerID, ownerID, 990951)); err != nil {
		t.Fatalf("打开下课提醒选群失败: %v", err)
	}
	page := privateGroupControlLatestMessageContaining(t, capture, ownerID, "第1步：先选择目标群")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("closing-menu-select-all", privateGroupControlButtonData(t, page, "全选本页"), ownerID, ownerID, 990952)); err != nil {
		t.Fatalf("全选下课提醒目标失败: %v", err)
	}
	page = privateGroupControlLatestMessageContaining(t, capture, ownerID, "已选：")
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("closing-menu-choose", privateGroupControlButtonData(t, page, "确认选择"), ownerID, ownerID, 990953)); err != nil {
		t.Fatalf("确认下课提醒目标失败: %v", err)
	}
	return privateGroupControlLatestMessageContaining(t, capture, ownerID, "下课提醒设置")
}

func privateGroupControlOpenClosingReminderTextManagement(t *testing.T, app *App, capture *fourLayerCapture, ownerID int64) fourLayerCapturedMessage {
	t.Helper()
	menu := privateGroupControlOpenClosingReminderMenu(t, app, capture, ownerID)
	if err := app.handleCallbackQuery(context.Background(), privateGroupControlCallback("closing-text-management-open", privateGroupControlButtonData(t, menu, "文案管理"), ownerID, ownerID, 990954)); err != nil {
		t.Fatalf("打开下课提醒文案管理失败: %v", err)
	}
	return privateGroupControlLatestMessageContaining(t, capture, ownerID, "文案管理")
}

func privateGroupControlCallback(queryID, data string, fromID, chatID int64, messageID int) *tgbotapi.CallbackQuery {
	return &tgbotapi.CallbackQuery{
		ID:   queryID,
		Data: data,
		From: &tgbotapi.User{
			ID:        fromID,
			UserName:  "callback_user",
			FirstName: "callback_user",
		},
		Message: &tgbotapi.Message{
			MessageID: messageID,
			Chat: &tgbotapi.Chat{
				ID:   chatID,
				Type: "private",
			},
		},
	}
}

func privateGroupControlLatestMessageContaining(t *testing.T, capture *fourLayerCapture, chatID int64, keyword string) fourLayerCapturedMessage {
	t.Helper()
	snapshot := capture.snapshot()
	for i := len(snapshot) - 1; i >= 0; i-- {
		item := snapshot[i]
		if item.chatID == chatID && strings.Contains(item.text, keyword) {
			return item
		}
	}
	t.Fatalf("未找到包含 %q 的消息，snapshot=%+v", keyword, snapshot)
	return fourLayerCapturedMessage{}
}

func privateGroupControlRequireButton(t *testing.T, msg fourLayerCapturedMessage, label string) {
	t.Helper()
	_ = privateGroupControlButtonData(t, msg, label)
}

func privateGroupControlRequireExactButton(t *testing.T, msg fourLayerCapturedMessage, label string) {
	t.Helper()
	_ = privateGroupControlExactButtonData(t, msg, label)
}

func privateGroupControlRequireNoButton(t *testing.T, msg fourLayerCapturedMessage, label string) {
	t.Helper()
	if privateGroupControlHasButton(t, msg, label) {
		t.Fatalf("不应展示按钮 %q: markup=%s text=%q", label, msg.replyMarkup, msg.text)
	}
}

func privateGroupControlAssertCallbacksValid(t *testing.T, msg fourLayerCapturedMessage) {
	t.Helper()
	if strings.TrimSpace(msg.replyMarkup) == "" {
		t.Fatalf("消息缺少 reply_markup: %+v", msg)
	}
	var markup privateGroupControlTestMarkup
	if err := json.Unmarshal([]byte(msg.replyMarkup), &markup); err != nil {
		t.Fatalf("解析 reply_markup 失败: %v raw=%s", err, msg.replyMarkup)
	}
	for _, row := range markup.InlineKeyboard {
		for _, button := range row {
			if strings.TrimSpace(button.CallbackData) == "" {
				t.Fatalf("按钮缺少 callback_data: %+v", button)
			}
			if len(button.CallbackData) > 64 {
				t.Fatalf("callback_data 过长: %q len=%d", button.CallbackData, len(button.CallbackData))
			}
			if button.CallbackData == "pc:invalid" {
				t.Fatalf("callback_data 不应退化为 pc:invalid: %+v", button)
			}
		}
	}
}

func privateGroupControlHasButton(t *testing.T, msg fourLayerCapturedMessage, label string) bool {
	t.Helper()
	if strings.TrimSpace(msg.replyMarkup) == "" {
		return false
	}
	var markup privateGroupControlTestMarkup
	if err := json.Unmarshal([]byte(msg.replyMarkup), &markup); err != nil {
		t.Fatalf("解析 reply_markup 失败: %v raw=%s", err, msg.replyMarkup)
	}
	for _, row := range markup.InlineKeyboard {
		for _, button := range row {
			if strings.Contains(button.Text, label) {
				return true
			}
		}
	}
	return false
}

func privateGroupControlExactButtonData(t *testing.T, msg fourLayerCapturedMessage, label string) string {
	t.Helper()
	if strings.TrimSpace(msg.replyMarkup) == "" {
		t.Fatalf("消息缺少 reply_markup: %+v", msg)
	}
	var markup privateGroupControlTestMarkup
	if err := json.Unmarshal([]byte(msg.replyMarkup), &markup); err != nil {
		t.Fatalf("解析 reply_markup 失败: %v raw=%s", err, msg.replyMarkup)
	}
	for _, row := range markup.InlineKeyboard {
		for _, button := range row {
			if strings.TrimSpace(button.Text) == label {
				if strings.TrimSpace(button.CallbackData) == "" {
					t.Fatalf("按钮 %q 缺少 callback_data: %+v", label, button)
				}
				return button.CallbackData
			}
		}
	}
	t.Fatalf("未找到精确按钮 %q: markup=%s text=%q", label, msg.replyMarkup, msg.text)
	return ""
}

func privateGroupControlButtonData(t *testing.T, msg fourLayerCapturedMessage, label string) string {
	t.Helper()
	if strings.TrimSpace(msg.replyMarkup) == "" {
		t.Fatalf("消息缺少 reply_markup: %+v", msg)
	}
	var markup privateGroupControlTestMarkup
	if err := json.Unmarshal([]byte(msg.replyMarkup), &markup); err != nil {
		t.Fatalf("解析 reply_markup 失败: %v raw=%s", err, msg.replyMarkup)
	}
	for _, row := range markup.InlineKeyboard {
		for _, button := range row {
			if strings.Contains(button.Text, label) {
				if strings.TrimSpace(button.CallbackData) == "" {
					t.Fatalf("按钮 %q 缺少 callback_data: %+v", label, button)
				}
				return button.CallbackData
			}
		}
	}
	t.Fatalf("未找到按钮 %q: markup=%s text=%q", label, msg.replyMarkup, msg.text)
	return ""
}

func privateGroupControlGroupButtonLabels(t *testing.T, msg fourLayerCapturedMessage) []string {
	t.Helper()
	if strings.TrimSpace(msg.replyMarkup) == "" {
		t.Fatalf("消息缺少 reply_markup: %+v", msg)
	}
	var markup privateGroupControlTestMarkup
	if err := json.Unmarshal([]byte(msg.replyMarkup), &markup); err != nil {
		t.Fatalf("解析 reply_markup 失败: %v raw=%s", err, msg.replyMarkup)
	}
	labels := []string{}
	for _, row := range markup.InlineKeyboard {
		for _, button := range row {
			label := strings.TrimPrefix(strings.TrimSpace(button.Text), "☐ ")
			label = strings.TrimPrefix(label, "☑ ")
			if strings.HasPrefix(label, "私聊群控-") {
				labels = append(labels, label)
			}
		}
	}
	return labels
}

func privateGroupControlAssertGroupTags(t *testing.T, database *db.Database, chatID int64, want string) {
	t.Helper()
	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群失败(chat=%d): %v", chatID, err)
	}
	if got := strings.TrimSpace(group.GroupTags); got != want {
		t.Fatalf("群标签异常(chat=%d): got=%q want=%q", chatID, got, want)
	}
}

func privateGroupControlAssertGroupType(t *testing.T, database *db.Database, chatID int64, want string) {
	t.Helper()
	group, err := database.GetGroup(chatID)
	if err != nil {
		t.Fatalf("读取群失败(chat=%d): %v", chatID, err)
	}
	if got := strings.TrimSpace(group.GroupType); got != want {
		t.Fatalf("群类型异常(chat=%d): got=%q want=%q", chatID, got, want)
	}
}

func privateGroupControlAssertClosingReminderTime(t *testing.T, database *db.Database, chatID int64, want string) {
	t.Helper()
	config, err := database.GetClosingReminderConfig(chatID)
	if err != nil {
		t.Fatalf("读取下课提醒配置失败(chat=%d): %v", chatID, err)
	}
	if got := strings.TrimSpace(config.ReminderTime); got != want {
		t.Fatalf("下课提醒时间异常(chat=%d): got=%q want=%q", chatID, got, want)
	}
}

func privateGroupControlAssertClosingReminderThreshold(t *testing.T, database *db.Database, chatID int64, want float64) {
	t.Helper()
	config, err := database.GetClosingReminderConfig(chatID)
	if err != nil {
		t.Fatalf("读取下课提醒配置失败(chat=%d): %v", chatID, err)
	}
	if !privateGroupControlFloatEqual(config.ThresholdU, want) {
		t.Fatalf("下课提醒阈值异常(chat=%d): got=%v want=%v", chatID, config.ThresholdU, want)
	}
}

func privateGroupControlAssertClosingReminderText(t *testing.T, database *db.Database, chatID int64, status string, want string) {
	t.Helper()
	config, err := database.GetClosingReminderConfig(chatID)
	if err != nil {
		t.Fatalf("读取下课提醒配置失败(chat=%d): %v", chatID, err)
	}
	if got := closingReminderTextField(config, status); got != strings.TrimSpace(want) {
		t.Fatalf("下课文案异常(chat=%d status=%s): got=%q want=%q", chatID, status, got, strings.TrimSpace(want))
	}
}

func privateGroupControlFloatEqual(a, b float64) bool {
	if a > b {
		return a-b < 0.000001
	}
	return b-a < 0.000001
}

func privateGroupControlHasExactMessage(snapshot []fourLayerCapturedMessage, chatID int64, text string) bool {
	for _, item := range snapshot {
		if item.chatID == chatID && strings.TrimSpace(item.text) == text {
			return true
		}
	}
	return false
}

func privateGroupControlCountExactText(snapshot []fourLayerCapturedMessage, text string) int {
	count := 0
	for _, item := range snapshot {
		if strings.TrimSpace(item.text) == text {
			count++
		}
	}
	return count
}
