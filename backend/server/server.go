package server

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elysia-api/backend/agent"
	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/internal/admission"
	"github.com/elysia-api/backend/internal/routing"
	"github.com/elysia-api/backend/logging"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
	"github.com/gin-gonic/gin"
)

// Server composes application services and their HTTP adapters. Construction is
// in bootstrap.go; lifecycle.go owns request draining and resource shutdown.
type Server struct {
	config            *config.Config
	engine            *gin.Engine
	store             *storage.Store
	logger            *logging.Logger
	protocolTransport *relay.ProtocolTransport
	startupErr        error

	// Routing components own their synchronization and mutable state.
	routes          routing.Cache
	scheduler       routing.Scheduler
	limiter         routing.Limiter
	affinity        *affinityCache
	conversionPlans gatewayConversionPlans
	preparationOnce sync.Once
	preparation     *admission.Limiter

	refreshJobs   sourceRefreshJobs
	catalog       *modelCatalog
	healthChecker *healthChecker

	// Request producers stop before usage persistence is canceled and drained.
	usageWriterMu    sync.Mutex
	usageWriter      *usageWriterState
	usageWriteGen    atomic.Uint64 // Reset discards queued records from old generations.
	usageSeq         atomic.Uint64
	usageWriteCtx    context.Context
	usageWriteCancel context.CancelFunc
	usagePersistMu   sync.Mutex
	usageStatsMu     sync.Mutex
	usageStats       logging.Stats
	usageWriteErr    error
	usageCache       usageResponseCache
	usageRetention   *usageRetention
	assetsUsageMu    sync.Mutex
	assetsUsage      usageAssetsUsage
	assetsUsageAt    time.Time

	httpServer          *http.Server
	shutdownErr         error
	shutdownOnce        sync.Once
	lifecycleOnce       sync.Once
	lifecycleCtx        context.Context
	lifecycleCancel     context.CancelFunc
	shutdownRequested   chan struct{}
	shutdownRequestOnce sync.Once
	shutdownDone        chan struct{} // Closed only after the full shutdown sequence.
	requestMu           sync.Mutex
	requestWG           sync.WaitGroup
	stopping            bool

	restartMu       sync.Mutex
	restartRequired bool
	configUpdateMu  sync.Mutex // Serializes persistence, publication and reload.

	// Test-only preflight bypass for local httptest upstreams; false in production.
	skipOutboundValidation bool

	agentEngineOnce   sync.Once
	agentEngineMu     sync.Mutex
	agentEngineClosed bool
	agentEngineInst   *agent.Engine
	a2aOnce           sync.Once
	a2aRuntime        *a2aService
	mcpOnce           sync.Once
	mcpHandler        http.Handler

	protocolServiceOnce       sync.Once
	protocolServiceInst       *protocol.Service
	protocolServiceErr        error
	isProtocolRuntimeRequired atomic.Bool
	protocolRuntimeReady      atomic.Bool
	protocolRuntimeFailure    atomic.Pointer[string]
	protocolStartupFailure    atomic.Pointer[protocolStartupFailure]
	protocolStartupMu         sync.Mutex
	gatewaySessions           gatewaySessionSet
	protocolUses              protocolRevisionUses
	gatewayJobs               gatewayJobService
}
