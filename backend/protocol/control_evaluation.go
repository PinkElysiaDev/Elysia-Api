package protocol

// One small shared frame replaces eager reader closures, binding storage and
// independent budget/reader allocations. Its context encoding and object index
// are initialized only by an actual context read, not merely a declared branch.
type controlEvaluationResources struct {
	budget       evaluationBudget
	reader       valueReader
	bindingSlots int
	bindings     []JSONOutput
	transport    Transport
	model        string
	isSameWire   bool
	observations map[string]fieldObservation
	context      Value
	objects      *planObjectReader
	contextReady bool
}

func (state *evaluation) ensureControlBindings() {
	if resources := state.control; resources != nil {
		if resources.bindings == nil && resources.bindingSlots != 0 {
			resources.bindings = make([]JSONOutput, resources.bindingSlots)
		}
		state.bindings = resources.bindings
	}
}

func (resources *controlEvaluationResources) readContext(pointer []string) (Value, error) {
	if !resources.contextReady {
		value, err := EncodeValue(struct {
			Transport    Transport                   `json:"transport"`
			Model        string                      `json:"model"`
			IsSameWire   bool                        `json:"isSameWire"`
			Observations map[string]fieldObservation `json:"observations,omitempty"`
		}{resources.transport, resources.model, resources.isSameWire, resources.observations})
		if err != nil {
			return Value{}, err
		}
		resources.context = value
		resources.objects = newPlanObjectReader(value, objectReuseReads)
		resources.contextReady = true
	}
	return readValuePointerWithObjectReader(resources.context, pointer, resources.reader.forParent(resources.context), resources.objects)
}

func readControlDocument(document any, pointer []string, cache *semanticCache) (JSONOutput, error) {
	switch current := document.(type) {
	case *Request:
		return readCoreRequestOutput(current, pointer, cache)
	case *Response:
		return readCoreResponseOutput(current, pointer, cache)
	case *Event:
		return readCoreEventOutput(current, pointer, cache)
	default:
		panic("compiled control reader received a different core type")
	}
}
