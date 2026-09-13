package service

import (
	"fmt"
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/communitygarden/server/internal/constants"
	"github.com/communitygarden/server/internal/model"
	"github.com/communitygarden/server/internal/util"
)

// newSerialDB 构造单连接数据库：同一地块的协作事务在单连接上严格串行执行，
// 精确模拟 PostgreSQL 下“先取地块行 FOR UPDATE 锁”的串行化语义，
// 使名额计数/重复邀请/单次处理等竞态断言可重复。
func newSerialDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s/conc.db?mode=memory&cache=shared", t.TempDir())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open serial db: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(
		&model.User{}, &model.Plot{}, &model.PlotMember{}, &model.PlotInvitation{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func collabSetup(t *testing.T, db *gorm.DB) (*PlotService, *PlotInvitationService, *PlotMemberService, *model.User, *model.Plot) {
	plotSvc, memberSvc, invSvc := newPlotCollabServices(t, db)
	owner := newTestUser(t, db, "owner-c", "citizen")
	plot := newTestPlot(t, db, "P-CONC", "available", nil)
	if _, err := plotSvc.Adopt(plot.ID, owner.ID, "citizen", owner.Username); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	return plotSvc, invSvc, memberSvc, owner, plot
}

func inviteUser(t *testing.T, db *gorm.DB, invSvc *PlotInvitationService, plotID, ownerID uint, username string) (*model.User, *model.PlotInvitation) {
	t.Helper()
	u := newTestUser(t, db, username, "citizen")
	inv, err := invSvc.Invite(plotID, ownerID, username)
	if err != nil {
		t.Fatalf("invite %s: %v", username, err)
	}
	return u, inv
}

// 统计 goroutine 返回的错误码分布。
func codeCounts(errs []error) map[int]int {
	out := map[int]int{}
	for _, err := range errs {
		if err == nil {
			out[0]++
			continue
		}
		out[errCode(err)]++
	}
	return out
}

func errCode(err error) int {
	if ae, ok := err.(*util.AppError); ok {
		return ae.Code
	}
	return -1
}

// 并发重复运行多轮，保证可重复性。
func TestConcurrent_AcceptAndDuplicateInvite(t *testing.T) {
	const rounds = 20
	for r := 0; r < rounds; r++ {
		db := newSerialDB(t)
		_, invSvc, _, owner, plot := collabSetup(t, db)
		u, inv := inviteUser(t, db, invSvc, plot.ID, owner.ID, fmt.Sprintf("dup%d", r))

		var wg sync.WaitGroup
		var mu sync.Mutex
		var errs []error
		wg.Add(2)
		go func() { defer wg.Done(); _, e := invSvc.Accept(inv.ID, u.ID); mu.Lock(); errs = append(errs, e); mu.Unlock() }()
		go func() { defer wg.Done(); _, e := invSvc.Invite(plot.ID, owner.ID, u.Username); mu.Lock(); errs = append(errs, e); mu.Unlock() }()
		wg.Wait()

		counts := codeCounts(errs)
		if counts[0] != 1 {
			t.Fatalf("round %d: want exactly 1 success, got %v", r, counts)
		}
		// 输的一方必须得到明确业务冲突：
		// 邀请先执行 -> 重复邀请 2012；接受先提交 -> 已是成员 2013。绝不允许 500/满员。
		businessConflict := counts[constants.CodeDuplicateInvitation] + counts[constants.CodeAlreadyPlotMember]
		if businessConflict != 1 {
			t.Fatalf("round %d: want exactly 1 clear conflict (2012/2013), got %v", r, counts)
		}
		if counts[constants.CodeInternalError] > 0 || counts[constants.CodePlotMemberFull] > 0 {
			t.Fatalf("round %d: unexpected codes %v", r, counts)
		}
		var members int64
		db.Model(&model.PlotMember{}).Where("plot_id = ?", plot.ID).Count(&members)
		if members != 2 {
			t.Fatalf("round %d: members=%d want 2 (owner+1)", r, members)
		}
		var pendings int64
		db.Model(&model.PlotInvitation{}).Where("plot_id = ? AND status = ?", plot.ID, string(constants.InvitationPending)).Count(&pendings)
		if pendings != 0 {
			t.Fatalf("round %d: pending=%d want 0", r, pendings)
		}
	}
}

func TestConcurrent_MultipleInvitationAccepts(t *testing.T) {
	const rounds = 10
	for r := 0; r < rounds; r++ {
		db := newSerialDB(t)
		_, invSvc, _, owner, plot := collabSetup(t, db)
		users := make([]*model.User, 5)
		invs := make([]*model.PlotInvitation, 5)
		for i := 0; i < 5; i++ {
			users[i], invs[i] = inviteUser(t, db, invSvc, plot.ID, owner.ID, fmt.Sprintf("u%d-%d", i, r))
		}

		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make([]error, 5)
		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				<-start
				_, errs[idx] = invSvc.Accept(invs[idx].ID, users[idx].ID)
			}(i)
		}
		close(start)
		wg.Wait()

		counts := codeCounts(errs)
		// owner 占 1 席，剩余 3 席：5 个并发接受中恰 3 个成功、2 个满员
		if counts[0] != 3 {
			t.Fatalf("round %d: success=%d want 3, counts=%v", r, counts[0], counts)
		}
		if counts[constants.CodePlotMemberFull] != 2 {
			t.Fatalf("round %d: full=%d want 2, counts=%v", r, counts[constants.CodePlotMemberFull], counts)
		}
		if counts[constants.CodeInternalError] > 0 {
			t.Fatalf("round %d: internal error occurred: %v", r, counts)
		}
		var members int64
		db.Model(&model.PlotMember{}).Where("plot_id = ?", plot.ID).Count(&members)
		if members != constants.MaxPlotMembers {
			t.Fatalf("round %d: members=%d want exactly 4", r, members)
		}
		// 无重复成员（唯一索引兜底）
		var dup int64
		db.Raw(`SELECT count(*) FROM (SELECT user_id FROM plot_members WHERE plot_id = ? GROUP BY user_id HAVING count(*) > 1)`, plot.ID).Scan(&dup)
		if dup != 0 {
			t.Fatalf("round %d: duplicate member rows detected", r)
		}
		// 未接受的两条邀请必须仍为 pending（不占名额，可等退出后再接受）
		var pendings int64
		db.Model(&model.PlotInvitation{}).Where("plot_id = ? AND status = ?", plot.ID, string(constants.InvitationPending)).Count(&pendings)
		if pendings != 2 {
			t.Fatalf("round %d: pendings=%d want 2", r, pendings)
		}
	}
}

func TestConcurrent_SameInvitationProcessedTwice(t *testing.T) {
	const rounds = 20
	for r := 0; r < rounds; r++ {
		db := newSerialDB(t)
		_, invSvc, _, owner, plot := collabSetup(t, db)
		u, inv := inviteUser(t, db, invSvc, plot.ID, owner.ID, fmt.Sprintf("twice%d", r))

		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				<-start
				_, errs[idx] = invSvc.Accept(inv.ID, u.ID)
			}(i)
		}
		close(start)
		wg.Wait()

		counts := codeCounts(errs)
		if counts[0] != 1 || counts[constants.CodeInvitationNotPending] != 1 {
			t.Fatalf("round %d: want 1 success + 1 not-pending, got %v", r, counts)
		}
		var members int64
		db.Model(&model.PlotMember{}).Where("plot_id = ? AND user_id = ?", plot.ID, u.ID).Count(&members)
		if members != 1 {
			t.Fatalf("round %d: member rows=%d want exactly 1", r, members)
		}
	}
}

func TestConcurrent_RevokeVsAccept(t *testing.T) {
	const rounds = 20
	for r := 0; r < rounds; r++ {
		db := newSerialDB(t)
		_, invSvc, _, owner, plot := collabSetup(t, db)
		u, inv := inviteUser(t, db, invSvc, plot.ID, owner.ID, fmt.Sprintf("rv%d", r))

		start := make(chan struct{})
		var wg sync.WaitGroup
		var acceptErr, revokeErr error
		wg.Add(2)
		go func() { defer wg.Done(); <-start; _, acceptErr = invSvc.Accept(inv.ID, u.ID) }()
		go func() { defer wg.Done(); <-start; _, revokeErr = invSvc.Revoke(inv.ID, owner.ID) }()
		close(start)
		wg.Wait()

		// 不能出现内部错误；必有一方成功，另一方拿到明确的 2015
		for _, e := range []error{acceptErr, revokeErr} {
			if e != nil {
				assertErrCode(t, e, constants.CodeInvitationNotPending)
			}
		}
		if (acceptErr == nil) == (revokeErr == nil) {
			t.Fatalf("round %d: exactly one side must win: acceptErr=%v revokeErr=%v", r, acceptErr, revokeErr)
		}
		var status string
		db.Model(&model.PlotInvitation{}).Select("status").Where("id = ?", inv.ID).Scan(&status)
		var members int64
		db.Model(&model.PlotMember{}).Where("plot_id = ? AND user_id = ?", plot.ID, u.ID).Count(&members)
		switch status {
		case string(constants.InvitationAccepted):
			if members != 1 {
				t.Fatalf("round %d: accepted but members=%d", r, members)
			}
		case string(constants.InvitationRevoked):
			if members != 0 {
				t.Fatalf("round %d: revoked but members=%d", r, members)
			}
		default:
			t.Fatalf("round %d: unexpected invitation status %q", r, status)
		}
	}
}

func TestConcurrent_ReleaseVsAccept(t *testing.T) {
	const rounds = 20
	for r := 0; r < rounds; r++ {
		db := newSerialDB(t)
		plotSvc, invSvc, _, owner, plot := collabSetup(t, db)
		u, inv := inviteUser(t, db, invSvc, plot.ID, owner.ID, fmt.Sprintf("rel%d", r))
		// 释放仅允许 harvested -> available
		if err := db.Model(&model.Plot{}).Where("id = ?", plot.ID).
			Update("status", string(constants.PlotStatusHarvested)).Error; err != nil {
			t.Fatal(err)
		}

		start := make(chan struct{})
		var wg sync.WaitGroup
		var acceptErr, releaseErr error
		wg.Add(2)
		go func() { defer wg.Done(); <-start; _, acceptErr = invSvc.Accept(inv.ID, u.ID) }()
		go func() {
			defer wg.Done()
			<-start
			_, releaseErr = plotSvc.Release(plot.ID, owner.ID, "citizen")
		}()
		close(start)
		wg.Wait()

		// 任何顺序都不允许 500；接受要么成功（随后被释放清理），要么拿到 2010
		if acceptErr != nil {
			assertErrCode(t, acceptErr, constants.CodePlotNotAdopted)
		}
		if releaseErr != nil {
			t.Fatalf("round %d: release must succeed, got %v", r, releaseErr)
		}
		var status string
		db.Model(&model.Plot{}).Select("status").Where("id = ?", plot.ID).Scan(&status)
		if status != string(constants.PlotStatusAvailable) {
			t.Fatalf("round %d: plot status=%q want available", r, status)
		}
		var members int64
		db.Model(&model.PlotMember{}).Where("plot_id = ?", plot.ID).Count(&members)
		if members != 0 {
			t.Fatalf("round %d: members after release=%d want 0", r, members)
		}
		var pending int64
		db.Model(&model.PlotInvitation{}).Where("plot_id = ? AND status = ?", plot.ID, string(constants.InvitationPending)).Count(&pending)
		if pending != 0 {
			t.Fatalf("round %d: pending after release=%d want 0", r, pending)
		}
		// 释放后再接受/邀请必须明确拒绝
		_, err := invSvc.Accept(inv.ID, u.ID)
		assertErrCode(t, err, constants.CodePlotNotAdopted)
	}
}

func TestConcurrent_LeaveTwice(t *testing.T) {
	db := newSerialDB(t)
	plotSvc, invSvc, memberSvc, owner, plot := collabSetup(t, db)
	u, inv := inviteUser(t, db, invSvc, plot.ID, owner.ID, "leave")
	if _, err := invSvc.Accept(inv.ID, u.ID); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			_, errs[idx] = memberSvc.Leave(plot.ID, u.ID)
		}(i)
	}
	close(start)
	wg.Wait()
	counts := codeCounts(errs)
	if counts[0] != 1 || counts[constants.CodeNotFound] != 1 {
		t.Fatalf("want 1 leave success + 1 not-found, got %v", counts)
	}

	// owner 退出在并发/单次下都被拒绝
	if _, err := memberSvc.Leave(plot.ID, owner.ID); err == nil {
		t.Fatal("owner leave must be rejected")
	}
	_ = plotSvc
}
