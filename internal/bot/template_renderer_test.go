package bot

import (
	"strings"
	"testing"
)

func TestControlPoint254TemplateRenderer(t *testing.T) {
	t.Run("控制点 2.5.4 功能验证", func(t *testing.T) {
		renderer := newTemplateRenderer()

		htmlRendered, err := renderer.RenderMessageTemplate(
			`[html]<a href="tg://user?id={{.UserID}}">@{{.Username}}</a>`,
			messageTemplateData{
				UserID:   9527,
				Username: `alice<&>"`,
			},
		)
		if err != nil {
			t.Fatalf("HTML 模板渲染失败: %v", err)
		}
		if htmlRendered.ParseMode != telegramParseModeHTML {
			t.Fatalf("HTML 模板 ParseMode 错误: %q", htmlRendered.ParseMode)
		}
		if !strings.Contains(htmlRendered.Text, `@alice&lt;&amp;&gt;&#34;`) {
			t.Fatalf("HTML 模板未完成变量转义: %q", htmlRendered.Text)
		}

		markdownRendered, err := renderer.RenderMessageTemplate(
			`[md]*{{.Nickname}}* {{.Text}}`,
			messageTemplateData{
				Nickname: "风控_机器人",
				Text:     "金额+100 (USDT)!",
			},
		)
		if err != nil {
			t.Fatalf("Markdown 模板渲染失败: %v", err)
		}
		if markdownRendered.ParseMode != telegramParseModeMarkdownV {
			t.Fatalf("Markdown 模板 ParseMode 错误: %q", markdownRendered.ParseMode)
		}
		if !strings.Contains(markdownRendered.Text, `*风控\_机器人*`) {
			t.Fatalf("Markdown 模板昵称转义失败: %q", markdownRendered.Text)
		}
		if !strings.Contains(markdownRendered.Text, `金额\+100 \(USDT\)\!`) {
			t.Fatalf("Markdown 模板正文转义失败: %q", markdownRendered.Text)
		}

		plainRendered, err := renderer.RenderMessageTemplate(
			`普通文本模板 {{.Nickname}}`,
			messageTemplateData{Nickname: "不会替换"},
		)
		if err != nil {
			t.Fatalf("纯文本模板不应报错: %v", err)
		}
		if plainRendered.ParseMode != "" {
			t.Fatalf("纯文本模板 ParseMode 应为空: %q", plainRendered.ParseMode)
		}
		if plainRendered.Text != `普通文本模板 {{.Nickname}}` {
			t.Fatalf("纯文本模板不应触发渲染: %q", plainRendered.Text)
		}
	})
}

func TestTemplateRendererInvalidTemplate(t *testing.T) {
	renderer := newTemplateRenderer()
	_, err := renderer.RenderMessageTemplate(`[markdown]{{.Nickname`, messageTemplateData{Nickname: "x"})
	if err == nil {
		t.Fatalf("非法模板应返回错误")
	}
}
