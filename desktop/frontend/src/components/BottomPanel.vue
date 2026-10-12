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
import {ArrowDown, ArrowUp, Search, Delete, CopyDocument} from '@element-plus/icons-vue'
import { useStore } from '../store'
import { useUi } from '../ui'
import api, { notify } from '../api'
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
// 活动节点的本地端口（升序）
const activePorts = computed(() =>
  (store.nodes || [])
    .filter((n) => n.active && n.listenPort)
    .map((n) => n.listenPort)
    .sort((a, b) => a - b),
)

// 状态区里显示成什么。
//
// 【为什么不能一路排下去】用户问："下面展示两个端口，如果我开四个呢、开八个呢，
// 都要显示吗" —— 8 个节点就是 "10810 / 10811 / … / 10817"，既占满状态区，
// 也看不出重点。规则：
//   ≤2 个        → 全列（"10810 / 10811"，最常用的情况，看着最直观）
//   3 个以上连续 → 区间（"10810–10817"：完整且只占几个字）
//   3 个以上不连续 → 首个 + 剩余个数（"10810 +3"，端口是用户随手改的时候会这样）
// 完整列表始终在 tooltip 里，点一下仍然复制全部。
const localPortsText = computed(() => {
  const ps = activePorts.value
  if (!ps.length) return '—'
  if (ps.length <= 2) return ps.join(' / ')
  const contiguous = ps.every((p, i) => i === 0 || p === ps[i - 1] + 1)
  return contiguous ? `${ps[0]}–${ps[ps.length - 1]}` : `${ps[0]} +${ps.length - 1}`
})

// 当前模式：分端口 / 全局代理
const sysModeLabel = computed(() => (store.settings?.systemProxy ? '全局代理' : '分端口'))
const sysModeTip = computed(() =>
  store.settings?.systemProxy
    ? `全局代理模式：系统代理指向全局节点${globalNodeName.value ? `「${globalNodeName.value}」` : ''}`
    : '分端口模式：每个节点各一个本地端口，自己把程序指到某个端口',
)

// 全局节点名（设过才有）
const globalNodeName = computed(() => {
  const id = store.settings?.globalNodeId
  if (!id) return ''
  return (store.nodes || []).find((n) => n.id === id)?.name || ''
})

// 健康判定：**已激活**的节点没在运行（含全局节点）就算异常。
const problems = computed(() => (store.nodes || []).filter((n) => n.active && n.state !== 'running'))
const healthLabel = computed(() => (problems.value.length ? '异常' : '正常'))

// 节点出问题 → 弹一次通知（后端已经把原因写进用户日志，这里只负责"让你知道"）。
// 用 Set 记住已经报过的节点，避免每次刷新都弹一次。
const notifiedBad = new Set()
watch(
  () => (store.nodes || []).map((n) => `${n.id}:${n.state}`).join(','),
  () => {
    for (const n of store.nodes || []) {
      if (n.active && n.state === 'error' && !notifiedBad.has(n.id)) {
        notifiedBad.add(n.id)
        notify('error', `节点「${n.name}」异常`, n.detail || '详见日志', 0)
      }
    }
  },
)

// tooltip 里给完整列表
const localPortsFull = computed(() => activePorts.value.join(' / '))
const frontOk = computed(() => !!store.summary?.frontOk)
const frontDesc = computed(() => store.summary?.frontDesc || '未配置')

function openLogs() {
  view.value = 'logs'
  open.value = true
  persist()
}

function copyPorts() {
  const ps = activePorts.value
  if (!ps.length) return
  // 一行一个可粘贴的地址：本项目其它地方（托盘"复制本地代理地址"）已经是这个格式，
  // 只复制光秃秃的数字没什么用。
  api.copyText(ps.map((p) => `socks5://127.0.0.1:${p}`).join('\n'))
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

// 过滤开关：照主流客户端的过滤栏的约定（Aa = 区分大小写、.* = 按正则）
const regexMode = ref(true)
const caseSensitive = ref(false)

const filteredLogs = computed(() => {
  const lines = sourceLines.value
  const q = filter.value.trim()
  if (!q) return lines
  if (regexMode.value) {
    try {
      return lines.filter((l) => new RegExp(q, caseSensitive.value ? '' : 'i').test(l))
    } catch {
      // 正则写到一半（比如刚敲了 "["）不该弹错误、也不该把列表清空 ——
      // 退回子串匹配，用户接着敲完就恢复成正则。
    }
  }
  const needle = caseSensitive.value ? q : q.toLowerCase()
  return lines.filter((l) => (caseSensitive.value ? l : l.toLowerCase()).includes(needle))
})

// 匹配计数：过滤时显示 "命中/总数"，让用户知道过滤生效了没
const matchInfo = computed(() => {
  if (!filter.value.trim()) return ''
  return `${filteredLogs.value.length}/${sourceLines.value.length}`
})

// 复制**当前显示**的那些行（过滤之后的结果）——
// 用户要的是"把我看到的这段日志发给你"，复制全部反而不是他要的。
function copyShown() {
  const lines = filteredLogs.value
  if (!lines.length) {
    notify('info', '没有可复制的内容', '')
    return
  }
  api.copyText(lines.join('\n'))
  notify('success', '已复制', `${lines.length} 行${filter.value.trim() ? '（当前过滤结果）' : ''}`)
}

// ---- 日志区右键菜单 ----
const logMenu = ref({ show: false, x: 0, y: 0 })

function openLogMenu(e) {
  const box = e.currentTarget.getBoundingClientRect()
  logMenu.value = {
    show: true,
    // 贴在光标处，但不超出日志框
    x: Math.min(e.clientX - box.left, box.width - 130),
    y: Math.min(e.clientY - box.top, Math.max(0, box.height - 78)),
  }
}

function closeLogMenu() {
  logMenu.value.show = false
}

// 全选：把日志框的文本整体选上，用户随后 Ctrl+C 即可
function selectAllLogs() {
  const el = logBox.value
  if (el) {
    const range = document.createRange()
    range.selectNodeContents(el)
    const sel = window.getSelection()
    sel.removeAllRanges()
    sel.addRange(range)
  }
  closeLogMenu()
}

// 复制所选：没选中就复制当前显示的（免得点了没反应）
function copySelected() {
  const sel = String(window.getSelection() || '').trim()
  const text = sel || filteredLogs.value.join('\n')
  if (!text) {
    notify('info', '没有可复制的内容', '')
    closeLogMenu()
    return
  }
  api.copyText(text)
  notify('success', '已复制', sel ? '所选内容' : `${filteredLogs.value.length} 行（未选中，按当前显示复制）`)
  closeLogMenu()
}

// 清除所有：用户日志与网络日志一起清（按钮上的"清除本页"只清当前这一页）
async function clearAllLogs() {
  store.logs = []
  await store.clearNetworkLogs()
  closeLogMenu()
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
        <!-- 模式（用户要求：这一格从"端口列表"改成"现在是什么模式"） -->
        <el-tooltip :content="sysModeTip" placement="top" :show-after="400">
          <span class="cp-bottom-chip" :class="{ 'is-mode-global': store.settings?.systemProxy }">
            {{ sysModeLabel }}
          </span>
        </el-tooltip>

        <!-- 健康（用户要求：这一格从"前置"改成"正常/异常"；
             激活的节点或全局节点出问题 → 红色 + 可点进日志） -->
        <el-tooltip
          :content="
            problems.length
              ? `${problems.length} 个已激活的节点没在运行：${problems.map((n) => n.name).join('、')}（点击看日志）`
              : '所有已激活的节点都在运行'
          "
          placement="top"
          :show-after="400"
        >
          <span
            class="cp-bottom-chip"
            :class="{ 'is-bad': problems.length }"
            @click="problems.length && openLogs()"
          >
            <i class="cp-bottom-dot" />{{ healthLabel }}
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
            <el-input v-model="filter" size="small" placeholder="过滤器（支持正则）">
            </el-input>
            <el-button size="small" :icon="CopyDocument" @click="copyShown">复制</el-button>
            <!-- 两个开关照主流客户端的过滤栏的写法：Aa = 区分大小写、.* = 按正则。
                 原来这里只有一个搜索图标，而它的点击只做了"滚到底" —— 等于没有功能。 -->
            <el-tooltip content="区分大小写" placement="top" :show-after="400">
              <button class="cp-log-flag" :class="{ 'is-on': caseSensitive }" @click="caseSensitive = !caseSensitive">Aa</button>
            </el-tooltip>
            <el-tooltip content="按正则匹配（关闭则按子串）" placement="top" :show-after="400">
              <button class="cp-log-flag" :class="{ 'is-on': regexMode }" @click="regexMode = !regexMode">.*</button>
            </el-tooltip>
            <span v-if="matchInfo" class="cp-log-match cp-num">{{ matchInfo }}</span>
            <el-button size="small" :icon="Delete" @click="clearCurrent">清除本页</el-button>
            <el-tooltip content="自动滚动到末尾" placement="top" :show-after="400">
              <el-switch v-model="autoScroll" size="small" active-text="自动滚动" />
            </el-tooltip>
          </div>

          <div ref="logBox" class="cp-logs-box" @contextmenu.prevent="openLogMenu" @click="closeLogMenu">
            <div v-if="!filteredLogs.length" class="cp-logs-empty">—</div>
            <div v-for="(l, i) in filteredLogs" :key="i" class="cp-logs-line">{{ l }}</div>

            <!-- 右键菜单：全选 / 复制所选 / 清除所有 -->
            <div
              v-if="logMenu.show"
              class="cp-logs-menu"
              :style="{ left: logMenu.x + 'px', top: logMenu.y + 'px' }"
              @click.stop
              @contextmenu.prevent
            >
              <div class="cp-logs-menu-item" @click="selectAllLogs">全选</div>
              <div class="cp-logs-menu-item" @click="copySelected">复制所选</div>
              <div class="cp-logs-menu-item" @click="clearAllLogs">清除所有</div>
            </div>
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
