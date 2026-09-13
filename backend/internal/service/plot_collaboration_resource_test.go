package service

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"github.com/communitygarden/server/internal/constants"
	"github.com/communitygarden/server/internal/model"
)

// 本组测试通过注入“第一把地块锁”的瞬态错误，验证：
//   - 连接排队超时 / PG 锁等待超时(55P03) / 连接数耗尽(53300) 会被有限重试吸收；
//   - 持续耗尽时返回明确的 1007/503（而不是 500）；
//   - 重试发生在业务校验之前，任何失败尝试都不改动成员/邀请（无残留）。
// 断言只使用错误码/HTTP 状态/数据库最终状态，不匹配错误文本。

// withLockFaults 临时替换第一把锁：前 failTimes 次返回 fail，之后恢复真实加锁。
func withLockFaults(t *testing.T, fail error, failTimes int) *atomic.Int32 {
	t.Helper()
	orig := lockPlotForUpdate
	var calls atomic.Int32
	lockPlotForUpdate = func(tx *gorm.DB, plotID uint) error {
		n := calls.Add(1)
		if int(n) <= failTimes {
			return fail
		}
		return orig(tx, plotID)
	}
	t.Cleanup(func() { lockPlotForUpdate = orig })
	return &calls
}

func pgCodeErr(code string) error { return &pgconn.PgError{Code: code} }

func acceptFixture(t *testing.T) (*gorm.DB, *PlotInvitationService, *model.User, *model.User, *model.Plot, *model.PlotInvitation) {
	t.Helper()
	db := newTestServiceDB(t)
	_, _, invSvc := newPlotCollabServices(t, db)
	owner := newTestUser(t, db, "ro", "citizen")
	u := newTestUser(t, db, "ru", "citizen")
	plot := newTestPlot(t, db, "P-RES", "available", nil)
	_, _, inv := adoptAndInvite(t, db, plot, owner.ID, "ru")
	return db, invSvc, owner, u, plot, inv
}

// 锁等待超时（55P03）前两次、第三次成功：业务回调只在拿到锁后执行一次，邀请 accepted、成员恰好 1 行。
func TestResource_LockTimeoutRetriedThenSuccess(t *testing.T) {
	db, invSvc, _, u, plot, inv := acceptFixture(t)
	calls := withLockFaults(t, pgCodeErr("55P03"), 2) // lock_not_available

	got, err := invSvc.Accept(inv.ID, u.ID)
	if err != nil {
		t.Fatalf("accept should succeed after lock-timeout retries: %v", err)
	}
	if got.Status != string(constants.InvitationAccepted) {
		t.Fatalf("status=%s want accepted", got.Status)
	}
	// 3 次尝试（2 次锁超时回滚 + 1 次提交）
	if c := calls.Load(); c != 3 {
		t.Fatalf("lock attempts=%d want 3", c)
	}
	assertInvStatus(t, db, inv.ID, string(constants.InvitationAccepted))
	assertMemberCount(t, db, plot.ID, 2)
	assertNoDuplicateMembers(t, db, plot.ID)
}

// 连接池排队超时（context.DeadlineExceeded）同样被重试吸收。
func TestResource_AcquireQueueTimeoutRetried(t *testing.T) {
	db, invSvc, _, u, plot, inv := acceptFixture(t)
	calls := withLockFaults(t, context.DeadlineExceeded, 3)

	if _, err := invSvc.Accept(inv.ID, u.ID); err != nil {
		t.Fatalf("acquire-queue timeout should be retried: %v", err)
	}
	if c := calls.Load(); c != 4 {
		t.Fatalf("lock attempts=%d want 4", c)
	}
	assertInvStatus(t, db, inv.ID, string(constants.InvitationAccepted))
	assertMemberCount(t, db, plot.ID, 2)
}

// too_many_connections(53300) 前两次后恢复：不应返回 500，最终成功且无残留。
func TestResource_TooManyClientsRetried(t *testing.T) {
	db, invSvc, _, u, plot, inv := acceptFixture(t)
	calls := withLockFaults(t, pgCodeErr("53300"), 2)

	if _, err := invSvc.Accept(inv.ID, u.ID); err != nil {
		t.Fatalf("53300 should be retried, got %v", err)
	}
	if c := calls.Load(); c != 3 {
		t.Fatalf("attempts=%d want 3", c)
	}
	assertMemberCount(t, db, plot.ID, 2)
	assertNoDuplicateMembers(t, db, plot.ID)
}

// 资源持续耗尽：重试上限后返回明确的 1007/HTTP 503；邀请保持 pending、无成员写入。
func TestResource_ExhaustionReturnsBusyNotInternal(t *testing.T) {
	db, invSvc, _, u, plot, inv := acceptFixture(t)
	calls := withLockFaults(t, pgCodeErr("53300"), 1<<20)

	_, err := invSvc.Accept(inv.ID, u.ID)
	assertErrCode(t, err, constants.CodeServiceBusy)
	if httpStatusOf(err) != 503 {
		t.Fatalf("http=%d want 503", httpStatusOf(err))
	}
	if c := calls.Load(); c != int32(collabTxMaxRetries) {
		t.Fatalf("attempts=%d want %d", c, collabTxMaxRetries)
	}
	// 所有尝试都在进入业务回调前失败：无任何残留
	assertInvStatus(t, db, inv.ID, string(constants.InvitationPending))
	assertMemberCount(t, db, plot.ID, 1)
	assertNoMember(t, db, plot.ID, u.ID)
	assertNoDuplicateMembers(t, db, plot.ID)
}

// 同一次操作中混合 55P03 与排队超时，最终仍稳定重试到成功。
func TestResource_MixedTransientErrors(t *testing.T) {
	db, invSvc, _, u, plot, inv := acceptFixture(t)
	orig := lockPlotForUpdate
	var calls atomic.Int32
	lockPlotForUpdate = func(tx *gorm.DB, plotID uint) error {
		n := calls.Add(1)
		switch n {
		case 1:
			return pgCodeErr("55P03")
		case 2:
			return context.DeadlineExceeded
		case 3:
			return pgCodeErr("40P01")
		default:
			return orig(tx, plotID)
		}
	}
	t.Cleanup(func() { lockPlotForUpdate = orig })

	if _, err := invSvc.Accept(inv.ID, u.ID); err != nil {
		t.Fatalf("mixed transient errors should recover: %v", err)
	}
	if calls.Load() != 4 {
		t.Fatalf("attempts=%d want 4", calls.Load())
	}
	assertMemberCount(t, db, plot.ID, 2)
}

// 冲突路径组合：同一邀请重复处理 / 满员接受 / 释放与接受竞争；
// 全部只允许成功或明确业务冲突，不允许 500/503，且失败无残留。
func TestConflictPaths_ConcurrentOutcomes(t *testing.T) {
	t.Run("same invitation processed twice", func(t *testing.T) {
		db, invSvc, _, u, plot, inv := acceptFixture(t)
		start := make(chan struct{})
		errs := make(chan error, 2)
		go func() { <-start; _, e := invSvc.Accept(inv.ID, u.ID); errs <- e }()
		go func() { <-start; _, e := invSvc.Reject(inv.ID, u.ID); errs <- e }()
		close(start)
		e1, e2 := <-errs, <-errs
		codes := map[int]int{}
		for _, e := range []error{e1, e2} {
			if e == nil {
				codes[0]++
			} else {
				codes[errCode(e)]++
			}
		}
		// 恰一个成功、另一个拿到明确的“邀请已处理”冲突；不允许 500/503
		if codes[0] != 1 || codes[constants.CodeInvitationNotPending] != 1 {
			t.Fatalf("want 1 success + 1 not-pending, got %v", codes)
		}
		// 终态：邀请只被处理一次；成员恒为 owner + 至多一个 helper（被拒绝则无）
		var status string
		db.Model(&model.PlotInvitation{}).Select("status").Where("id=?", inv.ID).Scan(&status)
		switch status {
		case string(constants.InvitationAccepted):
			assertMemberCount(t, db, plot.ID, 2)
		case string(constants.InvitationRejected):
			assertMemberCount(t, db, plot.ID, 1)
		default:
			t.Fatalf("invitation final status=%s want accepted/rejected", status)
		}
		assertNoDuplicateMembers(t, db, plot.ID)
	})

	t.Run("accept at full capacity", func(t *testing.T) {
		db, invSvc, owner, plot := fourMemberFullFixture(t)
		// 第 5 个 pending 邀请接受必须满员拒绝且不写成员
		extra := newTestUser(t, db, "extra", "citizen")
		inv, err := invSvc.Invite(plot.ID, owner.ID, "extra")
		if err != nil {
			t.Fatal(err)
		}
		_, err = invSvc.Accept(inv.ID, extra.ID)
		assertErrCode(t, err, constants.CodePlotMemberFull)
		assertMemberCount(t, db, plot.ID, constants.MaxPlotMembers)
		assertInvStatus(t, db, inv.ID, string(constants.InvitationPending))
		assertNoMember(t, db, plot.ID, extra.ID)
	})

	t.Run("release races accept", func(t *testing.T) {
		db, plotSvc, invSvc, owner, plot := releasedRaceFixture(t)
		u := newTestUser(t, db, "raceu", "citizen")
		inv, err := invSvc.Invite(plot.ID, owner.ID, "raceu")
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&model.Plot{}).Where("id=?", plot.ID).
			Update("status", string(constants.PlotStatusHarvested)).Error; err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var acceptErr, releaseErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); <-start; _, acceptErr = invSvc.Accept(inv.ID, u.ID) }()
		go func() { defer wg.Done(); <-start; _, releaseErr = plotSvc.Release(plot.ID, owner.ID, "citizen") }()
		close(start)
		wg.Wait()
		if releaseErr != nil {
			t.Fatalf("release must succeed: %v", releaseErr)
		}
		if acceptErr != nil {
			// 接受落后：必须是明确的 2010，而非 500
			assertErrCode(t, acceptErr, constants.CodePlotNotAdopted)
		}
		// 最终：地块释放、成员清零、无 pending
		var p model.Plot
		db.First(&p, plot.ID)
		if p.Status != string(constants.PlotStatusAvailable) || p.AdopterID != nil {
			t.Fatalf("plot not released: status=%s adopter=%v", p.Status, p.AdopterID)
		}
		assertMemberCount(t, db, plot.ID, 0)
		assertPendingCount(t, db, plot.ID, 0)
		assertNoDuplicateMembers(t, db, plot.ID)
	})
}

// fourMemberFullFixture 构造 owner + 3 helper（恰好 4/4）的已认养地块。
func fourMemberFullFixture(t *testing.T) (*gorm.DB, *PlotInvitationService, *model.User, *model.Plot) {
	t.Helper()
	db := newTestServiceDB(t)
	plotSvc, _, invSvc := newPlotCollabServices(t, db)
	owner := newTestUser(t, db, "fowner", "citizen")
	plot := newTestPlot(t, db, "P-FULL", "available", nil)
	if _, err := plotSvc.Adopt(plot.ID, owner.ID, "citizen", owner.Username); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		name := "full" + string(rune('a'+i))
		u := newTestUser(t, db, name, "citizen")
		inv, err := invSvc.Invite(plot.ID, owner.ID, name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := invSvc.Accept(inv.ID, u.ID); err != nil {
			t.Fatal(err)
		}
	}
	return db, invSvc, owner, plot
}

func releasedRaceFixture(t *testing.T) (*gorm.DB, *PlotService, *PlotInvitationService, *model.User, *model.Plot) {
	t.Helper()
	db := newTestServiceDB(t)
	plotSvc, _, invSvc := newPlotCollabServices(t, db)
	owner := newTestUser(t, db, "raceowner", "citizen")
	plot := newTestPlot(t, db, "P-RACE", "available", nil)
	if _, err := plotSvc.Adopt(plot.ID, owner.ID, "citizen", owner.Username); err != nil {
		t.Fatal(err)
	}
	return db, plotSvc, invSvc, owner, plot
}
