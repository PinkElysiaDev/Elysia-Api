package protocol

import (
	"errors"
	"testing"
	"time"
)

func TestContinuationCaptureIndexSharesSealedIdentity(t *testing.T) {
	compiled := compilePackageForTest(t, continuationTestPackage(t))
	source := Identity{DefinitionID: "source", Family: "source", WireVersion: "1"}
	node := Node{Kind: TextNode, Payload: StringValue("hi")}
	digest := ContinuationNodeDigest(node)
	codec, err := NewContinuationCodec([]byte("capture-index-fixture"))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := codec.SealState(ContinuationRecord{Version: 1, ID: "one", Ordinal: 2, Protocol: source, Digest: digest, Node: node, ExpiresAt: time.Now().Add(time.Hour).Unix()}, 4096)
	if err != nil {
		t.Fatal(err)
	}
	for _, fits := range []bool{false, true} {
		limits := DefaultLimits()
		limits.StateItems = 1 // The capture index aliases the existing sealed entry.
		limits.BufferBytes = len(sealed.Token()) + len("one") + len(digest)
		if !fits {
			limits.BufferBytes--
		}
		binding, err := compiled.BindContinuation(EncodeResponse, source, Scope{}, true)
		if err != nil {
			t.Fatal(err)
		}
		budget := NewStreamBudget(limits)
		if err := budget.Bind(binding); err != nil {
			t.Fatal(err)
		}
		err = binding.Append(sealed)
		if !fits {
			var failure *ResourceLimitError
			if !errors.As(err, &failure) || binding.HasCaptured(2, digest) || budget.bytes != 0 {
				t.Fatal("rejected capture published its deduplication receipt", err)
			}
		} else if err != nil || !binding.HasCaptured(2, digest) || binding.HasCaptured(3, digest) || binding.HasCaptured(2, "changed") || budget.items != 1 {
			t.Fatal("capture index lost position or used another identity slot", err)
		}
		binding.Release()
		if binding.HasCaptured(2, digest) || budget.items != 0 || budget.bytes != 0 {
			t.Fatal("capture receipt survived release")
		}
	}
}

func TestCollectorContinuationDigestSharesBudget(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		for _, fits := range []bool{false, true} {
			target := replayTarget()
			target.Scope = Scope{Provider: "fixture", Account: "local"}
			collector, err := NewResponseCollector(target, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(collector.Release)
			node := Node{Kind: TextNode, Payload: StringValue("hi"), Resources: []Resource{{Kind: "signature", ID: StringValue("signed"), Scope: target.Scope}}}
			start := messageEvent(ItemStarted, "text", "", &node)
			if _, _, err := collector.Consume(start); err != nil {
				t.Fatal(err)
			}
			binding := &ContinuationBinding{}
			bound, err := collector.BindContinuationEvent(start, binding)
			if err != nil {
				t.Fatal(err)
			}
			finish := messageEvent(ItemFinished, "text", "", nil)
			if _, _, err := collector.Consume(finish); err != nil {
				t.Fatal(err)
			}
			end := Event{SchemaVersion: 1, Type: ResponseFinished}
			if terminal {
				if _, _, err := collector.Consume(end); err != nil {
					t.Fatal(err)
				}
			}
			limits := DefaultLimits()
			limits.BufferBytes = collector.bytes + collector.replay.retainedBytes() + len(ContinuationNodeDigest(node))
			if !fits {
				limits.BufferBytes--
			}
			budget := NewStreamBudget(limits)
			if err := budget.Bind(collector); err != nil {
				t.Fatal(err)
			}
			before := budget.bytes
			complete := func() error {
				if terminal {
					_, err := collector.BindContinuationEvent(end, binding)
					return err
				}
				_, _, _, err := collector.CompletedNode(finish)
				return err
			}
			if err := complete(); !fits {
				var failure *ResourceLimitError
				if !errors.As(err, &failure) || bound.continuation.isComplete || bound.continuation.digest != "" || budget.bytes != before {
					t.Fatal("over-budget digest established completion", err)
				}
				if _, err := collector.Finish(); err == nil {
					t.Fatal("failed proof became a successful response")
				}
			} else {
				if err != nil || bound.continuation.digest != ContinuationNodeDigest(node) || budget.bytes != limits.BufferBytes {
					t.Fatal("completed digest was not charged once", err)
				}
				if err := complete(); err != nil || budget.bytes != limits.BufferBytes {
					t.Fatal("observing completion charged the digest twice", err)
				}
			}
			collector.Release()
			if budget.bytes != 0 || budget.items != 0 {
				t.Fatal("proof metadata survived collector release")
			}
		}
	}
}

func TestCollectorFinalUsageSharesBudget(t *testing.T) {
	for _, fits := range []bool{false, true} {
		collector, err := NewResponseCollector(replayTarget(), DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(collector.Release)
		usage := &Usage{Input: &Counter{Count: 7, Origin: ObservedCount}, Details: map[string]Counter{"media": {Count: 2, Origin: ObservedCount}}}
		if _, _, err := collector.Consume(Event{SchemaVersion: 1, Type: ResponseFinished, Response: &Response{SchemaVersion: 1, Usage: usage}}); err != nil {
			t.Fatal(err)
		}
		if collector.response.Usage != nil {
			t.Fatal("terminal retained a redundant usage snapshot before Finish")
		}
		limits := DefaultLimits()
		limits.BufferBytes = collector.bytes + collector.replay.retainedBytes() + collector.replay.usageBytes
		if !fits {
			limits.BufferBytes--
		}
		budget := NewStreamBudget(limits)
		if err := budget.Bind(collector); err != nil {
			t.Fatal(err)
		}
		response, err := collector.Finish()
		if !fits {
			var failure *ResourceLimitError
			if !errors.As(err, &failure) || response != nil || collector.response.Usage != nil {
				t.Fatal("Finish copied usage beyond shared capacity", err)
			}
		} else {
			if err != nil || response.Usage.Input.Count != 7 || budget.bytes != limits.BufferBytes || budget.items != 2 {
				t.Fatal("final usage copy escaped accounting", err)
			}
			if _, err := collector.Finish(); err != nil || budget.bytes != limits.BufferBytes {
				t.Fatal("repeated Finish retained another usage copy", err)
			}
			response.Usage.Input.Count = 100
			if collector.replay.Usage().Input.Count != 7 {
				t.Fatal("returned usage mutated replay counters")
			}
		}
		collector.Release()
		if budget.bytes != 0 || budget.items != 0 {
			t.Fatal("usage copy retained a stream reservation after release")
		}
	}
}
