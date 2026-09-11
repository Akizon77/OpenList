package emby

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	log "github.com/sirupsen/logrus"
)

const (
	embyPlaybackInfoMethod     = "playback_info"
	embyPlaybackStartMethod    = "playback_start"
	embyPlaybackProgressMethod = "playback_progress"
	embyPlaybackStopMethod     = "playback_stop"
	embyDefaultWebBitrate      = 120_000_000
)

type embyPlaybackInfoRequest struct {
	Mode                string `json:"mode"`
	DeviceID            string `json:"device_id"`
	MediaSourceID       string `json:"media_source_id"`
	AudioStreamIndex    *int   `json:"audio_stream_index"`
	SubtitleStreamIndex *int   `json:"subtitle_stream_index"`
	MaxStreamingBitrate int    `json:"max_streaming_bitrate"`
}

type embyPlaybackReportRequest struct {
	DeviceID            string `json:"device_id"`
	PlaySessionID       string `json:"play_session_id"`
	MediaSourceID       string `json:"media_source_id"`
	AudioStreamIndex    *int   `json:"audio_stream_index"`
	SubtitleStreamIndex *int   `json:"subtitle_stream_index"`
	PositionTicks       int64  `json:"position_ticks"`
	IsPaused            bool   `json:"is_paused"`
	IsMuted             bool   `json:"is_muted"`
	VolumeLevel         int    `json:"volume_level"`
	PlayMethod          string `json:"play_method"`
}

type embyPlaybackInfo struct {
	ItemID                      string                    `json:"item_id"`
	ItemType                    string                    `json:"item_type"`
	Name                        string                    `json:"name"`
	SeriesName                  string                    `json:"series_name,omitempty"`
	OriginalTitle               string                    `json:"original_title,omitempty"`
	SeasonNumber                *int                      `json:"season_number,omitempty"`
	EpisodeNumber               *int                      `json:"episode_number,omitempty"`
	MediaType                   string                    `json:"media_type"`
	RunTimeTicks                int64                     `json:"run_time_ticks"`
	PlaybackPositionTicks       int64                     `json:"playback_position_ticks"`
	PlaySessionID               string                    `json:"play_session_id"`
	DeviceID                    string                    `json:"device_id"`
	SelectedMediaSourceID       string                    `json:"selected_media_source_id"`
	SelectedAudioStreamIndex    int                       `json:"selected_audio_stream_index"`
	SelectedSubtitleStreamIndex int                       `json:"selected_subtitle_stream_index"`
	PlaybackURL                 string                    `json:"playback_url"`
	PlaybackType                string                    `json:"playback_type"`
	PlaybackMethod              string                    `json:"playback_method"`
	PlaybackError               string                    `json:"playback_error,omitempty"`
	MediaSources                []embyPlaybackMediaSource `json:"media_sources"`
}

type embyPlaybackMediaSource struct {
	ID                         string               `json:"id"`
	Name                       string               `json:"name"`
	Container                  string               `json:"container"`
	Protocol                   string               `json:"protocol"`
	SupportsDirectPlay         bool                 `json:"supports_direct_play"`
	SupportsDirectStream       bool                 `json:"supports_direct_stream"`
	SupportsTranscoding        bool                 `json:"supports_transcoding"`
	DefaultAudioStreamIndex    int                  `json:"default_audio_stream_index"`
	DefaultSubtitleStreamIndex int                  `json:"default_subtitle_stream_index"`
	DirectURL                  string               `json:"direct_url"`
	AudioStreams               []embyPlaybackStream `json:"audio_streams"`
	SubtitleStreams            []embyPlaybackStream `json:"subtitle_streams"`
}

type embyPlaybackStream struct {
	Index                  int    `json:"index"`
	Type                   string `json:"type"`
	Codec                  string `json:"codec"`
	Language               string `json:"language"`
	DisplayTitle           string `json:"display_title"`
	Title                  string `json:"title"`
	IsExternal             bool   `json:"is_external"`
	IsDefault              bool   `json:"is_default"`
	IsForced               bool   `json:"is_forced"`
	SupportsExternalStream bool   `json:"supports_external_stream"`
	DeliveryMethod         string `json:"delivery_method"`
	URL                    string `json:"url,omitempty"`
}

type embyPlaybackReport struct {
	ItemID              string `json:"ItemId"`
	MediaSourceID       string `json:"MediaSourceId,omitempty"`
	PlaySessionID       string `json:"PlaySessionId,omitempty"`
	PositionTicks       int64  `json:"PositionTicks"`
	AudioStreamIndex    *int   `json:"AudioStreamIndex,omitempty"`
	SubtitleStreamIndex *int   `json:"SubtitleStreamIndex,omitempty"`
	CanSeek             bool   `json:"CanSeek"`
	IsPaused            bool   `json:"IsPaused"`
	IsMuted             bool   `json:"IsMuted"`
	VolumeLevel         int    `json:"VolumeLevel"`
	PlayMethod          string `json:"PlayMethod"`
	RepeatMode          string `json:"RepeatMode"`
	FailedToStart       bool   `json:"FailedToStart,omitempty"`
}

func (d *Emby) Other(ctx context.Context, args model.OtherArgs) (interface{}, error) {
	switch args.Method {
	case embyPlaybackInfoMethod:
		var req embyPlaybackInfoRequest
		if err := decodeEmbyOtherData(args.Data, &req); err != nil {
			return nil, err
		}
		return d.buildPlaybackInfo(ctx, args.Obj.GetID(), req)
	case embyPlaybackStartMethod, embyPlaybackProgressMethod, embyPlaybackStopMethod:
		var req embyPlaybackReportRequest
		if err := decodeEmbyOtherData(args.Data, &req); err != nil {
			return nil, err
		}
		return nil, d.reportPlayback(ctx, args.Method, args.Obj.GetID(), req)
	default:
		return nil, errs.NotSupport
	}
}

func decodeEmbyOtherData(data interface{}, out interface{}) error {
	if data == nil {
		return nil
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("invalid emby playback data: %w", err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("invalid emby playback data: %w", err)
	}
	return nil
}

func (d *Emby) buildPlaybackInfo(ctx context.Context, itemID string, req embyPlaybackInfoRequest) (*embyPlaybackInfo, error) {
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return nil, fmt.Errorf("invalid file id")
	}
	detail, err := d.getItemDetail(ctx, itemID)
	if err != nil {
		return nil, err
	}
	if len(detail.MediaSources) == 0 {
		return nil, fmt.Errorf("emby item %s has no media sources", itemID)
	}

	selectedSource := selectPlaybackSource(detail.MediaSources, req.MediaSourceID)
	if selectedSource == nil {
		return nil, fmt.Errorf("emby media source %q not found", req.MediaSourceID)
	}
	selectedAudio := valueOrDefault(req.AudioStreamIndex, selectedSource.DefaultAudioStreamIndex, -1)
	selectedSubtitle := valueOrDefault(req.SubtitleStreamIndex, selectedSource.DefaultSubtitleStreamIndex, -1)
	deviceID := normalizeEmbyDeviceID(req.DeviceID)
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "web"
	}

	info := &embyPlaybackInfo{
		ItemID:                      itemID,
		ItemType:                    detail.Type,
		Name:                        detail.Name,
		SeriesName:                  detail.SeriesName,
		OriginalTitle:               detail.OriginalTitle,
		SeasonNumber:                cloneInt(detail.ParentIndex),
		EpisodeNumber:               cloneInt(detail.IndexNumber),
		MediaType:                   detail.MediaType,
		RunTimeTicks:                detail.RunTimeTicks,
		PlaybackPositionTicks:       detail.UserData.PlaybackPositionTicks,
		DeviceID:                    deviceID,
		SelectedMediaSourceID:       selectedSource.ID,
		SelectedAudioStreamIndex:    selectedAudio,
		SelectedSubtitleStreamIndex: selectedSubtitle,
		PlaybackMethod:              "DirectStream",
	}

	for i := range detail.MediaSources {
		source, err := d.toPlaybackMediaSource(itemID, detail.MediaType, detail.MediaSources[i])
		if err != nil {
			return nil, err
		}
		info.MediaSources = append(info.MediaSources, source)
	}

	directURL, err := d.buildDirectStreamURL(itemID, detail.MediaType, *selectedSource)
	if err != nil {
		return nil, err
	}
	directURL, err = addStreamSelection(directURL, selectedAudio)
	if err != nil {
		return nil, err
	}
	info.PlaybackURL = directURL
	info.PlaybackType = playbackURLType(directURL, selectedSource.Container)
	for i := range info.MediaSources {
		if info.MediaSources[i].ID == selectedSource.ID {
			info.MediaSources[i].DirectURL = directURL
			break
		}
	}

	if mode != "external" {
		bitrate := normalizeStreamingBitrate(req.MaxStreamingBitrate)
		plan, planErr := d.getWebPlaybackInfo(ctx, itemID, deviceID, selectedSource.ID, selectedAudio, selectedSubtitle, bitrate)
		if planErr != nil {
			info.PlaybackError = planErr.Error()
			log.WithError(planErr).Warnf("emby web playback plan failed for item %s; using direct stream", itemID)
		} else if plannedSource := selectPlaybackSource(plan.MediaSources, selectedSource.ID); plannedSource != nil {
			selectedMediaSource := getPlaybackMediaSource(info.MediaSources, selectedSource.ID)
			if selectedMediaSource != nil {
				if err := d.mergePlaybackMediaSource(itemID, detail.MediaType, selectedMediaSource, *plannedSource); err != nil {
					return nil, err
				}
			}
			info.PlaySessionID = plan.PlaySessionID
			rawPlaybackURL := plannedSource.DirectStreamURL
			if plannedSource.TranscodingURL != "" {
				rawPlaybackURL = plannedSource.TranscodingURL
				info.PlaybackMethod = "Transcode"
			} else if rawPlaybackURL == "" {
				rawPlaybackURL = directURL
				info.PlaybackMethod = "DirectPlay"
			}
			resolved, resolveErr := d.resolveEmbyURL(rawPlaybackURL)
			if resolveErr != nil {
				return nil, resolveErr
			}
			info.PlaybackURL = resolved
			info.PlaybackType = playbackURLType(resolved, plannedSource.Container)
		}
	}

	return info, nil
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func (d *Emby) toPlaybackMediaSource(itemID, mediaType string, source embyMediaSource) (embyPlaybackMediaSource, error) {
	directURL, err := d.buildDirectStreamURL(itemID, mediaType, source)
	if err != nil {
		return embyPlaybackMediaSource{}, err
	}
	result := embyPlaybackMediaSource{
		ID:                         source.ID,
		Name:                       source.Name,
		Container:                  source.Container,
		Protocol:                   source.Protocol,
		SupportsDirectPlay:         source.SupportsDirectPlay,
		SupportsDirectStream:       source.SupportsDirectStream,
		SupportsTranscoding:        source.SupportsTranscoding,
		DefaultAudioStreamIndex:    valueOrDefault(nil, source.DefaultAudioStreamIndex, -1),
		DefaultSubtitleStreamIndex: valueOrDefault(nil, source.DefaultSubtitleStreamIndex, -1),
		DirectURL:                  directURL,
	}
	for i := range source.MediaStreams {
		stream := source.MediaStreams[i]
		playbackStream := embyPlaybackStream{
			Index:                  stream.Index,
			Type:                   stream.Type,
			Codec:                  stream.Codec,
			Language:               stream.Language,
			DisplayTitle:           stream.DisplayTitle,
			Title:                  stream.Title,
			IsExternal:             stream.IsExternal,
			IsDefault:              stream.IsDefault,
			IsForced:               stream.IsForced,
			SupportsExternalStream: stream.SupportsExternalStream,
			DeliveryMethod:         stream.DeliveryMethod,
		}
		switch strings.ToLower(stream.Type) {
		case "audio":
			if stream.DeliveryURL != "" {
				playbackStream.URL, err = d.resolveEmbyURL(stream.DeliveryURL)
				if err != nil {
					return embyPlaybackMediaSource{}, err
				}
			}
			result.AudioStreams = append(result.AudioStreams, playbackStream)
		case "subtitle":
			if stream.DeliveryURL != "" {
				playbackStream.URL, err = d.resolveEmbyURL(stream.DeliveryURL)
			} else if source.ID != "" && supportsExtractedSubtitle(stream) {
				playbackStream.URL, err = d.buildSubtitleURL(itemID, source.ID, stream)
			}
			if err != nil {
				return embyPlaybackMediaSource{}, err
			}
			result.SubtitleStreams = append(result.SubtitleStreams, playbackStream)
		}
	}
	return result, nil
}

func getPlaybackMediaSource(sources []embyPlaybackMediaSource, sourceID string) *embyPlaybackMediaSource {
	for i := range sources {
		if strings.EqualFold(strings.TrimSpace(sources[i].ID), strings.TrimSpace(sourceID)) {
			return &sources[i]
		}
	}
	return nil
}

func (d *Emby) mergePlaybackMediaSource(itemID, mediaType string, target *embyPlaybackMediaSource, source embyMediaSource) error {
	merged, err := d.toPlaybackMediaSource(itemID, mediaType, source)
	if err != nil {
		return err
	}

	// Keep the static direct URL generated from the item detail. The playback
	// response is used here for its resolved stream URLs and stream metadata.
	merged.DirectURL = target.DirectURL
	if merged.DefaultAudioStreamIndex < 0 {
		merged.DefaultAudioStreamIndex = target.DefaultAudioStreamIndex
	}
	if merged.DefaultSubtitleStreamIndex < 0 {
		merged.DefaultSubtitleStreamIndex = target.DefaultSubtitleStreamIndex
	}
	*target = merged
	return nil
}

func (d *Emby) getWebPlaybackInfo(ctx context.Context, itemID, deviceID, mediaSourceID string, audioStreamIndex, subtitleStreamIndex, maxBitrate int) (*embyPlaybackInfoResp, error) {
	_, userID := d.auth()
	payload := map[string]interface{}{
		"UserId":              userID,
		"IsPlayback":          true,
		"AutoOpenLiveStream":  true,
		"MaxStreamingBitrate": maxBitrate,
		"MediaSourceId":       mediaSourceID,
		"AudioStreamIndex":    audioStreamIndex,
		"SubtitleStreamIndex": subtitleStreamIndex,
		"DeviceProfile":       embyWebDeviceProfile(maxBitrate),
	}
	var info embyPlaybackInfoResp
	if err := d.postJSONWithDevice(ctx, "/Items/"+itemID+"/PlaybackInfo", nil, payload, &info, "playback info", deviceID); err != nil {
		return nil, err
	}
	if info.ErrorCode != "" {
		return nil, fmt.Errorf("emby playback info failed: %s", info.ErrorCode)
	}
	if len(info.MediaSources) == 0 {
		return nil, fmt.Errorf("emby playback info returned no media sources")
	}
	return &info, nil
}

func embyWebDeviceProfile(maxBitrate int) map[string]interface{} {
	return map[string]interface{}{
		"Name":                             "OpenList Web",
		"MaxStreamingBitrate":              maxBitrate,
		"MusicStreamingTranscodingBitrate": 384_000,
		"DirectPlayProfiles": []map[string]interface{}{
			{"Container": "mp4,m4v", "Type": "Video", "VideoCodec": "h264", "AudioCodec": "aac,mp3"},
			{"Container": "webm", "Type": "Video", "VideoCodec": "vp8,vp9,av1", "AudioCodec": "vorbis,opus"},
		},
		"TranscodingProfiles": []map[string]interface{}{
			{
				"Container":           "ts",
				"Type":                "Video",
				"VideoCodec":          "h264",
				"AudioCodec":          "aac",
				"Protocol":            "hls",
				"Context":             "Streaming",
				"MaxAudioChannels":    "2",
				"MinSegments":         1,
				"BreakOnNonKeyFrames": true,
			},
		},
		"SubtitleProfiles": []map[string]interface{}{
			{"Format": "vtt", "Method": "External"},
			{"Format": "srt", "Method": "External"},
			{"Format": "ass", "Method": "External"},
			{"Format": "ssa", "Method": "External"},
		},
		"CodecProfiles":     []interface{}{},
		"ContainerProfiles": []interface{}{},
		"ResponseProfiles":  []interface{}{},
	}
}

func (d *Emby) reportPlayback(ctx context.Context, method, itemID string, req embyPlaybackReportRequest) error {
	if strings.TrimSpace(req.PlaySessionID) == "" {
		return nil
	}
	position := req.PositionTicks
	if position < 0 {
		position = 0
	}
	volume := req.VolumeLevel
	if volume < 0 {
		volume = 0
	} else if volume > 100 {
		volume = 100
	}
	payload := embyPlaybackReport{
		ItemID:              strings.TrimSpace(itemID),
		MediaSourceID:       strings.TrimSpace(req.MediaSourceID),
		PlaySessionID:       strings.TrimSpace(req.PlaySessionID),
		PositionTicks:       position,
		AudioStreamIndex:    req.AudioStreamIndex,
		SubtitleStreamIndex: req.SubtitleStreamIndex,
		CanSeek:             true,
		IsPaused:            req.IsPaused,
		IsMuted:             req.IsMuted,
		VolumeLevel:         volume,
		PlayMethod:          normalizeEmbyPlayMethod(req.PlayMethod),
		RepeatMode:          "RepeatNone",
	}
	endpoint := "/Sessions/Playing/Progress"
	action := "playback progress"
	switch method {
	case embyPlaybackStartMethod:
		endpoint = "/Sessions/Playing"
		action = "playback start"
	case embyPlaybackStopMethod:
		endpoint = "/Sessions/Playing/Stopped"
		action = "playback stop"
	}
	return d.postJSONWithDevice(ctx, endpoint, nil, payload, nil, action, normalizeEmbyDeviceID(req.DeviceID))
}

func normalizeEmbyPlayMethod(method string) string {
	switch strings.ToLower(strings.TrimSpace(method)) {
	case "directplay", "direct_play":
		return "DirectPlay"
	case "transcode", "transcoding":
		return "Transcode"
	default:
		return "DirectStream"
	}
}

func normalizeEmbyDeviceID(deviceID string) string {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return "openlist-web"
	}
	var b strings.Builder
	for _, r := range deviceID {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("-_.", r) {
			b.WriteRune(r)
		}
		if b.Len() >= 64 {
			break
		}
	}
	if b.Len() == 0 {
		return "openlist-web"
	}
	return b.String()
}

func normalizeStreamingBitrate(bitrate int) int {
	if bitrate <= 0 {
		return embyDefaultWebBitrate
	}
	if bitrate < 1_000_000 {
		return 1_000_000
	}
	if bitrate > 200_000_000 {
		return 200_000_000
	}
	return bitrate
}

func selectPlaybackSource(sources []embyMediaSource, requestedID string) *embyMediaSource {
	requestedID = strings.TrimSpace(requestedID)
	if requestedID != "" {
		for i := range sources {
			if strings.EqualFold(strings.TrimSpace(sources[i].ID), requestedID) {
				return &sources[i]
			}
		}
		return nil
	}
	for i := range sources {
		if sources[i].SupportsDirectStream || sources[i].SupportsDirectPlay {
			return &sources[i]
		}
	}
	if len(sources) == 0 {
		return nil
	}
	return &sources[0]
}

func valueOrDefault(requested, fallback *int, defaultValue int) int {
	if requested != nil {
		return *requested
	}
	if fallback != nil {
		return *fallback
	}
	return defaultValue
}

func supportsExtractedSubtitle(stream embyMediaStream) bool {
	if stream.SupportsExternalStream || stream.IsExternal {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(stream.Codec)) {
	case "ass", "ssa", "srt", "subrip", "vtt", "webvtt":
		return true
	default:
		return false
	}
}

func subtitleExtension(codec string) string {
	switch strings.ToLower(strings.TrimSpace(codec)) {
	case "subrip":
		return "srt"
	case "webvtt":
		return "vtt"
	default:
		codec = strings.ToLower(strings.TrimSpace(codec))
		if codec == "" {
			return "srt"
		}
		return codec
	}
}

func (d *Emby) buildDirectStreamURL(itemID, mediaType string, source embyMediaSource) (string, error) {
	streamPath := "Videos"
	if strings.EqualFold(strings.TrimSpace(mediaType), "audio") {
		streamPath = "Audio"
	}
	endpoint := path.Join("/", streamPath, itemID, "stream")
	if container := strings.TrimSpace(source.Container); container != "" {
		endpoint += "." + container
	}
	query := url.Values{}
	if source.ID != "" {
		query.Set("MediaSourceId", source.ID)
	}
	query.Set("Static", "true")
	return d.buildAuthenticatedURL(endpoint, query)
}

func addStreamSelection(rawURL string, audioStreamIndex int) (string, error) {
	if audioStreamIndex < 0 {
		return rawURL, nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	query := u.Query()
	query.Set("AudioStreamIndex", strconv.Itoa(audioStreamIndex))
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func (d *Emby) buildSubtitleURL(itemID, mediaSourceID string, stream embyMediaStream) (string, error) {
	endpoint := path.Join("/Videos", itemID, mediaSourceID, "Subtitles", strconv.Itoa(stream.Index), "Stream."+subtitleExtension(stream.Codec))
	return d.buildAuthenticatedURL(endpoint, nil)
}

func (d *Emby) buildAuthenticatedURL(endpoint string, query url.Values) (string, error) {
	u, err := url.Parse(d.URL)
	if err != nil {
		return "", err
	}
	u.Path = path.Join(u.Path, endpoint)
	q := u.Query()
	for key, values := range query {
		q.Del(key)
		for _, value := range values {
			q.Add(key, value)
		}
	}
	token, _ := d.auth()
	q.Set("api_key", token)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (d *Emby) resolveEmbyURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if parsed.IsAbs() {
		return parsed.String(), nil
	}
	base, err := url.Parse(d.URL)
	if err != nil {
		return "", err
	}
	if !strings.HasSuffix(base.Path, "/") {
		base.Path += "/"
	}
	resolved := base.ResolveReference(parsed)
	q := resolved.Query()
	if q.Get("api_key") == "" {
		token, _ := d.auth()
		q.Set("api_key", token)
		resolved.RawQuery = q.Encode()
	}
	return resolved.String(), nil
}

func playbackURLType(rawURL, fallbackContainer string) string {
	lower := strings.ToLower(rawURL)
	if strings.Contains(lower, ".m3u8") {
		return "m3u8"
	}
	if parsed, err := url.Parse(rawURL); err == nil {
		if ext := strings.TrimPrefix(path.Ext(parsed.Path), "."); ext != "" {
			return strings.ToLower(ext)
		}
	}
	return strings.ToLower(strings.TrimSpace(fallbackContainer))
}

var _ interface {
	Other(context.Context, model.OtherArgs) (interface{}, error)
} = (*Emby)(nil)
