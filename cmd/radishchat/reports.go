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

func executeReports(ctx context.Context, database string, args []string) error {
	var result any
	var err error
	switch {
	case len(args) >= 1 && args[0] == "list":
		flags := flag.NewFlagSet("ops reports list", flag.ContinueOnError)
		status := flags.String("status", "", "举报状态，open 或 closed")
		before := flags.Int64("before", 0, "只查看编号小于此值的举报，0 表示从最新举报开始")
		limit := flags.Int("limit", 50, "返回记录数，范围 1–200")
		if err := flags.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("用法: radishchat ops reports list [--status open|closed] [--before ID] [--limit N]")
		}
		result, err = chat.ListMessageReports(ctx, database, *status, *before, *limit)
	case len(args) == 2 && args[0] == "review", len(args) >= 3 && args[0] == "close":
		id, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil || id <= 0 {
			return errors.New("举报编号必须为正整数")
		}
		if args[0] == "review" {
			return outputReport(chat.ReviewMessageReport(ctx, database, id))
		}
		if len(args) == 5 && args[3] == "--replay-closed-at" {
			closedAt, err := strconv.ParseInt(args[4], 10, 64)
			if err != nil || closedAt <= 0 {
				return errors.New("原结案时间须为正整数 Unix 毫秒")
			}
			return outputReport(chat.ReplayMessageReportClosure(ctx, database, id, args[2], closedAt))
		}
		if len(args) != 3 {
			return errors.New("用法: radishchat ops reports close <举报编号> <处理结果> [--replay-closed-at <原结案 Unix 毫秒>]")
		}
		return outputReport(chat.CloseMessageReport(ctx, database, id, args[2]))
	default:
		return errors.New("用法: radishchat ops reports [list | review <举报编号> | close <举报编号> <处理结果>]")
	}
	return outputReport(result, err)
}

func outputReport(result any, err error) error {
	if err != nil {
		return fmt.Errorf("ops reports: %w", err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		return fmt.Errorf("输出举报: %w", err)
	}
	return nil
}
