//go:build ignore
// +build ignore

package main

import (
	"fmt"
	"log"
	"time"

	"go-bot/internal/db"
)

func main() {
	database, err := db.NewDatabase("data/bot_go.db")
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()

	chatID := int64(-1003829659428)
	startAt := time.Now().Add(-48 * time.Hour).UTC().Format("2006-01-02T15:04:05")
	rows, err := database.ListLedgerSince(chatID, startAt)
	if err != nil {
		log.Fatal(err)
	}
	if len(rows) == 0 {
		fmt.Println("rows=0")
		return
	}
	fmt.Printf("rows=%d\n", len(rows))
	begin := 0
	if len(rows) > 20 {
		begin = len(rows) - 20
	}
	for i := begin; i < len(rows); i++ {
		r := rows[i]
		fmt.Printf("id=%d type=%s chat=%d msg=%d amount=%.2f curr=%s fee=%.2f rate=%.2f final=%.2f track=%q nick=%q created=%s\n",
			r.ID,
			r.EntryType,
			r.ChatID,
			r.MessageID,
			r.Amount,
			r.Currency,
			r.FeeRate,
			r.Rate,
			r.FinalAmount,
			r.TrackLink,
			r.Nickname,
			r.CreatedAt,
		)
	}
}
