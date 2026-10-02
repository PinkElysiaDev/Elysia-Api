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

// PreviewInput selects a conversion or a complete declared workflow fixture.
// Both editor and Agent send this contract to the same authoring service.
type PreviewInput struct {
	Definition Value     `json:"definition"`
	Direction  Direction `json:"direction,omitempty"`
	Input      Value     `json:"input,omitzero"`
	Sequence   bool      `json:"sequence,omitempty"`
	Mode       string    `json:"mode,omitempty"`
	Sample     string    `json:"sample,omitempty"`
	Operation  string    `json:"operation,omitempty"`
	Kind       string    `json:"kind,omitempty"`
	Purpose    string    `json:"purpose,omitempty"`
}

// PreviewWorkflow executes request/response/event mappings, a mixed session
// trace, or one task mapping without saving or enabling a protocol revision.
func (service *Service) PreviewWorkflow(ctx context.Context, input PreviewInput) PreviewResult {
	if input.Mode == "" || input.Mode == "mapping" {
		return service.Preview(ctx, input.Definition.Bytes(), input.Direction, input.Input, input.Sequence, EvaluationContext{})
	}
	compiled, issues := service.compiler.Compile(input.Definition.Bytes())
	if IssuesError(issues) != nil {
		return PreviewResult{Issues: issues}
	}
	var semantic any
	var output Value
	var err error
	switch input.Mode {
	case "task":
		output, err = executeTaskSample(ctx, compiled, TaskSample{ID: "preview", Operation: input.Operation, Kind: input.Kind, Purpose: input.Purpose, Input: input.Input})
		semantic = input.Input
	case "session":
		var sample *SessionSample
		for _, entry := range compiled.Definition().SessionSamples {
			if entry.ID == input.Sample {
				sample = &entry
				break
			}
		}
		if sample == nil {
			err = fmt.Errorf("session preview requires an existing sample")
			break
		}
		var evidence sessionEvidence
		evidence, err = executeSessionSample(ctx, compiled, *sample)
		semantic = evidence.events
		if err == nil {
			output, err = EncodeValue(evidence.checks)
		}
	default:
		err = fmt.Errorf("unknown preview mode")
	}
	if err != nil {
		return PreviewResult{Issues: sampleIssues(compiled, Sample{ID: input.Sample}, "/preview", err)}
	}
	value, err := EncodeValue(semantic)
	if err != nil {
		return PreviewResult{Issues: sampleIssues(compiled, Sample{}, "/preview", err)}
	}
	return PreviewResult{Output: output, Semantic: value, Issues: []ConversionIssue{}}
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

// LoadRevision restores a pinned durable operation without consulting today's
// active pointer. Current-engine evidence is still required after an upgrade.
func (service *Service) LoadRevision(ctx context.Context, id, hash string) (*Compiled, error) {
	if active, exists := service.Pin(id); exists && active.Hash() == hash {
		return active, nil
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	key := id + "/" + hash
	if cached := service.retained[key]; cached != nil {
		return cached, nil
	}
	compiled, err := service.loadVerifiedRevision(ctx, id, hash)
	if err != nil {
		return nil, err
	}
	if service.retained == nil {
		service.retained = map[string]*Compiled{}
	}
	size := len(compiled.definition.raw)
	for len(service.retainedOrder) > 0 && (service.retainedBytes+size > service.compiler.limits.BufferBytes || len(service.retained) >= service.compiler.limits.StateItems) {
		oldest := service.retainedOrder[0]
		service.retainedOrder = service.retainedOrder[1:]
		service.retainedBytes -= len(service.retained[oldest].definition.raw)
		delete(service.retained, oldest)
	}
	service.retained[key] = compiled
	service.retainedOrder = append(service.retainedOrder, key)
	service.retainedBytes += size
	return compiled, nil
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
