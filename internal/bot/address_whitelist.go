package bot

import (
	"fmt"
	"regexp"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"go-bot/internal/db"
	"go-bot/internal/utils"
)

var (
	exclusiveWhitelistTRC20Pattern    = regexp.MustCompile(`^T[1-9A-HJ-NP-Za-km-z]{33}$`)
	exclusiveWhitelistERC20Pattern    = regexp.MustCompile(`^0[xX][0-9a-fA-F]{40}$`)
	exclusiveWhitelistTRC20Finder     = regexp.MustCompile(`\bT[1-9A-HJ-NP-Za-km-z]{33}\b`)
	exclusiveWhitelistERC20Finder     = regexp.MustCompile(`\b0[xX][0-9a-fA-F]{40}\b`)
	addressWhitelistRiskSeverityLevel = "warn"
)

func (a *App) enforcePayoutAddressWhitelist(msg *tgbotapi.Message, g db.GroupConfig, parsed *utils.LedgerParsed) (bool, error) {
	if a != nil && !a.businessRiskControlsEnabled() {
		return false, nil
	}
	if parsed == nil || parsed.EntryType != "payout" {
		return false, nil
	}
	payoutAddressRaw := strings.TrimSpace(g.PayoutAddress)
	if payoutAddressRaw == "" {
		return false, nil
	}

	payoutAddress := normalizeExclusiveWhitelistAddress(payoutAddressRaw)
	if payoutAddress == "" {
		a.recordAddressWhitelistRisk(
			msg,
			"payout_address_whitelist_invalid",
			fmt.Sprintf("payout_address=%s", utils.MaskSensitiveText(payoutAddressRaw)),
		)
		return true, a.replyBookkeepingResult(msg, "ledger", "下发已阻断：下发地址配置无效，请先重新设置下发地址。", false)
	}

	candidates := extractExclusiveAddressCandidates(parsed.Note)
	if len(candidates) == 0 {
		a.recordAddressWhitelistRisk(
			msg,
			"payout_address_whitelist_missing",
			fmt.Sprintf(
				"payout_address=%s note=%s",
				payoutAddress,
				utils.MaskSensitiveText(strings.TrimSpace(parsed.Note)),
			),
		)
		return true, a.replyBookkeepingResult(
			msg,
			"ledger",
			fmt.Sprintf("下发已阻断：已启用下发地址校验，请在备注中填写下发地址（仅允许 %s）。", payoutAddressRaw),
			false,
		)
	}
	for _, candidate := range candidates {
		if candidate == payoutAddress {
			continue
		}
		a.recordAddressWhitelistRisk(
			msg,
			"payout_address_whitelist_blocked",
			fmt.Sprintf(
				"payout_address=%s detected_address=%s note=%s",
				payoutAddress,
				candidate,
				utils.MaskSensitiveText(strings.TrimSpace(parsed.Note)),
			),
		)
		return true, a.replyBookkeepingResult(
			msg,
			"ledger",
			fmt.Sprintf("下发已阻断：检测到非下发地址 %s，仅允许下发地址 %s。", candidate, payoutAddressRaw),
			false,
		)
	}
	return false, nil
}

func (a *App) enforceExclusiveAddressWhitelist(msg *tgbotapi.Message, g db.GroupConfig, parsed *utils.LedgerParsed) (bool, error) {
	if a != nil && !a.businessRiskControlsEnabled() {
		return false, nil
	}
	if parsed == nil || parsed.EntryType != "payout" {
		return false, nil
	}
	whitelistRaw := strings.TrimSpace(g.ExclusiveAddress)
	if whitelistRaw == "" {
		return false, nil
	}

	whitelist := normalizeExclusiveWhitelistAddress(whitelistRaw)
	if whitelist == "" {
		a.recordAddressWhitelistRisk(
			msg,
			"exclusive_address_whitelist_invalid",
			fmt.Sprintf("exclusive_address=%s", utils.MaskSensitiveText(whitelistRaw)),
		)
		return true, a.replyBookkeepingResult(msg, "ledger", "下发已阻断：专属下发地址配置无效，请先重新设置专属地址。", false)
	}

	candidates := extractExclusiveAddressCandidates(parsed.Note)
	if len(candidates) == 0 {
		a.recordAddressWhitelistRisk(
			msg,
			"exclusive_address_whitelist_missing",
			fmt.Sprintf(
				"exclusive_address=%s note=%s",
				whitelist,
				utils.MaskSensitiveText(strings.TrimSpace(parsed.Note)),
			),
		)
		return true, a.replyBookkeepingResult(
			msg,
			"ledger",
			fmt.Sprintf("下发已阻断：已启用专属地址白名单，请在备注中填写下发地址（仅允许 %s）。", whitelistRaw),
			false,
		)
	}
	for _, candidate := range candidates {
		if candidate == whitelist {
			continue
		}
		a.recordAddressWhitelistRisk(
			msg,
			"exclusive_address_whitelist_blocked",
			fmt.Sprintf(
				"exclusive_address=%s detected_address=%s note=%s",
				whitelist,
				candidate,
				utils.MaskSensitiveText(strings.TrimSpace(parsed.Note)),
			),
		)
		return true, a.replyBookkeepingResult(
			msg,
			"ledger",
			fmt.Sprintf("下发已阻断：检测到非白名单地址 %s，仅允许专属地址 %s。", candidate, whitelistRaw),
			false,
		)
	}
	return false, nil
}

func (a *App) recordAddressWhitelistRisk(msg *tgbotapi.Message, eventType, detail string) {
	if a == nil || a.db == nil || msg == nil || msg.Chat == nil {
		return
	}
	userID := int64(0)
	if msg.From != nil {
		userID = msg.From.ID
	}
	_ = a.db.AddRiskEvent(msg.Chat.ID, msg.MessageID, userID, eventType, addressWhitelistRiskSeverityLevel, strings.TrimSpace(detail))
}

func extractExclusiveAddressCandidates(note string) []string {
	text := strings.TrimSpace(note)
	if text == "" {
		return nil
	}
	seen := make(map[string]struct{})
	candidates := make([]string, 0, 2)
	collect := func(raw string) {
		normalized := normalizeExclusiveWhitelistAddress(raw)
		if normalized == "" {
			return
		}
		if _, exists := seen[normalized]; exists {
			return
		}
		seen[normalized] = struct{}{}
		candidates = append(candidates, normalized)
	}
	for _, address := range exclusiveWhitelistERC20Finder.FindAllString(text, -1) {
		collect(address)
	}
	for _, address := range exclusiveWhitelistTRC20Finder.FindAllString(text, -1) {
		collect(address)
	}
	return candidates
}

func normalizeExclusiveWhitelistAddress(address string) string {
	addr := strings.TrimSpace(address)
	if addr == "" {
		return ""
	}
	switch {
	case exclusiveWhitelistERC20Pattern.MatchString(addr):
		return strings.ToLower(addr)
	case exclusiveWhitelistTRC20Pattern.MatchString(addr):
		return addr
	default:
		return ""
	}
}
