package server

import "github.com/elysia-api/backend/protocol"

func (s *Server) estimateProtocolTokens(request *protocol.Request) int {
	settings := s.config.GetUsageConfig()
	textBytes, mediaTokens := 0, 0
	var visit func([]protocol.Node)
	visit = func(nodes []protocol.Node) {
		for _, node := range nodes {
			switch node.Kind {
			case protocol.ImageNode, protocol.AudioNode, protocol.VideoNode:
				mediaTokens += settings.ImageInputTokenEstimate
			case protocol.DocumentNode:
				mediaTokens += settings.FileInputTokenEstimatePerKB
			default:
				textBytes += len(node.Payload.Bytes())
			}
			if node.Input != nil {
				textBytes += len(node.Input.Value.Bytes())
			}
			visit(node.Children)
		}
	}
	visit(request.Content)
	for _, tool := range request.Tools {
		textBytes += len(tool.Name.Bytes()) + len(tool.Description.Bytes()) + len(tool.InputSchema.Bytes())
	}
	return textBytes/settings.CharsPerToken + mediaTokens + settings.DefaultOutputTokenEstimate
}

func updateRecordProtocolUsage(record *usageRecord, usage *protocol.Usage) {
	if usage == nil {
		return
	}
	record.ProtocolUsage = protocol.MergeUsage(record.ProtocolUsage, usage)
	usage = record.ProtocolUsage
	count := func(counter *protocol.Counter) *int {
		if counter == nil {
			return nil
		}
		value := int(counter.Count)
		return &value
	}
	record.Usage.InputTokens, record.Usage.OutputTokens, record.Usage.TotalTokens, record.Usage.CacheHitTokens = count(usage.Input), count(usage.Output), count(usage.Total), count(usage.CacheRead)
	record.Usage.Estimated, record.UsageSource = false, "protocol_observed"
	record.UsageDetail.InputTokens, record.UsageDetail.OutputTokens, record.UsageDetail.TotalTokens = count(usage.Input), count(usage.Output), count(usage.Total)
	record.UsageDetail.CachedInputTokens, record.UsageDetail.CacheCreationInputTokens = count(usage.CacheRead), count(usage.CacheCreation)
	if usage.Total != nil && usage.Total.Origin == protocol.InferredCount {
		record.Usage.TotalTokens = nil
	}
}
