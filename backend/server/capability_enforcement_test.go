package server

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
)

// ---- 能力目录（方向1 / G1）----

func boolPtr(v bool) *bool { return &v }

// 目录解析：对象形态（models.dev api.json）与数组形态（镜像快照）都能读出条目，
// 字段映射正确（vision←modalities.input、tools←tool_call、maxTokens←limit.context）。
func TestParseCatalogDatasetObjectAndArrayShapes(t *testing.T) {
	objectShape := `{
		"openai": {"models": {
			"gpt-4o": {"name": "GPT-4o", "modalities": {"input": ["text","image"]}, "tool_call": true,
				"structured_output": true, "reasoning": {"supported": false}, "limit": {"context": 128000, "output": 16384}},
			"o3": {"name": "o3", "modalities": {"input": ["text"]}, "tool_call": true,
				"reasoning": {"supported": true}, "limit": {"context": 200000}}
		}},
		"anthropic": {"models": {
			"claude-3-5-sonnet": {"name": "Claude 3.5 Sonnet", "modalities": {"input": ["text","image"]}, "tool_call": true, "limit": {"context": 200000}}
		}}
	}`
	dataset, err := parseCatalogDataset([]byte(objectShape))
	if err != nil {
		t.Fatalf("parse object shape: %v", err)
	}
	if dataset.count != 3 {
		t.Fatalf("expected 3 entries, got %d", dataset.count)
	}
	entry, ok := dataset.entries["openai/gpt-4o"]
	if !ok || !entry.vision || !entry.tools || !entry.structured || entry.maxTokens != 128000 {
		t.Fatalf("gpt-4o entry mapped wrong: %+v", entry)
	}
	// 裸 id 键可命中。
	if bare, ok := dataset.entries["gpt-4o"]; !ok || bare != entry {
		t.Fatalf("bare id key missing or mismatched")
	}

	arrayShape := `{"nvidia": {"models": [
		{"id": "nvidia/nemotron-ultra", "name": "Nemotron Ultra", "tool_call": true,
		 "modalities": {"input": ["text"]}, "reasoning": {"supported": true}, "limit": {"context": 1000000}, "type": "chat"}
	]}}`
	dataset2, err := parseCatalogDataset([]byte(arrayShape))
	if err != nil {
		t.Fatalf("parse array shape: %v", err)
	}
	if dataset2.count != 1 {
		t.Fatalf("expected 1 entry, got %d", dataset2.count)
	}
	if e := dataset2.entries["nvidia/nvidia/nemotron-ultra"]; e == nil || e.maxTokens != 1000000 {
		t.Fatalf("array shape entry wrong: %+v", e)
	}
}

// 目录匹配变体：精确 → 裸 id（去 provider 前缀）→ 去 -latest → 去日期后缀。
func TestCatalogLookupVariants(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"gpt-4o", "gpt-4o"},
		{"openai/gpt-4o", "gpt-4o"},
		{"claude-3-5-sonnet-20241022", "claude-3-5-sonnet"},
		{"gpt-4o-2024-11-20", "gpt-4o"},
		{"claude-3-5-sonnet-latest", "claude-3-5-sonnet"},
	}
	for _, c := range cases {
		variants := catalogLookupVariants(c.in)
		found := false
		for _, v := range variants {
			if v == c.want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("lookup %q: expected variant %q in %v", c.in, c.want, variants)
		}
	}
}

// Enrich：命中回填能力并标记 catalog；未命中 no-op；上游已返回的 maxTokens 不被覆盖。
func TestModelCatalogEnrich(t *testing.T) {
	catalog := newModelCatalog(nil, nil)
	dataset, err := parseCatalogDataset([]byte(`{"openai": {"models": {
		"gpt-4o": {"modalities": {"input": ["text","image"]}, "tool_call": true, "reasoning": {"supported": false}, "limit": {"context": 128000}}
	}}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	catalog.entries = dataset.entries
	catalog.count = dataset.count

	model := &storage.Model{ID: "gpt-4o-2024-11-20", Type: "llm", ThinkingMode: "both"}
	if !catalog.Enrich(model) {
		t.Fatalf("expected catalog hit")
	}
	if !model.VisionCapable || !model.ToolsCapable || model.MaxTokens != 128000 {
		t.Fatalf("enriched fields wrong: %+v", model)
	}
	if model.ThinkingMode != "non-thinking-only" {
		t.Fatalf("reasoning=false should set non-thinking-only, got %q", model.ThinkingMode)
	}
	if model.CapabilitySource != "catalog" {
		t.Fatalf("capability source not marked: %q", model.CapabilitySource)
	}

	// 上游已解析 maxTokens（Gemini inputTokenLimit）时目录不覆盖。
	withUpstream := &storage.Model{ID: "gpt-4o", MaxTokens: 999, Type: "llm", ThinkingMode: "both"}
	catalog.Enrich(withUpstream)
	if withUpstream.MaxTokens != 999 {
		t.Fatalf("upstream maxTokens must win, got %d", withUpstream.MaxTokens)
	}

	miss := &storage.Model{ID: "totally-unknown-model", Type: "llm"}
	if catalog.Enrich(miss) {
		t.Fatalf("expected miss")
	}
	if miss.CapabilitySource != "" {
		t.Fatalf("miss must not touch capability source")
	}
}

// ---- 能力生效（方向2）----

// tools=false 且请求携带 tools / 工具消息 → 拒绝；tools=true 或不含工具 → 放行。
func TestRejectToolRequestsIfNeeded(t *testing.T) {
	group := &config.ModelGroupConfig{Name: "g", ToolsCapable: boolPtr(false)}
	plain := &relay.MaheshvaraRequest{Model: "m"}
	if rejectToolRequestsIfNeeded(group, plain) {
		t.Fatalf("plain request must pass")
	}
	withTools := &relay.MaheshvaraRequest{Model: "m", Tools: []relay.MaheshvaraTool{{}}}
	if !rejectToolRequestsIfNeeded(group, withTools) {
		t.Fatalf("request with tools must be rejected")
	}
	withChoice := &relay.MaheshvaraRequest{Model: "m", ToolChoice: "auto"}
	if !rejectToolRequestsIfNeeded(group, withChoice) {
		t.Fatalf("request with tool_choice must be rejected")
	}
	withToolMessage := &relay.MaheshvaraRequest{Model: "m", Messages: []relay.MaheshvaraMessage{{
		Role:    "assistant",
		Content: []relay.MaheshvaraContentPart{{Type: relay.MaheshvaraContentToolCall}},
	}}}
	if !rejectToolRequestsIfNeeded(group, withToolMessage) {
		t.Fatalf("request with tool_call message must be rejected")
	}
	withFunctionOutputItem := &relay.MaheshvaraRequest{Model: "m", InputItems: []relay.MaheshvaraInputItem{{
		Type: relay.MaheshvaraInputFunctionCallOutput,
	}}}
	if !rejectToolRequestsIfNeeded(group, withFunctionOutputItem) {
		t.Fatalf("request with function_call_output item must be rejected")
	}
	// tools=true 放行；指针为 nil（未知）也放行（宽松语义）。
	open := &config.ModelGroupConfig{Name: "g", ToolsCapable: boolPtr(true)}
	if rejectToolRequestsIfNeeded(open, withTools) {
		t.Fatalf("tools-capable group must pass")
	}
	unknown := &config.ModelGroupConfig{Name: "g"}
	if rejectToolRequestsIfNeeded(unknown, withTools) {
		t.Fatalf("unknown capability must pass")
	}
}

// vision=false：image/audio/video 全部剥离，返回被剥离的模态集合；vision=true 不动。
func TestFilterMaheshvaraMultimodalInputsIfNeeded(t *testing.T) {
	group := &config.ModelGroupConfig{Name: "g", VisionCapable: boolPtr(false)}
	request := &relay.MaheshvaraRequest{Model: "m", Messages: []relay.MaheshvaraMessage{{
		Role: "user",
		Content: []relay.MaheshvaraContentPart{
			{Type: relay.MaheshvaraContentText},
			{Type: relay.MaheshvaraContentImage},
			{Type: relay.MaheshvaraContentAudio},
			{Type: relay.MaheshvaraContentVideo},
		},
	}}}
	changed, parts, modalities := filterMaheshvaraMultimodalInputsIfNeeded(group, request)
	if !changed || parts != 3 {
		t.Fatalf("expected 3 stripped parts, changed=%v parts=%d", changed, parts)
	}
	if len(modalities) != 3 || modalities[0] != "audio" || modalities[1] != "image" || modalities[2] != "video" {
		t.Fatalf("modalities wrong: %v", modalities)
	}
	if len(request.Messages[0].Content) != 1 || request.Messages[0].Content[0].Type != relay.MaheshvaraContentText {
		t.Fatalf("text part must be kept, got %+v", request.Messages[0].Content)
	}

	visionGroup := &config.ModelGroupConfig{Name: "g", VisionCapable: boolPtr(true)}
	request2 := &relay.MaheshvaraRequest{Messages: []relay.MaheshvaraMessage{{
		Content: []relay.MaheshvaraContentPart{{Type: relay.MaheshvaraContentImage}},
	}}}
	if changed, _, _ := filterMaheshvaraMultimodalInputsIfNeeded(visionGroup, request2); changed {
		t.Fatalf("vision-capable group must not strip")
	}
}

// ---- 候选软过滤与多 key 展开（方向2/6）----

// 请求含多模态输入时，不支持视觉的候选被移到末尾（保持相对顺序）；全部不支持则原序。
func TestReorderCandidatesByRequestNeeds(t *testing.T) {
	candidates := []config.ModelRef{
		{ID: "a", VisionCapable: false, ToolsCapable: true},
		{ID: "b", VisionCapable: true, ToolsCapable: false},
		{ID: "c", VisionCapable: false, ToolsCapable: true},
	}
	reordered := reorderCandidatesByRequestNeeds(append([]config.ModelRef(nil), candidates...), true, false)
	if reordered[0].ID != "b" {
		t.Fatalf("vision-capable candidate should come first, got %s", reordered[0].ID)
	}
	if reordered[1].ID != "a" || reordered[2].ID != "c" {
		t.Fatalf("relative order of incapable candidates must be preserved, got %s,%s", reordered[1].ID, reordered[2].ID)
	}
	allIncapable := []config.ModelRef{{ID: "x"}, {ID: "y"}}
	same := reorderCandidatesByRequestNeeds(append([]config.ModelRef(nil), allIncapable...), true, false)
	if same[0].ID != "x" || same[1].ID != "y" {
		t.Fatalf("all-incapable must keep original order")
	}
	// 工具需求同理。
	toolCandidates := []config.ModelRef{{ID: "a", ToolsCapable: false}, {ID: "b", ToolsCapable: true}}
	toolReordered := reorderCandidatesByRequestNeeds(append([]config.ModelRef(nil), toolCandidates...), false, true)
	if toolReordered[0].ID != "b" {
		t.Fatalf("tools-capable candidate should come first")
	}
}

// 多 key 展开：priority 展开为候选×key 连续尝试；round-robin 按源级游标轮转；
// single（默认）原样。
func TestExpandCandidatesByKeyStrategy(t *testing.T) {
	s := &Server{}
	priority := []config.ModelRef{
		{ID: "m1", APIKeys: []string{"k1", "k2"}, KeyStrategy: string(storage.KeyStrategyPriority), SourceID: "src1"},
		{ID: "m2", APIKeys: []string{"k3", "k4"}, KeyStrategy: string(storage.KeyStrategyPriority), SourceID: "src1"},
	}
	expanded := s.expandCandidatesByKeyStrategy(priority)
	want := []struct{ id, key string }{
		{"m1", "k1"}, {"m1", "k2"}, {"m2", "k3"}, {"m2", "k4"},
	}
	if len(expanded) != len(want) {
		t.Fatalf("priority expansion length: want %d got %d", len(want), len(expanded))
	}
	for i, w := range want {
		if expanded[i].ID != w.id || expanded[i].APIKey != w.key {
			t.Fatalf("expanded[%d] = (%s,%s), want (%s,%s)", i, expanded[i].ID, expanded[i].APIKey, w.id, w.key)
		}
		if len(expanded[i].APIKeys) != 0 {
			t.Fatalf("expanded refs must not carry APIKeys further")
		}
	}

	// round-robin：同一源的连续候选取到不同 key，第二轮回到首个。
	rr := []config.ModelRef{
		{ID: "m1", APIKeys: []string{"k1", "k2"}, KeyStrategy: string(storage.KeyStrategyRoundRobin), SourceID: "src1"},
		{ID: "m2", APIKeys: []string{"k1", "k2"}, KeyStrategy: string(storage.KeyStrategyRoundRobin), SourceID: "src1"},
	}
	first := s.expandCandidatesByKeyStrategy(rr)
	if first[0].APIKey == first[1].APIKey {
		t.Fatalf("round-robin must advance key per candidate within one request: %s vs %s", first[0].APIKey, first[1].APIKey)
	}
	// 两个候选消耗两次游标推进（k1、k2），下一次请求回到 k1（2-key 环绕）。
	second := s.expandCandidatesByKeyStrategy(rr)
	if second[0].APIKey != "k1" || second[1].APIKey != "k2" {
		t.Fatalf("round-robin must wrap the key ring: got %s,%s", second[0].APIKey, second[1].APIKey)
	}
	// 单候选组：跨请求逐 key 轮转。
	singleRR := []config.ModelRef{{ID: "m1", APIKeys: []string{"k1", "k2"}, KeyStrategy: string(storage.KeyStrategyRoundRobin), SourceID: "src2"}}
	r1 := s.expandCandidatesByKeyStrategy(singleRR)
	r2 := s.expandCandidatesByKeyStrategy(singleRR)
	r3 := s.expandCandidatesByKeyStrategy(singleRR)
	if r1[0].APIKey != "k1" || r2[0].APIKey != "k2" || r3[0].APIKey != "k1" {
		t.Fatalf("single-candidate round-robin must rotate across requests: %s,%s,%s", r1[0].APIKey, r2[0].APIKey, r3[0].APIKey)
	}

	// 单 key（含未配置策略）原样返回。
	single := []config.ModelRef{{ID: "m1", APIKey: "k0", APIKeys: []string{"k0"}}}
	out := s.expandCandidatesByKeyStrategy(single)
	if len(out) != 1 || out[0].ID != "m1" || out[0].APIKey != "k0" {
		t.Fatalf("single-key candidates must pass through, got %+v", out)
	}
}

// EffectiveKeys：多 key 过滤 disabled；空列表回退单 key；两者皆空返回 nil。
func TestModelSourceEffectiveKeys(t *testing.T) {
	multi := storage.ModelSource{APIKeys: []storage.SourceAPIKey{
		{Value: "k1"}, {Value: "k2", Disabled: true}, {Value: "k3"},
	}}
	keys := multi.EffectiveKeys()
	if len(keys) != 2 || keys[0].Value != "k1" || keys[1].Value != "k3" {
		t.Fatalf("disabled key must be filtered: %+v", keys)
	}
	fallback := storage.ModelSource{APIKey: "legacy"}
	if keys := fallback.EffectiveKeys(); len(keys) != 1 || keys[0].Value != "legacy" {
		t.Fatalf("single key fallback broken: %+v", keys)
	}
	empty := storage.ModelSource{}
	if keys := empty.EffectiveKeys(); keys != nil {
		t.Fatalf("empty source must yield nil keys")
	}
}

// 落盘缓存往返：saveToCache 写出的文件被新建 catalog 的 loadFromCache 完整恢复
// （重启语义：无需网络即可用，lastSync 保留上次成功时间）。
func TestModelCatalogCacheRoundTrip(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "model-catalog.json")
	body := []byte(`{"openai": {"models": {"gpt-4o": {"modalities": {"input": ["text","image"]}, "tool_call": true, "limit": {"context": 128000}}}}}`)

	original := newModelCatalog(nil, func() string { return cachePath })
	original.saveToCache(body, time.Now())

	restored := newModelCatalog(nil, func() string { return cachePath })
	if status := restored.Status(); status["entries"].(int) != 1 {
		t.Fatalf("cache restore failed: %v", status)
	}
	entry, ok := restored.lookup("gpt-4o")
	if !ok || !entry.vision || !entry.tools || entry.maxTokens != 128000 {
		t.Fatalf("restored entry wrong: %+v", entry)
	}
	if restored.lastSync.IsZero() {
		t.Fatalf("lastSync must be restored from cache")
	}
}

// 周期换算：未配置默认 24h，配置后按分钟生效。
func TestModelCatalogSyncIntervalConfig(t *testing.T) {
	// nil（未配置）= 默认 24h 且启用。
	if got, enabled := (config.ModelCatalogConfig{}).ModelCatalogSyncInterval(); got != 24*time.Hour || !enabled {
		t.Fatalf("default interval = %v enabled=%v", got, enabled)
	}
	// 显式 0 = 不启用定期同步（仅快照/缓存）。
	zero := 0
	if got, enabled := (config.ModelCatalogConfig{SyncIntervalMinutes: &zero}).ModelCatalogSyncInterval(); enabled || got != 0 {
		t.Fatalf("explicit 0 must disable periodic sync, got %v enabled=%v", got, enabled)
	}
	// >0 = 该值。
	thirty := 30
	if got, enabled := (config.ModelCatalogConfig{SyncIntervalMinutes: &thirty}).ModelCatalogSyncInterval(); got != 30*time.Minute || !enabled {
		t.Fatalf("configured interval = %v enabled=%v", got, enabled)
	}
}

// 内置快照零配置可用：不配网络、无缓存，构造即有能力数据（origin=snapshot）。
func TestModelCatalogLoadsEmbeddedSnapshot(t *testing.T) {
	catalog := newModelCatalog(nil, nil)
	status := catalog.Status()
	if status["source"] != "snapshot" {
		t.Fatalf("expected snapshot origin, got %v", status["source"])
	}
	if entries := status["entries"].(int); entries < 100 {
		t.Fatalf("embedded snapshot suspiciously small: %d entries", entries)
	}
	if _, ok := catalog.lookup("gpt-4o"); !ok {
		t.Fatalf("snapshot should contain well-known models")
	}
}

// 镜像格式：providers 包裹 + models 数组形态可解析。
func TestParseCatalogDatasetMirrorWrapper(t *testing.T) {
	mirror := []byte(`{"providers": {"openai": {"models": [
		{"id": "gpt-4o", "tool_call": true, "modalities": {"input": ["text","image"]}, "limit": {"context": 128000}}
	]}}}`)
	dataset, err := parseCatalogDataset(mirror)
	if err != nil {
		t.Fatalf("parse mirror: %v", err)
	}
	if dataset.count != 1 {
		t.Fatalf("expected 1 entry, got %d", dataset.count)
	}
	if entry, ok := dataset.entries["gpt-4o"]; !ok || !entry.vision || !entry.tools {
		t.Fatalf("mirror entry wrong: %+v", entry)
	}
}
