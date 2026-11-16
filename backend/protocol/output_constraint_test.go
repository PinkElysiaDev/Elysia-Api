package protocol

import (
	"errors"
	"testing"
)

func TestOutputConstraintIntentValidation(t *testing.T) {
	for _, test := range []struct {
		value string
		path  string
	}{
		{`null`, outputConstraintPath},
		{`{}`, outputConstraintPath + "/version"},
		{`{"version":1,"kind":"json","enforcement":null}`, outputConstraintPath + "/enforcement"},
		{`{"version":1,"kind":"json","enforcement":"preferred"}`, outputConstraintPath + "/enforcement"},
		{`{"version":1,"kind":"json","enforcement":"required","schema":{}}`, outputConstraintPath + "/schema"},
		{`{"version":1,"kind":"json-schema","enforcement":"required","dialect":"future","schema":{}}`, outputConstraintPath + "/dialect"},
		{`{"version":1,"kind":"json-schema","enforcement":"required","dialect":"json-schema","schema":null}`, outputConstraintPath + "/schema"},
		{`{"version":1,"kind":"json-schema","enforcement":"required","dialect":"json-schema","schema":{},"name":null}`, outputConstraintPath + "/name"},
		{`{"version":1,"kind":"json","enforcement":"required","unknown/member":false}`, outputConstraintPath + "/unknown~1member"},
	} {
		t.Run(test.value, func(t *testing.T) {
			err := validateRequestContext(&Request{Parameters: Object{"outputConstraint": Value{raw: test.value}}})
			var failure *ConversionError
			if !errors.As(err, &failure) || len(failure.Issues) != 1 || failure.Issues[0].Code != InvalidInput || failure.Issues[0].Path != test.path {
				t.Fatal("invalid output constraint lost its core boundary", err)
			}
		})
	}
	for _, raw := range []string{
		`{"version":1,"kind":"text","enforcement":"required"}`,
		`{"version":1,"kind":"json","enforcement":"required"}`,
		`{"version":1,"kind":"json-object","enforcement":"required"}`,
		`{"version":1,"kind":"json-schema","enforcement":"preferred","dialect":"json-schema","schema":{"type":"integer","minimum":9007199254740993}}`,
	} {
		if err := validateOutputConstraint(Value{raw: raw}); err != nil {
			t.Fatal("core validation took over target schema expressibility", raw, err)
		}
	}
	if err := validateOutputConstraint(Value{}); err != nil {
		t.Fatal("missing intent became an explicit format", err)
	}
}
