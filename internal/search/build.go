package search

import (
	"context"
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

func BuildIndex(ctx context.Context, indexPaths, ignorePaths []string, maxDepth int, count bool) error {
	log.Infof("build index for: %+v", indexPaths)
	log.Infof("ignore paths: %+v", ignorePaths)
	quit := make(chan struct{}, 1)
	if !Quit.CompareAndSwap(nil, &quit) {
		// other goroutine is running
		return errs.BuildIndexIsRunning
	}
	stopped := atomic.Bool{}
	defer func() {
		Quit.Store(nil)
	}()
	stopRequested := func() bool {
		if stopped.Load() {
			return true
		}
		select {
		case <-quit:
			stopped.Store(true)
			return true
		default:
			return false
		}
	}
	finish := func(objCount uint64, err error) {
		if !count {
			return
		}
		now := time.Now()
		progress := &model.IndexProgress{
			ObjCount:     objCount,
			IsDone:       true,
			LastDoneTime: &now,
		}
		if err != nil {
			progress.Error = err.Error()
		}
		WriteProgress(progress)
	}
	admin, err := op.GetAdmin()
	if err != nil {
		finish(0, err)
		return err
	}
	if count {
		WriteProgress(&model.IndexProgress{
			ObjCount: 0,
			IsDone:   false,
		})
	}
	staged := make([]ObjWithParent, 0)
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
			staged = append(staged, ObjWithParent{
				Obj:    info,
				Parent: path.Dir(indexPath),
			})
			return nil
		}
		fi, err := fs.Get(ctx, indexPath, &fs.GetArgs{})
		if err != nil {
			finish(0, err)
			return err
		}
		// TODO: run walkFS concurrently
		err = fs.WalkFS(context.WithValue(ctx, conf.UserKey, admin), maxDepth, indexPath, fi, walkFn)
		if err != nil {
			finish(0, err)
			return err
		}
		if stopRequested() {
			err = fmt.Errorf("index build stopped")
			finish(0, err)
			return err
		}
	}
	if count {
		if err = Clear(ctx); err != nil {
			finish(0, err)
			return err
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
		if count {
			WriteProgress(&model.IndexProgress{
				ObjCount: uint64(end),
				IsDone:   false,
			})
		}
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
