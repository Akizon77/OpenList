package emby

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Eyevinn/hls-m3u8/m3u8"
	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/internal/sign"
)

func setupEmbyProxyTest(t *testing.T) context.Context {
	t.Helper()
	previous := conf.Conf
	conf.Conf = conf.DefaultConfig("data")
	for key, value := range map[string]string{conf.Token: "proxy-test-secret", conf.LinkExpiration: "0"} {
		op.Cache.SetSetting(key, &model.SettingItem{Key: key, Value: value})
	}
	t.Cleanup(func() {
		conf.Conf = previous
		op.Cache.ClearAll()
	})
	return context.WithValue(context.Background(), conf.ApiUrlKey, "http://client.test/base")
}

func proxyBrowserHeaders() http.Header {
	return http.Header{
		"Origin": {"https://browser.test"}, "Referer": {"https://browser.test/watch"},
		"User-Agent": {"Mozilla/5.0"}, "Cookie": {"session=browser"},
		"Authorization": {"Bearer browser"}, "Sec-Ch-Ua": {"Chromium"},
		"Sec-Fetch-Site": {"cross-site"}, "Accept-Language": {"zh-CN"},
		"Forwarded": {"for=192.0.2.1"}, "X-Forwarded-For": {"192.0.2.1"},
		"X-Real-Ip": {"192.0.2.1"}, "Accept-Encoding": {"gzip, br"},
	}
}

func checkProxyFingerprint(t *testing.T, request *http.Request) {
	t.Helper()
	for key := range proxyBrowserHeaders() {
		if key == "User-Agent" || key == "Accept-Encoding" {
			continue
		}
		if got := request.Header.Get(key); got != "" {
			t.Errorf("%s leaked to upstream: %q", key, got)
		}
	}
	if request.UserAgent() != "Mahiro Client/0.0.1" {
		t.Errorf("upstream user agent = %q", request.UserAgent())
	}
	if got := request.Header.Get("X-Emby-Authorization"); !strings.Contains(got, `Client="Mahiro Client"`) {
		t.Errorf("upstream identity = %q", got)
	}
	if got := request.Header.Get("Accept-Encoding"); got != "identity" {
		t.Errorf("upstream encoding = %q", got)
	}
}

func TestPlaybackInfoProxyOnlyWhenEnabled(t *testing.T) {
	ctx := setupEmbyProxyTest(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{
			"Id":"item-1","MediaType":"Video","UserData":{"PlaybackPositionTicks":120000000},
			"MediaSources":[{"Id":"source-1","Container":"mp4","MediaStreams":[
				{"Index":1,"Type":"Audio","Codec":"aac","IsExternal":true,"DeliveryUrl":"/audio.aac"},
				{"Index":2,"Type":"Subtitle","Codec":"srt","IsExternal":true,"DeliveryUrl":"/subtitle.srt"}
			]}]}`)
	}))
	defer upstream.Close()
	for _, enabled := range []bool{false, true} {
		d := newTestEmby(upstream)
		d.WebProxy = enabled
		d.MountPath = "/library"
		result, err := d.Other(ctx, model.OtherArgs{
			Obj:    &model.Object{ID: "item-1", Path: "/movie.mp4"},
			Method: embyPlaybackInfoMethod,
			Data:   embyPlaybackInfoRequest{Mode: "external", DeviceID: "mahiro-web-test"},
		})
		if err != nil {
			t.Fatal(err)
		}
		info := result.(*embyPlaybackInfo)
		if info.PlaybackPositionTicks != 120000000 || info.PlaybackType != "mp4" {
			t.Fatalf("playback metadata changed: %#v", info)
		}
		for _, raw := range []string{
			info.PlaybackURL, info.MediaSources[0].DirectURL,
			info.MediaSources[0].AudioStreams[0].URL, info.MediaSources[0].SubtitleStreams[0].URL,
		} {
			if !enabled {
				if !strings.HasPrefix(raw, upstream.URL+"/") {
					t.Errorf("disabled proxy changed URL: %s", raw)
				}
				continue
			}
			u, err := url.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			if u.Host != "client.test" || u.Path != "/base/ep/library/movie.mp4" {
				t.Errorf("proxy URL = %s", raw)
			}
			if err := sign.VerifyEmby("/library/movie.mp4", u.Query().Get("resource"), u.Query().Get("sign")); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestProxyResourceRangeHeadAndConditionalRequests(t *testing.T) {
	ctx := setupEmbyProxyTest(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkProxyFingerprint(t, r)
		if got := r.URL.Query().Get("api_key"); got != "test-token" {
			t.Errorf("upstream token = %q", got)
		}
		w.Header().Set("Etag", `"media"`)
		http.ServeContent(w, r, "movie.mp4", time.Unix(100, 0), strings.NewReader("01234567"))
	}))
	defer upstream.Close()
	d := newTestEmby(upstream)
	raw := embyProxyURL(ctx, "/library/movie.mp4", upstream.URL+"/stream", "mahiro-web-test", false)
	u, _ := url.Parse(raw)
	for _, test := range []struct {
		method, rangeValue, etag, body string
		status                         int
	}{
		{http.MethodGet, "bytes=2-4", "", "234", http.StatusPartialContent},
		{http.MethodHead, "", "", "", http.StatusOK},
		{http.MethodGet, "", `"media"`, "", http.StatusNotModified},
	} {
		request := httptest.NewRequest(test.method, raw, nil).WithContext(ctx)
		request.Header = proxyBrowserHeaders()
		if test.rangeValue != "" {
			request.Header.Set("Range", test.rangeValue)
			request.Header.Set("If-Range", `"media"`)
		}
		if test.etag != "" {
			request.Header.Set("If-None-Match", test.etag)
		}
		response, err := d.OpenProxyResource(request, "/library/movie.mp4", u.Query().Get("resource"))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != test.status || string(body) != test.body {
			t.Errorf("%s: status=%d, body=%q", test.method, response.StatusCode, body)
		}
	}
}

func TestHLSProxyRewritesRedirectedPlaylistsAndResources(t *testing.T) {
	ctx := setupEmbyProxyTest(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkProxyFingerprint(t, r)
		switch r.URL.Path {
		case "/master.m3u8":
			http.Redirect(w, r, "/hls/master.m3u8", http.StatusFound)
		case "/hls/master.m3u8":
			_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"audio\",NAME=\"Audio\",URI=\"audio.m3u8\"\n#EXT-X-STREAM-INF:BANDWIDTH=1000000,AUDIO=\"audio\"\nvideo.m3u8\n")
		case "/hls/video.m3u8", "/hls/audio.m3u8":
			w.Header().Set("Etag", `"upstream-playlist"`)
			_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:6\n#EXT-X-MAP:URI=\"init.mp4\"\n#EXT-X-KEY:METHOD=AES-128,URI=\"key.bin\"\n#EXTINF:6.0,\nsegment.m4s?part=1\n#EXT-X-ENDLIST\n")
		case "/hls/init.mp4", "/hls/key.bin", "/hls/segment.m4s":
			if r.URL.Query().Get("api_key") != "test-token" {
				t.Error("HLS resource missing Emby authentication")
			}
			_, _ = io.WriteString(w, "media")
		default:
			t.Errorf("unexpected upstream path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()
	d := newTestEmby(upstream)
	fetch := func(raw string) []byte {
		t.Helper()
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if u.Host != "client.test" {
			t.Fatalf("resource escaped the proxy: %s", raw)
		}
		if err := sign.VerifyEmby("/library/movie.mp4", u.Query().Get("resource"), u.Query().Get("sign")); err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodGet, raw, nil).WithContext(ctx)
		request.Header = proxyBrowserHeaders()
		response, err := d.OpenProxyResource(request, "/library/movie.mp4", u.Query().Get("resource"))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		content, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.Header.Get("Content-Type") == "application/vnd.apple.mpegurl" {
			if response.Header.Get("Etag") != "" || response.ContentLength != int64(len(content)) {
				t.Fatal("rewritten playlist kept upstream validators or length")
			}
		}
		return content
	}
	masterBody := fetch(embyProxyURL(ctx, "/library/movie.mp4", upstream.URL+"/master.m3u8", "mahiro-web-test", true))
	parsed, _, err := m3u8.DecodeFrom(bytes.NewReader(masterBody), true)
	if err != nil {
		t.Fatal(err)
	}
	master := parsed.(*m3u8.MasterPlaylist)
	for _, raw := range []string{master.Variants[0].URI, master.Variants[0].Alternatives[0].URI} {
		mediaBody := fetch(raw)
		parsed, _, err := m3u8.DecodeFrom(bytes.NewReader(mediaBody), true)
		if err != nil {
			t.Fatal(err)
		}
		media := parsed.(*m3u8.MediaPlaylist)
		for _, resource := range []string{media.Map.URI, media.Keys[0].URI, media.Segments[0].URI} {
			if got := string(fetch(resource)); got != "media" {
				t.Errorf("resource body = %q", got)
			}
		}
	}
}

func TestEmbyProxySignatureBindsPathAndResource(t *testing.T) {
	ctx := setupEmbyProxyTest(t)
	u, _ := url.Parse(embyProxyURL(ctx, "/library/movie.mp4", "https://emby.test/movie", "device", false))
	resource, signature := u.Query().Get("resource"), u.Query().Get("sign")
	data, _ := json.Marshal(embyProxyResource{URL: "https://other.test/movie", DeviceID: "device"})
	tampered := base64.RawURLEncoding.EncodeToString(data)
	for _, test := range []struct{ path, resource, signature string }{
		{"/other/movie.mp4", resource, signature},
		{"/library/movie.mp4", tampered, signature},
		{"/library/movie.mp4", resource, ""},
		{"/library/movie.mp4", resource, sign.Sign("/library/movie.mp4")},
	} {
		if err := sign.VerifyEmby(test.path, test.resource, test.signature); err == nil {
			t.Errorf("accepted tampered proxy request: %#v", test)
		}
	}
}
