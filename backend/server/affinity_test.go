package server

import (
	"testing"
	"time"

	"github.com/elysia-api/backend/config"
)

func refs(names ...string) []config.ModelRef {
	out := make([]config.ModelRef, 0, len(names))
	for _, n := range names {
		out = append(out, config.ModelRef{Name: n})
	}
	return out
}

func TestApplyAffinityMovesStickyToFront(t *testing.T) {
	cands := refs("a", "b", "c")
	got := applyAffinity(cands, "c")
	want := []string{"c", "a", "b"}
	for i, r := range got {
		if r.Name != want[i] {
			t.Fatalf("applyAffinity order = %v, want %v", names(got), want)
		}
	}
}

func TestApplyAffinityNoopWhenAbsentOrFirst(t *testing.T) {
	cands := refs("a", "b", "c")
	if got := applyAffinity(cands, "a"); got[0].Name != "a" || len(got) != 3 {
		t.Fatalf("sticky already first should be unchanged: %v", names(got))
	}
	if got := applyAffinity(cands, "zzz"); got[0].Name != "a" {
		t.Fatalf("absent sticky should be no-op: %v", names(got))
	}
	if got := applyAffinity(cands, ""); got[0].Name != "a" {
		t.Fatalf("empty sticky should be no-op")
	}
}

func TestAffinityCacheGetSetTTL(t *testing.T) {
	a := newAffinityCache()
	now := time.Unix(1_000_000, 0)
	a.set("keyhash", "g1", "model-x", now)

	if got := a.get("keyhash", "g1", now.Add(time.Minute)); got != "model-x" {
		t.Fatalf("within TTL should return model-x, got %q", got)
	}
	if got := a.get("keyhash", "g1", now.Add(AffinityTTL+time.Second)); got != "" {
		t.Fatalf("after TTL should expire, got %q", got)
	}
	// 不同 key / group 隔离
	if got := a.get("other", "g1", now); got != "" {
		t.Fatalf("different key should not match")
	}
	if got := a.get("keyhash", "g2", now); got != "" {
		t.Fatalf("different group should not match")
	}
}

func TestAffinityCacheNilSafe(t *testing.T) {
	var a *affinityCache
	if got := a.get("k", "g", time.Now()); got != "" {
		t.Fatalf("nil cache get should return empty")
	}
	a.set("k", "g", "m", time.Now()) // 不应 panic
}

func TestAffinityEmptyKeyHashIgnored(t *testing.T) {
	a := newAffinityCache()
	now := time.Unix(2_000_000, 0)
	a.set("", "g1", "m", now) // 无 keyHash（未鉴权场景）不记录
	if got := a.get("", "g1", now); got != "" {
		t.Fatalf("empty keyHash should never produce affinity")
	}
}
