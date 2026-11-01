package protocol

import (
	"crypto/sha256"
	"encoding/hex"
)

func (plan *compiledOutputPlan) buildJSONDigest(state evaluation) (plannedOutput, error) {
	input, err := plan.source.build(state)
	if err != nil || input.output.IsZero() {
		return input, err
	}
	hasher := streamValueHasher{ctx: state.ctx, digest: sha256.New()}
	digest, err := hasher.sum(input.output)
	if err != nil {
		return plannedOutput{}, err
	}
	return plannedLeaf(StringValue(hex.EncodeToString(digest[:]))), nil
}
