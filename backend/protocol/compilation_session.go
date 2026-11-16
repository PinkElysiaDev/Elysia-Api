package protocol

import (
	"context"
	"fmt"
	"sync"
)

// CompilationSession reuses admitted preset compilations for one preparation
// and publication attempt. It retains neither verification reports nor runtime
// state. Call Close when preparation fails; ReloadAvailable closes it itself.
type CompilationSession struct {
	mu       sync.Mutex
	service  *Service
	presets  map[string]sessionCompilation
	isClosed bool
}

type sessionCompilation struct {
	source   Value
	compiled *Compiled
}

// NewCompilationSession pins this service's compiler until one publication.
func (service *Service) NewCompilationSession() *CompilationSession {
	return &CompilationSession{service: service, presets: make(map[string]sessionCompilation)}
}

// Compile validates exact source bytes. Only prior admitted preset sources can
// hit the session; other successful compilations are not retained.
func (session *CompilationSession) Compile(value Value) (*Compiled, []ConversionIssue) {
	return session.compile(value, "")
}

// CompilePreset admits the first successful source for a recognized preset ID.
// The caller selects embedded sources; identity matching is not verification.
func (session *CompilationSession) CompilePreset(id string, value Value) (*Compiled, []ConversionIssue) {
	return session.compile(value, id)
}

func (session *CompilationSession) compile(value Value, presetID string) (*Compiled, []ConversionIssue) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.isClosed {
		return nil, []ConversionIssue{{Code: InvalidInput, Severity: SeverityError, Path: "/compilation", Reason: "compilation session is closed"}}
	}
	for _, admitted := range session.presets {
		if admitted.source == value {
			return admitted.compiled, nil
		}
	}
	compiled, issues := session.service.Validate(value.Bytes())
	if compiled == nil {
		return nil, issues
	}
	_, hasAdmission := session.presets[presetID]
	if !hasAdmission && IsPresetProtocolID(presetID) && compiled.Identity().DefinitionID == presetID {
		session.presets[presetID] = sessionCompilation{source: value, compiled: compiled}
	}
	return compiled, nil
}

// ReloadAvailable rereads activations, source bytes and current verification
// evidence before atomic publication. The session ends even when loading fails.
func (session *CompilationSession) ReloadAvailable(ctx context.Context, requiredIDs ...string) error {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.isClosed {
		return fmt.Errorf("compilation session is closed")
	}
	defer session.close()
	return session.service.reload(ctx, true, session.presets, requiredIDs...)
}

// Close releases retained sources and graphs. Published graphs remain
// owned by the registry snapshot; closing twice has no additional effect.
func (session *CompilationSession) Close() {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.close()
}

func (session *CompilationSession) close() {
	session.presets = nil
	session.service = nil
	session.isClosed = true
}
