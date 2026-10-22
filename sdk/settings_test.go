package sdk_test

import (
	"sync"
	"testing"

	pluginv1 "github.com/kryspal/plugin-sdk/pluginv1"
	"github.com/kryspal/plugin-sdk/sdk"
)

func library(overrides map[string]string) *pluginv1.LibraryContext {
	return &pluginv1.LibraryContext{LibraryId: "1", Overrides: overrides}
}

func TestSettingsOverridesWinByKeyPresence(t *testing.T) {
	var s sdk.Settings
	s.Store(map[string]string{"lang": "en", "adult": "false", "cast": "20", "types": `["poster"]`, "group": "anime"})

	e := s.For(library(map[string]string{"lang": "", "adult": "true", "cast": "not a number", "group": "gone"}))
	if got := e.String("lang", "def"); got != "" {
		t.Errorf("an empty override must win over the global value, got %q", got)
	}
	if !e.Bool("adult", false) {
		t.Error("bool override ignored")
	}
	if got := e.Int("cast", 5); got != 20 {
		t.Errorf("an override that is not an int must fall back to the global value, got %d", got)
	}
	if got := e.Strings("types", nil); len(got) != 1 || got[0] != "poster" {
		t.Errorf("Strings = %v", got)
	}
	if got := e.Select("group", []string{"anime", "general"}, "general"); got != "anime" {
		t.Errorf("a removed option must fall back to the global value, got %q", got)
	}
	if got := e.String("missing", "def"); got != "def" {
		t.Errorf("missing key = %q, want the default", got)
	}
	if !e.Overridden("lang") || e.Overridden("types") {
		t.Error("Overridden is wrong")
	}

	g := s.For(nil)
	if got := g.String("lang", "def"); got != "en" {
		t.Errorf("no library must give the global value, got %q", got)
	}
	if g.CacheKey() != "" {
		t.Error("no overrides must give an empty cache key")
	}
}

func TestSettingsCacheKeyIsStableAndDistinct(t *testing.T) {
	var s sdk.Settings
	a := s.For(library(map[string]string{"x": "1", "y": "2"})).CacheKey()
	b := s.For(library(map[string]string{"y": "2", "x": "1"})).CacheKey()
	c := s.For(library(map[string]string{"x": "12"})).CacheKey()
	d := s.For(library(map[string]string{"x1": "2"})).CacheKey()
	if a == "" || a != b {
		t.Errorf("cache key must not depend on map order: %q %q", a, b)
	}
	if c == d {
		t.Errorf("different overrides share a cache key: %q", c)
	}
}

func TestSettingsPreviewDoesNotStore(t *testing.T) {
	var s sdk.Settings
	s.Store(map[string]string{"lang": "en"})
	p := s.Preview(map[string]string{"lang": "ja"}, library(map[string]string{"cast": "3"}))
	if p.String("lang", "") != "ja" || p.Int("cast", 0) != 3 {
		t.Error("Preview must use the given values and overrides")
	}
	if s.For(nil).String("lang", "") != "en" {
		t.Error("Preview changed the stored values")
	}
}

func TestSettingsConcurrentLibrariesDoNotMix(t *testing.T) {
	var s sdk.Settings
	s.Store(map[string]string{"lang": "en"})
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if got := s.For(library(map[string]string{"lang": "ja"})).String("lang", ""); got != "ja" {
				t.Errorf("library A got %q", got)
			}
		}()
		go func() {
			defer wg.Done()
			if got := s.For(library(map[string]string{"lang": "zh"})).String("lang", ""); got != "zh" {
				t.Errorf("library B got %q", got)
			}
			if i%10 == 0 {
				s.Store(map[string]string{"lang": "en"})
			}
		}()
	}
	wg.Wait()
}

func TestSettingsFallBackToDeclaredDefaults(t *testing.T) {
	s := sdk.NewSettings([]*pluginv1.ConfigField{
		{Key: "cast", DefaultValue: "15"},
		{Key: "lang", DefaultValue: "original"},
	})
	s.Store(map[string]string{"lang": "ja"})
	e := s.For(library(map[string]string{"cast": "junk"}))
	if got := e.Int("cast", 5); got != 15 {
		t.Errorf("an unusable override with no global value must fall back to the declared default, got %d", got)
	}
	if got := e.String("lang", "x"); got != "ja" {
		t.Errorf("the global value must win over the declared default, got %q", got)
	}
	if got := e.String("missing", "x"); got != "x" {
		t.Errorf("no value anywhere must give def, got %q", got)
	}
	if e.LibraryID() != "1" || s.For(nil).LibraryID() != "" {
		t.Error("LibraryID is wrong")
	}
}
