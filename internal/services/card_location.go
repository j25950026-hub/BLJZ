package services

import (
	"regexp"
	"strings"
)

var cardLocationPattern = regexp.MustCompile(`^(.+?(?:省|自治区|特别行政区|市))[[:space:]/,，、-]*(.+)$`)

func normalizeCardLocation(info *CardInfo) {
	if info == nil {
		return
	}
	province, city := splitCardAddress(info.Address)
	if strings.TrimSpace(info.Province) == "" {
		info.Province = province
	}
	if strings.TrimSpace(info.City) == "" {
		info.City = city
	}
}

func splitCardAddress(address string) (string, string) {
	address = strings.Join(strings.Fields(strings.TrimSpace(address)), " ")
	if address == "" {
		return "", ""
	}
	match := cardLocationPattern.FindStringSubmatch(address)
	if len(match) != 3 {
		return "", ""
	}
	province := strings.TrimSpace(match[1])
	city := strings.TrimSpace(match[2])
	if province == "" || city == "" {
		return "", ""
	}
	return province, city
}
