package accounting

import (
	"math"
	"testing"
)

func TestControlPoint112JournalEntryGenerator(t *testing.T) {
	t.Run("控制点 1.1.2 功能验证", func(t *testing.T) {
		entry, err := GenerateJournalEntry("+100")
		if err != nil {
			t.Fatalf("生成分录失败: %v", err)
		}
		if entry.Instruction != "+100" {
			t.Fatalf("原始指令记录错误: %q", entry.Instruction)
		}
		if entry.EntryType != JournalEntryTypeDeposit {
			t.Fatalf("分录类型错误: %s", entry.EntryType)
		}
		if len(entry.Lines) != 2 {
			t.Fatalf("分录行数量错误: %d", len(entry.Lines))
		}

		debit := entry.Lines[0]
		if debit.Direction != JournalLineDirectionDebit || debit.AccountCode != JournalAccountCodeMerchantReceivable || debit.AccountName != JournalAccountNameMerchantReceivable || debit.Amount != 100 {
			t.Fatalf("借方分录不符合预期: %+v", debit)
		}

		credit := entry.Lines[1]
		if credit.Direction != JournalLineDirectionCredit || credit.AccountCode != JournalAccountCodeCustomerBalance || credit.AccountName != JournalAccountNameCustomerBalance || credit.Amount != 100 {
			t.Fatalf("贷方分录不符合预期: %+v", credit)
		}
		if debit.Amount != credit.Amount {
			t.Fatalf("借贷金额应保持平衡: debit=%v credit=%v", debit.Amount, credit.Amount)
		}

		wantSummary := "借：商户应收 100，贷：客户余额 100"
		if entry.SummaryCN() != wantSummary {
			t.Fatalf("分录摘要错误，want=%q got=%q", wantSummary, entry.SummaryCN())
		}
	})
}

func TestGenerateJournalEntry(t *testing.T) {
	t.Run("payout command", func(t *testing.T) {
		entry, err := GenerateJournalEntry("-88.5")
		if err != nil {
			t.Fatalf("生成下发分录失败: %v", err)
		}
		if entry.Instruction != "-88.5" {
			t.Fatalf("原始指令记录错误: %q", entry.Instruction)
		}
		if entry.EntryType != JournalEntryTypePayout {
			t.Fatalf("分录类型错误: %s", entry.EntryType)
		}
		if len(entry.Lines) != 2 {
			t.Fatalf("分录行数量错误: %d", len(entry.Lines))
		}

		debit := entry.Lines[0]
		if debit.Direction != JournalLineDirectionDebit || debit.AccountCode != JournalAccountCodeCustomerBalance || debit.AccountName != JournalAccountNameCustomerBalance || debit.Amount != 88.5 {
			t.Fatalf("借方分录不符合预期: %+v", debit)
		}

		credit := entry.Lines[1]
		if credit.Direction != JournalLineDirectionCredit || credit.AccountCode != JournalAccountCodeMerchantReceivable || credit.AccountName != JournalAccountNameMerchantReceivable || credit.Amount != 88.5 {
			t.Fatalf("贷方分录不符合预期: %+v", credit)
		}

		want := "借：客户余额 88.5，贷：商户应收 88.5"
		if entry.SummaryCN() != want {
			t.Fatalf("分录摘要错误，want=%q got=%q", want, entry.SummaryCN())
		}
	})

	t.Run("normalize full-width sign", func(t *testing.T) {
		entry, err := GenerateJournalEntry(" ＋100 ")
		if err != nil {
			t.Fatalf("全角符号指令解析失败: %v", err)
		}
		if entry.Instruction != "+100" {
			t.Fatalf("指令归一化失败: %q", entry.Instruction)
		}
		if entry.EntryType != JournalEntryTypeDeposit {
			t.Fatalf("分录类型错误: %s", entry.EntryType)
		}
	})

	t.Run("normalize full-width amount", func(t *testing.T) {
		entry, err := GenerateJournalEntry(" ＋１００．５０ ")
		if err != nil {
			t.Fatalf("全角金额指令解析失败: %v", err)
		}
		if entry.Instruction != "+100.5" {
			t.Fatalf("全角金额指令归一化失败: %q", entry.Instruction)
		}
		want := "借：商户应收 100.5，贷：客户余额 100.5"
		if entry.SummaryCN() != want {
			t.Fatalf("分录摘要错误，want=%q got=%q", want, entry.SummaryCN())
		}
	})

	t.Run("normalize full-width spacing", func(t *testing.T) {
		entry, err := GenerateJournalEntry(" ＋\u3000１００．５０ ")
		if err != nil {
			t.Fatalf("全角空白指令解析失败: %v", err)
		}
		if entry.Instruction != "+100.5" {
			t.Fatalf("全角空白指令归一化失败: %q", entry.Instruction)
		}
	})

	t.Run("normalize spacing and canonical amount", func(t *testing.T) {
		entry, err := GenerateJournalEntry(" + 100.00 ")
		if err != nil {
			t.Fatalf("含空白指令解析失败: %v", err)
		}
		if entry.Instruction != "+100" {
			t.Fatalf("指令应归一化为标准形式: %q", entry.Instruction)
		}
	})

	t.Run("accept unit suffix", func(t *testing.T) {
		entry, err := GenerateJournalEntry(" +100u ")
		if err != nil {
			t.Fatalf("带单位后缀指令解析失败: %v", err)
		}
		if entry.Instruction != "+100" {
			t.Fatalf("带单位后缀指令应归一化为标准形式: %q", entry.Instruction)
		}
		want := "借：商户应收 100，贷：客户余额 100"
		if entry.SummaryCN() != want {
			t.Fatalf("带单位后缀分录摘要错误，want=%q got=%q", want, entry.SummaryCN())
		}
	})

	t.Run("accept full-width unit suffix", func(t *testing.T) {
		entry, err := GenerateJournalEntry(" ＋１００．５ｕ ")
		if err != nil {
			t.Fatalf("带全角单位后缀指令解析失败: %v", err)
		}
		if entry.Instruction != "+100.5" {
			t.Fatalf("带全角单位后缀指令归一化失败: %q", entry.Instruction)
		}
		want := "借：商户应收 100.5，贷：客户余额 100.5"
		if entry.SummaryCN() != want {
			t.Fatalf("带全角单位后缀分录摘要错误，want=%q got=%q", want, entry.SummaryCN())
		}
	})

	t.Run("invalid command", func(t *testing.T) {
		cases := []string{"", "+", "+0", "abc", "入款100"}
		for _, item := range cases {
			if _, err := GenerateJournalEntry(item); err == nil {
				t.Fatalf("非法指令应返回错误: %q", item)
			}
		}
	})
}

func TestGenerateJournalSummaryCN(t *testing.T) {
	t.Run("summary from deposit instruction", func(t *testing.T) {
		summary, err := GenerateJournalSummaryCN("+100")
		if err != nil {
			t.Fatalf("生成分录摘要失败: %v", err)
		}
		want := "借：商户应收 100，贷：客户余额 100"
		if summary != want {
			t.Fatalf("分录摘要错误，want=%q got=%q", want, summary)
		}
	})

	t.Run("summary from payout instruction", func(t *testing.T) {
		summary, err := GenerateJournalSummaryCN("-66.6")
		if err != nil {
			t.Fatalf("生成下发分录摘要失败: %v", err)
		}
		want := "借：客户余额 66.6，贷：商户应收 66.6"
		if summary != want {
			t.Fatalf("下发分录摘要错误，want=%q got=%q", want, summary)
		}
	})

	t.Run("invalid instruction returns error", func(t *testing.T) {
		if _, err := GenerateJournalSummaryCN("invalid"); err == nil {
			t.Fatalf("非法指令应返回错误")
		}
	})
}

func TestValidateInstructionSplit(t *testing.T) {
	baseEntry := JournalEntry{
		EntryType: JournalEntryTypeDeposit,
		Amount:    100,
		Lines: []JournalLine{
			{
				Direction:   JournalLineDirectionDebit,
				AccountCode: JournalAccountCodeMerchantReceivable,
				AccountName: JournalAccountNameMerchantReceivable,
				Amount:      100,
			},
			{
				Direction:   JournalLineDirectionCredit,
				AccountCode: JournalAccountCodeCustomerBalance,
				AccountName: JournalAccountNameCustomerBalance,
				Amount:      100,
			},
		},
	}
	cloneBaseEntry := func() JournalEntry {
		item := baseEntry
		item.Lines = append([]JournalLine(nil), baseEntry.Lines...)
		return item
	}

	t.Run("valid split", func(t *testing.T) {
		if err := validateInstructionSplit(cloneBaseEntry()); err != nil {
			t.Fatalf("合法借贷拆分不应报错: %v", err)
		}
	})

	t.Run("line count must be two", func(t *testing.T) {
		item := cloneBaseEntry()
		item.Lines = item.Lines[:1]
		if err := validateInstructionSplit(item); err == nil {
			t.Fatalf("分录行数量不为2时应报错")
		}
	})

	t.Run("entry amount must be positive", func(t *testing.T) {
		item := cloneBaseEntry()
		item.Amount = 0
		if err := validateInstructionSplit(item); err == nil {
			t.Fatalf("分录金额不为正数时应报错")
		}
	})

	t.Run("debit line must be first", func(t *testing.T) {
		item := cloneBaseEntry()
		item.Lines[0].Direction = JournalLineDirectionCredit
		if err := validateInstructionSplit(item); err == nil {
			t.Fatalf("首行不是借方时应报错")
		}
	})

	t.Run("credit line must be second", func(t *testing.T) {
		item := cloneBaseEntry()
		item.Lines[1].Direction = JournalLineDirectionDebit
		if err := validateInstructionSplit(item); err == nil {
			t.Fatalf("次行不是贷方时应报错")
		}
	})

	t.Run("debit and credit amount must match", func(t *testing.T) {
		item := cloneBaseEntry()
		item.Lines[1].Amount = 99
		if err := validateInstructionSplit(item); err == nil {
			t.Fatalf("借贷金额不一致时应报错")
		}
	})

	t.Run("entry amount must match split lines", func(t *testing.T) {
		item := cloneBaseEntry()
		item.Amount = 99
		if err := validateInstructionSplit(item); err == nil {
			t.Fatalf("分录金额与借贷拆分金额不一致时应报错")
		}
	})

	t.Run("debit account must match template", func(t *testing.T) {
		item := cloneBaseEntry()
		item.Lines[0].AccountCode = JournalAccountCodeCustomerBalance
		if err := validateInstructionSplit(item); err == nil {
			t.Fatalf("借方账户不匹配模板时应报错")
		}
	})

	t.Run("credit account must match template", func(t *testing.T) {
		item := cloneBaseEntry()
		item.Lines[1].AccountCode = JournalAccountCodeMerchantReceivable
		if err := validateInstructionSplit(item); err == nil {
			t.Fatalf("贷方账户不匹配模板时应报错")
		}
	})

	t.Run("entry type must be supported", func(t *testing.T) {
		item := cloneBaseEntry()
		item.EntryType = "unknown"
		if err := validateInstructionSplit(item); err == nil {
			t.Fatalf("分录类型不受支持时应报错")
		}
	})
}

func TestParseJournalInstruction(t *testing.T) {
	t.Run("valid command", func(t *testing.T) {
		entryType, amount, err := ParseJournalInstruction("  +100.25 ")
		if err != nil {
			t.Fatalf("解析合法指令失败: %v", err)
		}
		if entryType != JournalEntryTypeDeposit || amount != 100.25 {
			t.Fatalf("解析结果错误: type=%s amount=%v", entryType, amount)
		}
	})

	t.Run("valid command with full-width sign", func(t *testing.T) {
		entryType, amount, err := ParseJournalInstruction("－90")
		if err != nil {
			t.Fatalf("解析全角符号指令失败: %v", err)
		}
		if entryType != JournalEntryTypePayout || amount != 90 {
			t.Fatalf("解析结果错误: type=%s amount=%v", entryType, amount)
		}
	})

	t.Run("valid command with full-width amount", func(t *testing.T) {
		entryType, amount, err := ParseJournalInstruction("＋８８．５")
		if err != nil {
			t.Fatalf("解析全角金额指令失败: %v", err)
		}
		if entryType != JournalEntryTypeDeposit || amount != 88.5 {
			t.Fatalf("解析结果错误: type=%s amount=%v", entryType, amount)
		}
	})

	t.Run("valid command with full-width spacing", func(t *testing.T) {
		entryType, amount, err := ParseJournalInstruction(" ＋\u3000８８．５ ")
		if err != nil {
			t.Fatalf("解析全角空白指令失败: %v", err)
		}
		if entryType != JournalEntryTypeDeposit || amount != 88.5 {
			t.Fatalf("解析结果错误: type=%s amount=%v", entryType, amount)
		}
	})

	t.Run("valid command with unit suffix", func(t *testing.T) {
		entryType, amount, err := ParseJournalInstruction("+90u")
		if err != nil {
			t.Fatalf("解析带单位后缀指令失败: %v", err)
		}
		if entryType != JournalEntryTypeDeposit || amount != 90 {
			t.Fatalf("解析结果错误: type=%s amount=%v", entryType, amount)
		}
	})

	t.Run("valid command with full-width unit suffix", func(t *testing.T) {
		entryType, amount, err := ParseJournalInstruction("－９９．５Ｕ")
		if err != nil {
			t.Fatalf("解析带全角单位后缀指令失败: %v", err)
		}
		if entryType != JournalEntryTypePayout || amount != 99.5 {
			t.Fatalf("解析结果错误: type=%s amount=%v", entryType, amount)
		}
	})

	t.Run("invalid command", func(t *testing.T) {
		cases := []string{"", "100", "+0", "-0", "入款100"}
		for _, item := range cases {
			if _, _, err := ParseJournalInstruction(item); err == nil {
				t.Fatalf("非法指令应返回错误: %q", item)
			}
		}
	})
}

func TestBuildJournalEntry(t *testing.T) {
	entry, err := BuildJournalEntry(" deposit ", -25)
	if err != nil {
		t.Fatalf("BuildJournalEntry 应支持 amount 归一化: %v", err)
	}
	if entry.Amount != 25 {
		t.Fatalf("金额归一化失败: %v", entry.Amount)
	}
	if _, err := BuildJournalEntry("unknown", 10); err == nil {
		t.Fatalf("未知分录类型应返回错误")
	}
	for _, amount := range []float64{0, math.NaN(), math.Inf(1)} {
		if _, err := BuildJournalEntry("deposit", amount); err == nil {
			t.Fatalf("非法金额应返回错误: %v", amount)
		}
	}
}

func TestJournalEntryBuilder(t *testing.T) {
	entry := NewJournalEntryBuilder(" Deposit ", 100).
		Debit(JournalAccountCodeMerchantReceivable, JournalAccountNameMerchantReceivable, 100).
		Credit(JournalAccountCodeCustomerBalance, JournalAccountNameCustomerBalance, 100).
		Build()

	if entry.EntryType != JournalEntryTypeDeposit {
		t.Fatalf("builder 分录类型错误: %s", entry.EntryType)
	}
	if entry.Amount != 100 {
		t.Fatalf("builder 金额错误: %v", entry.Amount)
	}
	if len(entry.Lines) != 2 {
		t.Fatalf("builder 分录行数量错误: %d", len(entry.Lines))
	}

	want := "借：商户应收 100，贷：客户余额 100"
	if entry.SummaryCN() != want {
		t.Fatalf("builder 分录摘要错误，want=%q got=%q", want, entry.SummaryCN())
	}
}

func TestJournalEntrySummaryCN(t *testing.T) {
	entry := JournalEntry{
		Lines: []JournalLine{
			{
				Direction:   JournalLineDirectionDebit,
				AccountName: JournalAccountNameMerchantReceivable,
				Amount:      100,
			},
			{
				Direction:   JournalLineDirectionCredit,
				AccountName: JournalAccountNameCustomerBalance,
				Amount:      100,
			},
			{
				Direction:   JournalLineDirectionCredit,
				AccountName: "手续费收入",
				Amount:      1,
			},
		},
	}

	want := "借：商户应收 100，贷：客户余额 100，贷：手续费收入 1"
	if entry.SummaryCN() != want {
		t.Fatalf("多行分录摘要错误，want=%q got=%q", want, entry.SummaryCN())
	}

	empty := JournalEntry{}
	if empty.SummaryCN() != "" {
		t.Fatalf("空分录摘要应为空字符串")
	}
}

func TestFormatJournalInstruction(t *testing.T) {
	if got := formatJournalInstruction(" deposit ", -100.5); got != "+100.5" {
		t.Fatalf("deposit 指令格式化错误: %q", got)
	}
	if got := formatJournalInstruction("PAYOUT", 88); got != "-88" {
		t.Fatalf("payout 指令格式化错误: %q", got)
	}
}
