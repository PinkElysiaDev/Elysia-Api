package protocol

import "testing"

func TestOutputConstraintRawAndTypedEntryPoints(t *testing.T) {
	compiled := compilePackageForTest(t, textProtocolPackage(t))
	for _, test := range []struct {
		constraint string
		path       string
	}{
		{`null`, outputConstraintPath},
		{`{"version":1,"kind":"future","enforcement":"required"}`, outputConstraintPath + "/kind"},
		{`{"version":1,"kind":"json-schema","enforcement":"required","dialect":"json-schema","schema":null}`, outputConstraintPath + "/schema"},
	} {
		t.Run(test.constraint, func(t *testing.T) {
			request, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","prompt":"hello"}`), EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			request.Native = nil
			request.Parameters = Object{"outputConstraint": Value{raw: test.constraint}}
			_, err = compiled.EncodeRequestValue(t.Context(), request, EvaluationContext{})
			assertPackageRuntimeIssue(t, err, InvalidInput, test.path)
			_, issues := compiled.Execute(t.Context(), EncodeRequest, fixtureValue(t, request), EvaluationContext{})
			assertPackageRuntimeIssue(t, IssuesError(issues), InvalidInput, test.path)
		})
	}
}
