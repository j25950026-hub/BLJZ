package db

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type CorrectionScope string

const (
	CorrectionScopeAll             CorrectionScope = "all"
	CorrectionScopeLedgerBalances  CorrectionScope = "ledger-balances"
	CorrectionScopeAccountBalances CorrectionScope = "account-balances"
)

type CorrectionToolInput struct {
	Scope  CorrectionScope `json:"scope"`
	ChatID int64           `json:"chat_id"`
	DryRun bool            `json:"dry_run"`
}

type LedgerBalanceCorrectionIssue struct {
	ChatID               int64   `json:"chat_id"`
	CurrentDepositTotal  float64 `json:"current_deposit_total"`
	CurrentPayoutTotal   float64 `json:"current_payout_total"`
	ExpectedDepositTotal float64 `json:"expected_deposit_total"`
	ExpectedPayoutTotal  float64 `json:"expected_payout_total"`
}

type AccountBalanceCorrectionIssue struct {
	UserID          int64   `json:"user_id"`
	Currency        string  `json:"currency"`
	CurrentBalance  float64 `json:"current_balance"`
	ExpectedBalance float64 `json:"expected_balance"`
}

type CorrectionToolResult struct {
	Scope                  CorrectionScope                 `json:"scope"`
	DryRun                 bool                            `json:"dry_run"`
	CheckedChats           int                             `json:"checked_chats"`
	DirtyLedgerChats       int                             `json:"dirty_ledger_chats"`
	FixedLedgerChats       int                             `json:"fixed_ledger_chats"`
	DirtyAccountBalances   int                             `json:"dirty_account_balances"`
	FixedAccountBalances   int                             `json:"fixed_account_balances"`
	AccountBalancesRebuilt bool                            `json:"account_balances_rebuilt"`
	LedgerBalanceIssues    []LedgerBalanceCorrectionIssue  `json:"ledger_balance_issues"`
	AccountBalanceIssues   []AccountBalanceCorrectionIssue `json:"account_balance_issues"`
}

func (d *Database) RunCorrectionTool(input CorrectionToolInput) (CorrectionToolResult, error) {
	if d == nil {
		return CorrectionToolResult{}, errors.New("database is nil")
	}
	scope, err := normalizeCorrectionScope(input.Scope)
	if err != nil {
		return CorrectionToolResult{}, err
	}

	if d.tenantRouter == nil {
		return d.runCorrectionToolLocal(scope, input.ChatID, input.DryRun)
	}
	if input.ChatID != 0 {
		routed := d.databaseForChat(input.ChatID)
		return routed.runCorrectionToolLocal(scope, input.ChatID, input.DryRun)
	}

	out := CorrectionToolResult{
		Scope:                scope,
		DryRun:               input.DryRun,
		LedgerBalanceIssues:  make([]LedgerBalanceCorrectionIssue, 0),
		AccountBalanceIssues: make([]AccountBalanceCorrectionIssue, 0),
	}
	for _, shard := range d.tenantRouter.shards {
		if shard == nil {
			continue
		}
		shardResult, runErr := shard.runCorrectionToolLocal(scope, 0, input.DryRun)
		if runErr != nil {
			return out, runErr
		}
		out.CheckedChats += shardResult.CheckedChats
		out.DirtyLedgerChats += shardResult.DirtyLedgerChats
		out.FixedLedgerChats += shardResult.FixedLedgerChats
		if shardResult.AccountBalancesRebuilt {
			out.AccountBalancesRebuilt = true
		}
		out.LedgerBalanceIssues = append(out.LedgerBalanceIssues, shardResult.LedgerBalanceIssues...)
		out.DirtyAccountBalances += shardResult.DirtyAccountBalances
		out.FixedAccountBalances += shardResult.FixedAccountBalances
		out.AccountBalanceIssues = append(out.AccountBalanceIssues, shardResult.AccountBalanceIssues...)
	}
	sort.SliceStable(out.LedgerBalanceIssues, func(i, j int) bool {
		return out.LedgerBalanceIssues[i].ChatID < out.LedgerBalanceIssues[j].ChatID
	})
	sort.SliceStable(out.AccountBalanceIssues, func(i, j int) bool {
		if out.AccountBalanceIssues[i].UserID == out.AccountBalanceIssues[j].UserID {
			return out.AccountBalanceIssues[i].Currency < out.AccountBalanceIssues[j].Currency
		}
		return out.AccountBalanceIssues[i].UserID < out.AccountBalanceIssues[j].UserID
	})
	return out, nil
}

func (d *Database) runCorrectionToolLocal(scope CorrectionScope, chatID int64, dryRun bool) (CorrectionToolResult, error) {
	out := CorrectionToolResult{
		Scope:                scope,
		DryRun:               dryRun,
		LedgerBalanceIssues:  make([]LedgerBalanceCorrectionIssue, 0),
		AccountBalanceIssues: make([]AccountBalanceCorrectionIssue, 0),
	}

	if scope == CorrectionScopeAll || scope == CorrectionScopeLedgerBalances {
		issues, checkedChats, err := d.collectLedgerBalanceCorrectionIssues(chatID)
		if err != nil {
			return out, err
		}
		out.CheckedChats = checkedChats
		out.LedgerBalanceIssues = issues
		out.DirtyLedgerChats = len(issues)
		if !dryRun {
			fixed, fixErr := d.applyLedgerBalanceCorrections(issues)
			if fixErr != nil {
				return out, fixErr
			}
			out.FixedLedgerChats = fixed
		}
	}

	if scope == CorrectionScopeAll || scope == CorrectionScopeAccountBalances {
		accountIssues, err := d.collectAccountBalanceCorrectionIssues()
		if err != nil {
			return out, err
		}
		out.AccountBalanceIssues = accountIssues
		out.DirtyAccountBalances = len(accountIssues)
		if !dryRun {
			if err := d.flushHotAccountPending(); err != nil {
				return out, err
			}
			if err := d.withLedgerAtomicTx(func(tx *sql.Tx) error {
				return rebuildAccountBalanceSnapshotTx(tx)
			}); err != nil {
				return out, err
			}
			out.FixedAccountBalances = len(accountIssues)
			out.AccountBalancesRebuilt = true
		}
	}

	return out, nil
}

func normalizeCorrectionScope(scope CorrectionScope) (CorrectionScope, error) {
	normalized := CorrectionScope(strings.ToLower(strings.TrimSpace(string(scope))))
	if normalized == "" {
		return CorrectionScopeAll, nil
	}
	switch normalized {
	case CorrectionScopeAll, CorrectionScopeLedgerBalances, CorrectionScopeAccountBalances:
		return normalized, nil
	default:
		return "", fmt.Errorf("unsupported correction scope: %q", string(scope))
	}
}

func (d *Database) collectLedgerBalanceCorrectionIssues(chatID int64) ([]LedgerBalanceCorrectionIssue, int, error) {
	snapshots, err := d.collectScheduledReconciliationSnapshots()
	if err != nil {
		return nil, 0, err
	}

	chatIDs := make([]int64, 0, len(snapshots))
	for id := range snapshots {
		if chatID != 0 && id != chatID {
			continue
		}
		chatIDs = append(chatIDs, id)
	}
	sort.SliceStable(chatIDs, func(i, j int) bool {
		return chatIDs[i] < chatIDs[j]
	})

	issues := make([]LedgerBalanceCorrectionIssue, 0, len(chatIDs))
	for _, id := range chatIDs {
		snapshot := snapshots[id]
		diff := ScheduledReconciliationDifference{
			ChatID:                  id,
			TransactionDepositTotal: snapshot.transactionDeposit,
			TransactionPayoutTotal:  snapshot.transactionPayout,
			BalanceDepositTotal:     snapshot.balanceDeposit,
			BalancePayoutTotal:      snapshot.balancePayout,
		}
		diff.DepositDifference = diff.TransactionDepositTotal - diff.BalanceDepositTotal
		diff.PayoutDifference = diff.TransactionPayoutTotal - diff.BalancePayoutTotal
		if !scheduledReconciliationDiffExceedsTolerance(diff, DefaultScheduledReconciliationTolerance) {
			continue
		}
		issues = append(issues, LedgerBalanceCorrectionIssue{
			ChatID:               id,
			CurrentDepositTotal:  snapshot.balanceDeposit,
			CurrentPayoutTotal:   snapshot.balancePayout,
			ExpectedDepositTotal: snapshot.transactionDeposit,
			ExpectedPayoutTotal:  snapshot.transactionPayout,
		})
	}
	return issues, len(chatIDs), nil
}

func (d *Database) applyLedgerBalanceCorrections(issues []LedgerBalanceCorrectionIssue) (int, error) {
	fixed := 0
	for _, issue := range issues {
		targetChatID := issue.ChatID
		if err := d.withLedgerAtomicTx(func(tx *sql.Tx) error {
			return rebuildLedgerBalanceSnapshotTx(tx, targetChatID)
		}); err != nil {
			return fixed, fmt.Errorf("rebuild ledger balance chat_id=%d failed: %w", targetChatID, err)
		}
		fixed++
	}
	return fixed, nil
}

type accountBalanceKey struct {
	userID   int64
	currency string
}

type correctionAccountBalanceSnapshot struct {
	current  float64
	expected float64
}

func (d *Database) collectAccountBalanceCorrectionIssues() ([]AccountBalanceCorrectionIssue, error) {
	snapshots := make(map[accountBalanceKey]correctionAccountBalanceSnapshot)

	expectedRows, err := d.raw.Query(`
SELECT
	user_id,
	UPPER(TRIM(currency)),
	COALESCE(SUM(
		CASE
			WHEN entry_type = 'deposit' THEN final_amount
			WHEN entry_type = 'payout' THEN -final_amount
			ELSE final_amount
		END
	), 0)
FROM ledger_entries
GROUP BY user_id, UPPER(TRIM(currency))`)
	if err != nil {
		return nil, err
	}
	defer expectedRows.Close()

	for expectedRows.Next() {
		var (
			userID   int64
			currency string
			balance  float64
		)
		if err := expectedRows.Scan(&userID, &currency, &balance); err != nil {
			return nil, err
		}
		key := accountBalanceKey{
			userID:   userID,
			currency: currency,
		}
		snapshot := snapshots[key]
		snapshot.expected = balance
		snapshots[key] = snapshot
	}
	if err := expectedRows.Err(); err != nil {
		return nil, err
	}

	currentRows, err := d.raw.Query(`
SELECT
	user_id,
	UPPER(TRIM(currency)),
	balance
FROM account_balances
`)
	if err != nil {
		return nil, err
	}
	defer currentRows.Close()

	for currentRows.Next() {
		var (
			userID   int64
			currency string
			balance  float64
		)
		if err := currentRows.Scan(&userID, &currency, &balance); err != nil {
			return nil, err
		}
		key := accountBalanceKey{
			userID:   userID,
			currency: currency,
		}
		snapshot := snapshots[key]
		snapshot.current = balance
		snapshots[key] = snapshot
	}
	if err := currentRows.Err(); err != nil {
		return nil, err
	}

	issues := make([]AccountBalanceCorrectionIssue, 0, len(snapshots))
	for key, snapshot := range snapshots {
		if !isFloatDifferenceSignificant(snapshot.current, snapshot.expected) {
			continue
		}
		issues = append(issues, AccountBalanceCorrectionIssue{
			UserID:          key.userID,
			Currency:        key.currency,
			CurrentBalance:  snapshot.current,
			ExpectedBalance: snapshot.expected,
		})
	}
	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].UserID == issues[j].UserID {
			return issues[i].Currency < issues[j].Currency
		}
		return issues[i].UserID < issues[j].UserID
	})
	return issues, nil
}

func isFloatDifferenceSignificant(left, right float64) bool {
	diff := ScheduledReconciliationDifference{
		DepositDifference: left - right,
		PayoutDifference:  0,
	}
	return scheduledReconciliationDiffExceedsTolerance(diff, DefaultScheduledReconciliationTolerance)
}
