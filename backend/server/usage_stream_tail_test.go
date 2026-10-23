package server

import (
	"fmt"
	"github.com/bytedance/sonic"
	"testing"
)

func TestUsageStreamTailReportsDiscardedFrames(t *testing.T) {
	for _, count := range []int{StreamEventsCacheMax - 1, StreamEventsCacheMax, StreamEventsCacheMax + 1} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			record := &usageRecord{bodyOpts: usageBodyOptions{maxBytes: 1 << 20}}
			for index := range count {
				record.appendStreamEvent(fmt.Sprintf(`{"index":%d}`, index))
			}
			record.materializeStreamEvents()
			if record.ProviderResponse.Truncated != (count > StreamEventsCacheMax) {
				t.Fatal("stream tail omitted frames without reporting truncation", record.ProviderResponse.Truncated)
			}
			var frames []struct {
				Index int `json:"index"`
			}
			if err := sonic.ConfigStd.Unmarshal([]byte(record.ProviderResponse.Content), &frames); err != nil {
				t.Fatal(err)
			}
			if len(frames) != min(count, StreamEventsCacheMax) || frames[0].Index != max(0, count-StreamEventsCacheMax) || frames[len(frames)-1].Index != count-1 {
				t.Fatal("truncation changed the retained tail", len(frames), frames[0], frames[len(frames)-1])
			}
			if record.pendingStreamEvents != nil || record.pendingStreamTruncated {
				t.Fatal("materialization retained raw capture state")
			}
			record.ProviderResponse = usageBody{}
			record.appendStreamEvent(`{"index":99}`)
			record.materializeStreamEvents()
			if record.ProviderResponse.Truncated {
				t.Fatal("a new capture inherited old truncation")
			}
		})
	}
}

func TestUsageStreamTailTruncationBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name          string
		limit         int
		frames        []string
		existing      usageBody
		wantTruncated bool
		wantContent   string
	}{
		{"bytes-only", 4, []string{`{"index":1}`}, usageBody{}, true, ""},
		{"invalid-only", 1024, []string{`{"broken":`}, usageBody{}, true, ""},
		{"mixed", 1024, []string{`{"broken":`, `{"index":1}`}, usageBody{}, true, `[{"index":1}]`},
		{"disabled", 0, []string{`{"broken":`, `{"index":1}`}, usageBody{}, false, ""},
		{"explicit-response", 1024, []string{`{"broken":`, `{"index":1}`}, usageBody{Content: `{"original":true}`}, false, `{"original":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := &usageRecord{bodyOpts: usageBodyOptions{maxBytes: tc.limit}, ProviderResponse: tc.existing}
			for _, frame := range tc.frames {
				record.appendStreamEvent(frame)
			}
			record.materializeStreamEvents()
			if record.ProviderResponse.Truncated != tc.wantTruncated {
				t.Fatal("discard evidence was lost", record.ProviderResponse)
			}
			if tc.name != "bytes-only" && record.ProviderResponse.Content != tc.wantContent {
				t.Fatal("capture changed content or explicit ownership", record.ProviderResponse)
			}
		})
	}
}
