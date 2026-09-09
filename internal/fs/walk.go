package fs

import (
	"context"
	"fmt"
	"path"
	"path/filepath"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

// WalkFS traverses filesystem fs starting at name up to depth levels.
//
// WalkFS will stop when current depth > `depth`. For each visited node,
// WalkFS calls walkFn. If a visited file system node is a directory and
// walkFn returns path.SkipDir, walkFS will skip traversal of this node.
func WalkFS(ctx context.Context, depth int, name string, info model.Obj, walkFn func(reqPath string, info model.Obj) error) error {
	// This implementation is based on Walk's code in the standard path/path package.
	walkFnErr := walkFn(name, info)
	if walkFnErr != nil {
		if info.IsDir() && walkFnErr == filepath.SkipDir {
			return nil
		}
		return walkFnErr
	}
	if !info.IsDir() || depth == 0 {
		return nil
	}
	meta, _ := op.GetNearestMeta(name)
	// Read directory names.
	objs, err := List(context.WithValue(ctx, conf.MetaKey, meta), name, &ListArgs{})
	if err != nil {
		return fmt.Errorf("list directory %q: %w", name, err)
	}
	for _, fileInfo := range objs {
		filename := path.Join(name, fileInfo.GetName())
		if err := WalkFS(ctx, depth-1, filename, fileInfo, walkFn); err != nil {
			if err == filepath.SkipDir {
				break
			}
			return err
		}
	}
	return nil
}

// WalkStorageFS traverses one concrete storage instead of resolving each path
// through the balanced-storage selector. This is used by full index builds so
// one unstable backend at a shared mount path cannot change which backend is
// scanned halfway through the walk.
func WalkStorageFS(ctx context.Context, storage driver.Driver, mountPath string, depth int, walkFn func(reqPath string, info model.Obj) error) error {
	mountPath = utils.FixAndCleanPath(mountPath)
	root, err := op.Get(ctx, storage, "/")
	if err != nil {
		return fmt.Errorf("get storage root %q: %w", mountPath, err)
	}
	return walkStorageFS(ctx, storage, mountPath, "/", depth, root, walkFn)
}

func walkStorageFS(ctx context.Context, storage driver.Driver, mountPath, actualPath string, depth int, info model.Obj, walkFn func(reqPath string, info model.Obj) error) error {
	reqPath := mountPath
	if actualPath != "/" {
		reqPath = path.Join(mountPath, actualPath)
	}
	if err := walkFn(reqPath, info); err != nil {
		if info.IsDir() && err == filepath.SkipDir {
			return nil
		}
		return err
	}
	if !info.IsDir() || depth == 0 {
		return nil
	}
	meta, _ := op.GetNearestMeta(reqPath)
	objs, err := op.List(context.WithValue(ctx, conf.MetaKey, meta), storage, actualPath, model.ListArgs{})
	if err != nil {
		return fmt.Errorf("list directory %q in storage %q: %w", reqPath, mountPath, err)
	}
	for _, fileInfo := range objs {
		childPath := path.Join(actualPath, fileInfo.GetName())
		if err := walkStorageFS(ctx, storage, mountPath, childPath, depth-1, fileInfo, walkFn); err != nil {
			if err == filepath.SkipDir {
				break
			}
			return err
		}
	}
	return nil
}
