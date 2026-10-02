package protocol

import (
	"context"
	"fmt"
)

// PreviewResult exposes the same typed conversion used by verification and
// forwarding, including provenance and diagnostics for the editor and Agent.
type PreviewResult struct {
	Output   Value             `json:"output,omitzero"`
	Semantic Value             `json:"semantic,omitzero"`
	Issues   []ConversionIssue `json:"issues"`
}

// Preview validates an unsaved definition and executes its typed adapter.
func (service *Service) Preview(ctx context.Context, raw []byte, direction Direction, input Value, isSequence bool, options EvaluationContext) PreviewResult {
	compiled, issues := service.compiler.Compile(raw)
	if IssuesError(issues) != nil {
		return PreviewResult{Issues: issues}
	}
	sample := Sample{ID: "preview", Direction: direction, Input: input, Sequence: isSequence, Context: options.Values, Scope: options.Scope}
	result, err := executeVerificationSample(ctx, compiled, sample)
	if err != nil {
		return PreviewResult{Issues: sampleIssues(compiled, sample, "/preview", err)}
	}
	semantic, err := EncodeValue(result.semantic)
	if err != nil {
		return PreviewResult{Issues: sampleIssues(compiled, sample, "/preview", err)}
	}
	output := result.output
	if direction == DecodeRequest || direction == DecodeResponse || isEventDecoder(direction) {
		output = semantic
	}
	return PreviewResult{Output: output, Semantic: semantic, Issues: []ConversionIssue{}}
}

// VerifyRevision refreshes offline evidence for an immutable rollback candidate
// using the currently installed compiler, without changing the author's draft.
func (service *Service) VerifyRevision(ctx context.Context, id, hash string) (VerificationReport, error) {
	revision, err := service.repository.ReadProtocolRevision(ctx, id, hash)
	if err != nil {
		return VerificationReport{}, err
	}
	compiled, issues := service.compiler.Compile(revision.Definition.Bytes())
	if err := IssuesError(issues); err != nil {
		return VerificationReport{}, err
	}
	if compiled.hash != hash || compiled.identity.DefinitionID != id {
		return VerificationReport{}, fmt.Errorf("stored revision identity/hash mismatch")
	}
	report := Verify(ctx, compiled)
	if err := ctx.Err(); err != nil {
		return VerificationReport{}, err
	}
	if err := service.repository.SaveProtocolReport(ctx, id, hash, report); err != nil {
		return VerificationReport{}, err
	}
	return report, nil
}

// ReadRevision exposes the selected immutable definition.
func (service *Service) ReadRevision(ctx context.Context, id, hash string) (Revision, error) {
	return service.repository.ReadProtocolRevision(ctx, id, hash)
}

// ReadReport returns current-engine offline evidence for the selected revision.
func (service *Service) ReadReport(ctx context.Context, id, hash string) (VerificationReport, error) {
	return service.repository.ReadProtocolReport(ctx, id, hash)
}

// Activations returns persisted revision pointers for management views.
func (service *Service) Activations(ctx context.Context) ([]Activation, error) {
	return service.repository.ListProtocolActivations(ctx)
}

// RevisionChange describes one field replacement without mutating either side.
type RevisionChange struct {
	Path   string `json:"path"`
	Before Value  `json:"before,omitzero"`
	After  Value  `json:"after,omitzero"`
}

// Diff compares immutable revisions. Arrays are shown as explicit replacements
// so a review never mistakes reorder/deletion for independent object edits.
func (service *Service) Diff(ctx context.Context, id, beforeHash, afterHash string) ([]RevisionChange, error) {
	before, err := service.repository.ReadProtocolRevision(ctx, id, beforeHash)
	if err != nil {
		return nil, err
	}
	after, err := service.repository.ReadProtocolRevision(ctx, id, afterHash)
	if err != nil {
		return nil, err
	}
	changes := []RevisionChange{}
	var visit func(string, Value, Value)
	visit = func(path string, left, right Value) {
		if equalValues(left, right) {
			return
		}
		if left.IsObject() && right.IsObject() {
			first, _ := left.ReadObject()
			second, _ := right.ReadObject()
			keys := map[string]bool{}
			for key := range first {
				keys[key] = true
			}
			for key := range second {
				keys[key] = true
			}
			for _, key := range sortedKeys(keys) {
				visit(path+"/"+escapePointer(key), first[key], second[key])
			}
			return
		}
		changes = append(changes, RevisionChange{Path: path, Before: left, After: right})
	}
	visit("", before.Definition, after.Definition)
	return changes, nil
}
