package danmaku

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var (
	seasonEpisodePattern   = regexp.MustCompile(`(?i)(?:^|[\s._\-\[\]()])(?:s(\d{1,2})[\s._-]*e(\d{1,3})|(\d{1,2})x(\d{1,3}))(?:$|[\s._\-\[\]()])`)
	chineseSeasonPattern   = regexp.MustCompile(`第\s*([0-9一二三四五六七八九十百]+)\s*季`)
	chineseEpisodePattern  = regexp.MustCompile(`第\s*([0-9一二三四五六七八九十百]+)\s*[话話集回]`)
	episodeWordPattern     = regexp.MustCompile(`(?i)(?:^|[\s._\-])(?:e|ep|episode)[\s._-]*(\d{1,3})(?:$|[\s._\-])`)
	seasonWordPattern      = regexp.MustCompile(`(?i)(?:season|s)[\s._-]*(\d{1,2})(?:$|[\s._\-])`)
	trailingEpisodePattern = regexp.MustCompile(`(?i)(?:^|[\s._\-\[\]()])(\d{1,3})(?:v\d)?(?:$|[\s._\-\[\]()])`)
	embyIDPattern          = regexp.MustCompile(`\s*\(ID[^)]+\)\s*$`)
	noiseTokenPattern      = regexp.MustCompile(`(?i)^(?:` +
		`\d{3,4}p|` +
		`(?:h|x|he|avc)?26[45]|hevc|av1|vp[89]|` +
		`web[-_.]?dl|webrip|bluray|bdrip|brrip|dvdrip|hdtv|remux|` +
		`aac|ac3|eac3|dts(?:-hd)?|flac|` +
		`\d+(?:\.\d+)?(?:mb|gb)|` +
		`10bit|8bit|hi10p|hdr(?:10)?|dolby[ ._-]?vision|dv` +
		`)$`)
	noisePhrasePattern = regexp.MustCompile(`(?i)(?:^|[\s._\-])(?:web[-_.]?dl|webrip|bluray|bdrip|brrip|dvdrip|hdtv|remux)(?:$|[\s._\-])`)
)

func ParseName(name, parentName string) parsedName {
	name = filepath.Base(strings.TrimSpace(name))
	ext := filepath.Ext(name)
	base := strings.TrimSpace(strings.TrimSuffix(name, ext))
	base = embyIDPattern.ReplaceAllString(base, "")

	season, episode := parseSeasonEpisode(base)
	query := cleanQuery(base)
	parentQuery := cleanQuery(parentName)
	if isGenericQuery(query) && parentQuery != "" {
		query = parentQuery
	}
	if season == nil {
		season, _ = parseSeasonEpisode(parentName)
	}
	return parsedName{
		Query:         query,
		SeasonNumber:  season,
		EpisodeNumber: episode,
	}
}

func ParseQuery(query string) parsedName {
	query = strings.TrimSpace(query)
	season, episode := parseSeasonEpisode(query)
	query = seasonEpisodePattern.ReplaceAllString(query, " ")
	query = chineseSeasonPattern.ReplaceAllString(query, " ")
	query = chineseEpisodePattern.ReplaceAllString(query, " ")
	query = episodeWordPattern.ReplaceAllString(query, " ")
	query = seasonWordPattern.ReplaceAllString(query, " ")
	return parsedName{
		Query:         normalizeQuery(query),
		SeasonNumber:  season,
		EpisodeNumber: episode,
	}
}

func FormatEpisodeCode(season, episode *int) string {
	if season == nil || episode == nil {
		return ""
	}
	return fmt.Sprintf("S%02dE%02d", *season, *episode)
}

func parseSeasonEpisode(value string) (*int, *int) {
	if match := seasonEpisodePattern.FindStringSubmatch(value); len(match) > 0 {
		if match[1] != "" {
			return intPointer(match[1]), intPointer(match[2])
		}
		return intPointer(match[3]), intPointer(match[4])
	}

	var season *int
	if match := chineseSeasonPattern.FindStringSubmatch(value); len(match) > 0 {
		season = chineseNumberPointer(match[1])
	}
	if season == nil {
		if match := seasonWordPattern.FindStringSubmatch(value); len(match) > 0 {
			season = intPointer(match[1])
		}
	}

	var episode *int
	if match := chineseEpisodePattern.FindStringSubmatch(value); len(match) > 0 {
		episode = chineseNumberPointer(match[1])
	}
	if episode == nil {
		if match := episodeWordPattern.FindStringSubmatch(value); len(match) > 0 {
			episode = intPointer(match[1])
		}
	}
	if episode == nil {
		matches := trailingEpisodePattern.FindAllStringSubmatch(value, -1)
		if len(matches) > 0 {
			last := matches[len(matches)-1]
			number := intPointer(last[1])
			if number != nil && *number <= 199 {
				episode = number
			}
		}
	}
	return season, episode
}

func cleanQuery(value string) string {
	value = embyIDPattern.ReplaceAllString(strings.TrimSpace(value), "")
	value = seasonEpisodePattern.ReplaceAllString(value, " ")
	value = chineseSeasonPattern.ReplaceAllString(value, " ")
	value = chineseEpisodePattern.ReplaceAllString(value, " ")
	value = episodeWordPattern.ReplaceAllString(value, " ")
	value = seasonWordPattern.ReplaceAllString(value, " ")
	value = noisePhrasePattern.ReplaceAllString(value, " ")
	if _, episode := parseSeasonEpisode(value); episode != nil {
		value = stripTrailingEpisode(value)
	}

	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == '.' || r == '_' || r == '-' || r == '[' || r == ']' || r == '(' || r == ')' || unicode.IsSpace(r)
	})
	filtered := parts[:0]
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || noiseTokenPattern.MatchString(part) {
			continue
		}
		filtered = append(filtered, part)
	}
	return normalizeQuery(strings.Join(filtered, " "))
}

func stripTrailingEpisode(value string) string {
	matches := trailingEpisodePattern.FindAllStringSubmatchIndex(value, -1)
	if len(matches) == 0 {
		return value
	}
	last := matches[len(matches)-1]
	if last[1] != len(value) {
		return value
	}
	number := intPointer(value[last[2]:last[3]])
	if number == nil || *number > 199 {
		return value
	}
	return strings.TrimSpace(value[:last[0]])
}

func normalizeQuery(value string) string {
	value = embyIDPattern.ReplaceAllString(value, " ")
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "-_:[]() ")
	return strings.Join(strings.Fields(value), " ")
}

func isGenericQuery(query string) bool {
	if query == "" {
		return true
	}
	if _, err := strconv.Atoi(query); err == nil {
		return true
	}
	switch strings.ToLower(query) {
	case "video", "movie", "episode", "sample", "trailer":
		return true
	default:
		return false
	}
}

func intPointer(value string) *int {
	number, err := strconv.Atoi(value)
	if err != nil {
		return nil
	}
	return &number
}

func intPointerValue(value int) *int {
	return &value
}

func chineseNumberPointer(value string) *int {
	if number, err := strconv.Atoi(value); err == nil {
		return &number
	}
	digits := map[rune]int{
		'零': 0, '〇': 0, '一': 1, '二': 2, '两': 2, '三': 3, '四': 4,
		'五': 5, '六': 6, '七': 7, '八': 8, '九': 9,
	}
	total := 0
	current := 0
	for _, r := range value {
		switch r {
		case '百':
			if current == 0 {
				current = 1
			}
			total += current * 100
			current = 0
		case '十':
			if current == 0 {
				current = 1
			}
			total += current * 10
			current = 0
		default:
			digit, ok := digits[r]
			if !ok {
				return nil
			}
			current = digit
		}
	}
	total += current
	if total == 0 {
		return nil
	}
	return &total
}
