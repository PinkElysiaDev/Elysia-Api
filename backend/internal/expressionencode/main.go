// Command expressionencode derives the Expression JSON writer and test oracle
// from the public struct field order, types and JSON omission tags.
package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"strconv"
	"strings"
)

const (
	expressionSourceFile = "definition.go"
	expressionWriterFile = "definition_encode_generated.go"
	expressionOracleFile = "definition_encode_oracle_test.go"
	generatedFileMode    = 0644
)

type field struct {
	name, kind, tag, key, omission string
}

type generator struct{ output bytes.Buffer }

func (generator *generator) writeLine(format string, values ...any) {
	fmt.Fprintf(&generator.output, format+"\n", values...)
}

func parseExpressionFields() []field {
	files := token.NewFileSet()
	source, err := parser.ParseFile(files, expressionSourceFile, nil, 0)
	if err != nil {
		panic(err)
	}
	var fields []field
	ast.Inspect(source, func(node ast.Node) bool {
		declaration, isType := node.(*ast.TypeSpec)
		if !isType || declaration.Name.Name != "Expression" {
			return true
		}
		structure := declaration.Type.(*ast.StructType)
		for _, member := range structure.Fields.List {
			if len(member.Names) != 1 || !member.Names[0].IsExported() {
				continue
			}
			tagText, err := strconv.Unquote(member.Tag.Value)
			if err != nil {
				panic(err)
			}
			tag := reflect.StructTag(tagText).Get("json")
			key, omission, _ := strings.Cut(tag, ",")
			if key == "" || omission != "" && omission != "omitempty" && omission != "omitzero" {
				panic("unsupported Expression JSON field tag: " + tag)
			}
			var kind bytes.Buffer
			if err := format.Node(&kind, files, member.Type); err != nil {
				panic(err)
			}
			fields = append(fields, field{member.Names[0].Name, kind.String(), tag, key, omission})
		}
		return false
	})
	if len(fields) == 0 || fields[0].name != "Op" || fields[0].omission != "" {
		panic("Expression must begin with its required Op field")
	}
	return fields
}

func (field field) formatOmissionCondition() string {
	value := "expression." + field.name
	if field.omission == "" {
		return ""
	}
	if strings.HasPrefix(field.kind, "*") || field.omission == "omitzero" && field.kind == "[]string" {
		return value + " != nil"
	}
	switch field.kind {
	case "Value":
		return "!" + value + ".IsZero()"
	case "bool":
		return value
	case "string", "JSONType":
		return value + ` != ""`
	case "map[string]Expression", "map[string]Value", "[]Expression":
		return "len(" + value + ") != 0"
	default:
		panic("unsupported Expression omission: " + field.kind + " " + field.tag)
	}
}

func (generator *generator) appendField(field field) {
	value := "expression." + field.name
	switch field.kind {
	case "string":
		generator.writeLine("writer.appendString(%s)", value)
	case "JSONType":
		generator.writeLine("writer.appendString(string(%s))", value)
	case "bool":
		if field.omission != "omitempty" {
			panic("boolean field requires explicit encoding: " + field.name)
		}
		generator.writeLine(`writer.bytes = append(writer.bytes, "true"...)`)
	case "Value":
		generator.writeLine("writer.appendValue(%s)", value)
	case "*Expression":
		generator.writeLine("writer.appendExpression(*%s)", value)
	case "[]Expression":
		generator.writeLine("writer.appendExpressions(%s)", value)
	case "map[string]Expression":
		generator.writeLine("writer.appendExpressionMap(%s)", value)
	case "map[string]Value", "[]string", "*MergeConflictDiagnostic", "*ObjectFieldDiagnostic", "*RejectDiagnostic":
		generator.writeLine("writer.appendEncodedValue(%s)", value)
	default:
		panic("unsupported Expression field: " + field.kind)
	}
}

func (generator *generator) appendObject(fields []field) {
	var contract []string
	for _, field := range fields {
		contract = append(contract, field.name+"|"+field.kind+"|"+field.tag)
	}
	generator.writeLine("const expressionEncodingContract = `%s`", strings.Join(contract, "\n"))
	generator.writeLine("")
	generator.writeLine("func (writer *expressionJSONWriter) appendObject(expression Expression) {")
	generator.writeLine("writer.bytes = append(writer.bytes, '{')")
	for index, field := range fields {
		condition := field.formatOmissionCondition()
		if condition != "" {
			generator.writeLine("if %s {", condition)
		}
		prefix := ""
		if index > 0 {
			prefix = ","
		}
		generator.writeLine("writer.bytes = append(writer.bytes, `%s\"%s\":`...)", prefix, field.key)
		generator.appendField(field)
		if condition != "" {
			generator.writeLine("}")
		}
	}
	generator.writeLine("writer.bytes = append(writer.bytes, '}')")
	generator.writeLine("}")
}

func (generator *generator) appendOracle(fields []field) {
	generator.writeLine("import (")
	generator.writeLine(`"sync"`)
	generator.writeLine("")
	generator.writeLine(`"github.com/bytedance/sonic"`)
	generator.writeLine(")")
	generator.writeLine("")
	generator.writeLine("// This test-only tree freezes the prior recursive marshaling boundary.")
	generator.writeLine("type recursiveExpressionOracle struct {")
	generator.writeLine("original Expression")
	generator.writeLine("marshalOnce func() ([]byte, error)")
	for _, field := range fields {
		generator.writeLine("%s %s `json:%q`", field.name, strings.ReplaceAll(field.kind, "Expression", "recursiveExpressionOracle"), field.tag)
	}
	generator.writeLine("}")
	generator.writeLine("")
	generator.writeLine("func buildRecursiveExpressionOracle(expression Expression) recursiveExpressionOracle {")
	generator.writeLine("oracle := recursiveExpressionOracle{original: expression}")
	for _, field := range fields {
		value := "expression." + field.name
		switch field.kind {
		case "*Expression":
			generator.writeLine("if %s != nil {", value)
			generator.writeLine("child := buildRecursiveExpressionOracle(*%s)", value)
			generator.writeLine("oracle.%s = &child", field.name)
			generator.writeLine("}")
		case "[]Expression", "map[string]Expression":
			generator.writeLine("if %s != nil {", value)
			generator.writeLine("oracle.%s = make(%s, len(%s))", field.name, strings.ReplaceAll(field.kind, "Expression", "recursiveExpressionOracle"), value)
			generator.writeLine("for key, child := range %s {", value)
			generator.writeLine("oracle.%s[key] = buildRecursiveExpressionOracle(child)", field.name)
			generator.writeLine("}")
			generator.writeLine("}")
		default:
			generator.writeLine("oracle.%s = %s", field.name, value)
		}
	}
	generator.writeLine("// Sonic's race check re-enters child marshalers; freeze each immutable node once.")
	generator.writeLine("oracle.marshalOnce = sync.OnceValues(oracle.marshalJSON)")
	generator.writeLine("return oracle")
	generator.writeLine("}")
	generator.writeLine("")
	generator.writeLine("func (oracle recursiveExpressionOracle) MarshalJSON() ([]byte, error) {")
	generator.writeLine("return oracle.marshalOnce()")
	generator.writeLine("}")
	generator.writeLine("")
	generator.writeLine("func (oracle recursiveExpressionOracle) marshalJSON() ([]byte, error) {")
	generator.writeLine("if oracle.original.isCompactRead {")
	generator.writeLine("if err := oracle.original.checkCompactFields(); err != nil { return nil, err }")
	generator.writeLine("if shorthand, canCompact := oracle.original.formatCompactRead(); canCompact { return sonic.ConfigStd.Marshal(shorthand) }")
	generator.writeLine("}")
	generator.writeLine("type plain recursiveExpressionOracle")
	generator.writeLine("return sonic.ConfigStd.Marshal(plain(oracle))")
	generator.writeLine("}")
}

func main() {
	fields := parseExpressionFields()
	for _, name := range []string{expressionWriterFile, expressionOracleFile} {
		generator := generator{}
		generator.writeLine("// Code generated by internal/expressionencode; DO NOT EDIT.")
		generator.writeLine("")
		generator.writeLine("package protocol")
		generator.writeLine("")
		if name == expressionWriterFile {
			generator.appendObject(fields)
		} else {
			generator.appendOracle(fields)
		}
		output, err := format.Source(generator.output.Bytes())
		if err != nil {
			panic(err)
		}
		if err := os.WriteFile(name, output, generatedFileMode); err != nil {
			panic(err)
		}
	}
}
