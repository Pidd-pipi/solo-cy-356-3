package service

import (
	"testing"

	"github.com/communitygarden/server/internal/constants"
	"github.com/communitygarden/server/internal/model"
	"github.com/communitygarden/server/internal/util"
)

// assertErrCode 校验错误是否为预期业务错误码。
func assertErrCode(t *testing.T, err error, wantCode int) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error code %d, got nil", wantCode)
	}
	ae, ok := err.(*util.AppError)
	if !ok {
		t.Fatalf("expected AppError, got %T: %v", err, err)
	}
	if ae.Code != wantCode {
		t.Fatalf("expected error code %d, got %d (%s)", wantCode, ae.Code, ae.Message)
	}
}

func TestPlotCollaboration_FullLifecycle(t *testing.T) {
	db := newTestServiceDB(t)
	plotSvc, memberSvc, invSvc := newPlotCollabServices(t, db)

	owner := newTestUser(t, db, "owner1", "citizen")
	u2 := newTestUser(t, db, "neighbor2", "citizen")
	u3 := newTestUser(t, db, "neighbor3", "citizen")
	u4 := newTestUser(t, db, "neighbor4", "citizen")
	u5 := newTestUser(t, db, "neighbor5", "citizen")
	u6 := newTestUser(t, db, "neighbor6", "citizen")
	plot := newTestPlot(t, db, "P-COLLAB", "available", nil)

	// 认养 -> owner 自动成为成员（1/4）
	adopted, err := plotSvc.Adopt(plot.ID, owner.ID, "citizen", owner.Username)
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	view, err := invSvc.GetCollaboration(plot.ID)
	if err != nil {
		t.Fatalf("get collab: %v", err)
	}
	if view.MemberCount != 1 || view.MaxMembers != constants.MaxPlotMembers {
		t.Fatalf("after adopt member_count=%d want 1, max=%d want 4", view.MemberCount, view.MaxMembers)
	}
	if adopted.Status != "adopted" {
		t.Fatalf("plot status=%s want adopted", adopted.Status)
	}

	// 邀请 u2：待处理邀请不占名额
	inv2, err := invSvc.Invite(plot.ID, owner.ID, u2.Username)
	if err != nil {
		t.Fatalf("invite u2: %v", err)
	}
	if inv2.Status != string(constants.InvitationPending) {
		t.Fatalf("invitation status=%s want pending", inv2.Status)
	}
	view, _ = invSvc.GetCollaboration(plot.ID)
	if view.MemberCount != 1 {
		t.Fatalf("pending invitation must not occupy slot: members=%d want 1", view.MemberCount)
	}

	// 重复邀请同一人 -> 拒绝
	_, err = invSvc.Invite(plot.ID, owner.ID, u2.Username)
	assertErrCode(t, err, constants.CodeDuplicateInvitation)

	// 邀请自己 -> 拒绝
	_, err = invSvc.Invite(plot.ID, owner.ID, owner.Username)
	assertErrCode(t, err, constants.CodeCannotInviteSelf)

	// 非认养人邀请 -> 拒绝
	_, err = invSvc.Invite(plot.ID, u3.ID, u4.Username)
	assertErrCode(t, err, constants.CodeNotPlotOwner)

	// 未注册居民 -> 404
	_, err = invSvc.Invite(plot.ID, owner.ID, "ghost")
	assertErrCode(t, err, constants.CodeNotFound)

	// 非被邀请人接受 -> 拒绝
	_, err = invSvc.Accept(inv2.ID, u3.ID)
	assertErrCode(t, err, constants.CodeNotInvitee)

	// u2 接受 -> 成员 2/4
	if _, err := invSvc.Accept(inv2.ID, u2.ID); err != nil {
		t.Fatalf("u2 accept: %v", err)
	}
	view, _ = invSvc.GetCollaboration(plot.ID)
	if view.MemberCount != 2 {
		t.Fatalf("after accept members=%d want 2", view.MemberCount)
	}

	// 已接受的成员再次邀请 -> 拒绝
	_, err = invSvc.Invite(plot.ID, owner.ID, u2.Username)
	assertErrCode(t, err, constants.CodeAlreadyPlotMember)

	// 重复接受（邀请已处理）-> 拒绝
	_, err = invSvc.Accept(inv2.ID, u2.ID)
	assertErrCode(t, err, constants.CodeInvitationNotPending)

	// 拒绝流程：u3 收到邀请后拒绝，不占名额
	inv3, err := invSvc.Invite(plot.ID, owner.ID, u3.Username)
	if err != nil {
		t.Fatalf("invite u3: %v", err)
	}
	if _, err := invSvc.Reject(inv3.ID, u3.ID); err != nil {
		t.Fatalf("u3 reject: %v", err)
	}
	view, _ = invSvc.GetCollaboration(plot.ID)
	if view.MemberCount != 2 {
		t.Fatalf("rejected invitation must not occupy slot, members=%d want 2", view.MemberCount)
	}
	// 拒绝后可再次邀请（仅待处理邀请拦截重复）
	inv3b, err := invSvc.Invite(plot.ID, owner.ID, u3.Username)
	if err != nil {
		t.Fatalf("re-invite after rejection must be allowed: %v", err)
	}

	// 撤回流程：u4 待处理邀请，认养人撤回
	inv4, err := invSvc.Invite(plot.ID, owner.ID, u4.Username)
	if err != nil {
		t.Fatalf("invite u4: %v", err)
	}
	if _, err := invSvc.Revoke(inv4.ID, owner.ID); err != nil {
		t.Fatalf("owner revoke: %v", err)
	}
	// 撤回后 u4 再接受 -> 拒绝
	_, err = invSvc.Accept(inv4.ID, u4.ID)
	assertErrCode(t, err, constants.CodeInvitationNotPending)
	// 非邀请人撤回 -> 拒绝
	_, err = invSvc.Revoke(inv3b.ID, u5.ID)
	assertErrCode(t, err, constants.CodeNotPlotOwner)

	// 名额边界：当前 owner+u2=2；u3 接受 -> 3/4
	if _, err := invSvc.Accept(inv3b.ID, u3.ID); err != nil {
		t.Fatalf("u3 accept second invite: %v", err)
	}
	// 多个待处理邀请不占名额：u4、u5 均可发送
	inv4b, err := invSvc.Invite(plot.ID, owner.ID, u4.Username)
	if err != nil {
		t.Fatalf("invite u4 again: %v", err)
	}
	inv5, err := invSvc.Invite(plot.ID, owner.ID, u5.Username)
	if err != nil {
		t.Fatalf("pending invitations must not occupy slot, invite u5: %v", err)
	}
	// u4 接受 -> 4/4 满员
	if _, err := invSvc.Accept(inv4b.ID, u4.ID); err != nil {
		t.Fatalf("u4 accept: %v", err)
	}
	// u5 接受时满员 -> 拒绝，邀请保持 pending（仍不占名额）
	_, err = invSvc.Accept(inv5.ID, u5.ID)
	assertErrCode(t, err, constants.CodePlotMemberFull)
	view, _ = invSvc.GetCollaboration(plot.ID)
	if view.MemberCount != constants.MaxPlotMembers {
		t.Fatalf("members=%d want 4", view.MemberCount)
	}
	// 已满员时仍可发出待处理邀请（不占名额，等待队列），接受时才被名额拦截
	inv6, err := invSvc.Invite(plot.ID, owner.ID, u6.Username)
	if err != nil {
		t.Fatalf("pending invitation at full capacity must be allowed: %v", err)
	}
	_, err = invSvc.Accept(inv6.ID, u6.ID)
	assertErrCode(t, err, constants.CodePlotMemberFull)

	// 地块详情 DTO 的协作摘要（inv5、inv6 两条待处理）
	detail, err := plotSvc.GetDetailDTO(plot.ID)
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	if detail.Collaboration == nil || detail.Collaboration.MemberCount != 4 ||
		detail.Collaboration.PendingCount != 2 {
		t.Fatalf("collaboration summary invalid: %+v", detail.Collaboration)
	}

	// 一名 helper 退出释放名额后，等待队列中的 u6 可接受
	if _, err := memberSvc.Leave(plot.ID, u2.ID); err != nil {
		t.Fatalf("u2 leave: %v", err)
	}
	if _, err := invSvc.Accept(inv6.ID, u6.ID); err != nil {
		t.Fatalf("u6 accept after slot freed: %v", err)
	}
}

func TestPlotCollaboration_LeaveAndRelease(t *testing.T) {
	db := newTestServiceDB(t)
	plotSvc, memberSvc, invSvc := newPlotCollabServices(t, db)

	owner := newTestUser(t, db, "owner2", "citizen")
	u2 := newTestUser(t, db, "helper2", "citizen")
	late := newTestUser(t, db, "latecomer", "citizen")
	plot := newTestPlot(t, db, "P-LEAVE", "available", nil)
	if _, err := plotSvc.Adopt(plot.ID, owner.ID, "citizen", owner.Username); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	inv, err := invSvc.Invite(plot.ID, owner.ID, u2.Username)
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	if _, err := invSvc.Accept(inv.ID, u2.ID); err != nil {
		t.Fatalf("accept: %v", err)
	}

	// owner 不能以成员身份退出
	_, err = memberSvc.Leave(plot.ID, owner.ID)
	assertErrCode(t, err, constants.CodeOwnerCannotLeave)

	// 协作成员可退出
	if _, err := memberSvc.Leave(plot.ID, u2.ID); err != nil {
		t.Fatalf("helper leave: %v", err)
	}
	view, _ := invSvc.GetCollaboration(plot.ID)
	if view.MemberCount != 1 {
		t.Fatalf("after leave members=%d want 1", view.MemberCount)
	}

	// 退出后可被再次邀请并接受
	inv2, err := invSvc.Invite(plot.ID, owner.ID, u2.Username)
	if err != nil {
		t.Fatalf("re-invite after leave: %v", err)
	}
	if _, err := invSvc.Accept(inv2.ID, u2.ID); err != nil {
		t.Fatalf("re-accept after leave: %v", err)
	}

	// 构造一条待处理邀请，再走 harvested -> released
	pending, err := invSvc.Invite(plot.ID, owner.ID, late.Username)
	if err != nil {
		t.Fatalf("invite latecomer: %v", err)
	}
	if err := db.Model(&model.Plot{}).Where("id = ?", plot.ID).
		Update("status", string(constants.PlotStatusHarvested)).Error; err != nil {
		t.Fatalf("mark harvested: %v", err)
	}
	if _, err := plotSvc.Release(plot.ID, owner.ID, "citizen"); err != nil {
		t.Fatalf("release: %v", err)
	}
	view, _ = invSvc.GetCollaboration(plot.ID)
	if view.MemberCount != 0 {
		t.Fatalf("after release members=%d want 0", view.MemberCount)
	}
	for _, iv := range view.Invitations {
		if iv.Status == string(constants.InvitationPending) {
			t.Fatalf("pending invitation %d must be revoked after release", iv.ID)
		}
	}

	// 释放后邀请 -> 拒绝
	_, err = invSvc.Invite(plot.ID, owner.ID, u2.Username)
	assertErrCode(t, err, constants.CodePlotNotAdopted)
	// 释放后接受被撤回的邀请 -> 拒绝（被邀请人身份不变，但地块已释放）
	_, err = invSvc.Accept(pending.ID, late.ID)
	assertErrCode(t, err, constants.CodePlotNotAdopted)
	// 释放后退出 -> 成员行已清空，404
	_, err = memberSvc.Leave(plot.ID, u2.ID)
	assertErrCode(t, err, constants.CodeNotFound)
}
