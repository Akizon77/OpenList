package immich

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/sign"
	"github.com/OpenListTeam/OpenList/v4/internal/stream"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/OpenListTeam/OpenList/v4/server/common"
)

func (d *Immich) setThumbnails(ctx context.Context, objects []*Object, reqPath string) {
	for _, obj := range objects {
		if obj.AssetType != "IMAGE" && obj.AssetType != "VIDEO" {
			continue
		}
		filePath := path.Join(reqPath, obj.Name)
		if reqPath == "" {
			filePath = utils.GetFullPath(d.MountPath, obj.Path)
		}
		obj.Thumbnail.Thumbnail = fmt.Sprintf("%s/p%s?type=thumb&sign=%s",
			common.GetApiUrl(ctx), utils.EncodePath(filePath, true), sign.Sign(filePath))
	}
}

func (d *Immich) thumbnailLink(ctx context.Context, assetID string) (*model.Link, error) {
	apiPath := "/assets/" + url.PathEscape(assetID) + "/thumbnail?size=thumbnail"
	resp, err := d.client.R().SetContext(ctx).
		SetHeader("Accept", "image/*").
		SetDoNotParseResponse(true).
		Get(d.Endpoint + apiPath)
	if err != nil {
		return nil, err
	}
	defer resp.RawBody().Close()
	if !resp.IsSuccess() {
		body, _ := io.ReadAll(io.LimitReader(resp.RawBody(), 1024))
		return nil, responseError(http.MethodGet, apiPath, resp.StatusCode(), body)
	}

	// Buffer only the small thumbnail so range requests use its size, not the original asset's.
	const maxThumbnailSize = 10 << 20
	data, err := io.ReadAll(io.LimitReader(resp.RawBody(), maxThumbnailSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > maxThumbnailSize {
		return nil, fmt.Errorf("immich thumbnail has an invalid size: %d", len(data))
	}
	contentType := resp.Header().Get("Content-Type")
	if !strings.HasPrefix(contentType, "image/") {
		contentType = http.DetectContentType(data)
	}
	if !strings.HasPrefix(contentType, "image/") {
		return nil, fmt.Errorf("immich thumbnail has an invalid content type: %s", contentType)
	}

	size := int64(len(data))
	expiration := time.Minute * 5
	return &model.Link{
		RangeReader:   stream.GetRangeReaderFromMFile(size, bytes.NewReader(data)),
		ContentLength: size,
		Header:        http.Header{"Content-Type": []string{contentType}},
		Expiration:    &expiration,
	}, nil
}
