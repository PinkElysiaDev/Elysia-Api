package server

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/elysia-api/backend/config"
)

func refs(names ...string) []config.ModelRef {
	out := make([]config.ModelRef, 0, len(names))
	for _, n := range names {
		out = append(out, config.ModelRef{ID: n, Name: n})
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
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		a := newAffinityCache()
		defer a.close()
		a.set("keyhash", "g1", "model-x")
		a.set("other", "g1", "model-y")
		time.Sleep(AffinityTTL - time.Second)
		if got := a.get("keyhash", "g1"); got != "model-x" {
			t.Fatalf("within TTL should return model-x, got %q", got)
		}
		if got := a.get("keyhash", "g2"); got != "" {
			t.Fatalf("different group should not match, got %q", got)
		}
		a.set("other", "g1", "model-z")
		time.Sleep(2 * time.Second)
		if got := a.get("keyhash", "g1"); got != "" {
			t.Fatalf("reads must not extend TTL, got %q", got)
		}
		if got := a.get("other", "g1"); got != "model-z" {
			t.Fatalf("writes must replace the value and refresh TTL, got %q", got)
		}
		time.Sleep(AffinityTTL)
		if got := a.get("other", "g1"); got != "" {
			t.Fatalf("updated entry must eventually expire, got %q", got)
		}
	})
}

func TestAffinityCacheRemoveGroup(t *testing.T) {
	t.Parallel()
	a := newAffinityCache()
	defer a.close()
	a.set("first", "g1", "model-a")
	a.set("second", "g1", "model-b")
	a.set("first", "prefix-g1", "model-c")
	a.removeGroup("g1")
	if a.get("first", "g1") != "" || a.get("second", "g1") != "" || a.get("first", "prefix-g1") != "model-c" {
		t.Fatal("group removal must invalidate all and only the matching group's entries")
	}
}

func TestAffinityCacheNilSafe(t *testing.T) {
	var a *affinityCache
	if got := a.get("k", "g"); got != "" {
		t.Fatalf("nil cache get should return empty")
	}
	a.set("k", "g", "m") // 不应 panic
	a.close()
}

func TestAffinityEmptyKeyHashIgnored(t *testing.T) {
	a := newAffinityCache()
	defer a.close()
	a.set("", "g1", "m") // 无 keyHash（未鉴权场景）不记录
	if got := a.get("", "g1"); got != "" {
		t.Fatalf("empty keyHash should never produce affinity")
	}
}
