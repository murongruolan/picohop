/**
 * 全局状态（Pinia）。
 *
 * 后端每秒推一次 stats 事件，这里做**去抖合并**：无论多少事件，
 * 最多每 700ms 真正向后端要一次节点列表，避免事件风暴把 IPC 打满。
 */
import { defineStore } from 'pinia'
import api, { notify, subscribeAll } from './api'
import { DEFAULT_VISIBLE, ALL_COLUMNS, MIN_COL_WIDTH, MAX_COL_WIDTH } from './columns'

const LS_COLUMNS = 'cp.visibleColumns'
const LS_COL_WIDTHS = 'cp.columnWidths'
const LS_THEME = 'cp.theme'
const LS_STATUS = 'cp.statusFilter'

function loadVisibleColumns() {
  try {
    const raw = localStorage.getItem(LS_COLUMNS)
    if (!raw) return [...DEFAULT_VISIBLE]
    const list = JSON.parse(raw)
    const valid = list.filter((k) => ALL_COLUMNS.some((c) => c.key === k))
    return valid.length ? valid : [...DEFAULT_VISIBLE]
  } catch {
    return [...DEFAULT_VISIBLE]
  }
}

/**
 * 用户拖拽过的列宽。
 *
 * 只记"用户改过的"，没改过的列仍然用列定义里的默认值 ——
 * 这样以后调整默认宽度时，没动过列宽的人能跟着变好。
 */
function loadColumnWidths() {
  try {
    const raw = localStorage.getItem(LS_COL_WIDTHS)
    if (!raw) return {}
    const obj = JSON.parse(raw)
    const out = {}
    for (const [k, v] of Object.entries(obj || {})) {
      const n = Number(v)
      if (ALL_COLUMNS.some((c) => c.key === k) && Number.isFinite(n)) {
        out[k] = Math.min(MAX_COL_WIDTH, Math.max(MIN_COL_WIDTH, Math.round(n)))
      }
    }
    return out
  } catch {
    return {}
  }
}

export const useStore = defineStore('cp', {
  state: () => ({
    ready: false,
    /**
     * settingsLoaded 表示 store.settings 里装的是**从后端读回来的真实配置**，
     * 而不是"还没加载完时的空对象"。
     *
     * 【为什么需要这个标记】设置是异步加载的（App.vue 的 onMounted 里 await init()），
     * 而界面在加载完之前就能点。凡是"改设置并写回"的地方都必须以它为门闩，
     * 否则会把一份残缺的（其余字段全是零值）设置对象写回配置 —— 那就是"自我擦除"。
     */
    settingsLoaded: false,
    nodes: [],
    summary: {},
    meta: {},
    settings: {},
    front: {},
    logs: [],
    // 网络日志（sing-box 连接流水）与它的拉取游标。
    // 游标只增不减：后端序号单调递增，回退会把清空前的日志又拉回来。
    netLogs: [],
    netSeq: 0,

    // 筛选
    searchText: '',
    searchApplied: '',
    statusFilter: localStorage.getItem(LS_STATUS) || 'all',
    visibleColumns: loadVisibleColumns(),
    columnWidths: loadColumnWidths(),

    // 选择
    selectedIds: [],
    anchorIndex: -1,

    // 测试
    testing: false,
    testKind: '',
    testingIds: [],

    // 界面
    theme: localStorage.getItem(LS_THEME) || 'system',
    logDrawer: false,

    // 圈选
    scanning: false,
  }),

  getters: {
    /** 应用搜索 + 状态筛选后的节点。 */
    visibleNodes(state) {
      const kw = (state.searchApplied || '').trim().toLowerCase()
      return state.nodes.filter((n) => {
        if (state.statusFilter === 'active' && n.state !== 'running') return false
        if (state.statusFilter === 'inactive' && n.state === 'running') return false
        if (!kw) return true
        return String(n.name || '').toLowerCase().includes(kw)
      })
    },

    /** 选中的节点对象。 */
    selectedNodes(state) {
      const set = new Set(state.selectedIds)
      return state.nodes.filter((n) => set.has(n.id))
    },

    runningCount(state) {
      return state.nodes.filter((n) => n.state === 'running').length
    },
  },

  actions: {
    // -----------------------------------------------------------------
    // 初始化
    // -----------------------------------------------------------------
    async init() {
      try {
        this.meta = await api.getMeta()
        this.settings = (await api.getSettings()) || {}
        // 【置位必须紧跟赋值】置位之后"写设置"这条路才算打开；
        // 之前写入的都会被认为"这不是完整配置"而拒绝（见 BottomPanel.persist）。
        this.settingsLoaded = true
        this.front = await api.getFront()
        // 主题以**配置文件**为准（localStorage 只是缓存）：
        // 这样直接改 config.json 换主题也能生效，清了浏览器数据也不会丢。
        if (this.settings?.theme) this.theme = this.settings.theme
        await this.refresh()
        this.applyTheme()
        this.ready = true
      } catch (e) {
        console.error(e)
        notify('error', '初始化失败', String(e?.message || e))
      }
    },

    /** 注册后端事件。返回取消函数。 */
    bindEvents() {
      return subscribeAll({
        log: () => this.markLogDirty(),
        nodes: () => this.scheduleRefresh(),
        stats: () => this.scheduleRefresh(),
        testDone: () => this.scheduleRefresh(true),
      })
    },

    /**
     * 去抖刷新：把一秒内的多次事件合并成一次真正拉取。
     */
    scheduleRefresh(immediate = false) {
      if (immediate) {
        this.refresh()
        return
      }
      if (this._pending) return
      this._pending = setTimeout(() => {
        this._pending = null
        this.refresh()
      }, 700)
    },

    async refresh() {
      if (!this.ready && !this.meta?.version) {
        // init 还没跑完，等它
      }
      try {
        const [nodes, summary, testing] = await Promise.all([
          api.getNodes(),
          api.getSummary(),
          api.isTesting(),
        ])
        this.nodes = nodes || []
        this.summary = summary || {}
        this.testing = !!testing
        // 清理已经不存在的选中项
        const ids = new Set(this.nodes.map((n) => n.id))
        this.selectedIds = this.selectedIds.filter((id) => ids.has(id))
      } catch (e) {
        console.error('刷新失败', e)
      }
    },

    async markLogDirty() {
      this._logDirty = true
      if (this.logDrawer) await this.refreshLogs()
    },

    async refreshLogs() {
      try {
        this.logs = (await api.getLogs()) || []
      } catch {
        /* 忽略 */
      }
    },

    // 网络日志：**游标式增量拉取**。日志每秒可能进来十几条，
    // 每次都把整段重新传一遍是不可接受的。
    async refreshNetworkLogs() {
      try {
        const [lines, seq] = await api.getNetworkLogs(this.netSeq)
        if (seq) this.netSeq = seq
        if (lines && lines.length) {
          const merged = this.netLogs.concat(lines)
          // 显示上限与后端保留条数一致，避免界面无限增长
          this.netLogs = merged.length > 2000 ? merged.slice(-2000) : merged
        }
      } catch {
        /* 忽略 */
      }
    },

    async clearNetworkLogs() {
      try {
        await api.clearNetworkLogs()
      } catch {
        /* 忽略 */
      }
      this.netLogs = []
      // 注意不改 netSeq：后端序号继续递增，新的条目照样能拉进来
    },

    // -----------------------------------------------------------------
    // 筛选 / 列
    // -----------------------------------------------------------------
    applySearch() {
      this.searchApplied = this.searchText
    },

    setStatusFilter(v) {
      this.statusFilter = v
      localStorage.setItem(LS_STATUS, v)
    },

    setVisibleColumns(keys) {
      // 至少留一列，否则表格会变成白板
      if (!keys.length) return
      this.visibleColumns = keys
      localStorage.setItem(LS_COLUMNS, JSON.stringify(keys))
    },

    resetColumns() {
      this.setVisibleColumns([...DEFAULT_VISIBLE])
      this.resetColumnWidths()
    },

    // -----------------------------------------------------------------
    // 列宽
    // -----------------------------------------------------------------
    /** 记录用户拖出来的列宽（已钳制到 [MIN_COL_WIDTH, MAX_COL_WIDTH]）。 */
    setColumnWidth(key, width) {
      if (!ALL_COLUMNS.some((c) => c.key === key)) return
      const w = Math.min(MAX_COL_WIDTH, Math.max(MIN_COL_WIDTH, Math.round(width)))
      this.columnWidths = { ...this.columnWidths, [key]: w }
      localStorage.setItem(LS_COL_WIDTHS, JSON.stringify(this.columnWidths))
    },

    resetColumnWidths() {
      this.columnWidths = {}
      localStorage.removeItem(LS_COL_WIDTHS)
    },

    // -----------------------------------------------------------------
    // 主题
    // -----------------------------------------------------------------
    setTheme(t) {
      this.theme = t
      localStorage.setItem(LS_THEME, t)
      this.applyTheme()
    },

    applyTheme() {
      const prefersDark =
        window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches
      const dark = this.theme === 'dark' || (this.theme === 'system' && prefersDark)
      document.documentElement.classList.toggle('dark', dark)
      // 让 Windows 标题栏也跟着变，否则深色界面配白色标题栏很割裂
      try {
        api.setWindowTheme(this.theme)
      } catch {
        /* 后端未就绪时忽略 */
      }
    },

    // -----------------------------------------------------------------
    // 选择
    // -----------------------------------------------------------------
    /**
     * 单击某一行。
     *
     * 支持 Ctrl（切换单个）与 Shift（范围选择），否则单选。
     * 之所以不用表格自带的勾选列：那样每行前面都多一个复选框，
     * 和"简洁"的目标相冲。
     */
    selectRow(node, ev) {
      const list = this.visibleNodes
      const idx = list.findIndex((n) => n.id === node.id)
      if (ev?.shiftKey && this.anchorIndex >= 0 && idx >= 0) {
        const [a, b] = [this.anchorIndex, idx].sort((x, y) => x - y)
        const range = list.slice(a, b + 1).map((n) => n.id)
        this.selectedIds = Array.from(new Set([...this.selectedIds, ...range]))
        return
      }
      if (ev?.ctrlKey || ev?.metaKey) {
        const set = new Set(this.selectedIds)
        if (set.has(node.id)) set.delete(node.id)
        else set.add(node.id)
        this.selectedIds = [...set]
        this.anchorIndex = idx
        return
      }
      this.selectedIds = [node.id]
      this.anchorIndex = idx
    },

    selectAllVisible() {
      this.selectedIds = this.visibleNodes.map((n) => n.id)
    },

    clearSelection() {
      this.selectedIds = []
    },

    /** 右键时：如果点在已选中集合里就保留多选，否则只选它。 */
    selectForContext(node) {
      if (!this.selectedIds.includes(node.id)) {
        this.selectedIds = [node.id]
      }
    },
  },
})
