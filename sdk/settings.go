package sdk

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"sync"

	pluginv1 "github.com/kryspal/plugin-sdk/pluginv1"
)

// Settings keeps a plugin's global configuration and gives each request its effective settings: the global values
// with the request's per-library overrides on top (host feature library_config).
//
// Store the values in Configure (not for a dry run), then read settings in a request through For:
//
//	func (p *plugin) Configure(ctx context.Context, req *pluginv1.ConfigureRequest) (*pluginv1.ConfigureResponse, error) {
//		if req.GetDryRun() {
//			return check(p.settings.Preview(req.GetValues(), req.GetLibrary()))
//		}
//		p.settings.Store(req.GetValues())
//		...
//	}
//
//	func (p *plugin) Search(ctx context.Context, req *pluginv1.SearchRequest) (*pluginv1.SearchResponse, error) {
//		s := p.settings.For(req.GetLibrary())
//		lang := s.String("title_language", "original")
//		...
//	}
//
// Never copy per-library values into the plugin's own fields: requests for different libraries run in parallel. A
// Settings is safe for concurrent use; the zero value has no values.
type Settings struct {
	mu       sync.RWMutex
	global   map[string]string
	defaults map[string]string
}

// NewSettings returns Settings whose getters fall back to the declared default_value of each field before the
// caller's def, so a default is written once, in the manifest.
func NewSettings(fields []*pluginv1.ConfigField) *Settings {
	d := map[string]string{}
	for _, f := range fields {
		if f.GetDefaultValue() != "" {
			d[f.GetKey()] = f.GetDefaultValue()
		}
	}
	return &Settings{defaults: d}
}

// Store replaces the global values with a copy of values (ConfigureRequest.values).
func (s *Settings) Store(values map[string]string) {
	c := maps.Clone(values)
	s.mu.Lock()
	s.global = c
	s.mu.Unlock()
}

// For returns the effective settings for a request. A nil library (older hosts, or requests that have no library,
// such as people, tasks and checks) gives the global values.
func (s *Settings) For(library *pluginv1.LibraryContext) Effective {
	s.mu.RLock()
	global := s.global
	s.mu.RUnlock()
	return s.newEffective(global, library)
}

// Preview returns the effective settings for a Configure dry run: values (the global values sent with the dry run)
// with library's overrides on top; a nil library previews the global values only. It does not change the stored
// values, so later requests see nothing of it; never call Store for a dry run.
func (s *Settings) Preview(values map[string]string, library *pluginv1.LibraryContext) Effective {
	return s.newEffective(values, library)
}

func (s *Settings) newEffective(global map[string]string, library *pluginv1.LibraryContext) Effective {
	return Effective{global: global, overrides: library.GetOverrides(), defaults: s.defaults, library: library.GetLibraryId()}
}

// Effective is a read-only view of the settings for one request. Typed getters try the library's override, then the
// global value, then the field's declared default (with NewSettings), then def, taking the first that fits the type,
// so an override that no longer fits (for example after an option was removed) falls back to the global value.
type Effective struct {
	global    map[string]string
	overrides map[string]string
	defaults  map[string]string
	library   string
}

// LibraryID returns the request's opaque library identifier ("" without one), for logs and cache keys only.
func (e Effective) LibraryID() string { return e.library }

// Lookup returns the effective raw value of key: the library's override when the key is present in it (even when
// empty), otherwise the global value, otherwise the declared default.
func (e Effective) Lookup(key string) (string, bool) {
	for _, m := range e.layers() {
		if v, ok := m[key]; ok {
			return v, true
		}
	}
	return "", false
}

func (e Effective) layers() [3]map[string]string {
	return [3]map[string]string{e.overrides, e.global, e.defaults}
}

// Overridden reports whether the library overrides key.
func (e Effective) Overridden(key string) bool {
	_, ok := e.overrides[key]
	return ok
}

// String returns the value of a string, secret, text or select field, or def when it has none.
func (e Effective) String(key, def string) string {
	if v, ok := e.Lookup(key); ok {
		return v
	}
	return def
}

// Select returns the value of a select field when it is one of allowed, otherwise the global value when that is
// allowed, otherwise def. Use it for options that can disappear, such as options taken from other settings.
func (e Effective) Select(key string, allowed []string, def string) string {
	for _, m := range e.layers() {
		if v, ok := m[key]; ok && slices.Contains(allowed, v) {
			return v
		}
	}
	return def
}

// Bool returns the value of a bool field, or def when it has none or the value is not true or false.
func (e Effective) Bool(key string, def bool) bool {
	return typed(e, key, def, func(v string) (bool, bool) {
		switch v {
		case "true":
			return true, true
		case "false":
			return false, true
		}
		return false, false
	})
}

// Int returns the value of an int field, or def when it has none or the value is not a decimal integer.
func (e Effective) Int(key string, def int64) int64 {
	return typed(e, key, def, func(v string) (int64, bool) {
		n, err := strconv.ParseInt(v, 10, 64)
		return n, err == nil
	})
}

// Strings returns the value of a multiselect, string_list or ordered_multiselect field (a JSON array of strings), or
// def when it has none or the value is not such an array.
func (e Effective) Strings(key string, def []string) []string {
	return typed(e, key, def, func(v string) ([]string, bool) {
		var out []string
		if json.Unmarshal([]byte(v), &out) != nil || out == nil {
			return nil, false
		}
		return out, true
	})
}

// typed reads the override, the global value and the declared default in turn, taking the first that parses.
func typed[T any](e Effective, key string, def T, parse func(string) (T, bool)) T {
	for _, m := range e.layers() {
		if v, ok := m[key]; ok {
			if t, ok := parse(v); ok {
				return t
			}
		}
	}
	return def
}

// CacheKey returns a stable digest of the library's overrides ("" when there are none). Add it to the key of any
// cache whose entries depend on settings that can be overridden per library, so two libraries with different
// overrides never share an entry; a plugin whose global settings change should clear such caches in Configure. An
// override equal to the global value still changes the key: such libraries share less, which is never wrong.
func (e Effective) CacheKey() string {
	if len(e.overrides) == 0 {
		return ""
	}
	keys := slices.Sorted(maps.Keys(e.overrides))
	h := sha256.New()
	for _, k := range keys {
		v := e.overrides[k]
		h.Write([]byte(strconv.Itoa(len(k))))
		h.Write([]byte{':'})
		h.Write([]byte(k))
		h.Write([]byte(strconv.Itoa(len(v))))
		h.Write([]byte{':'})
		h.Write([]byte(v))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}
