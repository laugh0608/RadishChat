package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"

	"radishchat/internal/chat"
)

func executeGovernanceData(ctx context.Context, database string, args []string) error {
	switch {
	case len(args) == 1 && args[0] == "prune":
		cleanup, err := chat.PruneGovernanceData(ctx, database)
		if err != nil {
			return fmt.Errorf("ops data prune: %w", err)
		}
		if err := json.NewEncoder(os.Stdout).Encode(cleanup); err != nil {
			return fmt.Errorf("输出数据清理结果: %w", err)
		}
		return nil
	case len(args) >= 2 && args[0] == "export":
		id, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil || id <= 0 {
			return errors.New("导出账号 ID 必须为正整数")
		}
		flags := flag.NewFlagSet("ops data export", flag.ContinueOnError)
		verified := flags.String("verified-username", "", "维护者已核实的请求人用户名")
		output := flags.String("output", "", "尚不存在的导出文件")
		if err := flags.Parse(args[2:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if flags.NArg() == 0 {
			if err := chat.ExportAccountData(ctx, database, id, *verified, *output); err != nil {
				return fmt.Errorf("ops data export: %w", err)
			}
			return nil
		}
	}
	return errors.New("用法: radishchat ops data [export <账号 ID> --verified-username <用户名> --output <文件> | prune]")
}
