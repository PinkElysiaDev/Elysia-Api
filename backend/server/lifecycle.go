package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"

	"github.com/elysia-api/backend/logging"
)

func (s *Server) ListenAndServe() (result error) {
	s.initLifecycle()
	defer func() { s.doShutdown(); result = errors.Join(result, s.shutdownErr) }()
	if s.startupErr != nil {
		return s.startupErr
	}
	s.setupRoutes()
	// OnRun runs after Hertz initialization, before binding the listener. Any
	// hook or bind failure still goes through the deferred application shutdown.
	s.engine.OnRun = append(s.engine.OnRun, s.startServices)

	listen := s.config.GetServer()
	addr := fmt.Sprintf("%s:%d", listen.Host, listen.Port)
	s.logEvent("info").Str("address", addr).Msg("starting server")

	// main owns OS signals; all shutdown sources share RequestShutdown.
	serveDone := make(chan struct{})
	defer close(serveDone)
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for !s.engine.IsRunning() {
			select {
			case <-serveDone:
				return
			case <-ticker.C:
			}
		}
		launchConsoleBrowser(s.config.OpenBrowserOnStart, listen.Host, listen.Port)
		select {
		case <-s.shutdownRequested:
			s.logEvent("info").Msg("shutdown requested; draining")
		case <-serveDone:
			return
		}
		s.doShutdown()
	}()

	return s.engine.Run()
}

func (s *Server) startServices(context.Context) error {
	s.startUsageWriter()
	if err := s.startGatewayJobs(); err != nil {
		return err
	}
	if s.store != nil {
		s.store.StartRollupBackfill()
	}
	go s.catalog.runPeriodic()
	s.healthChecker = newHealthChecker(s)
	s.healthChecker.start()
	s.usageRetention = newUsageRetention(s)
	s.usageRetention.start()
	return nil
}

func (s *Server) initLifecycle() {
	s.lifecycleOnce.Do(func() {
		s.lifecycleCtx, s.lifecycleCancel = context.WithCancel(context.Background())
		s.usageWriteCtx, s.usageWriteCancel = context.WithCancel(context.Background())
		s.shutdownRequested = make(chan struct{})
		s.shutdownDone = make(chan struct{})
	})
}

// RequestShutdown is safe before ListenAndServe binds its listener.
func (s *Server) RequestShutdown() {
	s.initLifecycle()
	s.shutdownRequestOnce.Do(func() { close(s.shutdownRequested) })
}

func (s *Server) shutdown(ctx context.Context, c *app.RequestContext) {
	s.logSystemEvent("info", "server shutdown requested", nil)
	c.JSON(http.StatusOK, utils.H{"shuttingDown": true})
	// JSON is buffered. Signal only after Hertz has attempted to send the
	// acknowledgment; RequestShutdown must remain non-blocking inside Finish.
	onHTTPComplete(c, s.RequestShutdown)
}

func (s *Server) doShutdown() {
	s.initLifecycle()
	s.shutdownOnce.Do(func() {
		defer close(s.shutdownDone)
		defer s.closeCaches()
		deadline := time.Now().Add(12 * time.Second)
		defer s.usageWriteCancel()
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		defer cancel()
		s.httpRequests.stop()
		s.preparationLimiter().Close()
		s.lifecycleCancel()
		if s.healthChecker != nil {
			s.healthChecker.cancel()
		}
		if s.catalog != nil {
			s.catalog.cancel()
		}
		if s.usageRetention != nil {
			s.usageRetention.cancel()
		}
		s.agentEngineMu.Lock()
		s.agentEngineClosed = true
		agentEngine := s.agentEngineInst
		s.agentEngineMu.Unlock()
		storageSafe := true
		if agentEngine != nil {
			if err := agentEngine.Shutdown(ctx); err != nil {
				s.logEvent("error").Err(err).Msg("agent shutdown error")
				s.shutdownErr = errors.Join(s.shutdownErr, err)
				storageSafe = false
			}
		}
		// Sessions and jobs own long-lived connections and stop before HTTP drains.
		s.gatewaySessions.stop()
		s.gatewayJobs.stop()
		if s.engine != nil && s.engine.IsRunning() {
			httpCtx, stopHTTP := context.WithTimeout(ctx, 5*time.Second)
			if err := s.engine.Shutdown(httpCtx); err != nil || httpCtx.Err() != nil {
				s.logEvent("warn").Err(err).Msg("HTTP graceful shutdown timed out; canceling connections")
				_ = s.engine.Close()
			}
			stopHTTP()
		}
		// Also covers an engine that returned after an external listener close.
		s.httpRequests.interrupt()
		requestsDone := make(chan struct{})
		go func() { s.httpRequests.wait(); s.sourceRefreshJobs().wait(); close(requestsDone) }()
		select {
		case <-requestsDone:
		case <-ctx.Done():
			s.logEvent("error").Err(ctx.Err()).Msg("request shutdown error")
			s.shutdownErr = errors.Join(s.shutdownErr, ctx.Err())
			storageSafe = false
		}
		if s.healthChecker != nil {
			s.healthChecker.shutdown()
		}
		if s.catalog != nil {
			s.catalog.shutdown()
		}
		if s.usageRetention != nil {
			s.usageRetention.shutdown()
		}
		if s.store != nil {
			s.store.LogStore.StopBackground()
		}
		// Final persistence has its own budget after request producers stop.
		flushCtx, stopFlush := context.WithTimeout(context.Background(), 15*time.Second)
		defer stopFlush()
		cancelWrites := context.AfterFunc(flushCtx, s.usageWriteCancel)
		defer cancelWrites()
		s.stopUsageWriter()
		s.shutdownErr = errors.Join(s.shutdownErr, s.usagePersistenceError())
		if storageSafe {
			s.logSystemEvent("info", "server stopped; flushing logs", nil)
		}
		if s.logger != nil {
			s.shutdownErr = errors.Join(s.shutdownErr, s.logger.Close(flushCtx))
		}
		if s.store != nil && storageSafe {
			if err := s.store.Close(); err != nil {
				logging.Emergency("store close error", err)
				s.shutdownErr = errors.Join(s.shutdownErr, err)
			}
		} else if s.store != nil {
			logging.Emergency("shutdown deadline reached; active work still owns storage", s.shutdownErr)
		}
	})
}

func (s *Server) closeCaches() {
	s.affinity.close()
	s.usageCache.close()
}
