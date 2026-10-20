package server

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/elysia-api/backend/storage"
)

func TestTokenMutationConflictsAreAtomic(t *testing.T) {
	for _, kind := range []string{"admin", "cli"} {
		t.Run(kind, func(t *testing.T) {
			st, err := storage.Open(filepath.Join(t.TempDir(), "review.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			ctx := context.Background()
			for _, token := range []storage.APIToken{{Name: "first", Token: "synthetic-first", Enabled: true}, {Name: "second", Token: "synthetic-second", Enabled: true}} {
				if err := st.UpsertAPIToken(ctx, token); err != nil {
					t.Fatal(err)
				}
			}
			s := &Server{store: st}
			before := s.routeSnapshot()
			if before == nil {
				t.Fatal("initial route snapshot unavailable")
			}
			if kind == "admin" {
				c, r := adminProtocolContext(http.MethodPut, "/tokens/first", `{"name":"first","newName":"second","enabled":false}`)
				s.adminUpsertToken(c)
				first, _, err := st.FindAPITokenByName(ctx, "first")
				if err != nil || r.Code != 409 || !first.Enabled || s.routeSnapshot() != before {
					t.Fatalf("partial mutation: code=%d enabled=%t err=%v", r.Code, first.Enabled, err)
				}
				t.Log("conflicting rename leaves DB and cache unchanged")
			} else {
				result := (&updateAPIKeyTool{server: s}).Execute(ctx, &opsTestContext{}, json.RawMessage(`{"name":"first","newName":"renamed","newSecret":"synthetic-second"}`))
				_, old, _ := st.FindAPITokenByName(ctx, "first")
				_, renamed, _ := st.FindAPITokenByName(ctx, "renamed")
				if result.OK || !old || renamed {
					t.Fatalf("partial mutation: ok=%t old=%t renamed=%t", result.OK, old, renamed)
				}
				t.Log("duplicate secret rolls rename back")
			}
		})
	}
}

func TestCorruptTokenAuthorizationCannotPopulateRouteCache(t *testing.T) {
	s := newOpsTestServer(t)
	if _, err := s.store.SaveAPIToken(t.Context(), "restricted", storage.APITokenUpdate{Token: "synthetic-restricted", AllowedGroups: []string{"private"}}, true); err != nil {
		t.Fatal(err)
	}
	if err := s.store.ExecRaw(t.Context(), `UPDATE api_tokens SET allowed_groups_json='broken-json' WHERE name='restricted'`); err != nil {
		t.Fatal(err)
	}
	s.invalidateRouteCache()
	if _, isFound := s.findAccessToken("synthetic-restricted"); isFound || s.routeSnapshot() != nil {
		t.Fatal("corrupt authorization was accepted into the routing cache")
	}
}
