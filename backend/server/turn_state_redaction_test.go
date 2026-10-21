package server

import (
	"strings"
	"testing"

	"github.com/bytedance/sonic"
)

func TestTurnStateRedactionAcrossLogContainers(t *testing.T) {
	const state = "synthetic-private-turn-state"
	const raw = `{"headers":{"X-Codex-Turn-State":"` + state + `"},"n":9007199254740993}`
	nested, _ := sonic.ConfigStd.Marshal(map[string]string{"body": raw})
	for _, body := range []string{raw, string(nested), "event: response.metadata\ndata: " + raw + "\n\n"} {
		for _, limit := range []int{1 << 20, 54} {
			record := &usageRecord{bodyOpts: usageBodyOptions{maxBytes: limit}}
			for _, result := range []usageBody{record.sanitizeBody([]byte(body)), record.finalizeDownstreamBody(usageBody{Content: body})} {
				if strings.Contains(result.Content, "synthetic-private") {
					t.Fatalf("state leaked: %s", result.Content)
				}
				if limit > len(body) && (!strings.Contains(result.Content, "[REDACTED]") || !strings.Contains(result.Content, "9007199254740993")) {
					t.Fatalf("redaction or precision lost: %s", result.Content)
				}
			}
		}
	}
	capture, writer, recorder := newTestStreamCapture(54)
	for _, chunk := range []string{raw[:25], raw[25:]} {
		_, _ = writer.Write([]byte(chunk))
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	record := &usageRecord{bodyOpts: usageBodyOptions{maxBytes: 54}}
	logged := record.finalizeDownstreamBody(capture.downstreamBody())
	if strings.Contains(logged.Content, "synthetic") || !logged.Truncated || recorder.Body.String() != raw {
		t.Fatalf("capture leaked or changed delivery: %#v", logged)
	}
}
