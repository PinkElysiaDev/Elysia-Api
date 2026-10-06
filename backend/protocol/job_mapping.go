package protocol

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"golang.org/x/net/http/httpguts"
)

// TaskControl builds only a body and query parameters for a fixed operation;
// it cannot select an arbitrary host, path, authorization header or credential.
type TaskControl struct {
	Body  Value             `json:"body,omitzero"`
	Query map[string]string `json:"query,omitempty"`
}

func (compiler *Compiler) compileTaskMappings(compiled *Compiled, definition Definition, expressions *expressionCompiler) error {
	compiled.taskMappings = map[string]map[string]compiledMapping{}
	for name, operation := range definition.Operations {
		if operation.Kind != "submit" {
			continue
		}
		flow := operation.Task
		if flow == nil || operation.Transport != HTTPJSON || !definition.Capabilities[AsyncJobsCapability] {
			return fmt.Errorf("submit requires an HTTP JSON task flow and tasks.async capability")
		}
		if !hasDefinitionDirections(definition, DecodeRequest, EncodeRequest, DecodeResponse, EncodeResponse) {
			return fmt.Errorf("task flows require request and result adapters in both directions")
		}
		for purpose, reference := range map[string]string{"status": flow.Status, "result": flow.Result, "cancel": flow.Cancel} {
			if purpose == "cancel" && reference == "" {
				continue
			}
			linked, exists := definition.Operations[reference]
			if !exists || linked.Kind != purpose || linked.Transport != HTTPJSON {
				return fmt.Errorf("task %s reference must name a matching HTTP JSON operation", purpose)
			}
			if !strings.Contains(linked.Path, "{taskId}") {
				return fmt.Errorf("task control endpoint must identify the gateway job with {taskId}")
			}
		}
		if header := flow.IdempotencyHeader; header != "" {
			if isCredentialField(header) || isTransportHeader(header) || !httpguts.ValidHeaderFieldName(header) || strings.EqualFold(header, operation.Auth.Name) {
				return fmt.Errorf("invalid task idempotency header")
			}
			for key := range operation.Headers {
				if strings.EqualFold(key, http.CanonicalHeaderKey(header)) {
					return fmt.Errorf("task idempotency header conflicts with static header")
				}
			}
		}
		compiled.taskMappings[name] = map[string]compiledMapping{}
		for kind, mapping := range map[string]Mapping{"decode": flow.Decode, "encode": flow.Encode, "control": flow.Control} {
			if mapping.Module != "" || len(mapping.Rules) > 0 || mapping.Transform == nil {
				return fmt.Errorf("task %s requires a declarative transform", kind)
			}
			direction := EncodeResponse
			if kind == "decode" {
				direction = DecodeResponse
			}
			entry, err := compiler.compileMapping(mapping, "/operations/"+name+"/task/"+kind, direction, expressions)
			if err != nil {
				return err
			}
			compiled.taskMappings[name][kind] = entry
		}
	}
	seen := map[string]bool{}
	for _, sample := range definition.TaskSamples {
		if sample.ID == "" || seen[sample.ID] || compiled.taskMappings[sample.Operation] == nil || !slices.Contains([]string{"decode", "encode", "control"}, sample.Kind) || !slices.Contains([]string{"submit", "status", "result", "cancel"}, sample.Purpose) || sample.Input.IsZero() || sample.Expected.IsZero() {
			return fmt.Errorf("invalid task sample identity, operation, direction or assertion")
		}
		seen[sample.ID] = true
	}
	return nil
}

// ConvertTask uses the compiled mapping evaluator shared with all adapters.
// Purpose is gateway-owned metadata and cannot be supplied by a client body.
func (compiled *Compiled) ConvertTask(ctx context.Context, operation, kind, purpose string, input Value) (Value, error) {
	mapping, exists := compiled.taskMappings[operation][kind]
	if !exists {
		return Value{}, fmt.Errorf("task mapping is unavailable")
	}
	direction := EncodeResponse
	if kind == "decode" {
		direction = DecodeResponse
	}
	return compiled.executeOperationMapping(ctx, mapping, direction, "/operations/"+operation+"/task/"+kind, input, EvaluationContext{Values: Object{"operation": StringValue(purpose)}})
}

// DecodeTaskUpdate validates provider state without inventing identities or
// treating missing usage as observed zero. State transitions are checked later.
func (compiled *Compiled) DecodeTaskUpdate(ctx context.Context, operation, purpose string, input Value) (JobUpdate, error) {
	value, err := compiled.ConvertTask(ctx, operation, "decode", purpose, input)
	var update JobUpdate
	if err != nil {
		return update, err
	}
	if err := decodeContract(value.Bytes(), &update); err != nil {
		return update, err
	}
	if err := checkTaskUpdate(update, false); err != nil {
		return update, err
	}
	return update, nil
}

func checkTaskUpdate(update JobUpdate, isReceipt bool) error {
	id, err := readString(update.UpstreamID)
	if err != nil || strings.TrimSpace(id) == "" {
		return streamIssue(UpstreamContractViolation, "/task/id", "task identity must be a nonempty string")
	}
	allowed := []TaskStatus{TaskQueued, TaskRunning, TaskCompleted, TaskFailed, TaskCancelled}
	if isReceipt {
		allowed = append(allowed, TaskSubmitting, TaskUncertain)
	}
	if !slices.Contains(allowed, update.Status) {
		return streamIssue(UpstreamContractViolation, "/task/status", "unknown task state")
	}
	check := capabilityCheck{target: Target{Capabilities: CapabilitySet{UsageCapability: true}}, limits: DefaultLimits()}
	check.usage(update.Usage, "/task/usage")
	return IssuesError(check.issues)
}

// EncodeTaskReceipt exposes the gateway identity in the declared client shape.
// Upstream account identities, pinned credentials and internal lease data stay
// outside this mapping input.
func (compiled *Compiled) EncodeTaskReceipt(ctx context.Context, operation, purpose string, receipt JobUpdate) (Value, error) {
	if err := checkTaskUpdate(receipt, true); err != nil {
		return Value{}, err
	}
	value, err := EncodeValue(receipt)
	if err != nil {
		return Value{}, err
	}
	return compiled.ConvertTask(ctx, operation, "encode", purpose, value)
}

// EncodeTaskControl encodes a fixed provider operation using its upstream ID.
func (compiled *Compiled) EncodeTaskControl(ctx context.Context, operation, purpose, id string) (TaskControl, error) {
	input, err := EncodeValue(Object{"id": StringValue(id)})
	if err != nil {
		return TaskControl{}, err
	}
	output, err := compiled.ConvertTask(ctx, operation, "control", purpose, input)
	var control TaskControl
	if err != nil {
		return control, err
	}
	if err := decodeContract(output.Bytes(), &control); err != nil {
		return control, err
	}
	return control, nil
}

// ApplyTaskControl merges declared dynamic query parameters without overriding
// static routing/authentication. HTTP construction subsequently injects secrets.
func ApplyTaskControl(operation Operation, control TaskControl) (Operation, error) {
	query := map[string]string{}
	for key, value := range operation.Query {
		query[key] = value
	}
	for key, value := range control.Query {
		if _, exists := query[key]; exists || (operation.Auth.Location == "query" && key == operation.Auth.Name) {
			return operation, fmt.Errorf("task query conflicts with a static or credential field")
		}
		query[key] = value
	}
	operation.Query = query
	return operation, nil
}
