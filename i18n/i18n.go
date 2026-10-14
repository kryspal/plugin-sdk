// Package i18n is a localization helper for plugin authors (change 0057): it picks a string table by the BCP 47 language
// tag the host sends, and when that language is missing it looks strings up along a fixed fallback chain that always ends
// in English. It depends only on the standard library.
//
// Usage:
//
//	var strings = i18n.New(map[string]map[string]string{
//		"en":    {"desc": "Example provider."},
//		"zh-TW": {"desc": "…"}, // Traditional Chinese text
//		"zh-CN": {"desc": "…"}, // Simplified Chinese text
//	})
//	// In Describe: t := strings.For(req.GetLanguage()); Description: t.T("desc")
//
// Fallback chain (Chain):
//   - zh-CN, zh-SG, zh-Hans, bare zh: zh-CN → zh-TW → en
//   - zh-TW, zh-HK, zh-MO, zh-Hant: zh-TW → zh-CN → en
//   - other languages: full tag → the language itself (ja-JP → ja) → en
//   - empty string: en
package i18n

import (
	"fmt"
	"strings"
)

// Fallback is the last fallback language. A catalog must have at least this language.
const Fallback = "en"

// Chain returns the lookup order for a language tag (lowercase, hyphen-separated); the last entry is always Fallback.
func Chain(lang string) []string {
	tag := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(lang), "_", "-"))
	if tag == "" || tag == Fallback {
		return []string{Fallback}
	}
	base, _, _ := strings.Cut(tag, "-")
	if base == "zh" {
		if traditional(tag) {
			return []string{"zh-tw", "zh-cn", Fallback}
		}
		return []string{"zh-cn", "zh-tw", Fallback}
	}
	out := []string{tag}
	if base != tag {
		out = append(out, base)
	}
	if base != Fallback {
		out = append(out, Fallback)
	}
	return out
}

// traditional reports whether a Chinese tag means Traditional Chinese: region TW, HK or MO, or script Hant.
func traditional(tag string) bool {
	for _, p := range strings.Split(tag, "-")[1:] {
		switch p {
		case "tw", "hk", "mo", "hant":
			return true
		}
	}
	return false
}

// Catalog holds the string tables of each language: language tag → key → text. Tags are case-insensitive (zh-TW and
// zh-tw are the same).
type Catalog struct {
	tables map[string]map[string]string
}

// New creates a Catalog. tables is not copied; do not modify it afterwards.
func New(tables map[string]map[string]string) *Catalog {
	c := &Catalog{tables: make(map[string]map[string]string, len(tables))}
	for tag, t := range tables {
		c.tables[strings.ToLower(strings.ReplaceAll(tag, "_", "-"))] = t
	}
	return c
}

// Tags returns the language tags that have a string table (lowercase).
func (c *Catalog) Tags() []string {
	out := make([]string, 0, len(c.tables))
	for tag := range c.tables {
		out = append(out, tag)
	}
	return out
}

// For returns the translator for a language.
func (c *Catalog) For(lang string) Translator {
	return Translator{c: c, chain: Chain(lang)}
}

// Translator looks strings up along the fallback chain. The zero value returns only keys.
type Translator struct {
	c     *Catalog
	chain []string
}

// T returns the text for key: the first language along the fallback chain that has the key; when none has it, T returns
// the key itself (so a missing translation is obvious at a glance).
func (t Translator) T(key string) string {
	if t.c == nil {
		return key
	}
	for _, tag := range t.chain {
		if s, ok := t.c.tables[tag][key]; ok {
			return s
		}
	}
	return key
}

// Tf is fmt.Sprintf(t.T(key), args...).
func (t Translator) Tf(key string, args ...any) string {
	return fmt.Sprintf(t.T(key), args...)
}
