package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// update_title：模型在理解任务后把会话标题改成对目标的简短概括。总览卡片
// 用它做名称，所以必须短、必须是目标而不是用户原话的截断。

const (
	agentToolUpdateTitle = "update_title"
	titleRuneLimit       = 16
)

type updateTitleTool struct{}

func (t *updateTitleTool) Name() string      { return agentToolUpdateTitle }
func (t *updateTitleTool) CLIEffect() string { return "" }

func (t *updateTitleTool) Description() string {
	return fmt.Sprintf("把会话标题改写成对任务目标的简洁概括（动宾短语，不超过 %d 个字，不要复述用户原话）。理解任务后调用一次；任务目标变化时再更新。", titleRuneLimit)
}

func (t *updateTitleTool) Execute(ctx context.Context, tctx CLIContext, args json.RawMessage) CLIResult {
	var params struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return CLIError("参数解析失败", "invalid_args")
	}
	title := strings.TrimSpace(params.Title)
	if title == "" {
		return CLIError("标题不能为空", "empty_title")
	}
	if utf8.RuneCountInString(title) > titleRuneLimit {
		return CLIError(fmt.Sprintf("标题超过 %d 个字，请缩短到能一眼看懂任务目标", titleRuneLimit), "title_too_long")
	}
	setter, ok := tctx.(interface{ SetTitle(string) error })
	if !ok {
		return CLIError("当前 CLI 上下文不支持会话标题", "title_unavailable")
	}
	if err := setter.SetTitle(title); err != nil {
		return CLIError("标题保存失败: "+err.Error(), "save_failed")
	}
	return CLIResult{OK: true, Summary: "标题已更新为「" + title + "」"}
}
