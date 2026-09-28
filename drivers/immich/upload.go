package immich

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/google/uuid"
)

func (d *Immich) Put(ctx context.Context, dstDir model.Obj, file model.FileStreamer, up driver.UpdateProgress) error {
	dstID, err := destinationID(dstDir)
	if err != nil {
		return err
	}
	if file.GetExist() != nil {
		return errs.ObjectAlreadyExists
	}
	if file.GetSize() < 0 {
		if _, err := file.CacheFullAndWriter(nil, nil); err != nil {
			return err
		}
	}
	if file.GetSize() == 0 {
		return fmt.Errorf("cannot upload an empty asset")
	}
	if up == nil {
		up = func(float64) {}
	}
	created, modified := file.CreateTime(), file.ModTime()
	if created.IsZero() {
		created = time.Now()
	}
	if modified.IsZero() {
		modified = created
	}

	var envelope bytes.Buffer
	writer := multipart.NewWriter(&envelope)
	for key, value := range map[string]string{
		"deviceId":       "OpenList",
		"deviceAssetId":  uuid.NewString(),
		"filename":       file.GetName(),
		"fileCreatedAt":  created.UTC().Format(time.RFC3339Nano),
		"fileModifiedAt": modified.UTC().Format(time.RFC3339Nano),
		"metadata":       "[]",
	} {
		if err := writer.WriteField(key, value); err != nil {
			return err
		}
	}
	if _, err := writer.CreateFormFile("assetData", file.GetName()); err != nil {
		return err
	}
	// Keep only the multipart envelope in memory; stream the asset itself.
	prefix := bytes.Clone(envelope.Bytes())
	envelope.Reset()
	if err := writer.Close(); err != nil {
		return err
	}
	reader := driver.NewLimitedUploadStream(ctx, &driver.ReaderUpdatingProgress{
		Reader:         file,
		UpdateProgress: up,
	})
	body := io.MultiReader(bytes.NewReader(prefix), reader, bytes.NewReader(envelope.Bytes()))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.Endpoint+"/assets", body)
	if err != nil {
		return err
	}
	req.ContentLength = int64(len(prefix)) + file.GetSize() + int64(envelope.Len())
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", base.UserAgent)
	req.Header.Set("x-api-key", d.APIKey)

	client := *base.HttpClient
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return responseError(http.MethodPost, "/assets", resp.StatusCode, data)
	}
	var result UploadResponse
	if err := utils.Json.Unmarshal(data, &result); err != nil {
		return err
	}
	if result.ID == "" {
		return fmt.Errorf("immich upload returned no asset ID")
	}
	if dstID != unassignedID {
		if _, err := d.albumAsset(ctx, http.MethodPut, dstID, result.ID); err != nil {
			return fmt.Errorf("asset %s uploaded, but could not be added to the album: %w", result.ID, err)
		}
	} else if result.Status == "duplicate" {
		albums, err := d.getAlbums(ctx, result.ID)
		if err != nil {
			return err
		}
		if len(albums) != 0 {
			return fmt.Errorf("asset %s already belongs to an album", result.ID)
		}
	}
	up(100)
	return nil
}
