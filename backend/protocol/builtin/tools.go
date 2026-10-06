package builtin

import (
	"fmt"

	p "github.com/elysia-api/backend/protocol"
)

func (adapter module) decodeTools(value p.Value, options p.EvaluationContext) ([]p.Tool, error) {
	items, err := readArray(value)
	if err != nil {
		return nil, err
	}
	var tools []p.Tool
	for index, item := range items {
		fields, err := item.ReadObject()
		if err != nil {
			return nil, err
		}
		path := fmt.Sprintf("/tools/%d", index)
		if adapter.name == Gemini {
			if declarations, exists := fields["functionDeclarations"]; exists {
				functions, err := readArray(declarations)
				if err != nil {
					return nil, err
				}
				for _, function := range functions {
					definition, err := function.ReadObject()
					if err != nil {
						return nil, err
					}
					tools = append(tools, p.Tool{Kind: p.FunctionTool, Name: definition["name"], Description: definition["description"], InputSchema: definition["parameters"], Options: adapter.extensions(definition, []string{"name", "description", "parameters"})})
				}
				delete(fields, "functionDeclarations")
				if len(fields) == 0 {
					continue
				}
				item = object(fields)
			}
			tools = append(tools, p.Tool{Kind: p.ServerTool, Native: adapter.native(item, path, p.DecodeRequest, options)})
			continue
		}
		kind, err := optionalString(fields["type"])
		if err != nil {
			return nil, err
		}
		cache, err := decodeCache(fields, "tool")
		if err != nil {
			return nil, err
		}
		tool := p.Tool{Kind: p.FunctionTool, Native: adapter.native(item, path, p.DecodeRequest, options), Cache: cache}
		definition := fields
		if adapter.name == Chat && !fields["function"].IsZero() {
			definition, err = fields["function"].ReadObject()
			if err != nil {
				return nil, err
			}
		}
		if adapter.name == Anthropic && (kind == "" || kind == "custom") {
			tool.Name, tool.Description, tool.InputSchema = definition["name"], definition["description"], definition["input_schema"]
			tool.Options = adapter.extensions(definition, []string{"type", "name", "description", "input_schema", "cache_control"})
		} else if kind == "function" {
			tool.Name, tool.Description, tool.InputSchema = definition["name"], definition["description"], definition["parameters"]
			tool.Options = adapter.extensions(definition, []string{"type", "name", "description", "parameters", "strict", "cache_control"})
			if adapter.name == Chat && !fields["function"].IsZero() {
				tool.Options, err = adapter.nestedExtensions(fields, []string{"type", "function", "cache_control"}, map[string][]string{"function": {"name", "description", "parameters", "strict"}})
				if err != nil {
					return nil, err
				}
			}
			if tool.Options == nil {
				tool.Options = p.Object{}
			}
			if strict := definition["strict"]; !strict.IsZero() {
				tool.Options["strict"] = strict
			}
		} else if adapter.name == Responses && kind == "custom" {
			tool.Kind, tool.Name, tool.Description, tool.Format = p.FreeTextTool, fields["name"], fields["description"], fields["format"]
			tool.Options = adapter.extensions(fields, []string{"type", "name", "description", "format", "cache_control"})
		} else {
			if kind == "" {
				return nil, fmt.Errorf("%s/type: tool type is required", path)
			}
			tool.Kind, tool.Name = p.ServerTool, fields["name"]
		}
		tools = append(tools, tool)
	}
	return tools, nil
}

func (adapter module) encodeTools(tools []p.Tool, options p.EvaluationContext) (p.Value, error) {
	if tools == nil {
		return p.Value{}, nil
	}
	var items []p.Value
	for _, tool := range tools {
		if tool.Kind == p.ServerTool || tool.Kind == p.OpaqueTool {
			value, err := adapter.replay(tool.Native, p.EncodeRequest, options)
			if err != nil {
				return p.Value{}, err
			}
			fields, err := value.ReadObject()
			if err != nil {
				return p.Value{}, err
			}
			if tool.Name.IsZero() {
				delete(fields, "name")
			} else {
				fields["name"] = tool.Name
			}
			items = append(items, object(fields))
			continue
		}
		fields := p.Object{"name": tool.Name, "description": tool.Description}
		if strict := tool.Options["strict"]; !strict.IsZero() {
			if adapter.name != Chat && adapter.name != Responses {
				return p.Value{}, unsupported("/tools/strict", "target has no equivalent strict tool-schema policy")
			}
			fields["strict"] = strict
		}
		if tool.Kind == p.FreeTextTool {
			if adapter.name != Responses {
				return p.Value{}, unsupported("/tools", "target cannot express free-text tools")
			}
			fields["type"], fields["format"] = p.StringValue("custom"), tool.Format
		} else {
			fields["parameters"] = tool.InputSchema
			switch adapter.name {
			case Chat:
				fields = p.Object{"type": p.StringValue("function"), "function": object(fields)}
			case Responses:
				fields["type"] = p.StringValue("function")
			case Anthropic:
				delete(fields, "parameters")
				fields["input_schema"] = tool.InputSchema
			case Gemini:
				if err := adapter.preserveExtensions(fields, tool.Options); err != nil {
					return p.Value{}, err
				}
				fields = p.Object{"functionDeclarations": array([]p.Value{object(fields)})}
			}
		}
		if err := adapter.encodeCache(fields, tool.Cache); err != nil {
			return p.Value{}, err
		}
		if adapter.name != Gemini {
			if err := adapter.preserveExtensions(fields, tool.Options); err != nil {
				return p.Value{}, err
			}
		}
		items = append(items, object(fields))
	}
	return array(items), nil
}
