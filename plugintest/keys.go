package plugintest

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"google.golang.org/grpc"

	pluginv1 "github.com/kryspal/plugin-sdk/pluginv1"
)

// checkOwnIDKey checks where an entry keeps its own ID in provider_ids (PROTOCOL.md §7): under the plugin ID,
// lowercased. The host finds "the ID this plugin can look up" by that key; the same ID under another key (a short name)
// makes the entry look as if it had none. required says the entry must carry the key; otherwise a key that is there
// only has to be right, and the ID may not sit under another key (candidates may leave provider_ids empty).
func checkOwnIDKey(t *testing.T, what, pluginID, ownID string, ids map[string]string, required bool) {
	t.Helper()
	if ownID == "" {
		return
	}
	key := strings.ToLower(pluginID)
	if got, ok := ids[key]; ok && got == ownID {
		return
	}
	hint := ""
	for k, v := range ids {
		if k != key && v == ownID {
			hint = "; the ID is there under the key " + strconv.Quote(k) + ", which the host does not look at"
		}
	}
	if _, ok := ids[key]; !ok && !required && hint == "" {
		return
	}
	t.Errorf("%s: provider_ids = %v; want the entry's own ID under the plugin ID, %q: %q%s", what, ids, key, ownID, hint)
}

// checkSearchPerson runs SearchPerson for the name and checks its candidates.
func checkSearchPerson(ctx context.Context, t *testing.T, conn *grpc.ClientConn, m *pluginv1.Manifest, pc *PersonSearchCase) {
	t.Helper()
	if !declares(m, pluginv1.CapabilityMetadataPersonSearch) {
		t.Fatalf("the manifest does not declare %s", pluginv1.CapabilityMetadataPersonSearch)
	}
	res, err := call(ctx, searchTimeout, func(cctx context.Context) (*pluginv1.SearchPersonResponse, error) {
		return pluginv1.NewMetadataProviderClient(conn).SearchPerson(cctx, &pluginv1.SearchPersonRequest{Name: pc.Name})
	})
	if err != nil {
		t.Fatalf("SearchPerson(%q): %v", pc.Name, err)
	}
	if len(res.GetCandidates()) == 0 {
		t.Fatalf("SearchPerson(%q) found nothing", pc.Name)
	}
	for i, c := range res.GetCandidates() {
		p := c.GetPerson()
		if p.GetId() == "" || p.GetName() == "" {
			t.Errorf("SearchPerson(%q) candidate %d: id %q name %q; both are required", pc.Name, i, p.GetId(), p.GetName())
		}
		checkOwnIDKey(t, "SearchPerson candidate "+strconv.Itoa(i), m.GetId(), p.GetId(), p.GetProviderIds(), true)
	}
}

// maxSubtitleComment is the longest SubtitleCandidate.comment the host keeps (subtitle.proto).
const maxSubtitleComment = 200

// checkSubtitles runs the subtitle Search for the case and checks what the host depends on: IDs, languages, a known
// way of matching, ranges, and a limit that is kept.
func checkSubtitles(ctx context.Context, t *testing.T, conn *grpc.ClientConn, m *pluginv1.Manifest, sc *SubtitleCase) {
	t.Helper()
	if !declares(m, pluginv1.CapabilitySubtitlesSearch) {
		t.Fatalf("the manifest does not declare %s", pluginv1.CapabilitySubtitlesSearch)
	}
	langs := sc.Languages
	if len(langs) == 0 {
		langs = []string{"en"}
	}
	const limit = 3
	target := &pluginv1.SubtitleTarget{Type: sc.Type, Title: sc.Title, Year: sc.Year, ProviderIds: sc.ProviderIDs,
		OriginalTitle: sc.OriginalTitle, ProductCode: sc.ProductCode}
	if sc.Type == pluginv1.ItemType_ITEM_TYPE_EPISODE {
		target.SeasonNumber, target.EpisodeNumber = &sc.Season, &sc.Episode
	}
	res, err := call(ctx, subtitleSearchTimeout, func(cctx context.Context) (*pluginv1.SearchSubtitlesResponse, error) {
		return pluginv1.NewSubtitleProviderClient(conn).Search(cctx, &pluginv1.SearchSubtitlesRequest{
			Target: target, Languages: langs, Limit: limit,
		})
	})
	if err != nil {
		t.Fatalf("subtitle Search(%q): %v", sc.Title, err)
	}
	if len(res.GetCandidates()) == 0 {
		t.Fatalf("subtitle Search(%q) found nothing", sc.Title)
	}
	if n := len(res.GetCandidates()); n > limit {
		t.Errorf("subtitle Search returned %d candidates for limit %d", n, limit)
	}
	if res.QuotaRemaining != nil && res.GetQuotaRemaining() < -1 {
		t.Errorf("SearchSubtitlesResponse.quota_remaining = %d; use -1 or leave it unset when unknown", res.GetQuotaRemaining())
	}
	seen := map[string]bool{}
	for i, c := range res.GetCandidates() {
		at := "subtitle candidate " + strconv.Itoa(i)
		switch {
		case c.GetId() == "":
			t.Errorf("%s: id is empty", at)
		case seen[c.GetId()]:
			t.Errorf("%s: id %q appears twice", at, c.GetId())
		}
		seen[c.GetId()] = true
		if c.GetLanguage() == "" {
			t.Errorf("%s: language is empty (BCP 47)", at)
		}
		if f := c.GetFormat(); f == "" || f != strings.ToLower(f) || strings.HasPrefix(f, ".") {
			t.Errorf("%s: format %q; want a lowercase extension without a dot, for example srt", at, f)
		}
		if c.GetMatchedBy() == pluginv1.SubtitleCandidate_MATCHED_BY_UNSPECIFIED {
			t.Errorf("%s: matched_by is not set; the host ranks by hash > ids > name", at)
		}
		if s := c.GetScore(); s < 0 || s > 1 {
			t.Errorf("%s: score %v is outside 0–1", at, s)
		}
		if r := c.GetRating(); r < 0 || r > 10 {
			t.Errorf("%s: rating %v is outside 0–10", at, r)
		}
		if n := utf8.RuneCountInString(c.GetComment()); n > maxSubtitleComment {
			t.Errorf("%s: comment has %d characters; at most %d", at, n, maxSubtitleComment)
		}
	}
}

// checkAutoFillLimit checks the reserved key autofill_daily_limit (PROTOCOL.md §7.2): only plugins that declare
// subtitles.search have it, it is an int whose default is 0–1000 (anything else makes the host use 10), and a plugin
// declares it only on a host that lists the feature autofill_limit.
func checkAutoFillLimit(t *testing.T, m *pluginv1.Manifest, hostHasFeature bool) {
	t.Helper()
	var f *pluginv1.ConfigField
	for _, x := range m.GetConfigFields() {
		if x.GetKey() == autoFillLimitKey {
			f = x
		}
	}
	if f == nil {
		return
	}
	if !declares(m, pluginv1.CapabilitySubtitlesSearch) {
		t.Errorf("field %q is declared without %s; the host adds it only for subtitle providers", autoFillLimitKey, pluginv1.CapabilitySubtitlesSearch)
	}
	if f.GetType() != pluginv1.ConfigFieldType_CONFIG_FIELD_TYPE_INT {
		t.Errorf("field %q has type %v; it must be an int (the host takes only its default)", autoFillLimitKey, f.GetType())
	}
	if n, err := strconv.Atoi(f.GetDefaultValue()); err != nil || n < 0 || n > 1000 {
		t.Errorf("field %q: default %q is not an integer from 0 to 1000, so the host uses 10 (0 means no limit)", autoFillLimitKey, f.GetDefaultValue())
	}
	if !hostHasFeature {
		t.Errorf("field %q is declared although the host does not list the feature autofill_limit; an older host would show a setting that does nothing", autoFillLimitKey)
	}
}

// autoFillLimitKey is the setting key the host reserves for the daily limit of automatic subtitle downloads.
const autoFillLimitKey = "autofill_daily_limit"

// hostListsFeature reports whether the host the test imitates lists the feature: KRYSPAL_HOST_FEATURES from
// Options.Env or Setup when set (an empty value imitates an older host), otherwise the default list.
func hostListsFeature(extra []string, feature string) bool {
	for _, kv := range extra {
		if v, ok := strings.CutPrefix(kv, "KRYSPAL_HOST_FEATURES="); ok {
			return slices.Contains(strings.Split(v, ","), feature)
		}
	}
	return slices.Contains(hostFeatures, feature)
}

// maxDanmaku is how many comments the host uses of an answer (PROTOCOL.md §7.5).
const (
	maxDanmaku         = 20000
	maxDanmakuTextRune = 500
)

// checkDanmaku runs GetDanmaku for the case and checks what the player depends on.
func checkDanmaku(ctx context.Context, t *testing.T, conn *grpc.ClientConn, m *pluginv1.Manifest, dc *DanmakuCase) {
	t.Helper()
	if !declares(m, pluginv1.CapabilityDanmakuProvider) {
		t.Fatalf("the manifest does not declare %s", pluginv1.CapabilityDanmakuProvider)
	}
	req := &pluginv1.GetDanmakuRequest{Type: dc.Type, Title: dc.Title, Year: dc.Year, ProviderIds: dc.ProviderIDs,
		OriginalTitle: dc.OriginalTitle, ProductCode: dc.ProductCode}
	if dc.Type == pluginv1.ItemType_ITEM_TYPE_EPISODE {
		req.SeasonNumber, req.EpisodeNumber = &dc.Season, &dc.Episode
	}
	res, err := call(ctx, danmakuTimeout, func(cctx context.Context) (*pluginv1.GetDanmakuResponse, error) {
		return pluginv1.NewDanmakuProviderClient(conn).GetDanmaku(cctx, req)
	})
	if err != nil {
		t.Fatalf("GetDanmaku(%q): %v", dc.Title, err)
	}
	if len(res.GetComments()) == 0 {
		t.Fatalf("GetDanmaku(%q) found nothing", dc.Title)
	}
	if n := len(res.GetComments()); n > maxDanmaku {
		t.Errorf("GetDanmaku returned %d comments; the host uses at most %d", n, maxDanmaku)
	}
	var last int64
	for i, c := range res.GetComments() {
		at := "danmaku comment " + strconv.Itoa(i)
		switch {
		case c.GetTimeMs() < 0:
			t.Errorf("%s: time_ms %d is negative", at, c.GetTimeMs())
		case c.GetTimeMs() < last:
			t.Errorf("%s: time_ms %d comes after %d; comments must be in increasing time order", at, c.GetTimeMs(), last)
		}
		last = max(last, c.GetTimeMs())
		if strings.TrimSpace(c.GetText()) == "" {
			t.Errorf("%s: text is empty", at)
		}
		if utf8.RuneCountInString(c.GetText()) > maxDanmakuTextRune {
			t.Errorf("%s: text has more than %d characters", at, maxDanmakuTextRune)
		}
		if c.GetColor() > 0xFFFFFF {
			t.Errorf("%s: color %#x is above 0xFFFFFF (0xRRGGBB; 0 means white)", at, c.GetColor())
		}
		if _, ok := pluginv1.DanmakuMode_name[int32(c.GetMode())]; !ok {
			t.Errorf("%s: mode %d is not a known DanmakuMode", at, c.GetMode())
		}
	}
}
