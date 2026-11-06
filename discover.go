package pluginapi

import (
	"unicode"
	"unicode/utf8"
)

// Limits of the discover.source and work.action capabilities (PROTOCOL.md §7.12). The host and the plugin test suite use
// the same numbers. Text lengths are in characters; the host cuts longer text.
const (
	MaxDiscoverRows     = 20  // rows per GetDiscoverPage answer
	MaxDiscoverTabs     = 8   // DiscoverSourceInfo.tabs
	MaxDiscoverFacts    = 8   // DiscoverPageHeader.facts
	MaxDiscoverCards    = 100 // cards per row page
	MaxDiscoverWorks    = 200 // works across all the rows of an answer
	MaxHeroWorks        = 10  // works in a hero row
	MaxWorksPerPage     = 50  // works per row page or search page
	MaxWorkSeasons      = 200 // seasons per work
	MaxWorkGenres       = 20  // Work.genres
	MaxWorkCompanies    = 20  // Work.companies
	MaxWorkCredits      = 30  // Work.credits
	MaxWorkLinks        = 4   // Work.external_links
	MaxWorkBadges       = 4   // Work.badges
	MaxDiscoverSeeds    = 50  // GetDiscoverPageRequest.seeds, both origins
	MaxLibrarySeeds     = 20  // GetDiscoverPageRequest.seeds from the library
	MaxWatchedSeeds     = 30  // GetDiscoverPageRequest.seeds from what the member watched
	MaxDiscoverSeedIDs  = 4   // Manifest.discover_seed_ids
	MaxWorkCornerBadges = 3   // Work.badges in one corner of the poster
	MaxWorkStates       = 50  // works per GetWorkStates request
	MaxWorkActions      = 8   // Manifest.work_actions
	MaxWorkRequiredIDs  = 8   // WorkActionDeclaration.required_provider_ids
	MaxWorkOptions      = 4   // WorkActionOffer.options
	MaxDiscoverFilters  = 8   // DiscoverPage.filters
	MaxMemberWorks      = 50  // works per ListMemberWorks answer
	MaxDiscoverName     = 48  // DiscoverSourceInfo.name
	MaxDiscoverRef      = 256 // every ref
	MaxWorkChoices      = 100 // WorkOption.choices
	MaxWorkKey          = 256 // Work.key
	MaxWorkPageToken    = 1024
	MaxWorkChoiceValue  = 64
	MaxWorkQuery        = 200
	MaxWorkTitle        = 256
	MaxWorkOverview     = 8000 // Work.overview, DiscoverPageHeader.overview
	MaxWorkRating       = 32   // Work.content_rating
	MaxWorkSeasonName   = 64
	MaxWorkTagline      = 256
	MaxWorkCaption      = 64
	MaxWorkProductCode  = 64
	MaxWorkBadgeText    = 8
	MaxWorkGenreName    = 64
	MaxWorkCompanyName  = 128
	MaxWorkCreditText   = 128 // WorkCredit.name and role
	MaxDiscoverTabName  = 32
	MaxDiscoverHeader   = 128 // DiscoverPageHeader.title and subtitle
	MaxDiscoverFactKey  = 32
	MaxDiscoverFact     = 128
	MaxDiscoverCard     = 64 // DiscoverCard.title
	MaxWorkRowTitle     = 64
	MaxWorkStatusText   = 32
	MaxWorkActionLabel  = 48
	MaxWorkOptionLabel  = 64
	MaxWorkChoiceLabel  = 64
	MaxWorkChoiceNote   = 32
	MaxWorkMessage      = 200
	MaxWorkLinkLabel    = 48
	maxWorkIdentifier   = 48
)

// ValidWorkIdentifier reports whether s is a valid DiscoverRow.id, WorkActionDeclaration.id or WorkOption.key: a lowercase
// letter, then lowercase letters, digits and underscores, at most 48 characters.
func ValidWorkIdentifier(s string) bool {
	if len(s) == 0 || len(s) > maxWorkIdentifier {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
		case (c >= '0' && c <= '9' || c == '_') && i > 0:
		default:
			return false
		}
	}
	return true
}

// ValidWorkKey reports whether s is a valid Work.key: 1 to 256 characters of valid UTF-8 with no control characters.
func ValidWorkKey(s string) bool { return validWorkToken(s, MaxWorkKey) }

// ValidWorkChoiceValue reports whether s is a valid WorkChoice.value: 1 to 64 characters of valid UTF-8 with no
// control characters.
func ValidWorkChoiceValue(s string) bool { return validWorkToken(s, MaxWorkChoiceValue) }

// ValidDiscoverRef reports whether s is a valid ref: 1 to 256 characters of valid UTF-8 with no control characters.
func ValidDiscoverRef(s string) bool { return validWorkToken(s, MaxDiscoverRef) }

func validWorkToken(s string, limit int) bool {
	if s == "" || !utf8.ValidString(s) || utf8.RuneCountInString(s) > limit {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
