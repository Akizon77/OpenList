package emby

import (
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func TestSidecarObjectsExposeExternalStreams(t *testing.T) {
	modified := &model.Object{Modified: time.Now()}
	item := embyItem{
		ID:   "item-1",
		Name: "episode.mkv",
		MediaSources: []embyMediaSource{{
			ID: "source-1",
			MediaStreams: []embyMediaStream{
				{
					Index:        2,
					Type:         "Subtitle",
					Codec:        "ass",
					DisplayTitle: "English",
					IsExternal:   true,
				},
				{
					Index:        3,
					Type:         "Subtitle",
					Codec:        "srt",
					DisplayTitle: "Embedded",
				},
				{
					Index:        4,
					Type:         "Audio",
					Codec:        "aac",
					DisplayTitle: "Commentary",
					DeliveryURL:  "https://example.test/commentary.aac",
					IsExternal:   true,
				},
			},
		}},
	}

	objects, err := (&Emby{}).sidecarObjects(item, "/episodes", "episode (IDitem-1).mkv", modified)
	if err != nil {
		t.Fatalf("sidecarObjects() error = %v", err)
	}
	if len(objects) != 2 {
		t.Fatalf("sidecarObjects() returned %d objects, want 2", len(objects))
	}
	if objects[0].GetName() != "episode (IDitem-1).English.ass" {
		t.Fatalf("subtitle name = %q", objects[0].GetName())
	}
	if objects[1].GetName() != "episode (IDitem-1).Commentary.aac" {
		t.Fatalf("audio name = %q", objects[1].GetName())
	}
	if _, ok := decodeSidecarRef(objects[0].GetID()); !ok {
		t.Fatal("subtitle sidecar id did not decode")
	}
}
