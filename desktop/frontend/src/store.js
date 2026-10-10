/**
 * 全局状态（Pinia）。
 *
 * 后端每秒推一次 stats 事件，这里做**去抖合并**：无论多少事件，
 * 最多每 700ms 真正向后端要一次节点列表，避免事件风暴把 IPC 打满。
 */
import { defineStore } from 'pinia'
import api, { notify, subscribeAll } from './api'
import { DEFAULT_VISIBLE, ALL_COLUMNS } from './columns'

const LS_COLUMNS = 'cp.visibleColumns'
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

export const useStore = defineStore('cp', {
  state: () => ({
    ready: false,
    nodes: [],
    summary: {},
    meta: {},
    settings: {},
    front: {},
    logs: [],

    // 筛选
    searchText: '',
    searchApplied: '',
    statusFilter: localStorage.getItem(LS_STATUS) || 'all',
    visibleColumns: loadVisibleColumns(),

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
        this.settings = await api.getSettings()
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
