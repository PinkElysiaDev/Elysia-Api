package server

import (
	"testing"

	"github.com/elysia-api/backend/relay"
)

// 四份 v2 预置必须通过整体校验（含双模板渲染）——预置是协议作者的起点，
// 坏预置会随播种进入每个部署。
func TestPresetProtocolsValidate(t *testing.T) {
	configs, err := PresetProtocolConfigs()
	if err != nil {
		t.Fatalf("presets: %v", err)
	}
	if len(configs) != 4 {
		t.Fatalf("presets = %d, want 4", len(configs))
	}
	for _, config := range configs {
		if err := relay.ValidateCustomProtocol(config); err != nil {
			t.Fatalf("preset %q invalid: %v", config.ID, err)
		}
		if config.Metadata == nil || config.Metadata["presetVersion"] == nil {
			t.Fatalf("preset %q missing metadata.presetVersion", config.ID)
		}
	}
}
