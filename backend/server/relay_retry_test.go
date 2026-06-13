package server

import (
	"sync"
	"testing"

	"github.com/elysia-api/backend/config"
)

func makeGroup(strategy string, modelNames ...string) *config.ModelGroupConfig {
	models := make([]config.ModelRef, 0, len(modelNames))
	for _, name := range modelNames {
		models = append(models, config.ModelRef{Name: name, BaseURL: "https://example.com/v1", Platform: "openai"})
	}
	return &config.ModelGroupConfig{ID: "g1", Name: "grp", Strategy: strategy, Models: models}
}

func names(refs []config.ModelRef) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.Name)
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestOrderedCandidatesSequential(t *testing.T) {
	g := makeGroup("sequential", "a", "b", "c")
	got := names(orderedCandidates(g, 0))
	if !eq(got, []string{"a", "b", "c"}) {
		t.Fatalf("sequential order wrong: %v", got)
	}
}

func TestOrderedCandidatesDefault(t *testing.T) {
	g := makeGroup("", "a", "b", "c")
	got := names(orderedCandidates(g, 0))
	if !eq(got, []string{"a", "b", "c"}) {
		t.Fatalf("default order wrong: %v", got)
	}
}

func TestOrderedCandidatesRoundRobinRotation(t *testing.T) {
	g := makeGroup("round-robin", "a", "b", "c")
	// 起点为 1 时，应从 b 开始环绕一圈：b, c, a
	got := names(orderedCandidates(g, 1))
	if !eq(got, []string{"b", "c", "a"}) {
		t.Fatalf("round-robin rotation wrong: %v", got)
	}
}

// 关键回归：高危1。起点（来自旧快照）超出当前模型数量时，
// 不应 panic，而应安全 clamp。
func TestOrderedCandidatesRoundRobinStaleIndexNoPanic(t *testing.T) {
	g := makeGroup("round-robin", "a", "b")
	got := names(orderedCandidates(g, 5)) // 5 % 2 = 1 → 从 b 开始
	if !eq(got, []string{"b", "a"}) {
		t.Fatalf("stale index clamp wrong: %v", got)
	}
}

func TestOrderedCandidatesEmpty(t *testing.T) {
	g := makeGroup("round-robin")
	if got := orderedCandidates(g, 0); got != nil {
		t.Fatalf("expected nil for empty group, got %v", got)
	}
}

func TestOrderedCandidatesRandomCoversAll(t *testing.T) {
	g := makeGroup("random", "a", "b", "c")
	got := orderedCandidates(g, 0)
	if len(got) != 3 {
		t.Fatalf("random should return all candidates, got %d", len(got))
	}
	seen := map[string]bool{}
	for _, r := range got {
		seen[r.Name] = true
	}
	if len(seen) != 3 {
		t.Fatalf("random should include every model exactly once: %v", names(got))
	}
}

func TestNextRoundRobinIndexAdvancesAndWraps(t *testing.T) {
	s := &Server{roundRobinIndex: map[string]int{}}
	got := []int{}
	for i := 0; i < 5; i++ {
		got = append(got, s.nextRoundRobinIndex("g1", 3))
	}
	want := []int{0, 1, 2, 0, 1}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("round-robin index sequence wrong: got %v want %v", got, want)
		}
	}
}

func TestNextRoundRobinIndexEmpty(t *testing.T) {
	s := &Server{roundRobinIndex: map[string]int{}}
	if idx := s.nextRoundRobinIndex("g1", 0); idx != 0 {
		t.Fatalf("expected 0 for empty group, got %d", idx)
	}
}

// 并发回归（高危1 + 高危2）：多 goroutine 同时推进 round-robin 游标
// 并读取候选列表，配合模型数量变化，验证不会越界 panic / 死锁。
// 注意：要真正检出数据竞争需在装有 C 编译器的环境用 `go test -race` 运行。
func TestRoundRobinConcurrentNoPanic(t *testing.T) {
	s := &Server{roundRobinIndex: map[string]int{}}
	g := makeGroup("round-robin", "a", "b", "c")
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				start := s.nextRoundRobinIndex(g.ID, len(g.Models))
				cands := orderedCandidates(g, start)
				if len(cands) != 3 {
					t.Errorf("expected 3 candidates, got %d", len(cands))
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestMaxAttempts(t *testing.T) {
	cases := []struct {
		maxRetries, candidates, want int
	}{
		{0, 3, 1},  // 不重试：只试 1 次
		{2, 3, 3},  // 重试 2 次：共 3 次
		{5, 3, 3},  // 重试次数超过候选数：上限为候选数
		{2, 1, 1},  // 只有 1 个候选
		{-1, 3, 1}, // 负数视为 0
		{3, 0, 0},  // 没有候选
	}
	for _, tc := range cases {
		if got := maxAttempts(tc.maxRetries, tc.candidates); got != tc.want {
			t.Fatalf("maxAttempts(%d,%d)=%d want %d", tc.maxRetries, tc.candidates, got, tc.want)
		}
	}
}

func TestShouldRetryStatus(t *testing.T) {
	retry := []int{0, -1, 408, 409, 425, 429, 500, 502, 503, 504, 524, 599}
	noRetry := []int{200, 201, 204, 301, 400, 401, 403, 404, 422, 501, 505}
	for _, code := range retry {
		if !shouldRetryStatus(code) {
			t.Fatalf("status %d should be retryable", code)
		}
	}
	for _, code := range noRetry {
		if shouldRetryStatus(code) {
			t.Fatalf("status %d should NOT be retryable", code)
		}
	}
}
