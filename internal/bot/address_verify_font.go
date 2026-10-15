package bot

import (
	_ "embed"
	"fmt"
	"os"
	"strings"
	"sync"

	"golang.org/x/image/font/opentype"
)

const addressVerifyRequiredGlyphs = "本次验证地址备注信息码时间靓仔无语TNo2PhgN4DMmtyvb5chhY6WJyuQSAhaA3B89532026-07-12 13:40:51："

//go:embed assets/NotoSansCJK-Bold.ttc
var embeddedAddressVerifyFont []byte

var (
	addressVerifyFontOnce  sync.Once
	addressVerifyFontCache *opentype.Font
	addressVerifyFontErr   error
)

func loadAddressVerifyFont() (*opentype.Font, error) {
	addressVerifyFontOnce.Do(func() {
		addressVerifyFontCache, addressVerifyFontErr = loadAddressVerifyFontUncached()
	})
	return addressVerifyFontCache, addressVerifyFontErr
}

func loadAddressVerifyFontUncached() (*opentype.Font, error) {
	if tt, err := parseAddressVerifyFont(embeddedAddressVerifyFont, addressVerifyRequiredGlyphs); err == nil {
		return tt, nil
	}
	for _, path := range addressVerifyFontCandidatePaths() {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		tt, err := parseAddressVerifyFont(data, addressVerifyRequiredGlyphs)
		if err == nil {
			return tt, nil
		}
	}
	return nil, fmt.Errorf("未找到支持地址验证图片中文标注的字体")
}

func addressVerifyFontCandidatePaths() []string {
	return []string{
		"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/opentype/noto/NotoSansCJK-Bold.ttc",
		"/usr/share/fonts/truetype/droid/DroidSansFallbackFull.ttf",
		"/usr/share/fonts/truetype/wqy/wqy-microhei.ttc",
		"/usr/share/fonts/truetype/wqy/wqy-zenhei.ttc",
	}
}

func parseAddressVerifyFont(data []byte, requiredGlyphs string) (*opentype.Font, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("字体数据为空")
	}
	collection, err := opentype.ParseCollection(data)
	if err != nil {
		return nil, err
	}
	for i := 0; i < collection.NumFonts(); i++ {
		tt, err := collection.Font(i)
		if err != nil {
			continue
		}
		if addressVerifyFontSupportsText(tt, requiredGlyphs) {
			return tt, nil
		}
	}
	return nil, fmt.Errorf("字体缺少地址验证图片中文标注字形")
}

func addressVerifyFontSupportsText(tt *opentype.Font, text string) bool {
	if tt == nil {
		return false
	}
	for _, r := range strings.TrimSpace(text) {
		glyph, err := tt.GlyphIndex(nil, r)
		if err != nil || glyph == 0 {
			return false
		}
	}
	return true
}
