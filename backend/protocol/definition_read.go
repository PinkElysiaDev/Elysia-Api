package protocol

import (
	"fmt"
	"strings"
)

func parseCompactRead(shorthand string) (Expression, error) {
	source, rest, hasPointer := strings.Cut(shorthand, "/")
	if !strings.HasPrefix(source, "$") || !isCompactReadSource(source[1:]) {
		return Expression{}, fmt.Errorf("compact read requires an explicit input scope")
	}
	expression := Expression{Op: "read", From: source[1:], isCompactRead: true,
		present: expressionFieldBits["op"] | expressionFieldBits["from"] | expressionFieldBits["path"]}
	if expression.From == "binding" {
		if !hasPointer {
			return Expression{}, fmt.Errorf("compact binding read requires an escaped binding name")
		}
		var name string
		name, rest, hasPointer = strings.Cut(rest, "/")
		if name == "" {
			return Expression{}, fmt.Errorf("compact binding read requires a nonempty binding name")
		}
		pointer, err := parsePointer("/" + name)
		if err != nil {
			return Expression{}, fmt.Errorf("compact binding name: %w", err)
		}
		expression.Name = pointer[0]
		expression.present |= expressionFieldBits["name"]
	}
	if hasPointer {
		expression.Path = "/" + rest
	}
	if _, err := parsePointer(expression.Path); err != nil {
		return Expression{}, fmt.Errorf("compact read path: %w", err)
	}
	return expression, nil
}

func isCompactReadSource(source string) bool {
	switch source {
	case "input", "root", "item", "context", "runtime", "state", "binding":
		return true
	default:
		return false
	}
}

func (expression Expression) checkCompactFields() error {
	// An emptied binding name would disappear under omitempty after a scope edit.
	// Keep the compiler's authored-name rejection visible across serialization.
	if expression.Op == "read" && expression.From != "binding" && (expression.Name != "" || expression.present.has("name")) {
		return fmt.Errorf("/expression/name: name requires the binding scope")
	}
	for _, operation := range expressionOperations {
		if expression.Op == operation.Name {
			return checkExpressionFields(expression, operation, "/expression")
		}
	}
	return fmt.Errorf("compact expression has unknown operation %q", expression.Op)
}

func (expression Expression) formatCompactRead() (string, bool) {
	if expression.Op != "read" || expression.Required || !isCompactReadSource(expression.From) {
		return "", false
	}
	if _, err := parsePointer(expression.Path); err != nil {
		return "", false
	}
	if expression.From == "binding" {
		if expression.Name == "" {
			return "", false
		}
		return "$binding/" + escapePointer(expression.Name) + expression.Path, true
	}
	return "$" + expression.From + expression.Path, true
}
