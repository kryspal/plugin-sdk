package pluginapi

import "testing"

func TestValidRatingSource(t *testing.T) {
	for _, s := range []string{"trakt", "imdb", "rt", "douban", "my_source", "a-b", "x1"} {
		if !ValidRatingSource(s) {
			t.Errorf("ValidRatingSource(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "Trakt", " trakt", "-x", "_x", "a b", "a.b", "abcdefghijklmnopqrstuvwxyz0123456"} {
		if ValidRatingSource(s) {
			t.Errorf("ValidRatingSource(%q) = true, want false", s)
		}
	}
}
