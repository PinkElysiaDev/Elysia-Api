package builtin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

type packageMigrationGap struct {
	reason    string
	paths     []string
	rejection bool
}

type migrationErrorState struct {
	Type       string                    `json:"type"`
	Text       string                    `json:"text"`
	Conversion *migrationConversionState `json:"conversion,omitempty"`
	Resource   *p.ResourceLimitError     `json:"resource,omitempty"`
	Children   []*migrationErrorState    `json:"children,omitempty"`
}

type migrationConversionState struct {
	CandidateOnly bool                `json:"candidateOnly"`
	Issues        []p.ConversionIssue `json:"issues"`
}

type migrationValueState struct {
	Path   string  `json:"path"`
	Legacy *string `json:"legacy"`
	Draft  *string `json:"draft"`
}

type migrationGapState struct {
	LegacyError *migrationErrorState  `json:"legacyError"`
	DraftError  *migrationErrorState  `json:"draftError"`
	Values      []migrationValueState `json:"values"`
}

// Reuse the shipped image request shapes; exercise URI edges on one shape,
// with one ordinary Responses image proving its declaration uses the same ref.
func checkPackageMediaURIs(t *testing.T, legacy, draft *p.Compiled, definition p.Definition) {
	t.Helper()
	var sample p.Sample
	for _, candidate := range definition.Samples {
		if candidate.ID == "image-decode" {
			sample = candidate
			break
		}
	}
	if sample.ID == "" {
		t.Fatal("missing shipped image request shape")
	}
	for _, fixture := range []struct{ protocol, name, path, raw string }{
		{"openai-chat-completions", "empty-mime", "/messages/0/content/0/image_url/url", `"data:;base64,AAAA"`},
		{"openai-chat-completions", "invalid-marker", "/messages/0/content/0/image_url/url", `"data:image/png;base64;charset=utf-8,AAAA"`},
		{"openai-chat-completions", "payload-comma", "/messages/0/content/0/image_url/url", `"data:image/png;base64,AAAA,tail"`},
		{"openai-chat-completions", "null", "/messages/0/content/0/image_url/url", `null`},
		{"openai-chat-completions", "missing", "/messages/0/content/0/image_url/url", ""},
		{"openai-responses", "base64", "/input/0/content/0/image_url", `"data:image/png;base64,AAAA"`},
	} {
		if fixture.protocol != definition.ID {
			continue
		}
		t.Run("media-uri/"+fixture.name, func(t *testing.T) {
			mutation := p.Mutation{Op: p.DeleteValue, Path: fixture.path}
			if fixture.raw != "" {
				mutation.Op, mutation.Value = p.SetValue, testValue(t, fixture.raw)
			}
			input, err := p.ApplyMutations(sample.Input, []p.Mutation{mutation}, p.DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			current := sample
			current.Input = input
			options := p.EvaluationContext{Scope: sample.Scope, Values: sample.Context}
			want, wantErr := executePackageMigrationSample(t, legacy, current, options, false)
			got, gotErr := executePackageMigrationSample(t, draft, current, options, true)
			gap := packageMigrationGap{}
			if fixture.name == "null" {
				gap = packageMigrationGap{reason: "null media URL is rejected, but codec and DSL diagnostics differ", rejection: true}
			}
			checkPackageMigration(t, want, got, wantErr, gotErr, gap)
		})
	}
}

func checkPackageMigration(t *testing.T, want, got []byte, wantErr, gotErr error, gap packageMigrationGap) {
	t.Helper()
	isEqual := bytes.Equal(got, want) && migrationError(gotErr) == migrationError(wantErr)
	if gap.reason == "" {
		if !isEqual {
			t.Fatalf("unexpected migration difference %v: old=%v, draft=%v\nold: %s\ndraft: %s", migrationDifference(want, got), wantErr, gotErr, want, got)
		}
		return
	}
	if isEqual {
		t.Fatalf("recorded migration blocker is now closed; update its explicit ledger: %s", gap.reason)
	}
	if gap.paths != nil && (wantErr != nil || gotErr != nil || !reflect.DeepEqual(gap.paths, migrationDifference(want, got))) {
		t.Fatalf("unregistered differences beyond %s: %v; old=%v; draft=%v", gap.reason, migrationDifference(want, got), wantErr, gotErr)
	}
	if gap.rejection && (wantErr == nil || gotErr == nil) {
		t.Fatalf("rejection acceptance changed beyond diagnostic gap: old=%v; draft=%v", wantErr, gotErr)
	}
	state := migrationGapState{LegacyError: describeMigrationError(wantErr), DraftError: describeMigrationError(gotErr), Values: describeMigrationValues(t, want, got, gap.paths)}
	checkMigrationGapState(t, state)
	t.Logf("NOT CLOSED: %s; old error=%v; draft error=%v; differences=%v", gap.reason, wantErr, gotErr, migrationDifference(want, got))
}

func checkMigrationGapState(t *testing.T, actual migrationGapState) {
	t.Helper()
	raw, err := os.ReadFile("testdata/package-migration-gaps.json")
	if err != nil {
		t.Fatal(err)
	}
	var ledger struct {
		Cases map[string]migrationGapState `json:"cases"`
	}
	if err := json.Unmarshal(raw, &ledger); err != nil {
		t.Fatal(err)
	}
	expected, exists := ledger.Cases[t.Name()]
	if !exists {
		t.Fatal("migration difference has no reviewed static snapshot")
	}
	if !reflect.DeepEqual(actual, expected) {
		got, _ := json.Marshal(actual)
		want, _ := json.Marshal(expected)
		t.Fatalf("unregistered migration error/value change:\nactual: %s\nrecorded: %s", got, want)
	}
}

func executePackageMigrationSample(t *testing.T, compiled *p.Compiled, sample p.Sample, options p.EvaluationContext, isPackage bool) ([]byte, error) {
	t.Helper()
	switch sample.Direction {
	case p.DecodeRequest:
		request, err := compiled.DecodeRequest(t.Context(), sample.Input.Bytes(), options)
		if err != nil {
			return nil, err
		}
		// Version 2 is the new core contract. All business fields and Native
		// evidence remain in the comparison, including number spelling.
		request.SchemaVersion = p.ProtocolCoreVersion
		value, err := p.EncodeValue(request)
		return value.Bytes(), err
	case p.EncodeRequest:
		var request p.Request
		if err := sample.Input.Decode(&request); err != nil {
			return nil, err
		}
		if isPackage {
			request.SchemaVersion = p.ProtocolCoreVersion
		}
		return compiled.EncodeRequest(t.Context(), &request, options)
	case p.DecodeResponse:
		response, err := compiled.DecodeResponse(t.Context(), sample.Input.Bytes(), options)
		if err != nil {
			return nil, err
		}
		response.SchemaVersion = p.ProtocolCoreVersion
		value, err := p.EncodeValue(response)
		return value.Bytes(), err
	case p.EncodeResponse:
		var response p.Response
		if err := sample.Input.Decode(&response); err != nil {
			return nil, err
		}
		if isPackage {
			response.SchemaVersion = p.ProtocolCoreVersion
		}
		return compiled.EncodeResponse(t.Context(), &response, options)
	default:
		t.Fatal("unexpected migration direction", sample.Direction)
		return nil, nil
	}
}

func migrationError(err error) string {
	raw, encodingErr := json.Marshal(describeMigrationError(err))
	if encodingErr != nil {
		panic(encodingErr)
	}
	return string(raw)
}

func describeMigrationError(err error) *migrationErrorState {
	if err == nil {
		return nil
	}
	state := &migrationErrorState{Type: fmt.Sprintf("%T", err), Text: err.Error()}
	switch failure := err.(type) {
	case *p.ConversionError:
		state.Conversion = &migrationConversionState{CandidateOnly: failure.CandidateOnly, Issues: failure.Issues}
	case *p.ResourceLimitError:
		state.Resource = failure
	}
	switch cause := err.(type) {
	case interface{ Unwrap() []error }:
		for _, child := range cause.Unwrap() {
			state.Children = append(state.Children, describeMigrationError(child))
		}
	case interface{ Unwrap() error }:
		if child := cause.Unwrap(); child != nil {
			state.Children = []*migrationErrorState{describeMigrationError(child)}
		}
	}
	return state
}

func describeMigrationValues(t *testing.T, want, got []byte, paths []string) []migrationValueState {
	t.Helper()
	var values []migrationValueState
	for _, path := range paths {
		values = append(values, migrationValueState{Path: path, Legacy: migrationPointerValue(t, want, path), Draft: migrationPointerValue(t, got, path)})
	}
	return values
}

func migrationPointerValue(t *testing.T, raw []byte, pointer string) *string {
	t.Helper()
	value, err := p.ParseValue(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		if value.IsObject() {
			fields, err := value.ReadObject()
			if err != nil {
				t.Fatal(err)
			}
			value = fields[token]
		} else {
			var items []p.Value
			index, err := strconv.Atoi(token)
			if err != nil || value.Decode(&items) != nil || index < 0 || index >= len(items) {
				t.Fatalf("invalid migration snapshot pointer %s", pointer)
			}
			value = items[index]
		}
	}
	if value.IsZero() {
		return nil
	}
	text := value.JSONString()
	return &text
}

func migrationDifference(want, got []byte) []string {
	if bytes.Equal(want, got) {
		return nil
	}
	left, leftErr := p.ParseValue(want)
	right, rightErr := p.ParseValue(got)
	if leftErr != nil || rightErr != nil {
		return []string{"/"}
	}
	var paths []string
	var visit func(p.Value, p.Value, string)
	visit = func(left, right p.Value, path string) {
		if left == right {
			return
		}
		before := len(paths)
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
			for key := range keys {
				visit(first[key], second[key], path+"/"+strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1"))
			}
		} else if left.IsArray() && right.IsArray() {
			var first, second []p.Value
			_ = left.Decode(&first)
			_ = right.Decode(&second)
			if len(first) == len(second) {
				for index := range first {
					visit(first[index], second[index], fmt.Sprintf("%s/%d", path, index))
				}
			}
		}
		if len(paths) == before {
			paths = append(paths, path)
		}
	}
	visit(left, right, "")
	sort.Strings(paths)
	return paths
}
