package services

import (
	"errors"
	"strings"
)

var ErrBranchLookupNotConfigured = errors.New("支行联行号查询接口未配置")

// LookupBranchCodeForCard returns a structured exact branch result for a card.
// It deliberately does not fall back to a bank head-office CNAPS code.
func (s *CardService) LookupBranchCodeForCard(info CardInfo) (*BranchLookupResult, error) {
	if s == nil || strings.TrimSpace(s.branchAPI.URL) == "" {
		return nil, ErrBranchLookupNotConfigured
	}
	return s.queryBranchFromProvider(info)
}
