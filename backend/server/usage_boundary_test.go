package server

import (
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// 内存回退路径与 SQL 侧的 [From, To) 半开区间口径一致：to 毫秒边界上的记录排除，
// to-1ms 的记录保留。此前内存路径是闭区间（包含 to），store 与非 store 两种部署
// 模式在边界毫秒记录上统计归属不同。
func TestFilterUsageRecordsHalfOpenToBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	to := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	records := []usageRecord{
		{StartedAt: to.Add(-time.Millisecond), ModelName: "before"},
		{StartedAt: to, ModelName: "at-boundary"},
		{StartedAt: to.Add(time.Millisecond), ModelName: "after"},
	}
	c, _ := gin.CreateTestContext(nil)
	c.Request = nil // 无 HTTP 查询参数：只用时间窗过滤

	filtered := filterUsageRecords(records, c, time.Time{}, to)
	if len(filtered) != 1 || filtered[0].ModelName != "before" {
		names := make([]string, 0, len(filtered))
		for _, r := range filtered {
			names = append(names, r.ModelName)
		}
		t.Fatalf("half-open [from,to) must keep only the record before `to`, got %v", names)
	}

	// from 边界：from 毫秒上的记录保留（含下界）。
	from := to.Add(-time.Millisecond)
	filtered = filterUsageRecords(records, c, from, to)
	if len(filtered) != 1 || filtered[0].ModelName != "before" {
		t.Fatalf("lower bound must be inclusive, got %d records", len(filtered))
	}
}
