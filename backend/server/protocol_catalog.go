package server

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/elysia-api/backend/protocol"
)

// enabledProtocol describes the pinned executable revision, never an editable
// draft. Selectors use its declared operations instead of a vendor-name list.
type enabledProtocol struct {
	ID                string                 `json:"id"`
	Name              string                 `json:"name"`
	Revision          string                 `json:"revision"`
	Preset            bool                   `json:"preset"`
	Directions        []protocol.Direction   `json:"directions"`
	Capabilities      protocol.CapabilitySet `json:"capabilities"`
	CanGenerate       bool                   `json:"canGenerate"`
	HasModelDiscovery bool                   `json:"hasModelDiscovery"`
	HasAgentPolicy    bool                   `json:"hasAgentPolicy"`
}

func (s *Server) adminEnabledProtocols(ctx context.Context, c *app.RequestContext) {
	service, ok := s.requireProtocolService(ctx, c)
	if !ok {
		return
	}
	view := service.View()
	items := make([]enabledProtocol, 0, len(view.IDs()))
	for _, id := range view.IDs() {
		compiled, _ := view.Pin(id)
		definition := compiled.Definition()
		item := enabledProtocol{ID: id, Name: definition.Name, Revision: compiled.Hash(), Preset: protocol.IsPresetProtocolID(id), Capabilities: definition.Capabilities, HasAgentPolicy: definition.Agent != nil, Directions: []protocol.Direction{}}
		for _, direction := range protocol.DirectionCatalog() {
			if compiled.Supports(direction) {
				item.Directions = append(item.Directions, direction)
			}
		}
		for _, operation := range compiled.Operations() {
			item.CanGenerate = item.CanGenerate || ((operation.Kind == "generate" || operation.Kind == "submit" || operation.Kind == "session") && compiled.Supports(protocol.EncodeRequest))
			item.HasModelDiscovery = item.HasModelDiscovery || operation.Kind == "models"
		}
		items = append(items, item)
	}
	respondOK(ctx, c, utils.H{"items": items})
}
