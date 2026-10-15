package bot

import (
	"strings"
	"testing"
)

func TestBotBrandingUsesChaJiJiQiRen(t *testing.T) {
	oldBrand := "查记" + "管家"
	checks := map[string]string{
		"private intro":     privateIntroText(),
		"usage guide":       privateUsageGuideText(),
		"group manage":      privateGroupManageText(),
		"invite welcome one": botInviteWelcomeFirstMessage,
	}

	for name, text := range checks {
		if !strings.Contains(text, "查记机器人") {
			t.Fatalf("%s 应使用新品牌名：%q", name, text)
		}
		if strings.Contains(text, oldBrand) {
			t.Fatalf("%s 不应再包含旧品牌名：%q", name, text)
		}
	}

	welcomeMessages := buildBotInviteWelcomeMessages("测试群")
	if len(welcomeMessages) == 0 {
		t.Fatalf("欢迎语不应为空")
	}
	for i, text := range welcomeMessages {
		if !strings.Contains(text, "查记机器人") {
			t.Fatalf("欢迎语第 %d 条应使用新品牌名：%q", i, text)
		}
		if strings.Contains(text, oldBrand) {
			t.Fatalf("欢迎语第 %d 条不应包含旧品牌名：%q", i, text)
		}
	}
}
