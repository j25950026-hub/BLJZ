package db

import (
	"errors"
	"math"
	"sort"
)

const DefaultScheduledReconciliationTolerance = 1e-6

type ScheduledReconciliationDifference struct {
	ChatID                  int64
	TransactionDepositTotal float64
	TransactionPayoutTotal  float64
	BalanceDepositTotal     float64
	BalancePayoutTotal      float64
	DepositDifference       float64
	PayoutDifference        float64
}

type ScheduledReconciliationResult struct {
	CheckedChats    int
	DifferenceCount int
	Tolerance       float64
	Differences     []ScheduledReconciliationDifference
}

type scheduledReconciliationSnapshot struct {
	transactionDeposit float64
	transactionPayout  float64
	balanceDeposit     float64
	balancePayout      float64
}

func (d *Database) RunScheduledReconciliation(tolerance float64) (ScheduledReconciliationResult, error) {
	if d == nil {
		return ScheduledReconciliationResult{}, errors.New("database is nil")
	}
	normalizedTolerance := normalizeScheduledReconciliationTolerance(tolerance)
	if d.tenantRouter != nil {
		out := ScheduledReconciliationResult{
			Tolerance:   normalizedTolerance,
			Differences: []ScheduledReconciliationDifference{},
		}
		for _, shard := range d.tenantRouter.shards {
			if shard == nil {
				continue
			}
			result, err := shard.runScheduledReconciliationLocal(normalizedTolerance)
			if err != nil {
				return out, err
			}
			out.CheckedChats += result.CheckedChats
			out.Differences = append(out.Differences, result.Differences...)
		}
		sort.SliceStable(out.Differences, func(i, j int) bool {
			return out.Differences[i].ChatID < out.Differences[j].ChatID
		})
		out.DifferenceCount = len(out.Differences)
		return out, nil
	}
	return d.runScheduledReconciliationLocal(normalizedTolerance)
}

func (d *Database) runScheduledReconciliationLocal(tolerance float64) (ScheduledReconciliationResult, error) {
	snapshots, err := d.collectScheduledReconciliationSnapshots()
	if err != nil {
		return ScheduledReconciliationResult{}, err
	}
	groups, err := d.ListEnabledGroups()
	if err != nil {
		return ScheduledReconciliationResult{}, err
	}
	filteredSnapshots := make(map[int64]scheduledReconciliationSnapshot, len(groups))
	for _, group := range groups {
		if group.ChatID == 0 {
			continue
		}
		snapshot, ok := snapshots[group.ChatID]
		if !ok {
			continue
		}
		filteredSnapshots[group.ChatID] = snapshot
	}

	out := ScheduledReconciliationResult{
		CheckedChats: len(filteredSnapshots),
		Tolerance:    tolerance,
		Differences:  make([]ScheduledReconciliationDifference, 0, len(filteredSnapshots)),
	}
	for chatID, snapshot := range filteredSnapshots {
		diff := ScheduledReconciliationDifference{
			ChatID:                  chatID,
			TransactionDepositTotal: snapshot.transactionDeposit,
			TransactionPayoutTotal:  snapshot.transactionPayout,
			BalanceDepositTotal:     snapshot.balanceDeposit,
			BalancePayoutTotal:      snapshot.balancePayout,
		}
		diff.DepositDifference = diff.TransactionDepositTotal - diff.BalanceDepositTotal
		diff.PayoutDifference = diff.TransactionPayoutTotal - diff.BalancePayoutTotal
		if !scheduledReconciliationDiffExceedsTolerance(diff, tolerance) {
			continue
		}
		out.Differences = append(out.Differences, diff)
	}

	sort.SliceStable(out.Differences, func(i, j int) bool {
		return out.Differences[i].ChatID < out.Differences[j].ChatID
	})
	out.DifferenceCount = len(out.Differences)
	return out, nil
}

func (d *Database) collectScheduledReconciliationSnapshots() (map[int64]scheduledReconciliationSnapshot, error) {
	snapshots := make(map[int64]scheduledReconciliationSnapshot)

	entriesRows, err := d.raw.Query(`
SELECT
	chat_id,
	COALESCE(SUM(CASE WHEN entry_type = 'deposit' THEN final_amount ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN entry_type = 'payout' THEN final_amount ELSE 0 END), 0)
FROM ledger_entries
GROUP BY chat_id`)
	if err != nil {
		return nil, err
	}
	defer entriesRows.Close()

	for entriesRows.Next() {
		var (
			chatID       int64
			depositTotal float64
			payoutTotal  float64
		)
		if err := entriesRows.Scan(&chatID, &depositTotal, &payoutTotal); err != nil {
			return nil, err
		}
		snapshot := snapshots[chatID]
		snapshot.transactionDeposit = depositTotal
		snapshot.transactionPayout = payoutTotal
		snapshots[chatID] = snapshot
	}
	if err := entriesRows.Err(); err != nil {
		return nil, err
	}

	balanceRows, err := d.raw.Query(`SELECT chat_id, deposit_total, payout_total FROM ledger_balances`)
	if err != nil {
		return nil, err
	}
	defer balanceRows.Close()

	for balanceRows.Next() {
		var (
			chatID       int64
			depositTotal float64
			payoutTotal  float64
		)
		if err := balanceRows.Scan(&chatID, &depositTotal, &payoutTotal); err != nil {
			return nil, err
		}
		snapshot := snapshots[chatID]
		snapshot.balanceDeposit = depositTotal
		snapshot.balancePayout = payoutTotal
		snapshots[chatID] = snapshot
	}
	if err := balanceRows.Err(); err != nil {
		return nil, err
	}

	return snapshots, nil
}

func normalizeScheduledReconciliationTolerance(tolerance float64) float64 {
	if tolerance < 0 || math.IsNaN(tolerance) || math.IsInf(tolerance, 0) {
		return DefaultScheduledReconciliationTolerance
	}
	return tolerance
}

func scheduledReconciliationDiffExceedsTolerance(diff ScheduledReconciliationDifference, tolerance float64) bool {
	return math.Abs(diff.DepositDifference) > tolerance || math.Abs(diff.PayoutDifference) > tolerance
}
