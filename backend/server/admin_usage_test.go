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
