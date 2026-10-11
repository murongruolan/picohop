<script setup>
/**
 * 流量统计：左侧曲线（上传/下载）+ 右侧指标格与按节点排行。
 *
 * 数据来源：
 *   - 实时速率与今日总量来自 summary（后端每秒采样，见 internal/app/stats.go）；
 *   - 曲线来自 GetTrafficHistory —— 历史留在 Go 侧，因为界面的刷新是去抖合并的，
 *     时间间隔不规则，拿它画曲线没有意义。
 *
 * 文案规则（docs/BOTTOM_PANEL.md）：只放标签与值，不写说明句。
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useStore } from '../../store'
import api from '../../api'
import { formatBytes } from '../../columns'

const store = useStore()

const RANGES = [
  { key: 60, label: '1 分钟' },
  { key: 600, label: '10 分钟' },
  { key: 3600, label: '1 小时' },
]
const range = ref(600)
const samples = ref([])

// 范围越长、样本越多，就拉得越稀：为了看 1 小时的曲线每 2 秒搬 3600 个样本不值得。
const POLL_MS = { 60: 2000, 600: 3000, 3600: 10000 }
let timer = null

async function load() {
  try {
    samples.value = (await api.getTrafficHistory(range.value)) || []
  } catch {
    // 拉不到就保持上一帧，别把曲线清空闪一下
  }
}
function restart() {
  clearInterval(timer)
  load()
  timer = setInterval(load, POLL_MS[range.value] || 3000)
}
watch(range, restart)
onMounted(restart)
onBeforeUnmount(() => clearInterval(timer))

const W = 600
const H = 120

const chart = computed(() => {
  const s = samples.value
  if (s.length < 2) return null
  // 两条线共用同一个上限，否则上下行没法比较
  let max = 1
  for (const x of s) max = Math.max(max, x.up || 0, x.down || 0)
  const px = (i) => (i / (s.length - 1)) * W
  const py = (v) => H - ((v || 0) / max) * H
  const line = (k) => s.map((x, i) => `${i ? 'L' : 'M'}${px(i).toFixed(1)},${py(x[k]).toFixed(1)}`).join(' ')
  const area = (k) => `${line(k)} L${W},${H} L0,${H} Z`
  return { max, upLine: line('up'), downLine: line('down'), upArea: area('up'), downArea: area('down') }
})

const rateText = (v) => `${formatBytes(v || 0)}/s`

const tiles = computed(() => {
  const s = store.summary || {}
  return [
    { k: '上传速度', v: rateText(s.upRate), cls: 'up' },
    { k: '下载速度', v: rateText(s.downRate), cls: 'down' },
    { k: '活跃连接', v: String(s.activeConn || 0) },
    { k: '今日上传', v: formatBytes(s.todayUp || 0) },
    { k: '今日下载', v: formatBytes(s.todayDown || 0) },
    { k: '内存占用', v: `${s.memMB || 0} MB` },
  ]
})

// 按节点流量排行：用今日总量（后端每秒累加，跨重启保留），取前 5 名。
const ranking = computed(() => {
  const rows = (store.nodes || [])
    .map((n) => ({
      id: n.id,
      name: n.name || '—',
      total: (n.todayUp || 0) + (n.todayDown || 0),
    }))
    .filter((r) => r.total > 0)
    .sort((a, b) => b.total - a.total)
  return { rows: rows.slice(0, 5), max: Math.max(1, rows.length ? rows[0].total : 1) }
})
</script>

<template>
  <div class="cp-traffic">
    <div class="cp-traffic-main">
      <div class="cp-traffic-head">
        <span class="cp-legend"><i class="up" />上传<b class="cp-num">{{ rateText(store.summary?.upRate) }}</b></span>
        <span class="cp-legend"><i class="down" />下载<b class="cp-num">{{ rateText(store.summary?.downRate) }}</b></span>
        <el-tooltip content="曲线纵轴上限" placement="top" :show-after="500">
          <span class="cp-num cp-traffic-max">{{ chart ? formatBytes(chart.max) + '/s' : '' }}</span>
        </el-tooltip>
        <div class="cp-ranges">
          <button
            v-for="r in RANGES"
            :key="r.key"
            class="cp-range"
            :class="{ 'is-active': range === r.key }"
            @click="range = r.key"
          >
            {{ r.label }}
          </button>
        </div>
      </div>

      <div class="cp-chart">
        <svg v-if="chart" :viewBox="`0 0 ${W} ${H}`" preserveAspectRatio="none">
          <path :d="chart.downArea" class="area down" />
          <path :d="chart.upArea" class="area up" />
          <path :d="chart.downLine" class="line down" />
          <path :d="chart.upLine" class="line up" />
        </svg>
        <div v-else class="cp-chart-empty">—</div>
      </div>
    </div>

    <div class="cp-traffic-side">
      <div class="cp-tiles">
        <div v-for="t in tiles" :key="t.k" class="cp-tile">
          <span class="cp-tile-k">{{ t.k }}</span>
          <span class="cp-tile-v cp-num" :class="t.cls">{{ t.v }}</span>
        </div>
      </div>

      <div class="cp-rank">
        <div class="cp-rank-title">按节点流量</div>
        <div v-for="r in ranking.rows" :key="r.id" class="cp-rank-row">
          <span class="cp-rank-name">{{ r.name }}</span>
          <span class="cp-rank-bar"><i :style="{ width: (r.total / ranking.max) * 100 + '%' }" /></span>
          <span class="cp-rank-num cp-num">{{ formatBytes(r.total) }}</span>
        </div>
        <div v-if="!ranking.rows.length" class="cp-rank-empty">—</div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.cp-traffic {
  height: 100%;
  display: flex;
  min-height: 0;
}

.cp-traffic-main {
  flex: 1 1 auto;
  min-width: 0;
  display: flex;
  flex-direction: column;
  padding: 8px 10px;
  gap: 6px;
}

.cp-traffic-head {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: 14px;
  font-size: 12px;
  color: var(--cp-text-2);
}
.cp-legend {
  display: inline-flex;
  align-items: center;
  gap: 5px;
}
.cp-legend i {
  width: 8px;
  height: 8px;
  border-radius: 2px;
}
.cp-legend i.up,
.cp-legend i.up + b {
  color: var(--cp-up);
}
.cp-legend i.up {
  background: var(--cp-up);
}
.cp-legend i.down {
  background: var(--cp-down);
}
.cp-legend b {
  font-weight: 600;
  color: var(--cp-text);
}
.cp-traffic-max {
  margin-left: auto;
  color: var(--cp-text-3);
}

.cp-ranges {
  display: flex;
  gap: 2px;
}
.cp-range {
  height: 22px;
  padding: 0 8px;
  border: 0;
  border-radius: var(--cp-radius-sm);
  background: transparent;
  color: var(--cp-text-2);
  font-size: 12px;
  cursor: default;
}
.cp-range:hover {
  background: var(--cp-brand-soft);
  color: var(--cp-brand);
}
.cp-range.is-active {
  background: var(--cp-brand-soft);
  color: var(--cp-brand);
  font-weight: 600;
}

.cp-chart {
  flex: 1 1 auto;
  min-height: 0;
  border: 1px solid var(--cp-border);
  border-radius: var(--cp-radius-sm);
  background: var(--cp-bg);
  overflow: hidden;
}
.cp-chart svg {
  width: 100%;
  height: 100%;
  display: block;
}
.cp-chart .line {
  fill: none;
  stroke-width: 1.5;
  vector-effect: non-scaling-stroke;
}
.cp-chart .line.up {
  stroke: var(--cp-up);
}
.cp-chart .line.down {
  stroke: var(--cp-down);
}
.cp-chart .area {
  stroke: none;
  opacity: 0.16;
}
.cp-chart .area.up {
  fill: var(--cp-up);
}
.cp-chart .area.down {
  fill: var(--cp-down);
}
.cp-chart-empty {
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--cp-text-3);
}

.cp-traffic-side {
  flex: 0 0 33%;
  min-width: 260px;
  max-width: 420px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 8px 10px 8px 0;
  overflow: auto;
}

.cp-tiles {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 6px;
}
.cp-tile {
  display: flex;
  flex-direction: column;
  gap: 1px;
  padding: 4px 8px;
  border: 1px solid var(--cp-border);
  border-radius: var(--cp-radius-sm);
  background: var(--cp-bg);
}
.cp-tile-k {
  font-size: 11.5px;
  color: var(--cp-text-3);
}
.cp-tile-v {
  font-size: 14px;
  font-weight: 600;
  color: var(--cp-text);
}
.cp-tile-v.up {
  color: var(--cp-up);
}
.cp-tile-v.down {
  color: var(--cp-down);
}

.cp-rank {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.cp-rank-title {
  font-size: 11.5px;
  color: var(--cp-text-3);
}
.cp-rank-row {
  display: flex;
  align-items: center;
  gap: 8px;
  height: 18px;
  font-size: 12px;
  color: var(--cp-text-2);
}
.cp-rank-name {
  flex: 0 0 88px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.cp-rank-bar {
  flex: 1 1 auto;
  height: 6px;
  border-radius: 3px;
  background: var(--cp-brand-soft);
  overflow: hidden;
}
.cp-rank-bar i {
  display: block;
  height: 100%;
  background: var(--cp-brand);
}
.cp-rank-num {
  flex: 0 0 auto;
  color: var(--cp-text);
}
.cp-rank-empty {
  color: var(--cp-text-3);
  font-size: 12px;
}
</style>
