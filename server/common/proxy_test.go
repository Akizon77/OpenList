package common

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/emby"
	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

func TestProxyOverridesUpstreamContentDisposition(t *testing.T) {
	previousConfig := conf.Conf
	conf.Conf = conf.DefaultConfig("data")
	t.Cleanup(func() {
		conf.Conf = previousConfig
	})

	const content = "archive content"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="download"`)
		w.Header().Set("Content-Type", "application/x-rar-compressed")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, content)
	}))
	t.Cleanup(upstream.Close)

	file := &model.Object{
		Name: "测试文件.rar",
		Size: int64(len(content)),
	}
	link := &model.Link{URL: upstream.URL}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/sd/example", nil)

	err := Proxy(recorder, request, link, file)
	if err != nil {
		t.Fatalf("Proxy() error = %v", err)
	}
	if got, want := recorder.Code, http.StatusOK; got != want {
		t.Fatalf("status code = %d, want %d", got, want)
	}
	if got, want := recorder.Header().Get("Content-Disposition"), utils.GenerateContentDisposition(file.GetName()); got != want {
		t.Errorf("Content-Disposition = %q, want %q", got, want)
	}
	if got, want := recorder.Header().Get("Content-Type"), "application/x-rar-compressed"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
	if got, want := recorder.Body.String(), content; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestEmbyHeadersAcrossProxyModes(t *testing.T) {
	previous := conf.Conf
	conf.Conf = conf.DefaultConfig("data")
	t.Cleanup(func() { conf.Conf = previous })
	for _, embyLink := range []bool{false, true} {
		for _, mode := range []string{"transparent", "range", "multipart"} {
			t.Run(fmt.Sprintf("emby=%t/%s", embyLink, mode), func(t *testing.T) {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if embyLink {
						if r.Header.Get("Origin") != "" || r.Header.Get("Referer") != "" ||
							r.Header.Get("Sec-Fetch-Site") != "" || r.Header.Get("Cookie") != "" {
							t.Errorf("browser headers leaked in %s: %#v", mode, r.Header)
						}
						if r.UserAgent() != "Mahiro Client/0.0.1" {
							t.Errorf("upstream user agent = %q", r.UserAgent())
						}
					} else if mode != "range" && r.Header.Get("Origin") != "https://browser.test" {
						t.Error("other driver's forwarding policy changed")
					}
					http.ServeContent(w, r, "movie.mp4", time.Unix(100, 0), strings.NewReader("01234567"))
				}))
				defer upstream.Close()
				file := &model.Object{ID: "item", Name: "movie.mp4", Size: 8}
				link := &model.Link{URL: upstream.URL}
				if embyLink {
					d := &emby.Emby{Addition: emby.Addition{URL: upstream.URL, LinkMethod: "download"}}
					var err error
					link, err = d.Link(context.Background(), file, model.LinkArgs{})
					if err != nil {
						t.Fatal(err)
					}
				}
				link = link.Clone()
				switch mode {
				case "range":
					link = ProxyRange(context.Background(), link, file.GetSize())
				case "multipart":
					link.Concurrency, link.PartSize = 2, 2
				}
				request := httptest.NewRequest(http.MethodGet, "/p/movie.mp4", nil)
				request.Header = http.Header{
					"Origin": {"https://browser.test"}, "Referer": {"https://browser.test/watch"},
					"Sec-Fetch-Site": {"cross-site"}, "Cookie": {"session=browser"}, "Range": {"bytes=1-3"},
				}
				recorder := httptest.NewRecorder()
				if err := Proxy(recorder, request, link, file); err != nil {
					t.Fatal(err)
				}
				if recorder.Code != http.StatusPartialContent || recorder.Body.String() != "123" {
					t.Fatalf("proxy response = %d %q", recorder.Code, recorder.Body.String())
				}
			})
		}
	}
}
