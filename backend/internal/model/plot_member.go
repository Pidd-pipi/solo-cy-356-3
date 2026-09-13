package model

import "time"

// PlotMember 地块协作成员（认养人 + 最多 3 名受邀居民，总数上限 constants.MaxPlotMembers）。
type PlotMember struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	PlotID    uint      `gorm:"not null;uniqueIndex:uniq_plot_member,priority:1;index" json:"plot_id"`
	UserID    uint      `gorm:"not null;uniqueIndex:uniq_plot_member,priority:2;index" json:"user_id"`
	User      *User     `gorm:"foreignKey:UserID" json:"user"`
	Role      string    `gorm:"size:32;not null;default:helper;index" json:"role"`
	InvitedBy *uint     `gorm:"index" json:"invited_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
