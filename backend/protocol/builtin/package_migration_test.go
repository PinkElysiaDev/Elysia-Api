package builtin

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

type packageMigrationGap struct {
	reason, errorText string
	paths             []string
	rejection         bool
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
	if gap.errorText != "" && (wantErr != nil || gotErr == nil || !strings.Contains(gotErr.Error(), gap.errorText)) {
		t.Fatalf("unexpected failure beyond %s: old=%v; draft=%v", gap.reason, wantErr, gotErr)
	}
	if gap.rejection && (wantErr == nil || gotErr == nil) {
		t.Fatalf("rejection acceptance changed beyond diagnostic gap: old=%v; draft=%v", wantErr, gotErr)
	}
	if gap.rejection {
		t.Logf("old diagnostics=%s; draft diagnostics=%s", migrationError(wantErr), migrationError(gotErr))
	}
	t.Logf("NOT CLOSED: %s; old error=%v; draft error=%v; differences=%v", gap.reason, wantErr, gotErr, migrationDifference(want, got))
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
	if err == nil {
		return ""
	}
	var conversion *p.ConversionError
	if errors.As(err, &conversion) {
		value, _ := p.EncodeValue(conversion.Issues)
		return fmt.Sprintf("%T: %s", err, value.JSONString())
	}
	return fmt.Sprintf("%T: %v", err, err)
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
