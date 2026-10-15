package utils

import (
	"io"
	"regexp"
)

var (
	maskCardPattern         = regexp.MustCompile(`\b\d{12,19}\b`)
	maskERC20AddressPattern = regexp.MustCompile(`\b0[xX][0-9a-fA-F]{40}\b`)
	maskTRC20AddressPattern = regexp.MustCompile(`\bT[1-9A-HJ-NP-Za-km-z]{33}\b`)
)

func MaskSensitiveText(raw string) string {
	if raw == "" {
		return ""
	}
	masked := maskERC20AddressPattern.ReplaceAllStringFunc(raw, func(address string) string {
		return maskKeepEdges(address, 6, 4)
	})
	masked = maskTRC20AddressPattern.ReplaceAllStringFunc(masked, func(address string) string {
		return maskKeepEdges(address, 6, 4)
	})
	masked = maskCardPattern.ReplaceAllStringFunc(masked, func(cardNo string) string {
		if !LuhnCheck(cardNo) {
			return cardNo
		}
		return maskKeepEdges(cardNo, 6, 4)
	})
	return masked
}

func maskKeepEdges(raw string, prefix, suffix int) string {
	if len(raw) == 0 {
		return raw
	}
	if prefix < 0 {
		prefix = 0
	}
	if suffix < 0 {
		suffix = 0
	}
	if prefix+suffix >= len(raw) {
		return raw
	}
	masked := make([]byte, 0, len(raw))
	masked = append(masked, raw[:prefix]...)
	for i := 0; i < len(raw)-prefix-suffix; i++ {
		masked = append(masked, '*')
	}
	masked = append(masked, raw[len(raw)-suffix:]...)
	return string(masked)
}

type sensitiveMaskWriter struct {
	base io.Writer
}

func NewSensitiveMaskWriter(base io.Writer) io.Writer {
	if base == nil {
		return io.Discard
	}
	return &sensitiveMaskWriter{base: base}
}

func (w *sensitiveMaskWriter) Write(p []byte) (int, error) {
	if w == nil || w.base == nil {
		return len(p), nil
	}
	if _, err := w.base.Write([]byte(MaskSensitiveText(string(p)))); err != nil {
		return 0, err
	}
	return len(p), nil
}
