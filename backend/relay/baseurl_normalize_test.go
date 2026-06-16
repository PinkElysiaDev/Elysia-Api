package relay

import "testing"

// OpenAI 系：裸 host 自动补 /v1；已含版本段则原样保留。
func TestNormalizeOpenAIBaseURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"裸 host 补 v1", "https://moyuu.cc", "https://moyuu.cc/v1"},
		{"裸 host 带尾斜杠", "https://moyuu.cc/", "https://moyuu.cc/v1"},
		{"已带 v1 原样", "https://api.openai.com/v1", "https://api.openai.com/v1"},
		{"已带 v1 尾斜杠去掉", "https://api.openai.com/v1/", "https://api.openai.com/v1"},
		{"已带 v2 原样", "https://example.com/v2", "https://example.com/v2"},
		{"自定义前缀含 v1 原样", "https://gw.example.com/api/v1", "https://gw.example.com/api/v1"},
		{"host 含 v1 不误判仍补", "https://v1.example.com", "https://v1.example.com/v1"},
		{"空串返回空", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := normalizeOpenAIBaseURL(c.in); got != c.want {
				t.Fatalf("normalizeOpenAIBaseURL(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// openAIEndpoint 把规范化后的 base 与端点路径拼成完整 URL。
func TestOpenAIEndpoint(t *testing.T) {
	if got := openAIEndpoint("https://moyuu.cc", "/responses"); got != "https://moyuu.cc/v1/responses" {
		t.Fatalf("bare host responses = %q", got)
	}
	if got := openAIEndpoint("https://api.openai.com/v1", "/chat/completions"); got != "https://api.openai.com/v1/chat/completions" {
		t.Fatalf("with v1 chat = %q", got)
	}
}

// Claude/Gemini 系：剥掉用户误填的末尾版本段，回到裸前缀。
func TestStripTrailingVersionSegment(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"裸 host 原样", "https://moyuu.cc", "https://moyuu.cc"},
		{"误填 v1 剥掉", "https://moyuu.cc/v1", "https://moyuu.cc"},
		{"误填 v1 尾斜杠剥掉", "https://moyuu.cc/v1/", "https://moyuu.cc"},
		{"误填 v1beta 剥掉", "https://moyuu.cc/v1beta", "https://moyuu.cc"},
		{"多重误填全剥", "https://moyuu.cc/v1beta/v1", "https://moyuu.cc"},
		{"自定义前缀保留", "https://gw.example.com/api", "https://gw.example.com/api"},
		{"host 含 v1 不误剥", "https://v1.example.com", "https://v1.example.com"},
		{"空串返回空", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := stripTrailingVersionSegment(c.in); got != c.want {
				t.Fatalf("stripTrailingVersionSegment(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
