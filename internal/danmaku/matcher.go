package danmaku

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var (
	candidateSeasonPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bseason[\s._-]*(\d{1,2})\b`),
		regexp.MustCompile(`(?i)(\d{1,2})(?:st|nd|rd|th)[\s._-]*season\b`),
		regexp.MustCompile(`(?i)(?:^|[\s._-])s(\d{1,2})(?:$|[\s._-])`),
		regexp.MustCompile(`第\s*([0-9一二三四五六七八九十百]+)\s*季`),
	}
	candidateEpisodePatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bS(\d{1,2})E(\d{1,3})\b`),
		regexp.MustCompile(`第\s*([0-9一二三四五六七八九十百]+)\s*[话話集回]`),
		regexp.MustCompile(`(?i)(?:^|[\s._-])(?:e|ep|episode)[\s._-]*(\d{1,3})(?:$|[\s._-])`),
	}
	specialEpisodePattern = regexp.MustCompile(`(?i)^\s*S(\d{1,3})(?:\s|$)`)
)

type Service struct {
	client *Client
}

func NewService(client *Client) *Service {
	return &Service{client: client}
}

func (s *Service) Comments(ctx context.Context, episodeID int64) (*CommentsResult, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("danmaku service is not configured")
	}
	result, err := s.client.Comments(ctx, episodeID)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *Service) Search(ctx context.Context, input SearchInput) (*SearchResult, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("danmaku service is not configured")
	}

	media := input.Media
	if media == nil {
		media = &MediaMetadata{}
	}
	manual := ParseQuery(input.Query)
	parsedMedia := ParseName(media.Name, media.ParentName)

	season := firstInt(manual.SeasonNumber, media.SeasonNumber, parsedMedia.SeasonNumber)
	episode := firstInt(manual.EpisodeNumber, media.EpisodeNumber, parsedMedia.EpisodeNumber)
	itemType := strings.ToLower(strings.TrimSpace(media.ItemType))
	isMovie := itemType == "movie"
	if isMovie {
		season = nil
		episode = nil
	}

	titleCandidates := titleCandidates(media, parsedMedia.Query, manual, isMovie)
	result := &SearchResult{
		SuggestedQuery: suggestedQuery(media, parsedMedia.Query, season, episode, isMovie),
		SeasonNumber:   cloneIntPointer(season),
		EpisodeNumber:  cloneIntPointer(episode),
		Status:         MatchStatusNotFound,
		Candidates:     []AnimeCandidate{},
	}
	if manual.Query != "" {
		result.SuggestedQuery = strings.TrimSpace(input.Query)
	}
	if len(titleCandidates) == 0 {
		return result, nil
	}

	seenTitles := make(map[string]struct{})
	for index, title := range titleCandidates {
		normalizedTitle := strings.TrimSpace(title)
		key := strings.ToLower(normalizedTitle)
		if normalizedTitle == "" {
			continue
		}
		if _, ok := seenTitles[key]; ok {
			continue
		}
		seenTitles[key] = struct{}{}
		if index > 0 {
			result.FallbackQueries = append(result.FallbackQueries, normalizedTitle)
		}

		candidates, err := s.client.SearchEpisodes(ctx, normalizedTitle)
		if err != nil {
			return nil, err
		}
		if len(candidates) == 0 {
			continue
		}
		result.Query = normalizedTitle
		result.Candidates = candidates
		result.Status, result.Match, result.EpisodeID = selectMatch(candidates, normalizedTitle, isMovie, season, episode)
		if result.Status != MatchStatusNotFound {
			return result, nil
		}
	}

	return result, nil
}

func titleCandidates(media *MediaMetadata, parsedQuery string, manual parsedName, isMovie bool) []string {
	if manual.Query != "" {
		return []string{manual.Query}
	}
	var candidates []string
	if isMovie {
		candidates = append(candidates, media.Name, media.OriginalTitle)
	} else {
		candidates = append(candidates, media.SeriesName, media.OriginalTitle, media.Name)
	}
	candidates = append(candidates, parsedQuery)
	return candidates
}

func suggestedQuery(media *MediaMetadata, parsedQuery string, season, episode *int, isMovie bool) string {
	code := FormatEpisodeCode(season, episode)
	var title string
	if isMovie {
		title = strings.TrimSpace(media.Name)
		if title == "" {
			title = strings.TrimSpace(media.OriginalTitle)
		}
	} else {
		title = strings.TrimSpace(media.SeriesName)
		if title == "" {
			title = strings.TrimSpace(media.OriginalTitle)
		}
		if title == "" {
			title = strings.TrimSpace(media.Name)
		}
	}
	if title == "" {
		title = parsedQuery
	}
	if code != "" {
		title += " " + code
	}
	return strings.TrimSpace(title)
}

func selectMatch(candidates []AnimeCandidate, requestedTitle string, isMovie bool, season, episode *int) (string, *MatchedEpisode, int64) {
	type candidateMatch struct {
		score   int
		anime   AnimeCandidate
		episode EpisodeCandidate
	}
	var matches []candidateMatch
	seenEpisodes := make(map[int64]struct{})

	for _, candidate := range candidates {
		if candidate.AnimeID <= 0 || len(candidate.Episodes) == 0 {
			continue
		}
		score := titleScore(requestedTitle, candidate.AnimeTitle)
		if score == 0 {
			continue
		}
		candidateSeason := extractCandidateSeason(candidate.AnimeTitle)
		if !seasonCompatible(season, candidateSeason) {
			continue
		}

		var episodeIndex = -1
		if isMovie {
			episodeIndex = 0
		} else {
			episodeIndex = findEpisodeIndex(candidate.Episodes, season, episode)
		}
		if episodeIndex < 0 || episodeIndex >= len(candidate.Episodes) {
			continue
		}
		matchedEpisode := candidate.Episodes[episodeIndex]
		if matchedEpisode.EpisodeID <= 0 {
			continue
		}
		key := matchedEpisode.EpisodeID
		if _, ok := seenEpisodes[key]; ok {
			continue
		}
		seenEpisodes[key] = struct{}{}
		matches = append(matches, candidateMatch{
			score:   score,
			anime:   candidate,
			episode: matchedEpisode,
		})
	}

	if len(matches) == 1 {
		return matchResult(matches[0].anime, matches[0].episode)
	}
	if len(matches) == 0 {
		return MatchStatusNotFound, nil, 0
	}

	bestScore := matches[0].score
	for _, match := range matches[1:] {
		if match.score > bestScore {
			bestScore = match.score
		}
	}
	var best []candidateMatch
	for _, match := range matches {
		if match.score == bestScore {
			best = append(best, match)
		}
	}
	if len(best) == 1 && bestScore >= 80 {
		return matchResult(best[0].anime, best[0].episode)
	}
	return MatchStatusAmbiguous, nil, 0
}

func matchResult(anime AnimeCandidate, episode EpisodeCandidate) (string, *MatchedEpisode, int64) {
	return MatchStatusMatched, &MatchedEpisode{
		AnimeID:         anime.AnimeID,
		AnimeTitle:      anime.AnimeTitle,
		AnimeType:       anime.Type,
		TypeDescription: anime.TypeDescription,
		EpisodeID:       episode.EpisodeID,
		EpisodeTitle:    episode.EpisodeTitle,
	}, episode.EpisodeID
}

func titleScore(requested, candidate string) int {
	requestedNorm := normalizeMatchTitle(stripSeasonSuffix(requested))
	candidateNorm := normalizeMatchTitle(stripSeasonSuffix(candidate))
	if requestedNorm == "" || candidateNorm == "" {
		return 0
	}
	if requestedNorm == candidateNorm {
		return 100
	}
	if strings.HasPrefix(candidateNorm, requestedNorm) || strings.HasPrefix(requestedNorm, candidateNorm) {
		return 80
	}
	return 0
}

func normalizeMatchTitle(value string) string {
	var builder strings.Builder
	lastSpace := false
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			builder.WriteRune(r)
			lastSpace = false
			continue
		}
		if !lastSpace {
			builder.WriteByte(' ')
			lastSpace = true
		}
	}
	return strings.Join(strings.Fields(builder.String()), " ")
}

func stripSeasonSuffix(value string) string {
	value = chineseSeasonPattern.ReplaceAllString(value, " ")
	for _, pattern := range candidateSeasonPatterns {
		value = pattern.ReplaceAllString(value, " ")
	}
	return strings.TrimSpace(value)
}

func extractCandidateSeason(title string) *int {
	for _, pattern := range candidateSeasonPatterns {
		match := pattern.FindStringSubmatch(title)
		if len(match) < 2 {
			continue
		}
		if number := chineseNumberPointer(match[1]); number != nil {
			return number
		}
	}
	return nil
}

func seasonCompatible(requested, candidate *int) bool {
	if requested == nil {
		return true
	}
	if *requested == 0 {
		return true
	}
	if candidate == nil {
		return *requested == 1
	}
	return *requested == *candidate
}

func findEpisodeIndex(episodes []EpisodeCandidate, season, episode *int) int {
	if episode == nil {
		if len(episodes) == 1 {
			return 0
		}
		return -1
	}
	requestedEpisode := *episode
	if season != nil && *season == 0 {
		for index, candidate := range episodes {
			match := specialEpisodePattern.FindStringSubmatch(strings.TrimSpace(candidate.EpisodeTitle))
			if len(match) < 2 {
				continue
			}
			if number, err := strconv.Atoi(match[1]); err == nil && number == requestedEpisode {
				return index
			}
		}
	}

	for index, candidate := range episodes {
		for _, number := range extractEpisodeNumbers(candidate.EpisodeTitle) {
			if number == requestedEpisode {
				return index
			}
		}
	}
	if requestedEpisode == 1 && len(episodes) > 0 {
		if len(extractEpisodeNumbers(episodes[0].EpisodeTitle)) == 0 {
			return 0
		}
	}
	return -1
}

func extractEpisodeNumbers(title string) []int {
	var numbers []int
	for _, pattern := range candidateEpisodePatterns {
		for _, match := range pattern.FindAllStringSubmatch(title, -1) {
			if len(match) < 2 {
				continue
			}
			value := match[len(match)-1]
			if number := chineseNumberPointer(value); number != nil {
				numbers = append(numbers, *number)
			}
		}
	}
	return numbers
}

func firstInt(values ...*int) *int {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func cloneIntPointer(value *int) *int {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}
