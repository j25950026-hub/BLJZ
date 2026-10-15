package services

import "strings"

const CardSourceDomesticBankcard = "domestic_bankcard"

const domesticBankcardSource = CardSourceDomesticBankcard

func lookupDomesticBankcard(cardNo string) (domesticBankcardRecord, bool) {
	cardNo = strings.TrimSpace(cardNo)
	if cardNo == "" {
		return domesticBankcardRecord{}, false
	}

	var (
		best      domesticBankcardRecord
		bestFound bool
	)
	for _, record := range domesticBankcardRecords {
		if record.length != len(cardNo) {
			continue
		}
		if !strings.HasPrefix(cardNo, record.cardBin) {
			continue
		}
		if !bestFound || len(record.cardBin) > len(best.cardBin) {
			best = record
			bestFound = true
		}
	}
	return best, bestFound
}

func applyDomesticBankcard(cardNo string, info *CardInfo) bool {
	if info == nil {
		return false
	}
	record, ok := lookupDomesticBankcard(cardNo)
	if !ok {
		return false
	}

	info.CardNo = cardNo
	info.Valid = true
	info.BankCode = record.bankCode
	info.BankName = record.bankName
	info.Brand = "unionpay"
	info.CardType = record.cardType
	info.Country = "中国"
	info.Source = domesticBankcardSource
	info.Fallback = false
	return true
}
