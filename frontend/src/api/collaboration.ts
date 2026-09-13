import { get, post } from '@/utils/request'
import type { UserInfo } from './auth'
import type { Plot } from './plot'

// 地块协作成员
export interface PlotMember {
  id: number
  plot_id: number
  user_id: number
  user: UserInfo | null
  role: string
  invited_by: number | null
  created_at: string
}

// 地块协作邀请
export interface PlotInvitation {
  id: number
  plot_id: number
  plot?: Plot
  inviter_id: number
  inviter: UserInfo | null
  invitee_id: number
  invitee: UserInfo | null
  status: string
  responded_at: string
  created_at: string
}

// 协作页聚合视图
export interface PlotCollaboration {
  plot: Plot
  member_count: number
  max_members: number
  members: PlotMember[]
  invitations: PlotInvitation[]
}

// 协作页：地块 + 成员 + 全部邀请
export function getCollaboration(plotId: number): Promise<PlotCollaboration> {
  return get(`/plots/${plotId}/collaboration`)
}

// 认养人邀请已注册居民
export function inviteMember(plotId: number, username: string): Promise<PlotInvitation> {
  return post(`/plots/${plotId}/invitations`, { username })
}

// 被邀请人接受
export function acceptInvitation(invitationId: number): Promise<PlotInvitation> {
  return post(`/plot-invitations/${invitationId}/accept`)
}

// 被邀请人拒绝
export function rejectInvitation(invitationId: number): Promise<PlotInvitation> {
  return post(`/plot-invitations/${invitationId}/reject`)
}

// 认养人撤回待处理邀请
export function revokeInvitation(invitationId: number): Promise<PlotInvitation> {
  return post(`/plot-invitations/${invitationId}/revoke`)
}

// 协作成员主动退出
export function leavePlotTeam(plotId: number): Promise<{ plot_id: number; message: string }> {
  return post(`/plots/${plotId}/members/leave`)
}

// 当前居民收到的邀请（不传 status 返回全部）
export function listMyPlotInvitations(status?: string): Promise<PlotInvitation[]> {
  return get('/my/plot-invitations', { params: status ? { status } : {} })
}
