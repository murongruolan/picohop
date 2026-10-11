<script setup>
/**
 * 网站测试：左侧网站卡片（单个/全部/自定义），右侧「我的 IP 信息」。
 *
 * 数据来源：
 *   - 站点测试走各节点的本地监听端口（和浏览器走的是同一条路），
 *     后端返回的是"站点 × 节点"的结果 —— 所以列表与矩阵是同一份数据的两种展示；
 *   - IP 信息查的是**本机直连 IP**（后端显式绕过代理），不是节点出口 IP。
 *
 * 文案规则（docs/BOTTOM_PANEL.md）：只放标签与值，不写说明句。
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
import { useStore } from '../../store'
import api, { notify } from '../../api'

const store = useStore()

const DEFAULT_SITES = [
  { name: 'Apple', url: 'https://www.apple.com' },
  { name: 'GitHub', url: 'https://www.github.com' },
  { name: 'Google', url: 'https://www.google.com' },
  { name: 'YouTube', url: 'https://www.youtube.com' },
]

const LS_SITES = 'cp_site_test_sites'
const LS_MATRIX = 'cp_site_test_matrix'

function loadSites() {
  try {
    const raw = localStorage.getItem(LS_SITES)
    if (raw) {
      const arr = JSON.parse(raw)
      if (Array.isArray(arr) && arr.length) return arr
    }
  } catch {
    /* 坏数据就当没存过 */
  }
  return DEFAULT_SITES.slice()
}

const sites = ref(loadSites())
const results = ref([])
const testing = ref(false)
const matrix = ref(localStorage.getItem(LS_MATRIX) === '1')
const addName = ref('')
const addURL = ref('')

const running = computed(() => (store.nodes || []).filter((n) => n.state === 'running'))
const pickNode = ref('')
watch(
  running,
  (list) => {
    if (!pickNode.value && list.length) pickNode.value = list[0].id
  },
  { immediate: true },
)

async function run(nodeIDs) {
  if (testing.value) return
  testing.value = true
  try {
    const ids = nodeIDs || (matrix.value ? running.value.map((n) => n.id) : [pickNode.value].filter(Boolean))
    if (!ids.length) {
      notify('warning', '网站测试', '没有正在运行的节点')
      return
    }
    results.value = (await api.testSites(sites.value.map((s) => s.url), ids)) || []
  } catch (e) {
    notify('error', '网站测试失败', String(e?.message || e), 0)
  } finally {
    testing.value = false
  }
}

// 某个站点在某个节点上的结果
// 开关由 el-switch 自己改 v-model，这里只负责持久化与清掉旧结果。
// （曾经在 @change 里再翻转一次，与 v-model 抵消 —— 开关看着会弹回去。）
watch(matrix, (v) => {
  localStorage.setItem(LS_MATRIX, v ? '1' : '0')
  results.value = []
})

function resultOf(url, nodeId) {
  return results.value.find((r) => r.url === url && r.nodeId === nodeId)
}

function addSite() {
  const name = addName.value.trim()
  let url = addURL.value.trim()
  if (!name || !url) return
  if (!/^https?:\/\//i.test(url)) url = 'https://' + url
  if (sites.value.some((s) => s.url === url)) {
    notify('warning', '网站测试', '这个站点已经在列表里了')
    return
  }
  sites.value = sites.value.concat([{ name, url, custom: true }])
  localStorage.setItem(LS_SITES, JSON.stringify(sites.value))
  addName.value = ''
  addURL.value = ''
}

function removeSite(url) {
  sites.value = sites.value.filter((s) => s.url !== url)
  localStorage.setItem(LS_SITES, JSON.stringify(sites.value))
  results.value = results.value.filter((r) => r.url !== url)
}

// ------------------------------------------------------------------
// 我的 IP 信息
// ------------------------------------------------------------------
const info = ref(null)
const loadingInfo = ref(false)
const masked = ref(true)
const REFRESH_SEC = 300
const countdown = ref(REFRESH_SEC)
let tick = null

async function loadInfo() {
  if (loadingInfo.value) return
  loadingInfo.value = true
  try {
    info.value = await api.getIPInfo()
  } catch (e) {
    info.value = { err: String(e?.message || e) }
  } finally {
    loadingInfo.value = false
    countdown.value = REFRESH_SEC
  }
}

// 国名本地化：用 Intl.DisplayNames 把 ISO 码转成中文国名 ——
// 这样就不必在代码里硬编码一张"国家码 → 国名"的映射表（那种表一定会过期）。
const countryName = computed(() => {
  const code = info.value?.country
  if (!code) return ''
  try {
    return new Intl.DisplayNames(['zh-CN'], { type: 'region' }).of(code) || code
  } catch {
    return code
  }
})

const maskedIP = computed(() => {
  const ip = info.value?.ip || ''
  if (!masked.value) return ip || '—'
  if (!ip) return '—'
  if (ip.includes(':')) return ip.split(':').slice(0, 2).join(':') + ':••••'
  const p = ip.split('.')
  return p.length === 4 ? `${p[0]}.${p[1]}.•••.•••` : '••••••'
})

onMounted(() => {
  loadInfo()
  tick = setInterval(() => {
    countdown.value -= 1
    if (countdown.value <= 0) loadInfo()
  }, 1000)
})
onBeforeUnmount(() => clearInterval(tick))
</script>

<template>
  <div class="cp-sites">
    <div class="cp-sites-main">
      <div class="cp-sites-bar">
        <el-select v-model="pickNode" size="small" style="width: 160px" :disabled="matrix">
          <el-option v-for="n in running" :key="n.id" :label="n.name" :value="n.id" />
        </el-select>
        <el-switch v-model="matrix" size="small" active-text="矩阵" />
        <el-button size="small" type="primary" plain :loading="testing" @click="run()">全部测试</el-button>
        <span class="cp-dim">{{ results.length ? `${results.length} 条结果` : '' }}</span>
      </div>

      <!-- 矩阵：行=站点，列=节点 -->
      <div v-if="matrix" class="cp-matrix">
        <div class="cp-matrix-row cp-matrix-head">
          <span class="cp-matrix-cell">站点</span>
          <span v-for="n in running" :key="n.id" class="cp-matrix-cell cp-ellipsis">{{ n.name }}</span>
        </div>
        <div v-for="s in sites" :key="s.url" class="cp-matrix-row">
          <span class="cp-matrix-cell cp-ellipsis">{{ s.name }}</span>
          <span
            v-for="n in running"
            :key="n.id"
            class="cp-matrix-cell cp-num"
            :class="{
              ok: resultOf(s.url, n.id)?.ok,
              bad: resultOf(s.url, n.id) && !resultOf(s.url, n.id).ok,
            }"
          >
            {{ resultOf(s.url, n.id) ? (resultOf(s.url, n.id).ok ? resultOf(s.url, n.id).ms + 'ms' : '×') : '—' }}
          </span>
        </div>
      </div>

      <!-- 列表：每个站点一张卡片 -->
      <div v-else class="cp-cards">
        <div v-for="s in sites" :key="s.url" class="cp-card">
          <div class="cp-card-name cp-ellipsis">{{ s.name }}</div>
          <div class="cp-card-url cp-ellipsis">{{ s.url.replace(/^https?:\/\//, '') }}</div>
          <div class="cp-card-foot">
            <span
              class="cp-card-res cp-num"
              :class="{ ok: resultOf(s.url, pickNode)?.ok, bad: resultOf(s.url, pickNode) && !resultOf(s.url, pickNode).ok }"
            >
              <template v-if="resultOf(s.url, pickNode)">
                {{ resultOf(s.url, pickNode).ok ? resultOf(s.url, pickNode).ms + ' ms' : resultOf(s.url, pickNode).err || '失败' }}
              </template>
              <template v-else>—</template>
            </span>
            <el-button size="small" text @click="run([pickNode])">测试</el-button>
            <el-button v-if="s.custom" size="small" text @click="removeSite(s.url)">删除</el-button>
          </div>
        </div>

        <div class="cp-card cp-card-add">
          <el-input v-model="addName" size="small" placeholder="名称" />
          <el-input v-model="addURL" size="small" placeholder="网址" @keyup.enter="addSite" />
          <el-button size="small" text @click="addSite">添加</el-button>
        </div>
      </div>
    </div>

    <div class="cp-ipinfo">
      <div class="cp-ip-head">
        <span class="cp-ip-title">我的 IP 信息</span>
        <el-tooltip content="刷新" placement="top" :show-after="400">
          <el-button size="small" text :icon="Refresh" :loading="loadingInfo" @click="loadInfo" />
        </el-tooltip>
      </div>

      <div v-if="info?.err" class="cp-ip-err">{{ info.err }}</div>

      <template v-else-if="info">
        <div class="cp-ip-line">
          <span class="cp-country">{{ info.country || '—' }}</span>
          <span class="cp-ellipsis">{{ countryName || '—' }}</span>
        </div>
        <div class="cp-ip-line">
          <span class="cp-ip-label">IP</span>
          <span class="cp-num cp-ellipsis">{{ maskedIP }}</span>
          <el-tooltip :content="masked ? '显示完整 IP' : '脱敏显示'" placement="top" :show-after="400">
            <button class="cp-eye" @click="masked = !masked">{{ masked ? '👁' : '🙈' }}</button>
          </el-tooltip>
        </div>
        <div class="cp-ip-line"><span class="cp-ip-label">位置</span><span class="cp-ellipsis">{{ [info.city, info.region].filter(Boolean).join(' · ') || '—' }}</span></div>
        <div class="cp-ip-line"><span class="cp-ip-label">组织</span><span class="cp-ellipsis">{{ info.org || '—' }}</span></div>
        <div class="cp-ip-line"><span class="cp-ip-label">自治域</span><span class="cp-num">{{ info.asn || '—' }}</span></div>
        <div class="cp-ip-line"><span class="cp-ip-label">时区</span><span class="cp-ellipsis">{{ info.timezone || '—' }}</span></div>
        <div class="cp-ip-line"><span class="cp-ip-label">经纬度</span><span class="cp-num">{{ info.lat && info.lon ? `${info.lat}, ${info.lon}` : '—' }}</span></div>
        <div class="cp-ip-foot">
          <span class="cp-dim">{{ countdown }}s 后刷新</span>
        </div>
      </template>

      <div v-else class="cp-ip-foot cp-dim">—</div>
    </div>
  </div>
</template>

<style scoped>
.cp-sites {
  height: 100%;
  display: flex;
  min-height: 0;
}

.cp-sites-main {
  flex: 1 1 auto;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 8px 10px;
  min-height: 0;
}

.cp-sites-bar {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 12px;
  color: var(--cp-text-2);
}

.cp-cards {
  flex: 1 1 auto;
  min-height: 0;
  overflow: auto;
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
  gap: 8px;
  align-content: start;
}
.cp-card {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 6px 8px;
  border: 1px solid var(--cp-border);
  border-radius: var(--cp-radius-sm);
  background: var(--cp-bg);
}
.cp-card-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--cp-text);
}
.cp-card-url {
  font-size: 11.5px;
  color: var(--cp-text-3);
}
.cp-card-foot {
  display: flex;
  align-items: center;
  gap: 4px;
  margin-top: 2px;
}
.cp-card-res {
  flex: 1 1 auto;
  min-width: 0;
  font-size: 12px;
  color: var(--cp-text-3);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.cp-card-res.ok {
  color: var(--cp-success);
}
.cp-card-res.bad {
  color: var(--cp-danger);
}
.cp-card-add {
  gap: 4px;
}

.cp-matrix {
  flex: 1 1 auto;
  min-height: 0;
  overflow: auto;
  font-size: 12px;
}
.cp-matrix-row {
  display: flex;
  align-items: center;
  gap: 6px;
  height: 24px;
}
.cp-matrix-head {
  color: var(--cp-text-3);
}
.cp-matrix-cell {
  flex: 0 0 96px;
  color: var(--cp-text-2);
}
.cp-matrix-cell.ok {
  color: var(--cp-success);
}
.cp-matrix-cell.bad {
  color: var(--cp-danger);
}

.cp-ipinfo {
  flex: 0 0 33%;
  min-width: 250px;
  max-width: 360px;
  display: flex;
  flex-direction: column;
  gap: 3px;
  padding: 8px 10px 8px 0;
  font-size: 12px;
  overflow: auto;
}
.cp-ip-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.cp-ip-title {
  color: var(--cp-text-3);
}
.cp-ip-line {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
.cp-ip-label {
  flex: 0 0 46px;
  color: var(--cp-text-3);
}
.cp-country {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 26px;
  height: 16px;
  padding: 0 4px;
  border-radius: 3px;
  background: var(--cp-brand-soft);
  color: var(--cp-brand);
  font-size: 11px;
  font-weight: 600;
}
.cp-eye {
  border: 0;
  background: transparent;
  cursor: default;
  font-size: 12px;
  padding: 0 2px;
  color: var(--cp-text-3);
}
.cp-eye:hover {
  color: var(--cp-brand);
}
.cp-ip-err {
  color: var(--cp-danger);
}
.cp-ip-foot {
  margin-top: auto;
  font-size: 11.5px;
}
</style>
