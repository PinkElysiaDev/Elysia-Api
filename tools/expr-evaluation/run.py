#!/usr/bin/env python3
"""Run the isolated Expr experiment against real, private protocol internals."""
import json
import pathlib
import shutil
import subprocess
import sys
import tempfile

here = pathlib.Path(__file__).resolve().parent
backend = here.parents[1] / "backend"
temporary_root = pathlib.Path("/tmp/opencode")
with tempfile.TemporaryDirectory(prefix="expr-evaluation-", dir=temporary_root if temporary_root.exists() else None) as directory:
    temporary = pathlib.Path(directory)
    module = temporary / "evaluation.mod"
    shutil.copyfile(backend / "go.mod", module)
    shutil.copyfile(backend / "go.sum", temporary / "evaluation.sum")
    overlay = temporary / "overlay.json"
    overlay.write_text(json.dumps({"Replace": {
        str(backend / "protocol" / "expr_evaluation_test.go"): str(here / "prototype_test.go.txt")
    }}))
    def go(*args):
        subprocess.run(["go", *args], cwd=backend, check=True)
    go("mod", "edit", f"-modfile={module}", "-require=github.com/expr-lang/expr@v1.17.8")
    go("mod", "download", f"-modfile={module}", "github.com/expr-lang/expr")
    go("test", f"-modfile={module}", f"-overlay={overlay}", "-tags=sonic", "./protocol",
       *(sys.argv[1:] or ["-run", "^TestExprEvaluation", "-count=1", "-timeout=2m"]))
