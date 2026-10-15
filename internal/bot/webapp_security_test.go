package bot

import (
	"strings"
	"testing"
	"time"

	"go-bot/internal/config"
)

func TestWebAppAuthRequiresExplicitSecret(t *testing.T) {
	now := time.Now()
	app := &App{
		cfg: config.Config{
			BotToken:    "bot-token",
			TGWebAppURL: "https://example.com/tg/webapp",
		},
		now: func() time.Time { return now },
	}

	if got := app.BuildWebAppURL(10001); strings.TrimSpace(got) != "" {
		t.Fatalf("缺少 TG_WEBAPP_SECRET 时不应生成 WebApp URL: %s", got)
	}

	exp := now.Add(webAppAuthTTL).Unix()
	sig := app.webAppSign(10001, exp)
	if err := app.VerifyWebAppAuth(10001, exp, sig); err == nil {
		t.Fatalf("缺少 TG_WEBAPP_SECRET 时不应通过 WebApp 鉴权")
	}
}
