package coalition

import (
	"fmt"
	"testing"
)

func TestMatch(t *testing.T) {
	cases := []struct {
		ref, domain string
		want        int
	}{
		{
			ref:    "Coalition, Inc",
			domain: "coalitioninc.com",
			want:   50, // exact root phrase match
		},
		{
			ref:    "Coalition, Inc",
			domain: "emphatic.com",
			want:   0,
		},
		{
			ref:    "Coalition, Inc",
			domain: "colition.com",
			want:   5, // misspelled root phrase match
		},
		{
			ref:    "Coalition, Inc",
			domain: "coalition-rutabaga.com",
			want:   40, // exact root phrase but with significant affix
		},
		{
			ref:    "Coalition Security, Inc.",
			domain: "coalition.com",
			want:   5,
		},
	}

	matcher := NewMatcher()
	delete(matcher.Scores, testWebPageRef) // No network requests during unit tests.

	for i, c := range cases {
		t.Run(fmt.Sprintf("case_%02d", i+1), func(t *testing.T) {
			got, err := matcher.doMatch(c.ref, c.domain)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("got %d, want %d", got, c.want)
			}
		})
	}
}
