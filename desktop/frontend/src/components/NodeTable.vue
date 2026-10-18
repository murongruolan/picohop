<script setup>
/**
 * 节点列表。
 *
 * 交互约定（与产品要求一致）：
 *   - 单击选中；Ctrl 加选、Shift 范围选
 *   - 回车激活选中项（快捷键在 App.vue 里统一处理）
 *   - 右键弹出菜单，操作对象是"当前选中集合"
 *   - 双击进入编辑
 *
 * 没使用表格自带的勾选列：每行前面多一个复选框，和"简洁"的目标冲突。
 * 选中态改用行底色表示，多选一样支持。
 *
 * ------------------------------------------------------------------
 * 分组（方案 A）
 * ------------------------------------------------------------------
 * 列表里插入"组行"：手动节点一组、每条订阅各一组，节点行紧跟在所属组行下面。
 *
 * 【为什么用 el-table 的树形行，而不是另搭一个列表】
 *   1. 列拖拽、填充列、每列渲染这些都已经被这张表吃透了，换列表等于全部重写；
 *   2. **关键**：树形模式只在"展开"时把子行放进 DOM。所以默认全折叠时，
 *      哪怕一份订阅有 1559 个节点，页面上也只有十来个组行 —— 长列表不卡靠的是它，
 *      不是靠分页装出来的。
 *
 * 组行整行横跨所有列（span-method），所以组头长什么样与用户怎么排列表格列无关。
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { useStore } from '../store'
import { useUi } from '../ui'
import { useActions } from '../actions'
import { ALL_COLUMNS, MIN_COL_WIDTH, MAX_COL_WIDTH, formatBytes, formatWhen } from '../columns'
import { Refresh, Top } from '@element-plus/icons-vue'
import NodeCell from './NodeCell.vue'

const store = useStore()
const ui = useUi()
const actions = useActions()

const tableRef = ref(null)
const wrapRef = ref(null)
/** 滚动时"组头已经滑出视野"的那个组：侧边浮动按钮针对它操作。 */
const stickyGroup = ref(null)

/** 当前应该渲染的列，顺序按 ALL_COLUMNS 固定，不随勾选顺序乱跳。 */
const cols = computed(() => ALL_COLUMNS.filter((c) => store.visibleColumns.includes(c.key)))

/**
 * 最右边那一列 = **填充列**。
 *
 * 它负责吃掉右侧所有剩余空间：这样任何一列被拖窄省出来的空间都会被它吸收，
 * 表格右边永远不会冒出一条留白。切换「列显示」时填充列会跟着变 ——
 * 哪一列在最右边，哪一列就是填充列。
 */
const tailKey = computed(() => cols.value[cols.value.length - 1]?.key)

/**
 * 某一列当前该用多宽。
 *
 * 返回 undefined 表示"这一列不写死宽度"：
 * Element Plus 判断一列是否弹性，依据正是 `!isNumber(column.width)`
 * （见 table-layout.mjs 的 updateColumnsWidth）。
 */
function widthOf(c) {
  if (c.key === tailKey.value) return undefined // 填充列：宽度交给"剩余空间"
  return store.columnWidths[c.key] ?? c.width
}

/**
 * 填充列的 min-width = 它的「最小宽度」（用户拖出来的值，或它自己的默认宽度）。
 *
 * 于是它的实际宽度 = minWidth + 剩余空间（table-layout.mjs 第 109 行），
 * 也就是永远铺满右端。
 */
function minWidthOf(c) {
  if (c.key === tailKey.value) return store.columnWidths[c.key] ?? c.width ?? 120
  return undefined
}

/**
 * 拖拽列宽结束。
 *
 * 【为什么要自己钳制】Element Plus 内部只保证不低于 30px
 * （table-header/event-helper.mjs 里的 `columnRect.left - tableLeft + 30`），
 * 而单元格左右各有 10px 内边距，30px 只剩 10px 文字区 ——
 * 一个汉字（约 13px）会被切掉一半，看起来就像两列叠在一起。
 * 这里把下限抬到 MIN_COL_WIDTH，保证至少能完整显示一个汉字。
 */
function onHeaderDragEnd(newWidth, oldWidth, column) {
  const key = column?.property
  if (!key) return
  const clamped = Math.max(MIN_COL_WIDTH, Math.min(MAX_COL_WIDTH, Math.round(newWidth || 0)))

  if (key === tailKey.value) {
    // 填充列：库刚刚把 column.width 写成了一个具体数字，那会让它不再是"弹性列"，
    // 于是右端立刻冒出一条留白 —— 正是要避免的现象。
    // 这里把宽度清掉，让它重新回到弹性状态（宽度重新由剩余空间决定）。
    // 紧随其后的重排由 min-width 的 watcher 触发
    // （watcher-helper.mjs 第 24-32 行会调 store.scheduleLayout），不需要我们手动排。
    column.width = undefined
    column.realWidth = undefined
  } else if (clamped !== newWidth) {
    // 直接改库内部列的宽度：此刻 handleMouseUp 里的 scheduleLayout 还排在
    // 下一个 requestAnimationFrame，所以这次改写会被紧接着的那次重排采用，
    // 不会先按超窄的宽度画一帧（否则会看到一次闪动）。
    column.width = clamped
    column.realWidth = clamped
  }
  store.setColumnWidth(key, clamped)
}

function rowClass({ row }) {
  if (row.__group) return 'cp-group-row'
  return store.selectedIds.includes(row.id) ? 'cp-row-selected' : ''
}

/**
 * 组行横跨所有列。
 *
 * 这样组头（名字、节点数、流量/到期、更新按钮）就与用户怎么排列表格列无关；
 * 不这么做的话，组头会被塞进"当前第一列"，用户一拖列宽就散了。
 */
function spanMethod({ row, columnIndex }) {
  if (row.__group) {
    return columnIndex === 0 ? [1, cols.value.length] : [0, 0]
  }
  return [1, 1]
}

function onRowClick(row, column, ev) {
  if (row.__group) {
    store.toggleGroup(row.group.id)
    return
  }
  ui.closeContextMenu()
  store.selectRow(row, ev)
}

function onRowContextMenu(row, column, ev) {
  ev.preventDefault()
  if (row.__group) {
    // 组头的订阅菜单由 ContextMenu 处理（见 ui.openGroupContextMenu）。
    ui.openGroupContextMenu(ev.clientX, ev.clientY, row.group)
    return
  }
  store.selectForContext(row)
  // 菜单贴着鼠标位置，但要防止贴边溢出
  const menuW = 216
  const menuH = 396
  const x = Math.min(ev.clientX, window.innerWidth - menuW - 8)
  const y = Math.min(ev.clientY, window.innerHeight - menuH - 8)
  ui.openContextMenu(Math.max(8, x), Math.max(8, y), row)
}

function onRowDblClick(row) {
  if (row.__group) return
  actions.editNode(row)
}

// ------------------------------------------------------------------
// 组头显示的信息
// ------------------------------------------------------------------

/** 流量文案：机场没给 total 就退回"已用"，什么都没给就显示 —。 */
function groupTrafficText(g) {
  const info = g.sub?.subscription?.userInfo || {}
  const used = (info.upload || 0) + (info.download || 0)
  const parts = []
  if (info.total > 0) parts.push(`剩余 ${formatBytes(Math.max(0, info.total - used))}`)
  else if (used > 0) parts.push(`已用 ${formatBytes(used)}`)
  if (info.expire > 0) parts.push(`到期 ${formatWhen(new Date(info.expire * 1000))}`)
  return parts.length ? parts.join(' · ') : '—'
}

/** 悬停看明细：已用/总量/到期/上次更新/失败原因。 */
function groupTrafficTip(g) {
  const s = g.sub?.subscription || {}
  const info = s.userInfo || {}
  const lines = []
  if (info.upload || info.download) {
    lines.push(`已用 ${formatBytes((info.upload || 0) + (info.download || 0))}`)
  }
  if (info.total > 0) lines.push(`总量 ${formatBytes(info.total)}`)
  lines.push(info.expire > 0 ? `到期 ${formatWhen(new Date(info.expire * 1000))}` : '到期时间：机场未提供')
  if (s.lastUpdate) lines.push(`上次更新 ${formatWhen(s.lastUpdate)}`)
  if (s.lastError) lines.push(`上次失败：${s.lastError}`)
  lines.push(s.userAgent ? `User-Agent：${s.userAgent}` : 'User-Agent：用设置里的默认值')
  return lines.join('\n')
}

// ------------------------------------------------------------------
// 滚动：记住位置 + 组头滑出时给出浮动按钮
// ------------------------------------------------------------------

/** 表体真正的滚动容器（Element Plus 用 el-scrollbar 包住表体）。 */
function scroller() {
  const root = wrapRef.value
  if (!root) return null
  return root.querySelector('.el-scrollbar__wrap') || root.querySelector('.el-table__body-wrapper')
}

/** 取某个 tr 对应的行对象（Element Plus 没有暴露，只能从组件实例上拿）。 */
function rowOf(tr) {
  return tr.__vueParentComponent?.props?.row || null
}

let scrollRaf = 0

function onScroll() {
  const el = scroller()
  if (!el) return
  // 位置记忆：store 里做了节流 + 去抖（滚动事件一秒能来上百次）
  store.onListScroll(el.scrollTop)
  if (scrollRaf) return
  scrollRaf = requestAnimationFrame(() => {
    scrollRaf = 0
    updateSticky(el)
  })
}

/**
 * 找出"组头已经滑到视野之上"的那个组。
 *
 * 逐个体检组行的位置：最后一个 top 已越过容器顶边的组，就是当前看不见组头的组。
 * 只查 DOM 里真实存在的组行 —— 折叠的组也有组行，所以这个判定始终准确。
 */
function updateSticky(el) {
  const rows = el.querySelectorAll('tr.cp-group-row')
  if (!rows.length) {
    stickyGroup.value = null
    return
  }
  const top = el.getBoundingClientRect().top
  let found = null
  for (const tr of rows) {
    if (tr.getBoundingClientRect().top - top <= 1) {
      found = rowOf(tr)?.group || found
    } else {
      break
    }
  }
  stickyGroup.value = found
}

/** 把某个组的组头滚回视野顶部。 */
function scrollToGroup(id) {
  const el = scroller()
  if (!el) return
  const top = el.getBoundingClientRect().top
  for (const tr of el.querySelectorAll('tr.cp-group-row')) {
    if (rowOf(tr)?.group?.id === id) {
      el.scrollTop += tr.getBoundingClientRect().top - top
      store.onListScroll(el.scrollTop)
      return
    }
  }
}

onMounted(async () => {
  await nextTick()
  const el = scroller()
  if (!el) return
  el.addEventListener('scroll', onScroll, { passive: true })
  // 恢复上次的滚动位置：等表格把行铺完（它是异步渲染的），否则会被重置到 0。
  const saved = store.savedScrollTop()
  if (saved > 0) {
    setTimeout(() => {
      el.scrollTop = saved
      updateSticky(el)
    }, 60)
  }
})

onBeforeUnmount(() => {
  const el = scroller()
  if (el) el.removeEventListener('scroll', onScroll)
  if (scrollRaf) cancelAnimationFrame(scrollRaf)
})
</script>

<template>
  <div ref="wrapRef" class="cp-table-wrap">
    <el-table
      ref="tableRef"
      :data="store.tableRows"
      height="100%"
      row-key="id"
      :tree-props="{ children: 'children' }"
      :expand-row-keys="store.openGroups.map((g) => 'g:' + g)"
      :row-class-name="rowClass"
      :span-method="spanMethod"
      @row-click="onRowClick"
      @row-contextmenu="onRowContextMenu"
      @row-dblclick="onRowDblClick"
      @header-dragend="onHeaderDragEnd"
      border
      allow-drag-last-column
    >
      <el-table-column
        v-for="c in cols"
        :key="c.key"
        :prop="c.key"
        :label="c.label"
        :width="widthOf(c)"
        :min-width="minWidthOf(c)"
        :align="c.align || 'left'"
        :header-align="c.align || 'left'"
      >
        <template #default="{ row }">
          <!-- 组行：整行横跨，画成组头 -->
          <div v-if="row.__group" class="cp-group" @click.stop="store.toggleGroup(row.group.id)">
            <span class="cp-group-chevron" :class="{ 'is-open': store.isGroupOpen(row.group.id) }">
              <el-icon><Top /></el-icon>
            </span>
            <span class="cp-group-name" :title="row.group.name">{{ row.group.name }}</span>
            <span class="cp-group-count">{{ row.group.nodes.length }} 个节点</span>

            <template v-if="!row.group.isManual">
              <span class="cp-group-meta" :title="groupTrafficTip(row.group)">
                {{ groupTrafficText(row.group) }}
              </span>
              <span v-if="row.group.sub?.missing" class="cp-tag is-warn">
                {{ row.group.sub.missing }} 个已下架
              </span>
              <span v-if="row.group.sub?.pending" class="cp-group-count">
                {{ row.group.sub.pending }} 个未启用
              </span>
            </template>

            <span class="cp-group-spacer" />

            <!-- 长列表的"按需展开"：默认每组只渲染前 200 个 -->
            <span
              v-if="row.hiddenCount > 0"
              class="cp-group-more"
              @click.stop="store.toggleGroupShowAll(row.group.id)"
            >
              显示全部 {{ row.group.nodes.length }} 个
            </span>

            <el-tooltip
              v-if="!row.group.isManual"
              content="更新这条订阅"
              placement="top"
              :show-after="400"
            >
              <span class="cp-group-action" @click.stop="store.refreshSubscription(row.group.id)">
                <el-icon :class="{ 'cp-spin': store.refreshingSubs.includes(row.group.id) }">
                  <Refresh />
                </el-icon>
              </span>
            </el-tooltip>
          </div>

          <!--
            节点行：还是原来的单元格渲染。

            订阅节点在「别名」列上加一点缩进，让"组 → 组里的节点"读起来有层次。
            缩进只加在别名列：类型列是最窄的一列，往里塞缩进会把协议标签挤断。
          -->
          <span
            v-else
            :class="{ 'cp-node-indent': row.subId && c.key === 'name' }"
          >
            <NodeCell :row="row" :col="c.key" />
          </span>
        </template>
      </el-table-column>

      <template #empty>
        <div class="cp-empty">
          <template v-if="store.nodes.length === 0">
            <div class="cp-empty-title">还没有任何节点</div>
            <div class="cp-hint" style="text-align: center; max-width: 470px; line-height: 1.8">
              点右上角「添加节点」手工添加，或者用「导入」从剪贴板里的分享链接、
              屏幕上的二维码批量导入。<br />
              每个节点可以单独指定本地代理端口，也可以让它经由另一个节点当前置。
            </div>
            <el-button type="primary" plain @click="actions.addNode()">
              添加第一个节点
            </el-button>
          </template>
          <template v-else>
            <div class="cp-empty-title">没有符合条件的节点</div>
            <div class="cp-hint">清空搜索框或把状态切回「全部状态」试试。</div>
          </template>
        </div>
      </template>
    </el-table>

    <!--
      组头滑出视野后的浮动按钮。

      【为什么要它】一份订阅可能有上千个节点，展开后往下翻很久，想"回到这组开头"
      或者"把这组收起来"就得一路滚回去 —— 那很烦。有这一对按钮，任何时候都能
      就地回到组头或收起本组。
    -->
    <transition name="cp-fade">
      <div v-if="stickyGroup" class="cp-sticky-group">
        <div class="cp-sticky-name" :title="stickyGroup.name">{{ stickyGroup.name }}</div>
        <button class="cp-sticky-btn" @click="scrollToGroup(stickyGroup.id)">
          <el-icon><Top /></el-icon>
          <span>组头</span>
        </button>
        <button class="cp-sticky-btn" @click="store.toggleGroup(stickyGroup.id)">
          <span>收起</span>
        </button>
      </div>
    </transition>
  </div>
</template>

<style>
/* 选中行：用品牌色浅底，比 Element 自带的 current-row 更柔和 */
.cp-table-wrap .el-table__body tr.cp-row-selected > td.el-table__cell,
.cp-table-wrap .el-table__body tr.cp-row-selected:hover > td.el-table__cell {
  background: var(--cp-brand-soft) !important;
}
.cp-empty-title {
  font-size: 14px;
  color: var(--cp-text-2);
  font-weight: 600;
}

/* ------------------------------------------------------------------
   组头：稍深的底 + 加粗名字，节点行保持原样，层次一眼可辨
   ------------------------------------------------------------------ */
.cp-table-wrap .el-table__body tr.cp-group-row > td.el-table__cell {
  background: var(--cp-group-bg);
  border-bottom: 1px solid var(--cp-border);
  padding-top: 6px;
  padding-bottom: 6px;
}
.cp-table-wrap .el-table__body tr.cp-group-row:hover > td.el-table__cell {
  background: var(--cp-group-bg-hover);
}
.cp-group {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  user-select: none;
  cursor: pointer;
  min-width: 0;
}
/*
 * 组行的方向标由我们自己画（见上面的 chevron），所以要把 Element Plus 树形模式
 * 自带的展开箭头与缩进关掉。
 *
 * 【为什么必须关】展开的组才有 children，库才会给这一行插它自己的"箭头 + 缩进"；
 * 结果是：组头被挤到第二行、整行变高，而收起时又恢复 —— 用户实测反馈的就是这个
 * "展开时组头往下走一行"。
 */
.cp-table-wrap .el-table__body tr.cp-group-row .el-table__expand-icon,
.cp-table-wrap .el-table__body tr.cp-group-row .el-table__indent,
.cp-table-wrap .el-table__body tr.cp-group-row .el-table__placeholder {
  display: none !important;
}
.cp-group-chevron {
  display: inline-flex;
  color: var(--cp-text-3);
  transition: transform 0.15s ease;
}
.cp-group-chevron.is-open {
  transform: rotate(180deg);
}
.cp-group-name {
  font-weight: 600;
  color: var(--cp-text-1);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 320px;
}
.cp-group-count,
.cp-group-meta {
  font-size: 12px;
  color: var(--cp-text-3);
  white-space: nowrap;
}
.cp-group-spacer {
  flex: 1 1 auto;
}
/* 组内节点的第一列缩进：只挪第一列，别动用户在列宽上排好的对齐 */
.cp-node-indent {
  padding-left: 18px;
}
.cp-group-more {
  font-size: 12px;
  color: var(--cp-brand);
  border-bottom: 1px dashed var(--cp-brand);
  white-space: nowrap;
  cursor: pointer;
}
.cp-group-more:hover {
  opacity: 0.8;
}
.cp-group-action {
  display: inline-flex;
  align-items: center;
  padding: 2px 6px;
  border-radius: 6px;
  color: var(--cp-text-2);
  cursor: pointer;
}
.cp-group-action:hover {
  background: var(--cp-brand-soft);
  color: var(--cp-brand);
}
.cp-tag.is-warn {
  color: var(--cp-warning);
  border-color: var(--cp-warning);
}

/* ------------------------------------------------------------------
   组头滑出后的浮动按钮（贴右边缘，竖向两个）
   ------------------------------------------------------------------ */
.cp-sticky-group {
  position: absolute;
  right: 14px;
  top: 50%;
  transform: translateY(-50%);
  z-index: 6;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
  padding: 8px 6px;
  border-radius: 10px;
  background: var(--cp-panel);
  border: 1px solid var(--cp-border);
  box-shadow: 0 6px 18px rgba(0, 0, 0, 0.14);
  max-width: 132px;
}
.cp-sticky-name {
  font-size: 11px;
  color: var(--cp-text-3);
  max-width: 120px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.cp-sticky-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 4px 8px;
  font-size: 12px;
  color: var(--cp-text-2);
  background: transparent;
  border: 1px solid var(--cp-border);
  border-radius: 8px;
  cursor: pointer;
}
.cp-sticky-btn:hover {
  color: var(--cp-brand);
  border-color: var(--cp-brand);
}
.cp-fade-enter-active,
.cp-fade-leave-active {
  transition: opacity 0.15s ease;
}
.cp-fade-enter-from,
.cp-fade-leave-to {
  opacity: 0;
}
</style>
