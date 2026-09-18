package server

import (
	"testing"
	"time"
)

// 跨午夜窗口的日配额保护:adjustTokenUsage 带 acquire 日期,跨日结算直接
// 丢弃(settle 到旧一天等价于不写),不得把旧一天消耗计入新一天。
// release 闭包的同源日期守卫(current.Date == acquiredDate 才退还预留)
// 与此同一比较,依赖真实时钟跨越无法在单进程构造,由代码审查保证。
func TestRateLimitMidnightRolloverProtectsNewDay(t *testing.T) {
	s := newTestServer(nil)
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")

	// 新一天已有计数。
	s.rateLimitMu.Lock()
	s.rateLimits["g1"] = &rateLimitState{Date: time.Now().Format("2006-01-02"), Tokens: 100}
	s.rateLimitMu.Unlock()

	// 跨日在途请求的实际消耗(昨天 acquire)不得加进今天。
	s.adjustTokenUsage("g1", 2000, yesterday)
	s.rateLimitMu.Lock()
	tokens := s.rateLimits["g1"].Tokens
	s.rateLimitMu.Unlock()
	if tokens != 100 {
		t.Fatalf("adjust must skip cross-day settlement: got %d, want 100", tokens)
	}

	// 同日结算正常累加。
	s.adjustTokenUsage("g1", 50, time.Now().Format("2006-01-02"))
	s.rateLimitMu.Lock()
	tokens = s.rateLimits["g1"].Tokens
	s.rateLimitMu.Unlock()
	if tokens != 150 {
		t.Fatalf("same-day adjust must accumulate: got %d, want 150", tokens)
	}
}
