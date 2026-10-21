package protocol

import "unsafe"

// immutableSpan identifies storage, not equal JSON content. It is only used in
// bounded execution-local caches of validated immutable strings. The typed
// pointer keeps storage alive (never convert it to uintptr), preventing address
// reuse while a fact is cached. Length distinguishes views at the same address.
// StringData is inspected for identity only: no dereference or mutation occurs.
// Equal content in independently owned storage may miss; it cannot reuse a stale
// fact. Public Value equality and its single-string representation are unchanged.
type immutableSpan struct {
	data  *byte
	bytes int
}

func spanIdentity(raw string) immutableSpan {
	return immutableSpan{unsafe.StringData(raw), len(raw)}
}
