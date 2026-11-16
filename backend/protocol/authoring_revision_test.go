package protocol

import (
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"testing"
	"weak"
)

func newRetainedRevisionFixture(t *testing.T, limits Limits, count int) (*Service, *compilationRepository, []Revision) {
	t.Helper()
	compiler, err := NewCompiler(limits, nil)
	if err != nil {
		t.Fatal(err)
	}
	repository := &compilationRepository{revisions: map[string]Revision{}, reports: map[string]VerificationReport{}}
	service, err := NewService(compiler, repository)
	if err != nil {
		t.Fatal(err)
	}
	var revisions []Revision
	for index := range count {
		id := fmt.Sprintf("retention-%02d", index)
		value := fixtureValue(t, verifiedTextPackage(t, id))
		compiled, issues := compiler.Compile(value.Bytes())
		if err := IssuesError(issues); err != nil {
			t.Fatal(err)
		}
		revision := Revision{ProtocolID: id, Hash: compiled.Hash(), Definition: value}
		report := Verify(t.Context(), compiled)
		if err := IssuesError(CanActivate(compiled, report)); err != nil {
			t.Fatal(err)
		}
		repository.revisions[id], repository.reports[id] = revision, report
		revisions = append(revisions, revision)
	}
	return service, repository, revisions
}

func assertRetainedAccounting(t *testing.T, service *Service, order []string) {
	t.Helper()
	bytes := 0
	for _, key := range service.retainedOrder {
		compiled := service.retained[key]
		if compiled == nil {
			t.Fatal("retention order names an absent graph", key)
		}
		bytes += compiled.definition.Len()
	}
	if !reflect.DeepEqual(service.retainedOrder, order) || len(service.retained) != len(order) || bytes != service.retainedBytes {
		t.Fatal("inconsistent FIFO/entry/byte accounting", service.retainedOrder, order, bytes, service.retainedBytes)
	}
	if bytes > service.compiler.limits.BufferBytes || len(order) > service.compiler.limits.StateItems {
		t.Fatal("historical graphs exceeded the engine retention bounds")
	}
}

func TestLoadRevisionRetainsGraphButRereadsAuthority(t *testing.T) {
	service, repository, revisions := newRetainedRevisionFixture(t, DefaultLimits(), 1)
	revision := revisions[0]
	first, err := service.LoadRevision(t.Context(), revision.ProtocolID, revision.Hash)
	if err != nil {
		t.Fatal(err)
	}
	pointer := weak.Make(first)
	first = nil
	runtime.GC()
	reads, reports := repository.revisionReads, repository.reportReads
	for index := range 3 {
		current, err := service.LoadRevision(t.Context(), revision.ProtocolID, revision.Hash)
		if err != nil || current != pointer.Value() || current == nil {
			t.Fatal("repeated historical load lost its strongly retained graph", err)
		}
		if repository.revisionReads != reads+index+1 || repository.reportReads != reports+index+1 {
			t.Fatal("retention bypassed durable revision or evidence reads")
		}
	}
	assertRetainedAccounting(t, service, []string{revision.ProtocolID + "/" + revision.Hash})
}

func TestLoadRevisionRetentionBoundsAndOldestFirstEviction(t *testing.T) {
	for _, bound := range []string{"entries", "bytes"} {
		t.Run(bound, func(t *testing.T) {
			limits := DefaultLimits()
			capacity := len(verifiedTextPackage(t, "retention-00").Mapping.Samples)
			if bound == "entries" {
				limits.StateItems = capacity
			} else {
				capacity = 2
				size := fixtureValue(t, verifiedTextPackage(t, "retention-00")).Len()
				limits.BufferBytes = size*2 + size/2
			}
			service, _, revisions := newRetainedRevisionFixture(t, limits, 12)
			var order []string
			for index, revision := range revisions {
				if _, err := service.LoadRevision(t.Context(), revision.ProtocolID, revision.Hash); err != nil {
					t.Fatal(err)
				}
				order = append(order, revision.ProtocolID+"/"+revision.Hash)
				if len(order) > capacity {
					order = order[1:]
				}
				assertRetainedAccounting(t, service, order)
				if index == capacity-1 {
					// Hits must not turn the FIFO into an LRU.
					first := revisions[0]
					if _, err := service.LoadRevision(t.Context(), first.ProtocolID, first.Hash); err != nil {
						t.Fatal(err)
					}
					assertRetainedAccounting(t, service, order)
				}
			}
		})
	}
}

func TestLoadRevisionRetentionInvalidatedAfterDeletion(t *testing.T) {
	for _, path := range []string{"history-callback", "external-deletion"} {
		t.Run(path, func(t *testing.T) {
			service, repository, revisions := newRetainedRevisionFixture(t, DefaultLimits(), 2)
			for _, revision := range revisions {
				if _, err := service.LoadRevision(t.Context(), revision.ProtocolID, revision.Hash); err != nil {
					t.Fatal(err)
				}
			}
			first, remaining := revisions[0], revisions[1]
			key := first.ProtocolID + "/" + first.Hash
			if path == "history-callback" {
				failure := errors.New("deletion failed")
				if err := service.DeleteRetained(first.ProtocolID, first.Hash, func() error { return failure }); !errors.Is(err, failure) || service.retained[key] == nil {
					t.Fatal("failed durable deletion evicted a live revision", err)
				}
				if err := service.DeleteRetained(first.ProtocolID, first.Hash, func() error {
					delete(repository.revisions, first.ProtocolID)
					delete(repository.reports, first.ProtocolID)
					return nil
				}); err != nil || service.retained[key] != nil {
					t.Fatal("successful history deletion did not drop retention", err)
				}
			} else {
				delete(repository.revisions, first.ProtocolID)
			}
			if compiled, err := service.LoadRevision(t.Context(), first.ProtocolID, first.Hash); compiled != nil || !errors.Is(err, ErrNotFound) {
				t.Fatal("retention served a durably deleted revision", err)
			}
			assertRetainedAccounting(t, service, []string{remaining.ProtocolID + "/" + remaining.Hash})
		})
	}
}

func TestLoadRevisionRetentionCannotAuthorizeInvalidEvidence(t *testing.T) {
	for _, alteration := range []string{"failed", "compiler", "samples", "kind", "missing", "no-checks", "issues", "definition"} {
		t.Run(alteration, func(t *testing.T) {
			service, repository, revisions := newRetainedRevisionFixture(t, DefaultLimits(), 1)
			revision := revisions[0]
			if _, err := service.LoadRevision(t.Context(), revision.ProtocolID, revision.Hash); err != nil {
				t.Fatal(err)
			}
			report := repository.reports[revision.ProtocolID]
			switch alteration {
			case "failed":
				report.Passed = false
			case "compiler":
				report.CompilerVersion = "stale"
			case "samples":
				report.SamplesHash = "stale"
			case "kind":
				report.Kind = UpstreamVerification
			case "missing":
				repository.isReportMissing = true
			case "no-checks":
				report.Checks = nil
			case "issues":
				report.Issues = []ConversionIssue{{Severity: SeverityError, Code: VerificationMismatch}}
			case "definition":
				changed := revision
				changed.Definition = fixtureValue(t, verifiedTextPackage(t, "changed-revision"))
				repository.revisions[revision.ProtocolID] = changed
			}
			repository.reports[revision.ProtocolID] = report
			if compiled, err := service.LoadRevision(t.Context(), revision.ProtocolID, revision.Hash); compiled != nil || err == nil {
				t.Fatal("retained graph authorized invalid durable evidence")
			}
			assertRetainedAccounting(t, service, []string{})
			// Removing invalid evidence must not permanently poison graph reuse.
			repository.revisions[revision.ProtocolID] = revision
			repository.reports[revision.ProtocolID] = Verify(t.Context(), mustCompileRetainedRevision(t, service, revision))
			repository.isReportMissing = false
			if _, err := service.LoadRevision(t.Context(), revision.ProtocolID, revision.Hash); err != nil {
				t.Fatal("restored durable evidence could not reload", err)
			}
		})
	}
}

func mustCompileRetainedRevision(t *testing.T, service *Service, revision Revision) *Compiled {
	t.Helper()
	compiled, issues := service.Validate(revision.Definition.Bytes())
	if err := IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	return compiled
}
