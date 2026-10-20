package server

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/elysia-api/backend/internal/clishell"
	"mvdan.cc/sh/v3/syntax"
)

type cliPipeKind int

const (
	pipeGrep cliPipeKind = iota
	pipeHead
)

type cliPipe struct {
	kind cliPipeKind
	text string
	n    int
}
type cliStatement struct {
	raw   string
	args  []string
	pipes []cliPipe
}
type cliSegment struct {
	raw                  string
	mustSucceed, fromAnd bool
}

func cliPlainStatement(stmt *syntax.Stmt) error {
	if stmt.Background || stmt.Negated || stmt.Coprocess || len(stmt.Redirs) != 0 {
		return fmt.Errorf("不支持后台执行、否定或重定向")
	}
	return nil
}

// The parser owns quoting, operator precedence, comments, and source positions.
// Adapt only && lists to the executor's existing short-circuit representation.
func cliSplitStatements(script string) ([]cliSegment, error) {
	file, err := clishell.Parse(script)
	if err != nil {
		return nil, err
	}
	var segments []cliSegment
	var visit func(*syntax.Stmt) error
	visit = func(stmt *syntax.Stmt) error {
		if err := cliPlainStatement(stmt); err != nil {
			return err
		}
		if binary, ok := stmt.Cmd.(*syntax.BinaryCmd); ok && binary.Op == syntax.AndStmt {
			if err := visit(binary.X); err != nil {
				return err
			}
			segments[len(segments)-1].mustSucceed = true
			next := len(segments)
			if err := visit(binary.Y); err != nil {
				return err
			}
			segments[next].fromAnd = true
			return nil
		}
		// Use command spans, excluding the statement's trailing semicolon.
		raw := script[stmt.Cmd.Pos().Offset():stmt.Cmd.End().Offset()]
		segments = append(segments, cliSegment{raw: raw})
		return nil
	}
	for _, stmt := range file.Stmts {
		if err := visit(stmt); err != nil {
			return nil, err
		}
	}
	if len(segments) == 0 {
		return nil, fmt.Errorf("空命令")
	}
	return segments, nil
}

func cliCallArgs(stmt *syntax.Stmt) ([]string, error) {
	if err := cliPlainStatement(stmt); err != nil {
		return nil, err
	}
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Assigns) != 0 || len(call.Args) == 0 {
		return nil, fmt.Errorf("只支持简单 CLI 命令，不支持赋值或 shell 控制结构")
	}
	args := make([]string, 0, len(call.Args))
	for _, word := range call.Args {
		value, err := clishell.Literal(word)
		if err != nil {
			return nil, err
		}
		args = append(args, value)
	}
	return args, nil
}

func cliParseStatement(raw string) (cliStatement, error) {
	statement := cliStatement{raw: raw}
	file, err := clishell.Parse(raw)
	if err != nil {
		return statement, err
	}
	if len(file.Stmts) != 1 {
		return statement, fmt.Errorf("需要一条 CLI 语句")
	}
	var calls [][]string
	var visit func(*syntax.Stmt) error
	visit = func(stmt *syntax.Stmt) error {
		if err := cliPlainStatement(stmt); err != nil {
			return err
		}
		if binary, ok := stmt.Cmd.(*syntax.BinaryCmd); ok && binary.Op == syntax.Pipe {
			if err := visit(binary.X); err != nil {
				return err
			}
			return visit(binary.Y)
		}
		args, err := cliCallArgs(stmt)
		if err != nil {
			return err
		}
		calls = append(calls, args)
		return nil
	}
	if err := visit(file.Stmts[0]); err != nil {
		return statement, err
	}
	if calls[0][0] != cliName {
		return statement, fmt.Errorf("命令必须以 %s 开头", cliName)
	}
	statement.args = calls[0][1:]
	for _, args := range calls[1:] {
		pipe, err := parseCLIPipe(args)
		if err != nil {
			return statement, err
		}
		statement.pipes = append(statement.pipes, pipe)
	}
	return statement, nil
}

// These are in-memory output filters, not external shell commands.
func parseCLIPipe(tokens []string) (cliPipe, error) {
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
		n, err := strconv.Atoi(arg)
		if err != nil || n <= 0 {
			return cliPipe{}, fmt.Errorf("head 行数必须是正整数（| head <n> 或 | head -n <n>）")
		}
		return cliPipe{kind: pipeHead, n: n}, nil
	default:
		return cliPipe{}, fmt.Errorf("只支持 | grep <子串> 与 | head <n> 管道")
	}
}
