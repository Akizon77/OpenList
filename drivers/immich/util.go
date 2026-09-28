package immich

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/go-resty/resty/v2"
)

func (d *Immich) request(ctx context.Context, method, apiPath string, callback base.ReqCallback, result any) error {
	req := d.client.R().SetContext(ctx)
	if callback != nil {
		callback(req)
	}
	if result != nil {
		req.SetResult(result)
	}
	resp, err := req.Execute(method, d.Endpoint+apiPath)
	if err != nil {
		return err
	}
	if !resp.IsSuccess() {
		return responseError(method, apiPath, resp.StatusCode(), resp.Body())
	}
	return nil
}

func responseError(method, apiPath string, status int, body []byte) error {
	message := strings.TrimSpace(string(body))
	if len(message) > 1024 {
		message = message[:1024]
	}
	return fmt.Errorf("immich %s %s: HTTP %d: %s", method, apiPath, status, message)
}

func (d *Immich) getAlbums(ctx context.Context, assetID string) ([]Album, error) {
	var albums []Album
	err := d.request(ctx, http.MethodGet, "/albums", func(req *resty.Request) {
		if assetID != "" {
			req.SetQueryParam("assetId", assetID)
		}
	}, &albums)
	return albums, err
}

func (d *Immich) getAssets(ctx context.Context, albumID string) ([]Asset, error) {
	if albumID != unassignedID {
		var album Album
		err := d.request(ctx, http.MethodGet, "/albums/"+url.PathEscape(albumID), nil, &album)
		if err != nil {
			return nil, err
		}
		// Older servers include assets here; newer servers require a metadata search.
		if album.Assets != nil {
			return album.Assets, nil
		}
	}

	assets := make([]Asset, 0)
	seen := make(map[string]bool)
	for _, visibility := range []string{"timeline", "archive"} {
		page := 1
		for {
			body := base.Json{
				"page":        page,
				"size":        1000,
				"withExif":    true,
				"withDeleted": false,
				"visibility":  visibility,
			}
			if albumID == unassignedID {
				body["isNotInAlbum"] = true
			} else {
				body["albumIds"] = []string{albumID}
			}
			var result SearchResponse
			err := d.request(ctx, http.MethodPost, "/search/metadata", func(req *resty.Request) {
				req.SetBody(body)
			}, &result)
			if err != nil {
				return nil, err
			}
			for _, asset := range result.Assets.Items {
				if !seen[asset.ID] {
					seen[asset.ID] = true
					assets = append(assets, asset)
				}
			}
			if result.Assets.NextPage == "" {
				break
			}
			nextPage, err := strconv.Atoi(result.Assets.NextPage)
			if err != nil || nextPage <= page {
				return nil, fmt.Errorf("invalid immich next page: %q", result.Assets.NextPage)
			}
			page = nextPage
		}
	}
	return assets, nil
}

// The boolean reports whether an association changed, for move rollback.
func (d *Immich) albumAsset(ctx context.Context, method, albumID, assetID string) (bool, error) {
	var results []BulkResult
	err := d.request(ctx, method, "/albums/"+url.PathEscape(albumID)+"/assets", func(req *resty.Request) {
		req.SetBody(base.Json{"ids": []string{assetID}})
	}, &results)
	if err != nil {
		return false, err
	}
	for _, result := range results {
		if result.ID != assetID {
			continue
		}
		if result.Success {
			return true, nil
		}
		if method == http.MethodPut && result.Error == "duplicate" ||
			method == http.MethodDelete && result.Error == "not_found" {
			return false, nil
		}
		return false, fmt.Errorf("immich album asset %s: %s %s", assetID, result.Error, result.ErrorMessage)
	}
	return false, fmt.Errorf("immich returned no result for asset %s", assetID)
}

func destinationID(dir model.Obj) (string, error) {
	if !dir.IsDir() {
		return "", errs.NotFolder
	}
	if dir.GetID() == "" || dir.GetID() == rootID {
		return "", errs.NotSupport
	}
	return dir.GetID(), nil
}

func sourceAsset(obj model.Obj) (*Object, error) {
	if obj.IsDir() {
		return nil, errs.NotSupport
	}
	asset, ok := obj.(*Object)
	if !ok || asset.ID == "" || asset.AlbumID == "" {
		return nil, errs.NotSupport
	}
	return asset, nil
}

func namedObjects(objects []*Object, parentPath string, reserved ...string) []model.Obj {
	counts := make(map[string]int, len(objects)+len(reserved))
	used := make(map[string]bool, len(objects)+len(reserved))
	for _, name := range reserved {
		counts[name]++
		used[name] = true
	}
	for _, obj := range objects {
		obj.Name = utils.MappingName(obj.Name)
		if obj.Name == "" || obj.Name == "." || obj.Name == ".." {
			obj.Name = obj.ID
		}
		counts[obj.Name]++
	}

	result := make([]model.Obj, 0, len(objects))
	for _, obj := range objects {
		name := obj.Name
		ext := ""
		if !obj.IsDir() {
			ext = path.Ext(name)
		}
		stem := strings.TrimSuffix(name, ext)
		if counts[name] > 1 {
			name = stem + " [" + obj.ID + "]" + ext
		}
		for i := 1; used[name] || name != obj.Name && counts[name] > 0; i++ {
			name = fmt.Sprintf("%s [%s-%d]%s", stem, obj.ID, i, ext)
		}
		used[name] = true
		obj.Name = name
		obj.Path = path.Join(parentPath, name)
		result = append(result, obj)
	}
	return result
}
