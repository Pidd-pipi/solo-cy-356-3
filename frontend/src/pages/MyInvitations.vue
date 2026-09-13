<template>
  <div class="page-card">
    <div style="display: flex; justify-content: space-between; align-items: center">
      <h3 class="page-title">我的协作邀请</h3>
      <el-radio-group v-model="filter" size="small" @change="fetch">
        <el-radio-button label="pending">待处理</el-radio-button>
        <el-radio-button label="">全部</el-radio-button>
      </el-radio-group>
    </div>

    <el-table :data="store.myInvitations" v-loading="store.myInvitationsLoading" border stripe style="margin-top: 16px">
      <el-table-column label="地块" min-width="180">
        <template #default="{ row }">
          <el-link type="primary" @click="openCollab(row)">{{ row.plot?.name || `地块 #${row.plot_id}` }}</el-link>
          <span class="muted">{{ row.plot?.code }}</span>
        </template>
      </el-table-column>
      <el-table-column label="邀请人" min-width="140">
        <template #default="{ row }">{{ row.inviter?.nickname || row.inviter?.username || '-' }}</template>
      </el-table-column>
      <el-table-column prop="created_at" label="邀请时间" width="180" />
      <el-table-column label="状态" width="100">
        <template #default="{ row }">
          <StatusBadge :value="row.status" :meta-map="InvitationStatusMeta" />
        </template>
      </el-table-column>
      <el-table-column label="操作" width="200">
        <template #default="{ row }">
          <template v-if="row.status === 'pending'">
            <el-button type="success" size="small" @click="respond(row, true)">接受</el-button>
            <el-button size="small" @click="respond(row, false)">拒绝</el-button>
          </template>
          <span v-else class="muted">已处理</span>
        </template>
      </el-table-column>
      <template #empty><EmptyState description="暂无协作邀请" /></template>
    </el-table>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useCollaborationStore } from '@/stores/collaboration'
import StatusBadge from '@/components/StatusBadge.vue'
import EmptyState from '@/components/EmptyState.vue'
import { InvitationStatusMeta } from '@/constants'
import type { PlotInvitation } from '@/api/collaboration'

const router = useRouter()
const store = useCollaborationStore()
const filter = ref<'pending' | ''>('pending')

async function fetch() {
  await store.fetchMyInvitations(filter.value || undefined)
}

async function respond(row: PlotInvitation, accept: boolean) {
  if (accept) {
    try {
      await ElMessageBox.confirm(`确认接受地块「${row.plot?.name || row.plot_id}」的协作邀请吗？`, '接受邀请', { type: 'success' })
    } catch {
      return
    }
  } else {
    try {
      await ElMessageBox.confirm('确认拒绝该协作邀请吗？', '拒绝邀请', { type: 'warning' })
    } catch {
      return
    }
  }
  await store.respond(row.id, accept, row.plot_id)
  ElMessage.success(accept ? '已接受邀请，成为地块协作成员' : '已拒绝该地块协作邀请')
}

function openCollab(row: PlotInvitation) {
  router.push(`/plots/${row.plot_id}/collaboration`)
}

onMounted(fetch)
</script>

<style scoped>
.muted { color: #909399; font-size: 12px; margin-left: 6px; }
</style>
