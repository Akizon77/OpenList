package immich

import (
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

const (
	rootID         = "root"
	unassignedID   = "unassigned"
	unassignedName = "\u672a\u6dfb\u52a0\u5230\u76f8\u518c"
)

type Object struct {
	model.Object
	model.Thumbnail
	AlbumID   string
	AssetType string
}

type Album struct {
	ID        string    `json:"id"`
	Name      string    `json:"albumName"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Assets    []Asset   `json:"assets"`
}

type Asset struct {
	ID               string    `json:"id"`
	Type             string    `json:"type"`
	OriginalFileName string    `json:"originalFileName"`
	FileCreatedAt    time.Time `json:"fileCreatedAt"`
	FileModifiedAt   time.Time `json:"fileModifiedAt"`
	IsTrashed        bool      `json:"isTrashed"`
	Visibility       string    `json:"visibility"`
	ExifInfo         struct {
		FileSize int64 `json:"fileSizeInByte"`
	} `json:"exifInfo"`
}

type SearchResponse struct {
	Assets struct {
		Items    []Asset `json:"items"`
		NextPage string  `json:"nextPage"`
	} `json:"assets"`
}

type BulkResult struct {
	ID           string `json:"id"`
	Success      bool   `json:"success"`
	Error        string `json:"error"`
	ErrorMessage string `json:"errorMessage"`
}

type UploadResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func (a Asset) object(albumID string) *Object {
	size := a.ExifInfo.FileSize
	if size <= 0 {
		// Unknown sizes must not trigger OpenList's empty-file overwrite cleanup.
		size = -1
	}
	return &Object{
		Object: model.Object{
			ID:       a.ID,
			Name:     a.OriginalFileName,
			Size:     size,
			Modified: a.FileModifiedAt,
			Ctime:    a.FileCreatedAt,
			Mask:     model.NoRename,
		},
		AlbumID:   albumID,
		AssetType: a.Type,
	}
}

var _ model.Thumb = (*Object)(nil)
