package protocol

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

const compilationPublicationTimeout = 30 * time.Second

type compilationRepository struct {
	Repository
	revision              Revision
	report                VerificationReport
	activation            Activation
	additionalActivations []Activation
	revisions             map[string]Revision
	reports               map[string]VerificationReport
	isReportMissing       bool
	beforeReport          func(context.Context, string) error
	revisionReads         int
	reportReads           int
}

func (repository *compilationRepository) ListProtocolActivations(ctx context.Context) ([]Activation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append([]Activation{repository.activation}, repository.additionalActivations...), nil
}

func (repository *compilationRepository) ReadProtocolRevision(ctx context.Context, id, hash string) (Revision, error) {
	repository.revisionReads++
	if err := ctx.Err(); err != nil {
		return Revision{}, err
	}
	revision := repository.revision
	if id != revision.ProtocolID {
		revision = repository.revisions[id]
	}
	if id != revision.ProtocolID || hash != revision.Hash {
		return Revision{}, ErrNotFound
	}
	return revision, nil
}

func (repository *compilationRepository) ReadProtocolReport(ctx context.Context, id, hash string) (VerificationReport, error) {
	repository.reportReads++
	if err := ctx.Err(); err != nil {
		return VerificationReport{}, err
	}
	if repository.beforeReport != nil {
		if err := repository.beforeReport(ctx, id); err != nil {
			return VerificationReport{}, err
		}
	}
	revision, report := repository.revision, repository.report
	if id != revision.ProtocolID {
		revision, report = repository.revisions[id], repository.reports[id]
	}
	if repository.isReportMissing || id != revision.ProtocolID || hash != revision.Hash {
		return VerificationReport{}, ErrNotFound
	}
	return report, nil
}

func newCompilationSessionFixture(t *testing.T) (*Service, *compilationRepository, Value) {
	t.Helper()
	document := verifiedTextPackage(t, PresetAnthropicID)
	value := fixtureValue(t, document)
	compiler, err := NewCompiler(DefaultLimits(), nil)
	if err != nil {
		t.Fatal(err)
	}
	repository := &compilationRepository{}
	service, err := NewService(compiler, repository)
	if err != nil {
		t.Fatal(err)
	}
	compiled, issues := service.Validate(value.Bytes())
	if err := IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	repository.revision = Revision{ProtocolID: document.Spec.ID, Hash: compiled.Hash(), Definition: value}
	repository.report = Verify(t.Context(), compiled)
	if err := IssuesError(CanActivate(compiled, repository.report)); err != nil {
		t.Fatal(err)
	}
	repository.activation = Activation{ProtocolID: document.Spec.ID, RevisionHash: compiled.Hash()}
	return service, repository, value
}

func TestCompilationSessionRequiresExactAdmittedSource(t *testing.T) {
	service, _, value := newCompilationSessionFixture(t)
	session := service.NewCompilationSession()
	defer session.Close()
	first, issues := session.CompilePreset(PresetAnthropicID, value)
	if err := IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	repeated, issues := session.Compile(value)
	if IssuesError(issues) != nil || repeated != first {
		t.Fatal("exact admitted source was recompiled", issues)
	}
	spelled, err := ParseValue([]byte(" " + value.JSONString()))
	if err != nil {
		t.Fatal(err)
	}
	second, issues := session.Compile(spelled)
	third, otherIssues := session.Compile(spelled)
	if IssuesError(issues) != nil || IssuesError(otherIssues) != nil || second == first || second != third || second.Hash() != first.Hash() {
		t.Fatal("source spelling isolation or exact-key reuse failed", issues, otherIssues)
	}
	wrongIdentity := service.NewCompilationSession()
	defer wrongIdentity.Close()
	unadmitted, issues := wrongIdentity.CompilePreset(PresetGeminiID, value)
	recompiled, otherIssues := wrongIdentity.Compile(value)
	if IssuesError(issues) != nil || IssuesError(otherIssues) != nil || unadmitted != recompiled || len(wrongIdentity.presets) != 0 {
		t.Fatal("a mismatched preset identity entered reuse")
	}
	oversized, err := ParseValue([]byte(strings.Repeat(" ", DefaultLimits().BufferBytes) + value.JSONString()))
	if err != nil {
		t.Fatal(err)
	}
	if compiled, issues := session.Compile(oversized); compiled != nil || IssuesError(issues) == nil {
		t.Fatal("equivalent source bypassed raw input limits")
	}
}

func TestCompilationSessionRetainsFirstSourcePerPreset(t *testing.T) {
	service, _, value := newCompilationSessionFixture(t)
	session := service.NewCompilationSession()
	defer session.Close()
	first, issues := session.CompilePreset(PresetAnthropicID, value)
	if err := IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{" ", "\n", "\t"} {
		spelled, err := ParseValue([]byte(prefix + value.JSONString()))
		if err != nil {
			t.Fatal(err)
		}
		second, issues := session.CompilePreset(PresetAnthropicID, spelled)
		third, otherIssues := session.CompilePreset(PresetAnthropicID, spelled)
		if IssuesError(issues) != nil || IssuesError(otherIssues) != nil || second == first || second != third || second.Hash() != first.Hash() {
			t.Fatal("later preset spelling isolation or exact-key reuse failed", issues, otherIssues)
		}
		admitted := session.presets[PresetAnthropicID]
		if len(session.presets) != 1 || admitted.source != value || admitted.compiled != first {
			t.Fatal("later preset spellings expanded or replaced the first admission")
		}
	}
	retained, issues := session.Compile(value)
	if IssuesError(issues) != nil || retained != first {
		t.Fatal("the first admitted source lost reuse", issues)
	}
}

func TestCompilationSessionDoesNotAdmitFailures(t *testing.T) {
	service, _, value := newCompilationSessionFixture(t)
	session := service.NewCompilationSession()
	defer session.Close()
	document := textProtocolPackage(t)
	document.Spec.ID = PresetAnthropicID
	document.Mapping.DSLVersion = 0
	invalid := fixtureValue(t, document)
	for range 2 {
		compiled, issues := session.CompilePreset(PresetAnthropicID, invalid)
		if compiled != nil || IssuesError(issues) == nil || len(session.presets) != 0 {
			t.Fatal("a failed compilation entered the session", issues)
		}
	}
	corrected, issues := session.CompilePreset(PresetAnthropicID, value)
	repeated, otherIssues := session.Compile(value)
	if IssuesError(issues) != nil || IssuesError(otherIssues) != nil || corrected != repeated || len(session.presets) != 1 {
		t.Fatal("an earlier failure prevented the corrected source from being admitted", issues, otherIssues)
	}
}

func TestCompilationSessionPublicationRereadsAuthority(t *testing.T) {
	for _, change := range []string{"none", "spelling", "failed-report", "stale-report", "missing-report", "stale-samples", "upstream-report", "required-absent", "corrupt-identity", "canceled"} {
		t.Run(change, func(t *testing.T) {
			service, repository, value := newCompilationSessionFixture(t)
			if err := service.Reload(t.Context()); err != nil {
				t.Fatal(err)
			}
			previous, _ := service.Pin(PresetAnthropicID)
			session := service.NewCompilationSession()
			defer session.Close()
			prepared, issues := session.CompilePreset(PresetAnthropicID, value)
			if err := IssuesError(issues); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			requiredIDs := []string{PresetAnthropicID}
			switch change {
			case "spelling":
				repository.revision.Definition, _ = ParseValue([]byte(" " + value.JSONString()))
			case "failed-report":
				repository.report.Passed = false
			case "stale-report":
				repository.report.CompilerVersion = "stale"
			case "missing-report":
				repository.isReportMissing = true
			case "stale-samples":
				repository.report.SamplesHash = "stale"
			case "upstream-report":
				repository.report.Kind = UpstreamVerification
			case "required-absent":
				requiredIDs = append(requiredIDs, PresetGeminiID)
			case "corrupt-identity":
				document := textProtocolPackage(t)
				document.Spec.ID = "changed-id"
				repository.revision.Definition = fixtureValue(t, document)
			case "canceled":
				cancel()
			}
			revisionReads, reportReads := repository.revisionReads, repository.reportReads
			err := session.ReloadAvailable(ctx, requiredIDs...)
			published, _ := service.Pin(PresetAnthropicID)
			if change == "none" || change == "spelling" {
				if err != nil || (published == previous) != (change == "none") || repository.revisionReads != revisionReads+1 || repository.reportReads != reportReads+1 {
					t.Fatal("publication skipped fresh authority or failed", err)
				}
				if (published == prepared) != (change == "none") {
					t.Fatal("reuse did not require exact reread source bytes")
				}
			} else if err == nil || published != previous {
				t.Fatal("failed publication replaced the pinned snapshot", err)
			}
			if change == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("repository cancellation was lost", err)
			}
			if change == "missing-report" && !errors.Is(err, ErrNotFound) {
				t.Fatal("missing report identity was lost", err)
			}
			if change == "required-absent" && !strings.Contains(err.Error(), "required preset is not available") {
				t.Fatal("missing required preset was not identified", err)
			}
			if !session.isClosed || session.presets != nil || session.service != nil {
				t.Fatal("publication retained the preparation session")
			}
			if err := session.ReloadAvailable(t.Context(), PresetAnthropicID); err == nil {
				t.Fatal("session was reused across publication attempts")
			}
		})
	}
}

func TestCompilationSessionPublicationIsolatesInvalidCustom(t *testing.T) {
	const customID = "invalid-custom"
	service, repository, value := newCompilationSessionFixture(t)
	if err := service.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	session := service.NewCompilationSession()
	defer session.Close()
	prepared, issues := session.CompilePreset(PresetAnthropicID, value)
	if err := IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	document := textProtocolPackage(t)
	document.Spec.ID = customID
	document.Mapping.DSLVersion = 0
	custom := fixtureValue(t, document)
	revision := Revision{ProtocolID: customID, Hash: hashValue(custom), Definition: custom}
	repository.revisions = map[string]Revision{customID: revision}
	repository.additionalActivations = []Activation{{ProtocolID: customID, RevisionHash: revision.Hash}}
	if err := session.ReloadAvailable(t.Context(), PresetAnthropicID); err != nil {
		t.Fatal("invalid custom revision blocked a verified preset", err)
	}
	view := service.View()
	published, hasPreset := view.Pin(PresetAnthropicID)
	_, hasCustom := view.Pin(customID)
	failures := view.Failures()
	if !hasPreset || published != prepared || hasCustom || len(failures) != 1 || failures[customID] == "" {
		t.Fatal("publication did not isolate only the invalid custom revision", failures)
	}
}

func TestCompilationSessionPublicationKeepsViewsAtomic(t *testing.T) {
	service, repository, value := newCompilationSessionFixture(t)
	if err := service.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	previousView := service.View()
	previous, _ := previousView.Pin(PresetAnthropicID)
	session := service.NewCompilationSession()
	defer session.Close()
	ctx, cancel := context.WithTimeout(t.Context(), compilationPublicationTimeout)
	defer cancel()
	prepared, issues := session.CompilePreset(PresetAnthropicID, value)
	if err := IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	document := verifiedTextPackage(t, PresetGeminiID)
	additional := fixtureValue(t, document)
	added, issues := session.CompilePreset(PresetGeminiID, additional)
	if err := IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	report := Verify(ctx, added)
	if err := IssuesError(CanActivate(added, report)); err != nil {
		t.Fatal(err)
	}
	repository.revisions = map[string]Revision{PresetGeminiID: {ProtocolID: PresetGeminiID, Hash: added.Hash(), Definition: additional}}
	repository.reports = map[string]VerificationReport{PresetGeminiID: report}
	repository.additionalActivations = []Activation{{ProtocolID: PresetGeminiID, RevisionHash: added.Hash()}}
	entered, release := make(chan struct{}), make(chan struct{})
	repository.beforeReport = func(ctx context.Context, id string) error {
		if id != PresetGeminiID {
			return nil
		}
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	publication := make(chan error, 1)
	go func() { publication <- session.ReloadAvailable(ctx, PresetAnthropicID, PresetGeminiID) }()
	select {
	case <-entered:
	case err := <-publication:
		t.Fatal("publication did not reach its second report read", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	readOld := make(chan struct{})
	observations := make(chan error, 1)
	go func() {
		observations <- observeCompilationPublication(ctx, service, previous, prepared, added, readOld)
	}()
	select {
	case <-readOld:
	case err := <-observations:
		t.Fatal("reader failed before publication", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	close(release)
	select {
	case err := <-publication:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case err := <-observations:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	retained, _ := previousView.Pin(PresetAnthropicID)
	_, hasAdded := previousView.Pin(PresetGeminiID)
	if retained != previous || hasAdded || len(previousView.IDs()) != 1 {
		t.Fatal("publication mutated an already captured view")
	}
}

func observeCompilationPublication(ctx context.Context, service *Service, previous, prepared, added *Compiled, readOld chan<- struct{}) error {
	hasReadOld := false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		view := service.View()
		first, hasFirst := view.Pin(PresetAnthropicID)
		second, hasSecond := view.Pin(PresetGeminiID)
		pinned, hasPinned := service.Pin(PresetAnthropicID)
		if !hasFirst || !hasPinned || (pinned != previous && pinned != prepared) {
			return fmt.Errorf("View or Pin exposed an absent or unknown preset")
		}
		if !hasSecond {
			if first != previous || len(view.IDs()) != 1 {
				return fmt.Errorf("old view included part of the new publication")
			}
			if !hasReadOld {
				hasReadOld = true
				close(readOld)
			}
			continue
		}
		if first != prepared || !hasSecond || second != added || len(view.IDs()) != 2 {
			return fmt.Errorf("new view exposed an incomplete publication")
		}
		if !hasReadOld {
			return fmt.Errorf("reader did not inspect the old view during publication")
		}
		return nil
	}
}

func TestCompilationSessionDoesNotCrossServiceLimits(t *testing.T) {
	service, repository, value := newCompilationSessionFixture(t)
	session := service.NewCompilationSession()
	defer session.Close()
	if _, issues := session.CompilePreset(PresetAnthropicID, value); IssuesError(issues) != nil {
		t.Fatal(issues)
	}
	limits := DefaultLimits()
	limits.BufferBytes = value.Len() - 1
	compiler, err := NewCompiler(limits, nil)
	if err != nil {
		t.Fatal(err)
	}
	limited, err := NewService(compiler, repository)
	if err != nil {
		t.Fatal(err)
	}
	other := limited.NewCompilationSession()
	defer other.Close()
	if compiled, issues := other.CompilePreset(PresetAnthropicID, value); compiled != nil || IssuesError(issues) == nil {
		t.Fatal("another service inherited a less restrictive compilation")
	}
}

func TestCompilationSessionCloseSerializesCompilation(t *testing.T) {
	service, _, value := newCompilationSessionFixture(t)
	session := service.NewCompilationSession()
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() { session.CompilePreset(PresetAnthropicID, value) })
	}
	session.Close()
	workers.Wait()
	if compiled, issues := session.Compile(value); compiled != nil || IssuesError(issues) == nil {
		t.Fatal("closed session admitted new work")
	}
}
