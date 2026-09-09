package search

import (
	"context"
	stderrors "errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/fs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	internalnet "github.com/OpenListTeam/OpenList/v4/internal/net"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/internal/search/searcher"
	"github.com/OpenListTeam/OpenList/v4/internal/setting"
	mapset "github.com/deckarep/golang-set/v2"
	log "github.com/sirupsen/logrus"
)

var (
	Quit = atomic.Pointer[chan struct{}]{}
)

func Running() bool {
	return Quit.Load() != nil
}

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

func BuildIndex(ctx context.Context, indexPaths, ignorePaths []string, maxDepth int, count bool) error {
	log.Infof("build index for: %+v", indexPaths)
	log.Infof("ignore paths: %+v", ignorePaths)
	requestRateLimit := setting.GetFloat(conf.IndexRequestRateLimit, 0)
	ctx = internalnet.WithRequestRateLimit(ctx, requestRateLimit)
	if requestRateLimit > 0 {
		log.Infof("index request rate limit: %.2f requests/s", requestRateLimit)
	}
	quit := make(chan struct{}, 1)
	if !Quit.CompareAndSwap(nil, &quit) {
		// other goroutine is running
		return errs.BuildIndexIsRunning
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopped := atomic.Bool{}
	defer func() {
		Quit.Store(nil)
	}()
	go func() {
		select {
		case <-quit:
			stopped.Store(true)
			cancel()
		case <-ctx.Done():
		}
	}()
	stopRequested := func() bool {
		return stopped.Load()
	}
	previousProgress := &model.IndexProgress{}
	if count {
		progress, err := Progress()
		if err != nil {
			log.Warnf("failed to read previous index progress: %+v", err)
		} else {
			previousProgress = progress
		}
	}
	var scannedCount uint64
	existingIndexPreserved := true
	lastProgressWrite := time.Time{}
	writeRunningProgress := func(objCount uint64) {
		if !count {
			return
		}
		WriteProgress(&model.IndexProgress{
			ObjCount:        objCount,
			ScannedCount:    scannedCount,
			IsDone:          false,
			LastDoneTime:    previousProgress.LastDoneTime,
			LastAttemptTime: previousProgress.LastAttemptTime,
		})
		lastProgressWrite = time.Now()
	}
	finish := func(indexedCount uint64, err error) {
		if !count {
			return
		}
		WriteProgress(completedBuildProgress(previousProgress, indexedCount, scannedCount, existingIndexPreserved, time.Now(), err))
		if err != nil {
			log.Errorf("index build failed after scanning %d objects; existing index preserved: %t; error: %+v", scannedCount, existingIndexPreserved, err)
		}
	}
	admin, err := op.GetAdmin()
	if err != nil {
		finish(0, err)
		return err
	}
	if count {
		writeRunningProgress(previousProgress.ObjCount)
	}
	staged := make([]ObjWithParent, 0)
	stagedPaths := make(map[string]struct{})
	for _, indexPath := range indexPaths {
		walkFn := func(indexPath string, info model.Obj) error {
			if stopRequested() {
				return filepath.SkipDir
			}
			for _, avoidPath := range ignorePaths {
				if strings.HasPrefix(indexPath, avoidPath) {
					return filepath.SkipDir
				}
			}
			if storage, _, err := op.GetStorageAndActualPath(indexPath); err == nil {
				if storage.GetStorage().DisableIndex {
					return filepath.SkipDir
				}
			}
			// ignore root
			if indexPath == "/" {
				return nil
			}
			if _, exists := stagedPaths[indexPath]; exists {
				return nil
			}
			stagedPaths[indexPath] = struct{}{}
			staged = append(staged, ObjWithParent{
				Obj:    info,
				Parent: path.Dir(indexPath),
			})
			scannedCount++
			if scannedCount%100 == 0 && time.Since(lastProgressWrite) >= time.Second {
				writeRunningProgress(previousProgress.ObjCount)
			}
			return nil
		}
		if count && indexPath == "/" {
			storages := op.GetAllStorages()
			if len(storages) > 0 {
				errs := make([]error, 0)
				for _, storage := range storages {
					if storage.GetStorage().DisableIndex {
						continue
					}
					mountPath := storage.GetStorage().MountPath
					if err := fs.WalkStorageFS(context.WithValue(ctx, conf.UserKey, admin), storage, mountPath, maxDepth, walkFn); err != nil {
						errs = append(errs, fmt.Errorf("storage %q (%s): %w", mountPath, storage.Config().Name, err))
						log.Errorf("index storage %q (%s) failed after scanning %d objects: %+v", mountPath, storage.Config().Name, scannedCount, err)
					}
				}
				if len(errs) > 0 {
					err = stderrors.Join(errs...)
					finish(0, err)
					return err
				}
				writeRunningProgress(previousProgress.ObjCount)
				continue
			}
		}
		fi, err := fs.Get(ctx, indexPath, &fs.GetArgs{})
		if err != nil {
			if stopRequested() {
				err = fmt.Errorf("index build stopped")
			} else {
				err = fmt.Errorf("get index path %s: %w", indexPath, err)
			}
			finish(0, err)
			return err
		}
		// TODO: run walkFS concurrently
		err = fs.WalkFS(context.WithValue(ctx, conf.UserKey, admin), maxDepth, indexPath, fi, walkFn)
		if err != nil {
			if stopRequested() {
				err = fmt.Errorf("index build stopped")
			} else {
				err = fmt.Errorf("walk index path %s: %w", indexPath, err)
			}
			finish(0, err)
			return err
		}
		if stopRequested() {
			err = fmt.Errorf("index build stopped")
			finish(0, err)
			return err
		}
		writeRunningProgress(previousProgress.ObjCount)
	}
	// Replace existing entries only after every storage walk has succeeded.
	if count {
		existingIndexPreserved = false
		if err = Clear(ctx); err != nil {
			finish(0, err)
			return err
		}
		writeRunningProgress(0)
	} else {
		for _, indexPath := range indexPaths {
			if err = Del(ctx, indexPath); err != nil {
				return fmt.Errorf("delete old index on %s: %w", indexPath, err)
			}
		}
	}
	for start := 0; start < len(staged); start += searchBatchSize {
		end := start + searchBatchSize
		if end > len(staged) {
			end = len(staged)
		}
		if err = BatchIndex(ctx, staged[start:end]); err != nil {
			finish(uint64(start), err)
			return err
		}
		writeRunningProgress(uint64(end))
	}
	log.Infof("success build index, count: %d", len(staged))
	finish(uint64(len(staged)), nil)
	return nil
}

func Del(ctx context.Context, prefix string) error {
	return instance.Del(ctx, prefix)
}

func Clear(ctx context.Context) error {
	return instance.Clear(ctx)
}

func Config(ctx context.Context) searcher.Config {
	return instance.Config()
}

func Update(ctx context.Context, parent string, objs []model.Obj) {
	if instance == nil || !instance.Config().AutoUpdate || !setting.GetBool(conf.AutoUpdateIndex) || Running() {
		return
	}
	if isIgnorePath(parent) {
		return
	}
	// only update when index have built
	progress, err := Progress()
	if err != nil {
		log.Errorf("update search index error while get progress: %+v", err)
		return
	}
	if !progress.IsDone {
		return
	}

	// Use task queue for Meilisearch to avoid race conditions with async indexing
	if msInstance, ok := instance.(interface {
		EnqueueUpdate(parent string, objs []model.Obj)
	}); ok {
		// Enqueue task for async processing (diff calculation happens at consumption time)
		msInstance.EnqueueUpdate(parent, objs)
		return
	}

	unlock := lockUpdate(parent)
	defer unlock()

	nodes, err := instance.Get(ctx, parent)
	if err != nil {
		log.Errorf("update search index error while get nodes: %+v", err)
		return
	}
	now := mapset.NewSet[string]()
	for i := range objs {
		now.Add(objs[i].GetName())
	}
	old := mapset.NewSet[string]()
	for i := range nodes {
		old.Add(nodes[i].Name)
	}
	// delete data that no longer exists
	toDelete := old.Difference(now)
	toAdd := now.Difference(old)
	for i := range nodes {
		if toDelete.Contains(nodes[i].Name) && !op.HasStorage(path.Join(parent, nodes[i].Name)) {
			log.Debugf("delete index: %s", path.Join(parent, nodes[i].Name))
			err = instance.Del(ctx, path.Join(parent, nodes[i].Name))
			if err != nil {
				log.Errorf("update search index error while del old node: %+v", err)
				return
			}
		}
	}
	// collect files and folders to add in batch
	var toAddObjs []ObjWithParent
	for i := range objs {
		if toAdd.Contains(objs[i].GetName()) {
			log.Debugf("add index: %s", path.Join(parent, objs[i].GetName()))
			toAddObjs = append(toAddObjs, ObjWithParent{
				Parent: parent,
				Obj:    objs[i],
			})
		}
	}
	// batch index all files and folders at once
	if len(toAddObjs) > 0 {
		err = BatchIndex(ctx, toAddObjs)
		if err != nil {
			log.Errorf("update search index error while batch index new nodes: %+v", err)
			return
		}
	}
}

func init() {
	op.RegisterObjsUpdateHook(Update)
}
