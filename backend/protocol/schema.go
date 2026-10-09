package protocol

import (
	"reflect"
	"strings"
)

// SchemaCatalog is the shared machine-readable contract for editor, Agent and
// API clients. The JSON schema is generated from the actual strict Go model.
type SchemaCatalog struct {
	Conversion      map[string]any  `json:"conversion"`
	SchemaVersion   int             `json:"schemaVersion"`
	CompilerVersion string          `json:"compilerVersion"`
	Directions      []Direction     `json:"directions"`
	Transports      []Transport     `json:"transports"`
	OperationKinds  []string        `json:"operationKinds"`
	Capabilities    []Capability    `json:"capabilities"`
	Events          []EventType     `json:"events"`
	Diagnostics     []IssueCode     `json:"diagnostics"`
	EngineFeatures  []string        `json:"engineFeatures"`
	Modules         []ModuleSpec    `json:"modules"`
	Operations      []OperationInfo `json:"mappingOperations"`
	Definition      map[string]any  `json:"definitionSchema"`
	Semantic        map[string]any  `json:"semanticSchema"`
	Binding         map[string]any  `json:"bindingSchema"`
	Limits          Limits          `json:"limits"`
}

// ModuleSpec advertises implemented directions, not merely known vendor names.
type ModuleSpec struct {
	Name       string      `json:"name"`
	Directions []Direction `json:"directions"`
}

// Schema combines the language schema with this compiler's installed modules.
func (compiler *Compiler) Schema() SchemaCatalog {
	catalog := DescribeSchema()
	catalog.Limits = compiler.limits
	catalog.EngineFeatures = sortedKeys(compiler.features)
	for _, name := range sortedKeys(compiler.modules) {
		catalog.Modules = append(catalog.Modules, ModuleSpec{Name: name, Directions: append([]Direction(nil), compiler.modules[name].Directions()...)})
	}
	return catalog
}

// DescribeSchema returns fresh schema/catalog objects without registry state.
func DescribeSchema() SchemaCatalog {
	definitions := make(map[string]any)
	root := schemaForType(reflect.TypeFor[Definition](), definitions)
	root["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	root["$defs"] = definitions
	semanticDefinitions := make(map[string]any)
	bindingDefinitions := make(map[string]any)
	binding := schemaForType(reflect.TypeFor[Binding](), bindingDefinitions)
	binding["$defs"] = bindingDefinitions
	semantic := map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "request": schemaForType(reflect.TypeFor[Request](), semanticDefinitions), "response": schemaForType(reflect.TypeFor[Response](), semanticDefinitions), "event": schemaForType(reflect.TypeFor[Event](), semanticDefinitions), "task": schemaForType(reflect.TypeFor[Task](), semanticDefinitions), "models": schemaForType(reflect.TypeFor[ModelPage](), semanticDefinitions), "agent": schemaForType(reflect.TypeFor[AgentPreferences](), semanticDefinitions), "$defs": semanticDefinitions}
	conversionDefs := make(map[string]any)
	conversionSchema := schemaForType(reflect.TypeFor[ConversionPolicy](), conversionDefs)
	conversionSchema["$defs"] = conversionDefs
	return SchemaCatalog{Conversion: map[string]any{"actionSchemas": map[string]any{
		"tool_result_text":         map[string]any{"phases": []ConversionPhase{ConversionRequest}, "description": "将对象工具结果序列化为 JSON 文本，保留数值精度及调用关联。兼容模式记录类型变化；严格模式拒绝。"},
		"system_instruction_hoist": map[string]any{"phases": []ConversionPhase{ConversionRequest}, "description": "按原顺序将 system/developer 上提为顶层系统要求。作用范围或角色优先级变化时记录降级；严格模式拒绝。"},
		"response_metadata":        map[string]any{"phases": []ConversionPhase{ConversionRequest, ConversionResponse, ConversionEvent}, "value": map[string]any{"type": "string", "enum": []string{"openai-chat", "responses", "anthropic", "gemini"}}, "description": "逐项转换已识别的响应元数据，保留所属文本、候选及来源路径。未知扩展、文件引用和上下文状态仍受保护。"},
		"response_shape":           map[string]any{"phases": []ConversionPhase{ConversionResponse, ConversionEvent}, "value": map[string]any{"type": "string", "enum": []string{"openai-chat", "responses", "anthropic", "gemini"}}, "description": "转换一轮回答的有序输出项；消息边界或交错关系丢失时诊断，严格模式拒绝。不会将多个候选答案混成一轮。"},
		"response_envelope":        map[string]any{"phases": []ConversionPhase{ConversionResponse, ConversionEvent}, "value": map[string]any{"type": "string", "enum": []string{"openai-chat", "responses", "anthropic", "gemini"}}, "description": "补齐目标响应的稳定 ID、时间和项目状态，不改变上游观测用量。合法原生输出保持原样。"},
		"anthropic_usage_envelope": map[string]any{"phases": []ConversionPhase{ConversionResponse, ConversionEvent}, "description": "补齐 Anthropic 必填用量。兼容模式缺失计数补客户端专用零值并诊断；严格模式拒绝。内部计量不变，旧客户端可能保留首帧占位数。"},
		"responses_storage":        map[string]any{"phases": []ConversionPhase{ConversionRequest}, "value": map[string]any{"type": "object", "default": map[string]any{"targetCodec": "gemini", "onUnsupported": "degrade"}, "properties": map[string]any{"targetCodec": map[string]any{"type": "string", "enum": []string{"responses", "gemini", "anthropic", "openai-chat"}}, "onUnsupported": map[string]any{"type": "string", "enum": []string{"degrade", "reject"}}}}, "description": "Responses 保存意图：false 允许无状态转换；true、缺省和 null 在兼容模式诊断降级，严格模式拒绝。不影响日志及签名恢复副本。"},
		"responses_include":        map[string]any{"phases": []ConversionPhase{ConversionRequest}, "value": map[string]any{"type": "string", "enum": []string{"openai-chat", "responses", "anthropic", "gemini"}}, "description": "分离 Responses 输出选择；reasoning.encrypted_content 接入认证续传，未知选择项跨协议拒绝。"},
		"responses_context":        map[string]any{"phases": []ConversionPhase{ConversionRequest}, "value": map[string]any{"type": "string", "enum": []string{"openai-chat", "responses", "anthropic", "gemini"}}, "description": "跨协议将 null 上一轮引用规范化为无引用并记录信息级诊断；非空引用和未实现的截断约束明确拒绝。原生 Responses 保留原值，不扩展其会话能力。"},
		"usage_projection":         map[string]any{"phases": []ConversionPhase{ConversionResponse, ConversionEvent}, "value": map[string]any{"type": "string", "enum": []string{"openai-chat", "responses", "anthropic", "gemini"}}, "description": "按目标投影思考、工具提示及缓存明细；内部计量保留，严格模式拒绝信息损失。"},
	}, "policy": conversionSchema, "phases": []ConversionPhase{ConversionIngress, ConversionRequest, ConversionResponse, ConversionEvent, ConversionWire}, "actions": []string{"set", "remove", "transform", "warn", "reject", "signatures", "stream_options", "tool_result_object", "tool_result_text", "system_instruction_hoist", "response_metadata", "response_shape", "response_envelope", "buffer_node", "provider_signature", "responses_include", "responses_context", "usage_projection", "responses_storage", "anthropic_usage_envelope"}}, SchemaVersion: DefinitionSchemaVersion, CompilerVersion: CompilerVersion, Directions: DirectionCatalog(), Transports: TransportCatalog(), OperationKinds: OperationKindCatalog(), Capabilities: CapabilityCatalog(), Events: EventCatalog(), Diagnostics: DiagnosticCatalog(), Operations: ExpressionCatalog(), Definition: root, Semantic: semantic, Binding: binding, Limits: DefaultLimits()}
}

func schemaForType(kind reflect.Type, definitions map[string]any) map[string]any {
	if kind == reflect.TypeFor[Value]() {
		return map[string]any{}
	}
	if kind.Kind() == reflect.Pointer {
		return schemaForType(kind.Elem(), definitions)
	}
	switch kind.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int64:
		return map[string]any{"type": "integer"}
	case reflect.Slice:
		return map[string]any{"type": "array", "items": schemaForType(kind.Elem(), definitions)}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": schemaForType(kind.Elem(), definitions)}
	case reflect.Struct:
		name := kind.Name()
		if _, exists := definitions[name]; !exists {
			definitions[name] = map[string]any{}
			properties := make(map[string]any)
			required := []string{}
			for index := 0; index < kind.NumField(); index++ {
				field := kind.Field(index)
				if !field.IsExported() {
					continue
				}
				tag := strings.Split(field.Tag.Get("json"), ",")
				if tag[0] == "-" {
					continue
				}
				fieldName := tag[0]
				if fieldName == "" {
					fieldName = field.Name
				}
				property := schemaForType(field.Type, definitions)
				if description := field.Tag.Get("description"); description != "" {
					property["description"] = description
				}
				properties[fieldName] = property
				if len(tag) == 1 {
					required = append(required, fieldName)
				}
			}
			definitions[name] = map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
		}
		return map[string]any{"$ref": "#/$defs/" + name}
	}
	return map[string]any{}
}
