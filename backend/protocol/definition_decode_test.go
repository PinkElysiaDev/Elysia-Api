package protocol

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// Retain the former two-pass decoder only as a rejection/diagnostic oracle.
func legacyExpressionDecode(raw []byte) (Expression, error) {
	text := strings.TrimSpace(string(raw))
	if strings.HasPrefix(text, `"`) {
		var shorthand string
		if err := decodeContractText(text, &shorthand); err != nil {
			return Expression{}, err
		}
		return parseCompactRead(shorthand)
	}
	type plain Expression
	var decoded plain
	if err := decodeContractText(text, &decoded); err != nil {
		return Expression{}, err
	}
	expression := Expression(decoded)
	if text == "null" {
		return expression, nil
	}
	for offset := skipJSONSpace(text, 1); text[offset] != '}'; {
		end := jsonStringEnd(text, offset)
		key := text[offset+1 : end-1]
		if strings.Contains(key, `\`) || !utf8.ValidString(key) {
			var err error
			key, err = decodeObjectKey(text[offset:end])
			if err != nil {
				return Expression{}, err
			}
		}
		if field := expressionFieldBits[key]; field != 0 {
			expression.present |= field
		} else {
			expression.unknownKeys = append(expression.unknownKeys, strings.Clone(key))
		}
		offset = skipJSONSpace(text, skipJSONSpace(text, end)+1)
		offset = skipJSONSpace(text, jsonValueEnd(text, offset))
		if text[offset] == ',' {
			offset = skipJSONSpace(text, offset+1)
		}
	}
	return expression, nil
}

func TestExpressionDecoderMatchesLegacyDiagnostics(t *testing.T) {
	for _, raw := range []string{
		`null`, `{}`, `{"op":"read","path":""}`, `{"op":"literal","value":null}`,
		`{"op":"omit","required":true,"required":false}`, `{"op":"object","fields":{"a":"$input"},"fields":{"b":"$root"}}`,
		`{"op":"literal","value":0}`, `{"Op":"omit","Required":false}`, `{"op":"project","sourCe":{},"keYs":[],"poLiCY":"0"}`,
		`{"op":"omit","unknown":0}`, `{"op":"omit","unknown":null}`, `{"op":"literal","value":1,"value":null}`,
		`[]`, `0`, `true`, `{"op":7}`, `{"required":"false"}`, `{"op":"object","fields":{"x":{"unknown":1}}}`,
		`{"op":"read",}`, `{"op":"read"} {}`, `{"op":"read"`, `{"op":"read","path":"\0"}`, `{"op":"read","path":"line` + "\n" + `break"}`,
	} {
		t.Run(raw, func(t *testing.T) {
			want, wantErr := legacyExpressionDecode([]byte(raw))
			var got Expression
			gotErr := got.UnmarshalJSON([]byte(raw))
			if (gotErr == nil) != (wantErr == nil) {
				t.Fatalf("acceptance differs: got %v; want %v", gotErr, wantErr)
			}
			if wantErr != nil {
				if gotErr.Error() != wantErr.Error() {
					t.Fatalf("diagnostic differs:\ngot %s\nwant %s", gotErr, wantErr)
				}
			} else if !reflect.DeepEqual(got, want) {
				t.Fatalf("decoded values/presence differ: got %#v; want %#v", got, want)
			}
		})
	}
}

func FuzzExpressionDecoderCompatibility(f *testing.F) {
	for _, raw := range []string{`{"op":"literal","value":null}`, `{"op":"object","fields":{}}`, `{"op":"read","required":false}`, `{"Op":"omit","source":null}`} {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 1<<16 {
			t.Skip()
		}
		want, wantErr := legacyExpressionDecode([]byte(raw))
		var got Expression
		gotErr := got.UnmarshalJSON([]byte(raw))
		if (gotErr == nil) != (wantErr == nil) || wantErr != nil && gotErr.Error() != wantErr.Error() || wantErr == nil && !reflect.DeepEqual(got, want) {
			t.Fatalf("decoder compatibility failed for %q: %v / %v", raw, gotErr, wantErr)
		}
	})
}
