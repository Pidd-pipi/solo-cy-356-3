package service

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"github.com/communitygarden/server/internal/constants"
	"github.com/communitygarden/server/internal/model"
	"github.com/communitygarden/server/internal/repository"
	"github.com/communitygarden/server/internal/util"
)

// 本文件通过故障注入仓储，验证协作写事务的两条可靠性保证：
//  1. 多表写入中途失败 -> 整个事务回滚，不留下“邀请已改但成员没写/成员已删但地块没释放”之类的脏数据；
//  2. 锁竞争（SQLite database is locked / PostgreSQL 40P01 deadlock）-> 整事务回滚后重试，
//     重试期间的中间写入不落地；超过上限才返回规范 5000。
//
// 断言全部针对数据库最终状态，而非仅检查 error 返回值。

var (
	// errInjectedWrite 普通写失败（非锁错误，不应触发重试）。
	errInjectedWrite = errors.New("injected multi-table write failure")
	// lockedErr 模拟 SQLite 写锁竞争（util.IsRetryableLockError 识别）。
	lockedErr = errors.New("database is locked")
	// pgDeadlockErr 模拟 PostgreSQL 死锁中止（SQLSTATE 40P01）。
	pgDeadlockErr = &pgconn.PgError{Code: "40P01", Message: "deadlock detected"}
)

// faultInvRepo 在邀请仓储上注入故障。
type faultInvRepo struct {
	repository.PlotInvitationRepository
	updateRemain     atomic.Int32 // 剩余强制普通写失败次数
	updateLockRemain atomic.Int32 // 剩余锁错误次数
	updateCalls      atomic.Int32
}

func (r *faultInvRepo) UpdateWithTx(tx *gorm.DB, inv *model.PlotInvitation) error {
	r.updateCalls.Add(1)
	if r.updateLockRemain.Add(-1) >= 0 {
		return lockedErr
	}
	if r.updateRemain.Add(-1) >= 0 {
		return errInjectedWrite
	}
	return r.PlotInvitationRepository.UpdateWithTx(tx, inv)
}

// pgFaultInvRepo 注入 PostgreSQL 40P01 死锁错误。
type pgFaultInvRepo struct {
	repository.PlotInvitationRepository
	remain int32
	calls  int32
}

func (r *pgFaultInvRepo) UpdateWithTx(tx *gorm.DB, inv *model.PlotInvitation) error {
	r.calls++
	if r.remain > 0 {
		r.remain--
		return pgDeadlockErr
	}
	return r.PlotInvitationRepository.UpdateWithTx(tx, inv)
}

// faultMemberRepo 在成员仓储上注入故障。
type faultMemberRepo struct {
	repository.PlotMemberRepository
	createRemain atomic.Int32
	deleteRemain atomic.Int32
	createCalls  atomic.Int32
}

func (r *faultMemberRepo) CreateWithTx(tx *gorm.DB, m *model.PlotMember) error {
	r.createCalls.Add(1)
	if r.createRemain.Add(-1) >= 0 {
		return errInjectedWrite
	}
	return r.PlotMemberRepository.CreateWithTx(tx, m)
}

func (r *faultMemberRepo) DeleteByPlotWithTx(tx *gorm.DB, plotID uint) error {
	if r.deleteRemain.Add(-1) >= 0 {
		return errInjectedWrite
	}
	return r.PlotMemberRepository.DeleteByPlotWithTx(tx, plotID)
}

// newFaultableServices 用可替换仓储构造全套协作服务。
func newFaultableServices(t *testing.T, db *gorm.DB, memberRepo repository.PlotMemberRepository, invRepo repository.PlotInvitationRepository) (*PlotService, *PlotMemberService, *PlotInvitationService) {
	t.Helper()
	plotRepo := repository.NewPlotRepository(db)
	userRepo := repository.NewUserRepository(db)
	memberSvc := NewPlotMemberService(memberRepo, plotRepo, db, testLogger())
	invSvc := NewPlotInvitationService(invRepo, memberRepo, plotRepo, userRepo, memberSvc, db, testLogger())
	plotSvc := NewPlotService(plotRepo, memberSvc, invSvc, db, testLogger())
	return plotSvc, memberSvc, invSvc
}

// adoptAndInvite 用正常服务完成认养并向 username 发出一条邀请。
func adoptAndInvite(t *testing.T, db *gorm.DB, plot *model.Plot, ownerID uint, username string) (*PlotService, *PlotInvitationService, *model.PlotInvitation) {
	t.Helper()
	plotSvc, _, invSvc := newPlotCollabServices(t, db)
	if _, err := plotSvc.Adopt(plot.ID, ownerID, "citizen", username); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	inv, err := invSvc.Invite(plot.ID, ownerID, username)
	if err != nil {
		t.Fatalf("invite %s: %v", username, err)
	}
	return plotSvc, invSvc, inv
}

// TestRollback_AcceptMemberWriteFails 接受邀请时成员表写入失败：邀请状态与成员表都必须回滚。
func TestRollback_AcceptMemberWriteFails(t *testing.T) {
	db := newTestServiceDB(t)
	owner := newTestUser(t, db, "xowner", "citizen")
	u := newTestUser(t, db, "xinvitee", "citizen")
	plot := newTestPlot(t, db, "P-FAULT", "available", nil)
	_, goodInvSvc, inv := adoptAndInvite(t, db, plot, owner.ID, "xinvitee")

	// 成员表第一次插入必失败（普通写错误，不重试）
	faultMember := &faultMemberRepo{PlotMemberRepository: repository.NewPlotMemberRepository(db)}
	faultMember.createRemain.Store(1)
	_, _, badInvSvc := newFaultableServices(t, db, faultMember, repository.NewPlotInvitationRepository(db))

	_, err := badInvSvc.Accept(inv.ID, u.ID)
	assertErrCode(t, err, constants.CodeInternalError)
	if c := faultMember.createCalls.Load(); c != 1 {
		t.Fatalf("member create calls=%d want 1 (no retry on plain write error)", c)
	}

	// 多表回滚：邀请仍 pending、没有 helper 成员、成员总数仍为 1（owner）
	assertInvStatus(t, db, inv.ID, string(constants.InvitationPending))
	assertNoMember(t, db, plot.ID, u.ID)
	assertMemberCount(t, db, plot.ID, 1)

	// 故障消除后同一邀请可正常接受，数据最终一致
	if _, err := goodInvSvc.Accept(inv.ID, u.ID); err != nil {
		t.Fatalf("accept after fault cleared: %v", err)
	}
	assertInvStatus(t, db, inv.ID, string(constants.InvitationAccepted))
	assertMemberCount(t, db, plot.ID, 2)
	assertNoDuplicateMembers(t, db, plot.ID)
}

// TestRollback_RevokeWriteFails 撤回写失败：邀请保持 pending，只尝试一次，随后可撤回成功。
func TestRollback_RevokeWriteFails(t *testing.T) {
	db := newTestServiceDB(t)
	owner := newTestUser(t, db, "rov", "citizen")
	newTestUser(t, db, "ru", "citizen")
	plot := newTestPlot(t, db, "P-ROV", "available", nil)
	plotSvc, invSvc, inv := adoptAndInvite(t, db, plot, owner.ID, "ru")
	_ = plotSvc

	faultInv := &faultInvRepo{PlotInvitationRepository: repository.NewPlotInvitationRepository(db)}
	faultInv.updateRemain.Store(1)
	_, _, badSvc := newFaultableServices(t, db, repository.NewPlotMemberRepository(db), faultInv)

	_, err := badSvc.Revoke(inv.ID, owner.ID)
	assertErrCode(t, err, constants.CodeInternalError)
	assertInvStatus(t, db, inv.ID, string(constants.InvitationPending))
	if faultInv.updateCalls.Load() != 1 {
		t.Fatalf("update calls=%d want 1 (no retry on plain write error)", faultInv.updateCalls.Load())
	}

	if _, err := invSvc.Revoke(inv.ID, owner.ID); err != nil {
		t.Fatalf("revote retry: %v", err)
	}
	assertInvStatus(t, db, inv.ID, string(constants.InvitationRevoked))
}

// TestRollback_ReleaseCleansAtomic 释放时成员清理失败：地块、成员、邀请全部保持原状。
func TestRollback_ReleaseCleansAtomic(t *testing.T) {
	db := newTestServiceDB(t)
	owner := newTestUser(t, db, "rlo", "citizen")
	u := newTestUser(t, db, "rlu", "citizen")
	waitUser := newTestUser(t, db, "rlp", "citizen")
	plot := newTestPlot(t, db, "P-RL", "available", nil)
	plotSvc, invSvc, inv := adoptAndInvite(t, db, plot, owner.ID, "rlu")
	if _, err := invSvc.Accept(inv.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	pending, err := invSvc.Invite(plot.ID, owner.ID, "rlp")
	if err != nil {
		t.Fatal(err)
	}
	_ = waitUser
	if err := db.Model(&model.Plot{}).Where("id=?", plot.ID).Update("status", string(constants.PlotStatusHarvested)).Error; err != nil {
		t.Fatal(err)
	}

	// 成员删除第一次失败
	faultMember := &faultMemberRepo{PlotMemberRepository: repository.NewPlotMemberRepository(db)}
	faultMember.deleteRemain.Store(1)
	badPlotSvc, _, _ := newFaultableServices(t, db, faultMember, repository.NewPlotInvitationRepository(db))

	_, err = badPlotSvc.Release(plot.ID, owner.ID, "citizen")
	assertErrCode(t, err, constants.CodeInternalError)

	// 回滚：地块仍 harvested 且认养人仍在；成员仍 2；待处理邀请仍 pending
	var p model.Plot
	db.First(&p, plot.ID)
	if p.Status != string(constants.PlotStatusHarvested) || p.AdopterID == nil || *p.AdopterID != owner.ID {
		t.Fatalf("plot should remain harvested+adopted, got status=%s adopter=%v", p.Status, p.AdopterID)
	}
	assertMemberCount(t, db, plot.ID, 2)
	assertInvStatus(t, db, pending.ID, string(constants.InvitationPending))

	// 故障消除后释放成功并完成全部清理
	if _, err := plotSvc.Release(plot.ID, owner.ID, "citizen"); err != nil {
		t.Fatalf("release retry: %v", err)
	}
	db.First(&p, plot.ID)
	if p.Status != string(constants.PlotStatusAvailable) || p.AdopterID != nil {
		t.Fatalf("plot after release status=%s adopter=%v", p.Status, p.AdopterID)
	}
	assertMemberCount(t, db, plot.ID, 0)
	assertPendingCount(t, db, plot.ID, 0)
}

// TestRetry_LockContentionThenSuccess 锁竞争前两次失败、第三次成功：
// 前两次的成员写入必须随回滚撤销，最终恰好 1 个 helper 成员、邀请 accepted。
func TestRetry_LockContentionThenSuccess(t *testing.T) {
	db := newTestServiceDB(t)
	owner := newTestUser(t, db, "lk1", "citizen")
	u := newTestUser(t, db, "lk2", "citizen")
	plot := newTestPlot(t, db, "P-LK", "available", nil)
	_, _, inv := adoptAndInvite(t, db, plot, owner.ID, "lk2")

	// 邀请更新（成员插入之后的步骤）前两次返回锁错误 -> 整事务回滚并重试
	faultInv := &faultInvRepo{PlotInvitationRepository: repository.NewPlotInvitationRepository(db)}
	faultInv.updateLockRemain.Store(2)
	faultMember := &faultMemberRepo{PlotMemberRepository: repository.NewPlotMemberRepository(db)}
	_, _, retrySvc := newFaultableServices(t, db, faultMember, faultInv)

	got, err := retrySvc.Accept(inv.ID, u.ID)
	if err != nil {
		t.Fatalf("accept should succeed after lock retries: %v", err)
	}
	if got.Status != string(constants.InvitationAccepted) {
		t.Fatalf("invitation status=%s want accepted", got.Status)
	}
	if c := faultInv.updateCalls.Load(); c != 3 {
		t.Fatalf("invitation update calls=%d want 3", c)
	}
	if c := faultMember.createCalls.Load(); c != 3 {
		t.Fatalf("member create calls=%d want 3 (2 rolled back + 1 commit)", c)
	}
	// 前两次回滚没有留下重复成员
	assertMemberCount(t, db, plot.ID, 2)
	assertNoDuplicateMembers(t, db, plot.ID)
	assertInvStatus(t, db, inv.ID, string(constants.InvitationAccepted))
}

// TestRetry_LockAlwaysFails 锁竞争持续失败：重试上限后返回明确的“繁忙排队”1007(503)，
// 所有尝试回滚，数据保持原状（不暴露成 500）。
func TestRetry_LockAlwaysFails(t *testing.T) {
	db := newTestServiceDB(t)
	owner := newTestUser(t, db, "lx1", "citizen")
	u := newTestUser(t, db, "lx2", "citizen")
	plot := newTestPlot(t, db, "P-LX", "available", nil)
	_, _, inv := adoptAndInvite(t, db, plot, owner.ID, "lx2")

	faultInv := &faultInvRepo{PlotInvitationRepository: repository.NewPlotInvitationRepository(db)}
	faultInv.updateLockRemain.Store(1 << 20) // 始终锁错误
	faultMember := &faultMemberRepo{PlotMemberRepository: repository.NewPlotMemberRepository(db)}
	_, _, retrySvc := newFaultableServices(t, db, faultMember, faultInv)

	_, err := retrySvc.Accept(inv.ID, u.ID)
	assertErrCode(t, err, constants.CodeServiceBusy)
	if httpStatusOf(err) != 503 {
		t.Fatalf("lock exhaustion http status=%d want 503", httpStatusOf(err))
	}

	if c := faultInv.updateCalls.Load(); c != int32(collabTxMaxRetries) {
		t.Fatalf("update calls=%d want %d", c, collabTxMaxRetries)
	}
	// 所有尝试全部回滚
	assertInvStatus(t, db, inv.ID, string(constants.InvitationPending))
	assertMemberCount(t, db, plot.ID, 1)
	assertNoMember(t, db, plot.ID, u.ID)
	assertNoDuplicateMembers(t, db, plot.ID)
}

// TestRetry_PoolExhaustedReturnsBusy 连接池耗尽/too many clients：重试耗尽返回 1007/503，而非 500。
func TestRetry_PoolExhaustedReturnsBusy(t *testing.T) {
	db := newTestServiceDB(t)
	owner := newTestUser(t, db, "pe1", "citizen")
	u := newTestUser(t, db, "pe2", "citizen")
	plot := newTestPlot(t, db, "P-PE", "available", nil)
	_, _, inv := adoptAndInvite(t, db, plot, owner.ID, "pe2")

	// 模拟 PostgreSQL too_many_connections（53300）
	exhausted := &pgconn.PgError{Code: "53300", Message: "too many clients already"}
	if !util.IsPoolOrConnExhausted(exhausted) {
		t.Fatalf("53300 must be classified pool exhausted")
	}
	repo := &poolExhaustedInvRepo{PlotInvitationRepository: repository.NewPlotInvitationRepository(db)}
	repo.remain = collabTxMaxRetries
	repo.fail = exhausted
	faultMember := &faultMemberRepo{PlotMemberRepository: repository.NewPlotMemberRepository(db)}
	_, _, retrySvc := newFaultableServices(t, db, faultMember, repo)

	_, err := retrySvc.Accept(inv.ID, u.ID)
	assertErrCode(t, err, constants.CodeServiceBusy)
	if httpStatusOf(err) != 503 {
		t.Fatalf("pool exhaustion http status=%d want 503", httpStatusOf(err))
	}
	assertInvStatus(t, db, inv.ID, string(constants.InvitationPending))
	assertMemberCount(t, db, plot.ID, 1)
}

type poolExhaustedInvRepo struct {
	repository.PlotInvitationRepository
	remain int
	fail   error
	calls  int
}

func (r *poolExhaustedInvRepo) UpdateWithTx(tx *gorm.DB, inv *model.PlotInvitation) error {
	r.calls++
	if r.remain > 0 {
		r.remain--
		return r.fail
	}
	return r.PlotInvitationRepository.UpdateWithTx(tx, inv)
}

func httpStatusOf(err error) int {
	if ae, ok := err.(*util.AppError); ok {
		return ae.HTTPStatus
	}
	return 0
}

// TestRetry_PGDeadlockRetried PostgreSQL 40P01 死锁错误同样触发回滚重试，第二次成功。
func TestRetry_PGDeadlockRetried(t *testing.T) {
	if !util.IsRetryableLockError(pgDeadlockErr) {
		t.Fatalf("pg deadlock error must be classified retryable")
	}
	db := newTestServiceDB(t)
	owner := newTestUser(t, db, "pg1", "citizen")
	u := newTestUser(t, db, "pg2", "citizen")
	plot := newTestPlot(t, db, "P-PG", "available", nil)
	_, _, inv := adoptAndInvite(t, db, plot, owner.ID, "pg2")

	pgRepo := &pgFaultInvRepo{PlotInvitationRepository: repository.NewPlotInvitationRepository(db), remain: 1}
	faultMember := &faultMemberRepo{PlotMemberRepository: repository.NewPlotMemberRepository(db)}
	_, _, retrySvc := newFaultableServices(t, db, faultMember, pgRepo)

	if _, err := retrySvc.Accept(inv.ID, u.ID); err != nil {
		t.Fatalf("accept after pg deadlock retry: %v", err)
	}
	if pgRepo.calls != 2 {
		t.Fatalf("update calls=%d want 2", pgRepo.calls)
	}
	assertInvStatus(t, db, inv.ID, string(constants.InvitationAccepted))
	assertMemberCount(t, db, plot.ID, 2)
	assertNoDuplicateMembers(t, db, plot.ID)
}

func assertNoDuplicateMembers(t *testing.T, db *gorm.DB, plotID uint) {
	t.Helper()
	var dup int64
	db.Raw(`SELECT count(*) FROM (SELECT user_id FROM plot_members WHERE plot_id = ? GROUP BY user_id HAVING count(*) > 1)`, plotID).Scan(&dup)
	if dup != 0 {
		t.Fatalf("plot %d has %d duplicated member users", plotID, dup)
	}
}
