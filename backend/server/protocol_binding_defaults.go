package server

import (
	"fmt"
	"sort"
	"strings"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func protocolIDForPlatform(platform string) (string, error) {
	if strings.HasPrefix(platform, "custom:") && len(platform) > len("custom:") {
		return strings.TrimPrefix(platform, "custom:"), nil
	}
	return "", fmt.Errorf("platform %q requires an explicit verified protocol binding", platform)
}

func makeProtocolBinding(key storage.BindingKey, compiled *protocol.Compiled) storage.ProtocolBinding {
	capabilities := compiled.Definition().Capabilities
	transports := []protocol.Transport{}
	hasTransport := map[protocol.Transport]bool{}
	for _, operation := range compiled.Operations() {
		if operation.Kind != "generate" || hasTransport[operation.Transport] {
			continue
		}
		hasTransport[operation.Transport] = true
		transports = append(transports, operation.Transport)
	}
	sort.Slice(transports, func(i, j int) bool { return transports[i] < transports[j] })
	return storage.ProtocolBinding{Kind: key.Kind, SourceID: key.SourceID, ModelID: key.ModelID, GroupID: key.GroupID, Binding: protocol.Binding{ProtocolID: compiled.Identity().DefinitionID, RevisionHash: compiled.Hash(), Capabilities: capabilities, Transports: transports}}
}
