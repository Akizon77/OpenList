package emby

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"
)

func (d *Emby) getViews(ctx context.Context) ([]embyItem, error) {
	_, userID := d.auth()
	var data listResp
	if err := d.getJSON(ctx, "/Users/"+userID+"/Views", nil, &data, "views"); err != nil {
		return nil, err
	}
	if data.TotalRecordCount == nil {
		return nil, fmt.Errorf("emby views response missing total record count")
	}
	if *data.TotalRecordCount != len(data.Items) {
		return nil, fmt.Errorf("emby views response reported total record count %d but returned %d items", *data.TotalRecordCount, len(data.Items))
	}
	return data.Items, nil
}

func (d *Emby) getItemDetail(ctx context.Context, fileID string) (*itemDetailResp, error) {
	_, userID := d.auth()
	var detail itemDetailResp
	query := url.Values{}
	query.Set("Fields", "Type,SeriesName,OriginalTitle,ParentIndexNumber,IndexNumber,MediaSources,MediaType,RunTimeTicks,UserData")
	if err := d.getJSON(ctx, "/Users/"+userID+"/Items/"+fileID, query, &detail, "item detail"); err != nil {
		return nil, err
	}
	return &detail, nil
}

func embyItemExtension(item embyItem) string {
	candidates := make([]string, 0, len(item.MediaSources)*2+2)
	for _, source := range item.MediaSources {
		candidates = append(candidates, embyPathExtension(source.Path), embyContainerExtension(source.Container))
	}
	candidates = append(candidates, embyPathExtension(item.Path), path.Ext(strings.TrimSpace(item.Name)))

	fallback := ""
	for _, ext := range candidates {
		if ext == "" {
			continue
		}
		if fallback == "" {
			fallback = ext
		}
		if !strings.EqualFold(ext, ".strm") {
			return ext
		}
	}
	return fallback
}

func embyPathExtension(rawPath string) string {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return ""
	}
	if parsed, err := url.Parse(rawPath); err == nil && parsed.Path != "" {
		if ext := path.Ext(parsed.Path); ext != "" {
			return ext
		}
	}
	if index := strings.IndexAny(rawPath, "?#"); index >= 0 {
		rawPath = rawPath[:index]
	}
	return path.Ext(rawPath)
}

func embyContainerExtension(container string) string {
	container = strings.TrimSpace(strings.SplitN(container, ",", 2)[0])
	container = strings.TrimPrefix(container, ".")
	if container == "" {
		return ""
	}
	return "." + container
}

func selectMediaSource(mediaSources []embyMediaSource) (string, string) {
	var fallback *embyMediaSource
	for i := range mediaSources {
		source := &mediaSources[i]
		if strings.TrimSpace(source.ID) == "" {
			continue
		}
		if source.SupportsDirectStream {
			return strings.TrimSpace(source.ID), strings.TrimSpace(source.Container)
		}
		if fallback == nil {
			fallback = source
		}
	}
	if fallback != nil {
		return strings.TrimSpace(fallback.ID), strings.TrimSpace(fallback.Container)
	}
	return "", ""
}
