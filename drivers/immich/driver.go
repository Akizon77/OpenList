package immich

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/go-resty/resty/v2"
)

type Immich struct {
	model.Storage
	Addition
	client *resty.Client
}

func (d *Immich) Config() driver.Config {
	return config
}

func (d *Immich) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *Immich) Init(ctx context.Context) error {
	d.Endpoint = strings.TrimRight(strings.TrimSpace(d.Endpoint), "/")
	endpoint, err := url.Parse(d.Endpoint)
	if err != nil || endpoint.Host == "" || endpoint.Scheme != "http" && endpoint.Scheme != "https" ||
		endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" || endpoint.User != nil {
		return fmt.Errorf("endpoint must be an HTTP(S) Immich server URL without credentials, query or fragment")
	}
	if !strings.HasSuffix(endpoint.Path, "/api") {
		d.Endpoint += "/api"
	}
	d.APIKey = strings.TrimSpace(d.APIKey)
	if d.APIKey == "" {
		return fmt.Errorf("api_key is required")
	}

	d.WebProxy = true
	d.WebdavPolicy = "native_proxy"
	d.DownProxyURL = ""
	d.client = base.NewRestyClient().SetRetryCount(0).
		SetHeader("x-api-key", d.APIKey).
		SetHeader("Accept", "application/json").
		SetRedirectPolicy(resty.NoRedirectPolicy())

	_, err = d.getAlbums(ctx, "")
	return err
}

func (d *Immich) Drop(ctx context.Context) error {
	return nil
}

func (d *Immich) GetRoot(ctx context.Context) (model.Obj, error) {
	return &Object{Object: model.Object{
		ID:       rootID,
		Name:     "root",
		Path:     "/",
		Modified: d.Modified,
		IsFolder: true,
		Mask:     model.Locked | model.NoCopy | model.NoWrite,
	}}, nil
}

func (d *Immich) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	if !dir.IsDir() {
		return nil, errs.NotFolder
	}
	if dir.GetID() == rootID {
		albums, err := d.getAlbums(ctx, "")
		if err != nil {
			return nil, err
		}
		objects := make([]*Object, 0, len(albums))
		for _, album := range albums {
			objects = append(objects, &Object{Object: model.Object{
				ID:       album.ID,
				Name:     album.Name,
				Modified: album.UpdatedAt,
				Ctime:    album.CreatedAt,
				IsFolder: true,
				Mask:     model.Locked | model.NoCopy,
			}})
		}
		result := namedObjects(objects, "/", unassignedName)
		return append(result, &Object{Object: model.Object{
			ID:       unassignedID,
			Name:     unassignedName,
			Path:     path.Join("/", unassignedName),
			Modified: d.Modified,
			IsFolder: true,
			Mask:     model.Locked | model.NoCopy,
		}}), nil
	}

	assets, err := d.getAssets(ctx, dir.GetID())
	if err != nil {
		return nil, err
	}
	objects := make([]*Object, 0, len(assets))
	for _, asset := range assets {
		if !asset.IsTrashed && asset.Visibility != "hidden" && asset.Visibility != "locked" {
			objects = append(objects, asset.object(dir.GetID()))
		}
	}
	result := namedObjects(objects, dir.GetPath())
	d.setThumbnails(ctx, objects, args.ReqPath)
	return result, nil
}

func (d *Immich) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	if file.IsDir() {
		return nil, errs.NotFile
	}
	if file.GetID() == "" {
		return nil, errs.ObjectNotFound
	}
	if args.Type == "thumb" {
		return d.thumbnailLink(ctx, file.GetID())
	}
	expiration := time.Minute * 5
	return &model.Link{
		URL:    d.Endpoint + "/assets/" + url.PathEscape(file.GetID()) + "/original",
		Header: http.Header{"X-Api-Key": []string{d.APIKey}},
		RequestHeaderAllowlist: []string{
			"Accept", "Accept-Encoding", "Range", "If-Range", "If-None-Match", "If-Modified-Since",
		},
		Expiration: &expiration,
	}, nil
}

func (d *Immich) MakeDir(ctx context.Context, parentDir model.Obj, dirName string) error {
	return errs.NotSupport
}

func (d *Immich) Rename(ctx context.Context, srcObj model.Obj, newName string) error {
	return errs.NotSupport
}

func (d *Immich) Move(ctx context.Context, srcObj, dstDir model.Obj) error {
	asset, err := sourceAsset(srcObj)
	if err != nil {
		return err
	}
	dstID, err := destinationID(dstDir)
	if err != nil {
		return err
	}
	if asset.AlbumID == dstID {
		return nil
	}
	if dstID == unassignedID {
		albums, err := d.getAlbums(ctx, asset.ID)
		if err != nil {
			return err
		}
		for _, album := range albums {
			if _, err := d.albumAsset(ctx, http.MethodDelete, album.ID, asset.ID); err != nil {
				return err
			}
		}
		return nil
	}

	added, err := d.albumAsset(ctx, http.MethodPut, dstID, asset.ID)
	if err != nil || asset.AlbumID == unassignedID {
		return err
	}
	if _, err := d.albumAsset(ctx, http.MethodDelete, asset.AlbumID, asset.ID); err != nil {
		if added {
			_, rollbackErr := d.albumAsset(context.WithoutCancel(ctx), http.MethodDelete, dstID, asset.ID)
			return errors.Join(err, rollbackErr)
		}
		return err
	}
	return nil
}

func (d *Immich) Copy(ctx context.Context, srcObj, dstDir model.Obj) error {
	// Do not return NotSupport here: it enables the generic download/upload fallback.
	if srcObj.IsDir() {
		return fmt.Errorf("copying albums is not supported")
	}
	asset, err := sourceAsset(srcObj)
	if err != nil {
		return err
	}
	dstID, err := destinationID(dstDir)
	if err != nil {
		return err
	}
	if dstID == unassignedID {
		return fmt.Errorf("copying to assets not in an album is not supported")
	}
	_, err = d.albumAsset(ctx, http.MethodPut, dstID, asset.ID)
	return err
}

func (d *Immich) Remove(ctx context.Context, obj model.Obj) error {
	asset, err := sourceAsset(obj)
	if err != nil {
		return err
	}
	return d.request(ctx, http.MethodDelete, "/assets", func(req *resty.Request) {
		req.SetBody(base.Json{"ids": []string{asset.ID}, "force": false})
	}, nil)
}

var _ driver.Driver = (*Immich)(nil)
var _ driver.GetRooter = (*Immich)(nil)
var _ driver.Mkdir = (*Immich)(nil)
var _ driver.Rename = (*Immich)(nil)
var _ driver.Move = (*Immich)(nil)
var _ driver.Copy = (*Immich)(nil)
var _ driver.Remove = (*Immich)(nil)
var _ driver.Put = (*Immich)(nil)
