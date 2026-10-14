package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/elysia-api/backend/agent"
)

// elysia CLI：把既有工具面收敛为单一 elysia_cli 入口的命令路由层。命令表里的
// 每条命令委托一个中立 CLI handler（行为与管理端业务实现一致）；权限/风险/
// 超时均由 handler 元数据描述，CLI 服务本身不执行审批。
//
// 支持的脚本语法（轻量 shell 子集）：
//   - 引号：'...'（原样）与 "..."（支持 \" 转义）；'' / "" 表示空串；
//   - flag：--name value / --name=value；布尔 flag 单独出现即 true；
//     列表型 flag 可重复出现或逗号分隔；
//   - 批处理：`&&` 失败即跳过其所在链的剩余命令，`;` 或换行 继续执行
//     （`a && b; c` 中 a 失败时 b 被跳过、c 照常执行）；
//   - 尾管道：| grep <子串>（大小写不敏感）与 | head <n>（支持 -n N / -N）。

const (
	cliOutputBudgetBytes = 32 * 1024
	cliName              = "elysia"
)

// isCLIHelpArgs reports whether a statement is a help request. Help is handled
// by the CLI service before command resolution, so it has no handler effect.
func isCLIHelpArgs(args []string) bool {
	return len(args) == 0 || args[0] == "help" || args[0] == "--help"
}

// runCLI 解析并执行一段 elysia 脚本，返回模型可读的结果。
func (s *Server) runCLI(ctx context.Context, tctx CLIContext, script string) CLIResult {
	return s.runCLIWithOptions(ctx, tctx, script, false)
}

// runCLIWithOptions is kept private so only the built-in Agent adapter can
// opt into its session-title command. MCP always uses the generic CLI path.
func (s *Server) runCLIWithOptions(ctx context.Context, tctx CLIContext, script string, allowInternal bool) CLIResult {
	segments, err := cliSplitStatements(script)
	if err != nil {
		return CLIError("脚本解析失败: "+err.Error(), "parse_failed")
	}
	var outputs []cliStatementOutput
	succeeded, failed := 0, 0
	skipChain := false // && 链跳过模式：直到 ;/换行 边界恢复执行
	// 批内逐命令进度：批处理对模型是一次 elysia_cli 调用，没有逐条上报时前端
	// 只有一行「执行中」挂全程（旧每工具一行的过程可见性丢失）。
	reporter, hasProgress := tctx.(interface{ ReportProgress(string) })
	total := len(segments)
	var batchSecrets []string
	for index, segment := range segments {
		if skipChain {
			if segment.fromAnd {
				continue
			}
			skipChain = false
		}
		// 停止/超时取消后不再起下一条命令（执行中的由各工具自身 ctx 中断）。
		if ctx.Err() != nil {
			break
		}
		if hasProgress {
			reporter.ReportProgress(fmt.Sprintf("正在执行（%d/%d）：%s", index+1, total, redactCLICommandLine(segment.raw)))
		}
		output := s.runOneCLIStatement(ctx, tctx, segment.raw, allowInternal)
		outputs = append(outputs, output)
		batchSecrets = append(batchSecrets, output.secrets...)
		if output.isOK {
			succeeded++
		} else {
			failed++
			// `&&` 分隔：失败跳过所在链的剩余命令；`;`/换行边界照常执行。
			if segment.mustSucceed {
				skipChain = true
			}
		}
	}
	summary := fmt.Sprintf("执行 %d 条命令：%d 成功、%d 失败", succeeded+failed, succeeded, failed)
	var live, redacted strings.Builder
	for _, output := range outputs {
		live.WriteString(output.render(nil))
		redacted.WriteString(output.render(batchSecrets))
	}
	exitCode := 0
	if ctx.Err() != nil {
		summary += "；执行已取消或超时，部分命令可能已完成"
	}
	if failed > 0 || ctx.Err() != nil {
		exitCode = 1
	}
	redactedData, _ := json.Marshal(map[string]any{"output": clampCLIOutput(redacted.String()), "exitCode": exitCode})
	return CLIResult{OK: exitCode == 0, Summary: summary,
		Data:         map[string]any{"output": clampCLIOutput(live.String()), "exitCode": exitCode},
		RedactedData: redactedData,
		SecretValues: batchSecrets,
	}
}

// runOneCLIStatement 执行单条语句，返回渲染后的文本块与成败。
func (s *Server) runOneCLIStatement(ctx context.Context, tctx CLIContext, raw string, allowInternal bool) cliStatementOutput {
	output := cliStatementOutput{}
	// 回显打码：输出会随 tool_result 落库并回放给模型，敏感 flag 的值
	// 不允许经此二次出站（模型自己发的命令，原文在它的上下文里）。
	output.prefix = "$ " + redactCLICommandLine(raw) + "\n"
	statement, err := cliParseStatement(raw)
	if err != nil {
		output.text = "错误: " + err.Error()
		return output
	}
	output.statement = statement
	if isCLIHelpArgs(statement.args) {
		// help 输出同样支持 grep/head 管道（help 文本可能上百行）。
		helpArgs := statement.args
		if len(helpArgs) > 0 {
			helpArgs = helpArgs[1:]
		}
		output.text, output.isOK = renderCLIHelp(helpArgs), true
		return output
	}
	if allowInternal && len(statement.args) >= 3 && statement.args[0] == "session" && statement.args[1] == "title" && len(statement.pipes) == 0 {
		encoded, _ := json.Marshal(map[string]any{"title": strings.Join(statement.args[2:], " ")})
		result := normalizeCLIResult((&updateTitleTool{}).Execute(ctx, tctx, encoded))
		output.text, output.isOK = renderCLIResult(result, cliStatement{}), result.OK
		return output
	}
	inv, err := cliResolve(statement.args)
	if err != nil {
		output.text = "错误: " + err.Error()
		return output
	}
	args, err := inv.command.mapper(inv)
	if err != nil {
		output.text = "错误: " + err.Error()
		return output
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		output.text = "错误: 参数序列化失败: " + err.Error()
		return output
	}
	handler := inv.command.handler(s)
	// 语句级超时：批级预算内按 handler 配置收紧，未声明时使用默认值。
	toolTimeout := cliMetaOf(handler).Timeout
	execCtx := ctx
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > toolTimeout {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, toolTimeout)
		defer cancel()
	}
	result := normalizeCLIResult(handler.Execute(execCtx, tctx, encoded))
	output.text, output.isOK, output.secrets = renderCLIResult(result, cliStatement{}), result.OK, result.SecretValues
	return output
}

// cliStatementOutput retains original text until all batch secrets are known.
type cliStatementOutput struct {
	prefix, text string
	statement    cliStatement
	isOK         bool
	secrets      []string
}

func (output cliStatementOutput) render(secrets []string) string {
	return agent.MaskSecretText(output.prefix, secrets) + applyCLIPipes(agent.MaskSecretText(output.text, secrets), output.statement) + "\n\n"
}

// renderCLIResult 把单条命令的 CLIResult 渲染为文本（含管道过滤）。
func renderCLIResult(result CLIResult, statement cliStatement) string {
	var text string
	if result.OK {
		text = result.Summary
	} else {
		text = "失败: " + result.Summary
	}
	if result.Data != nil {
		text += "\n" + string(result.Data.(json.RawMessage))
	}
	return applyCLIPipes(text, statement)
}

// applyCLIPipes 按书写顺序应用尾管道。
func applyCLIPipes(text string, statement cliStatement) string {
	for _, pipe := range statement.pipes {
		switch pipe.kind {
		case pipeGrep:
			text = filterLines(text, pipe.text)
		case pipeHead:
			text = headLines(text, pipe.n)
		}
	}
	return text
}

func filterLines(text, pattern string) string {
	needle := strings.ToLower(pattern) // grep 大小写不敏感
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.Contains(strings.ToLower(line), needle) {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

func headLines(text string, n int) string {
	lines := strings.Split(text, "\n")
	if len(lines) <= n {
		return text
	}
	return strings.Join(lines[:n], "\n") + "\n…（已截断）"
}

const (
	// cliOutputHeadBytes / cliOutputTailBytes 是输出超预算时保留的头尾
	// 体量；两者之和须小于 cliOutputBudgetBytes。
	cliOutputHeadBytes = 24 * 1024
	cliOutputTailBytes = 4 * 1024
)

// clampCLIOutput 把总输出压进预算：保头尾、截中间并标注。
func clampCLIOutput(text string) string {
	if len(text) <= cliOutputBudgetBytes {
		return text
	}
	head := cliOutputHeadBytes
	tailStart := len(text) - cliOutputTailBytes
	// 字节切点对齐 UTF-8 边界，避免截断出乱码。
	for head > 0 && !utf8.RuneStart(text[head]) {
		head--
	}
	for tailStart < len(text) && !utf8.RuneStart(text[tailStart]) {
		tailStart++
	}
	return text[:head] + "\n…[输出超限，中间已截断]…\n" + text[tailStart:]
}
