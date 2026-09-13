package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/communitygarden/server/internal/middleware"
	"github.com/communitygarden/server/internal/service"
	"github.com/communitygarden/server/internal/util"
)

// PlotMemberHandler 地块协作成员接口（主动退出）。
type PlotMemberHandler struct {
	memberService *service.PlotMemberService
	audit         middleware.AuditWriter
}

// NewPlotMemberHandler 构造地块协作成员接口。
func NewPlotMemberHandler(memberService *service.PlotMemberService, audit middleware.AuditWriter) *PlotMemberHandler {
	return &PlotMemberHandler{memberService: memberService, audit: audit}
}

// Leave 已接受协作成员主动退出地块。
func (h *PlotMemberHandler) Leave(c *gin.Context) {
	plotID, ok := parsePathID(c, "id")
	if !ok {
		return
	}
	claims, _ := util.GetClaims(c)
	plot, err := h.memberService.Leave(plotID, claims.UserID)
	if err != nil {
		util.FailWithAppError(c, err)
		return
	}
	_ = h.audit.Write(claims.UserID, claims.Username, claims.Role, "LEAVE_PLOT_TEAM", "plot", strconv.FormatUint(uint64(plotID), 10),
		"退出地块 "+plot.Code+" 的协作团队", c.ClientIP(), util.GetRequestID(c))
	util.OK(c, gin.H{"plot_id": plotID, "message": "已退出地块协作"})
}
