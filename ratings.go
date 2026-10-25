package pluginapi

// MaxRatingSources is the most entries of Manifest.rating_sources the host uses (PROTOCOL.md §7.11); it ignores the rest.
const MaxRatingSources = 8

// ValidRatingSource reports whether s is a valid entry of Manifest.rating_sources and Rating.source written by a rating
// provider: 1 to 32 characters, lowercase ASCII letters and digits, '_' or '-', starting with a letter or digit.
// The host and the plugin test suite use the same rule.
func ValidRatingSource(s string) bool {
	if len(s) == 0 || len(s) > 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case (c == '_' || c == '-') && i > 0:
		default:
			return false
		}
	}
	return true
}
