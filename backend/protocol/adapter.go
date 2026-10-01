package protocol

// DecodeOptions supplies transport context which is not necessarily in JSON.
type DecodeOptions struct {
	Model string
	Scope Scope
}

// Adapter defines four independent wire directions. The type parameters allow
// legacy callers to share modules during migration without importing relay
// into the protocol engine. New definitions use Request and Response.
type Adapter[Q, R any] interface {
	Identity() Identity
	DecodeRequest([]byte, DecodeOptions) (*Q, error)
	EncodeRequest(*Q) ([]byte, error)
	DecodeResponse([]byte) (*R, error)
	EncodeResponse(*R) ([]byte, error)
}
