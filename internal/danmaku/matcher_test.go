package danmaku

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSelectMatchPrefersExactTitleAndEpisode(t *testing.T) {
	candidates := []AnimeCandidate{
		{
			AnimeID:    1,
			AnimeTitle: "Series",
			Type:       "tvseries",
			Episodes: []EpisodeCandidate{
				{EpisodeID: 101, EpisodeTitle: "第1话"},
				{EpisodeID: 102, EpisodeTitle: "第2话"},
			},
		},
		{
			AnimeID:    2,
			AnimeTitle: "Series Movie",
			Type:       "movie",
			Episodes: []EpisodeCandidate{
				{EpisodeID: 201, EpisodeTitle: "Movie"},
			},
		},
	}
	status, match, episodeID := selectMatch(candidates, "Series", false, intPointerValue(1), intPointerValue(2))
	if status != MatchStatusMatched || match == nil || episodeID != 102 || match.AnimeID != 1 {
		t.Fatalf("status=%q match=%#v episodeID=%d", status, match, episodeID)
	}
}

func TestSelectMatchReturnsAmbiguousForMultipleExactCandidates(t *testing.T) {
	candidates := []AnimeCandidate{
		{AnimeID: 1, AnimeTitle: "Series", Episodes: []EpisodeCandidate{{EpisodeID: 101, EpisodeTitle: "第1话"}}},
		{AnimeID: 2, AnimeTitle: "Series", Episodes: []EpisodeCandidate{{EpisodeID: 201, EpisodeTitle: "第1话"}}},
	}
	status, match, _ := selectMatch(candidates, "Series", false, intPointerValue(1), intPointerValue(1))
	if status != MatchStatusAmbiguous || match != nil {
		t.Fatalf("status=%q match=%#v", status, match)
	}
}

func TestSelectMatchSupportsSpecials(t *testing.T) {
	candidates := []AnimeCandidate{{
		AnimeID:    1,
		AnimeTitle: "Series",
		Episodes: []EpisodeCandidate{
			{EpisodeID: 101, EpisodeTitle: "第1话"},
			{EpisodeID: 191, EpisodeTitle: "S1 Special"},
			{EpisodeID: 192, EpisodeTitle: "S2 OVA"},
		},
	}}
	status, match, episodeID := selectMatch(candidates, "Series", false, intPointerValue(0), intPointerValue(2))
	if status != MatchStatusMatched || match == nil || episodeID != 192 {
		t.Fatalf("status=%q match=%#v episodeID=%d", status, match, episodeID)
	}
}

func TestServiceSearchFallsBackToOriginalTitle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("anime")
		animes := []any{}
		if query == "Original Title" {
			animes = []any{map[string]any{
				"animeId":    9,
				"animeTitle": "Original Title",
				"type":       "tvseries",
				"episodes": []any{
					map[string]any{"episodeId": 901, "episodeTitle": "第1话"},
				},
			}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "animes": animes})
	}))
	defer server.Close()

	service := NewService(newTestClient(t, server))
	result, err := service.Search(context.Background(), SearchInput{
		Media: &MediaMetadata{
			ItemType:      "Episode",
			Name:          "Episode One",
			SeriesName:    "Primary Title",
			OriginalTitle: "Original Title",
			SeasonNumber:  intPointerValue(1),
			EpisodeNumber: intPointerValue(1),
		},
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.Query != "Original Title" || result.Status != MatchStatusMatched || result.EpisodeID != 901 {
		t.Fatalf("result = %#v", result)
	}
	if len(result.FallbackQueries) != 1 || result.FallbackQueries[0] != "Original Title" {
		t.Fatalf("fallback queries = %#v", result.FallbackQueries)
	}
}

func TestServiceSearchFallsBackWhenPrimaryCandidatesDoNotMatchEpisode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("anime")
		var animes []any
		switch query {
		case "Primary Title":
			animes = []any{map[string]any{
				"animeId":    1,
				"animeTitle": "Primary Title",
				"type":       "tvseries",
				"episodes": []any{
					map[string]any{"episodeId": 101, "episodeTitle": "第1话"},
				},
			}}
		case "Original Title":
			animes = []any{map[string]any{
				"animeId":    9,
				"animeTitle": "Original Title",
				"type":       "tvseries",
				"episodes": []any{
					map[string]any{"episodeId": 902, "episodeTitle": "第2话"},
				},
			}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "animes": animes})
	}))
	defer server.Close()

	service := NewService(newTestClient(t, server))
	result, err := service.Search(context.Background(), SearchInput{
		Media: &MediaMetadata{
			ItemType:      "Episode",
			SeriesName:    "Primary Title",
			OriginalTitle: "Original Title",
			SeasonNumber:  intPointerValue(1),
			EpisodeNumber: intPointerValue(2),
		},
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.Query != "Original Title" || result.Status != MatchStatusMatched || result.EpisodeID != 902 {
		t.Fatalf("result = %#v", result)
	}
}

func TestServiceSearchManualQueryOverridesStructuredEpisode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("anime"); got != "Manual Title" {
			t.Fatalf("anime query = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"animes": []any{map[string]any{
				"animeId":    3,
				"animeTitle": "Manual Title Season 2",
				"type":       "tvseries",
				"episodes": []any{
					map[string]any{"episodeId": 301, "episodeTitle": "第1话"},
					map[string]any{"episodeId": 302, "episodeTitle": "第2话"},
					map[string]any{"episodeId": 303, "episodeTitle": "第3话"},
				},
			}},
		})
	}))
	defer server.Close()

	service := NewService(newTestClient(t, server))
	result, err := service.Search(context.Background(), SearchInput{
		Query: "Manual Title S02E03",
		Media: &MediaMetadata{
			ItemType:      "Episode",
			SeriesName:    "Wrong Title",
			SeasonNumber:  intPointerValue(1),
			EpisodeNumber: intPointerValue(1),
		},
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.Status != MatchStatusMatched || result.EpisodeID != 303 || result.SeasonNumber == nil || *result.SeasonNumber != 2 {
		t.Fatalf("result = %#v", result)
	}
}
