package main

import (
	"strings"
	"testing"
	"unicode"

	pluginv1 "github.com/kryspal/plugin-sdk/pluginv1"
)

// manifestStrings collects every human-readable string in a Manifest.
func manifestStrings(m *pluginv1.Manifest) []string {
	out := []string{m.GetName(), m.GetDescription(), m.GetAttribution().GetText()}
	for _, g := range m.GetConfigGroups() {
		out = append(out, g.GetTitle())
	}
	for _, f := range m.GetConfigFields() {
		out = append(out, f.GetLabel(), f.GetDescription(), f.GetLinkLabel())
		for _, o := range f.GetOptions() {
			out = append(out, o.GetLabel())
		}
	}
	return out
}

func hasCJK(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return unicode.Is(unicode.Han, r) }) >= 0
}

// TestDescribeLanguages: each language's manifest and error messages contain only text in that language (English has no Han
// characters; Simplified Chinese has no Traditional-only characters, and vice versa), and unknown languages fall back to
// English.
func TestDescribeLanguages(t *testing.T) {
	const traditionalOnly = "金鑰請這資語進階動態電影設選項個"
	const simplifiedOnly = "钥请这资语进阶动态电影设选项个"
	p := &provider{}
	for _, tc := range []struct {
		lang string
		ok   func(string) bool
	}{
		{"en", func(s string) bool { return !hasCJK(s) }},
		{"", func(s string) bool { return !hasCJK(s) }},
		{"fr", func(s string) bool { return !hasCJK(s) }}, // unknown language → English
		{"zh-TW", func(s string) bool { return !strings.ContainsAny(s, simplifiedOnly) }},
		{"zh-CN", func(s string) bool { return !strings.ContainsAny(s, traditionalOnly) }},
	} {
		m, err := p.Describe(t.Context(), &pluginv1.DescribeRequest{Language: tc.lang})
		if err != nil {
			t.Fatal(err)
		}
		all := manifestStrings(m)
		resp, err := p.Configure(t.Context(), &pluginv1.ConfigureRequest{Values: map[string]string{"api_key": "wrong", "language_mode": "x"}, Language: tc.lang})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range resp.GetFieldErrors() {
			all = append(all, e)
		}
		for _, s := range all {
			if s != "" && !tc.ok(s) {
				t.Errorf("lang %q: unexpected text %q", tc.lang, s)
			}
		}
		if tc.lang == "zh-CN" && m.GetDescription() != "返回固定数据的示例提供者。" || tc.lang == "zh-TW" && m.GetDescription() != "回傳固定資料的範例提供者。" {
			t.Errorf("lang %q: description %q", tc.lang, m.GetDescription())
		}
	}
}
