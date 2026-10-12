package server

import "testing"

func TestCLIGroupCreateRejectsDuplicateSlug(t *testing.T) {
	s := newOpsTestServer(t)
	tool := &createGroupTool{server: s}
	if result := opsExecute(t, tool, `{"name":"audit-group","strategy":"random","maxConcurrency":9,"models":["s:m"]}`); !result.OK {
		t.Fatal(result.Summary)
	}
	if result := opsExecute(t, tool, `{"name":"audit-group"}`); result.OK {
		t.Fatal("duplicate create succeeded")
	}
	groups, err := s.store.ListGroups(t.Context())
	if err != nil || len(groups) != 1 || groups[0].Strategy != "random" || groups[0].MaxConcurrency != 9 || len(groups[0].Models) != 1 {
		t.Fatalf("duplicate replaced group: %+v, %v", groups, err)
	}
}
