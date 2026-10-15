package services

import "strings"

const (
	screeningScopeOFAC = "OFAC制裁名单"
	screeningScopeScam = "已知诈骗名单"
)

type blacklistEntry struct {
	network string
	address string
	scope   string
	label   string
}

type blacklistHit struct {
	scope string
	label string
}

type addressBlacklist struct {
	entries map[string]blacklistHit
}

var defaultScreeningBlacklist = newAddressBlacklist([]blacklistEntry{
	{
		network: "ERC20",
		address: "0xd90e2f925da726b50c4ed8d0fb90ad053324f31b",
		scope:   screeningScopeOFAC,
		label:   "Tornado Cash",
	},
	{
		network: "ERC20",
		address: "0xd691f27f38b395864ea86cfc7253969b409c362d",
		scope:   screeningScopeOFAC,
		label:   "Tornado Cash",
	},
	{
		network: "ERC20",
		address: "0x1111111111111111111111111111111111111111",
		scope:   screeningScopeScam,
		label:   "已知诈骗样本#ERC20-001",
	},
	{
		network: "TRC20",
		address: "T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb",
		scope:   screeningScopeScam,
		label:   "已知诈骗样本#TRC20-001",
	},
})

func defaultAddressBlacklist() *addressBlacklist {
	return defaultScreeningBlacklist
}

func (b *addressBlacklist) Match(network, address string) (blacklistHit, bool) {
	if b == nil {
		return blacklistHit{}, false
	}
	normalizedAddress := normalizeAddressForLookup(network, address)
	if normalizedAddress == "" {
		return blacklistHit{}, false
	}
	hit, ok := b.entries[blacklistLookupKey(network, normalizedAddress)]
	return hit, ok
}

func newAddressBlacklist(entries []blacklistEntry) *addressBlacklist {
	if len(entries) == 0 {
		return nil
	}
	blacklist := &addressBlacklist{
		entries: make(map[string]blacklistHit, len(entries)),
	}
	for _, entry := range entries {
		network := strings.ToUpper(strings.TrimSpace(entry.network))
		addr := normalizeAddressForLookup(network, entry.address)
		scope := strings.TrimSpace(entry.scope)
		if network == "" || addr == "" || scope == "" {
			continue
		}
		blacklist.entries[blacklistLookupKey(network, addr)] = blacklistHit{
			scope: scope,
			label: strings.TrimSpace(entry.label),
		}
	}
	if len(blacklist.entries) == 0 {
		return nil
	}
	return blacklist
}

func blacklistLookupKey(network, address string) string {
	return strings.ToUpper(strings.TrimSpace(network)) + ":" + strings.TrimSpace(address)
}

func normalizeAddressForLookup(network, address string) string {
	cleanNetwork := strings.ToUpper(strings.TrimSpace(network))
	cleanAddress := strings.TrimSpace(address)
	if cleanNetwork == "ERC20" {
		return strings.ToLower(cleanAddress)
	}
	return cleanAddress
}
