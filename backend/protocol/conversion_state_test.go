package protocol

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestConversionStrictLoadMatchesContract(t *testing.T) {
	payload := `"` + strings.Repeat("p", coreDirectDecodeBytes+1) + `"`
	for _, phase := range []ConversionPhase{ConversionIngress, ConversionRequest, ConversionResponse, ConversionEvent} {
		for _, raw := range []string{
			`null`, strings.Repeat(" ", coreDirectDecodeBytes+1) + `null`, `{}`, `[]`,
			`{"native":{"value":` + payload + `}}`,
			`{"native":{"value":` + payload + `},"source":{"family":"first"},"source":{"revision":"second"},"source":null}`,
			`{"native":{"value":` + payload + `},"native":{"source":{"direction":"decode_request"}}}`,
			`{"native":{"value":` + payload + `},"native":null}`,
			`{"native":{"value":` + payload + `},"schemaVersion":"wrong"}`,
			`{"native":{"value":` + payload + `,"source":{"scope":{"account":false}}}}`,
			`{"native":{"value":` + payload + `,"requestWorkspace":{}}}`,
			`{"native":{"value":` + payload + `},"future":true}`,
		} {
			input := parseTestValue(t, raw)
			var expected any
			switch phase {
			case ConversionIngress, ConversionRequest:
				expected = &Request{}
			case ConversionResponse:
				expected = &Response{}
			case ConversionEvent:
				expected = &Event{}
			}
			expectedErr := decodeContract(input.Bytes(), expected)
			state := conversionState{wire: input}
			actualErr := state.load(phase, true)
			for before, after := expectedErr, actualErr; before != nil || after != nil; before, after = errors.Unwrap(before), errors.Unwrap(after) {
				if reflect.TypeOf(before) != reflect.TypeOf(after) || fmt.Sprint(before) != fmt.Sprint(after) {
					t.Fatalf("%s strict error chain changed: %T %v / %T %v", phase, after, after, before, before)
				}
			}
			if expectedErr != nil {
				if state.document != nil || state.isStrict {
					t.Fatal("failed strict decode published a partial document", phase)
				}
			} else if !state.isStrict || !reflect.DeepEqual(state.document, expected) {
				t.Fatalf("%s strict document changed: %#v / %#v", phase, state.document, expected)
			}
		}
	}
}

func TestConversionStrictLoadRetainsLenientBoundary(t *testing.T) {
	input := parseTestValue(t, `{"schemaVersion":1,"future":{"body":"`+strings.Repeat("p", coreDirectDecodeBytes+1)+`"}}`)
	state := conversionState{wire: input}
	if err := state.load(ConversionRequest, false); err != nil || state.isStrict {
		t.Fatal("lenient context validation became strict", err)
	}
	validated := state.document
	err := state.load(ConversionRequest, true)
	if err == nil || !strings.Contains(err.Error(), "unknown field") || state.document != validated || state.isStrict || state.wire != input {
		t.Fatal("strict upgrade lost its wire boundary or published partial state", err)
	}
}

func TestConversionStrictLoadOwnsContainersAndDetachesSmallViews(t *testing.T) {
	input := parseTestValue(t, `{"model":"`+strings.Repeat("m", 90<<10)+`","content":[{"kind":"text","payload":"`+strings.Repeat("p", 2<<20)+`","attributes":{"label":"original"}}]}`)
	first, second := conversionState{wire: input}, conversionState{wire: input}
	if err := first.load(ConversionRequest, true); err != nil {
		t.Fatal(err)
	}
	if err := second.load(ConversionRequest, true); err != nil {
		t.Fatal(err)
	}
	left, right := first.document.(*Request), second.document.(*Request)
	if !sharesValueStorage(input, left.Content[0].Payload) || sharesValueStorage(input, left.Model) {
		t.Fatal("strict load lost the root-relative ownership threshold")
	}
	left.Content[0].Kind = ImageNode
	left.Content[0].Attributes["label"] = StringValue("changed")
	if right.Content[0].Kind != TextNode || !right.Content[0].Attributes["label"].IsString("original") {
		t.Fatal("strict loads shared mutable containers")
	}
}
