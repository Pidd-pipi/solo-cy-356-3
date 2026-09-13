package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/communitygarden/server/internal/constants"
	"github.com/communitygarden/server/internal/dto"
	"github.com/communitygarden/server/internal/middleware"
	"github.com/communitygarden/server/internal/model"
	"github.com/communitygarden/server/internal/service"
	"github.com/communitygarden/server/internal/util"
)

// PlotInvitationHandler 地块协作邀请接口（邀请/接受/拒绝/撤回/协作视图/我的邀请）。
type PlotInvitationHandler struct {
	invService *service.PlotInvitationService
	audit      middleware.AuditWriter
}

// NewPlotInvitationHandler 构造地块协作邀请接口。
func NewPlotInvitationHandler(invService *service.PlotInvitationService, audit middleware.AuditWriter) *PlotInvitationHandler {
	return &PlotInvitationHandler{invService: invService, audit: audit}
}

// parsePathID 解析路径参数 id，失败时已写响应并返回 ok=false。
func parsePathID(c *gin.Context, name string) (uint, bool) {
	v, err := strconv.ParseUint(c.Param(name), 10, 64)
	if err != nil {
		util.Fail(c, http.StatusBadRequest, constants.CodeBadRequest, "路径参数 "+name+" 必须为正整数")
		return 0, false
	}
	return uint(v), true
}

// GetCollaboration 协作页：地块 + 成员名单 + 全部邀请。
func (h *PlotInvitationHandler) GetCollaboration(c *gin.Context) {
	plotID, ok := parsePathID(c, "id")
	if !ok {
		return
	}
	data, err := h.invService.GetCollaboration(plotID)
	if err != nil {
		util.FailWithAppError(c, err)
		return
	}
	util.OK(c, data)
}

// Invite 认养人邀请已注册居民。
func (h *PlotInvitationHandler) Invite(c *gin.Context) {
	plotID, ok := parsePathID(c, "id")
	if !ok {
		return
	}
	var req dto.CreateInvitationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.Fail(c, http.StatusBadRequest, constants.CodeValidationFailed, constants.ErrorText[constants.CodeValidationFailed]+": "+err.Error())
		return
	}
	claims, _ := util.GetClaims(c)
	inv, err := h.invService.Invite(plotID, claims.UserID, req.Username)
	if err != nil {
		util.FailWithAppError(c, err)
		return
	}
	_ = h.audit.Write(claims.UserID, claims.Username, claims.Role, "INVITE_PLOT_MEMBER", "plot", strconv.FormatUint(uint64(plotID), 10),
		"邀请居民 "+req.Username+" 协作地块", c.ClientIP(), util.GetRequestID(c))
	util.OK(c, dto.ToPlotInvitationOutDTO(inv))
}

// Accept 被邀请人接受邀请。
func (h *PlotInvitationHandler) Accept(c *gin.Context) {
	h.respond(c, true)
}

// Reject 被邀请人拒绝邀请。
func (h *PlotInvitationHandler) Reject(c *gin.Context) {
	h.respond(c, false)
}

func (h *PlotInvitationHandler) respond(c *gin.Context, accept bool) {
	invitationID, ok := parsePathID(c, "id")
	if !ok {
		return
	}
	claims, _ := util.GetClaims(c)
	var inv *model.PlotInvitation
	var err error
	if accept {
		inv, err = h.invService.Accept(invitationID, claims.UserID)
	} else {
		inv, err = h.invService.Reject(invitationID, claims.UserID)
	}
	if err != nil {
		util.FailWithAppError(c, err)
		return
	}
	action := "REJECT_PLOT_INVITATION"
	detail := "拒绝地块协作邀请"
	if accept {
		action = "ACCEPT_PLOT_INVITATION"
		detail = "接受地块协作邀请"
	}
	_ = h.audit.Write(claims.UserID, claims.Username, claims.Role, action, "plot_invitation", strconv.FormatUint(uint64(invitationID), 10),
		detail, c.ClientIP(), util.GetRequestID(c))
	util.OK(c, dto.ToPlotInvitationOutDTO(inv))
}

// Revoke 邀请人（认养人）撤回待处理邀请。
func (h *PlotInvitationHandler) Revoke(c *gin.Context) {
	invitationID, ok := parsePathID(c, "id")
	if !ok {
		return
	}
	claims, _ := util.GetClaims(c)
	inv, err := h.invService.Revoke(invitationID, claims.UserID)
	if err != nil {
		util.FailWithAppError(c, err)
		return
	}
	_ = h.audit.Write(claims.UserID, claims.Username, claims.Role, "REVOKE_PLOT_INVITATION", "plot_invitation", strconv.FormatUint(uint64(invitationID), 10),
		"撤回地块协作邀请", c.ClientIP(), util.GetRequestID(c))
	util.OK(c, dto.ToPlotInvitationOutDTO(inv))
}

// ListMine 当前登录用户收到的邀请（?status=pending 默认全部）。
func (h *PlotInvitationHandler) ListMine(c *gin.Context) {
	claims, _ := util.GetClaims(c)
	invitations, err := h.invService.ListMine(claims.UserID, c.Query("status"))
	if err != nil {
		util.FailWithAppError(c, err)
		return
	}
	list := make([]*dto.PlotInvitationOutDTO, 0, len(invitations))
	for i := range invitations {
		list = append(list, dto.ToPlotInvitationOutDTO(&invitations[i]))
	}
	util.OK(c, list)
}
