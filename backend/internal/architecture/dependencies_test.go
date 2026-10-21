package architecture_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Internal dependency allowlists describe architectural boundaries, not files or
// symbol names. A new dependency must be reviewed together with this contract.
func TestPackageBoundaries(t *testing.T) {
	allowed := map[string][]string{
		"server":                 {"agent", "config", "internal/admission", "internal/atomicfile", "internal/clishell", "internal/eventstream", "internal/jsonutil", "internal/outbound", "internal/routing", "logging", "protocol", "protocol/builtin", "relay", "storage", "webui"},
		"config":                 {"internal/atomicfile", "internal/dbpath", "internal/outbound", "logging", "protocol"},
		"agent":                  {"internal/clishell", "internal/jsonutil", "logging", "protocol"},
		"protocol":               {"internal/eventstream", "internal/jsonutil"},
		"protocol/builtin":       {"protocol"},
		"relay":                  {"internal/outbound", "protocol", "protocol/builtin"},
		"storage":                {"agent", "internal/atomicfile", "internal/dbpath", "internal/storagequery", "logging", "protocol", "protocol/builtin"},
		"internal/routing":       {"config", "storage"},
		"internal/admission":     {},
		"internal/semanticmeter": {"protocol"},
		"internal/outbound":      {"logging"},
		"internal/dbpath":        {}, "internal/atomicfile": {}, "internal/clishell": {},
		"internal/eventstream": {}, "internal/jsonutil": {}, "internal/storagequery": {},
		"logging": {}, "webui": {},
	}
	// Background components have narrower boundaries than the application
	// adapters in the same package. Keep assembly and wire adapters separate.
	workers := map[string]bool{"health_checker.go": true, "usage_retention.go": true, "refresh_jobs.go": true}
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && (entry.Name() == "testdata" || strings.HasPrefix(entry.Name(), ".") || strings.HasPrefix(entry.Name(), "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		pkg, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		pkg = filepath.ToSlash(pkg)
		if _, constrained := allowed[pkg]; !constrained && pkg != "." {
			t.Errorf("%s: declare dependency boundaries for package %s", path, pkg)
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		imports := make(map[string]string)
		worker := pkg == "server" && workers[filepath.Base(path)]
		transportIndependent := pkg == "server" && (filepath.Base(path) == "continuation.go" || filepath.Base(path) == "a2a_server.go")
		for _, spec := range file.Imports {
			dependency, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			name := filepath.Base(dependency)
			if spec.Name != nil {
				name = spec.Name.Name
			}
			imports[name] = dependency
			if local, ok := strings.CutPrefix(dependency, "github.com/elysia-api/backend/"); ok {
				if boundaries, constrained := allowed[pkg]; constrained && !slices.Contains(boundaries, local) {
					t.Errorf("%s: %s must not depend on %s", path, pkg, local)
				}
			}
			if pkg != "." && dependency == "flag" {
				t.Errorf("%s: command-line parsing belongs to main", path)
			}
			if (pkg != "server" || worker || transportIndependent) && (strings.HasPrefix(dependency, "github.com/cloudwego/hertz/") || strings.HasPrefix(dependency, "github.com/hertz-contrib/")) {
				t.Errorf("%s: Hertz belongs to the HTTP adapter", path)
			}
			if strings.HasPrefix(dependency, "github.com/gin-gonic/") || strings.HasPrefix(dependency, "github.com/gin-contrib/") || dependency == "github.com/cloudwego/hertz/pkg/common/adaptor" {
				t.Errorf("%s: HTTP adapters must use native Hertz", path)
			}
			if pkg == "server" && dependency == "database/sql" {
				t.Errorf("%s: SQL belongs to storage", path)
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			if name, ok := node.(*ast.Ident); ok && worker && name.Name == "Server" {
				t.Errorf("%s: background components receive explicit dependencies, not Server", path)
			}
			if fn, ok := node.(*ast.FuncDecl); ok && pkg == "config" && fn.Recv == nil && fn.Name.Name == "init" {
				t.Errorf("%s: configuration must be loaded explicitly", path)
			}
			if selector, ok := node.(*ast.SelectorExpr); ok && pkg != "." && selector.Sel.Name == "Exit" {
				if name, ok := selector.X.(*ast.Ident); ok && imports[name.Name] == "os" {
					t.Errorf("%s: libraries return errors; main owns process exit", path)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
