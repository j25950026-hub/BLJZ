package services

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestControlPoint414TimescaleIntegration(t *testing.T) {
	t.Run("控制点 4.1.4 功能验证", func(t *testing.T) {
		execer := &recordingExecutor{}
		if err := executeTimescaleSchema(context.Background(), execer); err != nil {
			t.Fatalf("初始化 Timescale schema 失败: %v", err)
		}
		if len(execer.calls) != len(timescaleSchemaStatements) {
			t.Fatalf("schema 语句数量错误: got=%d want=%d", len(execer.calls), len(timescaleSchemaStatements))
		}
		for idx, stmt := range timescaleSchemaStatements {
			if normalizeSQL(execer.calls[idx].query) != normalizeSQL(stmt) {
				t.Fatalf("schema 语句顺序或内容错误 idx=%d got=%q want=%q", idx, execer.calls[idx].query, stmt)
			}
		}

		store := &TimescaleStore{exec: execer}
		rateAt := time.Date(2026, 2, 19, 20, 10, 0, 0, time.FixedZone("CST", 8*3600))
		if err := store.RecordRateTick(context.Background(), RateTick{
			OccurredAt: rateAt,
			ChatID:     -100414001,
			Provider:   "OKX",
			PayType:    "",
			Price:      7.2345,
			Source:     " https://example.com/rate ",
		}); err != nil {
			t.Fatalf("写入汇率样本失败: %v", err)
		}

		flowAt := rateAt.Add(2 * time.Minute)
		if err := store.RecordCapitalFlowTick(context.Background(), CapitalFlowTick{
			OccurredAt:  flowAt,
			ChatID:      -100414001,
			UserID:      9527,
			EntryType:   "Deposit",
			Currency:    "usdt",
			Amount:      1000,
			BaseAmount:  7234.5,
			FinalAmount: 7220,
			Rate:        7.2345,
			FeeRate:     0.2,
		}); err != nil {
			t.Fatalf("写入资金流向样本失败: %v", err)
		}

		rateCall := execer.calls[len(timescaleSchemaStatements)]
		if normalizeSQL(rateCall.query) != normalizeSQL(insertRateTickSQL) {
			t.Fatalf("汇率写入 SQL 错误: %q", rateCall.query)
		}
		if got := rateCall.args[1]; got != int64(-100414001) {
			t.Fatalf("汇率写入 chat_id 错误: %v", got)
		}
		if got := rateCall.args[2]; got != "okx" {
			t.Fatalf("汇率写入 provider 归一化错误: %v", got)
		}
		if got := rateCall.args[3]; got != "all" {
			t.Fatalf("汇率写入 pay_type 归一化错误: %v", got)
		}
		if got := rateCall.args[5]; got != "https://example.com/rate" {
			t.Fatalf("汇率写入 source 归一化错误: %v", got)
		}
		rateTimeArg, ok := rateCall.args[0].(time.Time)
		if !ok || !rateTimeArg.Equal(rateAt.UTC()) {
			t.Fatalf("汇率写入时间应转换为 UTC，got=%v", rateCall.args[0])
		}

		flowCall := execer.calls[len(timescaleSchemaStatements)+1]
		if normalizeSQL(flowCall.query) != normalizeSQL(insertCapitalFlowTickSQL) {
			t.Fatalf("资金流向写入 SQL 错误: %q", flowCall.query)
		}
		if got := flowCall.args[3]; got != "deposit" {
			t.Fatalf("资金流向 entry_type 归一化错误: %v", got)
		}
		if got := flowCall.args[4]; got != "USDT" {
			t.Fatalf("资金流向 currency 归一化错误: %v", got)
		}
		flowTimeArg, ok := flowCall.args[0].(time.Time)
		if !ok || !flowTimeArg.Equal(flowAt.UTC()) {
			t.Fatalf("资金流向写入时间应转换为 UTC，got=%v", flowCall.args[0])
		}
	})
}

func TestTimescaleSchemaExecutionFailure(t *testing.T) {
	execer := &recordingExecutor{
		failAt: 2,
		err:    errors.New("ddl failed"),
	}
	err := executeTimescaleSchema(context.Background(), execer)
	if err == nil {
		t.Fatalf("schema 执行失败应返回错误")
	}
	if !strings.Contains(err.Error(), "ddl failed") {
		t.Fatalf("错误信息应包含底层原因: %v", err)
	}
}

func TestTimescaleStoreRecordCapitalFlowTickKeepsEmptyCurrency(t *testing.T) {
	execer := &recordingExecutor{}
	store := &TimescaleStore{exec: execer}
	if err := store.RecordCapitalFlowTick(context.Background(), CapitalFlowTick{
		ChatID:    1001,
		UserID:    2002,
		EntryType: "deposit",
		Currency:  "",
		Amount:    10,
	}); err != nil {
		t.Fatalf("写入资金流向失败: %v", err)
	}
	if len(execer.calls) != 1 {
		t.Fatalf("写入调用次数异常: got=%d", len(execer.calls))
	}
	call := execer.calls[0]
	if normalizeSQL(call.query) != normalizeSQL(insertCapitalFlowTickSQL) {
		t.Fatalf("写入 SQL 异常: %q", call.query)
	}
	got, ok := call.args[4].(string)
	if !ok {
		t.Fatalf("currency 参数类型异常: %T", call.args[4])
	}
	if got != "" {
		t.Fatalf("空币种不应回填默认值: got=%q", got)
	}
}

type recordingExecutor struct {
	calls  []recordedExecCall
	failAt int
	err    error
}

type recordedExecCall struct {
	query string
	args  []any
}

func (e *recordingExecutor) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	e.calls = append(e.calls, recordedExecCall{query: query, args: append([]any(nil), args...)})
	if e.failAt > 0 && len(e.calls) == e.failAt {
		return nil, e.err
	}
	return staticSQLResult(1), nil
}

type staticSQLResult int64

func (r staticSQLResult) LastInsertId() (int64, error) {
	return int64(r), nil
}

func (r staticSQLResult) RowsAffected() (int64, error) {
	return int64(r), nil
}

func normalizeSQL(in string) string {
	return strings.Join(strings.Fields(in), " ")
}
