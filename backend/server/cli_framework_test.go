package server

import (
	"fmt"
	"strings"
	"testing"

	"github.com/elysia-api/backend/agent"
)

func TestCLIShellSubset(t *testing.T) {
	t.Parallel()
	for _, script := range []string{
		`elysia group create --name $(touch /tmp/unwanted)`,
		"elysia group create --name `uname`",
		`elysia group create --name "$HOME"`,
		`elysia group create --name $((1+1))`,
		`NAME=value elysia source ls`,
		`elysia source ls > file`,
		`elysia source ls < file`,
		"elysia source ls <<EOF\ntext\nEOF",
		`elysia source ls || elysia group ls`,
		`! elysia source ls`,
		`(elysia source ls)`,
		`{ elysia source ls; }`,
		`for x in a; do elysia source ls; done`,
		`elysia source ls &`,
		`elysia source ls |& head 1`,
		`elysia source ls |`,
		`elysia source ls &&`,
		"elysia group create --name \x00ignored",
	} {
		t.Run(script, func(t *testing.T) {
			segments, err := cliSplitStatements(script)
			if err == nil {
				for _, segment := range segments {
					if _, parseErr := cliParseStatement(segment.raw); parseErr != nil {
						err = parseErr
						break
					}
				}
			}
			if err == nil {
				t.Fatal("unsupported shell syntax accepted")
			}
			if notes, err := probeAgentCLI(script); err == nil || len(notes) != 0 {
				t.Fatalf("probe disagrees: %v %v", notes, err)
			}
		})
	}
	for _, tc := range []struct{ input, want string }{
		{`hello\ world`, "hello world"},
		{`pre' middle '"post"`, "pre middle post"},
		{`'literal $HOME $(whoami) > * ~'`, "literal $HOME $(whoami) > * ~"},
		{`"escaped \$HOME \` + "`" + ` \\ \""`, "escaped $HOME ` \\ \""},
		{`'line one
line two'`, "line one\nline two"},
		{`~literal/*/{a,b}`, "~literal/*/{a,b}"},
	} {
		inv := resolveCLI(t, "elysia group create --name "+tc.input)
		if inv.Str("name") != tc.want {
			t.Fatalf("%s: got %q want %q", tc.input, inv.Str("name"), tc.want)
		}
	}
	segments, err := cliSplitStatements("# intro\nelysia source ls && # continue\nelysia group ls; elysia key ls # tail")
	if err != nil || len(segments) != 3 || !segments[0].mustSucceed || !segments[1].fromAnd || segments[2].fromAnd {
		t.Fatalf("comments and chain: %+v %v", segments, err)
	}
	inv := resolveCLI(t, "elysia group create \\\n --name continued")
	if inv.Str("name") != "continued" {
		t.Fatalf("line continuation: %v", inv.flags)
	}
}

func TestCLICobraHelpAndApprovalAgree(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"help", "group", "create"},
		{"group", "create", "--help"},
		{"group", "create", "-h"},
		{"group", "create", "--name", "unused", "--help=true"},
		{"group", "member"},
	} {
		inv, help, err := cliResolveRequest(args)
		if err != nil || inv != nil || !strings.Contains(help, "Usage:") {
			t.Fatalf("help %v: inv=%v help=%s err=%v", args, inv, help, err)
		}
		notes, err := probeAgentCLI("elysia " + strings.Join(args, " "))
		if err != nil || len(notes) != 0 {
			t.Fatalf("help prompted for approval: %+v %v", notes, err)
		}
	}
	for _, script := range []string{
		"elysia group create --name real --help=false",
		"elysia group create --name '--help'",
	} {
		notes, err := probeAgentCLI(script)
		if err != nil || len(notes) != 1 || notes[0].PermissionKey != agent.PermissionKeySave {
			t.Fatalf("write escaped approval: %+v %v", notes, err)
		}
	}
	inv := resolveCLI(t, "elysia usage log -- --help")
	if len(inv.args) != 1 || inv.args[0] != "--help" {
		t.Fatal("-- did not terminate flags")
	}
	// Request-local trees must not carry Changed/value/help state between calls.
	for i := range 16 {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			t.Parallel()
			params := mapCLI(t, fmt.Sprintf("elysia key update --name key%d --enabled=no --allowed-groups a --allowed-groups b,c --new-secret ''", i))
			if params["enabled"] != false || params["newSecret"] != "" || len(params["allowedGroups"].([]string)) != 3 {
				t.Fatalf("values: %v", params)
			}
			clean := resolveCLI(t, "elysia key update --name clean")
			if clean.Has("enabled") || clean.Has("new-secret") || clean.Has("allowed-groups") {
				t.Fatalf("flag state leaked: %v", clean.flags)
			}
		})
	}
}

func TestCLIShellCredentialsRedacted(t *testing.T) {
	t.Parallel()
	for _, script := range []string{
		`elysia key create --name x --secret "credential\" marker"`,
		`elysia key create --name x --secret credential\ marker`,
		`elysia key create --name x --secret=credential' marker'`,
		`elysia key create --name x '--secret' 'credential marker'`,
		"elysia key create --name x --secret 'credential\nmarker'",
		`elysia key create --name x --secret 'credential marker`,
		`elysia key create --name x --secret "credential$HOME"`,
	} {
		for _, masked := range []string{redactCLICommandLine(script), agent.RedactCommandLine(script)} {
			if strings.Contains(masked, "credential") || strings.Contains(masked, "marker") {
				t.Fatalf("credential fragment exposed: %s", masked)
			}
		}
	}
}

func TestCLICobraHelpDoesNotWrite(t *testing.T) {
	s := newAgentIntegrationServer(t)
	result := s.runCLI(t.Context(), &opsTestContext{}, "elysia group create --name unused --help | head 3; elysia group create --name actual")
	if !result.OK {
		t.Fatalf("help batch failed: %+v", result)
	}
	groups, err := s.store.ListGroups(t.Context())
	if err != nil || len(groups) != 1 || groups[0].Name != "actual" {
		t.Fatalf("help executed a write: %+v %v", groups, err)
	}
}
