package router

import (
	"github.com/gin-gonic/gin"

	"github.com/communitygarden/server/internal/middleware"
)

// registerPlotInvitations 地块协作邀请路由：
// 协作视图、邀请、接受、拒绝、撤回、我的邀请。
func (r *Router) registerPlotInvitations(g *gin.RouterGroup) {
	auth := g.Group("")
	auth.Use(middleware.Auth(r.cfg, r.logger))
	{
		// 认养人视角：协作页与邀请
		auth.GET("/plots/:id/collaboration", r.plotInvitationHandler.GetCollaboration)
		auth.POST("/plots/:id/invitations", r.plotInvitationHandler.Invite)

		// 被邀请人视角：收到的邀请列表
		auth.GET("/my/plot-invitations", r.plotInvitationHandler.ListMine)
	}

	inv := g.Group("/plot-invitations")
	inv.Use(middleware.Auth(r.cfg, r.logger))
	{
		inv.POST("/:id/accept", r.plotInvitationHandler.Accept)
		inv.POST("/:id/reject", r.plotInvitationHandler.Reject)
		inv.POST("/:id/revoke", r.plotInvitationHandler.Revoke)
	}
}
