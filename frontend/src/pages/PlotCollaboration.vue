<template>
  <div class="page-card" v-loading="store.loading">
    <div class="collab-header">
      <div>
        <el-button link @click="goPlots"><el-icon><ArrowLeft /></el-icon> 返回地块列表</el-button>
        <h3 class="page-title" style="margin: 8px 0 4px">
          地块协作 · {{ detail?.plot.name }}（{{ detail?.plot.code }}）
        </h3>
        <StatusBadge v-if="detail" :value="detail.plot.status" :meta-map="PlotStatusMeta" />
      </div>
      <div v-if="detail" class="quota-box">
        <div class="quota-title">协作名额</div>
        <div class="quota-num">{{ detail.member_count }}/{{ detail.max_members }}</div>
        <el-progress :percentage="quotaPercent" :status="detail.member_count >= detail.max_members ? 'success' : ''" :stroke-width="8" style="width: 160px" />
        <div class="quota-sub">待处理邀请 {{ pendingInvitations.length }} 条（不占名额）</div>
      </div>
    </div>

    <el-alert
      v-if="released"
      type="info"
      :closable="false"
      show-icon
      title="地块已释放回共享池，协作已结束：成员已清空、待处理邀请已撤回，不能再邀请或接受。"
      style="margin: 12px 0"
    />

    <!-- 成员名单 -->
    <el-card shadow="never" style="margin-bottom: 16px">
      <template #header>
        <div class="card-title">
          <span>👨‍🌾 协作成员（{{ detail?.member_count ?? 0 }}/{{ detail?.max_members ?? 4 }}）</span>
        </div>
      </template>
      <el-table :data="detail?.members ?? []" border stripe size="small">
        <el-table-column label="居民" min-width="160">
          <template #default="{ row }">
            {{ row.user?.nickname || row.user?.username || '-' }}
            <span class="muted">@{{ row.user?.username }}</span>
          </template>
        </el-table-column>
        <el-table-column label="角色" width="110">
          <template #default="{ row }">
            <el-tag :type="row.role === 'owner' ? 'warning' : 'success'" size="small">
              {{ PlotMemberRoleText[row.role] || row.role }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="created_at" label="加入时间" width="180" />
        <el-table-column label="操作" width="120">
          <template #default="{ row }">
            <el-button
              v-if="row.user_id === myUserId && row.role === 'helper'"
              type="danger"
              size="small"
              link
              @click="onLeave"
            >退出协作</el-button>
            <span v-else class="muted">-</span>
          </template>
        </el-table-column>
        <template #empty><EmptyState description="暂无协作成员" /></template>
      </el-table>
    </el-card>

    <!-- 待处理邀请 -->
    <el-card shadow="never" style="margin-bottom: 16px">
      <template #header>
        <div class="card-title">
          <span>✉️ 待处理邀请（{{ pendingInvitations.length }}）</span>
          <el-tag type="warning" size="small" effect="plain">待处理不占名额</el-tag>
        </div>
      </template>
      <el-table :data="pendingInvitations" border stripe size="small">
        <el-table-column label="被邀请居民" min-width="140">
          <template #default="{ row }">
            {{ row.invitee?.nickname || row.invitee?.username || '-' }}
            <span class="muted">@{{ row.invitee?.username }}</span>
          </template>
        </el-table-column>
        <el-table-column label="邀请人" min-width="120">
          <template #default="{ row }">{{ row.inviter?.nickname || row.inviter?.username || '-' }}</template>
        </el-table-column>
        <el-table-column prop="created_at" label="邀请时间" width="180" />
        <el-table-column label="操作" width="260">
          <template #default="{ row }">
            <template v-if="row.invitee_id === myUserId">
              <el-button type="success" size="small" @click="onRespond(row, true)" :disabled="released">接受</el-button>
              <el-button size="small" @click="onRespond(row, false)">拒绝</el-button>
            </template>
            <el-button v-if="isOwner" type="warning" size="small" @click="onRevoke(row)" :disabled="released">撤回</el-button>
            <span v-if="row.invitee_id !== myUserId && !isOwner" class="muted">等待对方处理</span>
          </template>
        </el-table-column>
        <template #empty><EmptyState description="暂无待处理邀请" /></template>
      </el-table>
    </el-card>

    <!-- 历史邀请 -->
    <el-card shadow="never">
      <template #header>📜 邀请历史（{{ historyInvitations.length }}）</template>
      <el-table :data="historyInvitations" border stripe size="small">
        <el-table-column label="被邀请居民" min-width="140">
          <template #default="{ row }">
            {{ row.invitee?.nickname || row.invitee?.username || '-' }}
            <span class="muted">@{{ row.invitee?.username }}</span>
          </template>
        </el-table-column>
        <el-table-column label="邀请人" min-width="120">
          <template #default="{ row }">{{ row.inviter?.nickname || row.inviter?.username || '-' }}</template>
        </el-table-column>
        <el-table-column prop="created_at" label="邀请时间" width="180" />
        <el-table-column prop="responded_at" label="处理时间" width="180" />
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <StatusBadge :value="row.status" :meta-map="InvitationStatusMeta" />
          </template>
        </el-table-column>
        <template #empty><EmptyState description="暂无历史邀请" /></template>
      </el-table>
    </el-card>

    <!-- 邀请弹窗（仅认养人） -->
    <el-dialog v-model="inviteVisible" title="邀请居民共同照料地块" width="440px">
      <el-alert
        type="info"
        :closable="false"
        :title="`当前名额 ${detail?.member_count ?? 0}/${detail?.max_members ?? 4}；待处理邀请不占名额，接受时若已满员将无法加入。`"
        style="margin-bottom: 12px"
      />
      <el-form label-width="92px" @submit.prevent>
        <el-form-item label="居民用户名" required>
          <el-input v-model="inviteUsername" placeholder="输入已注册居民用户名，如 neighbor1" clearable @keyup.enter="onInvite" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="inviteVisible = false">取消</el-button>
        <el-button type="primary" :loading="inviting" @click="onInvite">发送邀请</el-button>
      </template>
    </el-dialog>

    <!-- 悬浮邀请按钮 -->
    <el-button
      v-if="isOwner && !released"
      type="primary"
      round
      class="invite-fab"
      @click="openInvite"
    >＋ 邀请居民</el-button>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { ArrowLeft } from '@element-plus/icons-vue'
import { useCollaborationStore } from '@/stores/collaboration'
import { useAuth } from '@/hooks/useAuth'
import StatusBadge from '@/components/StatusBadge.vue'
import EmptyState from '@/components/EmptyState.vue'
import { InvitationStatusMeta, MaxPlotMembers, PlotMemberRoleText, PlotStatusMeta } from '@/constants'
import type { PlotInvitation } from '@/api/collaboration'

const route = useRoute()
const router = useRouter()
const store = useCollaborationStore()
const { user } = useAuth()

const inviteVisible = ref(false)
const inviteUsername = ref('')
const inviting = ref(false)

const plotId = Number(route.params.id)
const detail = computed(() => store.detail)
const myUserId = computed(() => user.value?.id ?? 0)
const isOwner = computed(() => !!detail.value && detail.value.plot.adopter_id === myUserId.value)
const released = computed(() => !!detail.value && (detail.value.plot.status === 'available' || !detail.value.plot.adopter_id))
const quotaPercent = computed(() => Math.round(((detail.value?.member_count ?? 0) / MaxPlotMembers) * 100))
const pendingInvitations = computed<PlotInvitation[]>(() =>
  (detail.value?.invitations ?? []).filter((i) => i.status === 'pending')
)
const historyInvitations = computed<PlotInvitation[]>(() =>
  (detail.value?.invitations ?? []).filter((i) => i.status !== 'pending')
)

async function fetchAll() {
  await store.fetch(plotId)
  await store.fetchMyInvitations('pending')
}

function openInvite() {
  inviteUsername.value = ''
  inviteVisible.value = true
}

async function onInvite() {
  const username = inviteUsername.value.trim()
  if (!username) {
    ElMessage.warning('请输入已注册居民用户名')
    return
  }
  inviting.value = true
  try {
    await store.invite(plotId, username)
    ElMessage.success('协作邀请已发送，等待对方接受')
    inviteVisible.value = false
  } finally {
    inviting.value = false
  }
}

async function onRespond(row: PlotInvitation, accept: boolean) {
  if (accept) {
    try {
      await ElMessageBox.confirm(`确认接受该地块的协作邀请吗？接受后将成为协作成员（名额受 4 人上限约束）。`, '接受邀请', { type: 'success' })
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
  await store.respond(row.id, accept, plotId)
  ElMessage.success(accept ? '已接受邀请，成为地块协作成员' : '已拒绝该地块协作邀请')
}

async function onRevoke(row: PlotInvitation) {
  try {
    await ElMessageBox.confirm(`确认撤回发给 @${row.invitee?.username} 的待处理邀请吗？`, '撤回邀请', { type: 'warning' })
  } catch {
    return
  }
  await store.revoke(row.id, plotId)
  ElMessage.success('待处理邀请已撤回')
}

async function onLeave() {
  try {
    await ElMessageBox.confirm('退出后将不再参与本地块协作，确认退出吗？', '退出协作', { type: 'warning' })
  } catch {
    return
  }
  await store.leave(plotId)
  ElMessage.success('已退出地块协作')
}

function goPlots() {
  router.push('/plots')
}

onMounted(fetchAll)
</script>

<style scoped>
.collab-header { display: flex; justify-content: space-between; align-items: flex-start; gap: 16px; flex-wrap: wrap; }
.quota-box { background: #fff; border: 1px solid #ebeef5; border-radius: 8px; padding: 12px 16px; text-align: right; }
.quota-title { color: #909399; font-size: 12px; }
.quota-num { font-size: 24px; font-weight: 700; color: #409eff; line-height: 1.4; }
.quota-sub { color: #909399; font-size: 12px; margin-top: 4px; }
.card-title { display: flex; justify-content: space-between; align-items: center; }
.muted { color: #909399; font-size: 12px; margin-left: 4px; }
.invite-fab { position: fixed; right: 36px; bottom: 36px; z-index: 100; }
</style>
