package agent

import "testing"

func TestMaskSecretTextOverlappingSpans(t *testing.T) {
	for _, test := range []struct {
		text    string
		secrets []string
		want    string
	}{
		{"review-secret-tail", []string{"review-secret", "review-secret-tail"}, "***"},
		{"xabcdefy", []string{"abcd", "cdef"}, "x***y"},
		{"ababa", []string{"aba"}, "***"},
		{"one + two", []string{"", "two", "one"}, "*** + ***"},
	} {
		if got := MaskSecretText(test.text, test.secrets); got != test.want {
			t.Fatalf("got %q, want %q", got, test.want)
		}
	}
}
