package server

import (
	"unicode/utf8"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
)

const estimatedFileBlockBytes = 1024

func (s *Server) estimateProtocolTokens(request *protocol.Request) int {
	output := s.config.GetUsageConfig().DefaultOutputTokenEstimate
	var budget int
	if value := request.Parameters["max_output_tokens"]; !value.IsZero() && value.Decode(&budget) == nil && budget > 0 {
		output = budget
	}
	return s.estimateProtocolInputTokens(request) + output
}

func (s *Server) estimateProtocolInputTokens(request *protocol.Request) int {
	settings := s.config.GetUsageConfig()
	textChars, mediaTokens := 0, 0
	var visit func([]protocol.Node)
	visit = func(nodes []protocol.Node) {
		for _, node := range nodes {
			switch node.Kind {
			case protocol.ImageNode, protocol.AudioNode, protocol.VideoNode:
				mediaTokens += settings.ImageInputTokenEstimate
			case protocol.DocumentNode:
				fileKB := 1
				if payload, err := node.Payload.ReadObject(); err == nil {
					var content string
					if value := payload["data"]; !value.IsZero() && value.Decode(&content) == nil {
						fileKB = max(1, (len(content)+estimatedFileBlockBytes-1)/estimatedFileBlockBytes)
					}
				}
				mediaTokens += fileKB * settings.FileInputTokenEstimatePerKB
			default:
				// Structured tool-result children are the same content as Payload.
				if len(node.Children) == 0 {
					textChars += estimateValueChars(node.Payload)
				}
			}
			textChars += estimateValueChars(node.Name)
			if node.Input != nil {
				textChars += estimateValueChars(node.Input.Value)
			}
			visit(node.Children)
		}
	}
	visit(request.Content)
	for _, tool := range request.Tools {
		textChars += estimateValueChars(tool.Name) + estimateValueChars(tool.Description) + estimateValueChars(tool.InputSchema)
	}
	return (textChars+settings.CharsPerToken-1)/settings.CharsPerToken + mediaTokens
}

func estimateValueChars(value protocol.Value) int {
	if value.IsZero() || value.IsNull() {
		return 0
	}
	var text string
	if value.Decode(&text) == nil {
		return utf8.RuneCountInString(text)
	}
	return utf8.RuneCount(value.Bytes())
}

// appendConversionIssues merges request diagnostics into the record, keeping
// one entry per distinct code/path so a stream's repeated renders stay quiet.
func (record *usageRecord) appendConversionIssues(issues []protocol.ConversionIssue) {
	for _, issue := range issues {
		exists := false
		for _, existing := range record.ConversionIssues {
			if existing.Code == issue.Code && existing.Path == issue.Path {
				exists = true
				break
			}
		}
		if !exists {
			record.ConversionIssues = append(record.ConversionIssues, issue)
		}
	}
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
	record.Usage.CacheCreationTokens = count(usage.CacheCreation)
	record.Usage.Estimated, record.UsageSource = false, "protocol_observed"
	record.UsageDetail.InputTokens, record.UsageDetail.OutputTokens, record.UsageDetail.TotalTokens = count(usage.Input), count(usage.Output), count(usage.Total)
	record.UsageDetail.CachedInputTokens, record.UsageDetail.CacheCreationInputTokens = count(usage.CacheRead), count(usage.CacheCreation)
	for _, entry := range []struct {
		name   string
		target **int
	}{
		{"output.reasoning_tokens", &record.UsageDetail.ReasoningTokens},
		{"input.text_tokens", &record.UsageDetail.TextInputTokens},
		{"output.text_tokens", &record.UsageDetail.TextOutputTokens},
		{"input.image_tokens", &record.UsageDetail.ImageInputTokens},
		{"output.image_tokens", &record.UsageDetail.ImageOutputTokens},
		{"input.audio_tokens", &record.UsageDetail.AudioInputTokens},
		{"output.audio_tokens", &record.UsageDetail.AudioOutputTokens},
		{"toolUsePromptTokenCount", &record.UsageDetail.ToolUseTokens},
	} {
		if detail, exists := usage.Details[entry.name]; exists {
			*entry.target = count(&detail)
		}
	}
	for name, target := range map[string]*int{
		"tools.web_search_calls":       &record.BuiltinToolUsage.WebSearchCalls,
		"tools.file_search_calls":      &record.BuiltinToolUsage.FileSearchCalls,
		"tools.image_generation_calls": &record.BuiltinToolUsage.ImageGenerationCalls,
		"tools.code_interpreter_calls": &record.BuiltinToolUsage.CodeInterpreterCalls,
		"tools.computer_use_calls":     &record.BuiltinToolUsage.ComputerUseCalls,
	} {
		if counter, exists := usage.Details[name]; exists {
			*target = int(counter.Count)
		}
	}
	// A total derived from observed input/output remains available to the UI;
	// ProtocolUsage retains its inferred origin independently of token estimates.
}

func observeHostedTools(record *usageRecord, compiled *protocol.Compiled, body []byte) error {
	if record.hostedTools == nil {
		record.hostedTools = builtin.NewToolAccounting(compiled.ResourceLimits().StateItems)
	}
	if err := record.hostedTools.Observe(compiled.Identity(), body); err != nil {
		return err
	}
	for kind, target := range map[string]*int{
		"web_search": &record.BuiltinToolUsage.WebSearchCalls, "file_search": &record.BuiltinToolUsage.FileSearchCalls,
		"image_generation": &record.BuiltinToolUsage.ImageGenerationCalls, "code_interpreter": &record.BuiltinToolUsage.CodeInterpreterCalls,
		"computer_use": &record.BuiltinToolUsage.ComputerUseCalls,
	} {
		*target = record.hostedTools.Count(kind)
	}
	return nil
}
