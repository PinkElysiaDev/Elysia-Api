package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/elysia-api/backend/logging"
	"github.com/gin-gonic/gin"
)

func (s *Server) ListenAndServe() (result error) {
	s.initLifecycle()
	defer func() { s.doShutdown(); result = errors.Join(result, s.shutdownErr) }()
	if s.startupErr != nil {
		return s.startupErr
	}
	s.setupRoutes()
	s.startUsageWriter()
	if err := s.startGatewayJobs(); err != nil {
		return err
	}
	s.healthChecker = newHealthChecker(s)
	s.healthChecker.start()
	s.usageRetention = newUsageRetention(s)
	s.usageRetention.start()

	listen := s.config.GetServer()
	addr := fmt.Sprintf("%s:%d", listen.Host, listen.Port)
	s.logEvent("info").Str("address", addr).Msg("starting server")
	s.httpServer = &http.Server{
		Addr: addr, Handler: http.HandlerFunc(s.serveHTTP),
		ErrorLog:    s.Logger().Standard("http_server"),
		BaseContext: func(net.Listener) context.Context { return s.lifecycleCtx },
	}

	// Bind before launching the console; startup errors still drain owned work.
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	launchConsoleBrowser(s.config.OpenBrowserOnStart, listen.Host, listen.Port)

	// main owns OS signals; all shutdown sources share RequestShutdown.
	serveDone := make(chan struct{})
	defer close(serveDone)
	go func() {
		select {
		case <-s.shutdownRequested:
			s.logEvent("info").Msg("shutdown requested; draining")
		case <-serveDone:
			return
		}
		s.doShutdown()
	}()

	err = s.httpServer.Serve(listener)
	if err == http.ErrServerClosed {
		<-s.shutdownDone
		return nil
	}
	return err
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

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	s.requestMu.Lock()
	if s.stopping {
		s.requestMu.Unlock()
		http.Error(w, "server is shutting down", http.StatusServiceUnavailable)
		return
	}
	s.requestWG.Add(1)
	s.requestMu.Unlock()
	defer s.requestWG.Done()
	s.engine.ServeHTTP(w, r)
}

func (s *Server) shutdown(c *gin.Context) {
	s.logSystemEvent("info", "server shutdown requested", nil)
	c.JSON(http.StatusOK, gin.H{"shuttingDown": true})
	s.RequestShutdown()
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
		s.requestMu.Lock()
		s.stopping = true
		s.requestMu.Unlock()
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
		if s.httpServer != nil {
			httpCtx, stopHTTP := context.WithTimeout(ctx, 5*time.Second)
			if err := s.httpServer.Shutdown(httpCtx); err != nil {
				s.logEvent("warn").Err(err).Msg("HTTP graceful shutdown timed out; canceling connections")
				_ = s.httpServer.Close()
			}
			stopHTTP()
		}
		// Wait for any task that passed cancellation to register its ownership.
		s.sourceRefreshMu.Lock()
		//lint:ignore SA2001 Synchronize task registration before Wait, not shared data access.
		s.sourceRefreshMu.Unlock()
		requestsDone := make(chan struct{})
		go func() { s.requestWG.Wait(); s.sourceRefreshWG.Wait(); close(requestsDone) }()
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
