package server

import (
	"context"
	"sync"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

// Snapshots are pinned for the whole request. Only compiled programs are shared;
// provider evidence, diagnostics, delivery and continuation state remain local.
type gatewayConversionPlans struct {
	mu      sync.Mutex
	current *gatewayConversionSnapshot
}

type gatewayConversionSnapshot struct {
	generation int64
	policies   []storage.ConversionPolicyRecord
	bindings   []storage.ProtocolBinding
	mu         sync.Mutex
	plans      map[gatewayConversionKey]*protocol.CompiledConversion
}

type gatewayConversionKey struct {
	sourceHash, targetHash              string
	sourceID, modelID, model, operation string
	transport                           protocol.Transport
}

func (s *Server) pinGatewayConversions(ctx context.Context) (*gatewayConversionSnapshot, error) {
	cache := &s.conversionPlans
	cache.mu.Lock()
	defer cache.mu.Unlock()
	generation, err := s.store.ConversionGeneration(ctx)
	if err != nil {
		return nil, err
	}
	if cache.current != nil && cache.current.generation == generation {
		return cache.current, nil
	}
	policies, bindings, generation, err := s.store.ConversionSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	snapshot := &gatewayConversionSnapshot{generation: generation, policies: policies, bindings: bindings, plans: make(map[gatewayConversionKey]*protocol.CompiledConversion)}
	cache.current = snapshot
	return snapshot, nil
}

func (snapshot *gatewayConversionSnapshot) resolve(ingress, upstream *protocol.Compiled, model config.ModelRef, operation string, transport protocol.Transport) (*protocol.CompiledConversion, error) {
	key := gatewayConversionKey{ingress.Hash(), upstream.Hash(), model.SourceID, model.ID, model.Identifier(), operation, transport}
	snapshot.mu.Lock()
	defer snapshot.mu.Unlock()
	compiled := snapshot.plans[key]
	if compiled == nil {
		var err error
		compiled, err = resolveGatewayConversion(snapshot.policies, snapshot.bindings, ingress, upstream, model, operation, transport)
		if err != nil {
			return nil, err
		}
		// Bound revisions/model identifiers retained within a generation, including
		// protocol reloads that do not change the conversion generation.
		if len(snapshot.plans) >= 256 {
			clear(snapshot.plans)
		}
		snapshot.plans[key] = compiled
	}
	return compiled.ForExecution(), nil
}
