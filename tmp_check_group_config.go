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

  chatID := int64(-1003829659428)
  g, err := database.GetGroup(chatID)
  if err != nil {
    log.Fatal(err)
  }
  testUsers := []int64{6253397539, 8468417665}
  fmt.Printf("chat_id=%d\nowner_user_id=%d\npayout_address=%q\nexclusive_address=%q\n", g.ChatID, g.OwnerUserID, g.PayoutAddress, g.ExclusiveAddress)
  for _, uid := range testUsers {
    fmt.Printf("user=%d privileged=%v\n", uid, database.IsPrivileged(chatID, uid))
  }
}
