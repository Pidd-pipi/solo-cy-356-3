import { defineStore } from 'pinia'
import {
  acceptInvitation as apiAccept,
  getCollaboration as apiGet,
  inviteMember as apiInvite,
  leavePlotTeam as apiLeave,
  listMyPlotInvitations as apiListMine,
  rejectInvitation as apiReject,
  revokeInvitation as apiRevoke,
  type PlotCollaboration,
  type PlotInvitation
} from '@/api/collaboration'

interface CollaborationState {
  detail: PlotCollaboration | null
  loading: boolean
  myInvitations: PlotInvitation[]
  myInvitationsLoading: boolean
}

export const useCollaborationStore = defineStore('collaboration', {
  state: (): CollaborationState => ({
    detail: null,
    loading: false,
    myInvitations: [],
    myInvitationsLoading: false
  }),
  actions: {
    async fetch(plotId: number) {
      this.loading = true
      try {
        this.detail = await apiGet(plotId)
      } finally {
        this.loading = false
      }
    },
    async invite(plotId: number, username: string) {
      await apiInvite(plotId, username)
      await this.fetch(plotId)
    },
    async respond(invitationId: number, accept: boolean, plotId?: number) {
      if (accept) {
        await apiAccept(invitationId)
      } else {
        await apiReject(invitationId)
      }
      await this.fetchMyInvitations('pending')
      if (plotId && this.detail?.plot.id === plotId) {
        await this.fetch(plotId)
      }
    },
    async revoke(invitationId: number, plotId: number) {
      await apiRevoke(invitationId)
      await this.fetch(plotId)
    },
    async leave(plotId: number) {
      await apiLeave(plotId)
      await this.fetch(plotId)
    },
    async fetchMyInvitations(status?: string) {
      this.myInvitationsLoading = true
      try {
        this.myInvitations = await apiListMine(status)
      } finally {
        this.myInvitationsLoading = false
      }
    }
  }
})
