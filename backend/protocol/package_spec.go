package protocol

import (
	"fmt"
	"slices"
	"strings"
)

// ProtocolSpec describes wire documents and endpoints without core directions.
type ProtocolSpec struct {
	ID          string                           `json:"id"`
	Name        string                           `json:"name"`
	Version     string                           `json:"version"`
	Family      string                           `json:"family"`
	WireVersion string                           `json:"wireVersion"`
	Documents   map[string]WireContractSpec      `json:"documents"`
	Expressions map[string]Expression            `json:"expressions,omitempty"`
	Constraints map[string][]WireAssertion       `json:"constraints,omitempty"`
	Operations  map[string]ProtocolOperationSpec `json:"operations"`
}

// ProtocolOperationSpec binds transport metadata to named wire documents.
// Request and Response identify documents, not executable mapping programs.
type ProtocolOperationSpec struct {
	Kind      string                      `json:"kind"`
	Method    string                      `json:"method"`
	Path      string                      `json:"path"`
	Transport Transport                   `json:"transport"`
	Request   string                      `json:"request,omitempty"`
	Response  string                      `json:"response,omitempty"`
	Headers   map[string]string           `json:"headers,omitempty"`
	Query     map[string]string           `json:"query,omitempty"`
	Auth      Credential                  `json:"auth"`
	Framing   *Framing                    `json:"framing,omitempty"`
	Input     *ValueSchema                `json:"input,omitempty"`
	Models    *ProtocolModelDiscoverySpec `json:"models,omitempty"`
	Task      *ProtocolTaskFlowSpec       `json:"task,omitempty"`
	Session   *ProtocolSessionSpec        `json:"session,omitempty"`
}

// ProtocolModelDiscoverySpec declares wire pagination without a core decoder.
type ProtocolModelDiscoverySpec struct {
	CursorParameter string `json:"cursorParameter,omitempty"`
}

// ProtocolOperationMapping selects core adapters for an existing wire operation.
type ProtocolOperationMapping struct {
	Request      Direction            `json:"request,omitempty"`
	Response     Direction            `json:"response,omitempty"`
	DecodeModels *Expression          `json:"decodeModels,omitempty"`
	Task         *ProtocolTaskMapping `json:"task,omitempty"`
}

// ProtocolTaskFlowSpec links fixed wire operations, never executable mappings.
type ProtocolTaskFlowSpec struct {
	Status            string `json:"status"`
	Result            string `json:"result"`
	Cancel            string `json:"cancel,omitempty"`
	IdempotencyHeader string `json:"idempotencyHeader,omitempty"`
}

// ProtocolTaskMapping maps provider updates, client receipts and control input.
type ProtocolTaskMapping struct {
	Decode  Expression `json:"decode"`
	Encode  Expression `json:"encode"`
	Control Expression `json:"control"`
}

type compiledProtocolSpec struct {
	documents         map[string]compiledWireContract
	resolvedDocuments map[string]WireContractSpec
	documentHashes    map[string]string
	operations        map[string]Operation
	operationSchemas  map[string]*runtimeValueSchema
}

// Identity returns the authored wire identity; content hashes bind its execution.
func (spec ProtocolSpec) Identity() Identity {
	return Identity{DefinitionID: spec.ID, Revision: spec.Version, Family: spec.Family, WireVersion: spec.WireVersion}
}

func (spec ProtocolSpec) compile(limits Limits) (compiledProtocolSpec, error) {
	if !definitionIdentifier.MatchString(spec.ID) || strings.TrimSpace(spec.Version) == "" || strings.TrimSpace(spec.Family) == "" || strings.TrimSpace(spec.WireVersion) == "" {
		return compiledProtocolSpec{}, fmt.Errorf("/spec: id, version, family and wireVersion are required")
	}
	if len(spec.Documents) == 0 || len(spec.Operations) == 0 {
		return compiledProtocolSpec{}, fmt.Errorf("/spec: requires named documents and operations")
	}
	compiled := compiledProtocolSpec{
		documents:         make(map[string]compiledWireContract, len(spec.Documents)),
		resolvedDocuments: make(map[string]WireContractSpec, len(spec.Documents)),
		documentHashes:    make(map[string]string, len(spec.Documents)),
		operations:        make(map[string]Operation, len(spec.Operations)),
		operationSchemas:  make(map[string]*runtimeValueSchema, len(spec.Operations)),
	}
	expressions := newExpressionCompiler(limits, spec.Expressions)
	expressions.constraints = newWireConstraintCompiler(spec.Constraints)
	documentCompiler := protocolDocumentCompiler{spec: spec, compiled: &compiled, expressions: expressions, visiting: map[string]bool{}, depths: map[string]int{}}
	for _, name := range sortedKeys(spec.Documents) {
		if err := documentCompiler.compile(name, 1); err != nil {
			return compiledProtocolSpec{}, err
		}
	}
	for _, name := range sortedKeys(spec.Expressions) {
		if !expressions.used[name] {
			return compiledProtocolSpec{}, fmt.Errorf("/spec/expressions/%s: unreachable expression", escapePointer(name))
		}
	}
	for _, name := range sortedKeys(spec.Constraints) {
		if !expressions.constraints.used[name] {
			return compiledProtocolSpec{}, fmt.Errorf("/spec/constraints/%s: unreachable constraint", escapePointer(name))
		}
	}
	for _, name := range sortedKeys(spec.Operations) {
		wire := spec.Operations[name]
		operation, err := wire.compile(name, compiled.documents, limits)
		if err != nil {
			return compiledProtocolSpec{}, err
		}
		schema, err := compileValueSchema(wire.Input, limits)
		if err != nil {
			return compiledProtocolSpec{}, err
		}
		compiled.operations[name], compiled.operationSchemas[name] = operation, schema
	}
	return compiled, nil
}

func (wire ProtocolOperationSpec) compile(name string, documents map[string]compiledWireContract, limits Limits) (Operation, error) {
	path := "/spec/operations/" + escapePointer(name)
	if !slices.Contains(OperationKindCatalog(), wire.Kind) {
		return Operation{}, fmt.Errorf("%s: unsupported operation kind", path)
	}
	operation := Operation{Kind: wire.Kind, Method: wire.Method, Path: wire.Path, Transport: wire.Transport,
		Headers: wire.Headers, Query: wire.Query, Auth: wire.Auth, Framing: wire.Framing, Input: wire.Input}
	if wire.Session != nil {
		configuration := wire.Session.SessionConfig
		operation.Session = &configuration
	}
	if err := checkSessionTransport(operation, limits); err != nil {
		return Operation{}, fmt.Errorf("%s/session: %w", path, err)
	}
	if wire.Task != nil {
		if wire.Kind != "submit" {
			return Operation{}, fmt.Errorf("%s/task: flow requires a submit operation", path)
		}
		operation.Task = &TaskFlow{Status: wire.Task.Status, Result: wire.Task.Result, Cancel: wire.Task.Cancel, IdempotencyHeader: wire.Task.IdempotencyHeader}
	}
	if wire.Kind == "models" {
		if wire.Models == nil || wire.Method != "GET" || wire.Transport != HTTPJSON || wire.Request != "" || wire.Response == "" {
			return Operation{}, fmt.Errorf("%s: model discovery requires GET HTTP JSON, a response document and explicit models configuration", path)
		}
		operation.Models = &ModelDiscovery{CursorParameter: wire.Models.CursorParameter}
		if err := checkModelCursor(operation); err != nil {
			return Operation{}, fmt.Errorf("%s/models: %w", path, err)
		}
	} else if wire.Models != nil {
		return Operation{}, fmt.Errorf("%s/models: requires models operation kind", path)
	}
	if err := checkOperationEndpoint(name, operation, limits); err != nil {
		return Operation{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := checkOperationFraming(name, operation); err != nil {
		return Operation{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := checkOperationHeaders(name, operation); err != nil {
		return Operation{}, fmt.Errorf("%s: %w", path, err)
	}
	for _, reference := range []struct{ field, document string }{{"request", wire.Request}, {"response", wire.Response}} {
		if reference.document == "" {
			continue
		}
		contract, exists := documents[reference.document]
		if !exists {
			return Operation{}, fmt.Errorf("%s/%s: requires a declared wire document", path, reference.field)
		}
		isStream := reference.field == "response" && (wire.Transport == SSE || wire.Transport == NDJSON)
		if (contract.packageStream != nil) != isStream {
			return Operation{}, fmt.Errorf("%s/%s: document lifecycle does not match the operation transport", path, reference.field)
		}
	}
	return operation, nil
}

func (compiled *Compiled) bindPackageOperations(spec ProtocolSpec, mapping ProtocolMapping) error {
	for _, name := range sortedKeys(mapping.Operations) {
		if _, exists := spec.Operations[name]; !exists {
			return fmt.Errorf("/mapping/operations/%s: missing spec operation", escapePointer(name))
		}
	}
	for _, name := range sortedKeys(spec.Operations) {
		path := "/mapping/operations/" + escapePointer(name)
		binding, exists := mapping.Operations[name]
		if !exists {
			return fmt.Errorf("%s: operation mapping is required", path)
		}
		wire, operation := spec.Operations[name], compiled.operations[name]
		if operation.Kind == "submit" || operation.Kind == "status" || operation.Kind == "result" || operation.Kind == "cancel" {
			if binding.DecodeModels != nil || binding.Request != "" || binding.Response != "" || (operation.Kind == "submit") != (binding.Task != nil) {
				return fmt.Errorf("%s: task operations use their flow's declared programs", path)
			}
			continue
		}
		if binding.Task != nil {
			return fmt.Errorf("%s/task: requires a submit operation", path)
		}
		if operation.Kind == "models" {
			if binding.DecodeModels == nil || binding.Request != "" || binding.Response != "" {
				return fmt.Errorf("%s: model discovery requires only a declared decodeModels program", path)
			}
			continue
		}
		if binding.DecodeModels != nil {
			return fmt.Errorf("%s/decodeModels: requires models operation kind", path)
		}
		if operation.Transport == WebSocket {
			if err := compiled.bindPackageSession(wire, binding, mapping.Directions); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			operation.Request = binding.Request
			compiled.operations[name] = operation
			continue
		}
		if (operation.Transport == SSE || operation.Transport == NDJSON) && binding.Response == "" {
			return fmt.Errorf("%s/response: streaming operations require an event mapping", path)
		}
		for _, reference := range []struct {
			field, document string
			direction       Direction
		}{{"request", wire.Request, binding.Request}, {"response", wire.Response, binding.Response}} {
			if reference.direction == "" && reference.document == "" {
				continue
			}
			entry, exists := mapping.Directions[reference.direction]
			if !exists || reference.document == "" || entry.Document != reference.document {
				return fmt.Errorf("%s/%s: mapping must reference the operation's wire document", path, reference.field)
			}
			isRequest := reference.direction == DecodeRequest || reference.direction == EncodeRequest
			if (reference.field == "request") != isRequest {
				return fmt.Errorf("%s/%s: mapping direction does not match the operation boundary", path, reference.field)
			}
			if reference.field == "response" {
				isStream := operation.Transport == SSE || operation.Transport == NDJSON
				if isEventDirection(reference.direction) != isStream {
					return fmt.Errorf("%s/response: mapping direction does not match the operation transport", path)
				}
			}
		}
		operation.Request, operation.Response = binding.Request, binding.Response
		compiled.operations[name] = operation
	}
	return nil
}
