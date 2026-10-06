package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/elysia-api/backend/protocol"
)

type protocolV2Tool struct {
	server *Server
	action string
}

func (tool *protocolV2Tool) CLIEffect() string {
	switch tool.action {
	case "save", "activate", "rollback":
		return CLIEffectWrite
	case "test", "models":
		return CLIEffectOutbound
	default:
		return CLIEffectRead
	}
}
func (*protocolV2Tool) Description() string {
	return "受服务端权限与业务策略控制。Use the running protocol engine's schema, validation, preview and immutable revision service. Unsupported capabilities produce diagnostics; saving a draft never activates it."
}

type protocolCommandInput struct {
	Config        json.RawMessage    `json:"config,omitempty"`
	ID            string             `json:"id"`
	Hash          string             `json:"hash"`
	Expected      string             `json:"expected"`
	From          string             `json:"from"`
	To            string             `json:"to"`
	Section       string             `json:"section"`
	Type          string             `json:"type"`
	Direction     protocol.Direction `json:"direction"`
	SampleRequest protocol.Value     `json:"sampleRequest,omitzero"`
	Sequence      bool               `json:"sequence"`
	Mode          string             `json:"mode"`
	Sample        string             `json:"sample"`
	Operation     string             `json:"operation"`
	Kind          string             `json:"kind"`
	Purpose       string             `json:"purpose"`
	BaseURL       string             `json:"baseUrl"`
	APIKey        string             `json:"apiKey"`
}

func protocolCLIError(err error) CLIResult {
	var conversion *protocol.ConversionError
	if errors.As(err, &conversion) {
		return CLIResult{Summary: err.Error(), Data: map[string]any{"issues": conversion.Issues}}
	}
	return CLIError(err.Error(), "protocol_service_error")
}

func isVersionedDefinition(raw []byte) bool {
	var identity struct {
		SchemaVersion json.RawMessage `json:"schemaVersion"`
	}
	return json.Unmarshal(raw, &identity) == nil && len(identity.SchemaVersion) > 0
}

func (tool *protocolV2Tool) Execute(ctx context.Context, tctx CLIContext, raw json.RawMessage) CLIResult {
	var input protocolCommandInput
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &input); err != nil {
			return protocolCLIError(err)
		}
	}
	service, err := tool.server.protocolService()
	if err != nil {
		return protocolCLIError(err)
	}
	switch tool.action {
	case "schema":
		return protocolSchemaResult(service.Schema(), input.Section, input.Type)
	case "read":
		if input.ID == "" {
			return CLIError("protocol ID is required", "missing_id")
		}
		if input.Hash != "" {
			revision, err := service.ReadRevision(ctx, input.ID, input.Hash)
			if err != nil {
				return protocolCLIError(err)
			}
			return CLIResult{OK: true, Summary: "Immutable protocol revision", Data: revision}
		}
		draft, err := service.ReadDraft(ctx, input.ID)
		if err != nil {
			return protocolCLIError(err)
		}
		return CLIResult{OK: true, Summary: "Protocol draft; use its hash for a conditional save", Data: draft}
	case "diff":
		changes, err := service.Diff(ctx, input.ID, input.From, input.To)
		if err != nil {
			return protocolCLIError(err)
		}
		return CLIResult{OK: true, Summary: "Revision comparison", Data: changes}
	case "activate", "rollback":
		if input.ID == "" || input.Hash == "" {
			return CLIError("ID and revision hash are required", "missing_revision")
		}
		if edit := tctx.EditProtocolID(); edit != "" && edit != input.ID {
			return CLIError("Edit mode must preserve the protocol ID", "id_mismatch")
		}
		activation, err := service.Activate(ctx, input.ID, input.Hash, input.Expected)
		if err != nil {
			return protocolCLIError(err)
		}
		return CLIResult{OK: true, Summary: "Verified revision activated", Data: activation}
	}
	definition := tctx.Draft()
	if len(input.Config) > 0 {
		definition = input.Config
	}
	if input.ID != "" && tool.action != "save" {
		if input.Hash != "" {
			revision, err := service.ReadRevision(ctx, input.ID, input.Hash)
			if err != nil {
				return protocolCLIError(err)
			}
			definition = revision.Definition.Bytes()
		} else {
			draft, err := service.ReadDraft(ctx, input.ID)
			if err != nil {
				return protocolCLIError(err)
			}
			definition = draft.Definition.Bytes()
		}
	}
	if len(definition) == 0 {
		return CLIError("Read schema and author a complete v2 draft first", "no_draft")
	}
	value, err := protocol.ParseValue(definition)
	if err != nil {
		return protocolCLIError(err)
	}
	object, err := value.ReadObject()
	if err != nil {
		return protocolCLIError(err)
	}
	var id string
	if err := object["id"].Decode(&id); err != nil {
		return protocolCLIError(err)
	}
	if edit := tctx.EditProtocolID(); edit != "" && edit != id {
		return CLIError("Edit mode must preserve the protocol ID", "id_mismatch")
	}
	if protocol.IsPresetProtocolID(id) {
		return CLIError("预置协议只读且随引擎自动更新：请用「复制为新协议」派生副本后编辑（如 "+id+"-copy）", "preset_readonly")
	}
	compiled, issues := service.Validate(definition)
	switch tool.action {
	case "draft":
		if err := tctx.SetDraft(definition); err != nil {
			return protocolCLIError(err)
		}
		return CLIResult{OK: compiled != nil, Summary: "Draft retained; inspect diagnostics before verification", Data: map[string]any{"valid": compiled != nil, "issues": issues, "activated": false}}
	case "validate":
		result := map[string]any{"valid": compiled != nil, "issues": issues}
		if compiled != nil {
			result["hash"] = compiled.Hash()
		}
		return CLIResult{OK: compiled != nil, Summary: "Definition validation", Data: result}
	case "preview":
		mode := input.Mode
		if mode == "" {
			mode = "mapping"
		}
		preview := protocol.PreviewInput{Definition: value, Direction: input.Direction, Input: input.SampleRequest, Sequence: input.Sequence, Mode: mode, Sample: input.Sample, Operation: input.Operation, Kind: input.Kind, Purpose: input.Purpose}
		if mode == "mapping" && preview.Direction == "" {
			return CLIError("Specify an independent direction with --direction", "missing_direction")
		}
		result := service.PreviewWorkflow(ctx, preview)
		return CLIResult{OK: protocol.IssuesError(result.Issues) == nil, Summary: "Shared runtime preview", Data: result}
	case "save":
		if input.ID != "" && input.ID != id {
			return CLIError("Saved target differs from draft ID", "id_mismatch")
		}
		draft, issues, err := service.SaveDraft(ctx, id, definition, input.Expected)
		if err != nil {
			return protocolCLIError(err)
		}
		result := map[string]any{"draft": draft, "issues": issues, "activated": false}
		// Persist evidence alongside this exact revision for later activation.
		// Failed verification still retains the repairable authoring draft.
		if compiled != nil {
			revision, report, err := service.VerifyDraft(ctx, id, draft.Hash)
			if err != nil {
				return protocolCLIError(err)
			}
			result["revision"], result["report"] = revision, report
		}
		return CLIResult{OK: true, Summary: "Draft saved; activation remains a separate verified operation", Data: result}
	case "verify", "diagnose":
		if compiled == nil {
			return CLIResult{Summary: "Definition cannot compile", Data: map[string]any{"issues": issues}}
		}
		report := protocol.Verify(ctx, compiled)
		return CLIResult{OK: report.Passed, Summary: "Offline fidelity report; no provider request was sent", Data: report}
	case "test", "models":
		if compiled == nil {
			return CLIResult{Summary: "Definition cannot compile", Data: map[string]any{"issues": issues}}
		}
		var request protocol.Request
		if tool.action == "models" {
			name, _, err := selectModelDiscovery(compiled)
			if err != nil {
				return protocolCLIError(err)
			}
			input.Operation = name
		} else {
			if err := input.SampleRequest.Decode(&request); err != nil {
				return protocolCLIError(err)
			}
		}
		baseURL, apiKey, failure, ok := resolveTestTarget(tctx, input.BaseURL, input.APIKey)
		if !ok {
			return failure
		}
		result, err := tool.server.probeProtocol(ctx, protocolProbeInput{Definition: value, Request: request, Operation: input.Operation, BaseURL: baseURL, APIKey: apiKey})
		if err != nil {
			return protocolCLIError(err)
		}
		return CLIResult{OK: result.Report.Passed, Summary: "Target contract probe; separate from offline fidelity verification", Data: result, SecretValues: []string{apiKey}}
	default:
		return CLIError("Unsupported protocol command", "unsupported_command")
	}
}

func protocolSchemaResult(schema protocol.SchemaCatalog, section, typeName string) CLIResult {
	sections := map[string]any{"definition": schema.Definition, "semantic": schema.Semantic, "binding": schema.Binding, "directions": schema.Directions, "capabilities": schema.Capabilities, "operations": schema.Operations, "events": schema.Events, "diagnostics": schema.Diagnostics, "modules": schema.Modules}
	if section == "" {
		return CLIResult{OK: true, Summary: "Installed engine contract; request --section and optionally --type for full definitions", Data: map[string]any{"schemaVersion": schema.SchemaVersion, "compilerVersion": schema.CompilerVersion, "features": schema.EngineFeatures, "directions": schema.Directions, "capabilities": schema.Capabilities, "transports": schema.Transports, "operationKinds": schema.OperationKinds, "sections": []string{"definition", "semantic", "binding", "directions", "capabilities", "operations", "events", "diagnostics", "modules"}}}
	}
	value, exists := sections[section]
	if !exists {
		return CLIError("Unknown schema section", "invalid_section")
	}
	if typeName != "" {
		root, ok := value.(map[string]any)
		if !ok {
			return CLIError("Section has no named types", "invalid_type")
		}
		definitions, ok := root["$defs"].(map[string]any)
		if !ok {
			return CLIError("Section has no named types", "invalid_type")
		}
		value, exists = definitions[typeName]
		if !exists {
			return CLIError("Unknown schema type", "invalid_type")
		}
	}
	return CLIResult{OK: true, Summary: "Current engine schema: " + section, Data: value}
}

func protocolV2Commands() []*cliCommand {
	var commands []*cliCommand
	for _, action := range []string{"schema", "validate", "verify", "diagnose", "diff", "activate", "rollback"} {
		command := &cliCommand{group: "protocol", name: action, summary: "Protocol v2 " + action, usage: "elysia protocol " + action + " [--id <id>] [--hash <revision>] [--expected <active>] [--section <section>] [--type <type>] [--from <hash>] [--to <hash>]", handler: func(s *Server) CLIHandler { return &protocolV2Tool{server: s, action: action} }}
		for _, name := range []string{"id", "hash", "expected", "section", "type", "from", "to"} {
			command.flags = append(command.flags, cliFlagSpec{name: name, usage: protocolFlagUsage[name]})
		}
		command.mapper = func(inv *cliInvocation) (map[string]any, error) {
			params := map[string]any{}
			for _, name := range []string{"id", "hash", "expected", "section", "type", "from", "to"} {
				inv.setStr(params, name, name)
			}
			return params, nil
		}
		commands = append(commands, command)
	}
	return commands
}

func protocolV2DraftDetail() string {
	return strings.Join([]string{
		"接入工作流：读取提供的协议文档与完整样例；运行 elysia protocol schema 读取当前引擎能力，按 --section/--type 获取定义、语义、映射操作和约束。",
		"编写 schemaVersion=2 的完整定义：独立声明方向、传输、能力、映射和预期样例。elysia protocol draft '<JSON>' 保留草稿并检查定义。",
		"模型目录通过 models 操作与 modelSamples 声明。作为 Agent 模型时，配置 agent 工具结果输入类型、参数映射和各思考模式的样例；使用 preview --mode agent 检查实际渲染。",
		"真实测试受服务端权限与业务策略控制。示例：elysia protocol test --operation generate --sample '<完整语义请求 JSON>' --base-url <URL> --api-key <key>。操作名称必须来自定义，流式由操作的传输声明决定。",
		"运行 validate、preview、verify；按结构化诊断修订。不能靠删工具、删字段、降低能力声明或缩减必要测试掩盖用户需要的能力。无法表达的新机制必须报告不支持。",
		"save 保存草稿与当前验证报告；已有草稿用 --expected <draft-hash> 防止覆盖并发编辑。保存不启用。验证通过后 activate --id <id> --hash <revision-hash> --expected <active-hash> 启用。",
		"read --id <id> 读取当前草稿与哈希；diff --id <id> --from <hash> --to <hash> 比较修订；rollback 使用与 activate 相同的验证门槛。",
		"离线验证与真实上游验证独立；HTTP 200 不能证明语义保真。协议映射不能改变网关授权规则，也不能托管客户端业务工具。",
	}, "\n")
}
