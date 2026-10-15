package services

import (
	"bytes"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/url"
	"regexp"
	"strings"

	"github.com/liyue201/goqr"
)

var (
	erc20CandidatePattern = regexp.MustCompile(`(?i)0x[a-f0-9]{40}`)
	trc20CandidatePattern = regexp.MustCompile(`T[1-9A-HJ-NP-Za-km-z]{33}`)
)

type QRAddressDetection struct {
	Payload    string
	Address    string
	Validation AddressValidation
}

func (s *AddressService) ScanQRCodeImageForAddresses(imageData []byte) ([]QRAddressDetection, error) {
	if len(imageData) == 0 {
		return nil, errors.New("图片内容不能为空")
	}
	img, _, err := image.Decode(bytes.NewReader(imageData))
	if err != nil {
		return nil, err
	}
	codes, err := goqr.Recognize(img)
	if err != nil {
		return nil, err
	}
	if len(codes) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{})
	detections := make([]QRAddressDetection, 0, len(codes))
	for _, code := range codes {
		payload := strings.TrimSpace(string(code.Payload))
		if payload == "" {
			continue
		}
		for _, candidate := range extractAddressCandidatesFromPayload(payload) {
			network := detectAddressNetwork(candidate)
			if network == "" {
				continue
			}
			normalized := normalizeAddressForLookup(network, candidate)
			if normalized == "" {
				continue
			}
			key := blacklistLookupKey(network, normalized)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			detections = append(detections, QRAddressDetection{
				Payload:    payload,
				Address:    candidate,
				Validation: s.Validate(candidate),
			})
		}
	}
	if len(detections) == 0 {
		return nil, nil
	}
	return detections, nil
}

func extractAddressCandidatesFromPayload(payload string) []string {
	variants := payloadVariants(payload)
	if len(variants) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	out := make([]string, 0, 4)
	for _, item := range variants {
		matches := append(findAddressMatches(item, erc20CandidatePattern), findAddressMatches(item, trc20CandidatePattern)...)
		for _, candidate := range matches {
			network := detectAddressNetwork(candidate)
			if network == "" {
				continue
			}
			normalized := normalizeAddressForLookup(network, candidate)
			if normalized == "" {
				continue
			}
			key := blacklistLookupKey(network, normalized)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, candidate)
		}
	}
	return out
}

func payloadVariants(payload string) []string {
	raw := strings.TrimSpace(payload)
	if raw == "" {
		return nil
	}
	variants := []string{raw}
	appendVariant := func(v string) {
		candidate := strings.TrimSpace(v)
		if candidate == "" {
			return
		}
		for _, current := range variants {
			if current == candidate {
				return
			}
		}
		variants = append(variants, candidate)
	}
	if unescaped, err := url.QueryUnescape(raw); err == nil {
		appendVariant(unescaped)
	}
	if unescaped, err := url.PathUnescape(raw); err == nil {
		appendVariant(unescaped)
	}
	return variants
}

func findAddressMatches(payload string, pattern *regexp.Regexp) []string {
	if strings.TrimSpace(payload) == "" || pattern == nil {
		return nil
	}
	indexes := pattern.FindAllStringIndex(payload, -1)
	if len(indexes) == 0 {
		return nil
	}
	out := make([]string, 0, len(indexes))
	for _, idx := range indexes {
		start, end := idx[0], idx[1]
		if !isAddressBoundary(payload, start, end) {
			continue
		}
		out = append(out, payload[start:end])
	}
	return out
}

func isAddressBoundary(payload string, start, end int) bool {
	if start > 0 && isAddressByte(payload[start-1]) {
		return false
	}
	if end < len(payload) && isAddressByte(payload[end]) {
		return false
	}
	return true
}

func isAddressByte(ch byte) bool {
	if ch >= '0' && ch <= '9' {
		return true
	}
	if ch >= 'a' && ch <= 'z' {
		return true
	}
	if ch >= 'A' && ch <= 'Z' {
		return true
	}
	return false
}
