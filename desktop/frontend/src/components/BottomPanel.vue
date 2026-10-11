<script setup>
/**
 * 底部栏目：头部常驻 + 可收起 / 可拖高的身体。
 *
 * 为什么头部常驻（见 docs/BOTTOM_PANEL.md）：
 *   - 列表的横向滚动条被抬到这条栏之上 —— 原先它贴着窗口底边，很难按住；
 *   - 底部不再是一大片空白，端口 / 前置状态 / 流量常驻可见。
 *
 * 文案规则（同一份文档）：只放标签与值，解释一律进 tooltip，不写说明句。
 */
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { ArrowDown, ArrowUp, Search, Delete } from '@element-plus/icons-vue'
import { useStore } from '../store'
import { useUi } from '../ui'
import { api } from '../api'
import { formatBytes } from '../columns'
import TrafficView from './bottom/TrafficView.vue'
import SiteTestView from './bottom/SiteTestView.vue'
import ConnectionsView from './bottom/ConnectionsView.vue'
import NetworkView from './bottom/NetworkView.vue'

const store = useStore()
const ui = useUi()

const HEAD_H = 34

const TABS = [
  { key: 'traffic', label: '流量统计' },
  { key: 'network', label: '网络设置' },
  { key: 'logs', label: '日志' },
  { key: 'sites', label: '网站测试' },
  { key: 'connections', label: '连接' },
]

// ---- 布局偏好：跟着配置走（空/0 由这里补默认值）----
const view = ref('logs')
const open = ref(false)
const height = ref(260)

// 【不能在 setup 里直接读 store.settings】
//
// 它是 App.vue 的 onMounted 里 await store.init() 之后才填上的，而本组件在更早的
// 渲染阶段就 setup 了 —— 那时读到的是空对象，于是"上次展开的状态"永远恢复不了。
// 实测踩过：data/config.json 里明明是 bottomPanelOpen=true，重启后却是收起的。
// 这里改成"等设置真的到了再初始化一次"。
let prefReady = false
watch(
  () => store.settings,
  (s) => {
    if (prefReady || !s || !Object.keys(s).length) return
    prefReady = true
    view.value = s.bottomPanelView || 'logs'
    open.value = !!s.bottomPanelOpen
    if (s.bottomPanelHeight > 0) height.value = s.bottomPanelHeight
  },
  { immediate: true },
)

// 只在"动作结束"时写配置：拖拽期间连写会把 SaveSettings 打爆
// （它会重新注册全局快捷键，拖一次能触发上百次）。
function persist() {
  const s = {
    ...store.settings,
    bottomPanelView: view.value,
    bottomPanelHeight: height.value,
    bottomPanelOpen: open.value,
  }
  store.settings = s
  api.saveSettings(s).catch(() => {})
}

function toggle(next) {
  open.value = next === undefined ? !open.value : next
  persist()
}

function pick(key) {
  if (open.value && view.value === key) {
    toggle(false) // 点当前视图 = 收起
    return
  }
  view.value = key
  open.value = true
  persist()
}

// ---- 拖高 ----
let dragStartY = 0
let dragStartH = 0
function onGripDown(e) {
  dragStartY = e.clientY
  dragStartH = height.value
  window.addEventListener('mousemove', onGripMove)
  window.addEventListener('mouseup', onGripUp)
  e.preventDefault()
}
function onGripMove(e) {
  const next = dragStartH - (e.clientY - dragStartY)
  height.value = Math.max(120, Math.min(640, next))
}
function onGripUp() {
  window.removeEventListener('mousemove', onGripMove)
  window.removeEventListener('mouseup', onGripUp)
  persist()
}

// ---- 头部状态区 ----
const localPorts = computed(() =>
  (store.nodes || [])
    .filter((n) => n.active && n.listenPort)
    .map((n) => n.listenPort)
    .join(' / '),
)
const frontOk = computed(() => !!store.summary?.frontOk)
const frontDesc = computed(() => store.summary?.frontDesc || '未配置')

function openLogs() {
  view.value = 'logs'
  open.value = true
  persist()
}

function copyPorts() {
  if (localPorts.value) api.copyText(localPorts.value)
}

// ---- 日志视图 ----
const logTab = ref('user')
const filter = ref('')
const autoScroll = ref(true)
const logBox = ref(null)

// 网络日志按流水格式展示：时间 [节点] 内容（主流客户端那种）。
function fmtNetLine(e) {
  const d = new Date(e.at || 0)
  const p = (n) => String(n).padStart(2, '0')
  const t = `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
  return `${t} [${e.node || '—'}] ${e.text || ''}`
}

// 当前页签对应的原始行
const sourceLines = computed(() =>
  logTab.value === 'net' ? (store.netLogs || []).map(fmtNetLine) : store.logs || [],
)

const filteredLogs = computed(() => {
  const lines = sourceLines.value
  const q = filter.value.trim()
  if (!q) return lines
  try {
    const re = new RegExp(q)
    return lines.filter((l) => re.test(l))
  } catch {
    // 正则写坏了就退化成子串匹配，不弹错误 —— 用户还在打字
    return lines.filter((l) => l.includes(q))
  }
})

function applyFilter() {
  // 回车触发：这里只是把焦点收回，过滤本身是 computed 实时算的
  if (logBox.value) logBox.value.scrollTop = logBox.value.scrollHeight
}

function clearCurrent() {
  if (logTab.value === 'user') store.logs = []
  else store.clearNetworkLogs()
}

watch(
  () => sourceLines.value.length,
  () => {
    if (!autoScroll.value) return
    nextTick(() => {
      if (logBox.value) logBox.value.scrollTop = logBox.value.scrollHeight
    })
  },
)

// 日志视图一露出来就拉一次；网络日志在被查看时按秒增量拉取。
//
// 网络日志走**游标式增量**（见 store.refreshNetworkLogs）：内核每秒可能吐十几条，
// 每次重传整段是不可接受的。
let netTimer = null
watch(
  [open, view, logTab],
  () => {
    const viewing = open.value && view.value === 'logs'
    clearInterval(netTimer)
    netTimer = null
    if (!viewing) return
    if (logTab.value === 'net') {
      store.refreshNetworkLogs()
      netTimer = setInterval(() => store.refreshNetworkLogs(), 1000)
    } else {
      store.refreshLogs()
    }
  },
  { immediate: true },
)
onBeforeUnmount(() => clearInterval(netTimer))

watch(
  () => ui.bottomLogTab,
  (v) => {
    if (!v) return
    logTab.value = v
    ui.bottomLogTab = ''
  },
)

watch(
  () => ui.bottomPanelView,
  (v) => {
    if (v) pick(v)
    ui.bottomPanelView = ''
  },
)
</script>

<template>
  <div class="cp-bottom" :class="{ 'is-open': open }">
    <!-- 拖高把手：只在展开时出现 -->
    <div v-if="open" class="cp-bottom-grip" @mousedown="onGripDown" />

    <div class="cp-bottom-head" :style="{ height: HEAD_H + 'px' }">
      <button class="cp-bottom-chevron" @click="toggle()">
        <el-icon><component :is="open ? ArrowDown : ArrowUp" /></el-icon>
        <span>{{ open ? '收起' : '展开' }}</span>
      </button>

      <div class="cp-bottom-tabs">
        <button
          v-for="t in TABS"
          :key="t.key"
          class="cp-bottom-tab"
          :class="{ 'is-active': open && view === t.key }"
          @click="pick(t.key)"
        >
          {{ t.label }}
        </button>
      </div>

      <div class="cp-bottom-status">
        <el-tooltip :content="localPorts ? `本地端口：${localPorts}（点击复制）` : '没有运行中的节点'" placement="top" :show-after="400">
          <span class="cp-bottom-chip cp-num" :class="{ 'is-dim': !localPorts }" @click="copyPorts">
            {{ localPorts || '—' }}
          </span>
        </el-tooltip>

        <el-tooltip :content="`前置：${frontDesc}`" placement="top" :show-after="400">
          <span class="cp-bottom-chip" :class="{ 'is-off': !frontOk }">
            <i class="cp-bottom-dot" />前置
          </span>
        </el-tooltip>

        <el-tooltip
          :content="`实时速率 · 今日共 ${store.summary?.todayText || '0 KB'}`"
          placement="top"
          :show-after="400"
        >
          <span class="cp-bottom-chip cp-num">
            <span class="cp-up">↑ {{ formatBytes(store.summary?.upRate || 0) }}</span>
            <span class="cp-down">↓ {{ formatBytes(store.summary?.downRate || 0) }}</span>
          </span>
        </el-tooltip>

        <el-tooltip content="活动连接" placement="top" :show-after="400">
          <span class="cp-bottom-chip cp-num">{{ store.summary?.activeConn || 0 }}</span>
        </el-tooltip>

        <!-- 错误徽标：只在真有节点出错时出现（平时不占视线）。
             数字是"启动失败的节点数"；点一下跳到日志（那里有具体原因）。 -->
        <el-tooltip
          v-if="store.summary?.error"
          :content="`${store.summary.error} 个节点启动失败，点击看日志`"
          placement="top"
          :show-after="400"
        >
          <span class="cp-bottom-chip cp-bottom-error" @click="openLogs">错误 {{ store.summary.error }}</span>
        </el-tooltip>
      </div>
    </div>

    <div v-if="open" class="cp-bottom-body" :style="{ height: height + 'px' }">
      <!-- 日志 -->
      <div v-if="view === 'logs'" class="cp-logs">
        <div class="cp-logs-side">
          <button class="cp-logs-tab" :class="{ 'is-active': logTab === 'user' }" @click="logTab = 'user'">
            用户日志
          </button>
          <button class="cp-logs-tab" :class="{ 'is-active': logTab === 'net' }" @click="logTab = 'net'">
            网络日志
          </button>
        </div>

        <div class="cp-logs-main">
          <div class="cp-logs-bar">
            <el-input
              v-model="filter"
              size="small"
              placeholder="过滤器（支持正则）"
              @keyup.enter="applyFilter"
            >
              <template #suffix>
                <el-icon class="cp-logs-search" @click="applyFilter"><Search /></el-icon>
              </template>
            </el-input>
            <el-button size="small" :icon="Delete" @click="clearCurrent">清除本项</el-button>
            <el-tooltip content="自动滚动到末尾" placement="top" :show-after="400">
              <el-switch v-model="autoScroll" size="small" active-text="自动滚动" />
            </el-tooltip>
          </div>

          <div ref="logBox" class="cp-logs-box">
            <div v-if="!filteredLogs.length" class="cp-logs-empty">—</div>
            <div v-for="(l, i) in filteredLogs" :key="i" class="cp-logs-line">{{ l }}</div>
          </div>
        </div>
      </div>

      <!-- 流量统计 -->
      <TrafficView v-else-if="view === 'traffic'" />

      <!-- 网站测试 -->
      <SiteTestView v-else-if="view === 'sites'" />

      <!-- 连接 -->
      <ConnectionsView v-else-if="view === 'connections'" />

      <!-- 网络设置 -->
      <NetworkView v-else-if="view === 'network'" />

      <!-- 其余视图：骨架先占位，按施工顺序逐个补 -->
      <div v-else class="cp-bottom-placeholder">{{ TABS.find((t) => t.key === view)?.label }}</div>
    </div>
  </div>
</template>
