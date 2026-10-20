// Package clishell provides literal shell words and credential-safe presentation
// shared by the CLI executor and Agent event persistence. It never runs a shell.
package clishell

import (
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

func Parse(script string) (*syntax.File, error) {
	if strings.ContainsRune(script, 0) {
		return nil, fmt.Errorf("NUL is not supported in CLI scripts")
	}
	return syntax.NewParser(syntax.Variant(syntax.LangPOSIX)).Parse(strings.NewReader(script), "")
}

// Literal removes quotes from an already parsed word. Only literal word parts
// are allowed: no environment lookup, expansion, substitution, or filesystem I/O.
func Literal(word *syntax.Word) (string, error) {
	var out strings.Builder
	var parts func([]syntax.WordPart, bool) error
	parts = func(list []syntax.WordPart, double bool) error {
		for _, part := range list {
			switch part := part.(type) {
			case *syntax.Lit:
				for i := 0; i < len(part.Value); i++ {
					ch := part.Value[i]
					if ch == '\\' && i+1 < len(part.Value) {
						next := part.Value[i+1]
						if !double || strings.ContainsRune("\"\\$`\n", rune(next)) {
							i++
							if next == '\n' {
								continue
							}
							ch = next
						}
					}
					out.WriteByte(ch)
				}
			case *syntax.SglQuoted:
				if part.Dollar {
					return fmt.Errorf("dollar quoting is not supported")
				}
				out.WriteString(part.Value)
			case *syntax.DblQuoted:
				if part.Dollar {
					return fmt.Errorf("dollar quoting is not supported")
				}
				if err := parts(part.Parts, true); err != nil {
					return err
				}
			default:
				return fmt.Errorf("shell expansion is not supported; single-quote literal values")
			}
		}
		return nil
	}
	if err := parts(word.Parts, false); err != nil {
		return "", err
	}
	return out.String(), nil
}

// Redact uses AST word spans, so escaped spaces, concatenated quotes, and quoted
// flag names cannot expose credential fragments in progress or persisted events.
func Redact(script string) string {
	file, err := Parse(script)
	if err != nil {
		return "[invalid CLI script]"
	}
	type span struct{ start, end uint }
	var spans []span
	valid := true
	syntax.Walk(file, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok {
			return true
		}
		nextSecret := false
		for _, word := range call.Args {
			value, err := Literal(word)
			if err != nil {
				valid = false
				return false
			}
			name, _, inline := strings.Cut(value, "=")
			secret := false
			switch strings.ToLower(name) {
			case "--api-key", "--secret", "--new-secret", "--token":
				secret = true
			}
			if nextSecret || (secret && inline) {
				spans = append(spans, span{word.Pos().Offset(), word.End().Offset()})
			}
			nextSecret = secret && !inline && !nextSecret
		}
		return false
	})
	if !valid {
		return "[unsupported CLI script]"
	}
	for i := len(spans) - 1; i >= 0; i-- {
		script = script[:spans[i].start] + "***" + script[spans[i].end:]
	}
	return script
}
