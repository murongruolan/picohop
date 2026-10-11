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
 */
import { computed, ref } from 'vue'
import { useStore } from '../store'
import { useUi } from '../ui'
import { useActions } from '../actions'
import { ALL_COLUMNS, MIN_COL_WIDTH, MAX_COL_WIDTH } from '../columns'
import NodeCell from './NodeCell.vue'

const store = useStore()
const ui = useUi()
const actions = useActions()

const tableRef = ref(null)

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
  return store.selectedIds.includes(row.id) ? 'cp-row-selected' : ''
}

function onRowClick(row, column, ev) {
  ui.closeContextMenu()
  store.selectRow(row, ev)
}

function onRowContextMenu(row, column, ev) {
  ev.preventDefault()
  store.selectForContext(row)
  // 菜单贴着鼠标位置，但要防止贴边溢出
  const menuW = 216
  const menuH = 396
  const x = Math.min(ev.clientX, window.innerWidth - menuW - 8)
  const y = Math.min(ev.clientY, window.innerHeight - menuH - 8)
  ui.openContextMenu(Math.max(8, x), Math.max(8, y), row)
}

function onRowDblClick(row) {
  actions.editNode(row)
}
</script>

<template>
  <div class="cp-table-wrap">
    <el-table
      ref="tableRef"
      :data="store.visibleNodes"
      height="100%"
      row-key="id"
      :row-class-name="rowClass"
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
          <NodeCell :row="row" :col="c.key" />
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
</style>
