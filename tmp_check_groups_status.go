//go:build ignore
// +build ignore

package main

import (
	"fmt"
	"log"

	"go-bot/internal/db"
)

func main() {
	database, err := db.NewDatabase("data/bot_go.db")
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()
	rows, err := database.ListAllGroups()
	if err != nil {
		log.Fatal(err)
	}
	for _, g := range rows {
		fmt.Printf("chat=%d title=%q owner=%d bookkeeping_enabled=%d day_cut=%q fee=%.2f rate=%.2f\n",
			g.ChatID, g.Title, g.OwnerUserID, g.BookkeepingEnabled, g.DayCutTime, g.FeeRate, g.CustomRate)
	}
}
