package protocol

import (
	"reflect"
	"sync"

	"github.com/bytedance/sonic"
	"github.com/invopop/jsonschema"
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

type schemaTemplates struct {
	definition []byte
	binding    []byte
	conversion []byte
	semantic   map[string][]byte
}

// Only fixed Go contracts are shared. Every caller decodes its own mutable maps.
var loadSchemaTemplates = sync.OnceValue(buildSchemaTemplates)

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
	templates := loadSchemaTemplates()
	root := decodeSchemaTemplate(templates.definition)
	binding := decodeSchemaTemplate(templates.binding)
	// semanticSchema is a catalog of named roots, not a single object schema.
	// Keep its existing envelope and shared local-reference namespace.
	definitions := make(map[string]any)
	semantic := map[string]any{"$schema": jsonschema.Version, "$defs": definitions}
	for name, template := range templates.semantic {
		schema := decodeSchemaTemplate(template)
		for name, definition := range schema["$defs"].(map[string]any) {
			definitions[name] = definition
		}
		delete(schema, "$schema")
		delete(schema, "$defs")
		semantic[name] = schema
	}
	conversionSchema := decodeSchemaTemplate(templates.conversion)
	actionSchemas, actionNames := conversionActionSchemas()
	return SchemaCatalog{
		Conversion: map[string]any{
			"actionSchemas": actionSchemas, "policy": conversionSchema,
			"phases": conversionPhases(), "actions": actionNames,
		},
		SchemaVersion:   DefinitionSchemaVersion,
		CompilerVersion: CompilerVersion,
		Directions:      DirectionCatalog(),
		Transports:      TransportCatalog(),
		OperationKinds:  OperationKindCatalog(),
		Capabilities:    CapabilityCatalog(),
		Events:          EventCatalog(),
		Diagnostics:     DiagnosticCatalog(),
		Operations:      ExpressionCatalog(),
		Definition:      root,
		Semantic:        semantic,
		Binding:         binding,
		Limits:          DefaultLimits(),
	}
}

func buildSchemaTemplates() schemaTemplates {
	templates := schemaTemplates{
		definition: encodeSchemaTemplate(reflect.TypeFor[Definition]()),
		binding:    encodeSchemaTemplate(reflect.TypeFor[Binding]()),
		semantic:   make(map[string][]byte),
	}
	for name, kind := range map[string]reflect.Type{
		"request": reflect.TypeFor[Request](), "response": reflect.TypeFor[Response](),
		"event": reflect.TypeFor[Event](), "task": reflect.TypeFor[Task](),
		"models": reflect.TypeFor[ModelPage](), "agent": reflect.TypeFor[AgentPreferences](),
	} {
		templates.semantic[name] = encodeSchemaTemplate(kind)
	}
	templates.conversion = encodeSchemaTemplate(reflect.TypeFor[ConversionPolicy]())
	return templates
}

func encodeSchemaTemplate(kind reflect.Type) []byte {
	reflector := jsonschema.Reflector{
		// Catalog consumers resolve local $defs references; don't introduce an
		// automatic package-URL $id that would change the resolution base.
		Anonymous: true,
		Mapper: func(kind reflect.Type) *jsonschema.Schema {
			// Value's private representation stores arbitrary exact JSON, including
			// null, false and large numbers. Reflecting its Go struct would wrongly
			// describe a closed, empty object instead of the wire value.
			if kind == reflect.TypeFor[Value]() {
				return &jsonschema.Schema{}
			}
			return nil
		},
	}
	// Preserve SchemaCatalog's map-shaped API. The library's custom marshalers
	// own schema serialization; application-owned encoding/decoding uses Sonic.
	encoded, err := sonic.ConfigStd.Marshal(reflector.ReflectFromType(kind))
	if err != nil {
		panic("encode protocol schema: " + err.Error())
	}
	return encoded
}

func decodeSchemaTemplate(encoded []byte) map[string]any {
	var schema map[string]any
	if err := sonic.ConfigStd.Unmarshal(encoded, &schema); err != nil {
		panic("decode protocol schema: " + err.Error())
	}
	return schema
}
