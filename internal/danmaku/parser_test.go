package danmaku

import "testing"

func TestParseName(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		parent  string
		query   string
		season  *int
		episode *int
	}{
		{
			name:    "season episode release name",
			file:    "Show.Name.S02E07.1080p.WEB-DL.mkv",
			parent:  "Show Name",
			query:   "Show Name",
			season:  intPointerValue(2),
			episode: intPointerValue(7),
		},
		{
			name:    "chinese episode and parent season",
			file:    "孤独摇滚 第3集 [1080p].mkv",
			parent:  "孤独摇滚 Season 1",
			query:   "孤独摇滚",
			season:  intPointerValue(1),
			episode: intPointerValue(3),
		},
		{
			name:    "special episode",
			file:    "Series S00E02.mkv",
			query:   "Series",
			season:  intPointerValue(0),
			episode: intPointerValue(2),
		},
		{
			name:    "parent fallback for generic file",
			file:    "01.mkv",
			parent:  "Another Show",
			query:   "Another Show",
			episode: intPointerValue(1),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ParseName(test.file, test.parent)
			if got.Query != test.query {
				t.Fatalf("query = %q, want %q", got.Query, test.query)
			}
			assertOptionalInt(t, "season", got.SeasonNumber, test.season)
			assertOptionalInt(t, "episode", got.EpisodeNumber, test.episode)
		})
	}
}

func TestParseQueryRemovesEpisodeSuffix(t *testing.T) {
	got := ParseQuery("Series Name S02E03")
	if got.Query != "Series Name" {
		t.Fatalf("query = %q, want Series Name", got.Query)
	}
	assertOptionalInt(t, "season", got.SeasonNumber, intPointerValue(2))
	assertOptionalInt(t, "episode", got.EpisodeNumber, intPointerValue(3))
}

func TestParseNameDoesNotTreatResolutionAsEpisode(t *testing.T) {
	got := ParseName("Movie.2024.2160p.mkv", "Movies")
	if got.EpisodeNumber != nil {
		t.Fatalf("episode = %d, want nil", *got.EpisodeNumber)
	}
	if got.Query != "Movie 2024" {
		t.Fatalf("query = %q, want Movie 2024", got.Query)
	}
}

func assertOptionalInt(t *testing.T, name string, got, want *int) {
	t.Helper()
	if got == nil || want == nil {
		if got != nil || want != nil {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
		return
	}
	if *got != *want {
		t.Fatalf("%s = %d, want %d", name, *got, *want)
	}
}
