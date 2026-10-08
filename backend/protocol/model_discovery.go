package protocol

import (
	"context"
	"fmt"
	"strings"
)

// ModelDiscovery declares a bounded catalog decoder. Pagination only changes
// one declared query parameter; provider responses cannot choose URLs or auth.
type ModelDiscovery struct {
	Decode          Mapping `json:"decode"`
	CursorParameter string  `json:"cursorParameter,omitempty"`
}

// DiscoveredModel contains catalog metadata, never inferred LLM capabilities.
type DiscoveredModel struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	MaxTokens int    `json:"maxTokens,omitempty"`
}

// ModelPage is the result of an independently compiled discovery mapping.
// Empty Next ends pagination; an empty model array is a valid terminal page.
type ModelPage struct {
	Models  []DiscoveredModel `json:"models"`
	Next    string            `json:"next,omitempty"`
	HasMore bool              `json:"hasMore,omitempty"`
}

// ModelSample supplies wire-to-catalog evidence for one discovery operation.
type ModelSample struct {
	ID        string `json:"id"`
	Operation string `json:"operation"`
	Input     Value  `json:"input"`
	Expected  Value  `json:"expected"`
}

var modelPageSchema = ValueSchema{Type: ObjectType, Required: []string{"models"}, Properties: map[string]ValueSchema{
	"models": {Type: ArrayType, Items: &ValueSchema{Type: ObjectType, Required: []string{"id"}, Properties: map[string]ValueSchema{
		"id": {Type: StringType}, "name": {Type: StringType}, "maxTokens": {Type: IntegerType},
	}}},
	"next":    {Type: StringType, Nullable: true},
	"hasMore": {Type: BooleanType},
}}

func (compiler *Compiler) compileModelMappings(compiled *Compiled, definition Definition, expressions *expressionCompiler) error {
	compiled.modelMappings = map[string]compiledMapping{}
	for name, operation := range definition.Operations {
		if operation.Kind != "models" {
			if operation.Models != nil {
				return fmt.Errorf("operation %q: discovery mappings require models kind", name)
			}
			continue
		}
		if operation.Transport != HTTPJSON || operation.Method != "GET" || operation.Models == nil || operation.Request != "" || operation.Response != "" {
			return fmt.Errorf("operation %q: model discovery requires GET HTTP JSON and its own decoder", name)
		}
		discovery := operation.Models
		cursor := discovery.CursorParameter
		if cursor != "" {
			_, isStatic := operation.Query[cursor]
			if strings.TrimSpace(cursor) != cursor || strings.ContainsAny(cursor, "\r\n") || isCredentialField(cursor) || isStatic || (operation.Auth.Location == "query" && cursor == operation.Auth.Name) {
				return fmt.Errorf("operation %q: pagination conflicts with static routing or credentials", name)
			}
		}
		mapping := discovery.Decode
		if mapping.Module != "" || len(mapping.Rules) > 0 || mapping.Transform == nil || mapping.FrameBatch || mapping.After != nil || mapping.UnknownEvent != "" || mapping.Capabilities != nil {
			return fmt.Errorf("operation %q: discovery requires an independent declarative transform", name)
		}
		entry, err := compiler.compileMapping(mapping, "/operations/"+name+"/models/decode", DecodeResponse, expressions)
		if err != nil {
			return err
		}
		compiled.modelMappings[name] = entry
	}
	seen := map[string]bool{}
	for _, sample := range definition.ModelSamples {
		_, exists := compiled.modelMappings[sample.Operation]
		if sample.ID == "" || seen[sample.ID] || !exists || sample.Input.IsZero() || sample.Expected.IsZero() {
			return fmt.Errorf("invalid model sample identity, operation or assertion")
		}
		seen[sample.ID] = true
	}
	return nil
}

// DecodeModelPage uses the same evaluator and resource limits as generation.
// Invalid catalog entries fail the whole page instead of silently disappearing.
func (compiled *Compiled) DecodeModelPage(ctx context.Context, operation string, input Value) (ModelPage, error) {
	var page ModelPage
	mapping, exists := compiled.modelMappings[operation]
	if !exists {
		return page, fmt.Errorf("protocol has no model discovery decoder for %q", operation)
	}
	output, err := compiled.executeOperationMapping(ctx, mapping, DecodeResponse, "/operations/"+operation+"/models/decode", input, EvaluationContext{})
	if err != nil {
		return page, err
	}
	if err := checkValueSchema(output, &modelPageSchema, "/models", 0, compiled.limits); err != nil {
		return page, err
	}
	if err := decodeContract(output.Bytes(), &page); err != nil {
		return page, err
	}
	if page.Models == nil || len(page.Models) > compiled.limits.StateItems {
		return page, fmt.Errorf("model page requires a bounded models array")
	}
	if page.HasMore && strings.TrimSpace(page.Next) == "" {
		return page, fmt.Errorf("model page requires a continuation cursor when hasMore is true")
	}
	seen := map[string]bool{}
	for _, model := range page.Models {
		if strings.TrimSpace(model.ID) == "" || seen[model.ID] || model.MaxTokens < 0 {
			return page, fmt.Errorf("model page contains an empty/duplicate identity or invalid token limit")
		}
		seen[model.ID] = true
	}
	if page.Next != "" && compiled.operations[operation].Models.CursorParameter == "" {
		return page, fmt.Errorf("model page has a next cursor without declared pagination")
	}
	return page, nil
}

func (compiled *Compiled) executeOperationMapping(ctx context.Context, mapping compiledMapping, direction Direction, path string, input Value, options EvaluationContext) (Value, error) {
	view := *compiled
	view.mappings = map[Direction]compiledMapping{direction: mapping}
	output, issues := view.Execute(ctx, direction, input, options)
	for index := range issues {
		issues[index].Path = path + strings.TrimPrefix(issues[index].Path, "/directions/"+string(direction))
	}
	return output, IssuesError(issues)
}

func verifyModelSamples(ctx context.Context, compiled *Compiled, report *VerificationReport) {
	terminal, continuation := map[string]bool{}, map[string]bool{}
	for _, sample := range compiled.Definition().ModelSamples {
		page, err := compiled.DecodeModelPage(ctx, sample.Operation, sample.Input)
		if err == nil {
			actual, encodeErr := EncodeValue(page)
			err = encodeErr
			if err == nil && !equalValues(actual, sample.Expected) {
				err = fmt.Errorf("model sample differs from expected catalog page")
			}
		}
		report.Checks = append(report.Checks, VerificationCheck{SampleID: sample.ID, Passed: err == nil})
		if err != nil {
			report.Issues = append(report.Issues, verificationIssue(compiled, "", "/modelSamples/"+sample.ID, VerificationMismatch, err.Error(), sample.ID))
			continue
		}
		terminal[sample.Operation] = terminal[sample.Operation] || page.Next == ""
		continuation[sample.Operation] = continuation[sample.Operation] || page.Next != ""
	}
	for name := range compiled.modelMappings {
		if !terminal[name] || (compiled.operations[name].Models.CursorParameter != "" && !continuation[name]) {
			report.Issues = append(report.Issues, verificationIssue(compiled, "", "/operations/"+name+"/models", IncompleteCoverage, "discovery requires terminal and, when paginated, continuation page evidence", ""))
		}
	}
}
