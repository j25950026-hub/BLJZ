package accounting

import (
	"fmt"
	"strings"
)

type AccountCategory string

const (
	AccountCategoryAsset     AccountCategory = "asset"
	AccountCategoryLiability AccountCategory = "liability"
	AccountCategoryPnL       AccountCategory = "pnl"
)

const (
	AccountCurrencyUSDT = "USDT"
)

const (
	AccountCodeAssetUSDT            = "1001"
	AccountCodeLiabilityUserBalance = "2001"
	AccountCodePnLFeeIncome         = "5001"
)

const (
	AccountNameAssetUSDT            = "USDT"
	AccountNameLiabilityUserBalance = "用户余额"
	AccountNamePnLFeeIncome         = "手续费收入"
)

type Account struct {
	Code        string
	Name        string
	Category    AccountCategory
	Currency    string
	Description string
}

var defaultChartOfAccounts = []Account{
	{
		Code:        AccountCodeAssetUSDT,
		Name:        AccountNameAssetUSDT,
		Category:    AccountCategoryAsset,
		Currency:    AccountCurrencyUSDT,
		Description: "Platform-held USDT balance.",
	},
	{
		Code:        AccountCodeLiabilityUserBalance,
		Name:        AccountNameLiabilityUserBalance,
		Category:    AccountCategoryLiability,
		Currency:    AccountCurrencyUSDT,
		Description: "Funds owed to users.",
	},
	{
		Code:        AccountCodePnLFeeIncome,
		Name:        AccountNamePnLFeeIncome,
		Category:    AccountCategoryPnL,
		Currency:    AccountCurrencyUSDT,
		Description: "Trading and settlement fee income.",
	},
}

var supportedCategories = []AccountCategory{
	AccountCategoryAsset,
	AccountCategoryLiability,
	AccountCategoryPnL,
}

var categoryCodePrefixes = map[AccountCategory]string{
	AccountCategoryAsset:     "1",
	AccountCategoryLiability: "2",
	AccountCategoryPnL:       "5",
}

var requiredCoreAccountCodes = []string{
	AccountCodeAssetUSDT,
	AccountCodeLiabilityUserBalance,
	AccountCodePnLFeeIncome,
}

var expectedCoreAccountsByCode = map[string]Account{
	AccountCodeAssetUSDT: {
		Name:     AccountNameAssetUSDT,
		Category: AccountCategoryAsset,
		Currency: AccountCurrencyUSDT,
	},
	AccountCodeLiabilityUserBalance: {
		Name:     AccountNameLiabilityUserBalance,
		Category: AccountCategoryLiability,
		Currency: AccountCurrencyUSDT,
	},
	AccountCodePnLFeeIncome: {
		Name:     AccountNamePnLFeeIncome,
		Category: AccountCategoryPnL,
		Currency: AccountCurrencyUSDT,
	},
}

func init() {
	if err := ValidateChartOfAccounts(defaultChartOfAccounts); err != nil {
		panic(fmt.Sprintf("invalid default chart of accounts: %v", err))
	}
}

func DefaultChartOfAccounts() []Account {
	out := make([]Account, len(defaultChartOfAccounts))
	copy(out, defaultChartOfAccounts)
	return out
}

func ValidateChartOfAccounts(chart []Account) error {
	if len(chart) == 0 {
		return fmt.Errorf("chart of accounts is empty")
	}

	seenCategories := make(map[AccountCategory]struct{}, len(supportedCategories))
	seenAccountsByCode := make(map[string]Account, len(chart))
	for _, row := range chart {
		category := normalizeCategory(row.Category)
		code := strings.TrimSpace(row.Code)
		name := strings.TrimSpace(row.Name)
		currency := strings.ToUpper(strings.TrimSpace(row.Currency))
		if code == "" {
			return fmt.Errorf("account code must not be empty")
		}
		if name == "" {
			return fmt.Errorf("account name must not be empty for code %s", code)
		}
		if currency == "" {
			return fmt.Errorf("account currency must not be empty for code %s", code)
		}
		if !IsSupportedCategory(category) {
			return fmt.Errorf("unsupported category %q for account %s", row.Category, code)
		}
		if wantPrefix, ok := categoryCodePrefixes[category]; ok && !strings.HasPrefix(code, wantPrefix) {
			return fmt.Errorf("account %s category %s must use %sxxx code range", code, category, wantPrefix)
		}
		if _, exists := seenAccountsByCode[code]; exists {
			return fmt.Errorf("duplicate account code %s", code)
		}
		seenAccountsByCode[code] = Account{
			Code:     code,
			Name:     name,
			Category: category,
			Currency: currency,
		}
		seenCategories[category] = struct{}{}
	}

	for _, category := range supportedCategories {
		if _, ok := seenCategories[category]; !ok {
			return fmt.Errorf("missing account category %s", category)
		}
	}

	for _, code := range requiredCoreAccountCodes {
		account, ok := seenAccountsByCode[code]
		if !ok {
			return fmt.Errorf("missing required account code %s", code)
		}
		expected := expectedCoreAccountsByCode[code]
		if account.Name != expected.Name || account.Category != expected.Category || account.Currency != expected.Currency {
			return fmt.Errorf("account %s does not match required definition", code)
		}
	}

	return nil
}

func AccountsByCategory(category AccountCategory) []Account {
	items := []Account{}
	for _, row := range defaultChartOfAccounts {
		if row.Category == normalizeCategory(category) {
			items = append(items, row)
		}
	}
	return items
}

func FindAccountByCode(code string) (Account, bool) {
	trimmed := strings.TrimSpace(code)
	for _, row := range defaultChartOfAccounts {
		if row.Code == trimmed {
			return row, true
		}
	}
	return Account{}, false
}

func SupportedCategories() []AccountCategory {
	out := make([]AccountCategory, len(supportedCategories))
	copy(out, supportedCategories)
	return out
}

func IsSupportedCategory(category AccountCategory) bool {
	target := normalizeCategory(category)
	for _, item := range supportedCategories {
		if item == target {
			return true
		}
	}
	return false
}

func normalizeCategory(category AccountCategory) AccountCategory {
	return AccountCategory(strings.ToLower(strings.TrimSpace(string(category))))
}
