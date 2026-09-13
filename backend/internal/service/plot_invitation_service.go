package service

import (
	"context"
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

// collabTxMaxRetries 锁竞争/连接资源耗尽（死锁/锁等待/连接池排队超时）时的有限重试次数。
const collabTxMaxRetries = 6

// collabTxBaseDelay 事务重试基础退避。
const collabTxBaseDelay = 4 * time.Millisecond

// collabAttemptBudget 单次尝试的整体预算（连接池排队 + 加锁 + 语句）。
// 0 表示不额外设整体预算：排队由连接池 acquire timeout、持锁由 PostgreSQL lock_timeout 约束；
// 突发压测/特定部署可用 SetCollabAttemptBudget 调小，使排队尽快失败转重试/503。
var collabAttemptBudget time.Duration

// SetCollabAttemptBudget 调整单次尝试预算（主要用于测试与运维调优）。
func SetCollabAttemptBudget(d time.Duration) { collabAttemptBudget = d }

// PlotInvitationService 地块协作邀请服务（邀请/接受/拒绝/撤回 + 协作聚合视图）。
//
// 并发安全约定：
//   - 所有协作写事务必须先对 plots 行加 FOR UPDATE 锁（统一串行入口），
//     再锁 plot_invitations / plot_members 行；杜绝“邀请”与“接受”因锁顺序相反造成的死锁。
//   - 同一地块的写事务在地块行锁上串行化，名额计数与“待处理邀请是否已存在”的检查因此一致。
//   - 单条邀请仅能被处理一次：第二次处理在邀请行锁 + 状态检查下返回 2015。
//   - plot_members(plot_id,user_id) 唯一索引兜底，任何竞态都不会产生重复成员。
//   - 死锁/锁等待错误有限重试，绝不把数据库锁错误作为 500 暴露给用户。
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

// collabErr500 统一包装仓储内部错误。
func collabErr500(action string, err error) error {
	return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).
		Wrap(fmt.Errorf("%s: %w", action, err))
}

// runPlotTx 以“先锁地块行”为统一入口运行协作写事务，
// 并对死锁/锁等待/SQLite 写锁冲突做有限指数退避重试。
// 业务错误（AppError）原样返回，不重试；锁错误重试耗尽后统一转为 5000。
func runPlotTx(db *gorm.DB, plotID uint, fn func(tx *gorm.DB) error) error {
	var lastErr error
	for attempt := 0; attempt < collabTxMaxRetries; attempt++ {
		if attempt > 0 {
			// 指数退避 + 抖动：错峰后重试，避免同批被排队的请求再次同步拥塞
			backoff := time.Duration(attempt*attempt) * collabTxBaseDelay
			jitter := jitterMillis(collabTxBaseDelay)
			time.Sleep(backoff + jitter)
		}
		// 每次尝试可带整体预算（连接池排队过久被取消，交外层重试/503）；默认 0 表示不限制。
		var attemptCtx context.Context
		var cancel context.CancelFunc
		if collabAttemptBudget > 0 {
			attemptCtx, cancel = context.WithTimeout(context.Background(), collabAttemptBudget)
		} else {
			attemptCtx, cancel = context.WithCancel(context.Background())
		}
		// cancel 必须延迟到 gorm 完成 COMMIT 之后，不能在事务回调内提前取消。
		lastErr = func() error {
			defer cancel()
			return db.WithContext(attemptCtx).Transaction(func(tx *gorm.DB) error {
				// 统一第一把锁：地块行。同一地块的协作写在此处串行；连接池有界，请求先在池内排队。
				var locked model.Plot
				if err := tx.Clauses(lockForUpdate()).First(&locked, plotID).Error; err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						return util.NewAppError(constants.CodeNotFound, 404, fmt.Sprintf("地块实体 id=%d 不存在", plotID))
					}
					if util.IsRetryableResourceError(err) {
						return err // 交给外层按资源繁忙重试
					}
					return collabErr500("lock plot", err)
				}
				return fn(tx)
			})
		}()
		if lastErr == nil {
			return nil
		}
		if shouldRetryCollabTx(lastErr) {
			continue // 锁竞争/连接池耗尽：整事务已回滚，退避后重试
		}
		return lastErr // 业务错误或其它错误：直接返回
	}
	// 重试耗尽：资源/锁类瞬态错误返回明确的“繁忙排队”503，不暴露成 500
	if util.IsRetryableResourceError(lastErr) {
		return util.NewAppError(constants.CodeServiceBusy, 503, constants.ErrorText[constants.CodeServiceBusy]).
			Wrap(fmt.Errorf("协作事务繁忙重试耗尽: %w", lastErr))
	}
	return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).
		Wrap(fmt.Errorf("协作事务重试耗尽: %w", lastErr))
}

// shouldRetryCollabTx 判断协作写事务是否应重试：
// 裸数据库锁/资源错误，或内部错误的根因为锁/资源错误时重试；明确业务冲突不重试。
func shouldRetryCollabTx(err error) bool {
	var ae *util.AppError
	if errors.As(err, &ae) {
		return ae.Code == constants.CodeInternalError && util.IsRetryableResourceError(err)
	}
	return util.IsRetryableResourceError(err)
}

// lockActivePlot 校验地块仍在协作生命周期内（未释放回共享池）。
// 调用前必须已持有地块行锁（runPlotTx）。
func lockActivePlot(tx *gorm.DB, plotID uint) (*model.Plot, error) {
	var plot model.Plot
	if err := tx.First(&plot, plotID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, util.NewAppError(constants.CodeNotFound, 404, fmt.Sprintf("地块实体 id=%d 不存在", plotID))
		}
		return nil, collabErr500("load plot", err)
	}
	if plot.Status == string(constants.PlotStatusAvailable) || plot.AdopterID == nil {
		return nil, util.NewAppError(constants.CodePlotNotAdopted, 409,
			fmt.Sprintf("地块 %s 当前状态为 %s（已释放回共享池），不能再邀请或接受协作", plot.Code, util.PlotStatusText(plot.Status)))
	}
	return &plot, nil
}

// Invite 认养人邀请已注册居民共同照料地块。
func (s *PlotInvitationService) Invite(plotID, inviterID uint, username string) (*model.PlotInvitation, error) {
	// 用户查找在事务外完成（只读，与锁顺序无关）
	invitee, err := s.userRepo.FindByUsername(username)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, util.NewAppError(constants.CodeNotFound, 404,
				fmt.Sprintf("被邀请人用户名字段 username=%s 不是已注册居民", username))
		}
		return nil, collabErr500("find invitee", err)
	}
	if invitee.ID == inviterID {
		return nil, util.NewAppError(constants.CodeCannotInviteSelf, 400, "不能邀请认养人自己协作地块")
	}

	var created *model.PlotInvitation
	err = runPlotTx(s.db, plotID, func(tx *gorm.DB) error {
		plot, err := lockActivePlot(tx, plotID)
		if err != nil {
			return err
		}
		if plot.AdopterID == nil || *plot.AdopterID != inviterID {
			return util.NewAppError(constants.CodeNotPlotOwner, 403,
				fmt.Sprintf("用户 id=%d 不是地块 %s 的认养人，无权邀请协作", inviterID, plot.Code))
		}
		// 事务内复核居民状态（避免在锁外期间被禁用）
		var u model.User
		if err := tx.First(&u, invitee.ID).Error; err != nil {
			return collabErr500("load invitee", err)
		}
		if u.Status != string(constants.UserStatusActive) {
			return util.NewAppError(constants.CodeUserDisabled, 409,
				fmt.Sprintf("居民 %s（角色 %s）账号已被禁用，无法邀请协作", u.Username, util.RoleText(u.Role)))
		}
		// 已是成员 -> 明确业务冲突（唯一索引语义的预检查，地块行锁保护）
		exists, err := s.memberRepo.MemberExistsWithTx(tx, plotID, invitee.ID)
		if err != nil {
			return collabErr500("check member", err)
		}
		if exists {
			return util.NewAppError(constants.CodeAlreadyPlotMember, 409,
				fmt.Sprintf("居民 %s 已是地块 %s 的协作成员，不能重复邀请", invitee.Username, plot.Code))
		}
		// 存在待处理邀请 -> 明确业务冲突（与“同时接受”并发时，地块行锁保证读到对方已提交状态）
		var pendingCount int64
		if err := tx.Model(&model.PlotInvitation{}).
			Where("plot_id = ? AND invitee_id = ? AND status = ?", plotID, invitee.ID, string(constants.InvitationPending)).
			Count(&pendingCount).Error; err != nil {
			return collabErr500("check pending invitation", err)
		}
		if pendingCount > 0 {
			return util.NewAppError(constants.CodeDuplicateInvitation, 409,
				fmt.Sprintf("已向居民 %s 发送过待处理邀请，请勿重复邀请", invitee.Username))
		}
		// 待处理邀请不占名额：满员也允许发出邀请（等待队列），名额仅在接受瞬间校验。
		inv := &model.PlotInvitation{
			PlotID:    plotID,
			InviterID: inviterID,
			InviteeID: invitee.ID,
			Status:    string(constants.InvitationPending),
		}
		if err := s.invRepo.CreateWithTx(tx, inv); err != nil {
			return collabErr500("create invitation", err)
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
	// 事务外无锁读取 plot_id，用于以“地块行锁”为统一入口；邀请存在性在事务内加锁复核。
	head, err := s.invRepo.FindByID(invitationID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, util.NewAppError(constants.CodeNotFound, 404, fmt.Sprintf("协作邀请实体 id=%d 不存在", invitationID))
		}
		return nil, collabErr500("load invitation", err)
	}
	if head.InviteeID != userID {
		return nil, util.NewAppError(constants.CodeNotInvitee, 403,
			fmt.Sprintf("用户 id=%d 不是邀请 id=%d 的被邀请居民，无权接受或拒绝", userID, invitationID))
	}

	var updated *model.PlotInvitation
	var memberCount int64
	err = runPlotTx(s.db, head.PlotID, func(tx *gorm.DB) error {
		// 持有地块行锁后再锁邀请行：锁顺序与邀请/撤回/释放完全一致，无死锁。
		inv, err := s.invRepo.FindByIDForUpdate(tx, invitationID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return util.NewAppError(constants.CodeNotFound, 404, fmt.Sprintf("协作邀请实体 id=%d 不存在", invitationID))
			}
			return collabErr500("lock invitation", err)
		}
		// 地块释放后一律不能再接受或拒绝（先于状态判断，返回明确的 2010）
		if _, err := lockActivePlot(tx, inv.PlotID); err != nil {
			return err
		}
		// 同一邀请只能处理一次
		if inv.Status != string(constants.InvitationPending) {
			return util.NewAppError(constants.CodeInvitationNotPending, 409,
				fmt.Sprintf("邀请 id=%d 当前状态为 %s，仅待处理邀请可接受或拒绝", invitationID, util.InvitationStatusText(inv.Status)))
		}
		now := time.Now()
		inv.Status = string(decision)
		inv.RespondedAt = &now
		if decision == constants.InvitationAccepted {
			// 地块行锁串行化下，该计数为提交后的最新值，多个邀请同时接受也不会超员
			memberCount, err = s.memberRepo.CountByPlot(tx, inv.PlotID)
			if err != nil {
				return collabErr500("count members", err)
			}
			if memberCount >= constants.MaxPlotMembers {
				// 名额已满：邀请保持 pending（仍不占名额），等待成员退出释放名额后再接受
				inv.Status = string(constants.InvitationPending)
				inv.RespondedAt = nil
				return util.NewAppError(constants.CodePlotMemberFull, 409,
					fmt.Sprintf("协作名额已满：%d/%d，无法接受邀请（待处理邀请不占名额）", memberCount, constants.MaxPlotMembers))
			}
			newMember := &model.PlotMember{
				PlotID:    inv.PlotID,
				UserID:    userID,
				Role:      string(constants.PlotMemberHelper),
				InvitedBy: &inv.InviterID,
			}
			if err := s.memberRepo.CreateWithTx(tx, newMember); err != nil {
				if util.IsUniqueViolation(err) {
					// 唯一索引兜底：并发下已是成员（例如同用户对同地块多条邀请同时接受）
					return util.NewAppError(constants.CodeAlreadyPlotMember, 409, "您已是该地块的协作成员")
				}
				return collabErr500("add member", err)
			}
			memberCount++
		}
		if err := s.invRepo.UpdateWithTx(tx, inv); err != nil {
			return collabErr500("update invitation", err)
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
	head, err := s.invRepo.FindByID(invitationID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, util.NewAppError(constants.CodeNotFound, 404, fmt.Sprintf("协作邀请实体 id=%d 不存在", invitationID))
		}
		return nil, collabErr500("load invitation", err)
	}
	var updated *model.PlotInvitation
	err = runPlotTx(s.db, head.PlotID, func(tx *gorm.DB) error {
		inv, err := s.invRepo.FindByIDForUpdate(tx, invitationID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return util.NewAppError(constants.CodeNotFound, 404, fmt.Sprintf("协作邀请实体 id=%d 不存在", invitationID))
			}
			return collabErr500("lock invitation", err)
		}
		plot, err := lockActivePlot(tx, inv.PlotID)
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
			return collabErr500("update invitation", err)
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
		return nil, collabErr500("load plot", err)
	}
	members, err := s.memberRepo.ListByPlot(plotID)
	if err != nil {
		return nil, collabErr500("list members", err)
	}
	invitations, err := s.invRepo.ListByPlot(plotID, "")
	if err != nil {
		return nil, collabErr500("list invitations", err)
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
		return nil, collabErr500("list my invitations", err)
	}
	return all, nil
}
