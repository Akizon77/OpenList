package handles

import (
	"net/http"
	"path"

	"github.com/OpenListTeam/OpenList/v4/drivers/emby"
	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/fs"
	"github.com/OpenListTeam/OpenList/v4/internal/net"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

func EmbyProxy(c *gin.Context) {
	rawPath := c.Request.Context().Value(conf.PathKey).(string)
	storage, err := fs.GetStorage(rawPath, &fs.GetStoragesArgs{})
	if err != nil {
		common.ErrorPage(c, err, http.StatusNotFound)
		return
	}
	d, ok := storage.(*emby.Emby)
	if !ok || !d.WebProxy {
		common.ErrorStrResp(c, "Emby server proxy is not enabled", http.StatusForbidden)
		return
	}
	if d.Status != op.WORK {
		common.ErrorStrResp(c, d.Status, http.StatusServiceUnavailable)
		return
	}
	response, err := d.OpenProxyResource(c.Request, rawPath, c.Query("resource"))
	if err != nil {
		status := http.StatusBadGateway
		if upstreamStatus, ok := errs.UnwrapOrSelf(err).(net.HttpStatusCodeError); ok {
			status = int(upstreamStatus)
		}
		common.ErrorPage(c, err, status)
		return
	}
	defer response.Body.Close()
	if err := common.ProxyResponse(c.Writer, c.Request, response, path.Base(rawPath)); err != nil {
		log.Warnf("emby proxy stream interrupted: %v", err)
	}
}
