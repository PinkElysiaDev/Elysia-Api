package protocol

import (
	"crypto/rand"
	"time"
)

// DeliveryState is owned by one attempt, never by a shared compiled policy.
// Tests/previews can inject a deterministic ID and timestamp.
type DeliveryState struct {
	ID       Value
	Created  Value
}

func NewDeliveryState() *DeliveryState {
	created, _ := EncodeValue(time.Now().Unix())
	return &DeliveryState{ID: StringValue("resp_" + rand.Text()), Created: created}
}

func copyObject(o Object) Object {
	copy := Object{}
	for k, v := range o {
		copy[k] = v
	}
	return copy
}
