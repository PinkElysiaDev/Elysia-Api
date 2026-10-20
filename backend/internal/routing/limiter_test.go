package routing

import (
	"sync"
	"testing"
	"time"

	"github.com/elysia-api/backend/config"
)

func TestRateLimitMidnightRolloverProtectsNewDay(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 20, 23, 59, 0, 0, time.Local)
	limiter := Limiter{now: func() time.Time { return now }}
	group := &config.ModelGroupConfig{ID: "g1", MaxConcurrency: 2, DailyLimitMaxTokens: 1000}
	yesterday := now.Format("2006-01-02")
	oldRelease, err := limiter.Acquire(group, 800)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	newRelease, err := limiter.Acquire(group, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := limiter.Acquire(group, 0); err == nil {
		t.Fatal("midnight cleared in-flight concurrency")
	}
	limiter.Adjust("g1", 2000, yesterday)
	oldRelease()
	if got := limiter.Usage("g1"); got.Tokens != 100 || got.Active != 1 || got.Requests != 1 {
		t.Fatalf("old-day settlement changed new-day reservations: %+v", got)
	}
	limiter.Adjust("g1", 50, now.Format("2006-01-02"))
	newRelease()
	if got := limiter.Usage("g1"); got.Tokens != 50 || got.Active != 0 {
		t.Fatalf("same-day settlement did not retain actual usage: %+v", got)
	}
}

func TestReservationReleaseIsConcurrentAndIdempotent(t *testing.T) {
	t.Parallel()
	var limiter Limiter
	group := &config.ModelGroupConfig{ID: "g1", MaxConcurrency: 2}
	release, err := limiter.Acquire(group, 800)
	if err != nil {
		t.Fatal(err)
	}
	other, err := limiter.Acquire(group, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer other()
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(release)
	}
	wg.Wait()
	if got := limiter.Usage(group.ID); got.Active != 1 || got.Tokens != 100 {
		t.Fatalf("duplicate release affected another reservation: %+v", got)
	}
}

func TestDeletedGroupReleaseCannotAffectReplacement(t *testing.T) {
	t.Parallel()
	var limiter Limiter
	group := &config.ModelGroupConfig{ID: "g1"}
	old, _ := limiter.Acquire(group, 800)
	limiter.Forget(group.ID)
	release, _ := limiter.Acquire(group, 100)
	defer release()
	old()
	if got := limiter.Usage(group.ID); got.Active != 1 || got.Tokens != 100 {
		t.Fatalf("old reservation affected replacement group: %+v", got)
	}
}
