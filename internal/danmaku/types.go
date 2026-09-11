package danmaku

const (
	MatchStatusMatched   = "matched"
	MatchStatusAmbiguous = "ambiguous"
	MatchStatusNotFound  = "not_found"
)

type MediaMetadata struct {
	ItemID        string `json:"item_id,omitempty"`
	ItemType      string `json:"item_type,omitempty"`
	Name          string `json:"name,omitempty"`
	ParentName    string `json:"parent_name,omitempty"`
	SeriesName    string `json:"series_name,omitempty"`
	OriginalTitle string `json:"original_title,omitempty"`
	SeasonNumber  *int   `json:"season_number,omitempty"`
	EpisodeNumber *int   `json:"episode_number,omitempty"`
}

type SearchInput struct {
	Query string
	Media *MediaMetadata
}

type SearchResult struct {
	Query           string           `json:"query"`
	SuggestedQuery  string           `json:"suggested_query"`
	SeasonNumber    *int             `json:"season_number,omitempty"`
	EpisodeNumber   *int             `json:"episode_number,omitempty"`
	Status          string           `json:"status"`
	EpisodeID       int64            `json:"episode_id,omitempty"`
	Match           *MatchedEpisode  `json:"match,omitempty"`
	Candidates      []AnimeCandidate `json:"candidates"`
	FallbackQueries []string         `json:"fallback_queries,omitempty"`
}

type MatchedEpisode struct {
	AnimeID         int64  `json:"anime_id"`
	AnimeTitle      string `json:"anime_title"`
	AnimeType       string `json:"anime_type"`
	TypeDescription string `json:"type_description"`
	EpisodeID       int64  `json:"episode_id"`
	EpisodeTitle    string `json:"episode_title"`
}

type AnimeCandidate struct {
	AnimeID         int64              `json:"anime_id"`
	AnimeTitle      string             `json:"anime_title"`
	Type            string             `json:"type"`
	TypeDescription string             `json:"type_description"`
	Episodes        []EpisodeCandidate `json:"episodes"`
}

type EpisodeCandidate struct {
	EpisodeID    int64  `json:"episode_id"`
	EpisodeTitle string `json:"episode_title"`
}

type Comment struct {
	CID int64  `json:"cid"`
	P   string `json:"p"`
	M   string `json:"m"`
}

type Danmu struct {
	Text  string  `json:"text"`
	Mode  int     `json:"mode"`
	Color string  `json:"color"`
	Time  float64 `json:"time"`
}

type CommentsResult struct {
	Comments []Danmu `json:"comments"`
	Count    int     `json:"count"`
	Partial  bool    `json:"partial"`
}

type parsedName struct {
	Query         string
	SeasonNumber  *int
	EpisodeNumber *int
}
