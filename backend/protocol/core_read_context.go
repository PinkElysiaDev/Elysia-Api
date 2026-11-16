package protocol

const provenanceReadCapacity = 2

// Normalization keeps its original owner, including the uncached nil path.
// Provenance reuse belongs only to one immutable response encoding invocation.
type coreReadContext struct {
	cache      *semanticCache
	provenance *provenanceReadMemo
}

type provenanceReadEntry struct {
	source Provenance
	node   jsonOutputNode
}

type provenanceReadMemo struct {
	entries *[provenanceReadCapacity]provenanceReadEntry
	count   int
}

func (memo *provenanceReadMemo) readProvenance(source Provenance) (JSONOutput, bool) {
	for index := range memo.count {
		entry := &memo.entries[index]
		if entry.source == source {
			// Capture authority may tag a root; immutable children can be shared.
			node := entry.node
			return JSONOutput{node: &node}, true
		}
	}
	return JSONOutput{}, false
}

func (memo *provenanceReadMemo) retainProvenance(source Provenance, output JSONOutput) {
	if memo.count == provenanceReadCapacity {
		return
	}
	if memo.entries == nil {
		memo.entries = new([provenanceReadCapacity]provenanceReadEntry)
	}
	memo.entries[memo.count] = provenanceReadEntry{source: source, node: *output.node}
	memo.count++
}

func (memo *provenanceReadMemo) release() {
	if memo != nil {
		memo.entries = nil
		memo.count = 0
	}
}
