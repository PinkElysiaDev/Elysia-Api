package builtin

import (
	"fmt"
	"sync"

	p "github.com/elysia-api/backend/protocol"
)

const (
	Chat        = "openai-chat"
	Responses   = "responses"
	Anthropic   = "anthropic"
	Gemini      = "gemini"
	WireVersion = "v2"
)

var packageFixtures = []struct{ name, family string }{
	{Chat, "openai_chat"}, {Responses, "openai_responses"}, {Anthropic, "claude"}, {Gemini, "gemini"},
}

func packageName(name string) string {
	switch name {
	case Responses:
		return "openai-responses"
	case Gemini:
		return "google-generate-content"
	default:
		return name
	}
}

// Authored package fixtures share immutable compiled plans, never stream state.
var shippedTestProtocols = sync.OnceValues(func() (map[string]*p.Compiled, error) {
	compiler, err := p.NewCompiler(p.DefaultLimits(), nil)
	if err != nil {
		return nil, err
	}
	result := make(map[string]*p.Compiled, len(packageFixtures))
	for _, fixture := range packageFixtures {
		raw, err := readPackage(packageName(fixture.name))
		if err != nil {
			return nil, err
		}
		compiled, issues := compiler.Compile(raw.Bytes())
		if err := p.IssuesError(issues); err != nil {
			return nil, fmt.Errorf("compile %s: %w", fixture.name, err)
		}
		result[fixture.name] = compiled
		result[packageName(fixture.name)] = compiled
	}
	return result, nil
})

func packageMapping(compiled *p.Compiled) p.ProtocolMapping {
	document, _ := compiled.Package()
	return document.Mapping
}

func fixtureNodes(nodes []p.Node, kind p.NodeKind) []p.Node {
	var selected []p.Node
	for _, node := range nodes {
		if node.Kind == kind {
			selected = append(selected, node)
		}
		selected = append(selected, fixtureNodes(node.Children, kind)...)
		selected = append(selected, fixtureNodes(node.ReasoningContent, kind)...)
	}
	return selected
}

func setRequestParameter(request *p.Request, name string, value p.Value) {
	if request.Parameters == nil {
		request.Parameters = p.Object{}
	}
	request.Parameters[name] = value
}

const fixtureOperationID = "fixture-execution"
const fixtureStartedAt = 1

// fixtureOptions supplies the operation facts that the real gateway owns.
func fixtureOptions(options p.EvaluationContext) p.EvaluationContext {
	bound, err := options.WithOperation(fixtureOperationID, fixtureStartedAt)
	if err != nil {
		panic(err)
	}
	return bound
}
