package service

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"github.com/communitygarden/server/internal/constants"
	"github.com/communitygarden/server/internal/dto"
	"github.com/communitygarden/server/internal/model"
	"github.com/communitygarden/server/internal/repository"
	"github.com/communitygarden/server/internal/util"
)

// PlotInvitationService 地块协作邀请服务（邀请/接受/拒绝/撤回 + 协作聚合视图）。
// 所有写操作在事务内对 plots / plot_members / plot_invitations 加行锁。
type PlotInvitationService struct {
	invRepo    repository.PlotInvitationRepository
	memberRepo repository.PlotMemberRepository
	plotRepo   repository.PlotRepository
	userRepo   repository.UserRepository
	memberSvc  *PlotMemberService
	db         *gorm.DB
	logger     *slog.Logger
}

// NewPlotInvitationService 构造地块协作邀请服务。
func NewPlotInvitationService(
	invRepo repository.PlotInvitationRepository,
	memberRepo repository.PlotMemberRepository,
	plotRepo repository.PlotRepository,
	userRepo repository.UserRepository,
	memberSvc *PlotMemberService,
	db *gorm.DB,
	logger *slog.Logger,
) *PlotInvitationService {
	return &PlotInvitationService{
		invRepo: invRepo, memberRepo: memberRepo, plotRepo: plotRepo, userRepo: userRepo,
		memberSvc: memberSvc, db: db, logger: logger,
	}
}

// requireActivePlot 在事务内锁定地块并校验地块仍在协作生命周期内（已认养/待释放，未释放回共享池）。
func (s *PlotInvitationService) requireActivePlot(tx *gorm.DB, plotID uint) (*model.Plot, error) {
	plot, err := s.plotRepo.FindByIDForUpdate(tx, plotID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, util.NewAppError(constants.CodeNotFound, 404, fmt.Sprintf("地块实体 id=%d 不存在", plotID))
		}
		return nil, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	if plot.Status == string(constants.PlotStatusAvailable) || plot.AdopterID == nil {
		return nil, util.NewAppError(constants.CodePlotNotAdopted, 409,
			fmt.Sprintf("地块 %s 当前状态为 %s（已释放回共享池），不能再邀请或接受协作", plot.Code, util.PlotStatusText(plot.Status)))
	}
	return plot, nil
}

// Invite 认养人邀请已注册居民共同照料地块。
func (s *PlotInvitationService) Invite(plotID, inviterID uint, username string) (*model.PlotInvitation, error) {
	var created *model.PlotInvitation
	err := s.db.Transaction(func(tx *gorm.DB) error {
		plot, err := s.requireActivePlot(tx, plotID)
		if err != nil {
			return err
		}
		if plot.AdopterID == nil || *plot.AdopterID != inviterID {
			return util.NewAppError(constants.CodeNotPlotOwner, 403,
				fmt.Sprintf("用户 id=%d 不是地块 %s 的认养人，无权邀请协作", inviterID, plot.Code))
		}
		invitee, err := s.userRepo.FindByUsername(username)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return util.NewAppError(constants.CodeNotFound, 404,
					fmt.Sprintf("被邀请人用户名字段 username=%s 不是已注册居民", username))
			}
			return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
		}
		if invitee.Status != string(constants.UserStatusActive) {
			return util.NewAppError(constants.CodeUserDisabled, 409,
				fmt.Sprintf("居民 %s（角色 %s）账号已被禁用，无法邀请协作", invitee.Username, util.RoleText(invitee.Role)))
		}
		if invitee.ID == inviterID {
			return util.NewAppError(constants.CodeCannotInviteSelf, 400, "不能邀请认养人自己协作地块")
		}
		// 已是成员拒绝
		if _, err := s.memberRepo.FindByPlotAndUserForUpdate(tx, plotID, invitee.ID); err == nil {
			return util.NewAppError(constants.CodeAlreadyPlotMember, 409,
				fmt.Sprintf("居民 %s 已是地块 %s 的协作成员，不能重复邀请", invitee.Username, plot.Code))
		} else if !errors.Is(err, repository.ErrNotFound) {
			return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
		}
		// 存在待处理邀请拒绝（重复邀请同一人）
		if inv, err := s.invRepo.FindPendingByPlotAndInvitee(tx, plotID, invitee.ID); err == nil {
			return util.NewAppError(constants.CodeDuplicateInvitation, 409,
				fmt.Sprintf("已向居民 %s 发送过待处理邀请（邀请 id=%d），请勿重复邀请", invitee.Username, inv.ID))
		} else if !errors.Is(err, repository.ErrNotFound) {
			return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
		}
		// 名额边界：待处理邀请不占名额，因此满员时仍允许发出邀请（等待队列），
		// 仅在被邀请人接受瞬间按已接受成员计数校验
		inv := &model.PlotInvitation{
			PlotID:    plotID,
			InviterID: inviterID,
			InviteeID: invitee.ID,
			Status:    string(constants.InvitationPending),
		}
		if err := s.invRepo.CreateWithTx(tx, inv); err != nil {
			return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
		}
		created = inv
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.logger.Info(constants.LogPlotInvitationSent, "invitation_id", created.ID, "plot_id", plotID, "inviter_id", inviterID, "invitee_id", created.InviteeID)
	return created, nil
}

// Accept 被邀请人接受邀请：pending -> accepted 并登记为 helper 成员。
func (s *PlotInvitationService) Accept(invitationID, userID uint) (*model.PlotInvitation, error) {
	return s.respond(invitationID, userID, constants.InvitationAccepted)
}

// Reject 被邀请人拒绝邀请：pending -> rejected（不占名额）。
func (s *PlotInvitationService) Reject(invitationID, userID uint) (*model.PlotInvitation, error) {
	return s.respond(invitationID, userID, constants.InvitationRejected)
}

func (s *PlotInvitationService) respond(invitationID, userID uint, decision constants.InvitationStatus) (*model.PlotInvitation, error) {
	var updated *model.PlotInvitation
	var memberCount int64
	err := s.db.Transaction(func(tx *gorm.DB) error {
		inv, err := s.invRepo.FindByIDForUpdate(tx, invitationID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return util.NewAppError(constants.CodeNotFound, 404, fmt.Sprintf("协作邀请实体 id=%d 不存在", invitationID))
			}
			return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
		}
		if inv.InviteeID != userID {
			return util.NewAppError(constants.CodeNotInvitee, 403,
				fmt.Sprintf("用户 id=%d 不是邀请 id=%d 的被邀请居民，无权接受或拒绝", userID, invitationID))
		}
		// 先校验地块仍在协作生命周期：地块释放后一律不能再接受或拒绝
		plot, err := s.requireActivePlot(tx, inv.PlotID)
		if err != nil {
			return err
		}
		if inv.Status != string(constants.InvitationPending) {
			return util.NewAppError(constants.CodeInvitationNotPending, 409,
				fmt.Sprintf("邀请 id=%d 当前状态为 %s，仅待处理邀请可接受或拒绝", invitationID, util.InvitationStatusText(inv.Status)))
		}
		now := time.Now()
		inv.Status = string(decision)
		inv.RespondedAt = &now
		if decision == constants.InvitationAccepted {
			// 接受瞬间再次校验成员身份与名额（并发接受多个邀请的边界）
			if _, err := s.memberRepo.FindByPlotAndUserForUpdate(tx, inv.PlotID, userID); err == nil {
				return util.NewAppError(constants.CodeAlreadyPlotMember, 409, "您已是该地块的协作成员")
			} else if !errors.Is(err, repository.ErrNotFound) {
				return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
			}
			memberCount, err = s.memberRepo.CountByPlot(tx, inv.PlotID)
			if err != nil {
				return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
			}
			if memberCount >= constants.MaxPlotMembers {
				return util.NewAppError(constants.CodePlotMemberFull, 409,
					fmt.Sprintf("地块 %s 协作名额已满：%d/%d，无法接受邀请", plot.Code, memberCount, constants.MaxPlotMembers))
			}
			if err := s.memberRepo.CreateWithTx(tx, &model.PlotMember{
				PlotID:    inv.PlotID,
				UserID:    userID,
				Role:      string(constants.PlotMemberHelper),
				InvitedBy: &inv.InviterID,
			}); err != nil {
				return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
			}
			memberCount++
		}
		if err := s.invRepo.UpdateWithTx(tx, inv); err != nil {
			return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
		}
		updated = inv
		return nil
	})
	if err != nil {
		return nil, err
	}
	if decision == constants.InvitationAccepted {
		s.logger.Info(constants.LogPlotInvitationAccepted, "invitation_id", invitationID, "plot_id", updated.PlotID, "invitee_id", userID, "member_count", memberCount)
	} else {
		s.logger.Info(constants.LogPlotInvitationRejected, "invitation_id", invitationID, "plot_id", updated.PlotID, "invitee_id", userID)
	}
	return updated, nil
}

// Revoke 邀请人撤回待处理邀请：pending -> revoked。
func (s *PlotInvitationService) Revoke(invitationID, operatorID uint) (*model.PlotInvitation, error) {
	var updated *model.PlotInvitation
	err := s.db.Transaction(func(tx *gorm.DB) error {
		inv, err := s.invRepo.FindByIDForUpdate(tx, invitationID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return util.NewAppError(constants.CodeNotFound, 404, fmt.Sprintf("协作邀请实体 id=%d 不存在", invitationID))
			}
			return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
		}
		plot, err := s.requireActivePlot(tx, inv.PlotID)
		if err != nil {
			return err
		}
		if plot.AdopterID == nil || (*plot.AdopterID != operatorID && inv.InviterID != operatorID) {
			return util.NewAppError(constants.CodeNotPlotOwner, 403,
				fmt.Sprintf("用户 id=%d 不是地块 %s 的认养人，无权撤回邀请 id=%d", operatorID, plot.Code, invitationID))
		}
		if inv.Status != string(constants.InvitationPending) {
			return util.NewAppError(constants.CodeInvitationNotPending, 409,
				fmt.Sprintf("邀请 id=%d 当前状态为 %s，无法撤回", invitationID, util.InvitationStatusText(inv.Status)))
		}
		now := time.Now()
		inv.Status = string(constants.InvitationRevoked)
		inv.RespondedAt = &now
		if err := s.invRepo.UpdateWithTx(tx, inv); err != nil {
			return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
		}
		updated = inv
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.logger.Info(constants.LogPlotInvitationRevoked, "invitation_id", invitationID, "plot_id", updated.PlotID, "operator_id", operatorID)
	return updated, nil
}

// RevokeAllPendingOnRelease 释放地块的同一事务内撤回全部待处理邀请，返回撤回数量。
func (s *PlotInvitationService) RevokeAllPendingOnRelease(tx *gorm.DB, plotID uint) (int64, error) {
	return s.invRepo.RevokeAllPendingByPlotWithTx(tx, plotID)
}

// AttachSummary 为地块详情 DTO 附加协作状态摘要（成员名单 + 名额 + 待处理邀请数）。
func (s *PlotInvitationService) AttachSummary(p *dto.PlotOutDTO) error {
	if p == nil {
		return nil
	}
	members, err := s.memberRepo.ListByPlot(p.ID)
	if err != nil {
		return err
	}
	pending, err := s.invRepo.ListByPlot(p.ID, string(constants.InvitationPending))
	if err != nil {
		return err
	}
	summary := &dto.PlotCollaborationSummaryDTO{
		MemberCount:  len(members),
		MaxMembers:   constants.MaxPlotMembers,
		PendingCount: len(pending),
		Members:      make([]dto.PlotMemberOutDTO, 0, len(members)),
	}
	for i := range members {
		summary.Members = append(summary.Members, *dto.ToPlotMemberOutDTO(&members[i]))
	}
	p.Collaboration = summary
	return nil
}

// GetCollaboration 协作页聚合视图：地块 + 成员 + 全部邀请。
func (s *PlotInvitationService) GetCollaboration(plotID uint) (*dto.PlotCollaborationDTO, error) {
	plot, err := s.plotRepo.FindByID(plotID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, util.NewAppError(constants.CodeNotFound, 404, fmt.Sprintf("地块实体 id=%d 不存在", plotID))
		}
		return nil, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	members, err := s.memberRepo.ListByPlot(plotID)
	if err != nil {
		return nil, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	invitations, err := s.invRepo.ListByPlot(plotID, "")
	if err != nil {
		return nil, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	out := &dto.PlotCollaborationDTO{
		Plot:        dto.ToPlotOutDTO(plot),
		MemberCount: len(members),
		MaxMembers:  constants.MaxPlotMembers,
		Members:     make([]dto.PlotMemberOutDTO, 0, len(members)),
		Invitations: make([]dto.PlotInvitationOutDTO, 0, len(invitations)),
	}
	for i := range members {
		out.Members = append(out.Members, *dto.ToPlotMemberOutDTO(&members[i]))
	}
	for i := range invitations {
		out.Invitations = append(out.Invitations, *dto.ToPlotInvitationOutDTO(&invitations[i]))
	}
	return out, nil
}

// ListMine 查询当前用户收到的邀请（status 为空时返回全部，pending 时仅待处理）。
func (s *PlotInvitationService) ListMine(userID uint, status string) ([]model.PlotInvitation, error) {
	all, err := s.invRepo.ListByInvitee(userID, status)
	if err != nil {
		return nil, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	return all, nil
}
