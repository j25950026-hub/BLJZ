package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

func LoadDotEnv(path string) {
	if _, err := os.Stat(path); err != nil {
		return
	}
	if err := godotenv.Overload(path); err != nil {
		log.Printf("加载 .env 失败: %v", err)
	}
}
