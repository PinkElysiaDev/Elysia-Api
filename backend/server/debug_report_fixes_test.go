package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
	"github.com/gin-gonic/gin"
)

// DBG-001 回归:删除某 token 的唯一授权组后,该 token 必须被禁用,而不是因
// 「空列表=不限制」扩权为全组可用;多组/无限制 token 不受影响。
func TestDeleteLastAllowedGroupDisablesToken(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	store := s.store
	ctx := context.Background()

	seed := func(id, name string) {
		if err := store.UpsertGroup(ctx, storage.ModelGroup{ID: id, Name: name, Enabled: true}); err != nil {
			t.Fatalf("seed group %s: %v", name, err)
		}
	}
	seed("g-allowed", "allowed-group")
	seed("g-other", "restricted-group")
	seed("g-multi", "multi-group")

	tokens := []storage.APIToken{
		{Name: "restricted", Token: "sk-restricted", Enabled: true, AllowedGroups: []string{"allowed-group"}},
		{Name: "multi", Token: "sk-multi", Enabled: true, AllowedGroups: []string{"allowed-group", "multi-group"}},
		{Name: "unrestricted", Token: "sk-open", Enabled: true},
	}
	for _, token := range tokens {
		if err := store.UpsertAPIToken(ctx, token); err != nil {
			t.Fatalf("seed token %s: %v", token.Name, err)
		}
	}

	disabled, err := store.DeleteGroup(ctx, "g-allowed")
	if err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}
	if len(disabled) != 1 || disabled[0] != "restricted" {
		t.Fatalf("only the single-group token must be disabled, got %v", disabled)
	}

	byName := map[string]storage.APIToken{}
	all, err := store.ListAPITokens(ctx)
	if err != nil {
		t.Fatalf("ListAPITokens: %v", err)
	}
	for _, token := range all {
		byName[token.Name] = token
	}
	if byName["restricted"].Enabled {
		t.Fatal("token restricted must be disabled after its only allowed group is deleted")
	}
	if len(byName["restricted"].AllowedGroups) != 0 {
		t.Fatalf("dangling group reference must be removed: %v", byName["restricted"].AllowedGroups)
	}
	if !byName["multi"].Enabled || len(byName["multi"].AllowedGroups) != 1 || byName["multi"].AllowedGroups[0] != "multi-group" {
		t.Fatalf("multi-group token must survive with its remaining group: %+v", byName["multi"])
	}
	if !byName["unrestricted"].Enabled || len(byName["unrestricted"].AllowedGroups) != 0 {
		t.Fatalf("unrestricted token must be untouched: %+v", byName["unrestricted"])
	}

	// 管理响应透出禁用名单。
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/admin/groups/g-multi", nil)
	c.Params = gin.Params{{Key: "id", Value: "g-multi"}}
	s.adminDeleteGroup(c)
	if !strings.Contains(rec.Body.String(), `"disabledTokens":["multi"]`) {
		t.Fatalf("admin response must surface disabled tokens: %s", rec.Body.String())
	}
}

// DBG-002 回归:query 鉴权的 API Key 不得随网络错误文本流向下游——传输错误
// 统一剥离 URL 查询串。
func TestQueryAuthSecretSanitizedInTransportError(t *testing.T) {
	// 上游接受连接后立即关闭,制造携带 URL 的传输错误。
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, _ := w.(http.Hijacker).Hijack()
		_ = conn.Close()
	}))
	defer upstream.Close()

	adapter := relay.NewOpenAIAdapter(5_000_000_000)
	rendered := &relay.CustomProtocolRequestResult{
		Method: http.MethodPost,
		Path:   "/generate",
		Auth:   relay.CustomProtocolAuth{Mode: "query", Query: "api_key"},
	}
	_, err := adapter.SendCustomProtocolRequest(context.Background(), upstream.URL, "AUDIT_UPSTREAM_SECRET", rendered, false)
	if err == nil {
		t.Fatal("expected a transport error from the closed connection")
	}
	if strings.Contains(err.Error(), "AUDIT_UPSTREAM_SECRET") {
		t.Fatalf("transport error must not leak the query auth key: %v", err)
	}
	if !strings.Contains(err.Error(), "/generate") {
		t.Fatalf("sanitized error should keep the non-secret URL path for diagnosis: %v", err)
	}
}
