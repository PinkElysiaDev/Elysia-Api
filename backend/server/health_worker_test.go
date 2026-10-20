package server

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/storage"
)

func TestHealthWorkerNeutralEvidenceAndRouteInvalidation(t *testing.T) {
	t.Parallel()
	s, _ := newRetentionTestServer(t)
	s.config.HealthCheck = config.HealthCheckConfig{FailureThreshold: 2}
	source := storage.ModelSource{ID: "s", Name: "s", BaseURL: "https://upstream.invalid", APIKey: "key", Enabled: true}
	if err := s.store.UpsertSource(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	if err := s.store.ReplaceSourceModels(t.Context(), source, []storage.Model{{ID: "m", Name: "m"}}); err != nil {
		t.Fatal(err)
	}
	h := newHealthChecker(s)
	defer h.cancel()
	result := probeUnhealthy
	h.probe = func(context.Context, storage.Model, int) healthProbeResult { return result }
	invalidations := 0
	h.invalidate = func() { invalidations++ }
	h.runOnce() // First failure does not cross the threshold.
	result = probeUnavailable
	h.runOnce() // Missing evidence must neither count as failure nor reset it.
	if invalidations != 0 {
		t.Fatal("neutral evidence changed availability")
	}
	result = probeUnhealthy
	h.runOnce()
	models, err := s.store.ListModels(t.Context())
	if err != nil || len(models) != 1 || models[0].Available || invalidations != 1 {
		t.Fatalf("threshold transition: models=%+v invalidations=%d err=%v", models, invalidations, err)
	}
	result = probeHealthy
	h.runOnce()
	models, err = s.store.ListModels(t.Context())
	if err != nil || !models[0].Available || invalidations != 2 {
		t.Fatalf("recovery: models=%+v invalidations=%d err=%v", models, invalidations, err)
	}
}

func TestHealthWorkerDisabledAndHotEnable(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		// No repository is needed while disabled. Enabling must initiate a
		// round on the next interval, without reconstructing the worker.
		s := &Server{config: &config.Config{}}
		h := newHealthChecker(s)
		var enabled atomic.Bool
		h.settings = func() config.HealthCheckConfig {
			return config.HealthCheckConfig{Enabled: enabled.Load(), IntervalSeconds: 1, TimeoutSeconds: 1}
		}
		defer h.cancel()
		h.store = &healthWakeRepository{cancel: h.cancel}
		h.start()
		synctest.Wait()
		enabled.Store(true)
		time.Sleep(time.Second)
		synctest.Wait()
		if h.ctx.Err() == nil {
			t.Fatal("hot enable did not start a probe round")
		}
		h.shutdown()
	})
}

type healthWakeRepository struct{ cancel context.CancelFunc }

func (r *healthWakeRepository) ListAllModelsForProbe(context.Context) ([]storage.Model, error) {
	r.cancel()
	return nil, context.Canceled
}
func (*healthWakeRepository) ListSources(context.Context) ([]storage.ModelSource, error) {
	panic("unexpected source lookup")
}
func (*healthWakeRepository) SetModelAvailability(context.Context, string, string, bool) (int64, error) {
	panic("unexpected availability write")
}
