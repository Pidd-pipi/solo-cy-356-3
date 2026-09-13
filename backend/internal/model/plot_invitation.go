package model

import "time"

// PlotInvitation 地块协作邀请（pending 状态不占用协作名额）。
type PlotInvitation struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	PlotID     uint       `gorm:"not null;index" json:"plot_id"`
	Plot       *Plot      `gorm:"foreignKey:PlotID" json:"plot"`
	InviterID  uint       `gorm:"not null;index" json:"inviter_id"`
	Inviter    *User      `gorm:"foreignKey:InviterID" json:"inviter"`
	InviteeID  uint       `gorm:"not null;index:idx_invitation_invitee_status,priority:1" json:"invitee_id"`
	Invitee    *User      `gorm:"foreignKey:InviteeID" json:"invitee"`
	Status     string     `gorm:"size:32;not null;default:pending;index:idx_invitation_invitee_status,priority:2" json:"status"`
	RespondedAt *time.Time `json:"responded_at"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}
