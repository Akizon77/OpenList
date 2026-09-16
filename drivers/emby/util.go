package emby

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var episodeCodeRegexp = regexp.MustCompile(`(?i)\bS\d{1,2}E\d{1,2}\b`)

const (
	embyPageSize         = 1000
	embyResumeFolderID   = "emby-resume"
	embyResumeFolderName = "\u7ee7\u7eed\u89c2\u770b"
)

func (d *Emby) login(ctx context.Context) error {
	d.authMu.Lock()
	defer d.authMu.Unlock()

	token, userID, err := d.authenticate(ctx)
	if err != nil {
		return err
	}
	d.token = token
	d.userID = userID
	return nil
}

func (d *Emby) getItems(ctx context.Context, parentID string) ([]embyItem, error) {
	_, userID := d.auth()
	query := url.Values{}
	query.Set("ParentId", parentID)
	query.Set("Recursive", "false")
	return d.getPagedItems(ctx, "/Users/"+userID+"/Items", query)
}

func (d *Emby) getResumeItems(ctx context.Context) ([]embyItem, error) {
	_, userID := d.auth()
	query := url.Values{}
	if rootID := strings.TrimSpace(d.RootFolderID); rootID != "" {
		query.Set("ParentId", rootID)
	}
	query.Set("Recursive", "true")
	query.Set("MediaTypes", "Video")
	query.Set("SortBy", "DatePlayed")
	query.Set("SortOrder", "Descending")
	return d.getPagedItems(ctx, "/Users/"+userID+"/Items/Resume", query)
}

func (d *Emby) getPagedItems(ctx context.Context, endpoint string, query url.Values) ([]embyItem, error) {
	query.Set("Fields", "Path,Size,DateCreated,SeriesName,IndexNumber,ParentIndexNumber,MediaSources,MediaStreams")
	items := make([]embyItem, 0)
	for startIndex := 0; ; {
		var page listResp
		query.Set("StartIndex", fmt.Sprintf("%d", startIndex))
		query.Set("Limit", fmt.Sprintf("%d", embyPageSize))
		if err := d.getJSON(ctx, endpoint, query, &page, "list"); err != nil {
			return nil, err
		}

		if page.TotalRecordCount == nil {
			return nil, fmt.Errorf("emby list response missing total record count at start index %d", startIndex)
		}
		totalRecordCount := *page.TotalRecordCount
		if totalRecordCount < 0 {
			return nil, fmt.Errorf("emby list response reported negative total record count %d", totalRecordCount)
		}
		if totalRecordCount < startIndex+len(page.Items) {
			return nil, fmt.Errorf("emby list response reported total record count %d below returned range ending at %d", totalRecordCount, startIndex+len(page.Items))
		}
		if len(page.Items) == 0 && startIndex < totalRecordCount {
			return nil, fmt.Errorf("emby list returned an empty page at start index %d before total count %d", startIndex, totalRecordCount)
		}
		items = append(items, page.Items...)
		startIndex += len(page.Items)
		if startIndex >= totalRecordCount || len(page.Items) == 0 {
			return items, nil
		}
	}
}
