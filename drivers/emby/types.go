package emby

type authReq struct {
	Username string `json:"Username"`
	Pw       string `json:"Pw"`
}

type authResp struct {
	AccessToken string `json:"AccessToken"`
	User        struct {
		ID string `json:"Id"`
	} `json:"User"`
}

type listResp struct {
	Items            []embyItem `json:"Items"`
	TotalRecordCount *int       `json:"TotalRecordCount"`
}

type embyItem struct {
	MediaSources  []embyMediaSource `json:"MediaSources"`
	Name          string            `json:"Name"`
	ID            string            `json:"Id"`
	Type          string            `json:"Type"`
	MediaType     string            `json:"MediaType"`
	Path          string            `json:"Path"`
	SeriesName    string            `json:"SeriesName"`
	OriginalTitle string            `json:"OriginalTitle"`
	IndexNumber   *int              `json:"IndexNumber"`
	ParentIndex   *int              `json:"ParentIndexNumber"`
	IsFolder      bool              `json:"IsFolder"`
	Size          int64             `json:"Size"`
	RunTimeTicks  int64             `json:"RunTimeTicks"`
	DateCreated   string            `json:"DateCreated"`
	UserData      embyUserData      `json:"UserData"`
}

type itemDetailResp struct {
	embyItem
	MediaSources []embyMediaSource `json:"MediaSources"`
}

type embyMediaSource struct {
	ID                         string            `json:"Id"`
	Name                       string            `json:"Name"`
	Container                  string            `json:"Container"`
	Protocol                   string            `json:"Protocol"`
	Path                       string            `json:"Path"`
	Bitrate                    int               `json:"Bitrate"`
	SupportsDirectPlay         bool              `json:"SupportsDirectPlay"`
	SupportsDirectStream       bool              `json:"SupportsDirectStream"`
	SupportsTranscoding        bool              `json:"SupportsTranscoding"`
	DirectStreamURL            string            `json:"DirectStreamUrl"`
	TranscodingURL             string            `json:"TranscodingUrl"`
	RunTimeTicks               int64             `json:"RunTimeTicks"`
	DefaultAudioStreamIndex    *int              `json:"DefaultAudioStreamIndex"`
	DefaultSubtitleStreamIndex *int              `json:"DefaultSubtitleStreamIndex"`
	MediaStreams               []embyMediaStream `json:"MediaStreams"`
}

type embyMediaStream struct {
	Index                  int    `json:"Index"`
	Type                   string `json:"Type"`
	Codec                  string `json:"Codec"`
	Bitrate                int    `json:"BitRate"`
	Width                  int    `json:"Width"`
	Height                 int    `json:"Height"`
	Language               string `json:"Language"`
	DisplayTitle           string `json:"DisplayTitle"`
	Title                  string `json:"Title"`
	IsExternal             bool   `json:"IsExternal"`
	IsDefault              bool   `json:"IsDefault"`
	IsForced               bool   `json:"IsForced"`
	SupportsExternalStream bool   `json:"SupportsExternalStream"`
	DeliveryMethod         string `json:"DeliveryMethod"`
	DeliveryURL            string `json:"DeliveryUrl"`
}

type embyUserData struct {
	PlaybackPositionTicks int64 `json:"PlaybackPositionTicks"`
	Played                bool  `json:"Played"`
}

type embyPlaybackInfoResp struct {
	PlaySessionID string            `json:"PlaySessionId"`
	ErrorCode     string            `json:"ErrorCode"`
	MediaSources  []embyMediaSource `json:"MediaSources"`
}
