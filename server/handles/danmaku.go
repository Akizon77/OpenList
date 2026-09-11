package handles

import (
	stdpath "path"
	"strings"
	"unicode/utf8"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/danmaku"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/fs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	internalnet "github.com/OpenListTeam/OpenList/v4/internal/net"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/internal/setting"
	"github.com/OpenListTeam/OpenList/v4/internal/sharing"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

type DanmakuSearchReq struct {
	Path     string                 `json:"path" form:"path"`
	Password string                 `json:"password" form:"password"`
	Query    string                 `json:"query" form:"query"`
	Media    *danmaku.MediaMetadata `json:"media" form:"media"`
}

type DanmakuCommentsReq struct {
	Path      string `json:"path" form:"path"`
	Password  string `json:"password" form:"password"`
	EpisodeID int64  `json:"episode_id" form:"episode_id"`
}

func FsDanmakuSearch(c *gin.Context) {
	if !danmakuEnabled(c) {
		return
	}
	var req DanmakuSearchReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	req.Query = strings.TrimSpace(req.Query)
	if utf8.RuneCountInString(req.Query) > 256 {
		common.ErrorStrResp(c, "danmaku query is too long", 400)
		return
	}
	if ok := authorizeDanmakuPath(c, req.Path, req.Password); !ok {
		return
	}
	if req.Media == nil {
		req.Media = &danmaku.MediaMetadata{}
	}
	if strings.TrimSpace(req.Media.Name) == "" {
		req.Media.Name = stdpath.Base(strings.TrimSpace(req.Path))
		req.Media.ParentName = stdpath.Base(stdpath.Dir(strings.TrimSpace(req.Path)))
	}
	service, err := newDanmakuService()
	if err != nil {
		common.ErrorResp(c, err, 500, true)
		return
	}
	result, err := service.Search(c.Request.Context(), danmaku.SearchInput{
		Query: req.Query,
		Media: req.Media,
	})
	if err != nil {
		common.ErrorResp(c, err, 502, true)
		return
	}
	common.SuccessResp(c, result)
}

func FsDanmakuComments(c *gin.Context) {
	if !danmakuEnabled(c) {
		return
	}
	var req DanmakuCommentsReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	if req.EpisodeID <= 0 {
		common.ErrorStrResp(c, "invalid episode id", 400)
		return
	}
	if ok := authorizeDanmakuPath(c, req.Path, req.Password); !ok {
		return
	}
	service, err := newDanmakuService()
	if err != nil {
		common.ErrorResp(c, err, 500, true)
		return
	}
	result, err := service.Comments(c.Request.Context(), req.EpisodeID)
	if err != nil {
		common.ErrorResp(c, err, 502, true)
		return
	}
	common.SuccessResp(c, result)
}

func danmakuEnabled(c *gin.Context) bool {
	if setting.GetBool(conf.DanmakuEnabled) {
		return true
	}
	common.ErrorStrResp(c, "danmaku is disabled", 403)
	return false
}

func newDanmakuService() (*danmaku.Service, error) {
	apiURL := strings.TrimSpace(setting.GetStr(conf.DanmakuApiUrl))
	if apiURL == "" {
		return nil, errors.New("danmaku api url is empty")
	}
	client, err := danmaku.NewClient(apiURL, internalnet.HttpClient())
	if err != nil {
		return nil, err
	}
	return danmaku.NewService(client), nil
}

func authorizeDanmakuPath(c *gin.Context, reqPath, reqPassword string) bool {
	reqPath = strings.TrimSpace(reqPath)
	if reqPath == "" {
		common.ErrorStrResp(c, "path is required", 400)
		return false
	}
	if strings.HasPrefix(reqPath, "/@s") {
		sid, sharedPath, _ := strings.Cut(strings.TrimPrefix(reqPath, "/@s"), "/")
		if sid == "" {
			common.ErrorStrResp(c, "invalid share id", 400)
			return false
		}
		_, obj, err := sharing.Get(c.Request.Context(), sid, sharedPath, model.SharingListArgs{
			Pwd: reqPassword,
		})
		if dealError(c, err) {
			return false
		}
		if obj == nil || obj.IsDir() {
			common.ErrorStrResp(c, "danmaku requires a file path", 400)
			return false
		}
		return true
	}

	user := c.Request.Context().Value(conf.UserKey).(*model.User)
	if user.IsGuest() && user.Disabled {
		common.ErrorStrResp(c, "Guest user is disabled, login please", 401)
		return false
	}
	resolvedPath, err := user.JoinPath(reqPath)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return false
	}
	meta, err := op.GetNearestMeta(resolvedPath)
	if err != nil && !errors.Is(errors.Cause(err), errs.MetaNotFound) {
		common.ErrorResp(c, err, 500, true)
		return false
	}
	if !common.CanAccess(user, meta, resolvedPath, reqPassword) {
		common.ErrorStrResp(c, "password is incorrect or you have no permission", 403)
		return false
	}
	obj, err := fs.Get(c.Request.Context(), resolvedPath, &fs.GetArgs{NoLog: true})
	if err != nil {
		common.ErrorResp(c, err, 500)
		return false
	}
	if obj.IsDir() {
		common.ErrorStrResp(c, "danmaku requires a file path", 400)
		return false
	}
	return true
}
