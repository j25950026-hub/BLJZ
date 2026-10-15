package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const defaultTempDataStagingTTL = 5 * time.Minute

func (d *Database) StageTempData(chatID, userID int64, stageKey string, payload map[string]string, ttl time.Duration) error {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.StageTempData(chatID, userID, stageKey, payload, ttl)
	}
	normalizedKey := strings.TrimSpace(stageKey)
	if chatID == 0 || userID == 0 || normalizedKey == "" {
		return fmt.Errorf("chat_id、user_id、stage_key 不能为空")
	}
	rawPayload, err := json.Marshal(normalizeTempDataPayload(payload))
	if err != nil {
		return err
	}
	encryptedPayload, err := d.encryptSensitiveFieldValue(string(rawPayload))
	if err != nil {
		return err
	}
	expireAtUnix := resolveTempDataStageExpireAtUnix(ttl, time.Now().UTC())
	_, err = d.raw.Exec(`
INSERT INTO temp_data_staging (chat_id, user_id, stage_key, payload, expire_at_unix, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(chat_id, user_id, stage_key) DO UPDATE SET
payload = excluded.payload,
expire_at_unix = excluded.expire_at_unix,
updated_at = excluded.updated_at`,
		chatID, userID, normalizedKey, encryptedPayload, expireAtUnix, nowUTCText(),
	)
	return err
}

func (d *Database) GetTempData(chatID, userID int64, stageKey string) (map[string]string, bool, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.GetTempData(chatID, userID, stageKey)
	}
	normalizedKey := strings.TrimSpace(stageKey)
	if chatID == 0 || userID == 0 || normalizedKey == "" {
		return map[string]string{}, false, nil
	}
	row := d.raw.QueryRow(`SELECT payload, expire_at_unix FROM temp_data_staging WHERE chat_id = ? AND user_id = ? AND stage_key = ?`, chatID, userID, normalizedKey)
	var encryptedPayload string
	var expireAtUnix int64
	if err := row.Scan(&encryptedPayload, &expireAtUnix); err != nil {
		if err == sql.ErrNoRows {
			return map[string]string{}, false, nil
		}
		return nil, false, err
	}
	if expireAtUnix > 0 && expireAtUnix <= time.Now().UTC().Unix() {
		_ = d.ClearTempData(chatID, userID, normalizedKey)
		return map[string]string{}, false, nil
	}
	decryptedPayload, err := d.decryptSensitiveFieldValue(encryptedPayload)
	if err != nil {
		return nil, false, err
	}
	if strings.TrimSpace(decryptedPayload) == "" {
		return map[string]string{}, true, nil
	}
	var decoded map[string]string
	if err := json.Unmarshal([]byte(decryptedPayload), &decoded); err != nil {
		_ = d.ClearTempData(chatID, userID, normalizedKey)
		return nil, false, err
	}
	return normalizeTempDataPayload(decoded), true, nil
}

func (d *Database) PopTempData(chatID, userID int64, stageKey string) (map[string]string, bool, error) {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.PopTempData(chatID, userID, stageKey)
	}
	payload, ok, err := d.GetTempData(chatID, userID, stageKey)
	if err != nil || !ok {
		return payload, ok, err
	}
	if err := d.ClearTempData(chatID, userID, stageKey); err != nil {
		return nil, false, err
	}
	return payload, true, nil
}

func (d *Database) ClearTempData(chatID, userID int64, stageKey string) error {
	if routed := d.databaseForChat(chatID); routed != d {
		return routed.ClearTempData(chatID, userID, stageKey)
	}
	normalizedKey := strings.TrimSpace(stageKey)
	if chatID == 0 || userID == 0 || normalizedKey == "" {
		return nil
	}
	_, err := d.raw.Exec(`DELETE FROM temp_data_staging WHERE chat_id = ? AND user_id = ? AND stage_key = ?`, chatID, userID, normalizedKey)
	return err
}

func normalizeTempDataPayload(payload map[string]string) map[string]string {
	if len(payload) == 0 {
		return map[string]string{}
	}
	normalized := make(map[string]string, len(payload))
	for key, value := range payload {
		normalizedKey := strings.TrimSpace(key)
		if normalizedKey == "" {
			continue
		}
		normalized[normalizedKey] = value
	}
	if len(normalized) == 0 {
		return map[string]string{}
	}
	return normalized
}

func resolveTempDataStageExpireAtUnix(ttl time.Duration, now time.Time) int64 {
	if ttl == 0 {
		ttl = defaultTempDataStagingTTL
	}
	return now.Add(ttl).UTC().Unix()
}
