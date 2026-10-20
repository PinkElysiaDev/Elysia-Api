package server

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/logging"
	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
	"github.com/gin-gonic/gin"
)

func New(cfg *config.Config) *Server {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	server := &Server{
		config: cfg, engine: engine,
		protocolTransport: relay.NewProtocolTransport(time.Duration(cfg.GetHTTPTimeout()) * time.Second),
		affinity:          newAffinityCache(),
		sourceRefreshing:  make(map[string]bool),
		sourceLastFetch:   make(map[string]sourceRefreshState),
		refreshSem:        make(chan struct{}, sourceRefreshConcurrency),
		catalog: newModelCatalog(cfg.GetModelCatalog, func() string {
			dbPath := cfg.GetDatabasePath()
			if strings.TrimSpace(dbPath) == "" {
				return ""
			}
			return filepath.Join(filepath.Dir(dbPath), "model-catalog.json")
		}),
	}
	server.initLifecycle()
	server.logger = logging.New(cfg.GetLoggingOptions(), nil)
	engine.Use(server.requestLogging())
	store, err := storage.OpenWithLogPath(cfg.GetDatabasePath(), cfg.GetLogDatabasePath(), cfg.GetDBEncryptionKey())
	if err != nil {
		server.startupErr = fmt.Errorf("open sqlite store: %w", err)
		return server
	}
	server.store = store
	server.logger = logging.New(cfg.GetLoggingOptions(), logging.NewWriter(store.LogStore.WriteSystemLogBatch))
	if hasEncrypted, decryptOK, err := store.SecretIntegrityProbe(context.Background()); err != nil {
		server.logEvent("error").Err(err).Msg("secret integrity probe failed")
	} else if hasEncrypted && !decryptOK {
		server.logEvent("error").Msg("encrypted secrets cannot be decrypted; restore the original .master-key or ELYSIA_API_MASTER_KEY. Routing and token/source administration are unavailable until repaired; rows are retained")
	}
	store.StartRollupBackfill()
	protocolErr := server.initializeProtocolRuntime(context.Background())
	// Only start background work after storage migrations have succeeded.
	server.syncOutboundPolicy()
	go server.catalog.runPeriodic()
	if protocolErr != nil {
		server.logEvent("error").Err(protocolErr).Msg("protocol runtime requires repair; generation is disabled")
	}
	listen := cfg.GetServer()
	server.logSystemEvent("info", "server started", map[string]any{
		"version": AppVersion, "host": listen.Host, "port": listen.Port,
	})
	return server
}
