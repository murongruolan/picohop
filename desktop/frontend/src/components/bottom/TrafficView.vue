<script setup>
/**
 * 流量统计：曲线（可切节点）+ 指标格 + 按节点排行。
 *
 * 曲线的画法参考了主流客户端的开源实现（refs/local-refs 的
 * Y 轴三档刻度、虚线网格、
 * 底部留出时间轴、**bezier 平滑**（直线段会让峰谷显得很硬）。
 * 我们用 SVG 画，并让画布尺寸跟着容器走（1:1 渲染）——
 * 用 preserveAspectRatio="none" 拉伸会把文字也拉变形。
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
// 空串 = 全部节点（总量）。用户要求图能按节点切 —— 节点多的时候，
// 画成多条彩色曲线既不够颜色也不够看，用一个下拉选一条更清楚。
const pickNode = ref('')

// 范围越长、样本越多，就拉得越稀：为了看 1 小时的曲线每 2 秒搬 3600 个样本不值得。
const POLL_MS = { 60: 2000, 600: 3000, 3600: 10000 }
let timer = null

async function load() {
  try {
    samples.value = (await api.getTrafficHistory(range.value, pickNode.value)) || []
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
watch(pickNode, load)
onMounted(restart)
onBeforeUnmount(() => clearInterval(timer))

// ------------------------------------------------------------------
// 画布尺寸：跟着容器走（1:1，文字不变形）
// ------------------------------------------------------------------
const boxEl = ref(null)
const box = ref({ w: 600, h: 150 })
let ro = null
onMounted(() => {
  ro = new ResizeObserver((entries) => {
    const r = entries[0].contentRect
    box.value = {
      w: Math.max(160, Math.round(r.width)),
      h: Math.max(80, Math.round(r.height)),
    }
  })
  if (boxEl.value) ro.observe(boxEl.value)
})
onBeforeUnmount(() => ro?.disconnect())

const PAD = { top: 8, right: 10, bottom: 18, left: 48 }

/** 轴上的短标签：1.7M / 820K / 0 */
function fmtAxis(v) {
  if (!v) return '0'
  if (v >= 1024 * 1024) return `${(v / 1024 / 1024).toFixed(1)}M`
  if (v >= 1024) return `${Math.round(v / 1024)}K`
  return `${Math.round(v)}B`
}

/** 把上限取整到好看的值（1/2/5 × 10^n），免得刻度是 1.37M 这种 */
function niceMax(v) {
  if (v <= 0) return 1
  const p = Math.pow(10, Math.floor(Math.log10(v)))
  const n = v / p
  const step = n <= 1 ? 1 : n <= 2 ? 2 : n <= 5 ? 5 : 10
  return step * p
}

/**
 * 时间标签：按**实际跨度**选格式，而不是按用户选的时长。
 *
 * 为什么：程序刚启动时只有几十秒数据，即便选了"10 分钟"，四个标签会全是同一个
 * hh:mm（自测截图里就是四个 07:08，等于没标）。跨度小于 2 分钟就用 mm:ss。
 */
function fmtClock(ms, spanMs) {
  const d = new Date(ms || 0)
  const p = (n) => String(n).padStart(2, '0')
  return spanMs < 120000
    ? `${p(d.getMinutes())}:${p(d.getSeconds())}`
    : `${p(d.getHours())}:${p(d.getMinutes())}`
}

const chart = computed(() => {
  const s = samples.value
  const { w, h } = box.value
  const iw = w - PAD.left - PAD.right
  const ih = h - PAD.top - PAD.bottom
  if (s.length < 2 || iw <= 20 || ih <= 20) return null

  let raw = 1
  for (const x of s) raw = Math.max(raw, x.up || 0, x.down || 0)
  const max = niceMax(raw)

  const px = (i) => PAD.left + (i / (s.length - 1)) * iw
  const py = (v) => PAD.top + ih - ((v || 0) / max) * ih

  // Catmull-Rom → 三次贝塞尔：把折线的硬角抹平（主流客户端也是这么做的）
  const smooth = (k) => {
    const pts = s.map((x, i) => [px(i), py(x[k])])
    if (pts.length < 2) return ''
    let d = `M${pts[0][0].toFixed(1)},${pts[0][1].toFixed(1)}`
    for (let i = 0; i < pts.length - 1; i++) {
      const p0 = pts[i - 1] || pts[i]
      const p1 = pts[i]
      const p2 = pts[i + 1]
      const p3 = pts[i + 2] || p2
      const c1x = p1[0] + (p2[0] - p0[0]) / 6
      const c1y = p1[1] + (p2[1] - p0[1]) / 6
      const c2x = p2[0] - (p3[0] - p1[0]) / 6
      const c2y = p2[1] - (p3[1] - p1[1]) / 6
      d +=
        ` C${c1x.toFixed(1)},${c1y.toFixed(1)}` +
        ` ${c2x.toFixed(1)},${c2y.toFixed(1)} ${p2[0].toFixed(1)},${p2[1].toFixed(1)}`
    }
    return d
  }
  const baseline = PAD.top + ih
  const area = (k) =>
    `${smooth(k)} L${(PAD.left + iw).toFixed(1)},${baseline.toFixed(1)} L${PAD.left},${baseline.toFixed(1)} Z`

  const ticks = [0, 0.5, 1].map((f) => ({ y: py(max * f), label: fmtAxis(max * f) }))

  const t0 = s[0].at || 0
  const t1 = s[s.length - 1].at || 0
  const span = Math.max(0, t1 - t0)
  const xLabels = [0, 1 / 3, 2 / 3, 1].map((f) => ({
    x: PAD.left + f * iw,
    label: fmtClock(t0 + span * f, span),
    anchor: f === 0 ? 'start' : f === 1 ? 'end' : 'middle',
  }))

  return {
    w,
    h,
    max,
    upLine: smooth('up'),
    downLine: smooth('down'),
    upArea: area('up'),
    downArea: area('down'),
    ticks,
    xLabels,
  }
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

        <!-- 顺序按用户要求：网速（纵轴上限）→ 节点下拉 → 时间范围 -->
        <span class="cp-traffic-max cp-num">{{ chart ? formatBytes(chart.max) + '/s' : '' }}</span>

        <el-select v-model="pickNode" size="small" class="cp-node-select" placeholder="全部节点">
          <el-option label="全部节点" value="" />
          <el-option v-for="n in store.nodes" :key="n.id" :label="n.name" :value="n.id" />
        </el-select>

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

      <div ref="boxEl" class="cp-chart">
        <svg v-if="chart" :width="chart.w" :height="chart.h" :viewBox="`0 0 ${chart.w} ${chart.h}`">
          <defs>
            <linearGradient id="cpTrafficUp" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stop-color="var(--cp-up)" stop-opacity="0.28" />
              <stop offset="100%" stop-color="var(--cp-up)" stop-opacity="0.02" />
            </linearGradient>
            <linearGradient id="cpTrafficDown" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stop-color="var(--cp-down)" stop-opacity="0.28" />
              <stop offset="100%" stop-color="var(--cp-down)" stop-opacity="0.02" />
            </linearGradient>
          </defs>

          <!-- 网格 + Y 轴刻度 -->
          <g class="cp-grid">
            <line
              v-for="(tk, i) in chart.ticks"
              :key="i"
              :x1="PAD.left"
              :x2="chart.w - PAD.right"
              :y1="tk.y"
              :y2="tk.y"
            />
          </g>
          <g class="cp-axis-y">
            <text v-for="(tk, i) in chart.ticks" :key="i" :x="PAD.left - 8" :y="tk.y + 3.5">
              {{ tk.label }}
            </text>
          </g>

          <path :d="chart.downArea" fill="url(#cpTrafficDown)" stroke="none" />
          <path :d="chart.upArea" fill="url(#cpTrafficUp)" stroke="none" />
          <path :d="chart.downLine" class="cp-line down" />
          <path :d="chart.upLine" class="cp-line up" />

          <!-- X 轴时间 -->
          <g class="cp-axis-x">
            <text
              v-for="(l, i) in chart.xLabels"
              :key="i"
              :x="l.x"
              :y="chart.h - 5"
              :text-anchor="l.anchor"
            >
              {{ l.label }}
            </text>
          </g>
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
  gap: 12px;
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
/* 网速往左挪，给右侧的"节点下拉 + 时间范围"腾位置 */
.cp-traffic-max {
  margin-left: auto;
  color: var(--cp-text-3);
}
.cp-node-select {
  width: 132px;
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
  display: block;
}
.cp-grid line {
  stroke: var(--cp-border);
  stroke-width: 1;
  stroke-dasharray: 3 4;
}
.cp-axis-y text {
  fill: var(--cp-text-3);
  font-size: 10.5px;
  text-anchor: end;
}
.cp-axis-x text {
  fill: var(--cp-text-3);
  font-size: 10.5px;
}
.cp-line {
  fill: none;
  stroke-width: 1.8;
  stroke-linejoin: round;
  stroke-linecap: round;
}
.cp-line.up {
  stroke: var(--cp-up);
}
.cp-line.down {
  stroke: var(--cp-down);
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
