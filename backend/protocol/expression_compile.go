package protocol

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/bytedance/sonic"
)

type compiledExpression struct {
	op                                  string
	operation                           string
	path                                string
	pathPrefix                          string
	pointer                             []string
	from                                string
	bindingSlot                         int
	indexSlot                           int
	value                               Value
	fields                              map[string]*compiledExpression
	keys                                []string
	items                               []*compiledExpression
	source, body, when, then, otherwise *compiledExpression
	cases                               map[string]*compiledExpression
	values                              map[string]Value
	to                                  JSONType
	policy                              string
	isRequired                          bool
	result                              JSONType
	schema                              *ValueSchema
}

// isInputIdentity recognizes a compiled leaf, including an expanded reference.
// Root is deliberately excluded: after keeps root bound to the module's input.
func (expression *compiledExpression) isInputIdentity() bool {
	return expression != nil && expression.op == "read" && expression.from == "input" && len(expression.pointer) == 0 && !expression.isRequired
}

type expressionCompiler struct {
	limits            Limits
	nodes             int
	references        map[string]Expression
	resolving         map[string]bool
	used              map[string]bool
	mappingReferences map[string]Expression
}

type expressionScope struct {
	input          *ValueSchema
	root           *ValueSchema
	item           *ValueSchema
	isIteration    bool
	isWalk         bool
	wireCapture    bool
	canReadRuntime bool
	canReadNative  bool
	bindings       map[string]expressionBinding
	bindingDepth   int
	indexes        map[string]int
	indexDepth     int
}

type expressionBinding struct {
	slot   int
	result JSONType
	schema *ValueSchema
}

func (compiler *expressionCompiler) compilePlan(expression Expression, path string, scope expressionScope, depth int) (*compiledOutputPlan, error) {
	compiled, err := compiler.compile(expression, path, scope, depth)
	if err != nil {
		return nil, err
	}
	plan, ok := compileOutputPlan(compiled)
	if !ok {
		return nil, fmt.Errorf("%s: expression has no compiled execution plan", path)
	}
	return plan, nil
}

func (compiler *expressionCompiler) compile(expression Expression, path string, scope expressionScope, depth int) (result *compiledExpression, err error) {
	defer func() { err = locateMappingError(path, err) }()
	compiler.nodes++
	if depth > compiler.limits.Depth || compiler.nodes > compiler.limits.Nodes {
		return nil, fmt.Errorf("%s exceeds expression depth or node limit", path)
	}
	var operation *OperationInfo
	for index := range expressionOperations {
		if expressionOperations[index].Name == expression.Op {
			operation = &expressionOperations[index]
			break
		}
	}
	if operation == nil {
		return nil, fmt.Errorf("%s/op: unsupported mapping operation %q", path, expression.Op)
	}
	if err := checkExpressionFields(expression, *operation, path); err != nil {
		return nil, err
	}
	if expression.Op == "ref" {
		definition, exists := compiler.references[expression.Ref]
		if !exists {
			return nil, fmt.Errorf("%s/ref: unknown expression %q", path, expression.Ref)
		}
		if compiler.resolving[expression.Ref] {
			return nil, fmt.Errorf("%s/ref: recursive expression %q", path, expression.Ref)
		}
		compiler.used[expression.Ref] = true
		if compiler.mappingReferences != nil {
			compiler.mappingReferences[expression.Ref] = definition
		}
		compiler.resolving[expression.Ref] = true
		compiled, err := compiler.compile(definition, path+"/@"+expression.Ref, scope, depth+1)
		delete(compiler.resolving, expression.Ref)
		return compiled, err
	}
	compiled := &compiledExpression{op: expression.Op, operation: expression.Operation, path: path, from: expression.From, value: expression.Value, to: expression.To, policy: expression.Policy, isRequired: expression.Required, result: operation.Result}
	if compiled.op == "read" {
		pointer, err := parsePointer(expression.Path)
		if err != nil {
			return nil, fmt.Errorf("%s/path: %w", path, err)
		}
		compiled.pointer = pointer
		if compiled.from == "" {
			compiled.from = "input"
		}
		if !slices.Contains([]string{"input", "root", "item", "context", "binding", "runtime"}, compiled.from) {
			return nil, fmt.Errorf("%s/from: unknown input scope", path)
		}
		if compiled.from == "item" && !scope.isIteration {
			return nil, fmt.Errorf("%s/from: item requires an array iteration", path)
		}
		schema := scope.input
		switch compiled.from {
		case "root":
			if scope.root != nil {
				schema = scope.root
			}
		case "item":
			schema = scope.item
		case "context":
			schema = nil
		case "runtime":
			if !scope.canReadRuntime {
				return nil, fmt.Errorf("%s/from: runtime metadata requires a package mapping program or effect", path)
			}
			if len(pointer) != 1 || pointer[0] != "interleavedThinking" {
				return nil, fmt.Errorf("%s/path: unknown runtime metadata field", path)
			}
			compiled.result, schema = BooleanType, nil
		case "binding":
			binding, exists := scope.bindings[expression.Name]
			if !exists {
				return nil, fmt.Errorf("%s/name: binding is not declared in this scope", path)
			}
			compiled.bindingSlot, schema = binding.slot, binding.schema
			if len(pointer) == 0 {
				compiled.result = binding.result
			}
		}
		_, hasName := expression.present["name"]
		if compiled.from != "binding" && (expression.Name != "" || hasName) {
			return nil, fmt.Errorf("%s/name: name requires the binding scope", path)
		}
		fieldSchema, err := schemaAtPointer(schema, pointer)
		if err != nil {
			return nil, fmt.Errorf("%s/path: %w", path, err)
		}
		if fieldSchema != nil {
			compiled.result = fieldSchema.Type
			compiled.schema = fieldSchema
		}
	}
	if compiled.op == "literal" {
		compiled.result = valueType(expression.Value)
	}
	if compiled.op == "index" && !scope.isIteration {
		return nil, fmt.Errorf("%s: index requires an array iteration", path)
	}
	if compiled.op == "recur" && !scope.isWalk {
		return nil, fmt.Errorf("%s: recur requires an enclosing walk body", path)
	}
	if compiled.op == "capture_native" && !scope.wireCapture {
		return nil, fmt.Errorf("%s: native capture requires a package wire decoder", path)
	}
	if (compiled.op == "replay_native" || compiled.op == "native_matches") && !scope.canReadNative {
		return nil, fmt.Errorf("%s: native target operations require a package encode program", path)
	}
	if compiled.op == "remember" || compiled.op == "recall" {
		slot, exists := scope.indexes[expression.Name]
		if !exists {
			return nil, fmt.Errorf("%s/name: index is not declared in this scope", path)
		}
		compiled.indexSlot = slot
	}
	if expression.Source != nil {
		child, err := compiler.compile(*expression.Source, path+"/source", scope, depth+1)
		if err != nil {
			return nil, err
		}
		compiled.source = child
	}
	childScope := scope
	if compiled.op == "with_index" {
		if !definitionIdentifier.MatchString(expression.Name) {
			return nil, fmt.Errorf("%s/name: expected an index identifier", path)
		}
		compiled.indexSlot = scope.indexDepth
		childScope.indexDepth++
		childScope.indexes = make(map[string]int, len(scope.indexes)+1)
		for name, slot := range scope.indexes {
			childScope.indexes[name] = slot
		}
		childScope.indexes[expression.Name] = compiled.indexSlot
	}
	if compiled.op == "walk" {
		childScope.isWalk = true
	}
	if compiled.op == "let" {
		if !definitionIdentifier.MatchString(expression.Name) {
			return nil, fmt.Errorf("%s/name: expected a binding identifier", path)
		}
		compiled.bindingSlot = scope.bindingDepth
		childScope.bindingDepth++
		childScope.bindings = make(map[string]expressionBinding, len(scope.bindings)+1)
		for name, binding := range scope.bindings {
			childScope.bindings[name] = binding
		}
		childScope.bindings[expression.Name] = expressionBinding{compiled.bindingSlot, compiled.source.result, compiled.source.schema}
	}
	if slices.Contains([]string{"map", "flatmap", "filter", "walk"}, compiled.op) {
		if !isAssignable(compiled.source.result, ArrayType) {
			return nil, fmt.Errorf("%s/source must produce an array", path)
		}
		childScope.isIteration = true
		childScope.item = nil
		if expression.Source.Op == "read" {
			arraySchema := compiled.source.schema
			if arraySchema != nil {
				childScope.item = arraySchema.Items
			}
		}
	}
	children := []struct {
		name   string
		input  *Expression
		output **compiledExpression
	}{{"body", expression.Body, &compiled.body}, {"when", expression.When, &compiled.when}, {"then", expression.Then, &compiled.then}, {"otherwise", expression.Otherwise, &compiled.otherwise}}
	for _, child := range children {
		if child.input == nil {
			continue
		}
		value, err := compiler.compile(*child.input, path+"/"+child.name, childScope, depth+1)
		if err != nil {
			return nil, err
		}
		*child.output = value
	}
	compiled.fields = make(map[string]*compiledExpression, len(expression.Fields))
	compiled.keys = sortedKeys(expression.Fields)
	for _, key := range compiled.keys {
		child, err := compiler.compile(expression.Fields[key], path+"/fields/"+escapePointer(key), scope, depth+1)
		if err != nil {
			return nil, err
		}
		compiled.fields[key] = child
	}
	for index, item := range expression.Items {
		child, err := compiler.compile(item, fmt.Sprintf("%s/items/%d", path, index), scope, depth+1)
		if err != nil {
			return nil, err
		}
		compiled.items = append(compiled.items, child)
	}
	compiled.cases = make(map[string]*compiledExpression, len(expression.Cases))
	for _, key := range sortedKeys(expression.Cases) {
		child, err := compiler.compile(expression.Cases[key], path+"/cases/"+escapePointer(key), scope, depth+1)
		if err != nil {
			return nil, err
		}
		compiled.cases[key] = child
	}
	compiled.values = make(map[string]Value, len(expression.Values))
	for key, value := range expression.Values {
		compiled.values[key] = value
	}
	if err := checkCompiledExpression(compiled, expression); err != nil {
		return nil, err
	}
	return compiled, nil
}

func checkExpressionFields(expression Expression, operation OperationInfo, path string) error {
	encoded, err := sonic.ConfigStd.Marshal(expression)
	if err != nil {
		return err
	}
	var object map[string]json.RawMessage
	if err := sonic.ConfigStd.Unmarshal(encoded, &object); err != nil {
		return err
	}
	for key := range expression.present {
		if _, exists := object[key]; !exists {
			object[key] = nil
		}
	}
	for key := range object {
		if key != "op" && !slices.Contains(operation.Fields, key) {
			return fmt.Errorf("%s/%s: field is not allowed for %s", path, key, operation.Name)
		}
	}
	for _, key := range operation.Required {
		if value, exists := object[key]; !exists || len(value) == 0 || (string(value) == "null" && key != "value") {
			return fmt.Errorf("%s/%s is required for %s", path, key, operation.Name)
		}
	}
	return nil
}

func checkCompiledExpression(expression *compiledExpression, source Expression) error {
	fail := func(reason string) error { return fmt.Errorf("%s: %s", expression.path, reason) }
	if expression.when != nil && !isAssignable(expression.when.result, BooleanType) {
		return fail("condition must produce a boolean")
	}
	switch expression.op {
	case "replay_native", "native_matches":
		if !isAssignable(expression.source.result, ObjectType) {
			return fail("native source must produce an object")
		}
		if expression.op == "native_matches" {
			if expression.operation != "array_item" {
				return fail("native_matches requires array_item operation")
			}
			if _, err := parsePointer(source.Path); err != nil {
				return fail("native_matches path must be a parent JSON pointer")
			}
			expression.pathPrefix = source.Path + "/"
		}
	case "capture_native":
		if expression.source.op != "read" || !slices.Contains([]string{"input", "root", "item", "binding"}, expression.source.from) {
			return fail("native capture requires a read from the original wire")
		}
	case "let", "remember":
		expression.result, expression.schema = expression.body.result, expression.body.schema
	case "with_index":
		if source.KeyMode != "raw_json" || !slices.Contains([]string{"last", "first", "reject"}, expression.policy) {
			return fail("with_index requires raw_json keys and last, first or reject duplicate policy")
		}
		expression.result, expression.schema = expression.body.result, expression.body.schema
	case "recall":
		if !slices.Contains([]string{"omit", "reject"}, expression.policy) {
			return fail("recall requires omit or reject missing-key policy")
		}
	case "recur":
		if !isAssignable(expression.source.result, ArrayType) {
			return fail("recur source must produce an array")
		}
	case "strip_prefix", "trim_prefix":
		prefix, err := readString(expression.value)
		if err != nil || prefix == "" || !isAssignable(expression.source.result, StringType) {
			return fail(expression.op + " requires a nonempty literal string prefix and a string source")
		}
	case "choose", "enum":
		if !isAssignable(expression.source.result, StringType) {
			return fail("type/enum discriminator must be a string")
		}
		if expression.source.schema != nil && expression.otherwise == nil {
			for _, value := range expression.source.schema.Enum {
				var name string
				if err := value.Decode(&name); err != nil {
					return fail("discriminator enum must contain strings")
				}
				_, hasCase := expression.cases[name]
				_, hasValue := expression.values[name]
				if !hasCase && !hasValue {
					return fail("declared discriminator value has no branch: " + name)
				}
			}
		}
	case "flatmap":
		if !isAssignable(expression.body.result, ArrayType) {
			return fail("flatmap body must produce an array")
		}
	case "cast":
		if !slices.Contains([]JSONType{StringType, NumberType, IntegerType, BooleanType}, expression.to) {
			return fail("cast supports scalar types only")
		}
		expression.result = expression.to
	case "merge":
		if !slices.Contains([]string{"reject", "first", "last"}, expression.policy) {
			return fail("merge requires reject, first or last policy")
		}
		for _, item := range expression.items {
			if !isAssignable(item.result, ObjectType) {
				return fail("merge inputs must be objects")
			}
		}
	case "project":
		if !isAssignable(expression.source.result, ObjectType) {
			return fail("project source must be an object")
		}
		if !slices.Contains([]string{"include", "exclude"}, expression.policy) {
			return fail("project requires include or exclude policy")
		}
		expression.keys = slices.Clone(source.Keys)
		for index, key := range expression.keys {
			expression.keys[index] = coreJSONKey(key)
		}
		slices.Sort(expression.keys)
		for index := 1; index < len(expression.keys); index++ {
			if expression.keys[index] == expression.keys[index-1] {
				return fail("project keys must be unique")
			}
		}
	case "concat":
		for _, item := range expression.items {
			if !isAssignable(item.result, ArrayType) {
				return fail("concat inputs must be arrays")
			}
		}
	case "join":
		_, hasItems := source.present["items"]
		hasItems = hasItems || source.Items != nil
		if hasItems == (expression.source != nil) {
			return fail("join requires exactly one of items or source")
		}
		if expression.source != nil && !isAssignable(expression.source.result, ArrayType) {
			return fail("join source must be an array of strings")
		}
		for _, item := range expression.items {
			if !isAssignable(item.result, StringType) {
				return fail("join inputs must be strings")
			}
		}
	case "sort", "associate":
		if !isAssignable(expression.source.result, ArrayType) {
			return fail("source must be an array")
		}
		pointer, err := parsePointer(source.Key)
		if err != nil {
			return fail("key must be a JSON pointer")
		}
		expression.pointer = pointer
	case "equal":
		if len(expression.items) != 2 {
			return fail("equal requires exactly two operands")
		}
	case "integer":
		switch expression.operation {
		case "add":
			if len(expression.items) < 2 {
				return fail("integer add requires at least two operands")
			}
			expression.result = IntegerType
		case "subtract", "lt", "lte", "gt", "gte":
			if len(expression.items) != 2 {
				return fail("integer " + expression.operation + " requires exactly two operands")
			}
			expression.result = BooleanType
			if expression.operation == "subtract" {
				expression.result = IntegerType
			}
		default:
			return fail("integer operation must be add, subtract, lt, lte, gt or gte")
		}
		for _, item := range expression.items {
			if !isAssignable(item.result, NumberType) {
				return fail("integer operands must produce numbers")
			}
		}
	case "all", "any":
		for _, item := range expression.items {
			if !isAssignable(item.result, BooleanType) {
				return fail("boolean operands required")
			}
		}
	case "not":
		if !isAssignable(expression.source.result, BooleanType) {
			return fail("boolean operand required")
		}
	case "parse_json":
		if !isAssignable(expression.source.result, StringType) {
			return fail("parse_json requires a string")
		}
	}
	return nil
}

func schemaAtPointer(schema *ValueSchema, pointer []string) (*ValueSchema, error) {
	for _, key := range pointer {
		if schema == nil || schema.Type == AnyType {
			return nil, nil
		}
		if schema.Type == ArrayType {
			index, err := strconv.Atoi(key)
			if err != nil || index < 0 || strconv.Itoa(index) != key {
				return nil, fmt.Errorf("array pointer index must be a nonnegative integer")
			}
			schema = schema.Items
			continue
		}
		if schema.Type != ObjectType {
			return nil, fmt.Errorf("cannot traverse scalar field")
		}
		child, exists := schema.Properties[key]
		if !exists {
			if schema.AllowUnknown {
				return nil, nil
			}
			return nil, fmt.Errorf("field %q is not declared by the input schema", key)
		}
		schema = &child
	}
	return schema, nil
}

func escapePointer(key string) string {
	return strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
}

func checkExpressionOutput(expression *compiledExpression, schema *ValueSchema, limits Limits) error {
	if schema == nil {
		return nil
	}
	if !isAssignable(expression.result, schema.Type) {
		return fmt.Errorf("%s: result type does not match output schema", expression.path)
	}
	if expression.op == "literal" {
		return checkValueSchema(expression.value, schema, expression.path, 1, limits)
	}
	if expression.op == "if" {
		if err := checkExpressionOutput(expression.then, schema, limits); err != nil {
			return err
		}
		if expression.otherwise != nil {
			return checkExpressionOutput(expression.otherwise, schema, limits)
		}
	}
	if expression.op == "choose" {
		for _, key := range sortedKeys(expression.cases) {
			if err := checkExpressionOutput(expression.cases[key], schema, limits); err != nil {
				return err
			}
		}
		if expression.otherwise != nil {
			return checkExpressionOutput(expression.otherwise, schema, limits)
		}
	}
	if schema.Type == ObjectType && expression.op == "object" {
		for _, key := range schema.Required {
			child := expression.fields[key]
			if child == nil || child.op == "omit" {
				return fmt.Errorf("%s: required output field %q has no mapping", expression.path, key)
			}
		}
		for _, key := range expression.keys {
			property, exists := schema.Properties[key]
			if !exists {
				if !schema.AllowUnknown {
					return fmt.Errorf("%s: output field %q is not declared", expression.path, key)
				}
				continue
			}
			if err := checkExpressionOutput(expression.fields[key], &property, limits); err != nil {
				return err
			}
		}
	}
	if schema.Type == ArrayType && schema.Items != nil {
		if expression.op == "map" {
			return checkExpressionOutput(expression.body, schema.Items, limits)
		}
		if expression.op == "array" {
			for _, item := range expression.items {
				if err := checkExpressionOutput(item, schema.Items, limits); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
