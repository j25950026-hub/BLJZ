//go:build ignore
// +build ignore

package main

import (
	"database/sql"
	"fmt"
	"log"

	_ "modernc.org/sqlite"
)

func main() {
	db, err := sql.Open("sqlite", "data/bot_go.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	rows, err := db.Query(`
SELECT chat_id,
       COUNT(1) AS total,
       SUM(CASE WHEN entry_type='deposit' THEN 1 ELSE 0 END) AS dep,
       SUM(CASE WHEN entry_type='payout' THEN 1 ELSE 0 END) AS pay,
       MIN(created_at) AS min_ct,
       MAX(created_at) AS max_ct,
       SUM(CASE WHEN COALESCE(track_message_link,'')<>'' THEN 1 ELSE 0 END) AS track_cnt,
       SUM(CASE WHEN message_id<>0 THEN 1 ELSE 0 END) AS msg_cnt
FROM ledger_entries
GROUP BY chat_id
ORDER BY max_ct DESC`)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	for rows.Next() {
		var chatID int64
		var total, dep, pay, trackCnt, msgCnt int
		var minCT, maxCT string
		if err := rows.Scan(&chatID, &total, &dep, &pay, &minCT, &maxCT, &trackCnt, &msgCnt); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("chat=%d total=%d dep=%d pay=%d track=%d msg=%d min=%s max=%s\n", chatID, total, dep, pay, trackCnt, msgCnt, minCT, maxCT)
	}
	if err := rows.Err(); err != nil {
		log.Fatal(err)
	}
}
