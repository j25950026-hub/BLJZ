package accounting

import "testing"

func TestControlPoint111ChartOfAccounts(t *testing.T) {
	t.Run("控制点 1.1.1 功能验证", func(t *testing.T) {
		chart := DefaultChartOfAccounts()
		if len(chart) < 3 {
			t.Fatalf("会计科目数量不足: %d", len(chart))
		}

		wantCategories := map[AccountCategory]bool{
			AccountCategoryAsset:     false,
			AccountCategoryLiability: false,
			AccountCategoryPnL:       false,
		}
		for _, row := range chart {
			if _, ok := wantCategories[row.Category]; ok {
				wantCategories[row.Category] = true
			}
		}
		for category, ok := range wantCategories {
			if !ok {
				t.Fatalf("缺少科目分类: %s", category)
			}
		}

		asset, ok := FindAccountByCode(AccountCodeAssetUSDT)
		if !ok || asset.Category != AccountCategoryAsset || asset.Currency != AccountCurrencyUSDT || asset.Name != AccountNameAssetUSDT {
			t.Fatalf("资产科目校验失败: %+v", asset)
		}

		liability, ok := FindAccountByCode(AccountCodeLiabilityUserBalance)
		if !ok || liability.Category != AccountCategoryLiability || liability.Name != AccountNameLiabilityUserBalance {
			t.Fatalf("负债科目校验失败: %+v", liability)
		}

		pnl, ok := FindAccountByCode(AccountCodePnLFeeIncome)
		if !ok || pnl.Category != AccountCategoryPnL || pnl.Name != AccountNamePnLFeeIncome {
			t.Fatalf("损益科目校验失败: %+v", pnl)
		}
	})
}

func TestDefaultChartOfAccountsReturnsCopy(t *testing.T) {
	chart := DefaultChartOfAccounts()
	chart[0].Name = "mutated"

	fresh := DefaultChartOfAccounts()
	if fresh[0].Name == "mutated" {
		t.Fatalf("DefaultChartOfAccounts 应返回副本")
	}
}

func TestSupportedCategories(t *testing.T) {
	categories := SupportedCategories()
	if len(categories) != 3 {
		t.Fatalf("SupportedCategories 数量错误: %d", len(categories))
	}

	// Ensure the caller cannot mutate package-level defaults.
	categories[0] = AccountCategory("invalid")
	fresh := SupportedCategories()
	if fresh[0] != AccountCategoryAsset {
		t.Fatalf("SupportedCategories 应返回副本")
	}

	if !IsSupportedCategory(" asset ") {
		t.Fatalf("asset 分类应被识别为有效")
	}
	if IsSupportedCategory("equity") {
		t.Fatalf("equity 分类不应被识别为有效")
	}
}

func TestValidateChartOfAccounts(t *testing.T) {
	valid := DefaultChartOfAccounts()
	if err := ValidateChartOfAccounts(valid); err != nil {
		t.Fatalf("默认科目表应通过校验: %v", err)
	}

	t.Run("duplicate code", func(t *testing.T) {
		invalid := []Account{
			{
				Code:     AccountCodeAssetUSDT,
				Name:     AccountNameAssetUSDT,
				Category: AccountCategoryAsset,
				Currency: AccountCurrencyUSDT,
			},
			{
				Code:     AccountCodeAssetUSDT,
				Name:     AccountNameLiabilityUserBalance,
				Category: AccountCategoryLiability,
				Currency: AccountCurrencyUSDT,
			},
			{
				Code:     AccountCodePnLFeeIncome,
				Name:     AccountNamePnLFeeIncome,
				Category: AccountCategoryPnL,
				Currency: AccountCurrencyUSDT,
			},
		}
		if err := ValidateChartOfAccounts(invalid); err == nil {
			t.Fatalf("重复科目编码未被拒绝")
		}
	})

	t.Run("missing required category", func(t *testing.T) {
		invalid := []Account{
			{
				Code:     AccountCodeAssetUSDT,
				Name:     AccountNameAssetUSDT,
				Category: AccountCategoryAsset,
				Currency: AccountCurrencyUSDT,
			},
			{
				Code:     AccountCodeLiabilityUserBalance,
				Name:     AccountNameLiabilityUserBalance,
				Category: AccountCategoryLiability,
				Currency: AccountCurrencyUSDT,
			},
		}
		if err := ValidateChartOfAccounts(invalid); err == nil {
			t.Fatalf("缺少损益类时应校验失败")
		}
	})

	t.Run("missing currency", func(t *testing.T) {
		invalid := []Account{
			{
				Code:     AccountCodeAssetUSDT,
				Name:     AccountNameAssetUSDT,
				Category: AccountCategoryAsset,
				Currency: "",
			},
			{
				Code:     AccountCodeLiabilityUserBalance,
				Name:     AccountNameLiabilityUserBalance,
				Category: AccountCategoryLiability,
				Currency: AccountCurrencyUSDT,
			},
			{
				Code:     AccountCodePnLFeeIncome,
				Name:     AccountNamePnLFeeIncome,
				Category: AccountCategoryPnL,
				Currency: AccountCurrencyUSDT,
			},
		}
		if err := ValidateChartOfAccounts(invalid); err == nil {
			t.Fatalf("空币种应校验失败")
		}
	})

	t.Run("category code prefix mismatch", func(t *testing.T) {
		invalid := []Account{
			{
				Code:     "2001",
				Name:     AccountNameAssetUSDT,
				Category: AccountCategoryAsset,
				Currency: AccountCurrencyUSDT,
			},
			{
				Code:     AccountCodeLiabilityUserBalance,
				Name:     AccountNameLiabilityUserBalance,
				Category: AccountCategoryLiability,
				Currency: AccountCurrencyUSDT,
			},
			{
				Code:     AccountCodePnLFeeIncome,
				Name:     AccountNamePnLFeeIncome,
				Category: AccountCategoryPnL,
				Currency: AccountCurrencyUSDT,
			},
		}
		if err := ValidateChartOfAccounts(invalid); err == nil {
			t.Fatalf("科目类别与编码区间不匹配时应校验失败")
		}
	})

	t.Run("required account definition mismatch", func(t *testing.T) {
		invalid := []Account{
			{
				Code:     AccountCodeAssetUSDT,
				Name:     AccountNameAssetUSDT,
				Category: AccountCategoryLiability,
				Currency: AccountCurrencyUSDT,
			},
			{
				Code:     AccountCodeLiabilityUserBalance,
				Name:     AccountNameLiabilityUserBalance,
				Category: AccountCategoryLiability,
				Currency: AccountCurrencyUSDT,
			},
			{
				Code:     AccountCodePnLFeeIncome,
				Name:     AccountNamePnLFeeIncome,
				Category: AccountCategoryPnL,
				Currency: AccountCurrencyUSDT,
			},
		}
		if err := ValidateChartOfAccounts(invalid); err == nil {
			t.Fatalf("核心科目定义不匹配时应校验失败")
		}
	})
}
