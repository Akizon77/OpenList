# 上游同步说明

## 原则

- 定制功能放在独立文件中，上游文件只保留必要的接入点。
- 不为缩小 diff 而移除分页校验、限流、认证刷新或索引失败保护。
- 不整仓格式化，不混入依赖升级，不用整文件覆盖解决功能冲突。
- 后端与 `../OpenList-Frontend` 是两个独立 Git 仓库，需要分别合并、验证。

## 后端边界

| 功能 | 独立实现 | 同步时检查的接入点 |
| --- | --- | --- |
| Emby 请求、响应解析、重试 | `drivers/emby/client.go` | `util.go` 的分页查询、`playback.go` 的请求调用 |
| Emby 认证刷新与并发保护 | `drivers/emby/auth.go` | `driver.go` 的初始化及 `authMu` |
| Emby 媒体详情、STRM 扩展名和源选择 | `drivers/emby/items.go` | `driver.go` 的 List / Link |
| Emby 播放、附属资源 | `drivers/emby/playback.go`、`sidecar.go` | `driver.go`、`types.go` |
| 弹幕 | `internal/danmaku/`、`server/handles/danmaku.go` | 路由、预览设置、配置常量 |
| 固定存储遍历 | `internal/fs/walk_storage.go` | `internal/search/build.go` |
| 索引进度与限流设置 | `internal/search/build_progress.go`、`request_rate_limit.go` | `build.go`、`util.go`、设置项、进度模型 |
| 请求限流 | `internal/net/request_rate_limit.go` | `drivers/base/client.go`、`internal/net/serve.go`、`internal/op/fs.go` |

`internal/search/build.go` 仍然是有意保留的核心差异：完整扫描成功后才清理旧索引，
全量构建按具体存储遍历，而不是在同一路径的负载均衡存储之间切换。
不能直接恢复上游的先清空、再扫描流程。这里的保护只针对扫描阶段；
清理之后的批量写入并不具备事务回滚能力。

Emby 分页必须继续检查 `TotalRecordCount`，避免把失败或不完整的列表当成空目录。
播放需保留原始流优先、可选转码、会话上报、字幕及 STRM 处理。
定制镜像发布工作流也需保留 fork 的前端来源和镜像目标。

## 同步流程

先处理并提交各自工作区中的改动。每个仓库只需配置一次 `upstream`：

```sh
# 后端仓库
git remote add upstream https://github.com/OpenListTeam/OpenList.git

# 前端仓库中另行执行
git remote add upstream https://github.com/OpenListTeam/OpenList-Frontend.git
```

如果 remote 已存在，先检查 `git remote -v`，不要重复添加。随后在两个仓库分别执行：

```sh
git fetch upstream
git log --left-right --oneline HEAD...upstream/main
git diff --stat upstream/main...HEAD
git merge upstream/main
```

按上表逐处解决冲突，不对整个冲突文件统一选择 ours / theirs。
前端同步边界见前端仓库的 `docs/UPSTREAM_SYNC.md`。

## 验证

```sh
go test ./drivers/emby ./internal/search ./internal/fs ./internal/danmaku ./server/handles
go test ./internal/net
go test -race ./drivers/emby ./internal/search ./internal/fs
```

环境代理测试可能受进程代理环境及初始化顺序影响；如果失败，需与改动前基线对照，
不能直接记为已通过。上线前还需用真实 Emby 验证续播、画质切换、字幕、STRM，
以及存储扫描失败时旧索引仍可用。

## 本次基准

2026-09-12 拉取并比较的上游提交：

- 后端：`f18b4acc76f231dd425acf4e587446b0332f68c4`
- 后端共同祖先：`6247cf7be2a0b84e2135d393694f00d1ef0cdcaf`
- 前端：`8de44dadf199580ba9b09a5cc5430525be803a21`

本次只整理现有定制实现，没有合并这些上游提交，也没有修改运行时配置或发布镜像。
