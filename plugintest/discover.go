package plugintest

import (
	"context"
	"maps"
	"math"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/kryspal/plugin-sdk"
	pluginv1 "github.com/kryspal/plugin-sdk/pluginv1"
)

// workActionManifestProblems lists what the host would ignore in Manifest.work_actions (PROTOCOL.md §7.12).
func workActionManifestProblems(m *pluginv1.Manifest) []string {
	var out []string
	acts := m.GetWorkActions()
	switch {
	case declares(m, pluginv1.CapabilityWorkAction) && len(acts) == 0:
		out = append(out, "the manifest declares work.action but lists no work_actions")
	case !declares(m, pluginv1.CapabilityWorkAction) && len(acts) > 0:
		out = append(out, "the manifest lists work_actions but does not declare work.action")
	case len(acts) > pluginapi.MaxWorkActions:
		out = append(out, "more work_actions than the host uses")
	}
	seen := map[string]bool{}
	for _, a := range acts {
		if !pluginapi.ValidWorkIdentifier(a.GetId()) || seen[a.GetId()] {
			out = append(out, "work action id "+quote(a.GetId())+" is not valid or is repeated")
		}
		seen[a.GetId()] = true
		if a.GetLabel() == "" || utf8.RuneCountInString(a.GetLabel()) > pluginapi.MaxWorkActionLabel {
			out = append(out, "work action "+quote(a.GetId())+" needs a label of at most 48 characters")
		}
		if len(a.GetRequiredProviderIds()) == 0 {
			out = append(out, "work action "+quote(a.GetId())+" lists no required_provider_ids; the host ignores it")
		}
		for _, k := range a.GetRequiredProviderIds() {
			if k == "" || k != strings.ToLower(k) {
				out = append(out, "work action "+quote(a.GetId())+": provider ID key "+quote(k)+" must be lowercase")
			}
		}
	}
	return out
}

func quote(s string) string { return `"` + s + `"` }

func tooLong(t *testing.T, what, s string, limit int) {
	t.Helper()
	if utf8.RuneCountInString(s) > limit {
		t.Errorf("%s is longer than %d characters; the host cuts it: %q", what, limit, s)
	}
}

func absoluteHTTP(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https")
}

func checkWork(t *testing.T, where string, w *pluginv1.Work) {
	t.Helper()
	if !pluginapi.ValidWorkKey(w.GetKey()) {
		t.Errorf("%s: key %q is not valid; the host drops the work", where, w.GetKey())
	}
	if ty := w.GetType(); ty != pluginv1.ItemType_ITEM_TYPE_MOVIE && ty != pluginv1.ItemType_ITEM_TYPE_SERIES {
		t.Errorf("%s (%s): type %s; the host drops the work", where, w.GetKey(), ty)
	}
	if strings.TrimSpace(w.GetTitle()) == "" {
		t.Errorf("%s (%s): the title is empty; the host drops the work", where, w.GetKey())
	}
	tooLong(t, where+" title", w.GetTitle(), pluginapi.MaxWorkTitle)
	tooLong(t, where+" overview", w.GetOverview(), pluginapi.MaxWorkOverview)
	tooLong(t, where+" caption", w.GetCaption(), pluginapi.MaxWorkCaption)
	tooLong(t, where+" product_code", strings.TrimSpace(w.GetProductCode()), pluginapi.MaxWorkProductCode)
	if strings.ContainsFunc(strings.TrimSpace(w.GetProductCode()), unicode.IsControl) {
		t.Errorf("%s (%s): product_code %q has control characters; the host drops it", where, w.GetKey(), w.GetProductCode())
	}
	checkWorkBadges(t, where+" ("+w.GetKey()+")", w.GetBadges())
	for name, u := range map[string]string{"poster_url": w.GetPosterUrl(), "backdrop_url": w.GetBackdropUrl()} {
		if u != "" && !absoluteHTTP(u) {
			t.Errorf("%s (%s): %s %q is not an absolute http or https URL", where, w.GetKey(), name, u)
		}
	}
	for k := range w.GetProviderIds() {
		if k != strings.ToLower(k) {
			t.Errorf("%s (%s): provider ID key %q must be lowercase", where, w.GetKey(), k)
		}
	}
}

// checkWorkBadges reports what the host would drop from Work.badges.
func checkWorkBadges(t *testing.T, where string, badges []*pluginv1.WorkBadge) {
	t.Helper()
	if len(badges) > pluginapi.MaxWorkBadges {
		t.Errorf("%s: %d badges; the host keeps %d", where, len(badges), pluginapi.MaxWorkBadges)
	}
	perCorner := map[pluginv1.WorkBadgeCorner]int{}
	for _, b := range badges {
		text, r := strings.TrimSpace(b.GetText()), b.GetRating()
		if text == "" && r == nil {
			t.Errorf("%s: a badge has neither text nor rating; the host drops it", where)
		}
		if utf8.RuneCountInString(text) > pluginapi.MaxWorkBadgeText || strings.ContainsFunc(text, unicode.IsControl) {
			t.Errorf("%s: badge text %q is longer than %d characters or has control characters; the host drops it", where, text, pluginapi.MaxWorkBadgeText)
		}
		for name, c := range map[string]string{"color": b.GetColor(), "outline": b.GetOutline()} {
			if c != "" && !badgeColorRE.MatchString(c) {
				t.Errorf("%s: badge %q %s %q is not #RRGGBB; the host drops it", where, text, name, c)
			}
		}
		corner := b.GetCorner()
		if corner < pluginv1.WorkBadgeCorner_WORK_BADGE_CORNER_UNSPECIFIED || corner > pluginv1.WorkBadgeCorner_WORK_BADGE_CORNER_BOTTOM_RIGHT {
			t.Errorf("%s: badge %q has corner %d; the host puts it top left", where, text, corner)
			corner = pluginv1.WorkBadgeCorner_WORK_BADGE_CORNER_TOP_LEFT
		}
		if corner == pluginv1.WorkBadgeCorner_WORK_BADGE_CORNER_UNSPECIFIED {
			corner = pluginv1.WorkBadgeCorner_WORK_BADGE_CORNER_TOP_LEFT
		}
		if perCorner[corner]++; perCorner[corner] == pluginapi.MaxWorkCornerBadges+1 {
			t.Errorf("%s: more than %d badges in corner %s; the host keeps %d", where, pluginapi.MaxWorkCornerBadges, corner, pluginapi.MaxWorkCornerBadges)
		}
		if r != nil {
			limit := r.GetMax()
			if limit == 0 {
				limit = 10
			}
			if !pluginapi.ValidRatingSource(r.GetSource()) || !(r.GetValue() > 0) || r.GetValue() > limit {
				t.Errorf("%s: badge rating %q %v of %v is not valid; the host drops the badge", where, r.GetSource(), r.GetValue(), r.GetMax())
			}
		}
	}
}

// checkDiscoverSeedDeclaration checks Manifest.discover_seed_ids against the permission and the capability it needs.
func checkDiscoverSeedDeclaration(t *testing.T, m *pluginv1.Manifest) {
	t.Helper()
	ids := m.GetDiscoverSeedIds()
	permitted := slices.Contains(m.GetPermissions(), pluginv1.PermissionDiscoverSeeds)
	switch {
	case len(ids) > 0 && !permitted:
		t.Errorf("discover_seed_ids without the permission %s; the host ignores them", pluginv1.PermissionDiscoverSeeds)
	case permitted && len(ids) == 0:
		t.Errorf("the permission %s without discover_seed_ids; the plugin would never receive a seed", pluginv1.PermissionDiscoverSeeds)
	case permitted && !declares(m, pluginv1.CapabilityDiscoverSource):
		t.Errorf("the permission %s needs the capability %s", pluginv1.PermissionDiscoverSeeds, pluginv1.CapabilityDiscoverSource)
	}
	if len(ids) > pluginapi.MaxDiscoverSeedIDs {
		t.Errorf("%d discover_seed_ids; the host keeps %d", len(ids), pluginapi.MaxDiscoverSeedIDs)
	}
	for _, id := range ids {
		if !pluginapi.ValidRatingSource(id) {
			t.Errorf("discover_seed_ids: %q is not lowercase letters, digits, _ and -, at most 32 characters; the host ignores it", id)
		}
	}
}

// checkDiscoverSeeds asks for the first seeded tab (DiscoverTab.seeded) with seeds and without. A page must work both ways, because the plugin cannot know
// whether the administrator allowed the seeds, and its filters and sort must be the same, because the host keeps them per page
// and not per set of seeds.
func checkDiscoverSeeds(ctx context.Context, t *testing.T, conn *grpc.ClientConn, m *pluginv1.Manifest, seeds []*pluginv1.DiscoverSeed) {
	t.Helper()
	c := pluginv1.NewDiscoverSourceClient(conn)
	langs := &pluginv1.Languages{Language: "en"}
	info, err := call(ctx, lookupTimeout, func(cctx context.Context) (*pluginv1.DiscoverSourceInfo, error) {
		return c.DescribeDiscoverSource(cctx, &pluginv1.DescribeDiscoverSourceRequest{Languages: langs})
	})
	if err != nil {
		t.Fatalf("DescribeDiscoverSource: %v", err)
	}
	var ref string
	for i, tab := range info.GetTabs() {
		if tab.GetSeeded() && i > 0 && ref == "" {
			ref = tab.GetRef()
		}
	}
	if ref == "" {
		t.Errorf("the permission %s without a seeded tab after the first; the plugin would never receive a seed", pluginv1.PermissionDiscoverSeeds)
		return
	}
	if seeds == nil {
		for _, origin := range []pluginv1.DiscoverSeedOrigin{pluginv1.DiscoverSeedOrigin_DISCOVER_SEED_ORIGIN_LIBRARY, pluginv1.DiscoverSeedOrigin_DISCOVER_SEED_ORIGIN_WATCHED} {
			seed := &pluginv1.DiscoverSeed{Type: pluginv1.ItemType_ITEM_TYPE_MOVIE, ProviderIds: map[string]string{}, Origin: origin}
			for _, id := range m.GetDiscoverSeedIds() {
				if id == pluginv1.DiscoverSeedIDProductCode {
					seed.ProductCode = "PLUGINTEST-1"
				} else {
					seed.ProviderIds[id] = "1"
				}
			}
			seeds = append(seeds, seed)
		}
	}
	ask := func(with []*pluginv1.DiscoverSeed) *pluginv1.DiscoverPage {
		page, err := call(ctx, lookupTimeout, func(cctx context.Context) (*pluginv1.DiscoverPage, error) {
			return c.GetDiscoverPage(cctx, &pluginv1.GetDiscoverPageRequest{Ref: ref, Languages: langs, Seeds: with})
		})
		if err != nil {
			t.Fatalf("GetDiscoverPage(ref %q) with %d seeds: %v", ref, len(with), err)
		}
		return page
	}
	plain, seeded := ask(nil), ask(seeds)
	same := len(plain.GetFilters()) == len(seeded.GetFilters()) && proto.Equal(plain.GetSort(), seeded.GetSort())
	for i := range plain.GetFilters() {
		same = same && i < len(seeded.GetFilters()) && proto.Equal(plain.GetFilters()[i], seeded.GetFilters()[i])
	}
	if !same {
		t.Errorf("GetDiscoverPage(ref %q): the filters or the sort depend on the seeds; the host keeps them per page", ref)
	}
	for _, r := range seeded.GetRows() {
		checkWorks(t, "a page with seeds", r.GetWorks(), pluginv1.ItemType_ITEM_TYPE_MOVIE, pluginv1.ItemType_ITEM_TYPE_SERIES)
		if r.GetRefreshable() {
			checkRefresh(t, "page "+quote(ref)+" with seeds", r.GetId(), func(req *pluginv1.GetDiscoverPageRequest) (*pluginv1.DiscoverPage, error) {
				req.Ref, req.Languages, req.Seeds = ref, langs, seeds
				return call(ctx, lookupTimeout, func(cctx context.Context) (*pluginv1.DiscoverPage, error) { return c.GetDiscoverPage(cctx, req) })
			})
		}
	}
}

// checkWorks checks one page of works. allowed lists the types the host keeps on this page.
func checkWorks(t *testing.T, where string, works []*pluginv1.Work, allowed ...pluginv1.ItemType) {
	t.Helper()
	if len(works) > pluginapi.MaxWorksPerPage {
		t.Errorf("%s: %d works on a page; the host keeps %d", where, len(works), pluginapi.MaxWorksPerPage)
	}
	for _, w := range works {
		checkWork(t, where, w)
		if !slices.Contains(allowed, w.GetType()) {
			t.Errorf("%s (%s): type %s is not declared here; the host drops the work", where, w.GetKey(), w.GetType())
		}
	}
}

// checkDiscoverOption checks a filter or the sort of a kind and records its key in keys.
func checkDiscoverOption(t *testing.T, where string, o *pluginv1.WorkOption, keys map[string]*pluginv1.WorkOption) {
	t.Helper()
	if !pluginapi.ValidWorkIdentifier(o.GetKey()) {
		t.Errorf("%s: key %q is not valid; the host drops it", where, o.GetKey())
	}
	if keys[o.GetKey()] != nil {
		t.Errorf("%s: key %q is repeated; keys are unique among a kind's filters and sort", where, o.GetKey())
	}
	keys[o.GetKey()] = o
	if strings.TrimSpace(o.GetLabel()) == "" {
		t.Errorf("%s %q has no label; the host drops it", where, o.GetKey())
	}
	tooLong(t, where+" label", o.GetLabel(), pluginapi.MaxWorkOptionLabel)
	if n := len(o.GetChoices()); n == 0 || n > pluginapi.MaxWorkChoices {
		t.Errorf("%s %q has %d choices; want 1 to %d", where, o.GetKey(), n, pluginapi.MaxWorkChoices)
	}
	if o.GetRequired() {
		t.Errorf("%s %q is required; required does not apply here", where, o.GetKey())
	}
	selected := 0
	for _, c := range o.GetChoices() {
		if !pluginapi.ValidWorkChoiceValue(c.GetValue()) {
			t.Errorf("%s %q: choice value %q is not valid; the host drops it", where, o.GetKey(), c.GetValue())
		}
		tooLong(t, where+" choice label", c.GetLabel(), pluginapi.MaxWorkChoiceLabel)
		if c.GetDisabled() || c.GetNote() != "" {
			t.Errorf("%s %q: choice %q has disabled or note, which do not apply here", where, o.GetKey(), c.GetValue())
		}
		if c.GetSelected() {
			selected++
		}
	}
	if selected > 1 && !o.GetMultiple() {
		t.Errorf("%s %q is a single choice with %d selected; the host keeps the first", where, o.GetKey(), selected)
	}
}

// A ref that no plugin gives out: GetDiscoverPage must answer it with NOT_FOUND.
const unknownDiscoverRef = "plugintest-no-such-ref"

var workTypes = []pluginv1.ItemType{pluginv1.ItemType_ITEM_TYPE_MOVIE, pluginv1.ItemType_ITEM_TYPE_SERIES}

// checkDiscover runs the discover.source methods and returns the first work with provider IDs, for the work.action checks.
func checkDiscover(ctx context.Context, t *testing.T, conn *grpc.ClientConn, query string) *pluginv1.Work {
	t.Helper()
	c := pluginv1.NewDiscoverSourceClient(conn)
	langs := &pluginv1.Languages{Language: "en"}
	info, err := call(ctx, lookupTimeout, func(cctx context.Context) (*pluginv1.DiscoverSourceInfo, error) {
		return c.DescribeDiscoverSource(cctx, &pluginv1.DescribeDiscoverSourceRequest{Languages: langs})
	})
	if err != nil {
		t.Fatalf("DescribeDiscoverSource: %v", err)
	}
	tooLong(t, "source name", info.GetName(), pluginapi.MaxDiscoverName)
	if n := len(info.GetTabs()); n > pluginapi.MaxDiscoverTabs {
		t.Errorf("%d tabs; the host keeps %d", n, pluginapi.MaxDiscoverTabs)
	}
	var tabs []string
	for i, tab := range info.GetTabs() {
		tooLong(t, "tab title", tab.GetTitle(), pluginapi.MaxDiscoverTabName)
		switch {
		case i == 0 && tab.GetSeeded():
			t.Errorf("the first tab %q is seeded; it is where viewers land, so the host drops it", tab.GetTitle())
		case strings.TrimSpace(tab.GetTitle()) == "":
			t.Errorf("tab with ref %q has no title; the host drops it", tab.GetRef())
		case !pluginapi.ValidDiscoverRef(tab.GetRef()):
			t.Errorf("tab %q: ref %q is not valid; the host drops it", tab.GetTitle(), tab.GetRef())
		default:
			tabs = append(tabs, tab.GetRef())
		}
	}
	if len(tabs) == 0 {
		t.Fatal("DescribeDiscoverSource declares no valid tab; the host shows nothing for this source")
	}
	for _, ty := range info.GetSearchTypes() {
		if !slices.Contains(workTypes, ty) {
			t.Errorf("search type %s; the host drops it", ty)
		}
	}
	missing, err := call(ctx, lookupTimeout, func(cctx context.Context) (*pluginv1.DiscoverPage, error) {
		return c.GetDiscoverPage(cctx, &pluginv1.GetDiscoverPageRequest{Ref: unknownDiscoverRef, Languages: langs})
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("GetDiscoverPage(ref %q) = %d rows, %v; a member can send any ref, so answer NOT_FOUND", unknownDiscoverRef, len(missing.GetRows()), err)
	}
	var first *pluginv1.Work
	for _, ref := range tabs {
		w, more := checkDiscoverPage(ctx, t, c, langs, ref)
		if first == nil {
			first = w
		}
		// One step further: the pages this one links to. They are ordinary pages, so one of each is enough.
		for _, next := range more {
			if w, _ := checkDiscoverPage(ctx, t, c, langs, next); first == nil {
				first = w
			}
		}
	}
	if len(info.GetSearchTypes()) == 0 {
		t.Log("the source declares no search_types; SearchWorks is not checked")
	} else {
		found, err := call(ctx, lookupTimeout, func(cctx context.Context) (*pluginv1.SearchWorksResponse, error) {
			return c.SearchWorks(cctx, &pluginv1.SearchWorksRequest{Query: query, Languages: langs})
		})
		if err != nil {
			t.Fatalf("SearchWorks(%q): %v; no match is an empty list", query, err)
		}
		checkWorks(t, "SearchWorks", found.GetWorks(), info.GetSearchTypes()...)
		for _, w := range found.GetWorks() {
			if first == nil && len(w.GetProviderIds()) > 0 {
				first = w
			}
		}
	}
	if first == nil {
		t.Log("no work with provider IDs to look at; GetWork is not checked")
		return nil
	}
	full, err := call(ctx, lookupTimeout, func(cctx context.Context) (*pluginv1.Work, error) {
		return c.GetWork(cctx, &pluginv1.GetWorkRequest{Key: first.GetKey(), Languages: langs})
	})
	if err != nil {
		t.Fatalf("GetWork(%q): %v", first.GetKey(), err)
	}
	checkWork(t, "GetWork", full)
	if full.GetKey() != first.GetKey() {
		t.Errorf("GetWork(%q) returned key %q", first.GetKey(), full.GetKey())
	}
	if len(full.GetSeasons()) > pluginapi.MaxWorkSeasons {
		t.Errorf("%d seasons; the host keeps %d", len(full.GetSeasons()), pluginapi.MaxWorkSeasons)
	}
	if len(full.GetSeasons()) > 0 && full.GetType() != pluginv1.ItemType_ITEM_TYPE_SERIES {
		t.Errorf("GetWork(%q): a %s has seasons; the host ignores them", first.GetKey(), full.GetType())
	}
	for _, ref := range checkWorkDetails(t, full) {
		checkDiscoverPage(ctx, t, c, langs, ref)
	}
	return first
}

// checkWorkDetails checks what only GetWork supplies, all of it optional, and returns the refs worth opening.
func checkWorkDetails(t *testing.T, w *pluginv1.Work) []string {
	t.Helper()
	tooLong(t, "GetWork tagline", w.GetTagline(), pluginapi.MaxWorkTagline)
	var refs []string
	addRef := func(what, ref string) {
		t.Helper()
		switch {
		case ref == "":
		case !pluginapi.ValidDiscoverRef(ref):
			t.Errorf("GetWork: %s: ref %q is not valid; the host drops it", what, ref)
		case len(refs) < 3:
			refs = append(refs, ref)
		}
	}
	links := func(what string, in []*pluginv1.WorkRefLink, limit, length int) {
		t.Helper()
		if len(in) > limit {
			t.Errorf("GetWork: %d %s; the host keeps %d", len(in), what, limit)
		}
		for _, l := range in {
			if strings.TrimSpace(l.GetText()) == "" {
				t.Errorf("GetWork: an empty text in %s; the host drops it", what)
			}
			tooLong(t, "GetWork "+what, l.GetText(), length)
			addRef(what+" "+l.GetText(), l.GetRef())
		}
	}
	links("genres", w.GetGenres(), pluginapi.MaxWorkGenres, pluginapi.MaxWorkGenreName)
	links("companies", w.GetCompanies(), pluginapi.MaxWorkCompanies, pluginapi.MaxWorkCompanyName)
	if n := len(w.GetCredits()); n > pluginapi.MaxWorkCredits {
		t.Errorf("GetWork: %d credits; the host keeps the first %d", n, pluginapi.MaxWorkCredits)
	}
	for _, credit := range w.GetCredits() {
		if strings.TrimSpace(credit.GetName()) == "" {
			t.Error("GetWork: a credit has no name; the host drops it")
		}
		if k := credit.GetKind(); k <= pluginv1.WorkCreditKind_WORK_CREDIT_KIND_UNSPECIFIED || k > pluginv1.WorkCreditKind_WORK_CREDIT_KIND_OTHER {
			t.Errorf("GetWork: credit %q has kind %s; the host counts it as other", credit.GetName(), k)
		}
		tooLong(t, "GetWork credit name", credit.GetName(), pluginapi.MaxWorkCreditText)
		tooLong(t, "GetWork credit role", credit.GetRole(), pluginapi.MaxWorkCreditText)
		if u := credit.GetImageUrl(); u != "" && !absoluteHTTP(u) {
			t.Errorf("GetWork: credit %q: image_url %q is not an absolute http or https URL; the host drops it", credit.GetName(), u)
		}
		addRef("credit "+credit.GetName(), credit.GetRef())
	}
	addRef("related_ref", w.GetRelatedRef())
	if n := len(w.GetExternalLinks()); n > pluginapi.MaxWorkLinks {
		t.Errorf("GetWork: %d external links; the host keeps %d", n, pluginapi.MaxWorkLinks)
	}
	for _, l := range w.GetExternalLinks() {
		if strings.TrimSpace(l.GetLabel()) == "" {
			t.Errorf("GetWork: external link %q has no label; the host drops it", l.GetUrl())
		}
		tooLong(t, "GetWork external link label", l.GetLabel(), pluginapi.MaxWorkLinkLabel)
		if u, err := url.Parse(l.GetUrl()); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			t.Errorf("GetWork: external link %q is not an http or https URL; the host drops it", l.GetUrl())
		}
	}
	return refs
}

// checkDiscoverHeader checks the top of a page.
func checkDiscoverHeader(t *testing.T, where string, h *pluginv1.DiscoverPageHeader) {
	t.Helper()
	if h == nil {
		return
	}
	if strings.TrimSpace(h.GetTitle()) == "" {
		t.Errorf("%s: the header has no title; the host ignores it", where)
	}
	tooLong(t, where+" header title", h.GetTitle(), pluginapi.MaxDiscoverHeader)
	tooLong(t, where+" header subtitle", h.GetSubtitle(), pluginapi.MaxDiscoverHeader)
	tooLong(t, where+" header overview", h.GetOverview(), pluginapi.MaxWorkOverview)
	for name, u := range map[string]string{"image_url": h.GetImageUrl(), "backdrop_url": h.GetBackdropUrl()} {
		if u != "" && !absoluteHTTP(u) {
			t.Errorf("%s: header %s %q is not an absolute http or https URL", where, name, u)
		}
	}
	if n := len(h.GetFacts()); n > pluginapi.MaxDiscoverFacts {
		t.Errorf("%s: %d facts; the host keeps %d", where, n, pluginapi.MaxDiscoverFacts)
	}
	for _, f := range h.GetFacts() {
		if strings.TrimSpace(f.GetLabel()) == "" || strings.TrimSpace(f.GetValue()) == "" {
			t.Errorf("%s: fact %q=%q needs both a label and a value; the host drops it", where, f.GetLabel(), f.GetValue())
		}
		tooLong(t, where+" fact label", f.GetLabel(), pluginapi.MaxDiscoverFactKey)
		tooLong(t, where+" fact value", f.GetValue(), pluginapi.MaxDiscoverFact)
	}
}

// checkDiscoverPage opens one page: its header, filters and rows, then each row by itself, a filter, and a next page. It
// returns the first work with provider IDs and up to two refs the page links to.
func checkDiscoverPage(ctx context.Context, t *testing.T, c pluginv1.DiscoverSourceClient, langs *pluginv1.Languages, ref string) (*pluginv1.Work, []string) {
	t.Helper()
	get := func(req *pluginv1.GetDiscoverPageRequest) (*pluginv1.DiscoverPage, error) {
		req.Ref, req.Languages = ref, langs
		return call(ctx, lookupTimeout, func(cctx context.Context) (*pluginv1.DiscoverPage, error) {
			return c.GetDiscoverPage(cctx, req)
		})
	}
	where := "page " + quote(ref)
	page, err := get(&pluginv1.GetDiscoverPageRequest{})
	if err != nil {
		t.Errorf("GetDiscoverPage(%s): %v", where, err)
		return nil, nil
	}
	checkDiscoverHeader(t, where, page.GetHeader())
	keys := checkDiscoverFilters(t, where, page)
	if n := len(page.GetRows()); n > pluginapi.MaxDiscoverRows {
		t.Errorf("%s: %d rows; the host keeps %d", where, n, pluginapi.MaxDiscoverRows)
	}
	var first *pluginv1.Work
	var more []string
	addMore := func(what, r string) {
		t.Helper()
		switch {
		case !pluginapi.ValidDiscoverRef(r):
			t.Errorf("%s: %s: ref %q is not valid; the host drops it", where, what, r)
		case len(more) < 2:
			more = append(more, r)
		}
	}
	seen := map[string]bool{}
	total := 0
	for i, r := range page.GetRows() {
		rowWhere := "row " + r.GetId()
		if !pluginapi.ValidWorkIdentifier(r.GetId()) || seen[r.GetId()] {
			t.Errorf("row id %q is not valid or is repeated; the host drops the row", r.GetId())
		}
		seen[r.GetId()] = true
		tooLong(t, "row title", r.GetTitle(), pluginapi.MaxWorkRowTitle)
		hero := r.GetStyle() == pluginv1.DiscoverRowStyle_DISCOVER_ROW_STYLE_HERO
		if strings.TrimSpace(r.GetTitle()) == "" && !hero {
			t.Errorf("row %q has no title; the host drops it", r.GetId())
		}
		if hero && i > 0 {
			t.Errorf("row %q is a hero but not the first row; the host shows it as posters", r.GetId())
		}
		if r.GetStyle() == pluginv1.DiscoverRowStyle_DISCOVER_ROW_STYLE_CARDS {
			if len(r.GetWorks()) > 0 {
				t.Errorf("row %q has cards and works; the host ignores the works", r.GetId())
			}
			if n := len(r.GetCards()); n > pluginapi.MaxDiscoverCards {
				t.Errorf("row %q: %d cards; the host keeps %d", r.GetId(), n, pluginapi.MaxDiscoverCards)
			}
			for _, card := range r.GetCards() {
				if strings.TrimSpace(card.GetTitle()) == "" {
					t.Errorf("row %q: a card has no title; the host drops it", r.GetId())
				}
				tooLong(t, "card title", card.GetTitle(), pluginapi.MaxDiscoverCard)
				if u := card.GetImageUrl(); u != "" && !absoluteHTTP(u) {
					t.Errorf("row %q: card image_url %q is not an absolute http or https URL", r.GetId(), u)
				}
				if k := card.GetImageKind(); k < pluginv1.DiscoverCardImageKind_DISCOVER_CARD_IMAGE_KIND_UNSPECIFIED || k > pluginv1.DiscoverCardImageKind_DISCOVER_CARD_IMAGE_KIND_PORTRAIT {
					t.Errorf("row %q: card %q has image_kind %d; the host shows it as a cover", r.GetId(), card.GetTitle(), k)
				}
				addMore("card "+card.GetTitle(), card.GetRef())
			}
		} else {
			if len(r.GetCards()) > 0 {
				t.Errorf("row %q has works and cards; the host ignores the cards", r.GetId())
			}
			checkWorks(t, rowWhere, r.GetWorks(), workTypes...)
			if r.GetStyle() == pluginv1.DiscoverRowStyle_DISCOVER_ROW_STYLE_LANDSCAPE && len(r.GetWorks()) > 0 &&
				!slices.ContainsFunc(r.GetWorks(), func(w *pluginv1.Work) bool { return w.GetBackdropUrl() != "" }) {
				t.Errorf("landscape row %q: no work has a backdrop_url; they all show a placeholder", r.GetId())
			}
			if hero {
				if n := len(r.GetWorks()); n > pluginapi.MaxHeroWorks {
					t.Errorf("hero row %q: %d works; the host keeps %d", r.GetId(), n, pluginapi.MaxHeroWorks)
				}
				for _, w := range r.GetWorks() {
					if w.GetBackdropUrl() == "" {
						t.Errorf("hero row %q: work %q has no backdrop_url; the host skips it", r.GetId(), w.GetKey())
					}
				}
			}
			total += len(r.GetWorks())
		}
		for _, w := range r.GetWorks() {
			if first == nil && len(w.GetProviderIds()) > 0 {
				first = w
			}
		}
		if m := r.GetMoreRef(); m != "" {
			addMore("more_ref", m)
		}
		if r.GetRefreshable() {
			checkRefresh(t, where, r.GetId(), get)
		}
		token := r.GetNextPageToken()
		if len(token) > pluginapi.MaxWorkPageToken {
			t.Errorf("row %q: next_page_token is longer than %d", r.GetId(), pluginapi.MaxWorkPageToken)
			token = ""
		}
		if token == "" {
			continue
		}
		if hero {
			t.Errorf("hero row %q has a next_page_token; the host ignores it", r.GetId())
			continue
		}
		// The row by itself, as "view more" and a row that loads later ask for it.
		next, err := get(&pluginv1.GetDiscoverPageRequest{RowId: r.GetId(), PageToken: token})
		switch {
		case err != nil:
			t.Errorf("GetDiscoverPage(row %q, next page): %v", r.GetId(), err)
		case len(next.GetRows()) > 1:
			t.Errorf("GetDiscoverPage(row %q) returned %d rows; want only that row", r.GetId(), len(next.GetRows()))
		}
		alone, err := get(&pluginv1.GetDiscoverPageRequest{RowId: r.GetId()})
		if err != nil {
			t.Errorf("GetDiscoverPage(row %q): %v", r.GetId(), err)
			continue
		}
		for _, sel := range firstChoices(checkDiscoverFilters(t, rowWhere, alone)) {
			if _, err := get(&pluginv1.GetDiscoverPageRequest{RowId: r.GetId(), Selections: []*pluginv1.WorkSelection{sel}}); err != nil {
				t.Errorf("GetDiscoverPage(row %q, %s=%s): %v", r.GetId(), sel.GetKey(), sel.GetValues()[0], err)
			}
		}
	}
	if total > pluginapi.MaxDiscoverWorks {
		t.Errorf("%s: %d works across its rows; the host keeps %d", where, total, pluginapi.MaxDiscoverWorks)
	}
	for _, sel := range firstChoices(keys) {
		if _, err := get(&pluginv1.GetDiscoverPageRequest{Selections: []*pluginv1.WorkSelection{sel}}); err != nil {
			t.Errorf("GetDiscoverPage(%s, %s=%s): %v", where, sel.GetKey(), sel.GetValues()[0], err)
		}
	}
	return first, more
}

// checkDiscoverFilters checks what an answer declares and returns it by key.
func checkDiscoverFilters(t *testing.T, where string, page *pluginv1.DiscoverPage) map[string]*pluginv1.WorkOption {
	t.Helper()
	keys := map[string]*pluginv1.WorkOption{}
	if n := len(page.GetFilters()); n > pluginapi.MaxDiscoverFilters {
		t.Errorf("%s: %d filters; the host keeps %d", where, n, pluginapi.MaxDiscoverFilters)
	}
	for _, f := range page.GetFilters() {
		checkDiscoverOption(t, "filter", f, keys)
	}
	if s := page.GetSort(); s != nil {
		checkDiscoverOption(t, "sort", s, keys)
		if s.GetMultiple() {
			t.Errorf("sort %q allows several choices; a sort is a single choice", s.GetKey())
		}
	}
	return keys
}

// firstChoices returns one selection for each declared key: its first valid choice. Keys the host would drop are left out.
func firstChoices(keys map[string]*pluginv1.WorkOption) []*pluginv1.WorkSelection {
	var out []*pluginv1.WorkSelection
	for _, k := range slices.Sorted(maps.Keys(keys)) {
		if o := keys[k]; pluginapi.ValidWorkIdentifier(k) && len(o.GetChoices()) > 0 && pluginapi.ValidWorkChoiceValue(o.GetChoices()[0].GetValue()) {
			out = append(out, &pluginv1.WorkSelection{Key: k, Values: []string{o.GetChoices()[0].GetValue()}})
		}
	}
	return out
}

// checkWorkState checks one state. declared are Manifest.work_actions; withActions says whether the request asked for them.
func checkWorkState(t *testing.T, where string, st *pluginv1.WorkState, declared map[string]bool, withActions bool) {
	t.Helper()
	if st.GetStatus() < pluginv1.WorkStatus_WORK_STATUS_UNSPECIFIED || st.GetStatus() > pluginv1.WorkStatus_WORK_STATUS_FAILED {
		t.Errorf("%s: status %d is not in the fixed set; the host counts it as none", where, st.GetStatus())
	}
	tooLong(t, where+" status_text", st.GetStatusText(), pluginapi.MaxWorkStatusText)
	if !withActions && len(st.GetActions()) > 0 {
		t.Errorf("%s: actions without include_actions; leave them empty and skip the upstream calls they need", where)
	}
	for _, o := range st.GetActions() {
		if !declared[o.GetAction()] {
			t.Errorf("%s: action %q is not in work_actions; the host drops it", where, o.GetAction())
		}
		tooLong(t, where+" action label", o.GetLabel(), pluginapi.MaxWorkActionLabel)
		if len(o.GetOptions()) > pluginapi.MaxWorkOptions {
			t.Errorf("%s: action %q has %d options; the host keeps %d", where, o.GetAction(), len(o.GetOptions()), pluginapi.MaxWorkOptions)
		}
		for _, opt := range o.GetOptions() {
			if !pluginapi.ValidWorkIdentifier(opt.GetKey()) {
				t.Errorf("%s: option key %q is not valid; the host drops the option", where, opt.GetKey())
			}
			if len(opt.GetChoices()) == 0 || len(opt.GetChoices()) > pluginapi.MaxWorkChoices {
				t.Errorf("%s: option %q has %d choices; want 1 to %d", where, opt.GetKey(), len(opt.GetChoices()), pluginapi.MaxWorkChoices)
			}
			for _, c := range opt.GetChoices() {
				if !pluginapi.ValidWorkChoiceValue(c.GetValue()) {
					t.Errorf("%s: choice value %q is not valid; the host drops it", where, c.GetValue())
				}
				tooLong(t, where+" choice label", c.GetLabel(), pluginapi.MaxWorkChoiceLabel)
				tooLong(t, where+" choice note", c.GetNote(), pluginapi.MaxWorkChoiceNote)
			}
		}
	}
}

// checkWorkActions asks about one work with and without actions, and runs the action named by run, if any.
func checkWorkActions(ctx context.Context, t *testing.T, conn *grpc.ClientConn, m *pluginv1.Manifest, ref *pluginv1.WorkRef, run string) {
	t.Helper()
	if ref == nil {
		t.Fatal("no work to ask about: set Options.WorkRef, or declare discover.source with works that have provider IDs")
	}
	c := pluginv1.NewWorkActionProviderClient(conn)
	declared := map[string]bool{}
	for _, a := range m.GetWorkActions() {
		declared[a.GetId()] = true
	}
	member := &pluginv1.WorkMember{Id: "plugintest-member", Name: "plugintest"}
	langs := &pluginv1.Languages{Language: "en"}
	asked := &pluginv1.WorkRef{Ref: "plugintest-ref", Type: ref.GetType(), ProviderIds: ref.GetProviderIds()}
	for _, withActions := range []bool{false, true} {
		resp, err := call(ctx, lookupTimeout, func(cctx context.Context) (*pluginv1.GetWorkStatesResponse, error) {
			return c.GetWorkStates(cctx, &pluginv1.GetWorkStatesRequest{Works: []*pluginv1.WorkRef{asked}, Member: member, Languages: langs, IncludeActions: withActions})
		})
		if err != nil {
			t.Fatalf("GetWorkStates(include_actions=%v): %v", withActions, err)
		}
		if len(resp.GetStates()) > 1 {
			t.Errorf("GetWorkStates returned %d states for one work", len(resp.GetStates()))
		}
		for _, st := range resp.GetStates() {
			if st.GetRef() != asked.GetRef() {
				t.Errorf("state ref = %q; echo WorkRef.ref (%q), or the host drops the state", st.GetRef(), asked.GetRef())
			}
			checkWorkState(t, "GetWorkStates", st, declared, withActions)
		}
	}
	checkMemberWorks(ctx, t, c, member, langs)
	if run == "" {
		return
	}
	resp, err := call(ctx, lookupTimeout, func(cctx context.Context) (*pluginv1.RunWorkActionResponse, error) {
		return c.RunWorkAction(cctx, &pluginv1.RunWorkActionRequest{Action: run, Work: &pluginv1.WorkRef{Type: ref.GetType(), ProviderIds: ref.GetProviderIds()}, Member: member, Languages: langs})
	})
	if err != nil {
		t.Fatalf("RunWorkAction(%q): %v", run, err)
	}
	tooLong(t, "RunWorkAction message", resp.GetMessage(), pluginapi.MaxWorkMessage)
	if resp.GetRefused() && resp.GetMessage() == "" {
		t.Errorf("RunWorkAction(%q) refused without a message for the member", run)
	}
	if l := resp.GetLink(); l != nil && !absoluteHTTP(l.GetUrl()) {
		t.Errorf("link %q is not an absolute http or https URL; the host drops it", l.GetUrl())
	}
	if st := resp.GetState(); st != nil {
		checkWorkState(t, "RunWorkAction", st, declared, true)
	}
}

// checkMemberWorks looks at the first page and asks for the next. UNIMPLEMENTED is a valid answer.
func checkMemberWorks(ctx context.Context, t *testing.T, c pluginv1.WorkActionProviderClient, member *pluginv1.WorkMember, langs *pluginv1.Languages) {
	t.Helper()
	list := func(token string) (*pluginv1.ListMemberWorksResponse, error) {
		return call(ctx, lookupTimeout, func(cctx context.Context) (*pluginv1.ListMemberWorksResponse, error) {
			return c.ListMemberWorks(cctx, &pluginv1.ListMemberWorksRequest{Member: member, Languages: langs, PageToken: token})
		})
	}
	resp, err := list("")
	if status.Code(err) == codes.Unimplemented {
		t.Log("ListMemberWorks is not implemented; the host shows nothing for this plugin")
		return
	}
	if err != nil {
		t.Fatalf("ListMemberWorks: %v; a member the tool does not know gets an empty list", err)
	}
	if n := len(resp.GetWorks()); n > pluginapi.MaxMemberWorks {
		t.Errorf("ListMemberWorks returned %d works; the host keeps %d", n, pluginapi.MaxMemberWorks)
	}
	var previous time.Time
	for _, e := range resp.GetWorks() {
		w := e.GetWork()
		if ty := w.GetType(); ty != pluginv1.ItemType_ITEM_TYPE_MOVIE && ty != pluginv1.ItemType_ITEM_TYPE_SERIES {
			t.Errorf("member work %q: type %s; the host drops it", w.GetTitle(), ty)
		}
		if strings.TrimSpace(w.GetTitle()) == "" {
			t.Errorf("member work %q has no title; the host drops it", w.GetKey())
		}
		if w.GetKey() != "" && !pluginapi.ValidWorkKey(w.GetKey()) {
			t.Errorf("member work %q: key %q is not valid; the host removes the key", w.GetTitle(), w.GetKey())
		}
		if e.GetStatus() < pluginv1.WorkStatus_WORK_STATUS_UNSPECIFIED || e.GetStatus() > pluginv1.WorkStatus_WORK_STATUS_FAILED {
			t.Errorf("member work %q: status %d is not in the fixed set; the host counts it as none", w.GetTitle(), e.GetStatus())
		}
		tooLong(t, "member work status_text", e.GetStatusText(), pluginapi.MaxWorkStatusText)
		if e.ProgressPercent != nil && !(e.GetProgressPercent() >= 0 && e.GetProgressPercent() <= 100) {
			t.Errorf("member work %q: progress %v is outside 0 to 100; the host drops it", w.GetTitle(), e.GetProgressPercent())
		}
		if e.GetUpdatedAt() == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339, e.GetUpdatedAt())
		if err != nil {
			t.Errorf("member work %q: updated_at %q is not RFC 3339; the entry sorts last", w.GetTitle(), e.GetUpdatedAt())
			continue
		}
		if !previous.IsZero() && at.After(previous) {
			t.Errorf("member work %q is newer than the one before it; list newest first, or the host's merge is out of order", w.GetTitle())
		}
		previous = at
	}
	token := resp.GetNextPageToken()
	if len(token) > pluginapi.MaxWorkPageToken {
		t.Errorf("ListMemberWorks: next_page_token is longer than %d; the host treats it as the end", pluginapi.MaxWorkPageToken)
	} else if token != "" {
		if _, err := list(token); err != nil {
			t.Errorf("ListMemberWorks(next page): %v", err)
		}
	}
}

// checkRefresh asks a refreshable row for two other batches, the second the largest value: any batch must be answered, with the
// same row.
func checkRefresh(t *testing.T, where, row string, get func(*pluginv1.GetDiscoverPageRequest) (*pluginv1.DiscoverPage, error)) {
	t.Helper()
	for _, batch := range []uint32{1, math.MaxUint32} {
		page, err := get(&pluginv1.GetDiscoverPageRequest{RowId: row, Batch: batch})
		switch {
		case err != nil:
			t.Errorf("%s: refreshable row %q, batch %d: %v; every batch must be answered", where, row, batch, err)
		case len(page.GetRows()) != 1 || page.GetRows()[0].GetId() != row:
			t.Errorf("%s: refreshable row %q, batch %d: %d rows; answer with the same row", where, row, batch, len(page.GetRows()))
		}
	}
}
