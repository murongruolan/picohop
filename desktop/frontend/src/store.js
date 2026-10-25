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
// 订阅分组的展开状态与列表滚动位置：属于**界面状态**，不进配置文件
// （配置是"用户意图"，而这些是"这次看到哪儿了"，跟着机器走更合适）。
const LS_OPEN_GROUPS = 'cp.openGroups'
const LS_LIST_SCROLL = 'cp.listScrollTop'

/**
 * "手动节点"那一组的固定 ID。
 *
 * 订阅节点的 subId 是订阅 ID，手动节点没有 subId —— 用这个常量把它们归到一组，
 * 避免和真实订阅 ID 撞名（订阅 ID 是后端生成的随机串，不可能等于这个带下划线的名字）。
 */
export const GROUP_MANUAL = '__manual'

/**
 * 展开的组里**默认只渲染**这么多个节点。
 *
 * 【为什么要有这个上限】实测真实机场一份订阅有 1559 个节点；全渲染出来，
 * 打开一组要卡上好几秒。默认先给前 200 个，用户真要看全（或要在里面搜）时
 * 再点组头那句"显示全部 N 个"。
 */
export const GROUP_LIMIT = 200

function loadOpenGroups() {
  try {
    const raw = localStorage.getItem(LS_OPEN_GROUPS)
    const list = raw ? JSON.parse(raw) : []
    return Array.isArray(list) ? list.filter((x) => typeof x === 'string') : []
  } catch {
    return []
  }
}

function loadListScrollTop() {
  const n = Number(localStorage.getItem(LS_LIST_SCROLL) || 0)
  return Number.isFinite(n) && n > 0 ? n : 0
}

function writeOpenGroups(list) {
  try {
    localStorage.setItem(LS_OPEN_GROUPS, JSON.stringify(list))
  } catch {
    /* 隐私模式下写不了，忽略 */
  }
}

function writeListScrollTop(top) {
  try {
    localStorage.setItem(LS_LIST_SCROLL, String(Math.max(0, Math.round(top || 0))))
  } catch {
    /* 同上 */
  }
}

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

    // ---- 订阅（分组列表）----
    /** 订阅列表（后端 GetSubscriptions，含节点数/未分配/已下架计数）。 */
    subscriptions: [],
    /** 展开的分组 ID；默认**全折叠**（1559 个节点全展开等于自杀）。 */
    openGroups: loadOpenGroups(),
    /** 点过"显示全部"的组（默认每组只渲染 GROUP_LIMIT 个）。 */
    groupShowAll: [],
    /** 上次的列表滚动位置（下次打开回到原处）。 */
    listScrollTop: loadListScrollTop(),
    /** 正在更新的订阅 ID（组头的更新按钮转圈用）。 */
    refreshingSubs: [],
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

    /**
     * 按订阅把节点分成组，最后再加上「手动节点」那一组。
     *
     * 【为什么分组在前端算】分组本质只是"按 subId 归堆"+一点显示信息。放在前端，
     * 筛选（搜索/状态）会天然跟着走 —— 用户搜"香港"时组头显示的计数就是命中的个数，
     * 不会出现"组头写着 40 个、点开只有 3 个"的错乱。
     *
     * 顺序（方案 A 的约定）：手动节点在最前（它是用户自己建的、找它最频繁），
     * 然后按后端给的 Sort 排各条订阅。
     */
    nodeGroups(state) {
      const groups = []
      const byId = new Map()

      const manual = {
        id: GROUP_MANUAL,
        name: '手动节点',
        isManual: true,
        sub: null,
        total: 0,
        nodes: [],
      }
      groups.push(manual)
      byId.set(GROUP_MANUAL, manual)

      for (const s of state.subscriptions || []) {
        const sub = s.subscription || {}
        const g = {
          id: sub.id,
          name: sub.name || '订阅',
          isManual: false,
          sub: s,
          total: 0,
          nodes: [],
        }
        groups.push(g)
        byId.set(g.id, g)
      }

      for (const n of state.nodes) {
        const key = n.subId || GROUP_MANUAL
        let g = byId.get(key)
        if (!g) {
          // 订阅记录还没拉到（或已被删）：按节点自带的分组名临时建一组，
          // 别让这些节点在界面上凭空消失。
          g = { id: key, name: n.subName || '订阅', isManual: false, sub: null, total: 0, nodes: [] }
          groups.push(g)
          byId.set(key, g)
        }
        g.total++
      }
      for (const n of state.visibleNodes) {
        const g = byId.get(n.subId || GROUP_MANUAL)
        if (g) g.nodes.push(n)
      }
      return groups
    },

    /**
     * 表格真正渲染的行：组行 +（已展开组里的）节点行。
     *
     * 【折叠的组为什么一个节点行都不渲染】el-table 的树形模式只在行处于"展开"状态时
     * 才把 children 放进 DOM。所以默认全折叠时，1559 个节点也只有十来个组行在页面上 ——
     * 这才是长列表不卡的根本原因，不是靠分页装出来的。
     */
    tableRows(state) {
      const open = new Set(state.openGroups)
      const filtering = !!(state.searchApplied || '').trim() || state.statusFilter !== 'all'
      const rows = []
      for (const g of this.nodeGroups) {
        // 空组别占地方：手动组没节点就整组不显示（那时应该是"还没有任何节点"的空态）；
        // 订阅组在筛选态下没命中就先藏起来，免得用户搜一个词还要滚过一堆空组。
        if (g.isManual && g.nodes.length === 0) continue
        if (!g.isManual && filtering && g.nodes.length === 0) continue
        const isOpen = open.has(g.id)
        const showAll = state.groupShowAll.includes(g.id)
        const children = isOpen ? (showAll ? g.nodes : g.nodes.slice(0, GROUP_LIMIT)) : []
        rows.push({
          id: `g:${g.id}`,
          __group: true,
          group: g,
          children,
          hiddenCount: isOpen && !showAll ? Math.max(0, g.nodes.length - children.length) : 0,
        })
      }
      return rows
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
        await this.refreshSubscriptions()
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
        // 订阅本身变了（增删/改名/更新完成后的元信息）：只重拉订阅表，不重画节点列表。
        subscriptions: () => this.refreshSubscriptions(),
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
        const [nodes, summary, testing, subs] = await Promise.all([
          api.getNodes(),
          api.getSummary(),
          api.isTesting(),
          // 订阅表也要一起刷：组头的「N 个未启用 / 已下架」是由它算的，
          // 只刷节点的话，激活一个节点后组头还显示旧的计数。
          api.getSubscriptions(),
        ])
        this.nodes = nodes || []
        this.summary = summary || {}
        this.subscriptions = subs || []
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

    // -----------------------------------------------------------------
    // 订阅与分组
    // -----------------------------------------------------------------
    async refreshSubscriptions() {
      try {
        this.subscriptions = (await api.getSubscriptions()) || []
      } catch (e) {
        console.error('拉取订阅列表失败', e)
      }
    },

    isGroupOpen(id) {
      return this.openGroups.includes(id)
    },

    /** 展开/收起一个分组，并把状态记住（下次打开还是这样）。 */
    toggleGroup(id) {
      const set = new Set(this.openGroups)
      if (set.has(id)) set.delete(id)
      else set.add(id)
      this.openGroups = [...set]
      writeOpenGroups(this.openGroups)
    },

    /** 组内「显示全部」（默认每组只渲染前 GROUP_LIMIT 个）。 */
    toggleGroupShowAll(id) {
      const set = new Set(this.groupShowAll)
      if (set.has(id)) set.delete(id)
      else set.add(id)
      this.groupShowAll = [...set]
    },

    /**
     * 列表滚动。
     *
     * 【为什么要节流 + 去抖】滚动事件一秒能来几十上百次，每次都写一遍 localStorage
     * 既没意义、又会卡住主线程。所以：滚动过程中最多每 1 秒写一次（节流），
     * 停手 400ms 后再补写一次收尾（去抖）—— 保证"最后停在哪儿"一定被记住。
     */
    onListScroll(top) {
      this.listScrollTop = Math.max(0, Math.round(top || 0))
      const now = Date.now()
      if (now - (this._scrollWroteAt || 0) >= 1000) {
        this._scrollWroteAt = now
        writeListScrollTop(this.listScrollTop)
      }
      clearTimeout(this._scrollTimer)
      this._scrollTimer = setTimeout(() => {
        this._scrollWroteAt = Date.now()
        writeListScrollTop(this.listScrollTop)
      }, 400)
    },

    /** 上次的滚动位置（表格挂载后恢复用）。 */
    savedScrollTop() {
      return this.listScrollTop || 0
    },

    /** 把所选节点的本地端口置于未分配（后端会先停掉激活中的）。 */
    async unassignPorts(ids) {
      const n = await api.unassignNodePorts(ids)
      await this.refresh()
      return n
    },

    /** 启用一个还没分配端口的订阅节点：后端会分配端口并启动它。 */
    async enableSubscriptionNode(row) {
      try {
        await api.enableSubscriptionNode(row.id)
        await this.refresh()
      } catch (e) {
        notify('error', '启用失败', String(e?.message || e))
      }
    },

    /** 更新一条订阅（增量合并：新增/刷新/标记下架）。 */
    async refreshSubscription(id) {
      if (this.refreshingSubs.includes(id)) return null
      this.refreshingSubs = [...this.refreshingSubs, id]
      try {
        const rep = (await api.refreshSubscription(id)) || {}
        await Promise.all([this.refresh(), this.refreshSubscriptions()])
        notify(
          'success',
          '订阅已更新',
          `新增 ${rep.added || 0}、刷新 ${rep.updated || 0}、标记下架 ${rep.missing || 0}`,
        )
        return rep
      } catch (e) {
        notify('error', '更新订阅失败', String(e?.message || e))
        return null
      } finally {
        this.refreshingSubs = this.refreshingSubs.filter((x) => x !== id)
      }
    },

    async previewSubscription(req) {
      return api.previewSubscription(req)
    },

    /** 导入一条订阅：建分组，节点以"未分配端口"的状态进来。 */
    async importSubscription(req) {
      const rep = (await api.importSubscription(req)) || {}
      await Promise.all([this.refresh(), this.refreshSubscriptions()])
      return rep
    },

    async saveSubscriptionMeta(sub) {
      const saved = await api.saveSubscriptionMeta(sub)
      await this.refreshSubscriptions()
      return saved
    },

    async deleteSubscription(id, alsoNodes) {
      await api.deleteSubscription(id, alsoNodes)
      await Promise.all([this.refresh(), this.refreshSubscriptions()])
    },

    async clearMissingNodes(id) {
      const n = await api.clearMissingNodes(id)
      await Promise.all([this.refresh(), this.refreshSubscriptions()])
      return n
    },
  },
})
