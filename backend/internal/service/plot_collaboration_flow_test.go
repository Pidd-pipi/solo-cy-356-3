package service

import (
	"fmt"
	"testing"

	"gorm.io/gorm"

	"github.com/communitygarden/server/internal/constants"
	"github.com/communitygarden/server/internal/model"
)

// 本组单测验证地块协作邀请的完整状态流与核心规则，所有用例除返回值外都断言数据库内最终状态。
//
// 状态机：
//
//	邀请:   (owner) -> pending
//	邀请:   pending -> accepted（被邀请人接受，写入 helper 成员）
//	邀请:   pending -> rejected（被邀请人拒绝，不写成员）
//	邀请:   pending -> revoked  （认养人撤回）
//	成员:   helper 存在 -> 删除（主动退出）
//	释放:   地块 available -> 成员清空、待处理邀请全部 revoked

type gormHolder struct{ db *gorm.DB }

type flowCtx struct {
	plotSvc   *PlotService
	memberSvc *PlotMemberService
	invSvc    *PlotInvitationService
	owner     *model.User
	plot      *model.Plot
}

// setupFlowsDB 准备 owner 认养的地块。
func setupFlowsDB(t *testing.T) (*gormHolder, flowCtx) {
	t.Helper()
	gdb := newTestServiceDB(t)
	plotSvc, memberSvc, invSvc := newPlotCollabServices(t, gdb)
	owner := newTestUser(t, gdb, "fowner", "citizen")
	plot := newTestPlot(t, gdb, "P-FLOW", "available", nil)
	if _, err := plotSvc.Adopt(plot.ID, owner.ID, "citizen", owner.Username); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	return &gormHolder{db: gdb}, flowCtx{plotSvc: plotSvc, memberSvc: memberSvc, invSvc: invSvc, owner: owner, plot: plot}
}

// ---- 数据库最终状态断言（不只检查返回值）----

func assertMemberCount(t *testing.T, db *gorm.DB, plotID uint, want int64) {
	t.Helper()
	var got int64
	if err := db.Model(&model.PlotMember{}).Where("plot_id=?", plotID).Count(&got).Error; err != nil {
		t.Fatalf("count members: %v", err)
	}
	if got != want {
		t.Fatalf("plot %d member count=%d want %d", plotID, got, want)
	}
}

func assertNoMember(t *testing.T, db *gorm.DB, plotID, userID uint) {
	t.Helper()
	var got int64
	db.Model(&model.PlotMember{}).Where("plot_id=? AND user_id=?", plotID, userID).Count(&got)
	if got != 0 {
		t.Fatalf("unexpected member row plot=%d user=%d count=%d", plotID, userID, got)
	}
}

func assertMemberRole(t *testing.T, db *gorm.DB, plotID, userID uint, wantRole string) {
	t.Helper()
	var m model.PlotMember
	if err := db.Where("plot_id=? AND user_id=?", plotID, userID).First(&m).Error; err != nil {
		t.Fatalf("load member: %v", err)
	}
	if m.Role != wantRole {
		t.Fatalf("member role=%s want %s", m.Role, wantRole)
	}
}

func assertInvStatus(t *testing.T, db *gorm.DB, invID uint, want string) {
	t.Helper()
	var inv model.PlotInvitation
	if err := db.First(&inv, invID).Error; err != nil {
		t.Fatalf("load invitation %d: %v", invID, err)
	}
	if inv.Status != want {
		t.Fatalf("invitation %d status=%s want %s", invID, inv.Status, want)
	}
}

func assertPendingCount(t *testing.T, db *gorm.DB, plotID uint, want int64) {
	t.Helper()
	var got int64
	db.Model(&model.PlotInvitation{}).Where("plot_id=? AND status=?", plotID, string(constants.InvitationPending)).Count(&got)
	if got != want {
		t.Fatalf("plot %d pending count=%d want %d", plotID, got, want)
	}
}

func assertInvCount(t *testing.T, db *gorm.DB, plotID uint, want int64) {
	t.Helper()
	var got int64
	db.Model(&model.PlotInvitation{}).Where("plot_id=?", plotID).Count(&got)
	if got != want {
		t.Fatalf("plot %d invitation count=%d want %d", plotID, got, want)
	}
}

func TestFlow_InviteAcceptRejectRevoke(t *testing.T) {
	db, s := setupFlowsDB(t)

	// 拒绝流：u2 拒绝 -> rejected、无成员行、释放出一个可再次邀请的名额
	u2 := newTestUser(t, db.db, "f2", "citizen")
	inv2, err := s.invSvc.Invite(s.plot.ID, s.owner.ID, "f2")
	if err != nil {
		t.Fatalf("invite f2: %v", err)
	}
	if inv2.Status != string(constants.InvitationPending) {
		t.Fatalf("new invitation status=%s want pending", inv2.Status)
	}
	if got, err := s.invSvc.Reject(inv2.ID, u2.ID); err != nil || got.Status != string(constants.InvitationRejected) {
		t.Fatalf("reject: status=%v err=%v", got, err)
	}
	assertMemberCount(t, db.db, s.plot.ID, 1) // 仅 owner
	assertInvStatus(t, db.db, inv2.ID, string(constants.InvitationRejected))
	assertNoMember(t, db.db, s.plot.ID, u2.ID)

	// 拒绝后可再次邀请并接受
	inv2b, err := s.invSvc.Invite(s.plot.ID, s.owner.ID, "f2")
	if err != nil {
		t.Fatalf("re-invite after reject: %v", err)
	}
	if got, err := s.invSvc.Accept(inv2b.ID, u2.ID); err != nil || got.Status != string(constants.InvitationAccepted) {
		t.Fatalf("accept after reject: status=%v err=%v", got, err)
	}
	assertMemberCount(t, db.db, s.plot.ID, 2)
	assertMemberRole(t, db.db, s.plot.ID, u2.ID, string(constants.PlotMemberHelper))
	assertInvStatus(t, db.db, inv2b.ID, string(constants.InvitationAccepted))

	// 撤回流：u3 pending -> owner 撤回 -> revoked，u3 接受被拒
	u3 := newTestUser(t, db.db, "f3", "citizen")
	inv3, err := s.invSvc.Invite(s.plot.ID, s.owner.ID, "f3")
	if err != nil {
		t.Fatalf("invite f3: %v", err)
	}
	if _, err := s.invSvc.Revoke(inv3.ID, s.owner.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	assertInvStatus(t, db.db, inv3.ID, string(constants.InvitationRevoked))
	_, err = s.invSvc.Accept(inv3.ID, u3.ID)
	assertErrCode(t, err, constants.CodeInvitationNotPending)
	assertNoMember(t, db.db, s.plot.ID, u3.ID)
}

func TestFlow_DuplicateInviteRules(t *testing.T) {
	db, s := setupFlowsDB(t)
	u := newTestUser(t, db.db, "dup", "citizen")

	if _, err := s.invSvc.Invite(s.plot.ID, s.owner.ID, "dup"); err != nil {
		t.Fatalf("first invite: %v", err)
	}
	// 待处理期间重复邀请同一人 -> 2012
	_, err := s.invSvc.Invite(s.plot.ID, s.owner.ID, "dup")
	assertErrCode(t, err, constants.CodeDuplicateInvitation)
	// 库内仍只有一条邀请
	assertInvCount(t, db.db, s.plot.ID, 1)

	// 其它规则：邀请自己 2018；未注册 404；非 owner 2011
	_, err = s.invSvc.Invite(s.plot.ID, s.owner.ID, "fowner")
	assertErrCode(t, err, constants.CodeCannotInviteSelf)
	_, err = s.invSvc.Invite(s.plot.ID, s.owner.ID, "ghost_user")
	assertErrCode(t, err, constants.CodeNotFound)
	other := newTestUser(t, db.db, "notowner", "citizen")
	_, err = s.invSvc.Invite(s.plot.ID, other.ID, "dup")
	assertErrCode(t, err, constants.CodeNotPlotOwner)

	// 接受后再次邀请同一人 -> 2013
	inv, _ := s.invSvc.Invite(s.plot.ID, s.owner.ID, "dup")
	_ = inv
	// 上面重复邀请已失败，需要取第一条 pending 邀请 id
	var first model.PlotInvitation
	if err := db.db.Where("plot_id=? AND invitee_id=? AND status=?", s.plot.ID, u.ID, "pending").First(&first).Error; err != nil {
		t.Fatalf("load first invitation: %v", err)
	}
	if _, err := s.invSvc.Accept(first.ID, u.ID); err != nil {
		t.Fatalf("accept: %v", err)
	}
	_, err = s.invSvc.Invite(s.plot.ID, s.owner.ID, "dup")
	assertErrCode(t, err, constants.CodeAlreadyPlotMember)
}

func TestFlow_NonInviteeCannotOperate(t *testing.T) {
	db, s := setupFlowsDB(t)
	invitee := newTestUser(t, db.db, "invee", "citizen")
	stranger := newTestUser(t, db.db, "stranger", "citizen")
	inv, err := s.invSvc.Invite(s.plot.ID, s.owner.ID, "invee")
	if err != nil {
		t.Fatalf("invite: %v", err)
	}

	// 非被邀请人不能接受/拒绝
	_, err = s.invSvc.Accept(inv.ID, stranger.ID)
	assertErrCode(t, err, constants.CodeNotInvitee)
	_, err = s.invSvc.Reject(inv.ID, stranger.ID)
	assertErrCode(t, err, constants.CodeNotInvitee)
	// 非认养人不能撤回
	_, err = s.invSvc.Revoke(inv.ID, stranger.ID)
	assertErrCode(t, err, constants.CodeNotPlotOwner)
	// 邀请仍是 pending、无成员产生
	assertInvStatus(t, db.db, inv.ID, string(constants.InvitationPending))
	assertMemberCount(t, db.db, s.plot.ID, 1)

	// 被邀请人本人接受成功
	if _, err := s.invSvc.Accept(inv.ID, invitee.ID); err != nil {
		t.Fatalf("invitee accept: %v", err)
	}
	assertMemberCount(t, db.db, s.plot.ID, 2)
}

func TestFlow_FourMemberCapAndPendingNotCounted(t *testing.T) {
	db, s := setupFlowsDB(t)
	// owner 已占 1 席；发送 5 条待处理邀请：全部允许（pending 不占名额）
	users := make([]*model.User, 5)
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("cap%d", i)
		users[i] = newTestUser(t, db.db, name, "citizen")
		if _, err := s.invSvc.Invite(s.plot.ID, s.owner.ID, name); err != nil {
			t.Fatalf("invite %s: %v", name, err)
		}
	}
	assertMemberCount(t, db.db, s.plot.ID, 1) // 5 条 pending 后成员仍只有 owner
	assertPendingCount(t, db.db, s.plot.ID, 5)

	// 前 3 人接受 -> 4/4
	for i := 0; i < 3; i++ {
		var inv model.PlotInvitation
		if err := db.db.Where("plot_id=? AND invitee_id=? AND status=?", s.plot.ID, users[i].ID, "pending").First(&inv).Error; err != nil {
			t.Fatalf("load inv %d: %v", i, err)
		}
		if _, err := s.invSvc.Accept(inv.ID, users[i].ID); err != nil {
			t.Fatalf("accept %d: %v", i, err)
		}
	}
	assertMemberCount(t, db.db, s.plot.ID, constants.MaxPlotMembers)

	// 第 4、5 人接受 -> 2014，邀请保持 pending（不占名额）
	for i := 3; i < 5; i++ {
		var inv model.PlotInvitation
		db.db.Where("plot_id=? AND invitee_id=? AND status=?", s.plot.ID, users[i].ID, "pending").First(&inv)
		_, err := s.invSvc.Accept(inv.ID, users[i].ID)
		assertErrCode(t, err, constants.CodePlotMemberFull)
		assertInvStatus(t, db.db, inv.ID, string(constants.InvitationPending))
		assertNoMember(t, db.db, s.plot.ID, users[i].ID)
	}
	assertMemberCount(t, db.db, s.plot.ID, constants.MaxPlotMembers)
}

func TestFlow_LeaveThenBecomeMemberAgain(t *testing.T) {
	db, s := setupFlowsDB(t)
	u := newTestUser(t, db.db, "leaveback", "citizen")
	inv, err := s.invSvc.Invite(s.plot.ID, s.owner.ID, "leaveback")
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	if _, err := s.invSvc.Accept(inv.ID, u.ID); err != nil {
		t.Fatalf("accept: %v", err)
	}
	assertMemberCount(t, db.db, s.plot.ID, 2)

	// owner 不能退出
	_, err = s.memberSvc.Leave(s.plot.ID, s.owner.ID)
	assertErrCode(t, err, constants.CodeOwnerCannotLeave)
	assertMemberCount(t, db.db, s.plot.ID, 2)

	// helper 退出
	if _, err := s.memberSvc.Leave(s.plot.ID, u.ID); err != nil {
		t.Fatalf("leave: %v", err)
	}
	assertNoMember(t, db.db, s.plot.ID, u.ID)
	assertMemberCount(t, db.db, s.plot.ID, 1)

	// 退出后可被再次邀请并接受，重新成为成员
	inv2, err := s.invSvc.Invite(s.plot.ID, s.owner.ID, "leaveback")
	if err != nil {
		t.Fatalf("re-invite after leave: %v", err)
	}
	if _, err := s.invSvc.Accept(inv2.ID, u.ID); err != nil {
		t.Fatalf("re-accept: %v", err)
	}
	assertMemberCount(t, db.db, s.plot.ID, 2)
	assertMemberRole(t, db.db, s.plot.ID, u.ID, string(constants.PlotMemberHelper))
}

func TestFlow_ReleaseCleansMembersAndPending(t *testing.T) {
	db, s := setupFlowsDB(t)
	// 一个已接受成员 + 一个待处理邀请 + 一个历史拒绝（应保留为审计轨迹）
	accepted := newTestUser(t, db.db, "relacc", "citizen")
	pending := newTestUser(t, db.db, "relpend", "citizen")
	rejected := newTestUser(t, db.db, "relrej", "citizen")

	iAcc, _ := s.invSvc.Invite(s.plot.ID, s.owner.ID, "relacc")
	iPen, _ := s.invSvc.Invite(s.plot.ID, s.owner.ID, "relpend")
	iRej, _ := s.invSvc.Invite(s.plot.ID, s.owner.ID, "relrej")
	if _, err := s.invSvc.Accept(iAcc.ID, accepted.ID); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if _, err := s.invSvc.Reject(iRej.ID, rejected.ID); err != nil {
		t.Fatalf("reject: %v", err)
	}
	assertMemberCount(t, db.db, s.plot.ID, 2)

	// 释放仅允许 harvested -> available
	if err := db.db.Model(&model.Plot{}).Where("id=?", s.plot.ID).
		Update("status", string(constants.PlotStatusHarvested)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.plotSvc.Release(s.plot.ID, s.owner.ID, "citizen"); err != nil {
		t.Fatalf("release: %v", err)
	}

	// 地块回到 available、无认养人
	var plot model.Plot
	db.db.First(&plot, s.plot.ID)
	if plot.Status != string(constants.PlotStatusAvailable) || plot.AdopterID != nil {
		t.Fatalf("plot after release status=%s adopter=%v", plot.Status, plot.AdopterID)
	}
	// 成员全部清空（含 owner）
	assertMemberCount(t, db.db, s.plot.ID, 0)
	// 待处理邀请被批量撤回
	assertInvStatus(t, db.db, iPen.ID, string(constants.InvitationRevoked))
	assertPendingCount(t, db.db, s.plot.ID, 0)
	// 历史终态邀请保留（rejected 不被改动）
	assertInvStatus(t, db.db, iRej.ID, string(constants.InvitationRejected))
	_ = iAcc

	// 释放后不能再邀请或接受
	_, err := s.invSvc.Invite(s.plot.ID, s.owner.ID, "relpend")
	assertErrCode(t, err, constants.CodePlotNotAdopted)
	_, err = s.invSvc.Accept(iPen.ID, pending.ID)
	assertErrCode(t, err, constants.CodePlotNotAdopted)
}
