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
		"responses_include": map[string]any{"phases": []ConversionPhase{ConversionRequest}, "value": map[string]any{"type": "string", "enum": []string{"openai-chat", "responses", "anthropic", "gemini"}}, "description": "分离 Responses 输出选择；reasoning.encrypted_content 接入认证续传，未知选择项跨协议拒绝。"},
		"usage_projection":  map[string]any{"phases": []ConversionPhase{ConversionResponse, ConversionEvent}, "value": map[string]any{"type": "string", "enum": []string{"openai-chat", "responses", "anthropic", "gemini"}}, "description": "按目标投影思考、工具提示及缓存明细；内部计量保留，严格模式拒绝信息损失。"},
	}, "policy": conversionSchema, "phases": []ConversionPhase{ConversionIngress, ConversionRequest, ConversionResponse, ConversionEvent, ConversionWire}, "actions": []string{"set", "remove", "transform", "warn", "reject", "signatures", "stream_options", "tool_result_object", "buffer_node", "provider_signature", "responses_include", "usage_projection"}}, SchemaVersion: DefinitionSchemaVersion, CompilerVersion: CompilerVersion, Directions: DirectionCatalog(), Transports: TransportCatalog(), OperationKinds: OperationKindCatalog(), Capabilities: CapabilityCatalog(), Events: EventCatalog(), Diagnostics: DiagnosticCatalog(), Operations: ExpressionCatalog(), Definition: root, Semantic: semantic, Binding: binding, Limits: DefaultLimits()}
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
