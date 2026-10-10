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
import { ALL_COLUMNS } from '../columns'
import NodeCell from './NodeCell.vue'

const store = useStore()
const ui = useUi()
const actions = useActions()

const tableRef = ref(null)

/** 当前应该渲染的列，顺序按 ALL_COLUMNS 固定，不随勾选顺序乱跳。 */
const cols = computed(() => ALL_COLUMNS.filter((c) => store.visibleColumns.includes(c.key)))

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
    >
      <el-table-column
        v-for="c in cols"
        :key="c.key"
        :prop="c.key"
        :label="c.label"
        :width="c.width"
        :min-width="c.minWidth"
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
