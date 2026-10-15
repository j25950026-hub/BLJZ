package accounting

import "testing"

func TestControlPoint532UnitTesting(t *testing.T) {
	t.Run("控制点 5.3.2 功能验证", func(t *testing.T) {
		chart := DefaultChartOfAccounts()
		if err := ValidateChartOfAccounts(chart); err != nil {
			t.Fatalf("默认科目表校验失败: %v", err)
		}

		assets := AccountsByCategory(" asset ")
		if len(assets) != 1 || assets[0].Code != AccountCodeAssetUSDT {
			t.Fatalf("资产科目分组异常: %+v", assets)
		}

		entry, err := GenerateJournalEntry("+100.5")
		if err != nil {
			t.Fatalf("生成核心记账分录失败: %v", err)
		}
		if err := validateInstructionSplit(entry); err != nil {
			t.Fatalf("借贷平衡校验失败: %v", err)
		}

		rate := MustParseDecimal("7.2")
		fiatAmount, err := ConvertUSDTToFiat(MustParseDecimal("100.5"), rate)
		if err != nil {
			t.Fatalf("USDT 换算法币失败: %v", err)
		}
		if got := fiatAmount.StringFixed(CurrencyScaleFiat); got != "723.60" {
			t.Fatalf("法币金额不符合预期 want=723.60 got=%s", got)
		}
	})
}

func TestDecimalGuardBranches(t *testing.T) {
	t.Run("parse invalid formats", func(t *testing.T) {
		for _, input := range []string{"", "+", "-", ".1", "1.", "1.2.3", "abc"} {
			if _, err := ParseDecimal(input); err == nil {
				t.Fatalf("非法金额应被拒绝: %q", input)
			}
		}
	})

	t.Run("string and zero handling", func(t *testing.T) {
		value := MustParseDecimal("-0")
		if got := value.String(); got != "0" {
			t.Fatalf("负零格式化错误 want=0 got=%s", got)
		}

		zero := ZeroDecimal()
		if !zero.IsZero() {
			t.Fatalf("ZeroDecimal 应返回零值")
		}
	})

	t.Run("division and rate guards", func(t *testing.T) {
		one := MustParseDecimal("1")
		zero := ZeroDecimal()

		if _, err := one.Div(zero); err == nil {
			t.Fatalf("除零应返回错误")
		}

		if _, err := ConvertUSDTToFiat(one, zero); err == nil {
			t.Fatalf("零汇率换算应返回错误")
		}

		if _, err := ConvertFiatToUSDT(one, zero); err == nil {
			t.Fatalf("零汇率回算应返回错误")
		}
	})

	t.Run("scale guards and clamped formatting", func(t *testing.T) {
		one := MustParseDecimal("1")
		for _, scale := range []int{-1, DecimalScale + 1} {
			if _, err := one.Round(scale); err == nil {
				t.Fatalf("非法四舍六入五成双精度应被拒绝: %d", scale)
			}
			if _, err := NewDecimalFromMinorUnits(1, scale); err == nil {
				t.Fatalf("非法最小单位精度应被拒绝: %d", scale)
			}
			if _, err := one.MinorUnits(scale); err == nil {
				t.Fatalf("非法最小单位转换精度应被拒绝: %d", scale)
			}
		}

		value := MustParseDecimal("1.23456789")
		if got := value.StringFixed(-1); got != "1" {
			t.Fatalf("负精度格式化错误 want=1 got=%s", got)
		}
		if got := value.StringFixed(99); got != "1.23456789" {
			t.Fatalf("超范围精度格式化错误 want=1.23456789 got=%s", got)
		}
	})
}

func TestChartValidationBranches(t *testing.T) {
	t.Run("lookup behavior", func(t *testing.T) {
		if _, ok := FindAccountByCode(" 1001 "); !ok {
			t.Fatalf("FindAccountByCode 应支持前后空白")
		}
		if items := AccountsByCategory("unknown"); len(items) != 0 {
			t.Fatalf("未知类别不应返回科目: %+v", items)
		}
	})

	t.Run("required field validation", func(t *testing.T) {
		invalidCode := cloneChart(DefaultChartOfAccounts())
		invalidCode[0].Code = ""
		if err := ValidateChartOfAccounts(invalidCode); err == nil {
			t.Fatalf("空科目编码应校验失败")
		}

		invalidName := cloneChart(DefaultChartOfAccounts())
		invalidName[0].Name = ""
		if err := ValidateChartOfAccounts(invalidName); err == nil {
			t.Fatalf("空科目名称应校验失败")
		}

		invalidCategory := cloneChart(DefaultChartOfAccounts())
		invalidCategory[0].Category = AccountCategory("equity")
		if err := ValidateChartOfAccounts(invalidCategory); err == nil {
			t.Fatalf("不支持的科目类别应校验失败")
		}
	})
}

func TestJournalGuardBranches(t *testing.T) {
	t.Run("unsupported direction in template", func(t *testing.T) {
		original := append([]JournalLineTemplate(nil), journalTemplates[JournalEntryTypeDeposit]...)
		mutated := append([]JournalLineTemplate(nil), original...)
		mutated[0].Direction = JournalLineDirection("unknown")
		journalTemplates[JournalEntryTypeDeposit] = mutated
		t.Cleanup(func() {
			journalTemplates[JournalEntryTypeDeposit] = original
		})

		if _, err := BuildJournalEntry(JournalEntryTypeDeposit, 10); err == nil {
			t.Fatalf("模板方向非法时应校验失败")
		}
	})

	t.Run("line amount must be positive", func(t *testing.T) {
		entry := JournalEntry{
			EntryType: JournalEntryTypeDeposit,
			Amount:    10,
			Lines: []JournalLine{
				{
					Direction:   JournalLineDirectionDebit,
					AccountCode: JournalAccountCodeMerchantReceivable,
					AccountName: JournalAccountNameMerchantReceivable,
					Amount:      10,
				},
				{
					Direction:   JournalLineDirectionCredit,
					AccountCode: JournalAccountCodeCustomerBalance,
					AccountName: JournalAccountNameCustomerBalance,
					Amount:      10,
				},
			},
		}

		entry.Lines[0].Amount = 0
		if err := validateInstructionSplit(entry); err == nil {
			t.Fatalf("借方金额为零时应校验失败")
		}

		entry.Lines[0].Amount = 10
		entry.Lines[1].Amount = 0
		if err := validateInstructionSplit(entry); err == nil {
			t.Fatalf("贷方金额为零时应校验失败")
		}
	})
}

func cloneChart(chart []Account) []Account {
	out := make([]Account, len(chart))
	copy(out, chart)
	return out
}
