package server

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// 回归 #1/#7：纯中文/纯符号名称清洗后为空时，slugID 必须回退到非空 id，
// 否则存储层会误报 "id is required"。
func TestSlugIDChineseAndSymbols(t *testing.T) {
	cases := []string{"源", "我的模型组", "测试 source", "！@￥", "  "}
	for _, name := range cases {
		got := slugID(name)
		if strings.TrimSpace(got) == "" {
			t.Fatalf("slugID(%q) returned empty, want non-empty fallback", name)
		}
	}
	// 正常英文名仍应得到可读 slug。
	if got := slugID("OpenAI Main"); got != "openai-main" {
		t.Fatalf("slugID(\"OpenAI Main\") = %q, want openai-main", got)
	}
	// 中英混合：保留英文数字部分。
	if got := slugID("源gpt4"); got != "gpt4" {
		t.Fatalf("slugID(\"源gpt4\") = %q, want gpt4", got)
	}
}

func newGroupAccessContext(allowed []string, set bool) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if set {
		c.Set("elysiaAllowedGroups", allowed)
	}
	return c
}

// 模型组级访问权限（#9）：空/未设置 = 放行；非空 = 仅白名单内放行。
func TestTokenAllowsGroup(t *testing.T) {
	s := &Server{}

	// 未设置 AllowedGroups（上下文无此键）→ 放行
	if !s.tokenAllowsGroup(newGroupAccessContext(nil, false), "any-group") {
		t.Fatalf("missing AllowedGroups should allow all")
	}
	// 空切片 → 不限制，放行
	if !s.tokenAllowsGroup(newGroupAccessContext([]string{}, true), "any-group") {
		t.Fatalf("empty AllowedGroups should allow all")
	}
	// 白名单命中 → 放行
	if !s.tokenAllowsGroup(newGroupAccessContext([]string{"g1", "g2"}, true), "g2") {
		t.Fatalf("group in allowlist should be allowed")
	}
	// 白名单未命中 → 拒绝
	if s.tokenAllowsGroup(newGroupAccessContext([]string{"g1", "g2"}, true), "g3") {
		t.Fatalf("group not in allowlist should be denied")
	}
}
