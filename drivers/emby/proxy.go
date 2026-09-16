package emby

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/Eyevinn/hls-m3u8/m3u8"
	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	internalnet "github.com/OpenListTeam/OpenList/v4/internal/net"
	"github.com/OpenListTeam/OpenList/v4/internal/sign"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

var embyProxyRequestHeaders = []string{
	"Range", "If-Range", "If-Match", "If-None-Match", "If-Modified-Since", "If-Unmodified-Since",
}

type embyProxyResource struct {
	URL      string `json:"url"`
	DeviceID string `json:"device_id"`
	Playlist bool   `json:"playlist,omitempty"`
}

func embyProxyHeaders(deviceID string) http.Header {
	return http.Header{
		"User-Agent":           {embyUserAgent},
		"X-Emby-Authorization": {embyAuthorization(deviceID)},
		"Accept":               {"*/*"},
		"Accept-Encoding":      {"identity"},
	}
}

func embyProxyURL(ctx context.Context, requestPath, rawURL, deviceID string, playlist bool) string {
	if rawURL == "" {
		return ""
	}
	data, _ := json.Marshal(embyProxyResource{URL: rawURL, DeviceID: deviceID, Playlist: playlist})
	resource := base64.RawURLEncoding.EncodeToString(data)
	query := url.Values{
		"resource": {resource},
		"sign":     {sign.SignEmby(requestPath, resource)},
	}
	api, _ := ctx.Value(conf.ApiUrlKey).(string)
	return api + "/ep" + utils.EncodePath(requestPath, true) + "?" + query.Encode()
}

func (d *Emby) proxyPlaybackInfo(ctx context.Context, requestPath string, info *embyPlaybackInfo) {
	proxy := func(rawURL string) string {
		return embyProxyURL(ctx, requestPath, rawURL, info.DeviceID, playbackURLType(rawURL, "") == "m3u8")
	}
	info.PlaybackURL = proxy(info.PlaybackURL)
	for i := range info.MediaSources {
		source := &info.MediaSources[i]
		source.DirectURL = proxy(source.DirectURL)
		for j := range source.AudioStreams {
			source.AudioStreams[j].URL = proxy(source.AudioStreams[j].URL)
		}
		for j := range source.SubtitleStreams {
			source.SubtitleStreams[j].URL = proxy(source.SubtitleStreams[j].URL)
		}
	}
}

// OpenProxyResource receives only resources verified by the proxy route's signature middleware.
func (d *Emby) OpenProxyResource(request *http.Request, requestPath, encoded string) (*http.Response, error) {
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	var resource embyProxyResource
	if err := json.Unmarshal(data, &resource); err != nil {
		return nil, err
	}
	target, err := url.Parse(resource.URL)
	if err != nil {
		return nil, err
	}
	server, err := url.Parse(d.URL)
	if err != nil {
		return nil, err
	}
	if target.Scheme == server.Scheme && target.Host == server.Host {
		token, _ := d.auth()
		query := target.Query()
		query.Set("api_key", token)
		target.RawQuery = query.Encode()
	}
	header := internalnet.ProcessHeader(request.Header, embyProxyHeaders(resource.DeviceID), embyProxyRequestHeaders)
	method := request.Method
	if resource.Playlist {
		// A rewritten playlist has its own length and must not reuse upstream ranges or validators.
		header = embyProxyHeaders(resource.DeviceID)
		method = http.MethodGet
	}
	response, err := internalnet.RequestHttp(request.Context(), method, header, target.String())
	if err != nil {
		return nil, err
	}
	if !resource.Playlist || response.StatusCode != http.StatusOK {
		return response, nil
	}
	defer response.Body.Close()
	content, err := rewriteEmbyPlaylist(response.Body, response.Request.URL, func(rawURL string, playlist bool) string {
		return embyProxyURL(request.Context(), requestPath, rawURL, resource.DeviceID, playlist)
	})
	if err != nil {
		return nil, err
	}
	response.Body = io.NopCloser(bytes.NewReader(content))
	response.ContentLength = int64(len(content))
	response.Header.Set("Content-Type", "application/vnd.apple.mpegurl")
	response.Header.Set("Content-Length", strconv.Itoa(len(content)))
	response.Header.Set("Cache-Control", "no-store")
	for _, name := range []string{"Etag", "Last-Modified", "Content-Encoding", "Content-Range", "Accept-Ranges"} {
		response.Header.Del(name)
	}
	return response, nil
}

func rewriteEmbyPlaylist(reader io.Reader, base *url.URL, proxy func(string, bool) string) ([]byte, error) {
	playlist, _, err := m3u8.DecodeFrom(reader, false)
	if err != nil {
		return nil, fmt.Errorf("decode emby playlist: %w", err)
	}
	type reference struct {
		uri      *string
		playlist bool
	}
	var refs []reference
	addKeys := func(keys []m3u8.Key) {
		for i := range keys {
			if keys[i].URI == "" {
				continue
			}
			refs = append(refs, reference{uri: &keys[i].URI})
		}
	}
	switch p := playlist.(type) {
	case *m3u8.MasterPlaylist:
		for _, variant := range p.Variants {
			refs = append(refs, reference{&variant.URI, true})
			for _, alternative := range variant.Alternatives {
				refs = append(refs, reference{&alternative.URI, true})
			}
		}
		for _, key := range p.SessionKeys {
			refs = append(refs, reference{uri: &key.URI})
		}
	case *m3u8.MediaPlaylist:
		addKeys(p.Keys)
		if p.Map != nil {
			refs = append(refs, reference{uri: &p.Map.URI})
		}
		for i := range p.PartialSegments {
			if p.PartialSegments[i] != nil {
				refs = append(refs, reference{uri: &p.PartialSegments[i].URI})
			}
		}
		if p.PreloadHints != nil {
			refs = append(refs, reference{uri: &p.PreloadHints.URI})
		}
		for _, segment := range p.Segments {
			if segment == nil {
				continue
			}
			refs = append(refs, reference{uri: &segment.URI})
			addKeys(segment.Keys)
			if segment.Map != nil {
				refs = append(refs, reference{uri: &segment.Map.URI})
			}
		}
	}
	// Renditions and initialization maps can be shared by several parsed entries.
	seen := make(map[*string]bool)
	for _, ref := range refs {
		if seen[ref.uri] || *ref.uri == "" {
			continue
		}
		seen[ref.uri] = true
		target, err := url.Parse(*ref.uri)
		if err != nil {
			return nil, err
		}
		if target.Scheme == "data" {
			continue
		}
		target = base.ResolveReference(target)
		if target.Scheme != "http" && target.Scheme != "https" {
			return nil, fmt.Errorf("unsupported emby playlist URI scheme: %s", target.Scheme)
		}
		*ref.uri = proxy(target.String(), ref.playlist || strings.EqualFold(path.Ext(target.Path), ".m3u8"))
	}
	return playlist.Encode().Bytes(), nil
}
