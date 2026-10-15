package services

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"go-bot/internal/db"
	"go-bot/internal/utils"
)

const (
	exportFileTimestampLayout = "20060102_150405"
	exportDisplayTimeLayout   = "2006-01-02 15:04:05"
	maxPDFEntryPreviewRows    = 60
)

type LedgerExportFile struct {
	Name     string
	MIMEType string
	Content  []byte
}

type LedgerExportOptions struct {
	ChatID      int64
	GroupTitle  string
	DayCutTime  string
	Timezone    string
	GeneratedAt time.Time
}

type ExportEngine struct{}

func NewExportEngine() *ExportEngine {
	return &ExportEngine{}
}

func (e *ExportEngine) BuildLedgerExports(entries []db.LedgerEntry, opts LedgerExportOptions) ([]LedgerExportFile, error) {
	if len(entries) == 0 {
		return nil, errors.New("ledger entries are required")
	}
	normalized := normalizeLedgerExportOptions(opts)
	excelPayload, err := buildLedgerExcel(entries, normalized)
	if err != nil {
		return nil, err
	}
	pdfPayload, err := buildLedgerPDF(entries, normalized)
	if err != nil {
		return nil, err
	}
	return []LedgerExportFile{
		{
			Name:     normalized.baseFilename() + ".xlsx",
			MIMEType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
			Content:  excelPayload,
		},
		{
			Name:     normalized.baseFilename() + ".pdf",
			MIMEType: "application/pdf",
			Content:  pdfPayload,
		},
	}, nil
}

func buildLedgerExcel(entries []db.LedgerEntry, opts LedgerExportOptions) ([]byte, error) {
	rows := buildLedgerExcelRows(entries, opts)
	return buildSimpleXLSX(rows)
}

func buildLedgerPDF(entries []db.LedgerEntry, opts LedgerExportOptions) ([]byte, error) {
	lines := buildLedgerPDFLines(entries, opts)
	return buildSimplePDF(lines)
}

func BuildTabularXLSX(rows [][]string) ([]byte, error) {
	return buildSimpleXLSX(rows)
}

func BuildPlainTextPDF(lines []string) ([]byte, error) {
	return buildSimplePDF(lines)
}

func buildLedgerExcelRows(entries []db.LedgerEntry, opts LedgerExportOptions) [][]string {
	sorted := sortLedgerEntries(entries)
	summary := summarizeLedgerEntries(sorted)
	loc := opts.loadLocation()
	rows := [][]string{
		{"导出时间", opts.GeneratedAt.In(loc).Format(exportDisplayTimeLayout)},
		{"群组ID", strconv.FormatInt(opts.ChatID, 10)},
		{"群组名称", opts.displayGroupTitle()},
		{"日切时间", orDefaultValue(opts.DayCutTime, "04:00:00")},
		{"账目总笔数", strconv.Itoa(len(sorted))},
		{"入款笔数", strconv.Itoa(summary.depositCount)},
		{"下发笔数", strconv.Itoa(summary.payoutCount)},
		{"总入款", utils.FormatAmount(summary.depositTotal)},
		{"总下发", utils.FormatAmount(summary.payoutTotal)},
		{"净利润", utils.FormatAmount(summary.depositTotal - summary.payoutTotal)},
		{},
		{"序号", "记账时间", "类型", "原始金额", "币种", "记账金额", "汇率", "费率(%)", "操作人", "备注"},
	}
	for idx, item := range sorted {
		rows = append(rows, []string{
			strconv.Itoa(idx + 1),
			strings.TrimSpace(item.CreatedAt),
			ledgerEntryTypeLabel(item.EntryType),
			utils.FormatAmount(item.Amount),
			exportDisplayCurrency(item.Currency),
			utils.FormatAmount(item.FinalAmount),
			utils.FormatAmount(item.Rate),
			utils.FormatAmount(item.FeeRate),
			exportOperatorLabel(item),
			strings.TrimSpace(item.Note),
		})
	}
	return rows
}

func buildLedgerPDFLines(entries []db.LedgerEntry, opts LedgerExportOptions) []string {
	sorted := sortLedgerEntries(entries)
	summary := summarizeLedgerEntries(sorted)
	loc := opts.loadLocation()
	lines := []string{
		"Ledger Export",
		fmt.Sprintf("Generated At: %s", opts.GeneratedAt.In(loc).Format(exportDisplayTimeLayout)),
		fmt.Sprintf("Chat ID: %d", opts.ChatID),
		fmt.Sprintf("Group: %s", opts.displayGroupTitle()),
		fmt.Sprintf("Day Cut Time: %s", orDefaultValue(opts.DayCutTime, "04:00:00")),
		fmt.Sprintf("Entries: %d", len(sorted)),
		fmt.Sprintf("Deposit: count=%d total=%s", summary.depositCount, utils.FormatAmount(summary.depositTotal)),
		fmt.Sprintf("Payout: count=%d total=%s", summary.payoutCount, utils.FormatAmount(summary.payoutTotal)),
		fmt.Sprintf("Net Profit: %s", utils.FormatAmount(summary.depositTotal-summary.payoutTotal)),
		"",
		"Details:",
	}
	for idx, item := range sorted {
		if idx >= maxPDFEntryPreviewRows {
			lines = append(lines, fmt.Sprintf("... truncated, total rows: %d", len(sorted)))
			break
		}
		lines = append(lines, fmt.Sprintf(
			"#%d %s %s amount=%s final=%s rate=%s fee=%s%% user=%s note=%s",
			idx+1,
			strings.TrimSpace(item.CreatedAt),
			strings.ToUpper(strings.TrimSpace(item.EntryType)),
			formatExportAmountWithCurrency(item.Amount, item.Currency),
			utils.FormatAmount(item.FinalAmount),
			utils.FormatAmount(item.Rate),
			utils.FormatAmount(item.FeeRate),
			exportOperatorLabel(item),
			trimPreviewText(item.Note, 32),
		))
	}
	return lines
}

func exportDisplayCurrency(raw string) string {
	currency := strings.ToUpper(strings.TrimSpace(raw))
	if currency == "" || currency == "CNY" {
		return ""
	}
	return currency
}

func formatExportAmountWithCurrency(amount float64, currency string) string {
	text := utils.FormatAmount(amount)
	displayCurrency := exportDisplayCurrency(currency)
	if displayCurrency == "" {
		return text
	}
	return text + " " + displayCurrency
}

func buildSimpleXLSX(rows [][]string) ([]byte, error) {
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	writeFile := func(path, content string) error {
		writer, err := archive.Create(path)
		if err != nil {
			return err
		}
		_, err = writer.Write([]byte(content))
		return err
	}
	if err := writeFile("[Content_Types].xml", xlsxContentTypesXML); err != nil {
		return nil, err
	}
	if err := writeFile("_rels/.rels", xlsxRootRelsXML); err != nil {
		return nil, err
	}
	if err := writeFile("xl/workbook.xml", xlsxWorkbookXML); err != nil {
		return nil, err
	}
	if err := writeFile("xl/_rels/workbook.xml.rels", xlsxWorkbookRelsXML); err != nil {
		return nil, err
	}
	if err := writeFile("xl/styles.xml", xlsxStylesXML); err != nil {
		return nil, err
	}
	if err := writeFile("xl/worksheets/sheet1.xml", buildSheetXML(rows)); err != nil {
		return nil, err
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func buildSheetXML(rows [][]string) string {
	var builder strings.Builder
	builder.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	builder.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
	builder.WriteString(`<sheetData>`)
	for rowIdx, row := range rows {
		rowNumber := rowIdx + 1
		builder.WriteString(`<row r="`)
		builder.WriteString(strconv.Itoa(rowNumber))
		builder.WriteString(`">`)
		for colIdx, cell := range row {
			cellRef := excelCellReference(colIdx+1, rowNumber)
			builder.WriteString(`<c r="`)
			builder.WriteString(cellRef)
			builder.WriteString(`" t="inlineStr"><is><t xml:space="preserve">`)
			builder.WriteString(escapeXMLText(strings.TrimSpace(cell)))
			builder.WriteString(`</t></is></c>`)
		}
		builder.WriteString(`</row>`)
	}
	builder.WriteString(`</sheetData></worksheet>`)
	return builder.String()
}

func buildSimplePDF(lines []string) ([]byte, error) {
	content := buildPDFContentStream(lines)
	var output bytes.Buffer
	output.WriteString("%PDF-1.4\n")

	offsets := make([]int, 6)
	writeObject := func(index int, body string) {
		offsets[index] = output.Len()
		_, _ = fmt.Fprintf(&output, "%d 0 obj\n%s\nendobj\n", index, body)
	}

	writeObject(1, "<< /Type /Catalog /Pages 2 0 R >>")
	writeObject(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	writeObject(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>")
	writeObject(4, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	writeObject(5, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))

	xrefOffset := output.Len()
	output.WriteString("xref\n0 6\n0000000000 65535 f \n")
	for idx := 1; idx <= 5; idx++ {
		_, _ = fmt.Fprintf(&output, "%010d 00000 n \n", offsets[idx])
	}
	output.WriteString("trailer\n<< /Size 6 /Root 1 0 R >>\n")
	_, _ = fmt.Fprintf(&output, "startxref\n%d\n%%%%EOF", xrefOffset)
	return output.Bytes(), nil
}

func buildPDFContentStream(lines []string) string {
	var builder strings.Builder
	builder.WriteString("BT\n/F1 10 Tf\n36 806 Td\n14 TL\n")
	if len(lines) == 0 {
		lines = []string{"Ledger Export"}
	}
	for _, line := range lines {
		builder.WriteString("(")
		builder.WriteString(escapePDFText(sanitizePDFText(line)))
		builder.WriteString(") Tj\nT*\n")
	}
	builder.WriteString("ET")
	return builder.String()
}

func normalizeLedgerExportOptions(opts LedgerExportOptions) LedgerExportOptions {
	out := opts
	if out.GeneratedAt.IsZero() {
		out.GeneratedAt = time.Now().UTC()
	}
	if strings.TrimSpace(out.Timezone) == "" {
		out.Timezone = "UTC"
	}
	return out
}

func (o LedgerExportOptions) baseFilename() string {
	chatID := o.ChatID
	if chatID == 0 {
		chatID = -1
	}
	return fmt.Sprintf("ledger_export_%d_%s", chatID, o.GeneratedAt.UTC().Format(exportFileTimestampLayout))
}

func (o LedgerExportOptions) loadLocation() *time.Location {
	loc, err := time.LoadLocation(strings.TrimSpace(o.Timezone))
	if err != nil {
		return time.UTC
	}
	return loc
}

func (o LedgerExportOptions) displayGroupTitle() string {
	title := strings.TrimSpace(o.GroupTitle)
	if title != "" {
		return title
	}
	return strconv.FormatInt(o.ChatID, 10)
}

type ledgerSummary struct {
	depositCount int
	payoutCount  int
	depositTotal float64
	payoutTotal  float64
}

func summarizeLedgerEntries(entries []db.LedgerEntry) ledgerSummary {
	summary := ledgerSummary{}
	for _, item := range entries {
		switch strings.ToLower(strings.TrimSpace(item.EntryType)) {
		case "deposit":
			summary.depositCount++
			summary.depositTotal += item.FinalAmount
		case "payout":
			summary.payoutCount++
			summary.payoutTotal += item.FinalAmount
		}
	}
	return summary
}

func sortLedgerEntries(entries []db.LedgerEntry) []db.LedgerEntry {
	out := append([]db.LedgerEntry(nil), entries...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedAt == out[j].CreatedAt {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt < out[j].CreatedAt
	})
	return out
}

func ledgerEntryTypeLabel(entryType string) string {
	switch strings.ToLower(strings.TrimSpace(entryType)) {
	case "deposit":
		return "入款"
	case "payout":
		return "下发"
	default:
		return strings.ToUpper(strings.TrimSpace(entryType))
	}
}

func exportOperatorLabel(entry db.LedgerEntry) string {
	if nickname := strings.TrimSpace(entry.Nickname); nickname != "" {
		return nickname
	}
	if username := strings.TrimSpace(entry.Username); username != "" {
		return "@" + username
	}
	return strconv.FormatInt(entry.UserID, 10)
}

func trimPreviewText(v string, max int) string {
	trimmed := strings.TrimSpace(v)
	if max <= 0 || len(trimmed) <= max {
		return trimmed
	}
	return trimmed[:max] + "..."
}

func excelCellReference(column, row int) string {
	return excelColumnName(column) + strconv.Itoa(row)
}

func excelColumnName(column int) string {
	if column <= 0 {
		return "A"
	}
	var out []byte
	for column > 0 {
		column--
		out = append([]byte{byte('A' + column%26)}, out...)
		column /= 26
	}
	return string(out)
}

func escapeXMLText(v string) string {
	var buffer bytes.Buffer
	_ = xml.EscapeText(&buffer, []byte(sanitizeXMLText(v)))
	return buffer.String()
}

func sanitizeXMLText(v string) string {
	var builder strings.Builder
	for _, ch := range v {
		if ch == '\t' || ch == '\n' || ch == '\r' || ch >= 0x20 {
			builder.WriteRune(ch)
		}
	}
	return builder.String()
}

func sanitizePDFText(v string) string {
	var builder strings.Builder
	for _, ch := range strings.TrimSpace(v) {
		if ch < 32 || ch > 126 {
			builder.WriteByte('?')
			continue
		}
		builder.WriteRune(ch)
	}
	return builder.String()
}

func escapePDFText(v string) string {
	replacer := strings.NewReplacer(`\`, `\\`, "(", `\(`, ")", `\)`)
	return replacer.Replace(v)
}

func orDefaultValue(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

const xlsxContentTypesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>
  <Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>
  <Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>
</Types>`

const xlsxRootRelsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>
</Relationships>`

const xlsxWorkbookXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <sheets>
    <sheet name="Ledger" sheetId="1" r:id="rId1"/>
  </sheets>
</workbook>`

const xlsxWorkbookRelsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>
  <Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>
</Relationships>`

const xlsxStylesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
  <fonts count="1">
    <font><sz val="11"/><name val="Calibri"/></font>
  </fonts>
  <fills count="1">
    <fill><patternFill patternType="none"/></fill>
  </fills>
  <borders count="1">
    <border><left/><right/><top/><bottom/><diagonal/></border>
  </borders>
  <cellStyleXfs count="1">
    <xf numFmtId="0" fontId="0" fillId="0" borderId="0"/>
  </cellStyleXfs>
  <cellXfs count="1">
    <xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/>
  </cellXfs>
</styleSheet>`
