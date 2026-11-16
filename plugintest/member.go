package plugintest

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/kryspal/plugin-sdk"
	pluginv1 "github.com/kryspal/plugin-sdk/pluginv1"
)

// memberActionManifestProblems lists what the host would ignore in Manifest.page_actions and Manifest.subscription_actions
// (PROTOCOL.md §7.13).
func memberActionManifestProblems(m *pluginv1.Manifest) []string {
	var out []string
	if declares(m, pluginv1.CapabilityPageAction) && !declares(m, pluginv1.CapabilityDiscoverSource) {
		out = append(out, "the manifest declares page.action without discover.source; page actions apply to the plugin's own pages")
	}
	out = append(out, actionListProblems(m, pluginv1.CapabilityPageAction, "page_actions", m.GetPageActions(), pluginapi.MaxPageActions)...)
	out = append(out, actionListProblems(m, pluginv1.CapabilityDiscoverSubscriptions, "subscription_actions", m.GetSubscriptionActions(), pluginapi.MaxSubscriptionActions)...)
	if m.GetSubscriptionForm() && !declares(m, pluginv1.CapabilityDiscoverSubscriptions) {
		out = append(out, "the manifest sets subscription_form without discover.subscriptions; the host ignores it")
	}
	if slices.Contains(m.GetPermissions(), pluginv1.PermissionDiscoverLibraryFiles) && !declares(m, pluginv1.CapabilityWorkAction) {
		out = append(out, "the manifest declares the permission discover.library_files without work.action; only GetWorkStates carries library files")
	}
	return out
}

func actionListProblems(m *pluginv1.Manifest, capability, field string, acts []*pluginv1.ActionDeclaration, limit int) []string {
	var out []string
	switch {
	case capability == pluginv1.CapabilityPageAction && declares(m, capability) && len(acts) == 0:
		out = append(out, "the manifest declares page.action but lists no page_actions")
	case !declares(m, capability) && len(acts) > 0:
		out = append(out, "the manifest lists "+field+" but does not declare "+capability)
	case len(acts) > limit:
		out = append(out, "more "+field+" than the host uses")
	}
	seen := map[string]bool{}
	for _, a := range acts {
		if !pluginapi.ValidWorkIdentifier(a.GetId()) || seen[a.GetId()] {
			out = append(out, field+" id "+quote(a.GetId())+" is not valid or is repeated")
		}
		seen[a.GetId()] = true
		if a.GetLabel() == "" || len([]rune(a.GetLabel())) > pluginapi.MaxWorkActionLabel {
			out = append(out, field+" "+quote(a.GetId())+" needs a label of at most 48 characters")
		}
	}
	return out
}

func declaredIDs(acts []*pluginv1.ActionDeclaration) map[string]bool {
	out := map[string]bool{}
	for _, a := range acts {
		out[a.GetId()] = true
	}
	return out
}

// checkOffers checks the offers of a page state or a subscription against the declared actions.
func checkOffers(t *testing.T, where string, offers []*pluginv1.WorkActionOffer, declared map[string]bool, field string, limit int, searchable bool) {
	t.Helper()
	if len(offers) > limit {
		t.Errorf("%s: %d actions; the host keeps %d", where, len(offers), limit)
	}
	for _, o := range offers {
		if !declared[o.GetAction()] {
			t.Errorf("%s: action %q is not in %s; the host drops it", where, o.GetAction(), field)
		}
		checkOffer(t, where, o, searchable)
	}
}

// checkPageActions asks about the plugin's first tab. It runs no action: actions may change something outside the server.
func checkPageActions(ctx context.Context, t *testing.T, conn *grpc.ClientConn, m *pluginv1.Manifest) {
	t.Helper()
	info, err := call(ctx, lookupTimeout, func(cctx context.Context) (*pluginv1.DiscoverSourceInfo, error) {
		return pluginv1.NewDiscoverSourceClient(conn).DescribeDiscoverSource(cctx, &pluginv1.DescribeDiscoverSourceRequest{Languages: &pluginv1.Languages{Language: "en"}})
	})
	if err != nil {
		t.Fatalf("DescribeDiscoverSource: %v", err)
	}
	if len(info.GetTabs()) == 0 {
		t.Fatal("no page to ask about: the plugin's discover.source lists no tab")
	}
	ref := info.GetTabs()[0].GetRef()
	c := pluginv1.NewPageActionProviderClient(conn)
	declared := declaredIDs(m.GetPageActions())
	langs := &pluginv1.Languages{Language: "en"}
	st, err := call(ctx, lookupTimeout, func(cctx context.Context) (*pluginv1.PageState, error) {
		return c.GetPageState(cctx, &pluginv1.GetPageStateRequest{Ref: ref, Member: testMember, Languages: langs})
	})
	if err != nil {
		t.Fatalf("GetPageState(%q): %v", ref, err)
	}
	tooLong(t, "GetPageState status_text", st.GetStatusText(), pluginapi.MaxWorkStatusText)
	checkOffers(t, "GetPageState", st.GetActions(), declared, "page_actions", pluginapi.MaxPageOffers, false)
	_, err = call(ctx, lookupTimeout, func(cctx context.Context) (*pluginv1.PageState, error) {
		return c.GetPageState(cctx, &pluginv1.GetPageStateRequest{Ref: "plugintest:no-such-page", Member: testMember, Languages: langs})
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("GetPageState(unknown ref) = %v; want NOT_FOUND", err)
	}
}

// testMember is the member the member-facing checks ask for; the tool does not know it.
var testMember = &pluginv1.WorkMember{Id: "plugintest-member", Name: "plugintest"}

// checkSubscriptions lists the first page, checks each entry and asks for the next page. It runs no action.
func checkSubscriptions(ctx context.Context, t *testing.T, conn *grpc.ClientConn, m *pluginv1.Manifest) {
	t.Helper()
	c := pluginv1.NewMemberSubscriptionProviderClient(conn)
	declared := declaredIDs(m.GetSubscriptionActions())
	langs := &pluginv1.Languages{Language: "en"}
	list := func(token string) (*pluginv1.ListMemberSubscriptionsResponse, error) {
		return call(ctx, lookupTimeout, func(cctx context.Context) (*pluginv1.ListMemberSubscriptionsResponse, error) {
			return c.ListMemberSubscriptions(cctx, &pluginv1.ListMemberSubscriptionsRequest{Member: testMember, Languages: langs, PageToken: token})
		})
	}
	resp, err := list("")
	if err != nil {
		t.Fatalf("ListMemberSubscriptions: %v; a member the tool does not know gets an empty list", err)
	}
	if n := len(resp.GetSubscriptions()); n > pluginapi.MaxMemberSubscriptions {
		t.Errorf("ListMemberSubscriptions returned %d subscriptions; the host keeps %d", n, pluginapi.MaxMemberSubscriptions)
	}
	seen := map[string]bool{}
	var previous time.Time
	for _, sub := range resp.GetSubscriptions() {
		where := "subscription " + quote(sub.GetTitle())
		if !pluginapi.ValidSubscriptionID(sub.GetId()) || seen[sub.GetId()] {
			t.Errorf("%s: id %q is not valid or is repeated; the host drops it", where, sub.GetId())
		}
		seen[sub.GetId()] = true
		if strings.TrimSpace(sub.GetTitle()) == "" || strings.TrimSpace(sub.GetKindText()) == "" {
			t.Errorf("%s needs a title and a kind_text; the host drops it", where)
		}
		tooLong(t, where+" kind_text", sub.GetKindText(), pluginapi.MaxSubscriptionKind)
		tooLong(t, where+" title", sub.GetTitle(), pluginapi.MaxSubscriptionTitle)
		tooLong(t, where+" status_text", sub.GetStatusText(), pluginapi.MaxWorkStatusText)
		if u := sub.GetImageUrl(); u != "" && !absoluteHTTP(u) {
			t.Errorf("%s: image_url %q is not an absolute http or https URL; the host drops it", where, u)
		}
		switch tg := sub.GetTarget().(type) {
		case *pluginv1.MemberSubscription_Work:
			w := tg.Work
			if ty := w.GetType(); ty != pluginv1.ItemType_ITEM_TYPE_MOVIE && ty != pluginv1.ItemType_ITEM_TYPE_SERIES {
				t.Errorf("%s: work type %s; the host does not open it", where, ty)
			}
			if w.GetKey() != "" && !pluginapi.ValidWorkKey(w.GetKey()) {
				t.Errorf("%s: work key %q is not valid; the host removes the key", where, w.GetKey())
			}
		case *pluginv1.MemberSubscription_PageRef:
			if !declares(m, pluginv1.CapabilityDiscoverSource) {
				t.Errorf("%s: page_ref without discover.source; the host cannot open it", where)
			} else if !pluginapi.ValidDiscoverRef(tg.PageRef) {
				t.Errorf("%s: page_ref %q is not valid; the host does not open it", where, tg.PageRef)
			}
		}
		checkOffers(t, where, sub.GetActions(), declared, "subscription_actions", pluginapi.MaxSubscriptionOffers, true)
		for _, o := range sub.GetActions() {
			for _, opt := range o.GetOptions() {
				if opt.GetSearchable() {
					checkSuggestions(ctx, t, c, where, o.GetAction(), opt.GetKey(), sub.GetId())
				}
			}
		}
		if sub.GetUpdatedAt() == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339, sub.GetUpdatedAt())
		if err != nil {
			t.Errorf("%s: updated_at %q is not RFC 3339; the entry sorts last", where, sub.GetUpdatedAt())
			continue
		}
		if !previous.IsZero() && at.After(previous) {
			t.Errorf("%s is newer than the one before it; list newest first within a page", where)
		}
		previous = at
	}
	token := resp.GetNextPageToken()
	if len(token) > pluginapi.MaxWorkPageToken {
		t.Errorf("ListMemberSubscriptions: next_page_token is longer than %d; the host treats it as the end", pluginapi.MaxWorkPageToken)
	} else if token != "" {
		if _, err := list(token); err != nil {
			t.Errorf("ListMemberSubscriptions(next page): %v", err)
		}
	}
	// Any member can send any ID: an ID that is not this member's must not be acted on.
	if len(m.GetSubscriptionActions()) > 0 {
		r, err := call(ctx, lookupTimeout, func(cctx context.Context) (*pluginv1.RunSubscriptionActionResponse, error) {
			return pluginv1.NewMemberSubscriptionProviderClient(conn).RunSubscriptionAction(cctx, &pluginv1.RunSubscriptionActionRequest{
				Action: m.GetSubscriptionActions()[0].GetId(), SubscriptionId: "plugintest:not-this-members", Member: testMember, Languages: langs,
			})
		})
		switch {
		case status.Code(err) == codes.NotFound || status.Code(err) == codes.InvalidArgument:
		case err == nil && r.GetRefused():
		case err != nil:
			t.Errorf("RunSubscriptionAction(unknown id): %v; answer NOT_FOUND, INVALID_ARGUMENT or refused", err)
		default:
			t.Errorf("RunSubscriptionAction on an ID that is not the member's succeeded; check that the subscription belongs to the member")
		}
	}
}

// checkCalendar asks for the next 30 days and checks each entry against the window and the limits.
func checkCalendar(ctx context.Context, t *testing.T, conn *grpc.ClientConn) {
	t.Helper()
	c := pluginv1.NewMemberCalendarProviderClient(conn)
	from := time.Now().UTC().Truncate(24 * time.Hour)
	to := from.AddDate(0, 0, 29)
	const day = "2006-01-02"
	resp, err := call(ctx, lookupTimeout, func(cctx context.Context) (*pluginv1.ListMemberCalendarResponse, error) {
		return c.ListMemberCalendar(cctx, &pluginv1.ListMemberCalendarRequest{Member: testMember, Languages: &pluginv1.Languages{Language: "en"}, From: from.Format(day), To: to.Format(day)})
	})
	if err != nil {
		t.Fatalf("ListMemberCalendar: %v; a member the tool does not know gets an empty list", err)
	}
	if n := len(resp.GetEntries()); n > pluginapi.MaxCalendarEntries {
		t.Errorf("ListMemberCalendar returned %d entries; the host keeps %d", n, pluginapi.MaxCalendarEntries)
	}
	for _, e := range resp.GetEntries() {
		w := e.GetWork()
		where := "calendar entry " + quote(w.GetTitle())
		if ty := w.GetType(); ty != pluginv1.ItemType_ITEM_TYPE_MOVIE && ty != pluginv1.ItemType_ITEM_TYPE_SERIES {
			t.Errorf("%s: type %s; the host drops it", where, ty)
		}
		if strings.TrimSpace(w.GetTitle()) == "" {
			t.Errorf("calendar entry on %s has no title; the host drops it", e.GetDate())
		}
		d, err := time.Parse(day, e.GetDate())
		switch {
		case err != nil:
			t.Errorf("%s: date %q is not YYYY-MM-DD; the host drops it", where, e.GetDate())
		case d.Before(from) || d.After(to):
			t.Errorf("%s: date %s is outside the window %s to %s; the host drops it", where, e.GetDate(), from.Format(day), to.Format(day))
		}
		if tm := e.GetTime(); tm != "" {
			if _, err := time.Parse(time.RFC3339, tm); err != nil {
				t.Errorf("%s: time %q is not RFC 3339; the host shows the date only", where, tm)
			}
		}
		if e.GetSeason() < 0 || e.GetEpisode() < 0 {
			t.Errorf("%s: negative season or episode", where)
		}
		tooLong(t, where+" episode_title", e.GetEpisodeTitle(), pluginapi.MaxCalendarEpisodeTitle)
		tooLong(t, where+" note", e.GetNote(), pluginapi.MaxCalendarNote)
	}
}

// checkSubscriptionForm asks for the new-subscription form and for suggestions of each searchable option. It creates nothing:
// CreateSubscription changes something outside the server.
func checkSubscriptionForm(ctx context.Context, t *testing.T, conn *grpc.ClientConn) {
	t.Helper()
	c := pluginv1.NewMemberSubscriptionProviderClient(conn)
	form, err := call(ctx, lookupTimeout, func(cctx context.Context) (*pluginv1.SubscriptionForm, error) {
		return c.GetSubscriptionForm(cctx, &pluginv1.GetSubscriptionFormRequest{Member: testMember, Languages: &pluginv1.Languages{Language: "en"}})
	})
	if err != nil {
		t.Fatalf("GetSubscriptionForm: %v; the manifest sets subscription_form, so the host asks for it", err)
	}
	tooLong(t, "form title", form.GetTitle(), pluginapi.MaxSubscriptionFormTitle)
	tooLong(t, "form description", form.GetDescription(), pluginapi.MaxSubscriptionFormText)
	if n := len(form.GetOptions()); n == 0 || n > pluginapi.MaxSubscriptionFormOptions {
		t.Errorf("the form has %d options; want 1 to %d", n, pluginapi.MaxSubscriptionFormOptions)
	}
	keys := map[string]bool{}
	for _, o := range form.GetOptions() {
		where := "form option " + quote(o.GetKey())
		if keys[o.GetKey()] {
			t.Errorf("%s: the key is repeated; keys are unique within the form", where)
		}
		keys[o.GetKey()] = true
		if strings.TrimSpace(o.GetLabel()) == "" {
			t.Errorf("%s has no label; the host drops it", where)
		}
		tooLong(t, where+" label", o.GetLabel(), pluginapi.MaxWorkOptionLabel)
		checkOptionChoices(t, where, o, true)
		if o.GetSearchable() {
			checkSuggestions(ctx, t, c, where, pluginv1.SubscriptionFormNew, o.GetKey(), "")
		}
	}
}

// checkSuggestions asks SuggestChoices for one searchable option with a short text and checks the answer.
func checkSuggestions(ctx context.Context, t *testing.T, c pluginv1.MemberSubscriptionProviderClient, where, form, key, subscriptionID string) {
	t.Helper()
	resp, err := call(ctx, lookupTimeout, func(cctx context.Context) (*pluginv1.SuggestChoicesResponse, error) {
		return c.SuggestChoices(cctx, &pluginv1.SuggestChoicesRequest{Form: form, Key: key, Query: "a", SubscriptionId: subscriptionID,
			Member: testMember, Languages: &pluginv1.Languages{Language: "en"}})
	})
	if err != nil {
		t.Errorf("%s: SuggestChoices(form %q, key %q): %v; a searchable option needs suggestions", where, form, key, err)
		return
	}
	if n := len(resp.GetChoices()); n > pluginapi.MaxSuggestedChoices {
		t.Errorf("%s: SuggestChoices returned %d choices; the host keeps %d", where, n, pluginapi.MaxSuggestedChoices)
	}
	for _, ch := range resp.GetChoices() {
		if !pluginapi.ValidWorkChoiceValue(ch.GetValue()) {
			t.Errorf("%s: suggested value %q is not valid; the host drops it", where, ch.GetValue())
		}
		tooLong(t, where+" suggested label", ch.GetLabel(), pluginapi.MaxWorkChoiceLabel)
		tooLong(t, where+" suggested note", ch.GetNote(), pluginapi.MaxWorkChoiceNote)
	}
}
