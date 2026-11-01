package builtin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

type packageMigrationGap struct {
	reason                   string
	paths                    []string
	rejection                bool
	isApprovedRepresentation bool
}

func compileBuiltinPackage(t *testing.T, name string) *p.Compiled {
	t.Helper()
	read := func(part string) []byte {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join("packages", name, part+".json"))
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	compiler, err := p.NewCompiler(p.DefaultLimits(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	compiled, issues := compiler.CompileSources(read("spec"), read("mapping"))
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	return compiled
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

func checkPackageTrackingUsers(t *testing.T, legacy, draft *p.Compiled) {
	t.Helper()
	for _, fixture := range []struct {
		name, parameters, rejection string
		isRaw                       bool
	}{
		{"absent", `{}`, "", false},
		{"user-only", `{"user":"current"}`, "", false},
		{"metadata-empty", `{"anthropic_metadata":{}}`, "", false},
		{"metadata-null", `{"anthropic_metadata":null}`, "", false},
		{"tracking-user", `{"anthropic_metadata":{"user_id":"tracking"}}`, "", false},
		{"tracking-null", `{"anthropic_metadata":{"user_id":null}}`, "", false},
		{"same-null", `{"user":null,"anthropic_metadata":{"user_id":null}}`, "", false},
		{"same-string-spelling", `{"user":"\\path\/user","anthropic_metadata":{"user_id":"\\path/user"}}`, "", false},
		{"same-unicode", `{"user":"\u0075ser","anthropic_metadata":{"user_id":"user"}}`, "", false},
		{"current-user-priority", `{"user":"current","anthropic_metadata":{}}`, "", false},
		{"conflicting-user", `{"user":"current","anthropic_metadata":{"user_id":"other"}}`, "conflict", false},
		{"null-user-conflict", `{"user":null,"anthropic_metadata":{"user_id":"other"}}`, "conflict", false},
		{"null-tracking-conflict", `{"user":"current","anthropic_metadata":{"user_id":null}}`, "conflict", false},
		{"unknown-member", `{"anthropic_metadata":{"future":null}}`, "unknown", false},
		{"unknown-before-conflict", `{"user":"current","anthropic_metadata":{"future":false,"user_id":"other"}}`, "unknown", false},
		{"raw-string-equivalent", `{"user":"\u0075ser","anthropic_metadata":{"user_id":"user"}}`, "", true},
		{"raw-number-identical", `{"user":1,"anthropic_metadata":{"user_id":1}}`, "", true},
		{"raw-number-spelling", `{"user":1,"anthropic_metadata":{"user_id":1.0}}`, "conflict", true},
		{"raw-object-identical", `{"user":{"a":1,"b":2},"anthropic_metadata":{"user_id":{"a":1,"b":2}}}`, "", true},
		{"raw-object-order", `{"user":{"a":1,"b":2},"anthropic_metadata":{"user_id":{"b":2,"a":1}}}`, "conflict", true},
		{"raw-metadata-string", `{"anthropic_metadata":"ignored"}`, "", true},
		{"raw-metadata-number", `{"anthropic_metadata":1}`, "", true},
		{"raw-metadata-boolean", `{"anthropic_metadata":false}`, "", true},
		{"raw-metadata-array", `{"anthropic_metadata":[]}`, "", true},
	} {
		t.Run("tracking-user/"+fixture.name, func(t *testing.T) {
			var want, got []byte
			var wantErr, gotErr error
			if fixture.isRaw {
				encode := func(compiled *p.Compiled) ([]byte, error) {
					input := testValue(t, `{"schemaVersion":`+strconv.Itoa(compiled.CoreVersion())+`,"model":"m","content":[],"parameters":`+fixture.parameters+`}`)
					output, issues := compiled.Execute(t.Context(), p.EncodeRequest, input, p.EvaluationContext{})
					return output.Bytes(), p.IssuesError(issues)
				}
				want, wantErr = encode(legacy)
				got, gotErr = encode(draft)
			} else {
				parameters, err := testValue(t, fixture.parameters).ReadObject()
				if err != nil {
					t.Fatal(err)
				}
				request := &p.Request{SchemaVersion: p.SemanticSchemaVersion, Model: p.StringValue("m"), Content: []p.Node{}, Parameters: parameters}
				want, wantErr = legacy.EncodeRequest(t.Context(), request, p.EvaluationContext{})
				request.SchemaVersion = p.ProtocolCoreVersion
				got, gotErr = draft.EncodeRequest(t.Context(), request, p.EvaluationContext{})
			}
			if fixture.rejection == "" {
				if wantErr != nil {
					t.Fatal("legacy tracking fixture must encode", wantErr)
				}
				checkPackageMigration(t, want, got, wantErr, gotErr, packageMigrationGap{})
				return
			}
			checkTrackingUserRejection(t, wantErr, gotErr, fixture.rejection)
		})
	}
	t.Run("tracking-user/typed-whitespace-null", func(t *testing.T) {
		request := &p.Request{SchemaVersion: p.SemanticSchemaVersion, Model: p.StringValue("m"), Content: []p.Node{}, Parameters: p.Object{
			"user": testValue(t, " null "), "anthropic_metadata": testValue(t, `{"user_id":null}`),
		}}
		want, wantErr := legacy.EncodeRequest(t.Context(), request, p.EvaluationContext{})
		request.SchemaVersion = p.ProtocolCoreVersion
		got, gotErr := draft.EncodeRequest(t.Context(), request, p.EvaluationContext{})
		if wantErr != nil {
			t.Fatal("equivalent null identities must encode", wantErr)
		}
		checkPackageMigration(t, want, got, wantErr, gotErr, packageMigrationGap{})
		fields, err := testValue(t, string(got)).ReadObject()
		if err != nil || !fields["user"].IsNull() || request.Parameters["user"].JSONString() != " null " {
			t.Fatal("null identity encoding changed its value or caller input", err, string(got))
		}
	})
}

func checkTrackingUserRejection(t *testing.T, wantErr, gotErr error, rejection string) {
	t.Helper()
	oldPath, newPath, reason, rule := "/parameters/user", "/parameters/user", "conflicting user identities", "conflicting-user-identities"
	if rejection == "unknown" {
		oldPath, newPath = "/parameters/anthropic_metadata/future", "/parameters/anthropic_metadata"
		reason, rule = "unknown metadata requires explicit mapping", "unknown-tracking-metadata"
	}
	var oldFailure, newFailure *p.ConversionError
	if !errors.As(wantErr, &oldFailure) || len(oldFailure.Issues) != 1 || oldFailure.Issues[0].Code != p.UnsupportedCapability || oldFailure.Issues[0].Stage != "wire" || oldFailure.Issues[0].Path != oldPath || oldFailure.Issues[0].Reason != reason ||
		!errors.As(gotErr, &newFailure) || len(newFailure.Issues) != 1 || newFailure.Issues[0].Code != p.ConversionRejected || newFailure.Issues[0].Stage != "mapping.effects" || newFailure.Issues[0].Path != newPath || newFailure.Issues[0].Reason != reason || newFailure.Issues[0].RuleID != rule {
		t.Fatal("tracking metadata rejection changed", wantErr, gotErr)
	}
	// These new fixtures expose the existing effects/renderer boundary instead
	// of changing the reviewed snapshots for unrelated migration differences.
	t.Log("NOT CLOSED: metadata rejection diagnostics differ; renderer-wide first-error order remains unverified", migrationError(wantErr), migrationError(gotErr))
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
			if fixture.name == "null" {
				issue := requirePackageIssue(t, gotErr, p.InvalidInput)
				if issue.Direction != p.DecodeRequest || issue.Path != "/mapping/expressions/decode-media-url/then/source" {
					t.Fatal("invalid media URL lost its mapping location", issue)
				}
				return
			}
			checkPackageMigration(t, want, got, wantErr, gotErr, packageMigrationGap{})
		})
	}
}

func requirePackageIssue(t *testing.T, err error, code p.IssueCode) p.ConversionIssue {
	t.Helper()
	var failure *p.ConversionError
	if !errors.As(err, &failure) || len(failure.Issues) != 1 || failure.Issues[0].Code != code {
		t.Fatalf("expected one %s diagnostic, got %v", code, err)
	}
	return failure.Issues[0]
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
	if gap.isApprovedRepresentation {
		if gap.rejection || gap.paths == nil {
			t.Fatal("approved representation requires exact successful value differences")
		}
		t.Logf("APPROVED REPRESENTATION: %s; exact old/new values remain checked", gap.reason)
		return
	}
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

// Approved source representations may differ internally, but must not change
// the encoded response or let an old snapshot undo a current model edit.
func checkMigrationReencoding(t *testing.T, legacy, draft *p.Compiled, sample p.Sample, options p.EvaluationContext) {
	t.Helper()
	for _, edit := range []string{"native", "without-root-native", "model"} {
		encode := func(compiled *p.Compiled) ([]byte, error) {
			if sample.Direction == p.DecodeRequest {
				request, err := compiled.DecodeRequestValue(t.Context(), sample.Input, options)
				if err != nil {
					return nil, err
				}
				if edit == "without-root-native" {
					request.Native = nil
				} else if edit == "model" {
					request.Model = p.StringValue("edited-model")
				}
				return compiled.EncodeRequest(t.Context(), request, options)
			}
			response, err := compiled.DecodeResponse(t.Context(), sample.Input.Bytes(), options)
			if err != nil {
				return nil, err
			}
			if edit == "without-root-native" {
				response.Native = nil
			} else if edit == "model" {
				response.Model = p.StringValue("edited-model")
			}
			return compiled.EncodeResponse(t.Context(), response, options)
		}
		t.Run("source-reencoding/"+edit, func(t *testing.T) {
			want, wantErr := encode(legacy)
			got, gotErr := encode(draft)
			checkPackageMigration(t, want, got, wantErr, gotErr, packageMigrationGap{})
		})
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
		if !isPackage {
			migrateFixtureStorage(t, request, compiled.Codec(p.DecodeRequest))
			migrateFixtureInclude(t, request)
		}
		value, err := p.EncodeValue(request)
		return value.Bytes(), err
	case p.EncodeRequest:
		var request p.Request
		if err := sample.Input.Decode(&request); err != nil {
			return nil, err
		}
		if isPackage {
			request.SchemaVersion = p.ProtocolCoreVersion
			migrateFixtureInclude(t, &request)
			if !request.Parameters["store"].IsZero() {
				codec := map[string]string{"openai_chat": Chat, "openai_responses": Responses}[compiled.Identity().Family]
				migrateFixtureStorage(t, &request, codec)
			}
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

// Historical fixtures retain their original wire expectation. Move their known
// storage field into the new owner explicitly, rather than ignoring ClientOutput.
func migrateFixtureStorage(t *testing.T, request *p.Request, codec string) {
	t.Helper()
	storage := &p.ResponsesStorageIntent{SourceCodec: codec}
	switch codec {
	case Chat, Responses:
		storage.Raw = request.Parameters["store"]
		storage.Requested = codec == Responses
		storage.RequestedUnknown = storage.Raw.IsNull()
		storage.EffectiveUnknown = true
		if !storage.Raw.IsZero() {
			storage.Requested = false
			if !storage.Raw.IsNull() {
				if err := storage.Raw.Decode(&storage.Requested); err != nil {
					t.Fatal("legacy storage fixture is not a boolean or null", err)
				}
			}
		}
		delete(request.Parameters, "store")
	case Anthropic, Gemini:
	default:
		t.Fatal("fixture has no declared storage contract", codec)
	}
	if request.ClientOutput == nil {
		request.ClientOutput = &p.ClientOutput{}
	}
	request.ClientOutput.ResponsesStorage = storage
}

// Old fixtures stored the same delivery selectors under generation parameters.
// Move only that known field; retain null, unknown selectors and raw spelling.
func migrateFixtureInclude(t *testing.T, request *p.Request) {
	t.Helper()
	raw := request.Parameters["responses_include"]
	if raw.IsZero() {
		return
	}
	if request.ClientOutput == nil {
		request.ClientOutput = &p.ClientOutput{}
	}
	request.ClientOutput.RawResponsesInclude = raw
	if !raw.IsNull() {
		var selectors []string
		if err := raw.Decode(&selectors); err != nil {
			t.Fatal("historical include fixture is not an array or null", err)
		}
		for _, selector := range selectors {
			if selector == "reasoning.encrypted_content" {
				request.ClientOutput.RequireContinuationState = true
			}
		}
	}
	delete(request.Parameters, "responses_include")
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
