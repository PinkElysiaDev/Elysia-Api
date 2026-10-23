package server

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
)

func usageStringReferences(parent, value string) bool {
	if parent == "" || value == "" {
		return false
	}
	start := reflect.ValueOf(parent).Pointer()
	pointer := reflect.ValueOf(value).Pointer()
	return pointer >= start && pointer < start+uintptr(len(parent))
}

func assertUsageStringsDetached(t *testing.T, input string, body usageBody, assets assetSink) {
	t.Helper()
	if usageStringReferences(input, body.Content) {
		t.Fatal("retained log text borrows its input")
	}
	for _, asset := range assets.items {
		for field, value := range map[string]string{"hash": asset.Hash, "mime": asset.Mime, "extension": asset.Ext} {
			if usageStringReferences(input, value) {
				t.Fatalf("retained media %s borrows the input", field)
			}
		}
	}
	for key, placeholder := range assets.memo {
		if usageStringReferences(input, key) || usageStringReferences(input, placeholder) {
			t.Fatal("media memo retains a view into the input")
		}
	}
}

func TestUsageBorrowedDecoderSharesOnlyPrivateStrings(t *testing.T) {
	const textBytes = 1 << 20
	input := `{"number":9007199254740993,"text":"` + strings.Repeat("A", textBytes) + `"}`
	borrowed, ok := decodeUsageJSONBorrowed(input)
	if !ok {
		t.Fatal("borrowed decoder rejected valid JSON")
	}
	owned, ok := decodeUsageJSON(input)
	if !ok || !reflect.DeepEqual(borrowed, owned) {
		t.Fatal("borrowed decoder changed the owning decoder's result")
	}
	borrowedText := borrowed.(map[string]any)["text"].(string)
	ownedText := owned.(map[string]any)["text"].(string)
	if len(borrowedText) != textBytes || usageStringReferences(input, ownedText) {
		t.Fatal("the public decoder lost independent string ownership")
	}
	// Unsupported Sonic platforms may copy even in the private borrowed path.
	if sonic.APIKind == sonic.UseSonicJSON && !usageStringReferences(input, borrowedText) {
		t.Fatal("the private native decoder copied the unescaped large string")
	}
	borrowed.(map[string]any)["text"] = "changed locally"
	if owned.(map[string]any)["text"] != ownedText || !strings.Contains(input, strings.Repeat("A", textBytes)) {
		t.Fatal("private tree mutation escaped to the caller or owning decoder")
	}
}

func TestUsageBorrowedDecoderPreservesOwnedAcceptance(t *testing.T) {
	for index, input := range []string{
		"", " \r\n\t", `null`, `false`, `0000`, `[]`, `{}`, `"plain"`, `"\ud800"`,
		`{"a":1,"a":2,"numbers":[9007199254740993,1.00,1e999]}`,
		`{"escaped":"\u0041\n\"\\","html":"<>&","separator":"\u2028\u2029"}`,
		`{"nested":"{\"x-codex-turn-state\":\"synthetic\"}"}`,
		`{"valid":true} {"secret":"synthetic"}`, `{"x":"truncated`,
		"{\"invalid\":\"\xff\"}", "{\"key\x00\":true}", "{}\x00", "data: {}\n\n",
	} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			expected, expectedOK := decodeUsageJSON(input)
			actual, actualOK := decodeUsageJSONBorrowed(input)
			if actualOK != expectedOK || !reflect.DeepEqual(actual, expected) {
				t.Fatalf("decoder acceptance or values changed: got %#v/%t, want %#v/%t", actual, actualOK, expected, expectedOK)
			}
		})
	}
}

func TestUsageBorrowedEncodingDetachesPooledKeysAndClearsPrivateTrees(t *testing.T) {
	input := `{"short":"value","nested":{"data":"` + strings.Repeat("A", 1<<20) + `"},"items":[{"text":"child"},"scalar"]}`
	owned, ok := decodeUsageJSON(input)
	if !ok {
		t.Fatal("invalid owning fixture")
	}
	expected, err := sonic.ConfigStd.Marshal(owned)
	if err != nil {
		t.Fatal(err)
	}
	value, ok := decodeUsageJSONBorrowed(input)
	if !ok {
		t.Fatal("invalid fixture")
	}
	root := value.(map[string]any)
	nested := root["nested"].(map[string]any)
	items := root["items"].([]any)
	child := items[0].(map[string]any)
	detachUsageJSONKeys(value)
	for _, object := range []map[string]any{root, nested, child} {
		for key := range object {
			if usageStringReferences(input, key) {
				t.Fatal("a key copied into the encoder's iterator pool can retain the input")
			}
		}
	}
	encoded, err := encodeBorrowedUsageJSON(value)
	if err != nil || encoded != string(expected) {
		t.Fatal("releasing the cleaning tree changed encoded bytes", err)
	}
	if len(root) != 0 || len(nested) != 0 || len(child) != 0 || items[0] != nil || items[1] != nil {
		t.Fatal("encoder-retained maps or arrays still contain borrowed values")
	}
	assertUsageStringsDetached(t, input, usageBody{Content: encoded}, assetSink{})
	if !strings.Contains(input, strings.Repeat("A", 1<<20)) {
		t.Fatal("releasing the private tree modified the original input")
	}
}

func TestUsageBorrowedEncodingClearsLargeThenSmallMaps(t *testing.T) {
	const largeMapEntries = 32
	type encodingFixture struct {
		entries  int
		input    string
		expected string
	}
	var fixtures []encodingFixture
	for _, entries := range []int{largeMapEntries, 1} {
		fields := make([]string, entries)
		for index := range fields {
			fields[index] = fmt.Sprintf(`"field_%02d":"synthetic <>& \u2028 value %d"`, index, index)
		}
		objectFields := strings.Join(fields, ",")
		payload := "small"
		if entries == largeMapEntries {
			payload = strings.Repeat("A", 1<<20)
		}
		input := fmt.Sprintf(`{"payload":%q,"nested":{%s},"items":[{%s},[{%s},"tail"]],%s}`, payload, objectFields, objectFields, objectFields, objectFields)
		owned, ok := decodeUsageJSON(input)
		if !ok {
			t.Fatal("invalid owning fixture")
		}
		expected, err := sonic.ConfigStd.Marshal(owned)
		if err != nil {
			t.Fatal(err)
		}
		fixtures = append(fixtures, encodingFixture{entries, input, string(expected)})
	}
	var consumedMaps []map[string]any
	var consumedArrays [][]any
	for _, fixture := range fixtures {
		borrowed, ok := decodeUsageJSONBorrowed(fixture.input)
		if !ok {
			t.Fatal("invalid borrowed fixture")
		}
		root := borrowed.(map[string]any)
		nested := root["nested"].(map[string]any)
		items := root["items"].([]any)
		inner := items[1].([]any)
		consumedMaps = append(consumedMaps, root, nested, items[0].(map[string]any), inner[0].(map[string]any))
		consumedArrays = append(consumedArrays, items, inner)
		encoded, err := encodeBorrowedUsageJSON(borrowed)
		if err != nil || encoded != fixture.expected {
			t.Fatal("large-to-small encoding changed JSON bytes", fixture.entries, err)
		}
		for _, object := range consumedMaps {
			if len(object) != 0 {
				t.Fatal("consumed map retained entries after a later encoding", fixture.entries, len(object))
			}
		}
		for _, array := range consumedArrays {
			for _, value := range array {
				if value != nil {
					t.Fatal("consumed array retained an element after a later encoding", fixture.entries)
				}
			}
		}
		assertUsageStringsDetached(t, fixture.input, usageBody{Content: encoded}, assetSink{})
	}
}

func TestUsageEncodingPreservesCallerTrees(t *testing.T) {
	const input = `{"nested":{"text":"<>&","number":1.00},"items":[{"value":"keep"},"tail"]}`
	for _, shouldFail := range []bool{false, true} {
		t.Run(fmt.Sprintf("error=%t", shouldFail), func(t *testing.T) {
			caller, callerOK := decodeUsageJSON(input)
			expected, expectedOK := decodeUsageJSON(input)
			if !callerOK || !expectedOK {
				t.Fatal("invalid owning fixture")
			}
			if shouldFail {
				unsupported := make(chan int)
				caller.(map[string]any)["nested"].(map[string]any)["number"] = unsupported
				expected.(map[string]any)["nested"].(map[string]any)["number"] = unsupported
			}
			expectedBytes, expectedErr := sonic.ConfigStd.Marshal(expected)
			encoded, err := encodeUsageJSON(caller)
			if encoded != string(expectedBytes) || reflect.TypeOf(err) != reflect.TypeOf(expectedErr) {
				t.Fatal("owning encoder changed bytes or error type", err, expectedErr)
			}
			if err != nil && err.Error() != expectedErr.Error() {
				t.Fatal("owning encoder changed the error", err, expectedErr)
			}
			if !reflect.DeepEqual(caller, expected) {
				t.Fatal("ordinary encoding consumed or changed the caller's tree")
			}
		})
	}
}

func TestUsageBorrowedEncodingReleasesPrivateTreesOnError(t *testing.T) {
	value, ok := decodeUsageJSONBorrowed(`{"nested":{"number":1}}`)
	if !ok {
		t.Fatal("invalid fixture")
	}
	root := value.(map[string]any)
	nested := root["nested"].(map[string]any)
	nested["number"] = make(chan int)
	_, expectedErr := sonic.ConfigStd.Marshal(map[string]any{"nested": map[string]any{"number": make(chan int)}})
	_, err := encodeBorrowedUsageJSON(root)
	if err == nil || expectedErr == nil || reflect.TypeOf(err) != reflect.TypeOf(expectedErr) || err.Error() != expectedErr.Error() {
		t.Fatal("encoding error changed", err, expectedErr)
	}
	if len(root) != 0 || len(nested) != 0 {
		t.Fatal("an encoding failure retained the private tree")
	}
}

func TestUsageSanitizerTextAndBytesPreserveBodiesAndAssets(t *testing.T) {
	media := strings.Repeat("A", 4096)
	for index, input := range []string{
		` {"a":1,"a":2,"number":9007199254740993,"decimal":1.00,"token":"synthetic","text":"<>&\u2028\u2029"} `,
		`{"nested":"{\"X-Codex-Turn-State\":\"synthetic\",\"n\":9007199254740993}"}`,
		`{"source":{"type":"base64","media_type":"image/png","data":"` + media + `"}}`,
		"data: {\"headers\":{\"x-codex-turn-state\":\"synthetic\"}}\r\n\ndata: [DONE]\n",
		`{"x-codex-turn-state":"synthetic-truncated`, `{"valid":true} trailing`, `null`, `0000`, "{}\x00",
	} {
		for _, externalize := range []bool{false, true} {
			for _, limit := range []int{0, 32, 8192} {
				t.Run(fmt.Sprintf("%d/external=%t/limit=%d", index, externalize, limit), func(t *testing.T) {
					makeRecord := func() *usageRecord {
						return &usageRecord{bodyOpts: usageBodyOptions{maxBytes: limit, externalize: externalize}, assets: newAssetSink("synthetic-parity")}
					}
					fromText, fromBytes := makeRecord(), makeRecord()
					textBody := fromText.sanitizeBodyText(input)
					byteBody := fromBytes.sanitizeBody([]byte(input))
					if textBody != byteBody || !reflect.DeepEqual(fromText.assets, fromBytes.assets) {
						t.Fatal("text input changed log bytes, truncation, media metadata or payload")
					}
					assertUsageStringsDetached(t, input, textBody, fromText.assets)
				})
			}
		}
	}
}

func TestUsageSanitizerRetainedValuesDoNotBorrowLargeInput(t *testing.T) {
	const largeBytes = 1 << 20
	fullJSON := `{"text":"` + strings.Repeat("A", largeBytes) + `"}`
	for _, tc := range []struct {
		name  string
		input string
		limit int
	}{
		{"full-json", fullJSON, len(fullJSON)},
		{"truncated-json", fullJSON, 32},
		{"truncated-plain", strings.Repeat("A", largeBytes), 32},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := &usageRecord{bodyOpts: usageBodyOptions{maxBytes: tc.limit}}
			body := record.sanitizeBodyText(tc.input)
			if body.Content != tc.input[:tc.limit] || body.Truncated != (len(tc.input) > tc.limit) {
				t.Fatal("sanitizer changed exact bytes or truncation")
			}
			assertUsageStringsDetached(t, tc.input, body, record.assets)
		})
	}
	for _, fragment := range []string{`{"text":"small"}`, "small non-JSON fragment"} {
		parent := strings.Repeat("A", largeBytes) + fragment + strings.Repeat("B", largeBytes)
		input := parent[largeBytes : largeBytes+len(fragment)]
		record := &usageRecord{bodyOpts: usageBodyOptions{maxBytes: len(fragment)}}
		body := record.sanitizeBodyText(input)
		if body.Content != fragment || body.Truncated {
			t.Fatal("small input view changed its log bytes")
		}
		assertUsageStringsDetached(t, parent, body, record.assets)
	}

	mediaInput := `{"url":"data:image/png;base64,` + strings.Repeat("A", largeBytes) + `"}`
	mediaRecord := newExternalizeRecord("synthetic-owner")
	mediaRecord.bodyOpts.maxBytes = 32
	body := mediaRecord.sanitizeBodyText(mediaInput)
	if !body.Truncated || len(body.Content) != 32 || mediaRecord.assets.count() != 1 || len(mediaRecord.assets.memo) != 1 {
		t.Fatal("media must be extracted completely before truncation")
	}
	asset := mediaRecord.assets.items[0]
	if asset.Mime != "image/png" || asset.Ext != "png" || asset.payload.size() != largeBytes*3/4 {
		t.Fatal("media metadata or decoded payload changed")
	}
	assertUsageStringsDetached(t, mediaInput, body, mediaRecord.assets)
}

func TestUsageSanitizerOwnsResultsAfterInputBytesChange(t *testing.T) {
	for _, input := range []string{
		`{"text":"synthetic independent text","number":9007199254740993}`,
		"synthetic plain fragment",
		`{"url":"data:image/png;base64,` + strings.Repeat("A", 4096) + `"}`,
	} {
		buffer := []byte(input)
		record := newExternalizeRecord("synthetic-mutation")
		body := record.sanitizeBody(buffer)
		expectedBody := strings.Clone(body.Content)
		var payload []byte
		if record.assets.count() > 0 {
			payload = bytes.Clone(record.assets.items[0].payload.content)
		}
		for index := range buffer {
			buffer[index] = 'x'
		}
		if body.Content != expectedBody || len(payload) > 0 && !bytes.Equal(payload, record.assets.items[0].payload.content) {
			t.Fatal("caller input mutation changed retained log text or media")
		}
	}
}

func TestUsageDownstreamKeepsItsDistinctRedactionContract(t *testing.T) {
	input := " \n" + `{"n":1,"n":2,"token":"synthetic-secret","text":"\u003c"}` + "\t"
	record := &usageRecord{bodyOpts: usageBodyOptions{maxBytes: 8192}}
	downstream := record.finalizeDownstreamBody(usageBody{Content: input, Truncated: true})
	if downstream.Content != input || !downstream.Truncated {
		t.Fatal("ordinary downstream JSON lost its original bytes or capture truncation")
	}
	sanitized := record.sanitizeBodyText(input)
	if sanitized.Content != `{"n":2,"text":"\u003c","token":"[REDACTED]"}` || sanitized.Truncated {
		t.Fatal("request sanitization lost normalization or ordinary-secret redaction", sanitized)
	}
	stateInput := `{"token":"synthetic-secret","headers":{"X-Codex-Turn-State":"synthetic-state"},"n":9007199254740993}`
	downstream = record.finalizeDownstreamBody(usageBody{Content: stateInput})
	if strings.Contains(downstream.Content, "synthetic-state") || !strings.Contains(downstream.Content, "[REDACTED]") || !strings.Contains(downstream.Content, "synthetic-secret") || !strings.Contains(downstream.Content, "9007199254740993") {
		t.Fatal("downstream turn-state protection changed ordinary-secret or number policy")
	}
}
