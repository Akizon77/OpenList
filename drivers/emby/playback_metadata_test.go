package emby

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetItemDetailRequestsAndParsesDanmakuMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fields := r.URL.Query().Get("Fields")
		for _, field := range []string{"Type", "SeriesName", "OriginalTitle", "ParentIndexNumber", "IndexNumber"} {
			if !strings.Contains(fields, field) {
				t.Errorf("Fields %q does not contain %q", fields, field)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Id":                "item-1",
			"Name":              "Episode One",
			"Type":              "Episode",
			"SeriesName":        "Series",
			"OriginalTitle":     "Original Series",
			"ParentIndexNumber": 0,
			"IndexNumber":       1,
			"MediaSources":      []any{},
		})
	}))
	defer server.Close()

	detail, err := newTestEmby(server).getItemDetail(context.Background(), "item-1")
	if err != nil {
		t.Fatalf("getItemDetail() error = %v", err)
	}
	if detail.Type != "Episode" || detail.SeriesName != "Series" || detail.OriginalTitle != "Original Series" {
		t.Fatalf("detail metadata = %#v", detail)
	}
	if detail.ParentIndex == nil || *detail.ParentIndex != 0 || detail.IndexNumber == nil || *detail.IndexNumber != 1 {
		t.Fatalf("episode metadata = season:%v episode:%v", detail.ParentIndex, detail.IndexNumber)
	}
}

func TestBuildPlaybackInfoReturnsDanmakuMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Id":                "item-1",
			"Name":              "Episode One",
			"Type":              "Episode",
			"SeriesName":        "Series",
			"OriginalTitle":     "Original Series",
			"ParentIndexNumber": 2,
			"IndexNumber":       7,
			"MediaType":         "Video",
			"MediaSources": []any{
				map[string]any{
					"Id":                 "source-1",
					"Container":          "mkv",
					"SupportsDirectPlay": true,
				},
			},
		})
	}))
	defer server.Close()

	info, err := newTestEmby(server).buildPlaybackInfo(context.Background(), "item-1", embyPlaybackInfoRequest{
		Mode: "external",
	})
	if err != nil {
		t.Fatalf("buildPlaybackInfo() error = %v", err)
	}
	if info.ItemType != "Episode" || info.SeriesName != "Series" || info.OriginalTitle != "Original Series" {
		t.Fatalf("info metadata = %#v", info)
	}
	if info.SeasonNumber == nil || *info.SeasonNumber != 2 || info.EpisodeNumber == nil || *info.EpisodeNumber != 7 {
		t.Fatalf("episode metadata = season:%v episode:%v", info.SeasonNumber, info.EpisodeNumber)
	}
}
