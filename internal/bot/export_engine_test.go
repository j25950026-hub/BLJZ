package bot

import (
	"bytes"
	"context"
	"io"
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
	"go-bot/internal/services"
)

type capturedExportDocument struct {
	chatID   int64
	filename string
	payload  []byte
}

func TestControlPoint435ExportEngine(t *testing.T) {
	t.Run("控制点 4.3.5 功能验证", func(t *testing.T) {
		database, err := db.NewDatabase(filepath.Join(t.TempDir(), "export-engine.db"))
		if err != nil {
			t.Fatalf("初始化数据库失败: %v", err)
		}
		defer database.Close()

		const (
			chatID int64 = -100435001
			userID int64 = 43500101
		)
		if _, err := database.EnsureGroup(chatID, "控制点4.3.5测试群"); err != nil {
			t.Fatalf("初始化群配置失败: %v", err)
		}
		if err := database.SetOwner(chatID, userID); err != nil {
			t.Fatalf("设置群权限人失败: %v", err)
		}
		group, err := database.GetGroup(chatID)
		if err != nil {
			t.Fatalf("读取群配置失败: %v", err)
		}

		samples := []db.LedgerEntryInput{
			{
				ChatID:           chatID,
				MessageID:        435010,
				UserID:           userID,
				Username:         "cp435",
				Nickname:         "CP435",
				EntryType:        "deposit",
				Amount:           120,
				Currency:         "CNY",
				BaseAmount:       120,
				FeeRate:          0,
				Rate:             1,
				FinalAmount:      120,
				Note:             "control-point-435-deposit",
				TrackMessageLink: "tg://msg/435010",
			},
			{
				ChatID:           chatID,
				MessageID:        435011,
				UserID:           userID,
				Username:         "cp435",
				Nickname:         "CP435",
				EntryType:        "payout",
				Amount:           30,
				Currency:         "CNY",
				BaseAmount:       30,
				FeeRate:          0,
				Rate:             1,
				FinalAmount:      30,
				Note:             "control-point-435-payout",
				TrackMessageLink: "tg://msg/435011",
			},
		}
		for _, sample := range samples {
			result, err := database.ApplyLedgerEntry(sample)
			if err != nil {
				t.Fatalf("写入导出样本失败: %v", err)
			}
			if result.Duplicate || result.EntryID == 0 {
				t.Fatalf("写入导出样本结果异常: %+v", result)
			}
		}

		var (
			mu       sync.Mutex
			captured []capturedExportDocument
		)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if strings.HasSuffix(strings.TrimSpace(r.URL.Path), "/sendDocument") {
				if err := r.ParseMultipartForm(10 << 20); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"ok":false,"description":"invalid multipart form"}`))
					return
				}
				chatIDText := strings.TrimSpace(r.FormValue("chat_id"))
				chatValue, _ := strconv.ParseInt(chatIDText, 10, 64)
				file, header, err := r.FormFile("document")
				if err != nil {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"ok":false,"description":"document missing"}`))
					return
				}
				payload, _ := io.ReadAll(file)
				_ = file.Close()

				mu.Lock()
				captured = append(captured, capturedExportDocument{
					chatID:   chatValue,
					filename: strings.TrimSpace(header.Filename),
					payload:  payload,
				})
				mu.Unlock()
				_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":435099}}`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
		}))
		defer server.Close()

		api := &tgbotapi.BotAPI{
			Token:  "export-engine-test-token",
			Client: server.Client(),
		}
		api.SetAPIEndpoint(server.URL + "/bot%s/%s")
		loc, err := time.LoadLocation("Asia/Shanghai")
		if err != nil {
			t.Fatalf("加载时区失败: %v", err)
		}
		app := &App{
			cfg: config.Config{
				Timezone: "Asia/Shanghai",
			},
			db:           database,
			api:          api,
			loc:          loc,
			now:          func() time.Time { return time.Date(2026, 2, 19, 9, 30, 0, 0, time.UTC) },
			exportEngine: services.NewExportEngine(),
		}
		message := &tgbotapi.Message{
			MessageID: 435199,
			Chat: &tgbotapi.Chat{
				ID:    chatID,
				Type:  "supergroup",
				Title: "控制点4.3.5测试群",
			},
			From: &tgbotapi.User{
				ID:       userID,
				UserName: "cp435",
			},
		}

		if err := app.dispatchGroupMessage(context.Background(), message, group, "导出账单"); err != nil {
			t.Fatalf("导出命令执行失败: %v", err)
		}

		mu.Lock()
		docs := append([]capturedExportDocument(nil), captured...)
		mu.Unlock()
		if len(docs) != 2 {
			t.Fatalf("应发送 2 个导出文件，实际=%d", len(docs))
		}

		hasExcel := false
		hasPDF := false
		for _, item := range docs {
			if item.chatID != chatID {
				t.Fatalf("导出发送目标错误: want=%d got=%d", chatID, item.chatID)
			}
			if strings.HasSuffix(item.filename, ".xlsx") {
				hasExcel = true
				if !bytes.HasPrefix(item.payload, []byte("PK\x03\x04")) {
					t.Fatalf("Excel 导出文件头错误")
				}
				continue
			}
			if strings.HasSuffix(item.filename, ".pdf") {
				hasPDF = true
				if !bytes.HasPrefix(item.payload, []byte("%PDF-1.4")) {
					t.Fatalf("PDF 导出文件头错误")
				}
				continue
			}
			t.Fatalf("发送了未知导出文件: %s", item.filename)
		}
		if !hasExcel || !hasPDF {
			t.Fatalf("导出文件类型不完整: excel=%v pdf=%v", hasExcel, hasPDF)
		}
	})
}
