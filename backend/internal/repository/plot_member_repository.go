package repository

import (
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/communitygarden/server/internal/model"
)

// PlotMemberRepository 地块协作成员仓储接口。
type PlotMemberRepository interface {
	CreateWithTx(tx *gorm.DB, m *model.PlotMember) error
	MemberExistsWithTx(tx *gorm.DB, plotID, userID uint) (bool, error)
	DeleteByPlotAndUserWithTx(tx *gorm.DB, plotID, userID uint) error
	DeleteByPlotWithTx(tx *gorm.DB, plotID uint) error
	FindByPlotAndUserForUpdate(tx *gorm.DB, plotID, userID uint) (*model.PlotMember, error)
	ListByPlot(plotID uint) ([]model.PlotMember, error)
	ListByPlotForUpdate(tx *gorm.DB, plotID uint) ([]model.PlotMember, error)
	CountByPlot(tx *gorm.DB, plotID uint) (int64, error)
}

type plotMemberRepository struct {
	db *gorm.DB
}

// NewPlotMemberRepository 构造地块协作成员仓储。
func NewPlotMemberRepository(db *gorm.DB) PlotMemberRepository {
	return &plotMemberRepository{db: db}
}

func (r *plotMemberRepository) CreateWithTx(tx *gorm.DB, m *model.PlotMember) error {
	return tx.Create(m).Error
}

// MemberExistsWithTx 判断用户是否已是地块成员（无锁只读；唯一索引保证最终正确性）。
func (r *plotMemberRepository) MemberExistsWithTx(tx *gorm.DB, plotID, userID uint) (bool, error) {
	var count int64
	if err := tx.Model(&model.PlotMember{}).Where("plot_id = ? AND user_id = ?", plotID, userID).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *plotMemberRepository) DeleteByPlotAndUserWithTx(tx *gorm.DB, plotID, userID uint) error {
	return tx.Where("plot_id = ? AND user_id = ?", plotID, userID).Delete(&model.PlotMember{}).Error
}

// DeleteByPlotWithTx 释放地块时清空全部协作成员。
func (r *plotMemberRepository) DeleteByPlotWithTx(tx *gorm.DB, plotID uint) error {
	return tx.Where("plot_id = ?", plotID).Delete(&model.PlotMember{}).Error
}

// FindByPlotAndUserForUpdate 查询成员关系并加行锁（事务内）。
func (r *plotMemberRepository) FindByPlotAndUserForUpdate(tx *gorm.DB, plotID, userID uint) (*model.PlotMember, error) {
	var m model.PlotMember
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("plot_id = ? AND user_id = ?", plotID, userID).First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &m, nil
}

func (r *plotMemberRepository) ListByPlot(plotID uint) ([]model.PlotMember, error) {
	var members []model.PlotMember
	if err := r.db.Preload("User").Where("plot_id = ?", plotID).
		Order("role ASC, id ASC").Find(&members).Error; err != nil {
		return nil, err
	}
	return members, nil
}

// ListByPlotForUpdate 锁定本地块全部成员行（事务内）。
func (r *plotMemberRepository) ListByPlotForUpdate(tx *gorm.DB, plotID uint) ([]model.PlotMember, error) {
	var members []model.PlotMember
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("plot_id = ?", plotID).Order("id ASC").Find(&members).Error; err != nil {
		return nil, err
	}
	return members, nil
}

func (r *plotMemberRepository) CountByPlot(tx *gorm.DB, plotID uint) (int64, error) {
	var count int64
	if err := tx.Model(&model.PlotMember{}).Where("plot_id = ?", plotID).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}
