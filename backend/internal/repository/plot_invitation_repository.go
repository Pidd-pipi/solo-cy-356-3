package repository

import (
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/communitygarden/server/internal/model"
)

// PlotInvitationRepository 地块协作邀请仓储接口。
type PlotInvitationRepository interface {
	CreateWithTx(tx *gorm.DB, inv *model.PlotInvitation) error
	FindByIDForUpdate(tx *gorm.DB, id uint) (*model.PlotInvitation, error)
	UpdateWithTx(tx *gorm.DB, inv *model.PlotInvitation) error
	FindPendingByPlotAndInvitee(tx *gorm.DB, plotID, inviteeID uint) (*model.PlotInvitation, error)
	ListByPlot(plotID uint, status string) ([]model.PlotInvitation, error)
	ListByInvitee(inviteeID uint, status string) ([]model.PlotInvitation, error)
	RevokeAllPendingByPlotWithTx(tx *gorm.DB, plotID uint) (int64, error)
}

type plotInvitationRepository struct {
	db *gorm.DB
}

// NewPlotInvitationRepository 构造地块协作邀请仓储。
func NewPlotInvitationRepository(db *gorm.DB) PlotInvitationRepository {
	return &plotInvitationRepository{db: db}
}

func (r *plotInvitationRepository) CreateWithTx(tx *gorm.DB, inv *model.PlotInvitation) error {
	return tx.Create(inv).Error
}

// FindByIDForUpdate 按主键查询邀请并加行锁（事务内）。
func (r *plotInvitationRepository) FindByIDForUpdate(tx *gorm.DB, id uint) (*model.PlotInvitation, error) {
	var inv model.PlotInvitation
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&inv, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &inv, nil
}

func (r *plotInvitationRepository) UpdateWithTx(tx *gorm.DB, inv *model.PlotInvitation) error {
	return tx.Save(inv).Error
}

// FindPendingByPlotAndInvitee 查询指定居民在本地块的待处理邀请（重复邀请校验）。
func (r *plotInvitationRepository) FindPendingByPlotAndInvitee(tx *gorm.DB, plotID, inviteeID uint) (*model.PlotInvitation, error) {
	var inv model.PlotInvitation
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("plot_id = ? AND invitee_id = ? AND status = ?", plotID, inviteeID, "pending").
		First(&inv).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &inv, nil
}

func (r *plotInvitationRepository) ListByPlot(plotID uint, status string) ([]model.PlotInvitation, error) {
	var invitations []model.PlotInvitation
	q := r.db.Preload("Inviter").Preload("Invitee").Where("plot_id = ?", plotID)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if err := q.Order("id DESC").Find(&invitations).Error; err != nil {
		return nil, err
	}
	return invitations, nil
}

// ListByInvitee 查询居民收到的邀请（可按状态过滤），并预加载地块。
func (r *plotInvitationRepository) ListByInvitee(inviteeID uint, status string) ([]model.PlotInvitation, error) {
	var invitations []model.PlotInvitation
	q := r.db.Preload("Inviter").Preload("Invitee").Preload("Plot").Where("invitee_id = ?", inviteeID)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if err := q.Order("id DESC").Find(&invitations).Error; err != nil {
		return nil, err
	}
	return invitations, nil
}

// RevokeAllPendingByPlotWithTx 地块释放时将全部待处理邀请置为已撤回（保留历史）。
func (r *plotInvitationRepository) RevokeAllPendingByPlotWithTx(tx *gorm.DB, plotID uint) (int64, error) {
	res := tx.Model(&model.PlotInvitation{}).
		Where("plot_id = ? AND status = ?", plotID, "pending").
		Update("status", "revoked")
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}
