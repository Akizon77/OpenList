package middlewares

import (
	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/sign"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/gin-gonic/gin"
)

func EmbyProxySign(c *gin.Context) {
	path := c.Request.Context().Value(conf.PathKey).(string)
	if err := sign.VerifyEmby(path, c.Query("resource"), c.Query("sign")); err != nil {
		common.ErrorPage(c, err, 401)
		c.Abort()
		return
	}
	c.Next()
}
