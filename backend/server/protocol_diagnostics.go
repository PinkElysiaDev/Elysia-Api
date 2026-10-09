package server

import (
	"fmt"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
)

// This diagnostic contains structure only: never prompt text, attribute values,
// resource identities or cache keys. Keep it bounded even for oversized histories.
type systemNodeStructure struct {
	Path       string            `json:"path"`
	Kind       protocol.NodeKind `json:"kind"`
	Metadata   []string          `json:"metadata,omitempty"`
	CacheCount int               `json:"cacheCount"`
}
type systemStructureDiagnostic struct {
	Before []systemNodeStructure `json:"before"`
	After  []systemNodeStructure `json:"after"`
}

func systemStructure(request *protocol.Request) []systemNodeStructure {
	result := []systemNodeStructure{}
	var visit func(protocol.Node, string)
	visit = func(node protocol.Node, path string) {
		if len(result) >= 64 {
			return
		}
		item := systemNodeStructure{Path: path, Kind: node.Kind, CacheCount: len(node.Cache)}
		for _, field := range []struct {
			name    string
			present bool
		}{
			{"id", !node.ID.IsZero()}, {"status", !node.Status.IsZero()},
			{"attributes", len(node.Attributes) > 0}, {"cache", len(node.Cache) > 0}, {"resources", len(node.Resources) > 0},
		} {
			if field.present {
				item.Metadata = append(item.Metadata, field.name)
			}
		}
		result = append(result, item)
		for index, child := range node.Children {
			visit(child, fmt.Sprintf("%s/children/%d", path, index))
		}
	}
	for index, node := range request.Content {
		if node.Role == protocol.StringValue("system") || node.Role == protocol.StringValue("developer") {
			visit(node, fmt.Sprintf("/content/%d", index))
		}
	}
	return result
}

func prepareCandidateRecord(record *usageRecord, candidate gatewayCandidate, request *protocol.Request) {
	setRecordModel(record, candidate.model, builtin.Platform("custom:"+candidate.binding.ProtocolID))
	record.UpstreamRevision, record.TargetEndpoint, record.TargetFormat = candidate.compiled.Hash(), candidate.operation.Path, candidate.binding.ProtocolID
	record.ConversionPolicyHash = protocol.ConversionHash(candidate.conversion)
	record.CacheSynthesis = candidate.model.CacheSynthesis
	record.SystemStructure = &systemStructureDiagnostic{Before: systemStructure(request)}
}
