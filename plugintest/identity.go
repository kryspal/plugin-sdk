package plugintest

import (
	"context"
	"net/url"
	"slices"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/kryspal/plugin-sdk/pluginv1"
)

// nobodySubject is a subject no network sends; a plugin must never answer PRESENT for it.
const nobodySubject = "plugintest-nobody"

// checkIdentities calls NetworkIdentity.CheckIdentities the way the host does (PROTOCOL.md §7.14): with the subjects
// the plugin sent on the ingress plus one nobody has, and with an empty list. The service is optional: UNIMPLEMENTED
// passes. Otherwise every subject asked must be answered exactly once, with a set presence, within
// CheckIdentitiesTimeout, and the made-up subject must not be PRESENT. What a subject's presence is depends on the
// plugin's network, so the suite does not check it beyond that.
func checkIdentities(ctx context.Context, t *testing.T, conn *grpc.ClientConn, sent []string) {
	t.Helper()
	client := pluginv1.NewNetworkIdentityClient(conn)
	subjects := append(slices.Clone(sent), nobodySubject)
	answers, ok := askIdentities(ctx, t, client, subjects)
	if !ok {
		return
	}
	for _, problem := range identityAnswerProblems(subjects, answers) {
		t.Error(problem)
	}
	if empty, ok := askIdentities(ctx, t, client, nil); ok && len(empty) > 0 {
		t.Errorf("CheckIdentities with no subjects answered %d results, want none", len(empty))
	}
}

// askIdentities makes one call; ok is false when the call failed or the plugin does not implement the service.
func askIdentities(ctx context.Context, t *testing.T, client pluginv1.NetworkIdentityClient, subjects []string) ([]*pluginv1.IdentityPresence, bool) {
	t.Helper()
	cctx, cancel := context.WithTimeout(ctx, pluginv1.CheckIdentitiesTimeout)
	defer cancel()
	began := time.Now()
	resp, err := client.CheckIdentities(cctx, &pluginv1.CheckIdentitiesRequest{Subjects: subjects})
	switch code := status.Code(err); {
	case err == nil:
		return resp.GetResults(), true
	case code == codes.Unimplemented:
		t.Log("NetworkIdentity.CheckIdentities is not implemented: the host treats every subject as UNKNOWN and keeps bindings of people who are gone; implement it if your network can tell (PROTOCOL.md §7.14)")
	case code == codes.DeadlineExceeded:
		t.Errorf("CheckIdentities did not answer within %v (took %v); answer from the state you already hold", pluginv1.CheckIdentitiesTimeout, time.Since(began).Round(time.Millisecond))
	default:
		t.Errorf("CheckIdentities: %v; when unsure, answer PRESENCE_UNKNOWN instead of an error", err)
	}
	return nil, false
}

// identityAnswerProblems lists what is wrong with the answers to subjects.
func identityAnswerProblems(subjects []string, answers []*pluginv1.IdentityPresence) []string {
	var out []string
	count := map[string]int{}
	for _, a := range answers {
		s := a.GetSubject()
		count[s]++
		switch {
		case !slices.Contains(subjects, s):
			out = append(out, "CheckIdentities answered "+quote(s)+", which was not asked; the host ignores it")
		case a.GetPresence() == pluginv1.Presence_PRESENCE_UNSPECIFIED:
			out = append(out, "CheckIdentities answered PRESENCE_UNSPECIFIED for "+quote(s)+"; answer PRESENCE_UNKNOWN when unsure")
		case s == nobodySubject && a.GetPresence() == pluginv1.Presence_PRESENCE_PRESENT:
			out = append(out, "CheckIdentities answered PRESENCE_PRESENT for "+quote(s)+", a subject your network never sent")
		}
	}
	for _, s := range subjects {
		switch n := count[s]; {
		case n == 0:
			out = append(out, "CheckIdentities left out "+quote(s)+"; answer every subject (PRESENCE_UNKNOWN when unsure)")
		case n > 1:
			out = append(out, "CheckIdentities answered "+quote(s)+" more than once; the host treats it as UNKNOWN")
		}
	}
	return out
}

// sentSubjects returns the distinct subjects in the identity headers of the ingress requests, decoded as the host
// decodes them. The caller holds run.mu.
func sentSubjects(requests []ingressRequest) []string {
	var out []string
	for _, r := range requests {
		v := r.header.Get(pluginv1.HeaderIdentitySubject)
		if v == "" || v == forgedValue {
			continue
		}
		s, err := url.PathUnescape(v)
		if err != nil || s == "" || slices.Contains(out, s) {
			continue
		}
		out = append(out, s)
	}
	return out
}
