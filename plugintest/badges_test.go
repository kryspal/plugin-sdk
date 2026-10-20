package plugintest

import (
	"strings"
	"testing"

	pluginv1 "github.com/kryspal/plugin-sdk/pluginv1"
)

func TestBadgeSuggestionProblems(t *testing.T) {
	good := []*pluginv1.SuggestedBadgeRule{
		{Tag: "Director's Cut", Text: "DC"},
		{Tag: "中字", Text: "中字", Color: "#ff8800"},
	}
	if got := badgeSuggestionProblems(good); len(got) != 0 {
		t.Fatalf("valid rules reported %v", got)
	}
	bad := []*pluginv1.SuggestedBadgeRule{
		{Text: "X"},
		{Tag: " x", Text: "X"},
		{Tag: strings.Repeat("x", 33), Text: "X"},
		{Tag: "x", Text: "TOO LONG TEXT"},
		{Tag: "x", Text: "X\n"},
		{Tag: "x", Text: "X", Color: "red"},
	}
	if got := badgeSuggestionProblems(bad); len(got) != len(bad) {
		t.Fatalf("got %d problems, want %d: %v", len(got), len(bad), got)
	}
	many := make([]*pluginv1.SuggestedBadgeRule, maxBadgeSuggestions+1)
	for i := range many {
		many[i] = &pluginv1.SuggestedBadgeRule{Tag: "x", Text: "X"}
	}
	if got := badgeSuggestionProblems(many); len(got) != 1 {
		t.Fatalf("too many rules = %v, want one problem", got)
	}
}
