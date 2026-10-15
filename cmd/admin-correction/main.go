package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"go-bot/internal/db"
)

type cliOptions struct {
	dbPath string
	scope  string
	chatID int64
	apply  bool
}

func parseCLI(args []string, stderr io.Writer) (cliOptions, error) {
	var opts cliOptions
	fs := flag.NewFlagSet("admin-correction", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.dbPath, "db", "", "数据库路径（默认读取 DB_PATH）")
	fs.StringVar(&opts.scope, "scope", string(db.CorrectionScopeAll), "修正范围: all|ledger-balances|account-balances")
	fs.Int64Var(&opts.chatID, "chat-id", 0, "仅修正指定 chat_id，0 表示全部")
	fs.BoolVar(&opts.apply, "apply", false, "执行真实修正；默认仅 dry-run")
	if err := fs.Parse(args); err != nil {
		return cliOptions{}, err
	}
	if strings.TrimSpace(opts.dbPath) == "" {
		opts.dbPath = strings.TrimSpace(os.Getenv("DB_PATH"))
	}
	if strings.TrimSpace(opts.dbPath) == "" {
		return cliOptions{}, fmt.Errorf("missing database path: use -db or DB_PATH")
	}
	return opts, nil
}

func run(args []string, stdout, stderr io.Writer) int {
	opts, err := parseCLI(args, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "参数错误: %v\n", err)
		return 2
	}

	database, err := db.NewDatabase(opts.dbPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "初始化数据库失败: %v\n", err)
		return 1
	}
	defer database.Close()

	result, err := database.RunCorrectionTool(db.CorrectionToolInput{
		Scope:  db.CorrectionScope(opts.scope),
		ChatID: opts.chatID,
		DryRun: !opts.apply,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "执行修正工具失败: %v\n", err)
		return 1
	}

	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		_, _ = fmt.Fprintf(stderr, "输出结果失败: %v\n", err)
		return 1
	}
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
