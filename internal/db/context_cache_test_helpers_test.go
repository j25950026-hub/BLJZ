package db

import "github.com/alicebob/miniredis/v2"

func redisStringKeyExists(mini *miniredis.Miniredis, key string) bool {
	if mini == nil || key == "" {
		return false
	}
	_, err := mini.Get(key)
	return err == nil
}
