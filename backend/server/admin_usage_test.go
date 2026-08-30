package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elysia-api/backend/storage"
	"github.com/gin-gonic/gin"
)

func TestAdminUsageTrendValidatesUTCOffset(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := storage.Open(filepath.Join(t.TempDir(), "admin-usage.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	defer store.Close()
	server := &Server{store: store}

	for _, value := range []string{"not-a-number", "841", "-841"} {
		t.Run(value, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/api/admin/usage/trend?utcOffsetMinutes="+value, nil)

			server.adminUsageTrend(ctx)

			if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"code":"invalid_utc_offset"`) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}

	for _, value := range []string{"-840", "0", "840"} {
		t.Run("valid_"+value, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/api/admin/usage/trend?utcOffsetMinutes="+value, nil)

			server.adminUsageTrend(ctx)

			if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"data":[]`) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestAdminUsagePulseValidatesBucket(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := storage.Open(filepath.Join(t.TempDir(), "admin-pulse.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	defer store.Close()
	server := &Server{store: store}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/admin/usage/pulse?bucketMinutes=7", nil)
	server.adminUsagePulse(ctx)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"code":"invalid_bucket"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/admin/usage/pulse?bucketMinutes=1&utcOffsetMinutes=480", nil)
	server.adminUsagePulse(ctx)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"code":"invalid_from"`) {
		t.Fatalf("missing from status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/admin/usage/pulse?bucketMinutes=1&utcOffsetMinutes=480&from=2026-08-01T00:00:00Z&to=2026-08-04T00:00:00Z", nil)
	server.adminUsagePulse(ctx)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"code":"window_too_long"`) {
		t.Fatalf("long window status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/admin/usage/pulse?bucketMinutes=1&utcOffsetMinutes=480&from=2026-08-02T12:00:00Z&to=2026-08-02T13:00:00Z", nil)
	server.adminUsagePulse(ctx)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"points":[]`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/admin/usage/pulse?bucketMinutes=1&utcOffsetMinutes=480&from=2026-08-02T13:00:00Z&to=2026-08-02T12:00:00Z", nil)
	server.adminUsagePulse(ctx)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"code":"invalid_range"`) {
		t.Fatalf("inverted range status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAdminUsageByModelDailyValidatesTop(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := storage.Open(filepath.Join(t.TempDir(), "admin-model-daily.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	defer store.Close()
	server := &Server{store: store}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/admin/usage/by-model-daily?top=0", nil)
	server.adminUsageByModelDaily(ctx)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"code":"invalid_top"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/admin/usage/by-model-daily?utcOffsetMinutes=480", nil)
	server.adminUsageByModelDaily(ctx)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"data":[]`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestUsageQueryFromRequestParsesSemanticStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/?status=FAILED&statusCode=500", nil)

	query := usageQueryFromRequest(ctx)
	if query.Status != "failed" || query.StatusCode != 500 {
		t.Fatalf("usageQueryFromRequest() = %#v", query)
	}

	recorder = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/?status=unknown", nil)
	if query := usageQueryFromRequest(ctx); query.Status != "" {
		t.Fatalf("unknown status must be ignored, got %#v", query)
	}
}
