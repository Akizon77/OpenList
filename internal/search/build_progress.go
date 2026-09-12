package search

import (
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

func completedBuildProgress(previous *model.IndexProgress, indexedCount, scannedCount uint64, existingIndexPreserved bool, now time.Time, err error) *model.IndexProgress {
	progress := &model.IndexProgress{
		ObjCount:        indexedCount,
		ScannedCount:    scannedCount,
		IsDone:          true,
		LastAttemptTime: &now,
	}
	if err == nil {
		progress.LastDoneTime = &now
		return progress
	}

	progress.Error = err.Error()
	if previous != nil {
		progress.LastDoneTime = previous.LastDoneTime
		if existingIndexPreserved {
			progress.ObjCount = previous.ObjCount
		}
	}
	return progress
}

func normalizeIndexProgress(progress *model.IndexProgress) {
	if progress.Error != "" && progress.LastAttemptTime == nil {
		progress.LastAttemptTime = progress.LastDoneTime
		progress.LastDoneTime = nil
	}
}
