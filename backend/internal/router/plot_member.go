package router

import (
	"github.com/gin-gonic/gin"

	"github.com/communitygarden/server/internal/middleware"
)

// registerPlotMembers 地块协作成员路由（已接受成员主动退出）。
func (r *Router) registerPlotMembers(g *gin.RouterGroup) {
	auth := g.Group("")
	auth.Use(middleware.Auth(r.cfg, r.logger))
	{
		auth.POST("/plots/:id/members/leave", r.plotMemberHandler.Leave)
	}
}
