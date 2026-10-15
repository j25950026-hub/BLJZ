package bot

import (
	"io"
	"log"
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

type fourLayerCapturedMessage struct {
	chatID      int64
	replyTo     int
	text        string
	parseMode   string
	replyMarkup string
	source      string
}

type fourLayerCapturedCallbackAnswer struct {
	queryID string
	text    string
	alert   bool
}

type fourLayerCapturedEvent struct {
	kind    string
	source  string
	queryID string
	chatID  int64
	replyTo int
	text    string
}

type fourLayerCapturedCopyMessage struct {
	chatID          int64
	fromChatID      int64
	sourceMessageID int
}

type fourLayerCapture struct {
	mu           sync.Mutex
	messages     []fourLayerCapturedMessage
	answers      []fourLayerCapturedCallbackAnswer
	copyMessages []fourLayerCapturedCopyMessage
	events       []fourLayerCapturedEvent
}

func (c *fourLayerCapture) append(item fourLayerCapturedMessage) {
	c.mu.Lock()
	c.messages = append(c.messages, item)
	c.events = append(c.events, fourLayerCapturedEvent{
		kind:    "message",
		source:  item.source,
		chatID:  item.chatID,
		replyTo: item.replyTo,
		text:    item.text,
	})
	c.mu.Unlock()
}

func (c *fourLayerCapture) appendAnswer(item fourLayerCapturedCallbackAnswer) {
	c.mu.Lock()
	c.answers = append(c.answers, item)
	c.events = append(c.events, fourLayerCapturedEvent{
		kind:    "answer",
		queryID: item.queryID,
		text:    item.text,
	})
	c.mu.Unlock()
}

func (c *fourLayerCapture) appendCopyMessage(item fourLayerCapturedCopyMessage) {
	c.mu.Lock()
	c.copyMessages = append(c.copyMessages, item)
	c.mu.Unlock()
}

func (c *fourLayerCapture) snapshot() []fourLayerCapturedMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]fourLayerCapturedMessage, len(c.messages))
	copy(out, c.messages)
	return out
}

func (c *fourLayerCapture) answerSnapshot() []fourLayerCapturedCallbackAnswer {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]fourLayerCapturedCallbackAnswer, len(c.answers))
	copy(out, c.answers)
	return out
}

func (c *fourLayerCapture) eventSnapshot() []fourLayerCapturedEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]fourLayerCapturedEvent, len(c.events))
	copy(out, c.events)
	return out
}

func (c *fourLayerCapture) copyMessageSnapshot() []fourLayerCapturedCopyMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]fourLayerCapturedCopyMessage, len(c.copyMessages))
	copy(out, c.copyMessages)
	return out
}

func newFourLayerMockBotAPI(t testing.TB) (*tgbotapi.BotAPI, *fourLayerCapture) {
	t.Helper()
	capture := &fourLayerCapture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := strings.TrimSpace(r.URL.Path)

		switch {
		case strings.HasSuffix(path, "/sendMessage"):
			if err := r.ParseForm(); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"ok":false,"description":"invalid form"}`))
				return
			}
			chatID, _ := strconv.ParseInt(strings.TrimSpace(r.PostForm.Get("chat_id")), 10, 64)
			replyTo, _ := strconv.Atoi(strings.TrimSpace(r.PostForm.Get("reply_to_message_id")))
			text := strings.TrimSpace(r.PostForm.Get("text"))
			parseMode := strings.TrimSpace(r.PostForm.Get("parse_mode"))
			replyMarkup := strings.TrimSpace(r.PostForm.Get("reply_markup"))
			capture.append(fourLayerCapturedMessage{
				chatID:      chatID,
				replyTo:     replyTo,
				text:        text,
				parseMode:   parseMode,
				replyMarkup: replyMarkup,
				source:      "send_message",
			})
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":440001}}`))
			return
		case strings.HasSuffix(path, "/sendPhoto"):
			if err := r.ParseMultipartForm(8 << 20); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"ok":false,"description":"invalid multipart"}`))
				return
			}
			chatID, _ := strconv.ParseInt(strings.TrimSpace(r.FormValue("chat_id")), 10, 64)
			replyTo, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("reply_to_message_id")))
			caption := strings.TrimSpace(r.FormValue("caption"))
			parseMode := strings.TrimSpace(r.FormValue("parse_mode"))
			replyMarkup := strings.TrimSpace(r.FormValue("reply_markup"))
			capture.append(fourLayerCapturedMessage{
				chatID:      chatID,
				replyTo:     replyTo,
				text:        caption,
				parseMode:   parseMode,
				replyMarkup: replyMarkup,
				source:      "send_photo",
			})
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":440003}}`))
			return
		case strings.HasSuffix(path, "/editMessageText"):
			if err := r.ParseForm(); err == nil {
				chatID, _ := strconv.ParseInt(strings.TrimSpace(r.PostForm.Get("chat_id")), 10, 64)
				replyTo, _ := strconv.Atoi(strings.TrimSpace(r.PostForm.Get("message_id")))
				text := strings.TrimSpace(r.PostForm.Get("text"))
				parseMode := strings.TrimSpace(r.PostForm.Get("parse_mode"))
				replyMarkup := strings.TrimSpace(r.PostForm.Get("reply_markup"))
				capture.append(fourLayerCapturedMessage{
					chatID:      chatID,
					replyTo:     replyTo,
					text:        text,
					parseMode:   parseMode,
					replyMarkup: replyMarkup,
					source:      "edit_message_text",
				})
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":440002}}`))
			return
		case strings.HasSuffix(path, "/answerCallbackQuery"):
			if err := r.ParseForm(); err == nil {
				alert := strings.EqualFold(strings.TrimSpace(r.PostForm.Get("show_alert")), "true")
				capture.appendAnswer(fourLayerCapturedCallbackAnswer{
					queryID: strings.TrimSpace(r.PostForm.Get("callback_query_id")),
					text:    strings.TrimSpace(r.PostForm.Get("text")),
					alert:   alert,
				})
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
			return
		case strings.HasSuffix(path, "/copyMessage"):
			if err := r.ParseForm(); err == nil {
				chatID, _ := strconv.ParseInt(strings.TrimSpace(r.PostForm.Get("chat_id")), 10, 64)
				fromChatID, _ := strconv.ParseInt(strings.TrimSpace(r.PostForm.Get("from_chat_id")), 10, 64)
				sourceMessageID, _ := strconv.Atoi(strings.TrimSpace(r.PostForm.Get("message_id")))
				capture.appendCopyMessage(fourLayerCapturedCopyMessage{
					chatID:          chatID,
					fromChatID:      fromChatID,
					sourceMessageID: sourceMessageID,
				})
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":440004}}`))
			return
		default:
			_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
			return
		}
	}))
	t.Cleanup(server.Close)

	api := &tgbotapi.BotAPI{
		Token:  "four-layer-test-token",
		Client: server.Client(),
	}
	api.SetAPIEndpoint(server.URL + "/bot%s/%s")
	api.Self = tgbotapi.User{
		ID:       440000,
		UserName: "four_layer_test_bot",
	}
	return api, capture
}

func newFourLayerTestAppWithDBPath(t testing.TB, dbFile string) (*App, *db.Database, *fourLayerCapture, string) {
	t.Helper()

	prevLogWriter := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() {
		log.SetOutput(prevLogWriter)
	})

	dbPath := filepath.Join(t.TempDir(), dbFile)
	database, err := db.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("关闭数据库失败: %v", closeErr)
		}
	})

	api, capture := newFourLayerMockBotAPI(t)
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("加载时区失败: %v", err)
	}

	app := &App{
		cfg: config.Config{
			Timezone:            "Asia/Shanghai",
			ReplayWindowSeconds: 60,
			LargeTxnThreshold:   50000,
			PrivateTrialDays:    3,
		},
		loc: loc,
		db:  database,
		api: api,
		now: time.Now,
	}
	return app, database, capture, dbPath
}

func newFourLayerTestApp(t testing.TB, dbFile string) (*App, *db.Database, *fourLayerCapture) {
	t.Helper()
	app, database, capture, _ := newFourLayerTestAppWithDBPath(t, dbFile)
	return app, database, capture
}

func fourLayerGroupTextUpdate(updateID, messageID int, chatID, userID int64, username, text string, date int) tgbotapi.Update {
	return tgbotapi.Update{
		UpdateID: updateID,
		Message: &tgbotapi.Message{
			MessageID: messageID,
			Date:      date,
			Chat: &tgbotapi.Chat{
				ID:    chatID,
				Type:  "supergroup",
				Title: "四层测试群",
			},
			From: &tgbotapi.User{
				ID:        userID,
				UserName:  username,
				FirstName: username,
			},
			Text: text,
		},
	}
}

func fourLayerEditedGroupTextUpdate(updateID, messageID int, chatID, userID int64, username, text string, date, editDate int) tgbotapi.Update {
	return tgbotapi.Update{
		UpdateID: updateID,
		EditedMessage: &tgbotapi.Message{
			MessageID: messageID,
			Date:      date,
			EditDate:  editDate,
			Chat: &tgbotapi.Chat{
				ID:    chatID,
				Type:  "supergroup",
				Title: "四层测试群",
			},
			From: &tgbotapi.User{
				ID:        userID,
				UserName:  username,
				FirstName: username,
			},
			Text: text,
		},
	}
}

func fourLayerPrivateTextUpdate(updateID, messageID int, userID int64, username, text string, date int) tgbotapi.Update {
	return tgbotapi.Update{
		UpdateID: updateID,
		Message: &tgbotapi.Message{
			MessageID: messageID,
			Date:      date,
			Chat: &tgbotapi.Chat{
				ID:   userID,
				Type: "private",
			},
			From: &tgbotapi.User{
				ID:        userID,
				UserName:  username,
				FirstName: username,
			},
			Text: text,
		},
	}
}

func fourLayerPrivatePhotoUpdate(updateID, messageID int, userID int64, username, caption string, date int) tgbotapi.Update {
	return tgbotapi.Update{
		UpdateID: updateID,
		Message: &tgbotapi.Message{
			MessageID: messageID,
			Date:      date,
			Chat: &tgbotapi.Chat{
				ID:   userID,
				Type: "private",
			},
			From: &tgbotapi.User{
				ID:        userID,
				UserName:  username,
				FirstName: username,
			},
			Caption: caption,
			Photo: []tgbotapi.PhotoSize{
				{
					FileID:       "private_photo_" + strconv.Itoa(messageID),
					FileUniqueID: "private_photo_unique_" + strconv.Itoa(messageID),
					Width:        100,
					Height:       100,
				},
			},
		},
	}
}

func fourLayerPrivateVideoUpdate(updateID, messageID int, userID int64, username, caption string, date int) tgbotapi.Update {
	return tgbotapi.Update{
		UpdateID: updateID,
		Message: &tgbotapi.Message{
			MessageID: messageID,
			Date:      date,
			Chat: &tgbotapi.Chat{
				ID:   userID,
				Type: "private",
			},
			From: &tgbotapi.User{
				ID:        userID,
				UserName:  username,
				FirstName: username,
			},
			Caption: caption,
			Video: &tgbotapi.Video{
				FileID:       "private_video_" + strconv.Itoa(messageID),
				FileUniqueID: "private_video_unique_" + strconv.Itoa(messageID),
				Width:        640,
				Height:       360,
				Duration:     1,
			},
		},
	}
}

func fourLayerPrivateVoiceUpdate(updateID, messageID int, userID int64, username string, date int) tgbotapi.Update {
	return tgbotapi.Update{
		UpdateID: updateID,
		Message: &tgbotapi.Message{
			MessageID: messageID,
			Date:      date,
			Chat: &tgbotapi.Chat{
				ID:   userID,
				Type: "private",
			},
			From: &tgbotapi.User{
				ID:        userID,
				UserName:  username,
				FirstName: username,
			},
			Voice: &tgbotapi.Voice{
				FileID:       "private_voice_" + strconv.Itoa(messageID),
				FileUniqueID: "private_voice_unique_" + strconv.Itoa(messageID),
				Duration:     1,
			},
		},
	}
}

func fourLayerPrivateDocumentUpdate(updateID, messageID int, userID int64, username string, date int) tgbotapi.Update {
	return tgbotapi.Update{
		UpdateID: updateID,
		Message: &tgbotapi.Message{
			MessageID: messageID,
			Date:      date,
			Chat: &tgbotapi.Chat{
				ID:   userID,
				Type: "private",
			},
			From: &tgbotapi.User{
				ID:        userID,
				UserName:  username,
				FirstName: username,
			},
			Document: &tgbotapi.Document{
				FileID:       "private_doc_" + strconv.Itoa(messageID),
				FileUniqueID: "private_doc_unique_" + strconv.Itoa(messageID),
				FileName:     "unsupported.bin",
			},
		},
	}
}

func fourLayerPrivateMediaGroupPhotoUpdate(updateID, messageID int, userID int64, username string, date int) tgbotapi.Update {
	upd := fourLayerPrivatePhotoUpdate(updateID, messageID, userID, username, "", date)
	upd.Message.MediaGroupID = "media-group-" + strconv.Itoa(messageID)
	return upd
}

func fourLayerHasReply(snapshot []fourLayerCapturedMessage, chatID int64, replyTo int, keyword string) bool {
	for _, item := range snapshot {
		if item.chatID != chatID || item.replyTo != replyTo {
			continue
		}
		if strings.Contains(item.text, keyword) {
			return true
		}
	}
	return false
}

func fourLayerHasAnyReply(snapshot []fourLayerCapturedMessage, chatID int64, replyTo int) bool {
	for _, item := range snapshot {
		if item.chatID == chatID && item.replyTo == replyTo {
			return true
		}
	}
	return false
}

func fourLayerFindReply(snapshot []fourLayerCapturedMessage, chatID int64, replyTo int, keyword string) (fourLayerCapturedMessage, bool) {
	for _, item := range snapshot {
		if item.chatID != chatID || item.replyTo != replyTo {
			continue
		}
		if strings.Contains(item.text, keyword) {
			return item, true
		}
	}
	return fourLayerCapturedMessage{}, false
}

func fourLayerHasCallbackAnswer(snapshot []fourLayerCapturedCallbackAnswer, queryID, keyword string) bool {
	for _, item := range snapshot {
		if item.queryID != queryID {
			continue
		}
		if strings.Contains(item.text, keyword) {
			return true
		}
	}
	return false
}
