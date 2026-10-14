package server

import (
	"fmt"
	"strconv"
	"strings"
)

type cliPipeKind int

const (
	pipeGrep cliPipeKind = iota
	pipeHead
)

// cliPipe 一条尾管道：grep 记子串，head 记行数。
type cliPipe struct {
	kind cliPipeKind
	text string
	n    int
}

// cliStatement 是一条待执行语句（命令 + 尾管道）。
type cliStatement struct {
	raw   string
	args  []string
	pipes []cliPipe
}

// cliQuotes 是三个扫描器（tokenizer / 批切分 / 管道切分）共享的引号状态
// 机：' 与 " 互斥，双引号内 \" 与 \\ 是转义序列、不改变引号态。转义规则
// 集中在此——修引号相关的 bug 只需改这一处。
type cliQuotes struct {
	inSingle, inDouble bool
}

// toggle 消费一个字符并维护引号态，返回它是否为引号开闭字符。
func (q *cliQuotes) toggle(ch byte) bool {
	switch {
	case ch == '\'' && !q.inDouble:
		q.inSingle = !q.inSingle
		return true
	case ch == '"' && !q.inSingle:
		q.inDouble = !q.inDouble
		return true
	}
	return false
}

// inside 报告当前是否处于引号内（顶层扫描器据此跳过分隔符）。
func (q *cliQuotes) inside() bool { return q.inSingle || q.inDouble }

// escaped 报告 s[i] 是否为双引号内转义序列（\" 或 \\）的开头；调用方应
// 保留 s[i] 与 s[i+1] 并跳过两字节（tokenizer 只保留转义后的字符）。
func (q *cliQuotes) escaped(s string, i int) bool {
	return q.inDouble && s[i] == '\\' && i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '\\')
}

// cliTokenize 把单条命令切成参数（处理引号与转义）。空引号（” / ""）
// 产出空 token——`--secret ”` 依赖它表达空值。
func cliTokenize(line string) ([]string, error) {
	var tokens []string
	var current strings.Builder
	var quotes cliQuotes
	quoted := false // 当前 token 是否含过引号（空引号也要产出 token）
	flush := func() {
		if current.Len() > 0 || quoted {
			tokens = append(tokens, current.String())
			current.Reset()
			quoted = false
		}
	}
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if quotes.escaped(line, i) {
			current.WriteByte(line[i+1])
			i++
			continue
		}
		if quotes.toggle(ch) {
			quoted = true
			continue
		}
		if (ch == ' ' || ch == '\t') && !quotes.inside() {
			flush()
			continue
		}
		current.WriteByte(ch)
	}
	flush()
	if quotes.inside() {
		return nil, fmt.Errorf("引号未闭合")
	}
	return tokens, nil
}

// cliSegment 是一条语句及其分隔语义：mustSucceed=true（`&&` 分隔）时
// 本条失败即跳过其 && 链；fromAnd=true 表示本条由 `&&` 连接（是链的
// 后续成员，前驱失败时会被跳过）。
type cliSegment struct {
	raw         string
	mustSucceed bool
	fromAnd     bool
}

// cliSplitStatements 按顶层 `&&` / `;` / 换行切分脚本（引号内不切分，
// 双引号内 \" 与 \\ 不改变引号态——与 tokenizer 同一规则）。`&&` 后的换行
// 视为续行（空段不清除链标记），`a &&\nb` 与 `a && b` 同义。
func cliSplitStatements(script string) ([]cliSegment, error) {
	var parts []cliSegment
	var current strings.Builder
	var quotes cliQuotes
	pendingFromAnd := false // 下一段是否由 && 引入
	flush := func(mustSucceed bool) {
		if trimmed := strings.TrimSpace(current.String()); trimmed != "" {
			parts = append(parts, cliSegment{raw: trimmed, mustSucceed: mustSucceed, fromAnd: pendingFromAnd})
			// 仅实际产出语句段时清链标记：`&&` 右侧的空段（换行/分号前）
			// 表示链还在等右操作数，跨换行保持 fromAnd。
			pendingFromAnd = false
		}
		current.Reset()
	}
	for i := 0; i < len(script); i++ {
		ch := script[i]
		if quotes.escaped(script, i) {
			current.WriteByte(ch)
			current.WriteByte(script[i+1])
			i++
			continue
		}
		quotes.toggle(ch)
		if quotes.inside() {
			current.WriteByte(ch)
			continue
		}
		if ch == ';' || ch == '\n' {
			flush(false)
			continue
		}
		if ch == '&' {
			if i+1 >= len(script) || script[i+1] != '&' {
				return nil, fmt.Errorf("不支持单个 &（批处理请用 && 连接）")
			}
			// `&&`：本条失败即跳过所在链；右侧段标记 fromAnd。
			flush(true)
			pendingFromAnd = true
			i++
			continue
		}
		current.WriteByte(ch)
	}
	flush(false)
	if quotes.inside() {
		return nil, fmt.Errorf("引号未闭合")
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("空命令")
	}
	return parts, nil
}

// cliParseStatement 解析单条语句：切管道（仅支持 grep/head）、切参数。
func cliParseStatement(raw string) (cliStatement, error) {
	statement := cliStatement{raw: raw}
	segments := splitPipe(raw)
	tokens, err := cliTokenize(segments[0])
	if err != nil {
		return statement, err
	}
	if len(tokens) == 0 || tokens[0] != cliName {
		return statement, fmt.Errorf("命令必须以 %s 开头（当前 %q）", cliName, firstWord(segments[0]))
	}
	statement.args = tokens[1:]
	for _, segment := range segments[1:] {
		pipe, err := parseCLIPipe(segment)
		if err != nil {
			return statement, err
		}
		statement.pipes = append(statement.pipes, pipe)
	}
	return statement, nil
}

// parseCLIPipe 解析一条尾管道段（已切掉 | 的文本）：grep <子串> 或
// head <n>（head 也接受 -n N / -N 习惯写法）。
func parseCLIPipe(segment string) (cliPipe, error) {
	tokens, err := cliTokenize(segment)
	if err != nil {
		return cliPipe{}, err
	}
	if len(tokens) == 0 {
		return cliPipe{}, fmt.Errorf("空管道段")
	}
	switch tokens[0] {
	case "grep":
		if len(tokens) != 2 {
			return cliPipe{}, fmt.Errorf("grep 管道只接受一个子串参数")
		}
		return cliPipe{kind: pipeGrep, text: tokens[1]}, nil
	case "head":
		arg := ""
		if len(tokens) == 2 {
			arg = strings.TrimPrefix(tokens[1], "-")
		} else if len(tokens) == 3 && tokens[1] == "-n" {
			arg = tokens[2]
		}
		n, convErr := strconv.Atoi(arg)
		if arg == "" || convErr != nil || n <= 0 {
			return cliPipe{}, fmt.Errorf("head 行数必须是正整数（| head <n> 或 | head -n <n>）")
		}
		return cliPipe{kind: pipeHead, n: n}, nil
	default:
		return cliPipe{}, fmt.Errorf("只支持 | grep <子串> 与 | head <n> 管道（不支持 %q）", tokens[0])
	}
}

// splitPipe 按顶层 | 切分（引号内不切分，双引号内 \" 与 \\ 不改变引号态）。
func splitPipe(raw string) []string {
	var segments []string
	var current strings.Builder
	var quotes cliQuotes
	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if quotes.escaped(raw, i) {
			current.WriteByte(ch)
			current.WriteByte(raw[i+1])
			i++
			continue
		}
		quotes.toggle(ch)
		if ch == '|' && !quotes.inside() {
			segments = append(segments, current.String())
			current.Reset()
			continue
		}
		current.WriteByte(ch)
	}
	return append(segments, current.String())
}

func firstWord(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// cliInvocation 是一条命令解析后的调用形状。flag 是否出现由 flags 的键
// 存在性表达（值可为空串——`--secret ”` 是合法的空值）。
