package pluginapi

import (
	"strings"
	"testing"
)

func TestValidWorkIdentifier(t *testing.T) {
	for s, want := range map[string]bool{
		"a": true, "ask_again": true, "a1": true, strings.Repeat("a", 48): true,
		"": false, "1a": false, "_a": false, "A": false, "a-b": false, "a b": false, strings.Repeat("a", 49): false,
	} {
		if got := ValidWorkIdentifier(s); got != want {
			t.Errorf("ValidWorkIdentifier(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestValidWorkKey(t *testing.T) {
	for s, want := range map[string]bool{
		"movie:42": true, "a/b?c d": true, "作品": true, strings.Repeat("字", MaxWorkKey): true,
		"": false, "a\nb": false, "a\x00b": false, "\xff": false, strings.Repeat("a", MaxWorkKey+1): false,
	} {
		if got := ValidWorkKey(s); got != want {
			t.Errorf("ValidWorkKey(%q) = %v, want %v", s, got, want)
		}
	}
	if ValidWorkChoiceValue(strings.Repeat("a", MaxWorkChoiceValue+1)) || !ValidWorkChoiceValue("season_2") {
		t.Error("ValidWorkChoiceValue does not follow its limit")
	}
}
