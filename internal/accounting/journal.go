package accounting

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

type JournalLineDirection string

const (
	JournalLineDirectionDebit  JournalLineDirection = "debit"
	JournalLineDirectionCredit JournalLineDirection = "credit"
)

const (
	JournalEntryTypeDeposit = "deposit"
	JournalEntryTypePayout  = "payout"
)

const (
	JournalAccountCodeMerchantReceivable = "1101"
	JournalAccountCodeCustomerBalance    = "2002"
)

const (
	JournalAccountNameMerchantReceivable = "商户应收"
	JournalAccountNameCustomerBalance    = "客户余额"
)

type JournalLineTemplate struct {
	Direction   JournalLineDirection
	AccountCode string
	AccountName string
}

var journalTemplates = map[string][]JournalLineTemplate{
	JournalEntryTypeDeposit: {
		{
			Direction:   JournalLineDirectionDebit,
			AccountCode: JournalAccountCodeMerchantReceivable,
			AccountName: JournalAccountNameMerchantReceivable,
		},
		{
			Direction:   JournalLineDirectionCredit,
			AccountCode: JournalAccountCodeCustomerBalance,
			AccountName: JournalAccountNameCustomerBalance,
		},
	},
	JournalEntryTypePayout: {
		{
			Direction:   JournalLineDirectionDebit,
			AccountCode: JournalAccountCodeCustomerBalance,
			AccountName: JournalAccountNameCustomerBalance,
		},
		{
			Direction:   JournalLineDirectionCredit,
			AccountCode: JournalAccountCodeMerchantReceivable,
			AccountName: JournalAccountNameMerchantReceivable,
		},
	},
}

var journalInstructionPattern = regexp.MustCompile(`^\s*([+-])\s*(\d+(?:\.\d+)?)(?:\s*[uU])?\s*$`)
var journalInstructionNormalizer = strings.NewReplacer(
	"＋", "+",
	"－", "-",
	"﹢", "+",
	"﹣", "-",
	"−", "-",
	"\u3000", " ",
	"\u00A0", " ",
	"．", ".",
	"﹒", ".",
	"。", ".",
	"０", "0",
	"１", "1",
	"２", "2",
	"３", "3",
	"４", "4",
	"５", "5",
	"６", "6",
	"７", "7",
	"８", "8",
	"９", "9",
	"Ｕ", "U",
	"ｕ", "u",
)

type JournalLine struct {
	Direction   JournalLineDirection
	AccountCode string
	AccountName string
	Amount      float64
}

type JournalEntry struct {
	Instruction string
	EntryType   string
	Amount      float64
	Lines       []JournalLine
}

type JournalEntryBuilder struct {
	entry JournalEntry
}

func NewJournalEntryBuilder(entryType string, amount float64) *JournalEntryBuilder {
	return &JournalEntryBuilder{
		entry: JournalEntry{
			EntryType: strings.ToLower(strings.TrimSpace(entryType)),
			Amount:    math.Abs(amount),
		},
	}
}

func (builder *JournalEntryBuilder) Debit(accountCode, accountName string, amount float64) *JournalEntryBuilder {
	return builder.appendLine(JournalLineDirectionDebit, accountCode, accountName, amount)
}

func (builder *JournalEntryBuilder) Credit(accountCode, accountName string, amount float64) *JournalEntryBuilder {
	return builder.appendLine(JournalLineDirectionCredit, accountCode, accountName, amount)
}

func (builder *JournalEntryBuilder) appendLine(direction JournalLineDirection, accountCode, accountName string, amount float64) *JournalEntryBuilder {
	builder.entry.Lines = append(builder.entry.Lines, JournalLine{
		Direction:   direction,
		AccountCode: strings.TrimSpace(accountCode),
		AccountName: strings.TrimSpace(accountName),
		Amount:      math.Abs(amount),
	})
	return builder
}

func (builder *JournalEntryBuilder) Build() JournalEntry {
	result := builder.entry
	if len(builder.entry.Lines) > 0 {
		result.Lines = append([]JournalLine(nil), builder.entry.Lines...)
	}
	return result
}

func GenerateJournalEntry(instruction string) (JournalEntry, error) {
	entryType, amount, err := ParseJournalInstruction(instruction)
	if err != nil {
		return JournalEntry{}, err
	}

	entry, err := BuildJournalEntry(entryType, amount)
	if err != nil {
		return JournalEntry{}, err
	}
	entry.Instruction = formatJournalInstruction(entryType, amount)
	return entry, nil
}

func GenerateJournalSummaryCN(instruction string) (string, error) {
	entry, err := GenerateJournalEntry(instruction)
	if err != nil {
		return "", err
	}
	return entry.SummaryCN(), nil
}

func ParseJournalInstruction(instruction string) (string, float64, error) {
	trimmed := normalizeJournalInstruction(instruction)
	matched := journalInstructionPattern.FindStringSubmatch(trimmed)
	if matched == nil {
		return "", 0, fmt.Errorf("invalid instruction %q", instruction)
	}

	amount, err := strconv.ParseFloat(matched[2], 64)
	if err != nil || amount <= 0 {
		return "", 0, fmt.Errorf("invalid instruction amount %q", matched[2])
	}

	entryType := JournalEntryTypeDeposit
	if matched[1] == "-" {
		entryType = JournalEntryTypePayout
	}
	return entryType, amount, nil
}

func BuildJournalEntry(entryType string, amount float64) (JournalEntry, error) {
	normalizedType := strings.ToLower(strings.TrimSpace(entryType))
	normalizedAmount := math.Abs(amount)
	if math.IsNaN(normalizedAmount) || math.IsInf(normalizedAmount, 0) || normalizedAmount <= 0 {
		return JournalEntry{}, fmt.Errorf("amount must be greater than zero")
	}

	lineTemplates, ok := journalTemplates[normalizedType]
	if !ok {
		return JournalEntry{}, fmt.Errorf("unsupported entry type %q", entryType)
	}

	builder := NewJournalEntryBuilder(normalizedType, normalizedAmount)
	for _, lineTemplate := range lineTemplates {
		switch lineTemplate.Direction {
		case JournalLineDirectionDebit:
			builder.Debit(lineTemplate.AccountCode, lineTemplate.AccountName, normalizedAmount)
		case JournalLineDirectionCredit:
			builder.Credit(lineTemplate.AccountCode, lineTemplate.AccountName, normalizedAmount)
		default:
			return JournalEntry{}, fmt.Errorf("unsupported journal direction %q", lineTemplate.Direction)
		}
	}
	entry := builder.Build()
	if err := validateInstructionSplit(entry); err != nil {
		return JournalEntry{}, err
	}
	return entry, nil
}

func validateInstructionSplit(entry JournalEntry) error {
	lineTemplates, ok := journalTemplates[strings.ToLower(strings.TrimSpace(entry.EntryType))]
	if !ok {
		return fmt.Errorf("unsupported entry type %q", entry.EntryType)
	}
	if entry.Amount <= 0 {
		return fmt.Errorf("journal entry amount must be greater than zero")
	}
	if len(entry.Lines) != 2 {
		return fmt.Errorf("journal entry must contain 2 lines, got %d", len(entry.Lines))
	}
	if len(lineTemplates) != 2 {
		return fmt.Errorf("journal template must contain 2 lines, got %d", len(lineTemplates))
	}

	debit := entry.Lines[0]
	if debit.Direction != JournalLineDirectionDebit {
		return fmt.Errorf("journal entry first line must be debit")
	}
	if debit.Amount <= 0 {
		return fmt.Errorf("journal entry debit amount must be greater than zero")
	}
	expectedDebit := lineTemplates[0]
	if expectedDebit.Direction != JournalLineDirectionDebit ||
		debit.AccountCode != expectedDebit.AccountCode ||
		debit.AccountName != expectedDebit.AccountName {
		return fmt.Errorf(
			"journal entry debit line must be %s(%s)",
			expectedDebit.AccountName,
			expectedDebit.AccountCode,
		)
	}

	credit := entry.Lines[1]
	if credit.Direction != JournalLineDirectionCredit {
		return fmt.Errorf("journal entry second line must be credit")
	}
	if credit.Amount <= 0 {
		return fmt.Errorf("journal entry credit amount must be greater than zero")
	}
	expectedCredit := lineTemplates[1]
	if expectedCredit.Direction != JournalLineDirectionCredit ||
		credit.AccountCode != expectedCredit.AccountCode ||
		credit.AccountName != expectedCredit.AccountName {
		return fmt.Errorf(
			"journal entry credit line must be %s(%s)",
			expectedCredit.AccountName,
			expectedCredit.AccountCode,
		)
	}

	if math.Abs(debit.Amount-credit.Amount) > 1e-9 {
		return fmt.Errorf("journal entry debit and credit amounts must match")
	}
	if math.Abs(entry.Amount-debit.Amount) > 1e-9 {
		return fmt.Errorf("journal entry amount must match debit amount")
	}
	if math.Abs(entry.Amount-credit.Amount) > 1e-9 {
		return fmt.Errorf("journal entry amount must match credit amount")
	}
	return nil
}

func (entry JournalEntry) SummaryCN() string {
	if len(entry.Lines) == 0 {
		return ""
	}
	parts := make([]string, 0, len(entry.Lines))
	for _, line := range entry.Lines {
		parts = append(parts, formatJournalLineCN(line))
	}
	return strings.Join(parts, "，")
}

func formatJournalLineCN(line JournalLine) string {
	direction := "借"
	if line.Direction == JournalLineDirectionCredit {
		direction = "贷"
	}
	return fmt.Sprintf("%s：%s %s", direction, strings.TrimSpace(line.AccountName), formatJournalAmount(line.Amount))
}

func formatJournalAmount(amount float64) string {
	return strconv.FormatFloat(amount, 'f', -1, 64)
}

func formatJournalInstruction(entryType string, amount float64) string {
	sign := "+"
	if strings.EqualFold(strings.TrimSpace(entryType), JournalEntryTypePayout) {
		sign = "-"
	}
	return sign + formatJournalAmount(math.Abs(amount))
}

func normalizeJournalInstruction(instruction string) string {
	return journalInstructionNormalizer.Replace(strings.TrimSpace(instruction))
}
