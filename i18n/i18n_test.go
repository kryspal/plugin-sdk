package i18n_test

import (
	"slices"
	"testing"

	"github.com/kryspal/plugin-sdk/i18n"
)

func TestChain(t *testing.T) {
	for lang, want := range map[string][]string{
		"":        {"en"},
		"en":      {"en"},
		"en-US":   {"en-us", "en"},
		"zh-CN":   {"zh-cn", "zh-tw", "en"},
		"zh_cn":   {"zh-cn", "zh-tw", "en"},
		"zh":      {"zh-cn", "zh-tw", "en"},
		"zh-Hans": {"zh-cn", "zh-tw", "en"},
		"zh-TW":   {"zh-tw", "zh-cn", "en"},
		"zh-HK":   {"zh-tw", "zh-cn", "en"},
		"zh-Hant": {"zh-tw", "zh-cn", "en"},
		"ja-JP":   {"ja-jp", "ja", "en"},
		"fr":      {"fr", "en"},
	} {
		if got := i18n.Chain(lang); !slices.Equal(got, want) {
			t.Errorf("Chain(%q) = %v, want %v", lang, got, want)
		}
	}
}

func TestTranslate(t *testing.T) {
	c := i18n.New(map[string]map[string]string{
		"en":    {"a": "A", "b": "B", "c": "C"},
		"zh-TW": {"a": "甲", "b": "乙"},
		"zh-CN": {"a": "甲", "c": "丙"},
		"ja":    {"a": "あ"},
	})
	for _, tc := range []struct{ lang, key, want string }{
		{"en", "a", "A"},
		{"", "b", "B"},
		{"zh-TW", "b", "乙"},
		{"zh-TW", "c", "丙"}, // missing in zh-TW → zh-CN
		{"zh-CN", "b", "乙"}, // missing in zh-CN → zh-TW
		{"zh-CN", "c", "丙"},
		{"ja-JP", "a", "あ"},
		{"ja-JP", "b", "B"}, // missing in Japanese → en
		{"de", "a", "A"},    // unknown language → en
		{"en", "missing", "missing"},
	} {
		if got := c.For(tc.lang).T(tc.key); got != tc.want {
			t.Errorf("For(%q).T(%q) = %q, want %q", tc.lang, tc.key, got, tc.want)
		}
	}
	if got := (i18n.Translator{}).T("x"); got != "x" {
		t.Errorf("zero Translator = %q", got)
	}
	c2 := i18n.New(map[string]map[string]string{"en": {"n": "%d items"}})
	if got := c2.For("en").Tf("n", 3); got != "3 items" {
		t.Errorf("Tf = %q", got)
	}
}
