<template>
  <div class="page-card">
    <div style="display: flex; justify-content: space-between; align-items: center">
      <h3 class="page-title">菜园地块认养（GIS 分布）</h3>
      <div>
        <el-button v-if="isAdmin" type="primary" @click="openCreate">+ 新增地块</el-button>
      </div>
    </div>

    <el-card shadow="never" style="margin-bottom: 16px">
      <template #header>地块分布图（按经纬度示意）</template>
      <svg :viewBox="viewBoxStr" class="plot-map" xmlns="http://www.w3.org/2000/svg">
        <rect x="0" y="0" :width="mapW" :height="mapH" fill="#e8f5e9" stroke="#a5d6a7" />
        <line v-for="i in 4" :key="'h' + i" :x1="0" :y1="(mapH / 5) * i" :x2="mapW" :y2="(mapH / 5) * i" stroke="#c8e6c9" stroke-dasharray="4 4" />
        <line v-for="i in 4" :key="'v' + i" :x1="(mapW / 5) * i" :y1="0" :x2="(mapW / 5) * i" :y2="mapH" stroke="#c8e6c9" stroke-dasharray="4 4" />
        <g v-for="p in store.plots" :key="p.id">
          <circle :cx="mapX(p)" :cy="mapY(p)" r="10" :fill="colorOf(p.status)" stroke="#fff" stroke-width="2" />
          <text :x="mapX(p)" :y="mapY(p) - 14" text-anchor="middle" font-size="10" fill="#333">{{ p.code }}</text>
          <title>{{ p.name }}｜{{ PlotStatusMeta[p.status]?.label }}</title>
        </g>
      </svg>
    </el-card>

    <DataTable :data="store.plots" :loading="store.loading" :total="store.total" :page-size="pagination.size.value" :current-page="pagination.page.value" @update:current-page="onPage">
      <el-table-column prop="code" label="编号" width="90" />
      <el-table-column prop="name" label="地块名称" min-width="140" />
      <el-table-column label="面积" width="90">
        <template #default="{ row }">{{ formatArea(row.area) }}</template>
      </el-table-column>
      <el-table-column label="土壤" width="90">
        <template #default="{ row }">{{ SoilTypeText[row.soil_type] }}</template>
      </el-table-column>
      <el-table-column label="日照" width="90">
        <template #default="{ row }">{{ SunlightText[row.sunlight] }}</template>
      </el-table-column>
      <el-table-column label="状态" width="110">
        <template #default="{ row }"><StatusBadge :value="row.status" :meta-map="PlotStatusMeta" /></template>
      </el-table-column>
      <el-table-column label="认养人" width="120">
        <template #default="{ row }">{{ row.adopter?.nickname || row.adopter?.username || '-' }}</template>
      </el-table-column>
      <el-table-column label="协作状态" width="150">
        <template #default="{ row }">
          <template v-if="row.status !== 'available' && row.collaboration">
            <el-tag type="success" size="small" effect="plain">
              成员 {{ row.collaboration.member_count }}/{{ row.collaboration.max_members }}
            </el-tag>
            <el-tag v-if="row.collaboration.pending_invitation_count > 0" type="warning" size="small" effect="plain" style="margin-left: 4px">
              待处理 {{ row.collaboration.pending_invitation_count }}
            </el-tag>
          </template>
          <span v-else class="muted">-</span>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="300">
        <template #default="{ row }">
          <el-button type="info" size="small" link @click="openDetail(row)">详情</el-button>
          <el-button v-if="row.status === 'available'" type="success" size="small" @click="adopt(row)">认养</el-button>
          <el-button v-if="canRelease(row)" type="warning" size="small" @click="release(row)">释放</el-button>
          <el-button v-if="row.status !== 'available'" type="primary" size="small" link @click="goCollab(row)">协作管理</el-button>
        </template>
      </el-table-column>
    </DataTable>

    <!-- 地块详情：含协作状态 -->
    <el-dialog v-model="detailVisible" title="地块详情" width="640px">
      <template v-if="detail">
        <el-descriptions :column="2" border size="small">
          <el-descriptions-item label="编号">{{ detail.code }}</el-descriptions-item>
          <el-descriptions-item label="名称">{{ detail.name }}</el-descriptions-item>
          <el-descriptions-item label="面积">{{ formatArea(detail.area) }}</el-descriptions-item>
          <el-descriptions-item label="状态"><StatusBadge :value="detail.status" :meta-map="PlotStatusMeta" /></el-descriptions-item>
          <el-descriptions-item label="土壤">{{ SoilTypeText[detail.soil_type] }}</el-descriptions-item>
          <el-descriptions-item label="日照">{{ SunlightText[detail.sunlight] }}</el-descriptions-item>
          <el-descriptions-item label="认养人">{{ detail.adopter?.nickname || detail.adopter?.username || '-' }}</el-descriptions-item>
          <el-descriptions-item label="经纬度">{{ detail.latitude.toFixed(4) }}, {{ detail.longitude.toFixed(4) }}</el-descriptions-item>
          <el-descriptions-item label="描述" :span="2">{{ detail.description || '-' }}</el-descriptions-item>
        </el-descriptions>

        <el-divider content-position="left">协作状态</el-divider>
        <template v-if="detail.status !== 'available' && detail.collaboration">
          <div class="collab-summary">
            <el-tag type="success">成员 {{ detail.collaboration.member_count }}/{{ detail.collaboration.max_members }}</el-tag>
            <el-tag type="warning">待处理邀请 {{ detail.collaboration.pending_invitation_count }}</el-tag>
            <el-button type="primary" size="small" @click="goCollab(detail)">进入协作页</el-button>
          </div>
          <el-table :data="detail.collaboration.members" size="small" border style="margin-top: 8px">
            <el-table-column label="居民">
              <template #default="{ row }">{{ row.user?.nickname || row.user?.username || '-' }} <span class="muted">@{{ row.user?.username }}</span></template>
            </el-table-column>
            <el-table-column label="角色" width="110">
              <template #default="{ row }">
                <el-tag size="small" :type="row.role === 'owner' ? 'warning' : 'success'">{{ PlotMemberRoleText[row.role] || row.role }}</el-tag>
              </template>
            </el-table-column>
          </el-table>
        </template>
        <el-alert v-else type="info" :closable="false" title="地块已释放回共享池或尚未认养，暂无协作成员。" />
      </template>
    </el-dialog>

    <el-dialog v-model="createVisible" title="新增地块（管理员）" width="520px">
      <el-form :model="createForm" label-width="90px">
        <el-form-item label="地块名称"><el-input v-model="createForm.name" /></el-form-item>
        <el-form-item label="地块编号"><el-input v-model="createForm.code" placeholder="如 P-007" /></el-form-item>
        <el-form-item label="面积(m²)"><el-input-number v-model="createForm.area" :min="1" /></el-form-item>
        <el-form-item label="土壤类型">
          <el-select v-model="createForm.soil_type">
            <el-option v-for="(t, k) in SoilTypeText" :key="k" :label="t" :value="k" />
          </el-select>
        </el-form-item>
        <el-form-item label="日照条件">
          <el-select v-model="createForm.sunlight">
            <el-option v-for="(t, k) in SunlightText" :key="k" :label="t" :value="k" />
          </el-select>
        </el-form-item>
        <el-form-item label="纬度"><el-input-number v-model="createForm.latitude" :precision="4" :step="0.001" /></el-form-item>
        <el-form-item label="经度"><el-input-number v-model="createForm.longitude" :precision="4" :step="0.001" /></el-form-item>
        <el-form-item label="描述"><el-input v-model="createForm.description" type="textarea" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createVisible = false">取消</el-button>
        <el-button type="primary" :loading="creating" @click="submitCreate">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { usePlotStore } from '@/stores/plot'
import { createPlot, getPlot, type Plot } from '@/api/plot'
import { useAuth } from '@/hooks/useAuth'
import { usePagination } from '@/hooks/usePagination'
import DataTable from '@/components/DataTable.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import { PlotMemberRoleText, PlotStatusMeta, SoilTypeText, SunlightText } from '@/constants'
import { formatArea, clamp } from '@/utils/format'

const router = useRouter()
const store = usePlotStore()
const pagination = usePagination()
const { user, role, isAdmin } = useAuth()

const createVisible = ref(false)
const creating = ref(false)
const createForm = reactive({ name: '', code: '', area: 10, soil_type: 'loam', sunlight: 'full', latitude: 31.2304, longitude: 121.4737, description: '' })

// 地块详情弹窗（含协作状态）
const detailVisible = ref(false)
const detail = ref<Plot | null>(null)

async function openDetail(row: Plot) {
  try {
    detail.value = await getPlot(row.id)
    detailVisible.value = true
  } catch {
    // request 拦截器已统一提示
  }
}

function goCollab(row: Plot) {
  router.push(`/plots/${row.id}/collaboration`)
}

const mapW = 600
const mapH = 360
const viewBoxStr = `0 0 ${mapW} ${mapH}`

function mapX(p: Plot) {
  return clamp(((p.longitude - 121.47) / 0.01) * 1000 + mapW / 2, 20, mapW - 20)
}
function mapY(p: Plot) {
  return clamp(((31.24 - p.latitude) / 0.02) * 1000 + mapH / 2, 20, mapH - 20)
}
function colorOf(status: string) {
  if (status === 'available') return '#67c23a'
  if (status === 'adopted') return '#e6a23c'
  return '#909399'
}

function onPage(page: number) {
  pagination.page.value = page
  fetch()
}

async function fetch() {
  await store.fetchPlots({ page: pagination.page.value, page_size: pagination.size.value })
}

function canRelease(row: Plot) {
  return row.status === 'harvested' && (role.value === 'admin' || row.adopter_id === user.value?.id)
}

async function adopt(row: Plot) {
  try {
    await ElMessageBox.confirm(`确认认养地块 ${row.name}（${row.code}）吗？`, '认养确认', { type: 'success' })
  } catch {
    return
  }
  await store.adopt(row.id)
  ElMessage.success('认养成功，开始你的都市农夫之旅')
}

async function release(row: Plot) {
  try {
    await ElMessageBox.confirm(`确认释放地块 ${row.name} 吗？释放后将重新回到共享池。`, '释放确认', { type: 'warning' })
  } catch {
    return
  }
  const { releasePlot } = await import('@/api/plot')
  await releasePlot(row.id)
  ElMessage.success('地块已释放')
  await fetch()
}

function openCreate() {
  createVisible.value = true
}

async function submitCreate() {
  creating.value = true
  try {
    await createPlot({ ...createForm })
    ElMessage.success('地块创建成功')
    createVisible.value = false
    await fetch()
  } finally {
    creating.value = false
  }
}

onMounted(fetch)
</script>

<style scoped>
.plot-map { width: 100%; height: 360px; border-radius: 8px; }
.muted { color: #909399; font-size: 12px; margin-left: 4px; }
.collab-summary { display: flex; align-items: center; gap: 8px; }
</style>
