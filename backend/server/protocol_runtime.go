package server

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

// initializeProtocolRuntime publishes only verified current definitions.
// A failed initialization leaves the management API available for repairs.
func (s *Server) initializeProtocolRuntime(ctx context.Context) (err error) {
	s.protocolStartupMu.Lock()
	defer s.protocolStartupMu.Unlock()
	defer func() { s.recordProtocolRuntimeFailure(err) }()
	started := time.Now()
	s.isProtocolRuntimeRequired.Store(true)
	s.protocolRuntimeReady.Store(false)
	service, err := s.loadProtocolService(false)
	if err != nil {
		return &protocolStartupError{Stage: "preset_recovery", Err: err}
	}
	compilation := service.NewCompilationSession()
	defer compilation.Close()
	if err := s.refreshProtocolCompilation(ctx, compilation, false); err != nil {
		// Presets stay committed and published for repair even when custom
		// binding refresh fails; this pass runs only on that failure path.
		if ctx.Err() == nil {
			_ = s.refreshAndPublishProtocols(ctx, service, true)
		}
		return &protocolStartupError{Stage: "preset_recovery", Err: err}
	}
	if err := compilation.ReloadAvailable(ctx, requiredPresetIDs...); err != nil {
		return &protocolStartupError{Stage: "runtime_verification", Err: err}
	}
	s.protocolRuntimeReady.Store(true)
	s.logProtocolRuntimeStage("runtime_verification", started)
	// Compilation's temporary graphs can grow the heap far beyond live runtime
	// state. Scavenge once at startup, never on management reloads or requests.
	debug.FreeOSMemory()
	return nil
}

func (s *Server) logProtocolRuntimeStage(stage string, started time.Time) {
	view := s.protocolServiceInst.View()
	s.logEvent("info").Str("stage", stage).Str("compiler", protocol.CompilerVersion).
		Int("protocol_count", len(view.IDs())).Int("unavailable_count", len(view.Failures())).
		Int64("duration_ms", time.Since(started).Milliseconds()).Msg("protocol initialization stage completed")
}

func checkRequiredPresets(view protocol.RegistryView) error {
	for _, id := range requiredPresetIDs {
		if _, ok := view.Pin(id); !ok {
			return fmt.Errorf("preset %s runtime publication failed", id)
		}
	}
	return nil
}

func (s *Server) reloadProtocolRuntime(ctx context.Context) (err error) {
	defer func() { s.recordProtocolRuntimeFailure(err) }()
	s.protocolRuntimeReady.Store(false)
	service, err := s.protocolService()
	if err != nil {
		return err
	}
	if err := s.refreshAndPublishProtocols(ctx, service, false); err != nil {
		return err
	}
	if err := checkRequiredPresets(service.View()); err != nil {
		return err
	}
	if s.isProtocolRuntimeRequired.Load() {
		s.protocolRuntimeReady.Store(true)
	}
	return nil
}

func (s *Server) recordProtocolRuntimeFailure(err error) {
	if err == nil {
		s.protocolRuntimeFailure.Store(nil)
		s.protocolStartupFailure.Store(nil)
		return
	}
	message := err.Error()
	s.protocolRuntimeFailure.Store(&message)
	failure := &protocolStartupFailure{Stage: "runtime_publication", Code: "protocol_runtime_error", Message: message}
	var stage *protocolStartupError
	if errors.As(err, &stage) {
		failure.Stage = stage.Stage
	}
	var databaseError interface {
		error
		Code() int
	}
	if errors.As(err, &databaseError) {
		failure.Code = "storage_error"
		if databaseError.Code()&0xff == 1 {
			failure.Code = "storage_schema_error"
		}
	}
	var snapshot *storage.ProtocolSnapshotError
	if errors.As(err, &snapshot) {
		failure.Stage = "snapshot_" + snapshot.Stage
		failure.Code = "backup_" + snapshot.Stage + "_failed"
	}
	if errors.Is(err, protocol.ErrRevisionConflict) {
		failure.Code = "revision_conflict"
	}
	var conversion *protocol.ConversionError
	if errors.As(err, &conversion) {
		failure.Code = "protocol_verification_failed"
	}
	s.protocolStartupFailure.Store(failure)
}

type protocolStartupFailure struct {
	Stage   string `json:"stage"`
	Code    string `json:"code"`
	Message string `json:"message"`
}
type protocolStartupError struct {
	Stage string
	Err   error
}

func (e *protocolStartupError) Error() string {
	return fmt.Sprintf("protocol startup %s: %v", e.Stage, e.Err)
}
func (e *protocolStartupError) Unwrap() error { return e.Err }

func (s *Server) protocolRuntimeError() error {
	if s.isProtocolRuntimeRequired.Load() && !s.protocolRuntimeReady.Load() {
		return gatewayIssue(protocol.Identity{}, protocol.VerificationRequired, "/runtime", "protocol runtime verification requires repair; verify definitions and reload the registry")
	}
	return nil
}
