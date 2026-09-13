package service

import (
	"errors"
	"fmt"
	"log/slog"

	"gorm.io/gorm"

	"github.com/communitygarden/server/internal/constants"
	"github.com/communitygarden/server/internal/dto"
	"github.com/communitygarden/server/internal/model"
	"github.com/communitygarden/server/internal/repository"
	"github.com/communitygarden/server/internal/util"
)

// PlotService 地块服务（认养使用事务 + SELECT FOR UPDATE）。
type PlotService struct {
	plotRepo      repository.PlotRepository
	memberSvc     *PlotMemberService
	invitationSvc *PlotInvitationService
	db            *gorm.DB
	logger        *slog.Logger
}

// NewPlotService 构造地块服务。
func NewPlotService(plotRepo repository.PlotRepository, memberSvc *PlotMemberService, invitationSvc *PlotInvitationService, db *gorm.DB, logger *slog.Logger) *PlotService {
	return &PlotService{plotRepo: plotRepo, memberSvc: memberSvc, invitationSvc: invitationSvc, db: db, logger: logger}
}

// GetByID 查询地块详情（被地块 handler 与种植计划 service 复用）。
func (s *PlotService) GetByID(id uint) (*model.Plot, error) {
	p, err := s.plotRepo.FindByID(id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, util.NewAppError(constants.CodeNotFound, 404, fmt.Sprintf("地块实体 id=%d 不存在", id))
		}
		return nil, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	return p, nil
}

// GetDetailDTO 查询地块详情并附加协作状态（成员名单/名额/待处理邀请数），供地块详情接口使用。
func (s *PlotService) GetDetailDTO(id uint) (*dto.PlotOutDTO, error) {
	p, err := s.GetByID(id)
	if err != nil {
		return nil, err
	}
	out := dto.ToPlotOutDTO(p)
	if err := s.invitationSvc.AttachSummary(out); err != nil {
		return nil, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	return out, nil
}

// Create 创建地块（管理员）。
func (s *PlotService) Create(req *dto.CreatePlotRequest, operator string) (*model.Plot, error) {
	if _, err := s.plotRepo.FindByCode(req.Code); err == nil {
		return nil, util.NewAppError(constants.CodeConflict, 409, fmt.Sprintf("地块编号 %s 已存在", req.Code))
	}
	p := &model.Plot{
		Name:        req.Name,
		Code:        req.Code,
		Area:        req.Area,
		SoilType:    req.SoilType,
		Sunlight:    req.Sunlight,
		Latitude:    req.Latitude,
		Longitude:   req.Longitude,
		Status:      string(constants.PlotStatusAvailable),
		Description: req.Description,
	}
	if err := s.plotRepo.Create(p); err != nil {
		return nil, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	s.logger.Info(constants.LogPlotCreated, "plot_id", p.ID, "code", p.Code, "operator", operator)
	return p, nil
}

// Update 更新地块（管理员）。
func (s *PlotService) Update(id uint, req *dto.UpdatePlotRequest, operator string) (*model.Plot, error) {
	p, err := s.plotRepo.FindByID(id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, util.NewAppError(constants.CodeNotFound, 404, fmt.Sprintf("地块实体 id=%d 不存在", id))
		}
		return nil, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	if req.Name != nil {
		p.Name = *req.Name
	}
	if req.Code != nil {
		p.Code = *req.Code
	}
	if req.Area != nil {
		p.Area = *req.Area
	}
	if req.SoilType != nil {
		p.SoilType = *req.SoilType
	}
	if req.Sunlight != nil {
		p.Sunlight = *req.Sunlight
	}
	if req.Latitude != nil {
		p.Latitude = *req.Latitude
	}
	if req.Longitude != nil {
		p.Longitude = *req.Longitude
	}
	if req.Description != nil {
		p.Description = *req.Description
	}
	if err := s.plotRepo.Update(p); err != nil {
		return nil, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	return p, nil
}

// List 分页查询地块（可过滤状态）。
func (s *PlotService) List(pq util.PageQuery, status string) ([]model.Plot, int64, error) {
	plots, total, err := s.plotRepo.List(pq, status)
	if err != nil {
		return nil, 0, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	return plots, total, nil
}

// ListDetailDTOs 分页查询地块并为每行附加协作状态摘要（列表页协作状态列复用邀请服务）。
func (s *PlotService) ListDetailDTOs(pq util.PageQuery, status string) ([]*dto.PlotOutDTO, int64, error) {
	plots, total, err := s.List(pq, status)
	if err != nil {
		return nil, 0, err
	}
	list := make([]*dto.PlotOutDTO, 0, len(plots))
	for i := range plots {
		out := dto.ToPlotOutDTO(&plots[i])
		if err := s.invitationSvc.AttachSummary(out); err != nil {
			return nil, 0, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
		}
		list = append(list, out)
	}
	return list, total, nil
}

// Adopt 认养地块（统一地块行锁入口 + 资源重试，available -> adopted）。
func (s *PlotService) Adopt(plotID, userID uint, role, username string) (*model.Plot, error) {
	var adopted *model.Plot
	err := runPlotTx(s.db, plotID, func(tx *gorm.DB) error {
		var plot model.Plot
		if err := tx.First(&plot, plotID).Error; err != nil {
			return collabErr500("load plot", err)
		}
		if plot.Status != string(constants.PlotStatusAvailable) {
			return util.NewAppError(constants.CodePlotNotAvailable, 409, fmt.Sprintf("地块 %s 当前状态为 %s，不可认养", plot.Code, util.PlotStatusText(plot.Status)))
		}
		plot.Status = string(constants.PlotStatusAdopted)
		plot.AdopterID = &userID
		if err := s.plotRepo.UpdateWithTx(tx, &plot); err != nil {
			return collabErr500("update plot", err)
		}
		// 认养人自动成为地块协作 owner 成员（占用 1/4 名额）
		if err := s.memberSvc.EnsureOwner(tx, plot.ID, userID); err != nil {
			return collabErr500("ensure owner", err)
		}
		adopted = &plot
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.logger.Info(constants.LogPlotAdopted, "plot_id", adopted.ID, "code", adopted.Code, "user_id", userID, "role", role)
	return adopted, nil
}

// Release 释放地块（管理员或认养人，harvested -> available）。
// 同一事务内清空全部协作成员并撤回全部待处理邀请：释放后不能再邀请或接受。
// 使用统一地块行锁入口，突发并发下排队或返回明确 503，不产生 500。
func (s *PlotService) Release(plotID, operatorID uint, operatorRole string) (*model.Plot, error) {
	var released *model.Plot
	var resetMembers, resetInvitations int64
	err := runPlotTx(s.db, plotID, func(tx *gorm.DB) error {
		var plot model.Plot
		if err := tx.First(&plot, plotID).Error; err != nil {
			return collabErr500("load plot", err)
		}
		if operatorRole != string(constants.RoleAdmin) && (plot.AdopterID == nil || *plot.AdopterID != operatorID) {
			return util.NewAppError(constants.CodeForbidden, 403, fmt.Sprintf("角色 %s 无权释放地块 %s", util.RoleText(operatorRole), plot.Code))
		}
		if plot.Status != string(constants.PlotStatusHarvested) {
			return util.NewAppError(constants.CodePlotNotAvailable, 409, fmt.Sprintf("地块 %s 当前状态为 %s，仅待释放状态可释放", plot.Code, util.PlotStatusText(plot.Status)))
		}
		var err error
		resetMembers, err = s.memberSvc.ResetOnRelease(tx, plotID)
		if err != nil {
			return collabErr500("reset members", err)
		}
		resetInvitations, err = s.invitationSvc.RevokeAllPendingOnRelease(tx, plotID)
		if err != nil {
			return collabErr500("revoke pending", err)
		}
		plot.Status = string(constants.PlotStatusAvailable)
		plot.AdopterID = nil
		if err := s.plotRepo.UpdateWithTx(tx, &plot); err != nil {
			return collabErr500("update plot", err)
		}
		released = &plot
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.logger.Info(constants.LogPlotReleased, "plot_id", released.ID, "code", released.Code, "operator", operatorID)
	if resetMembers > 0 || resetInvitations > 0 {
		s.logger.Info(constants.LogPlotCollabReset, "plot_id", released.ID, "members", resetMembers, "invitations", resetInvitations)
	}
	return released, nil
}

// MarkHarvested 种植计划完成后将地块置为待释放（harvested）。调用方已在持有地块行锁的事务内。
func (s *PlotService) MarkHarvested(tx *gorm.DB, plotID uint) error {
	if err := tx.Model(&model.Plot{}).Where("id = ?", plotID).
		Update("status", string(constants.PlotStatusHarvested)).Error; err != nil {
		return err
	}
	return nil
}

// CountByStatus 地块状态统计（仪表盘复用）。
func (s *PlotService) CountByStatus() (map[string]int64, error) {
	return s.plotRepo.CountByStatus()
}
