package dto

import "github.com/communitygarden/server/internal/model"

// CreateInvitationRequest 认养人邀请居民协作地块。
type CreateInvitationRequest struct {
	Username string `json:"username" binding:"required,max=64"`
}

// PlotInvitationOutDTO 地块协作邀请输出。
type PlotInvitationOutDTO struct {
	ID          uint        `json:"id"`
	PlotID      uint        `json:"plot_id"`
	Plot        *PlotOutDTO `json:"plot,omitempty"`
	InviterID   uint        `json:"inviter_id"`
	Inviter     *UserOutDTO `json:"inviter"`
	InviteeID   uint        `json:"invitee_id"`
	Invitee     *UserOutDTO `json:"invitee"`
	Status      string      `json:"status"`
	RespondedAt string      `json:"responded_at"`
	CreatedAt   string      `json:"created_at"`
}

// PlotCollaborationDTO 协作页聚合视图：成员 + 邀请。
type PlotCollaborationDTO struct {
	Plot        *PlotOutDTO             `json:"plot"`
	MemberCount int                     `json:"member_count"`
	MaxMembers  int                     `json:"max_members"`
	Members     []PlotMemberOutDTO      `json:"members"`
	Invitations []PlotInvitationOutDTO  `json:"invitations"`
}

// ToPlotInvitationOutDTO 模型转 DTO。
func ToPlotInvitationOutDTO(inv *model.PlotInvitation) *PlotInvitationOutDTO {
	out := &PlotInvitationOutDTO{
		ID:        inv.ID,
		PlotID:    inv.PlotID,
		InviterID: inv.InviterID,
		InviteeID: inv.InviteeID,
		Status:    inv.Status,
		CreatedAt: inv.CreatedAt.Format("2006-01-02 15:04:05"),
	}
	if inv.RespondedAt != nil {
		out.RespondedAt = inv.RespondedAt.Format("2006-01-02 15:04:05")
	}
	if inv.Plot != nil {
		out.Plot = ToPlotOutDTO(inv.Plot)
	}
	if inv.Inviter != nil {
		out.Inviter = ToUserOutDTO(inv.Inviter)
	}
	if inv.Invitee != nil {
		out.Invitee = ToUserOutDTO(inv.Invitee)
	}
	return out
}
