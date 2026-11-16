package protocol

// Owner conditions are local, read-only programs. Evaluate their successful
// walks together, but commit their costs at the original declaration position.
// Any rejection, expression error or cumulative budget failure uses the original
// effect-major walk. That cold replay preserves FIRST-error precedence even
// across intervening non-owner effects, as well as exact resource diagnostics.
// In particular, node-major rejection order is NOT declaration-major order.
type ownerEffectVerdict struct {
	effect *compiledSemanticEffect
	budget evaluationBudget
	failed bool
}

func (compiled *Compiled) evaluateOwnerEffects(direction Direction, state evaluation, provenance *provenanceReadMemo) []ownerEffectVerdict {
	effects := compiled.packagePlan.effects[direction]
	count := 0
	for _, effect := range effects {
		if isOwnerEffectScope(effect.scope) {
			count++
		}
	}
	verdicts := make([]ownerEffectVerdict, 0, count)
	for index := range effects {
		if isOwnerEffectScope(effects[index].scope) {
			budget := *state.budget
			budget.steps, budget.walkNodes = 0, 0
			verdicts = append(verdicts, ownerEffectVerdict{effect: &effects[index], budget: budget})
		}
	}
	reader := coreReadContext{cache: state.cache, provenance: provenance}
	var selectedNode *Node
	var selectedMetadata ResponseMetadata
	readNode := func(pointer []string) (JSONOutput, error) { return reader.readNode(*selectedNode, pointer) }
	readMetadata := func(pointer []string) (JSONOutput, error) {
		return readCoreResponseMetadata(selectedMetadata, pointer, state.cache)
	}
	local := evaluation{ctx: state.ctx, valueReader: state.valueReader}
	remaining := state.budget.stepLimit
	if remaining == 0 {
		remaining = state.budget.limits.Nodes
	}
	remaining -= state.budget.steps
	incomplete := false
	check := func(index int, read func([]string) (JSONOutput, error)) {
		verdict := &verdicts[index]
		if verdict.failed || incomplete {
			return
		}
		if remaining <= 0 {
			incomplete = true
			return
		}
		local.budget, local.inputReader, local.rootReader = &verdict.budget, read, read
		// Speculation itself shares the original remaining step allowance. It
		// cannot multiply the work budget by the number of owner effects.
		before := local.budget.steps
		local.budget.stepLimit = before + remaining
		condition, err := verdict.effect.when.evaluate(local)
		remaining -= local.budget.steps - before
		if remaining < 0 {
			incomplete = true
		}
		if err != nil {
			verdict.failed = true
			return
		}
		reject, err := readBoolean(condition)
		verdict.failed = err != nil || reject
	}
	metadata := func(fields []ResponseMetadata) {
		for _, field := range fields {
			selectedMetadata = field
			for index := range verdicts {
				if verdicts[index].effect.scope == metadataScope {
					check(index, readMetadata)
				}
			}
		}
	}
	var walk func([]Node)
	var node func(*Node)
	node = func(current *Node) {
		if incomplete {
			return
		}
		selectedNode = current
		for index := range verdicts {
			effect := verdicts[index].effect
			if verdicts[index].failed {
				continue
			}
			if remaining <= 0 {
				incomplete = true
				return
			}
			remaining--
			local.budget = &verdicts[index].budget
			local.budget.stepLimit = local.budget.steps + remaining + 1
			if err := local.advance(); err != nil {
				verdicts[index].failed = true
				continue
			}
			if effect.scope == nodeScope {
				check(index, readNode)
			}
		}
		metadata(current.Metadata)
		walk(current.Children)
		walk(current.ReasoningContent)
	}
	walk = func(nodes []Node) {
		for index := range nodes {
			if incomplete {
				return
			}
			node(&nodes[index])
		}
	}
	switch document := state.coreDocument.(type) {
	case *Request:
		walk(document.Content)
	case *Response:
		metadata(document.Metadata)
		walk(document.Content)
	case *Event:
		metadata(document.Metadata)
		if document.Item != nil {
			node(document.Item)
		}
		if document.Response != nil {
			metadata(document.Response.Metadata)
			walk(document.Response.Content)
		}
		if document.Request != nil {
			walk(document.Request.Content)
		}
	default:
		panic("compiled owner selector received a different core type")
	}
	if incomplete {
		// No partial success is reusable. Declaration-major replay reports the
		// precise original failure instead of exposing a speculative limit.
		for index := range verdicts {
			verdicts[index].failed = true
		}
	}
	return verdicts
}

func (verdict ownerEffectVerdict) commit(state *evaluation) bool {
	budget := state.budget
	stepLimit := budget.stepLimit
	if stepLimit == 0 {
		stepLimit = budget.limits.Nodes
	}
	if verdict.failed || verdict.budget.steps > stepLimit-budget.steps || verdict.budget.walkNodes > budget.limits.Nodes-budget.walkNodes {
		return false
	}
	budget.steps += verdict.budget.steps
	budget.walkNodes += verdict.budget.walkNodes
	budget.hasReadOutputStream = budget.hasReadOutputStream || verdict.budget.hasReadOutputStream
	return true
}
