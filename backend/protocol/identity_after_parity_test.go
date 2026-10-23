package protocol

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

var errIdentityAfterModule = errors.New("synthetic identity module failure")

// Both wrappers execute the same adapter. Hiding typed interfaces changes only
// the boundary under test, not the request produced or the number of generations.
type identityAfterModule struct {
	request *Request
	cancel  context.CancelFunc
	err     error
	calls   int
}

func (*identityAfterModule) Name() string { return "identity-parity" }
func (*identityAfterModule) Directions() []Direction {
	return []Direction{DecodeRequest, EncodeRequest, DecodeResponse, EncodeResponse}
}
func (*identityAfterModule) RequestBaselineSafe() {}
func (module *identityAfterModule) DecodeRequest(_ context.Context, _ Value, _ EvaluationContext) (*Request, error) {
	module.calls++
	if module.cancel != nil {
		module.cancel()
	}
	return module.request, module.err
}
func (*identityAfterModule) EncodeRequest(context.Context, *Request, EvaluationContext) (Value, error) {
	return Value{}, fmt.Errorf("unused request encoder")
}
func (*identityAfterModule) DecodeResponse(context.Context, Value, EvaluationContext) (*Response, error) {
	return nil, fmt.Errorf("unused response decoder")
}
func (*identityAfterModule) EncodeResponse(context.Context, *Response, EvaluationContext) (Value, error) {
	return Value{}, fmt.Errorf("unused response encoder")
}
func (module *identityAfterModule) Convert(ctx context.Context, direction Direction, input Value, options EvaluationContext) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if direction != DecodeRequest {
		return Value{}, fmt.Errorf("unused conversion direction")
	}
	request, err := module.DecodeRequest(ctx, input, options)
	if err != nil {
		return Value{}, err
	}
	return EncodeSemantic(ctx, direction, request)
}

type identityAfterJSONModule struct{ Module }

type identityAfterCase struct {
	name           string
	request        func(*testing.T) *Request
	limits         func(*Limits)
	callBytes      int64
	isCanceled     bool
	cancelInModule bool
	hasModuleError bool
	rejectInput    bool
	invalidContext bool
	wantError      bool
	wantCalls      int
	wantDomain     string
	wantMetric     string
	wantCanceled   bool
	wantModuleErr  bool
}

type identityAfterObservation struct {
	output       string
	request      *Request
	errorChain   []string
	issues       []ConversionIssue
	resource     *ResourceLimitError
	mappingPath  string
	isCanceled   bool
	hasModuleErr bool
	moduleCalls  int
	isTypedPath  bool
}

func compileIdentityAfterBoundary(t *testing.T, module Module, limits Limits, rejectInput bool) *Compiled {
	t.Helper()
	wireSchema := &ValueSchema{Type: ObjectType, AllowUnknown: true}
	if rejectInput {
		wireSchema.Properties = map[string]ValueSchema{"required": {Type: StringType}}
		wireSchema.Required = []string{"required"}
	}
	definition := Definition{
		SchemaVersion: DefinitionSchemaVersion, ID: "identity-parity", Name: "identity-parity", Version: "1", Family: "identity-parity", WireVersion: "1", Limits: &limits,
		Directions: map[Direction]Mapping{
			DecodeRequest:  {Module: module.Name(), Input: wireSchema, After: &Expression{Op: "read"}},
			EncodeRequest:  {Module: module.Name()},
			DecodeResponse: {Module: module.Name()},
		},
		Operations: map[string]Operation{"generate": {Kind: "generate", Method: "POST", Path: "/generate", Transport: HTTPJSON, Request: EncodeRequest, Response: DecodeResponse, Auth: Credential{Location: "none"}}},
	}
	compiler, err := NewCompiler(DefaultLimits(), []Module{module}, nil)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := EncodeValue(definition)
	if err != nil {
		t.Fatal(err)
	}
	compiled, issues := compiler.Compile(wire.Bytes())
	if compiled == nil {
		t.Fatal(issues)
	}
	return compiled
}

func observeIdentityAfterBoundary(t *testing.T, test identityAfterCase, useTyped, useCache bool) identityAfterObservation {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	callBytes := test.callBytes
	if callBytes == 0 {
		callBytes = 4096
	}
	ctx, err := WithRequestBodyLimit(ctx, callBytes)
	if err != nil {
		t.Fatal(err)
	}
	if useCache {
		ctx = withDocumentMetrics(ctx, requestViewMinBytes)
	}
	module := &identityAfterModule{request: test.request(t)}
	if test.cancelInModule {
		module.cancel = cancel
	}
	if test.hasModuleError {
		module.err = fmt.Errorf("module rejected: %w", errIdentityAfterModule)
	}
	var installed Module = module
	if !useTyped {
		installed = identityAfterJSONModule{module}
	}
	limits := DefaultLimits()
	limits.BufferBytes = 4096
	if test.limits != nil {
		test.limits(&limits)
	}
	compiled := compileIdentityAfterBoundary(t, installed, limits, test.rejectInput)
	options := EvaluationContext{}
	if test.invalidContext {
		options.Values = Object{"absent": {}}
	}
	if test.isCanceled {
		cancel()
	}
	result, err := decodeTyped(ctx, compiled, DecodeRequest, parseTestValue(t, `{}`), options, func(adapter TypedModule, options EvaluationContext) (*Request, error) {
		return adapter.DecodeRequest(ctx, parseTestValue(t, `{}`), options)
	})
	observation := identityAfterObservation{moduleCalls: module.calls, isTypedPath: compiled.hasTypedRequestMapping(DecodeRequest), isCanceled: errors.Is(err, context.Canceled), hasModuleErr: errors.Is(err, errIdentityAfterModule)}
	if err == nil {
		observation.request = result
		output, encodeErr := EncodeValue(result)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		observation.output = output.raw
	}
	var conversion *ConversionError
	if errors.As(err, &conversion) {
		observation.issues = conversion.Issues
	}
	var failure *ResourceLimitError
	if errors.As(err, &failure) {
		copy := *failure
		observation.resource = &copy
	}
	var located *mappingError
	if errors.As(err, &located) {
		observation.mappingPath = located.path
	}
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		observation.errorChain = append(observation.errorChain, fmt.Sprintf("%T: %s", cause, cause))
	}
	return observation
}

func TestIdentityAfterDecodeBoundaryParity(t *testing.T) {
	plain := func(*testing.T) *Request { return &Request{Model: StringValue("m")} }
	large := func(*testing.T) *Request { return &Request{Model: StringValue(strings.Repeat("x", 2048))} }
	manyNodes := func(*testing.T) *Request {
		request := &Request{}
		for range 80 {
			request.Content = append(request.Content, Node{Kind: TextNode, Payload: StringValue("x")})
		}
		return request
	}
	deep := func(*testing.T) *Request {
		node := Node{Kind: TextNode, Payload: StringValue("x")}
		for range 12 {
			node = Node{Kind: MessageNode, Children: []Node{node}}
		}
		return &Request{Content: []Node{node}}
	}
	tests := []identityAfterCase{
		{name: "plain", request: plain, wantCalls: 1},
		{name: "nil output", request: func(*testing.T) *Request { return nil }, wantCalls: 1},
		{name: "normalization", request: func(t *testing.T) *Request {
			return &Request{Content: []Node{}, Tools: []Tool{}, Native: &Native{Value: parseTestValue(t, ` { "unknown":900719925474099312345, "escaped":"\u0061" } `)}, Parameters: Object{"html": parseTestValue(t, ` { "text":"<>&" } `), "big": parseTestValue(t, `900719925474099312345`), "escaped": parseTestValue(t, `"\u0061"`), "\xff": StringValue("invalid key"), "null": parseTestValue(t, `null`)}}
		}, wantCalls: 1},
		{name: "after bytes", request: large, limits: func(l *Limits) { l.BufferBytes = 32 }, wantError: true, wantCalls: 1, wantDomain: "evaluation", wantMetric: "bytes"},
		{name: "mapping nodes", request: manyNodes, limits: func(l *Limits) { l.Nodes = 4 }, wantError: true, wantCalls: 1, wantDomain: "mapping_output", wantMetric: "nodes"},
		{name: "mapping depth", request: deep, limits: func(l *Limits) { l.Depth = 2 }, wantError: true, wantCalls: 1, wantDomain: "mapping_output", wantMetric: "depth"},
		{name: "bytes before nodes", request: manyNodes, limits: func(l *Limits) { l.Nodes, l.BufferBytes = 4, 32 }, wantError: true, wantCalls: 1, wantDomain: "evaluation", wantMetric: "bytes"},
		{name: "bytes before depth", request: deep, limits: func(l *Limits) { l.Depth, l.BufferBytes = 2, 32 }, wantError: true, wantCalls: 1, wantDomain: "evaluation", wantMetric: "bytes"},
		{name: "cancel after module", request: plain, cancelInModule: true, wantError: true, wantCalls: 1, wantCanceled: true},
		{name: "cancel before after bytes", request: large, limits: func(l *Limits) { l.BufferBytes = 32 }, cancelInModule: true, wantError: true, wantCalls: 1, wantCanceled: true},
		{name: "cancel before nodes", request: manyNodes, limits: func(l *Limits) { l.Nodes = 4 }, cancelInModule: true, wantError: true, wantCalls: 1, wantCanceled: true},
		{name: "cancel before depth", request: deep, limits: func(l *Limits) { l.Depth = 2 }, cancelInModule: true, wantError: true, wantCalls: 1, wantCanceled: true},
		{name: "semantic bytes before cancel", request: large, callBytes: 64, cancelInModule: true, wantError: true, wantCalls: 1, wantDomain: "semantic", wantMetric: "bytes"},
		{name: "semantic depth before cancel", request: func(*testing.T) *Request {
			node := Node{Kind: TextNode, Payload: StringValue("x")}
			for range typedMeterTraversalDepth {
				node = Node{Kind: MessageNode, Children: []Node{node}}
			}
			return &Request{Content: []Node{node}}
		}, cancelInModule: true, wantError: true, wantCalls: 1, wantDomain: "semantic", wantMetric: "depth"},
		{name: "module error before cancel", request: plain, cancelInModule: true, hasModuleError: true, wantError: true, wantCalls: 1, wantModuleErr: true},
		{name: "input schema before context", request: plain, rejectInput: true, invalidContext: true, wantError: true},
		{name: "context before module", request: plain, invalidContext: true, hasModuleError: true, wantError: true},
		{name: "cancel before schema", request: plain, rejectInput: true, isCanceled: true, wantError: true, wantCanceled: true},
		{name: "invalid semantic before cancel", request: func(*testing.T) *Request { return &Request{Parameters: Object{"absent": {}}} }, cancelInModule: true, wantError: true, wantCalls: 1},
	}
	for _, test := range tests {
		for _, useCache := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/cache=%t", test.name, useCache), func(t *testing.T) {
				want := observeIdentityAfterBoundary(t, test, false, useCache)
				got := observeIdentityAfterBoundary(t, test, true, useCache)
				if want.isTypedPath || !got.isTypedPath {
					t.Fatal("reference did not hide typed dispatch or candidate did not exercise it")
				}
				got.isTypedPath = false
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("identity boundary differs\ngeneric=%+v\ntyped=%+v", want, got)
				}
				if (len(want.errorChain) != 0) != test.wantError || want.moduleCalls != test.wantCalls || want.isCanceled != test.wantCanceled || want.hasModuleErr != test.wantModuleErr {
					t.Fatalf("fixture did not exercise intended failure: %+v", want)
				}
				if test.wantDomain != "" && (want.resource == nil || want.resource.Domain != test.wantDomain || want.resource.Metric != test.wantMetric) {
					t.Fatalf("unexpected first resource failure: %+v", want)
				}
			})
		}
	}
}
