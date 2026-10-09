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

func (s *Server) migrateLegacyCustomProtocols() error {
	if s.store == nil {
		return nil
	}
	entries, err := s.config.ReadDeprecatedCustomProtocols()
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	existing, err := s.store.ListCustomProtocols(context.Background())
	if err != nil {
		return err
	}
	known := make(map[string]bool, len(existing))
	for _, row := range existing {
		known[strings.ToLower(row.ID)] = true
	}
	imported := 0
	for _, raw := range entries {
		var protocol relay.CustomProtocolConfig
		if err := json.Unmarshal(raw, &protocol); err != nil {
			return fmt.Errorf("legacy custom protocol invalid: %w", err)
		}
		id := strings.ToLower(strings.TrimSpace(protocol.ID))
		if id == "" {
			return fmt.Errorf("legacy custom protocol requires an id")
		}
		if known[id] {
			continue
		}
		if err := s.store.UpsertCustomProtocol(context.Background(), customProtocolRow(protocol, string(raw))); err != nil {
			return err
		}
		imported++
		known[id] = true
	}
	if imported > 0 {
		log.Printf("migrated %d custom protocol(s) from config.json into the database", imported)
	}
	return s.config.FinishDeprecatedCustomProtocols(entries)
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
