package plugintest

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	pluginv1 "github.com/kryspal/plugin-sdk/pluginv1"
)

func TestIdentityAnswerProblems(t *testing.T) {
	p := func(s string, v pluginv1.Presence) *pluginv1.IdentityPresence {
		return &pluginv1.IdentityPresence{Subject: s, Presence: v}
	}
	const (
		present = pluginv1.Presence_PRESENCE_PRESENT
		absent  = pluginv1.Presence_PRESENCE_ABSENT
		unknown = pluginv1.Presence_PRESENCE_UNKNOWN
	)
	asked := []string{"u1", nobodySubject}
	cases := map[string]struct {
		answers []*pluginv1.IdentityPresence
		want    string // a fragment of the one problem; empty for none
	}{
		"fine":            {[]*pluginv1.IdentityPresence{p("u1", present), p(nobodySubject, absent)}, ""},
		"unknown is fine": {[]*pluginv1.IdentityPresence{p(nobodySubject, unknown), p("u1", unknown)}, ""},
		"left out":        {[]*pluginv1.IdentityPresence{p("u1", present)}, `left out "plugintest-nobody"`},
		"twice":           {[]*pluginv1.IdentityPresence{p("u1", present), p("u1", present), p(nobodySubject, absent)}, `"u1" more than once`},
		"not asked":       {[]*pluginv1.IdentityPresence{p("u1", present), p(nobodySubject, absent), p("x", absent)}, `"x", which was not asked`},
		"unspecified":     {[]*pluginv1.IdentityPresence{p("u1", 0), p(nobodySubject, absent)}, `PRESENCE_UNSPECIFIED for "u1"`},
		"nobody present":  {[]*pluginv1.IdentityPresence{p("u1", present), p(nobodySubject, present)}, `PRESENCE_PRESENT for "plugintest-nobody"`},
	}
	for name, c := range cases {
		got := identityAnswerProblems(asked, c.answers)
		switch {
		case c.want == "" && len(got) > 0:
			t.Errorf("%s: problems %q", name, got)
		case c.want != "" && (len(got) != 1 || !strings.Contains(got[0], c.want)):
			t.Errorf("%s: problems %q, want one with %q", name, got, c.want)
		}
	}
}

// The subjects come from the identity headers, decoded as the host decodes them; the suite's forged value and
// repeats are left out.
func TestSentSubjects(t *testing.T) {
	req := func(subject string) ingressRequest {
		h := http.Header{}
		if subject != "" {
			h.Set(pluginv1.HeaderIdentitySubject, subject)
		}
		return ingressRequest{header: h}
	}
	got := sentSubjects([]ingressRequest{req("u%201"), req(""), req(forgedValue), req("u%201"), req("u2"), req("%zz")})
	if want := []string{"u 1", "u2"}; !slices.Equal(got, want) {
		t.Errorf("sent subjects %q, want %q", got, want)
	}
}
