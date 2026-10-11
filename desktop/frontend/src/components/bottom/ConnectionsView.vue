<script setup>
/**
 * 连接：活动 / 已关闭，过滤、关闭全部、连接表。
 *
 * 数据来自我们自己的 handler（连接生命周期事件），不是 sing-box 的 内核自带的 RESTful API ——
 * 原因见 internal/proxy/connlog.go 的注释：我们的 sing-box 绕过了路由器，
 * 它的连接列表里永远不会有东西。
 *
 * 文案规则（docs/BOTTOM_PANEL.md）：只放标签与值，不写说明句。
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useStore } from '../../store'
import api, { notify } from '../../api'
import { formatBytes } from '../../columns'

const store = useStore()

const tab = ref('active') // active / closed
const filter = ref('')
const rows = ref([])
const busy = ref(false)

async function load() {
  try {
    rows.value = (await api.getConnections()) || []
  } catch {
    /* 拉不到保持上一帧 */
  }
}

// 只在视图可见时轮询；连接变化很快，1 秒足够
let timer = null
function restart() {
  clearInterval(timer)
  load()
  timer = setInterval(load, 1000)
}
onMounted(restart)
onBeforeUnmount(() => clearInterval(timer))

const activeCount = computed(() => rows.value.filter((r) => r.state === 'active').length)
const closedCount = computed(() => rows.value.filter((r) => r.state === 'closed').length)

const shown = computed(() => {
  const q = filter.value.trim().toLowerCase()
  return rows.value
    .filter((r) => r.state === tab.value)
    .filter((r) => {
      if (!q) return true
      return (
        (r.target || '').toLowerCase().includes(q) ||
        (r.node || '').toLowerCase().includes(q) ||
        (r.protocol || '').toLowerCase().includes(q) ||
        String(r.srcPort || '').includes(q)
      )
    })
    .sort((a, b) => b.startAt - a.startAt)
})

function fmtDuration(ms) {
  if (!ms || ms < 0) return '—'
  const s = Math.floor(ms / 1000)
  if (s < 60) return `${(ms / 1000).toFixed(1)}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m${String(s % 60).padStart(2, '0')}s`
  return `${Math.floor(m / 60)}h${String(m % 60).padStart(2, '0')}m`
}

async function closeAll() {
  busy.value = true
  try {
    const n = await api.closeAllConnections()
    notify('success', '连接', `已关闭 ${n} 条`)
    await load()
  } catch (e) {
    notify('error', '关闭连接失败', String(e?.message || e), 0)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="cp-conn">
    <div class="cp-conn-bar">
      <div class="cp-conn-tabs">
        <button class="cp-conn-tab" :class="{ 'is-active': tab === 'active' }" @click="tab = 'active'">
          活跃 <b>{{ activeCount }}</b>
        </button>
        <button class="cp-conn-tab" :class="{ 'is-active': tab === 'closed' }" @click="tab = 'closed'">
          已关闭 <b>{{ closedCount }}</b>
        </button>
      </div>
      <el-input v-model="filter" size="small" class="cp-conn-filter" placeholder="过滤" clearable />
      <el-button size="small" :loading="busy" :disabled="!activeCount" @click="closeAll">关闭全部</el-button>
    </div>

    <div class="cp-conn-head">
      <span class="cp-c-host">主机</span>
      <span class="cp-c-proto">协议</span>
      <span class="cp-c-node">节点</span>
      <span class="cp-c-port">源端口</span>
      <span class="cp-c-time">时长</span>
      <span class="cp-c-num">上传</span>
      <span class="cp-c-num">下载</span>
    </div>

    <div class="cp-conn-body">
      <div v-if="!shown.length" class="cp-conn-empty">—</div>
      <div v-for="r in shown" :key="r.id" class="cp-conn-row">
        <span class="cp-c-host cp-ellipsis" :title="r.target">{{ r.target }}</span>
        <span class="cp-c-proto">{{ r.protocol }}</span>
        <span class="cp-c-node cp-ellipsis">{{ r.node }}</span>
        <span class="cp-c-port cp-num">{{ r.srcPort || '—' }}</span>
        <span class="cp-c-time cp-num">{{ fmtDuration(r.duration) }}</span>
        <span class="cp-c-num cp-num">{{ formatBytes(r.up || 0) }}</span>
        <span class="cp-c-num cp-num">{{ formatBytes(r.down || 0) }}</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.cp-conn {
  height: 100%;
  display: flex;
  flex-direction: column;
  min-height: 0;
  padding: 8px 10px;
  gap: 6px;
}

.cp-conn-bar {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: 10px;
}
.cp-conn-tabs {
  display: flex;
  gap: 2px;
}
.cp-conn-tab {
  height: 24px;
  padding: 0 10px;
  border: 0;
  border-radius: var(--cp-radius-sm);
  background: transparent;
  color: var(--cp-text-2);
  font-size: 12.5px;
  cursor: default;
}
.cp-conn-tab:hover {
  background: var(--cp-brand-soft);
  color: var(--cp-brand);
}
.cp-conn-tab.is-active {
  background: var(--cp-brand-soft);
  color: var(--cp-brand);
  font-weight: 600;
}
.cp-conn-filter {
  flex: 0 1 220px;
  min-width: 120px;
}

.cp-conn-head,
.cp-conn-row {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
}
.cp-conn-head {
  flex: 0 0 auto;
  color: var(--cp-text-3);
  padding-bottom: 4px;
  border-bottom: 1px solid var(--cp-border);
}
.cp-conn-body {
  flex: 1 1 auto;
  min-height: 0;
  overflow: auto;
}
.cp-conn-row {
  height: 22px;
  color: var(--cp-text-2);
}
.cp-conn-row:hover {
  background: var(--cp-brand-soft);
}
.cp-conn-empty {
  color: var(--cp-text-3);
  font-size: 12px;
  padding-top: 4px;
}

.cp-c-host {
  flex: 1 1 auto;
  min-width: 0;
}
.cp-c-proto {
  flex: 0 0 58px;
}
.cp-c-node {
  flex: 0 0 96px;
}
.cp-c-port {
  flex: 0 0 62px;
  text-align: right;
}
.cp-c-time {
  flex: 0 0 76px;
  text-align: right;
}
.cp-c-num {
  flex: 0 0 74px;
  text-align: right;
}
</style>
