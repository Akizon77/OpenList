package emby

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

func TestListAddsResumeFolderOnlyAtMountRoot(t *testing.T) {
	for _, test := range []struct {
		name     string
		rootID   string
		parentID string
		endpoint string
		wantRoot bool
	}{
		{"views", "", "", "/Users/test-user/Views", true},
		{"custom root", "library", "library", "/Users/test-user/Items", true},
		{"nested folder", "", "season", "/Users/test-user/Items", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != test.endpoint {
					t.Errorf("endpoint = %q, want %q", r.URL.Path, test.endpoint)
				}
				_ = json.NewEncoder(w).Encode(listResp{
					Items:            []embyItem{{ID: "child", Name: "Library", IsFolder: true}},
					TotalRecordCount: intPointer(1),
				})
			}))
			defer server.Close()
			d := newTestEmby(server)
			d.RootFolderID = test.rootID
			objects, err := d.List(context.Background(), &model.Object{ID: test.parentID, Path: "/"}, model.ListArgs{})
			if err != nil {
				t.Fatal(err)
			}
			wantLength := 1
			if test.wantRoot {
				wantLength++
				folder := objects[0]
				if folder.GetID() != embyResumeFolderID || folder.GetName() != embyResumeFolderName ||
					!folder.IsDir() || folder.GetPath() != "/"+embyResumeFolderName {
					t.Fatalf("resume folder = %#v", folder)
				}
			}
			if len(objects) != wantLength {
				t.Fatalf("objects count = %d, want %d", len(objects), wantLength)
			}
			if objects[len(objects)-1].GetID() != "child" {
				t.Fatal("existing library entry was lost")
			}
		})
	}
}

func TestResumeItemsPaginateWithinConfiguredRoot(t *testing.T) {
	for _, rootID := range []string{"", "library"} {
		t.Run("root="+rootID, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Path != "/Users/test-user/Items/Resume" {
					t.Errorf("endpoint = %q", r.URL.Path)
				}
				query := r.URL.Query()
				for key, want := range map[string]string{
					"ParentId": rootID, "Recursive": "true", "MediaTypes": "Video",
					"SortBy": "DatePlayed", "SortOrder": "Descending",
					"Limit": strconv.Itoa(embyPageSize),
				} {
					if got := query.Get(key); got != want {
						t.Errorf("%s = %q, want %q", key, got, want)
					}
				}
				start, _ := strconv.Atoi(query.Get("StartIndex"))
				count := embyPageSize
				if start == embyPageSize {
					count = 1
				}
				items := make([]embyItem, count)
				for i := range items {
					items[i] = embyItem{
						ID: fmt.Sprintf("item-%d", start+i), Name: "Episode", SeriesName: "Series",
						ParentIndex: intPointer(1), IndexNumber: intPointer(2),
						MediaSources: []embyMediaSource{{ID: "source", Container: "mkv"}},
						UserData:     embyUserData{LastPlayedDate: "2026-09-15T12:34:56Z"},
					}
				}
				_ = json.NewEncoder(w).Encode(listResp{Items: items, TotalRecordCount: intPointer(embyPageSize + 1)})
			}))
			defer server.Close()
			d := newTestEmby(server)
			d.RootFolderID = rootID
			folderPath := "/" + embyResumeFolderName
			objects, err := d.List(context.Background(), &model.Object{ID: embyResumeFolderID, Path: folderPath}, model.ListArgs{})
			if err != nil {
				t.Fatal(err)
			}
			if requests != 2 || len(objects) != embyPageSize+1 {
				t.Fatalf("requests = %d, objects = %d", requests, len(objects))
			}
			first := objects[0]
			if first.GetID() != "item-0" || first.GetName() != "Series Episode - [S01E02] (IDitem-0).mkv" ||
				first.IsDir() || first.GetPath() != path.Join(folderPath, first.GetName()) {
				t.Fatalf("resume media = %#v", first)
			}
			wantModified, _ := time.Parse(time.RFC3339Nano, "2026-09-15T12:34:56Z")
			if !first.ModTime().Equal(wantModified) {
				t.Fatalf("resume media modified = %v, want %v", first.ModTime(), wantModified)
			}
		})
	}
}

func TestResumeItemsBackfillLastPlayedDatesSequentially(t *testing.T) {
	var active atomic.Int32
	var maxActive atomic.Int32
	var detailRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Users/test-user/Items/Resume":
			_ = json.NewEncoder(w).Encode(listResp{
				Items: []embyItem{
					{ID: "item-1", Name: "Episode 1", DateCreated: "2024-01-01T00:00:00Z"},
					{ID: "item-2", Name: "Episode 2", DateCreated: "2024-01-02T00:00:00Z"},
				},
				TotalRecordCount: intPointer(2),
			})
		case "/Users/test-user/Items/item-1", "/Users/test-user/Items/item-2":
			detailRequests.Add(1)
			current := active.Add(1)
			for {
				previous := maxActive.Load()
				if current <= previous || maxActive.CompareAndSwap(previous, current) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
			active.Add(-1)
			lastPlayed := "2026-09-13T10:28:06Z"
			if r.URL.Path == "/Users/test-user/Items/item-2" {
				lastPlayed = "2026-09-14T10:28:06Z"
			}
			_ = json.NewEncoder(w).Encode(itemDetailResp{
				embyItem: embyItem{
					ID:       strings.TrimPrefix(r.URL.Path, "/Users/test-user/Items/"),
					UserData: embyUserData{LastPlayedDate: lastPlayed},
				},
			})
		default:
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	items, err := newTestEmby(server).getResumeItems(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if detailRequests.Load() != 2 || maxActive.Load() != 1 {
		t.Fatalf("detail requests = %d, max active = %d", detailRequests.Load(), maxActive.Load())
	}
	if items[0].UserData.LastPlayedDate != "2026-09-13T10:28:06Z" ||
		items[1].UserData.LastPlayedDate != "2026-09-14T10:28:06Z" {
		t.Fatalf("resume dates = %#v", []string{
			items[0].UserData.LastPlayedDate,
			items[1].UserData.LastPlayedDate,
		})
	}
}

func TestPlaybackStopRefreshesResumeDirectory(t *testing.T) {
	resumeRequests := 0
	stopped := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Users/test-user/Views":
			_ = json.NewEncoder(w).Encode(listResp{TotalRecordCount: intPointer(0)})
		case "/Users/test-user/Items/Resume":
			resumeRequests++
			items := []embyItem{{ID: "item-1", Name: "Movie.mp4"}}
			if stopped {
				items = nil
			}
			_ = json.NewEncoder(w).Encode(listResp{Items: items, TotalRecordCount: intPointer(len(items))})
		case "/Users/test-user/Items/item-1":
			_ = json.NewEncoder(w).Encode(itemDetailResp{
				embyItem: embyItem{ID: "item-1", UserData: embyUserData{LastPlayedDate: "2026-09-15T12:34:56Z"}},
			})
		case "/Sessions/Playing/Stopped":
			stopped = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	d := newTestEmby(server)
	d.Status = op.WORK
	d.MountPath = "/resume-cache-test"
	d.CacheExpiration = 30
	t.Cleanup(func() { op.Cache.DeleteDirectoryTree(d, "/") })
	ctx := context.Background()
	args := model.ListArgs{SkipHook: true}
	folderPath := "/" + embyResumeFolderName
	for range 2 {
		objects, err := op.List(ctx, d, folderPath, args)
		if err != nil || len(objects) != 1 {
			t.Fatalf("list = %#v, error = %v", objects, err)
		}
	}
	if resumeRequests != 1 {
		t.Fatalf("cached resume requests = %d, want 1", resumeRequests)
	}
	if err := d.reportPlayback(ctx, embyPlaybackStopMethod, "item-1", embyPlaybackReportRequest{PlaySessionID: "session"}); err != nil {
		t.Fatal(err)
	}
	objects, err := op.List(ctx, d, folderPath, args)
	if err != nil || len(objects) != 0 || resumeRequests != 2 {
		t.Fatalf("refreshed list = %#v, error = %v, requests = %d", objects, err, resumeRequests)
	}
}
