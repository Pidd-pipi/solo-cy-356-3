package dto

import "github.com/communitygarden/server/internal/model"

// PlotMemberOutDTO 地块协作成员输出。
type PlotMemberOutDTO struct {
	ID        uint        `json:"id"`
	PlotID    uint        `json:"plot_id"`
	UserID    uint        `json:"user_id"`
	User      *UserOutDTO `json:"user"`
	Role      string      `json:"role"`
	InvitedBy *uint       `json:"invited_by"`
	CreatedAt string      `json:"created_at"`
}

// PlotCollaborationSummaryDTO 地块详情中的协作状态摘要。
type PlotCollaborationSummaryDTO struct {
	MemberCount  int                  `json:"member_count"`
	MaxMembers   int                  `json:"max_members"`
	PendingCount int                  `json:"pending_invitation_count"`
	Members      []PlotMemberOutDTO   `json:"members"`
}

// ToPlotMemberOutDTO 模型转 DTO。
func ToPlotMemberOutDTO(m *model.PlotMember) *PlotMemberOutDTO {
	out := &PlotMemberOutDTO{
		ID:        m.ID,
		PlotID:    m.PlotID,
		UserID:    m.UserID,
		Role:      m.Role,
		InvitedBy: m.InvitedBy,
		CreatedAt: m.CreatedAt.Format("2006-01-02 15:04:05"),
	}
	if m.User != nil {
		out.User = ToUserOutDTO(m.User)
	}
	return out
}
