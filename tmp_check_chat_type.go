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

	chatIDs := []int64{-5266232385, -1003829659428}
	for _, chatID := range chatIDs {
		g, err := database.GetGroup(chatID)
		if err != nil {
			fmt.Printf("chat=%d get_group_err=%v\n", chatID, err)
			continue
		}
		fmt.Printf("chat=%d title=%q group_type=%q owner=%d\n", g.ChatID, g.Title, g.GroupType, g.OwnerUserID)
	}
}
