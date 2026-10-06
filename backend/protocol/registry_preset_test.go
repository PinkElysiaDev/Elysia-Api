package protocol

import (
	"strings"
	"testing"
)

// 预置只读：历史修订删除在服务层即被拒绝，且不会执行任何持久化回调。
func TestDeleteRetainedRefusesPresetProtocols(t *testing.T) {
	service := &Service{}
	for _, id := range []string{PresetChatCompletionsID, PresetResponsesID, PresetAnthropicID, PresetGeminiID} {
		committed := false
		err := service.DeleteRetained(id, "0000000000000000000000000000000000000000000000000000000000000000", func() error {
			committed = true
			return nil
		})
		if err == nil || !strings.Contains(err.Error(), "read-only") {
			t.Fatalf("DeleteRetained accepted preset %s: %v", id, err)
		}
		if committed {
			t.Fatalf("DeleteRetained committed a preset deletion for %s", id)
		}
	}
}
