package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
	"github.com/gin-gonic/gin"
)

const protocolAdminMaxBodyBytes = 8 << 20

func (s *Server) migrateLegacyCustomProtocols() {
	if s.store == nil {
		return
	}
	entries := s.config.TakeDeprecatedCustomProtocols()
	if len(entries) == 0 {
		return
	}
	existing, err := s.store.ListCustomProtocols(context.Background())
	if err != nil {
		log.Printf("custom protocol migration aborted: %v", err)
		return
	}
	known := make(map[string]bool, len(existing))
	for _, row := range existing {
		known[strings.ToLower(row.ID)] = true
	}
	imported := 0
	for _, raw := range entries {
		var protocol relay.CustomProtocolConfig
		if err := json.Unmarshal(raw, &protocol); err != nil {
			log.Printf("custom protocol migration skipped an invalid entry: %v", err)
			continue
		}
		id := strings.ToLower(strings.TrimSpace(protocol.ID))
		if id == "" || known[id] {
			continue
		}
		if err := s.store.UpsertCustomProtocol(context.Background(), customProtocolRow(protocol, string(raw))); err != nil {
			log.Printf("custom protocol migration failed for %q: %v", protocol.ID, err)
			continue
		}
		imported++
	}
	if imported > 0 {
		log.Printf("migrated %d custom protocol(s) from config.json into the database", imported)
	}
}

func findCustomProtocolTestModel(ctx context.Context, store *storage.Store, sourceID, modelName string) (storage.Model, bool) {
	models, err := store.ListModelsFiltered(ctx, storage.ModelListFilter{SourceID: strings.TrimSpace(sourceID)})
	if err != nil {
		return storage.Model{}, false
	}
	modelName = strings.TrimSpace(modelName)
	for _, model := range models {
		if model.Name == modelName || model.ID == modelName {
			return model, true
		}
	}
	for _, model := range models {
		if strings.EqualFold(model.Name, modelName) || strings.EqualFold(model.ID, modelName) {
			return model, true
		}
	}
	return storage.Model{}, false
}

func bindAdminJSON(c *gin.Context, target any) error {
	defer c.Request.Body.Close()
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, protocolAdminMaxBodyBytes))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, target)
}

// probeTimeout 计算探活类请求(真实测试/试拉/助手)的超时:自定义基础值,
// 被更短的 HTTP 全局超时钳制。
func (s *Server) probeTimeout(base time.Duration) time.Duration {
	if seconds := s.config.GetHTTPTimeout(); seconds > 0 && time.Duration(seconds)*time.Second < base {
		return time.Duration(seconds) * time.Second
	}
	return base
}

func truncateForDisplay(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	// 按字节截断可能劈开多字节字符：回退到最近的 rune 边界再切。
	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut] + fmt.Sprintf("\n…（已截断，共 %d 字节）", len(value))
}
