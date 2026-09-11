package emby

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestNormalizeDirectPlayProfilesUsesReportedCapabilities(t *testing.T) {
	profiles := normalizeDirectPlayProfiles([]embyPlaybackDirectPlayProfile{
		{
			Container:  "mp4,mkv,invalid",
			VideoCodec: "h264,hevc,unknown",
			AudioCodec: "aac,opus,unknown",
		},
	})
	if len(profiles) != 1 {
		t.Fatalf("profiles length = %d, want 1", len(profiles))
	}
	if profiles[0]["Container"] != "mp4,mkv" || profiles[0]["VideoCodec"] != "h264,hevc" || profiles[0]["AudioCodec"] != "aac,opus" {
		t.Fatalf("normalized profile = %#v", profiles[0])
	}
	if got := normalizeDirectPlayProfiles([]embyPlaybackDirectPlayProfile{}); len(got) != 0 {
		t.Fatalf("empty reported profiles = %#v, want none", got)
	}
	if got := normalizeDirectPlayProfiles(nil); len(got) == 0 {
		t.Fatal("legacy request should receive default direct play profiles")
	}
}

func TestBuildTranscodingQualitiesUsesSourceBitrateAndHeight(t *testing.T) {
	qualities := buildTranscodingQualities(embyMediaSource{
		Bitrate: 8_000_000,
		MediaStreams: []embyMediaStream{
			{Type: "Video", Codec: "hevc", Bitrate: 8_000_000, Height: 1080},
		},
	})
	if len(qualities) < 3 {
		t.Fatalf("qualities = %#v", qualities)
	}
	if qualities[0].MaxStreamingBitrate != 15_000_000 || qualities[0].MaxHeight != 1080 {
		t.Fatalf("first quality = %#v, want 1080p at 15 Mbps", qualities[0])
	}
	if qualities[1].MaxStreamingBitrate != 10_000_000 {
		t.Fatalf("second quality = %#v, want 10 Mbps", qualities[1])
	}
}

func TestBuildPlaybackInfoSelectsRequestedPlaybackMode(t *testing.T) {
	tests := []struct {
		name                  string
		mode                  string
		wantMethod            string
		wantTranscoding       bool
		wantDirectPlay        bool
		wantDirectStream      bool
		maxStreamingBitrate   int
		wantURLQueryParameter string
	}{
		{
			name:                  "auto prefers original stream",
			mode:                  "auto",
			wantMethod:            "DirectPlay",
			wantTranscoding:       true,
			wantDirectPlay:        true,
			wantDirectStream:      true,
			wantURLQueryParameter: "Static=true",
		},
		{
			name:                  "direct forces original stream",
			mode:                  "direct",
			wantMethod:            "DirectPlay",
			wantTranscoding:       false,
			wantDirectPlay:        true,
			wantDirectStream:      false,
			wantURLQueryParameter: "Static=true",
		},
		{
			name:                  "transcode forces selected bitrate",
			mode:                  "transcode",
			wantMethod:            "Transcode",
			wantTranscoding:       true,
			wantDirectPlay:        false,
			wantDirectStream:      false,
			maxStreamingBitrate:   4_000_000,
			wantURLQueryParameter: "transcode=true",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var playbackPayload map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/Users/test-user/Items/item-1":
					_ = json.NewEncoder(w).Encode(map[string]any{
						"Id":        "item-1",
						"Name":      "Episode",
						"MediaType": "Video",
						"MediaSources": []any{map[string]any{
							"Id":                   "source-1",
							"Container":            "mp4",
							"Bitrate":              8_000_000,
							"SupportsDirectPlay":   true,
							"SupportsDirectStream": true,
							"SupportsTranscoding":  true,
							"MediaStreams": []any{map[string]any{
								"Index": 0, "Type": "Video", "Codec": "h264", "BitRate": 8_000_000, "Height": 1080,
							}},
						}},
					})
				case "/Items/item-1/PlaybackInfo":
					if err := json.NewDecoder(r.Body).Decode(&playbackPayload); err != nil {
						t.Errorf("decode playback payload: %v", err)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{
						"PlaySessionId": "session-1",
						"MediaSources": []any{map[string]any{
							"Id":                   "source-1",
							"Container":            "mp4",
							"SupportsDirectPlay":   true,
							"SupportsDirectStream": true,
							"SupportsTranscoding":  true,
							"DirectStreamUrl":      "/Videos/item-1/stream.mp4?direct=true",
							"TranscodingUrl":       "/Videos/item-1/master.m3u8?transcode=true",
						}},
					})
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			info, err := newTestEmby(server).buildPlaybackInfo(context.Background(), "item-1", embyPlaybackInfoRequest{
				PlaybackMode:        tt.mode,
				MaxStreamingBitrate: tt.maxStreamingBitrate,
				DirectPlayProfiles: []embyPlaybackDirectPlayProfile{
					{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac"},
				},
			})
			if err != nil {
				t.Fatalf("buildPlaybackInfo() error = %v", err)
			}
			if info.PlaybackMethod != tt.wantMethod {
				t.Fatalf("playback method = %q, want %q", info.PlaybackMethod, tt.wantMethod)
			}
			parsed, err := url.Parse(info.PlaybackURL)
			if err != nil {
				t.Fatalf("parse playback URL: %v", err)
			}
			if parsed.Query().Encode() == "" || !containsQuery(info.PlaybackURL, tt.wantURLQueryParameter) {
				t.Fatalf("playback URL = %q, want query %q", info.PlaybackURL, tt.wantURLQueryParameter)
			}
			if playbackPayload["EnableTranscoding"] != tt.wantTranscoding || playbackPayload["EnableDirectPlay"] != tt.wantDirectPlay || playbackPayload["EnableDirectStream"] != tt.wantDirectStream {
				t.Fatalf("playback flags = %#v", playbackPayload)
			}
			if tt.maxStreamingBitrate > 0 && playbackPayload["MaxStreamingBitrate"] != float64(tt.maxStreamingBitrate) {
				t.Fatalf("max bitrate = %#v, want %d", playbackPayload["MaxStreamingBitrate"], tt.maxStreamingBitrate)
			}
			if len(info.TranscodingQualities) == 0 {
				t.Fatal("transcoding qualities are empty")
			}
		})
	}
}

func containsQuery(rawURL, expected string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	parts, err := url.ParseQuery(expected)
	if err != nil {
		return false
	}
	for key, values := range parts {
		for _, value := range values {
			if parsed.Query().Get(key) != value {
				return false
			}
		}
	}
	return true
}
