package protocol

import "maps"

// Clone returns a request that shares no mutable slice with its source. The
// gateway clones a bound request per attempt so a mutation applied for one
// candidate (synthesized cache breakpoints, a rewritten model) cannot reach the
// next candidate through a shared backing array.
//
// Values are immutable and are copied as-is. Only the slices that a caller can
// mutate in place are duplicated, and only along the content path actually
// reachable by those mutations.
func (request *Request) Clone() *Request {
	if request == nil {
		return nil
	}
	copy := *request
	copy.Parameters = maps.Clone(request.Parameters)
	if request.ClientOutput != nil {
		output := *request.ClientOutput
		if output.IncludeUsage != nil {
			value := *output.IncludeUsage
			output.IncludeUsage = &value
		}
		copy.ClientOutput = &output
	}
	copy.Cache = append([]CacheIntent(nil), request.Cache...)
	copy.Resources = append([]Resource(nil), request.Resources...)
	copy.Content = cloneNodes(request.Content)
	if request.Tools != nil {
		copy.Tools = make([]Tool, len(request.Tools))
		for index, tool := range request.Tools {
			copy.Tools[index] = tool
			copy.Tools[index].Cache = append([]CacheIntent(nil), tool.Cache...)
			copy.Tools[index].Options = maps.Clone(tool.Options)
		}
	}
	return &copy
}

func cloneNodes(nodes []Node) []Node {
	if nodes == nil {
		return nil
	}
	copy := make([]Node, len(nodes))
	for index, node := range nodes {
		copy[index] = node
		copy[index].Attributes = maps.Clone(node.Attributes)
		if node.Input != nil {
			input := *node.Input
			copy[index].Input = &input
		}
		copy[index].Cache = append([]CacheIntent(nil), node.Cache...)
		copy[index].Resources = append([]Resource(nil), node.Resources...)
		copy[index].Children = cloneNodes(node.Children)
	}
	return copy
}
