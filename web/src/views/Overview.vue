<script setup lang="ts">
import LoadingContent from '../components/LoadingContent.vue'
import DataList from '../components/DataList.vue'
import { computed, onMounted, onUnmounted, ref, watch, nextTick } from 'vue'
import { usePanel } from '../stores/panel'
import {
  Connection,
  Coin,
  Box,
  ArrowRight,
  Plus,
  CircleCheck,
  Warning,
  Upload,
  Download,
  Clock,
} from '@element-plus/icons-vue'
import { size, date } from '../lib/api'
import StatusBadge from '../components/StatusBadge.vue'
import { resolvedTheme } from '../lib/appearance'
import type * as ECharts from 'echarts/core'
let echarts: Pick<typeof ECharts, 'init' | 'graphic'>
let alive = true
const panel = usePanel(),
  graph = ref<HTMLElement>(),
  interval = ref('实时'),
  samples = ref<{ time: string; cpu: number; memory: number }[]>([])
let chart: ECharts.ECharts
const overview = computed(() => panel.overview)
const healthy = computed(() => panel.resources.filter((r) => r.status === 'ready').length)
const cards = computed(() => [
  {
    title: 'CPU 使用率',
    value: (overview.value?.cpu || 0).toFixed(1),
    unit: '%',
    meta: `${overview.value?.cpuCores || '—'} 核心处理器`,
    color: 'var(--el-color-primary)',
    usage: overview.value?.cpu || 0,
  },
  {
    title: '内存使用率',
    value: (overview.value?.memory?.usedPercent || 0).toFixed(1),
    unit: '%',
    meta: `${size(overview.value?.memory?.used)} / ${size(overview.value?.memory?.total)}`,
    color: 'var(--ops-chart-secondary)',
    usage: overview.value?.memory?.usedPercent || 0,
  },
  {
    title: '磁盘使用率',
    value: (overview.value?.disk?.usedPercent || 0).toFixed(1),
    unit: '%',
    meta: `${size(overview.value?.disk?.used)} / ${size(overview.value?.disk?.total)}`,
    color: 'var(--el-color-warning)',
    usage: overview.value?.disk?.usedPercent || 0,
  },
])
const shortcuts = computed(() => [
  {
    label: '网站',
    count: panel.kind('site').length,
    icon: Connection,
    route: '/sites',
    color: 'green',
  },
  {
    label: '数据库',
    count: panel.kind('database').length,
    icon: Coin,
    route: '/databases',
    color: 'blue',
  },
  {
    label: '软件实例',
    count: panel.kind('instance').length,
    icon: Box,
    route: '/software',
    color: 'amber',
  },
  {
    label: 'Compose 项目',
    count: panel.kind('compose').length,
    icon: Box,
    route: '/docker',
    color: 'purple',
  },
])
const net = computed(() => overview.value?.network?.[0])
function draw() {
  if (!chart) return
  const color = (name: string) => resolvedTheme.value.colors[name]!
  chart.setOption({
    animationDuration: 250,
    color: [color('--el-color-primary'), color('--ops-chart-secondary')],
    grid: { left: 8, right: 18, top: 22, bottom: 6, containLabel: true },
    tooltip: {
      trigger: 'axis',
      backgroundColor: color('--ops-raised'),
      textStyle: { color: color('--ops-text'), fontSize: 12 },
      borderWidth: 1,
      borderColor: color('--ops-border'),
      valueFormatter: (v: unknown) => `${Number(v).toFixed(1)}%`,
    },
    xAxis: {
      type: 'category',
      boundaryGap: false,
      data: samples.value.map((v) => v.time),
      axisLine: { show: false },
      axisTick: { show: false },
      axisLabel: { color: color('--ops-text-muted'), fontFamily: 'monospace', fontSize: 10 },
    },
    yAxis: {
      type: 'value',
      min: 0,
      max: 100,
      splitNumber: 4,
      axisLabel: { color: color('--ops-text-muted'), formatter: '{value}%', fontSize: 10 },
      splitLine: { lineStyle: { color: color('--ops-border-soft'), type: 'dashed' } },
    },
    series: [
      {
        name: 'CPU',
        type: 'line',
        smooth: true,
        showSymbol: samples.value.length < 2,
        lineStyle: { width: 2 },
        areaStyle: {
          color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [
            { offset: 0, color: color('--el-color-primary') + '30' },
            { offset: 1, color: color('--el-color-primary') + '00' },
          ]),
        },
        data: samples.value.map((v) => v.cpu),
      },
      {
        name: '内存',
        type: 'line',
        smooth: true,
        showSymbol: samples.value.length < 2,
        lineStyle: { width: 2, type: 'dashed' },
        data: samples.value.map((v) => v.memory),
      },
    ],
  })
}
watch(
  overview,
  (v) => {
    if (!v) return
    samples.value.push({
      time: new Date(v.timestamp).toLocaleTimeString('zh-CN', { hour12: false }),
      cpu: v.cpu,
      memory: v.memory?.usedPercent || 0,
    })
    if (samples.value.length > 60) samples.value.shift()
    draw()
  },
  { immediate: true },
)
function resize() {
  chart?.resize()
}
async function mountChart() {
  if (!echarts || !alive || !graph.value || chart) return
  chart = echarts.init(graph.value)
  draw()
}
watch(graph, () => {
  void nextTick(mountChart)
})
watch(resolvedTheme, draw, { flush: 'post' })
onMounted(async () => {
  echarts = await import('../lib/chart')
  await mountChart()
  if (alive) window.addEventListener('resize', resize)
})
onUnmounted(() => {
  alive = false
  chart?.dispose()
  window.removeEventListener('resize', resize)
})
</script>
<template>
  <LoadingContent :busy="panel.pending.overview">
    <div class="metrics-grid">
      <article v-for="card in cards" :key="card.title" class="metric-card">
        <div class="metric-title">
          {{ card.title }}
          <span class="metric-chip">实时</span>
        </div>
        <div class="metric-value">
          {{ card.value }}
          <span>{{ card.unit }}</span>
        </div>
        <div class="meter">
          <i :style="{ width: Math.min(card.usage, 100) + '%', background: card.color }"></i>
        </div>
        <div class="metric-meta">
          {{ card.meta }}
          <span :style="{ color: card.color }">{{ card.usage > 85 ? '负载较高' : '正常' }}</span>
        </div>
      </article>
      <article class="metric-card network-card">
        <div class="metric-title">
          网络累计流量
          <el-icon><Connection /></el-icon>
        </div>
        <div class="network-row">
          <span>
            <el-icon><Upload /></el-icon>
            发送
          </span>
          <strong>{{ size(net?.bytesSent) }}</strong>
        </div>
        <div class="network-row">
          <span>
            <el-icon><Download /></el-icon>
            接收
          </span>
          <strong>{{ size(net?.bytesRecv) }}</strong>
        </div>
        <div class="metric-meta">
          自系统启动以来
          <span>全接口汇总</span>
        </div>
      </article>
    </div>
    <div class="dashboard-middle">
      <section class="surface chart-panel">
        <div class="section-heading">
          <h3>
            资源趋势
            <span class="live-label">
              <i></i>
              LIVE
            </span>
          </h3>
          <el-select v-model="interval" size="small" style="width: 100px">
            <el-option label="实时" value="实时" />
          </el-select>
        </div>
        <div class="chart-legend">
          <span>
            <i></i>
            CPU 使用率
          </span>
          <span>
            <i></i>
            内存使用率
          </span>
          <small>当前会话</small>
        </div>
        <div ref="graph" class="resource-chart"></div>
      </section>
      <section class="surface quick-panel">
        <div class="section-heading">
          <h3>快捷操作</h3>
          <span class="section-caption">常用入口</span>
        </div>
        <router-link to="/sites" class="quick-action">
          <span class="quick-icon green"><Plus /></span>
          <div>
            <strong>创建网站</strong>
            <small>静态站点或反向代理</small>
          </div>
          <el-icon><ArrowRight /></el-icon>
        </router-link>
        <router-link to="/software" class="quick-action">
          <span class="quick-icon blue"><Box /></span>
          <div>
            <strong>安装运行环境</strong>
            <small>Nginx / MySQL / PostgreSQL</small>
          </div>
          <el-icon><ArrowRight /></el-icon>
        </router-link>
        <router-link to="/backups" class="quick-action">
          <span class="quick-icon amber"><Clock /></span>
          <div>
            <strong>配置自动备份</strong>
            <small>为重要数据多一份保障</small>
          </div>
          <el-icon><ArrowRight /></el-icon>
        </router-link>
      </section>
    </div>
    <div class="resource-shortcuts">
      <router-link
        v-for="item in shortcuts"
        :key="item.label"
        :to="item.route"
        class="surface resource-shortcut"
      >
        <span class="quick-icon" :class="item.color"><component :is="item.icon" /></span>
        <div>
          <span>{{ item.label }}</span>
          <strong>
            {{ item.count }}
            <small>个</small>
          </strong>
        </div>
        <el-icon><ArrowRight /></el-icon>
      </router-link>
    </div>
    <div class="dashboard-bottom">
      <section class="surface recent-tasks">
        <div class="section-heading">
          <h3>最近任务</h3>
          <router-link to="/tasks" class="text-link">
            查看全部
            <el-icon><ArrowRight /></el-icon>
          </router-link>
        </div>
        <DataList
          state-key="Overview-1"
          v-if="panel.tasks.length"
          :rows="panel.tasks.slice(0, 5)"
          style="width: 100%"
        >
          <el-table-column label="操作">
            <template #default="{ row }">
              <span class="task-operation">{{ row.operation }}</span>
              <small class="cell-subtext">{{ row.id.slice(0, 12) }}</small>
            </template>
          </el-table-column>
          <el-table-column label="状态" width="100">
            <template #default="{ row }"><StatusBadge :value="row.state" /></template>
          </el-table-column>
          <el-table-column label="时间" width="170">
            <template #default="{ row }">
              <span class="cell-date">{{ date(row.createdAt) }}</span>
            </template>
          </el-table-column>
        </DataList>
        <div v-else class="compact-empty">
          <span class="empty-symbol"><Clock /></span>
          <strong>还没有执行任务</strong>
          <p>创建站点、安装软件或运行备份后，操作记录会显示在这里。</p>
          <router-link to="/software" class="text-link">
            安装第一个软件
            <span>→</span>
          </router-link>
        </div>
      </section>
      <section class="surface health-panel">
        <div class="section-heading">
          <h3>运行提醒</h3>
          <span class="count-badge">{{ overview?.failedTasks || 0 }}</span>
        </div>
        <div class="health-summary">
          <span :class="['health-symbol', { warning: overview?.failedTasks }]">
            <el-icon>
              <Warning v-if="overview?.failedTasks" />
              <CircleCheck v-else />
            </el-icon>
          </span>
          <strong>{{ overview?.failedTasks ? '有任务需要关注' : '当前没有失败任务' }}</strong>
          <p>
            {{ overview?.failedTasks ? '查看任务日志，定位并处理执行异常。' : '' }}
          </p>
        </div>
        <div class="health-line">
          <span>已应用的资源</span>
          <strong>{{ healthy }} / {{ panel.resources.length }}</strong>
        </div>
        <div class="health-line">
          <span>等待或运行中的任务</span>
          <strong>{{ overview?.runningTasks || 0 }}</strong>
        </div>
        <router-link to="/tasks" class="text-link health-link">
          前往任务中心
          <el-icon><ArrowRight /></el-icon>
        </router-link>
      </section>
    </div>
  </LoadingContent>
</template>
