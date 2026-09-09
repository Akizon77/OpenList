package emby

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

const sidecarIDPrefix = "emby-sidecar:"

type sidecarRef struct {
	ItemID        string
	MediaSourceID string
	StreamIndex   int
	Kind          string
	Extension     string
	DeliveryURL   string
}

func encodeSidecarRef(ref sidecarRef) (string, error) {
	data, err := json.Marshal(ref)
	if err != nil {
		return "", err
	}
	return sidecarIDPrefix + base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeSidecarRef(id string) (sidecarRef, bool) {
	if !strings.HasPrefix(id, sidecarIDPrefix) {
		return sidecarRef{}, false
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(id, sidecarIDPrefix))
	if err != nil {
		return sidecarRef{}, false
	}
	var ref sidecarRef
	if err := json.Unmarshal(data, &ref); err != nil {
		return sidecarRef{}, false
	}
	if ref.ItemID == "" || ref.MediaSourceID == "" || ref.StreamIndex < 0 {
		return sidecarRef{}, false
	}
	return ref, true
}

func (d *Emby) sidecarObjects(item embyItem, parentPath, displayName string, modifiedTime model.Obj) ([]model.Obj, error) {
	if item.IsFolder || len(item.MediaSources) == 0 {
		return nil, nil
	}
	baseName := strings.TrimSuffix(displayName, path.Ext(displayName))
	seen := make(map[string]struct{})
	result := make([]model.Obj, 0)
	for _, source := range item.MediaSources {
		if source.ID == "" {
			continue
		}
		for _, stream := range source.MediaStreams {
			kind, extension, ok := sidecarStreamInfo(stream)
			if !ok {
				continue
			}
			label := sidecarStreamLabel(stream)
			name := fmt.Sprintf("%s.%s.%s", baseName, label, extension)
			if _, exists := seen[name]; exists {
				continue
			}
			seen[name] = struct{}{}
			id, err := encodeSidecarRef(sidecarRef{
				ItemID:        item.ID,
				MediaSourceID: source.ID,
				StreamIndex:   stream.Index,
				Kind:          kind,
				Extension:     extension,
				DeliveryURL:   strings.TrimSpace(stream.DeliveryURL),
			})
			if err != nil {
				return nil, err
			}
			result = append(result, &model.Object{
				ID:       id,
				Name:     name,
				Path:     path.Join(parentPath, name),
				Modified: modifiedTime.ModTime(),
				IsFolder: false,
			})
		}
	}
	return result, nil
}

func sidecarStreamInfo(stream embyMediaStream) (kind, extension string, ok bool) {
	if !stream.IsExternal && !stream.SupportsExternalStream && strings.TrimSpace(stream.DeliveryURL) == "" {
		return "", "", false
	}
	switch strings.ToLower(strings.TrimSpace(stream.Type)) {
	case "subtitle":
		return "subtitle", subtitleExtension(stream.Codec), true
	case "audio":
		extension = strings.ToLower(strings.TrimSpace(stream.Codec))
		if extension == "" {
			return "", "", false
		}
		return "audio", extension, strings.TrimSpace(stream.DeliveryURL) != ""
	default:
		return "", "", false
	}
}

func sidecarStreamLabel(stream embyMediaStream) string {
	label := strings.TrimSpace(stream.DisplayTitle)
	if label == "" {
		label = strings.TrimSpace(stream.Title)
	}
	if label == "" {
		label = strings.TrimSpace(stream.Language)
	}
	if label == "" {
		label = strings.TrimSpace(stream.Codec)
	}
	if label == "" {
		label = fmt.Sprintf("track%d", stream.Index)
	}
	label = strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(label)
	return strings.Trim(label, " .")
}

func (d *Emby) sidecarURL(ref sidecarRef) (string, error) {
	if ref.DeliveryURL != "" {
		return d.resolveEmbyURL(ref.DeliveryURL)
	}
	if ref.Kind != "subtitle" {
		return "", fmt.Errorf("emby sidecar %s has no delivery url", ref.Kind)
	}
	return d.buildAuthenticatedURL(
		path.Join("/Videos", ref.ItemID, ref.MediaSourceID, "Subtitles", fmt.Sprintf("%d", ref.StreamIndex), "Stream."+ref.Extension),
		url.Values{},
	)
}
