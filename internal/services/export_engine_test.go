package services

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"go-bot/internal/db"
)

func TestBuildLedgerExports(t *testing.T) {
	engine := NewExportEngine()
	files, err := engine.BuildLedgerExports([]db.LedgerEntry{
		{
			ID:          1,
			ChatID:      -100435001,
			EntryType:   "deposit",
			Amount:      120,
			Currency:    "CNY",
			FinalAmount: 120,
			Rate:        1,
			FeeRate:     0,
			Nickname:    "Alice",
			CreatedAt:   "2026-02-19T01:23:45",
			Note:        "sample deposit",
		},
		{
			ID:          2,
			ChatID:      -100435001,
			EntryType:   "payout",
			Amount:      20,
			Currency:    "CNY",
			FinalAmount: 20,
			Rate:        1,
			FeeRate:     0,
			Nickname:    "Bob",
			CreatedAt:   "2026-02-19T02:34:56",
			Note:        "sample payout",
		},
	}, LedgerExportOptions{
		ChatID:      -100435001,
		GroupTitle:  "控制点4.3.5样例群",
		DayCutTime:  "04:00:00",
		Timezone:    "Asia/Shanghai",
		GeneratedAt: time.Date(2026, 2, 19, 12, 30, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("生成导出文件失败: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("导出文件数量错误: %d", len(files))
	}

	var xlsxFound bool
	var pdfFound bool
	for _, file := range files {
		if strings.HasSuffix(file.Name, ".xlsx") {
			xlsxFound = true
			if !bytes.HasPrefix(file.Content, []byte("PK\x03\x04")) {
				t.Fatalf("Excel 文件头错误")
			}
			continue
		}
		if strings.HasSuffix(file.Name, ".pdf") {
			pdfFound = true
			if !bytes.HasPrefix(file.Content, []byte("%PDF-1.4")) {
				t.Fatalf("PDF 文件头错误")
			}
		}
	}
	if !xlsxFound || !pdfFound {
		t.Fatalf("导出文件类型不完整: xlsx=%v pdf=%v", xlsxFound, pdfFound)
	}
}
