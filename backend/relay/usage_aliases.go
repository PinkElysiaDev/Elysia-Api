package relay

// 用量字段别名总表：各厂商在 usage 对象里对同一语义使用不同键名
// （snake/camel/Pascal 混用）。所有用量解析路径统一引用此处，
// 新增厂商别名只改这一份表。
var usageAliasTables = struct {
	input    []string
	output   []string
	total    []string
	cached   []string
	cacheCre []string
	cacheRd  []string
	reason   []string
}{
	input:    []string{"input_tokens", "inputTokens", "prompt_tokens", "promptTokenCount"},
	output:   []string{"output_tokens", "outputTokens", "completion_tokens", "candidatesTokenCount"},
	total:    []string{"total_tokens", "totalTokens", "totalTokenCount"},
	cached:   []string{"cached_tokens", "cachedInputTokens", "cached_input_tokens", "cachedContentTokenCount"},
	cacheCre: []string{"cache_creation_input_tokens", "cacheCreationInputTokens"},
	cacheRd:  []string{"cache_read_input_tokens", "cacheReadInputTokens"},
	reason:   []string{"reasoning_tokens", "reasoningTokens", "thoughtsTokenCount"},
}

// usageAliasKeysWithDefaults 在用户别名（可整体替换键位）之后拼接默认表，
// 保持「用户优先、默认兜底」的既有语义。
func usageAliasKeysWithDefaults(custom []string, defaults []string) []string {
	if len(custom) > 0 {
		return custom
	}
	return defaults
}
