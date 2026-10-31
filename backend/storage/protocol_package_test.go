package storage

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func TestProtocolPackageRevisionLifecycle(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "packages.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	compiler, err := protocol.NewCompiler(protocol.DefaultLimits(), nil, []string{"tasks.async"})
	if err != nil {
		t.Fatal(err)
	}
	service, err := protocol.NewService(compiler, store)
	if err != nil {
		t.Fatal(err)
	}
	read := func(name string) protocol.Value {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join("..", "protocol", "testdata", "tasks", "job-alpha", name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		value, err := protocol.ParseValue(raw)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	document, err := protocol.EncodeValue(map[string]any{"schemaVersion": protocol.ProtocolPackageSchemaVersion, "spec": read("spec"), "mapping": read("mapping")})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.SaveDraft(t.Context(), "wrong-id", document.Bytes(), ""); err == nil {
		t.Fatal("package identity mismatch was saved")
	} else {
		var failure *protocol.ConversionError
		if !errors.As(err, &failure) || failure.Issues[0].Path != "/spec/id" {
			t.Fatal("package identity diagnostic did not locate spec", err)
		}
	}
	var draftHash, activeHash string
	var pinned *protocol.Compiled
	for _, change := range []protocol.Mutation{
		{Op: protocol.SetValue, Path: "/spec/version", Value: protocol.StringValue("1")},
		{Op: protocol.SetValue, Path: "/spec/version", Value: protocol.StringValue("2")},
		{Op: protocol.SetValue, Path: "/mapping/taskSamples/0/id", Value: protocol.StringValue("renamed-submit")},
	} {
		document, err = protocol.ApplyMutations(document, []protocol.Mutation{change}, protocol.DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		draft, issues, err := service.SaveDraft(t.Context(), "job-alpha", document.Bytes(), draftHash)
		if err != nil || protocol.IssuesError(issues) != nil {
			t.Fatal("package draft failed", err, issues)
		}
		if draft.Hash == draftHash {
			t.Fatal("source change did not replace the whole-package edit token")
		}
		if draftHash != "" {
			if _, _, err := service.VerifyDraft(t.Context(), "job-alpha", draftHash); !errors.Is(err, protocol.ErrRevisionConflict) {
				t.Fatal("stale source pair was verified", err)
			}
		}
		revision, report, err := service.VerifyDraft(t.Context(), "job-alpha", draft.Hash)
		if err != nil || !report.Passed {
			t.Fatal("package evidence failed", err, report.Issues)
		}
		fields, err := document.ReadObject()
		if err != nil {
			t.Fatal(err)
		}
		fromSources, issues := compiler.CompileSources(fields["spec"].Bytes(), fields["mapping"].Bytes())
		if protocol.IssuesError(issues) != nil || fromSources.Hash() != revision.Hash || fromSources.SamplesHash() != report.SamplesHash {
			t.Fatal("management and source compiler identities differ", issues)
		}
		if _, err := service.Activate(t.Context(), "job-alpha", revision.Hash, activeHash); err != nil {
			t.Fatal(err)
		}
		if pinned != nil && pinned.Hash() != activeHash {
			t.Fatal("activation changed the previously pinned package")
		}
		pinned, _ = service.Pin("job-alpha")
		draftHash, activeHash = draft.Hash, revision.Hash
	}
	service, err = protocol.NewService(compiler, store)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	if current, exists := service.Pin("job-alpha"); !exists || current.Hash() != activeHash {
		t.Fatal("reload lost the verified source pair")
	}
	baseline, err := store.ProtocolUpgradeBaseline(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RemoveActive("job-alpha", func() error {
		return store.ArchiveProtocol(t.Context(), "job-alpha", baseline, "", nil)
	}); err != nil {
		t.Fatal(err)
	}
	history, err := store.ListProtocolHistory(t.Context())
	if err != nil || len(history) == 0 {
		t.Fatal("package history missing", err)
	}
	for _, entry := range history {
		if entry.ReadError != "" || entry.Name != "job-alpha" || entry.Version == "" {
			t.Fatal("package history metadata lost its spec", entry)
		}
	}
}
