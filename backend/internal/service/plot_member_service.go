package service

import (
	"errors"
	"fmt"
	"log/slog"

	"gorm.io/gorm"

	"github.com/communitygarden/server/internal/constants"
	"github.com/communitygarden/server/internal/model"
	"github.com/communitygarden/server/internal/repository"
	"github.com/communitygarden/server/internal/util"
)

// PlotMemberService 地块协作成员服务（成员退出、名额计数、释放清理均在事务内执行）。
type PlotMemberService struct {
	memberRepo repository.PlotMemberRepository
	plotRepo   repository.PlotRepository
	db         *gorm.DB
	logger     *slog.Logger
}

// NewPlotMemberService 构造地块协作成员服务。
func NewPlotMemberService(memberRepo repository.PlotMemberRepository, plotRepo repository.PlotRepository, db *gorm.DB, logger *slog.Logger) *PlotMemberService {
	return &PlotMemberService{memberRepo: memberRepo, plotRepo: plotRepo, db: db, logger: logger}
}

// EnsureOwner 在认养成功的事务内登记认养人为 owner 成员。
func (s *PlotMemberService) EnsureOwner(tx *gorm.DB, plotID, ownerID uint) error {
	if _, err := s.memberRepo.FindByPlotAndUserForUpdate(tx, plotID, ownerID); err == nil {
		return nil
	} else if !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	return s.memberRepo.CreateWithTx(tx, &model.PlotMember{
		PlotID: plotID,
		UserID: ownerID,
		Role:   string(constants.PlotMemberOwner),
	})
}

// ResetOnRelease 在释放地块的事务内删除全部协作成员，返回被清理的成员数。
// 待处理邀请的撤回由 PlotInvitationService.RevokeAllPendingOnRelease 在同一事务内完成。
func (s *PlotMemberService) ResetOnRelease(tx *gorm.DB, plotID uint) (int64, error) {
	members, err := s.memberRepo.CountByPlot(tx, plotID)
	if err != nil {
		return 0, err
	}
	if err := s.memberRepo.DeleteByPlotWithTx(tx, plotID); err != nil {
		return 0, err
	}
	return members, nil
}

// ListByPlot 查询地块全部协作成员。
func (s *PlotMemberService) ListByPlot(plotID uint) ([]model.PlotMember, error) {
	members, err := s.memberRepo.ListByPlot(plotID)
	if err != nil {
		return nil, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	return members, nil
}

// Leave 协作成员主动退出（owner 认养人不能退出；地块释放后不存在成员行）。
// 与邀请/接受共用 runPlotTx 的“先锁地块行”入口，避免锁顺序交叉导致死锁。
func (s *PlotMemberService) Leave(plotID, userID uint) (*model.Plot, error) {
	var plot *model.Plot
	var remaining int64
	err := runPlotTx(s.db, plotID, func(tx *gorm.DB) error {
		var p model.Plot
		if err := tx.First(&p, plotID).Error; err != nil {
			return collabErr500("load plot", err)
		}
		plot = &p
		member, err := s.memberRepo.FindByPlotAndUserForUpdate(tx, plotID, userID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return util.NewAppError(constants.CodeNotFound, 404, "您不是该地块的协作成员，或地块已释放，无法退出")
			}
			return collabErr500("load member", err)
		}
		if member.Role == string(constants.PlotMemberOwner) {
			return util.NewAppError(constants.CodeOwnerCannotLeave, 409,
				fmt.Sprintf("用户 id=%d 是地块 %s 的认养人（角色 %s），不能以协作成员身份退出", userID, plot.Code, util.PlotMemberRoleText(member.Role)))
		}
		if err := s.memberRepo.DeleteByPlotAndUserWithTx(tx, plotID, userID); err != nil {
			return collabErr500("delete member", err)
		}
		remaining, err = s.memberRepo.CountByPlot(tx, plotID)
		if err != nil {
			return collabErr500("count members", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.logger.Info(constants.LogPlotMemberLeft, "plot_id", plotID, "member_id", userID, "remaining", remaining)
	return plot, nil
}
