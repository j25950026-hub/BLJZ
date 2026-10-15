package bot

import (
	"bytes"
	"fmt"
	htmltemplate "html/template"
	"strings"
	texttemplate "text/template"
)

const (
	templatePrefixHTML         = "[html]"
	templatePrefixMarkdown     = "[markdown]"
	templatePrefixMarkdownV2   = "[md]"
	telegramParseModeHTML      = "HTML"
	telegramParseModeMarkdownV = "MarkdownV2"
)

var telegramMarkdownEscaper = strings.NewReplacer(
	"\\", "\\\\",
	"_", `\_`,
	"*", `\*`,
	"[", `\[`,
	"]", `\]`,
	"(", `\(`,
	")", `\)`,
	"~", `\~`,
	"`", `\`+"`",
	">", `\>`,
	"#", `\#`,
	"+", `\+`,
	"-", `\-`,
	"=", `\=`,
	"|", `\|`,
	"{", `\{`,
	"}", `\}`,
	".", `\.`,
	"!", `\!`,
)

type renderedTemplate struct {
	Text      string
	ParseMode string
}

type messageTemplateData struct {
	ChatID   int64
	UserID   int64
	Username string
	Nickname string
	Text     string
	Now      string
}

func (d messageTemplateData) markdownSafe() messageTemplateData {
	d.Username = escapeTelegramMarkdownV2(d.Username)
	d.Nickname = escapeTelegramMarkdownV2(d.Nickname)
	d.Text = escapeTelegramMarkdownV2(d.Text)
	d.Now = escapeTelegramMarkdownV2(d.Now)
	return d
}

type templateRenderer struct{}

func newTemplateRenderer() *templateRenderer {
	return &templateRenderer{}
}

func (r *templateRenderer) RenderHTMLTemplate(rawTemplate string, data any) (string, error) {
	tpl, err := htmltemplate.New("message-html").Option("missingkey=zero").Parse(rawTemplate)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err := tpl.Execute(&out, data); err != nil {
		return "", err
	}
	return out.String(), nil
}

func (r *templateRenderer) RenderMarkdownTemplate(rawTemplate string, data any) (string, error) {
	tpl, err := texttemplate.New("message-md").Option("missingkey=zero").Parse(rawTemplate)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err := tpl.Execute(&out, escapeMarkdownTemplateData(data)); err != nil {
		return "", err
	}
	return out.String(), nil
}

func (r *templateRenderer) RenderMessageTemplate(rawTemplate string, data messageTemplateData) (renderedTemplate, error) {
	parseMode, templateBody, ok := parseTemplateModePrefix(rawTemplate)
	if !ok {
		return renderedTemplate{Text: rawTemplate}, nil
	}

	var (
		rendered string
		err      error
	)
	switch parseMode {
	case telegramParseModeHTML:
		rendered, err = r.RenderHTMLTemplate(templateBody, data)
	case telegramParseModeMarkdownV:
		rendered, err = r.RenderMarkdownTemplate(templateBody, data)
	default:
		return renderedTemplate{}, fmt.Errorf("unknown template parse mode %q", parseMode)
	}
	if err != nil {
		return renderedTemplate{}, err
	}
	return renderedTemplate{
		Text:      rendered,
		ParseMode: parseMode,
	}, nil
}

func parseTemplateModePrefix(rawTemplate string) (parseMode, body string, ok bool) {
	trimmed := strings.TrimLeft(rawTemplate, " \t\r\n")
	lowered := strings.ToLower(trimmed)

	switch {
	case strings.HasPrefix(lowered, templatePrefixHTML):
		return telegramParseModeHTML, strings.TrimSpace(trimmed[len(templatePrefixHTML):]), true
	case strings.HasPrefix(lowered, templatePrefixMarkdown):
		return telegramParseModeMarkdownV, strings.TrimSpace(trimmed[len(templatePrefixMarkdown):]), true
	case strings.HasPrefix(lowered, templatePrefixMarkdownV2):
		return telegramParseModeMarkdownV, strings.TrimSpace(trimmed[len(templatePrefixMarkdownV2):]), true
	default:
		return "", rawTemplate, false
	}
}

func escapeTelegramMarkdownV2(raw string) string {
	if raw == "" {
		return ""
	}
	return telegramMarkdownEscaper.Replace(raw)
}

func escapeMarkdownTemplateData(data any) any {
	switch typed := data.(type) {
	case messageTemplateData:
		return typed.markdownSafe()
	case *messageTemplateData:
		if typed == nil {
			return typed
		}
		normalized := typed.markdownSafe()
		return &normalized
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, value := range typed {
			text, ok := value.(string)
			if !ok {
				out[key] = value
				continue
			}
			out[key] = escapeTelegramMarkdownV2(text)
		}
		return out
	default:
		return data
	}
}
