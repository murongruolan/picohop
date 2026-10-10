<script setup>
/**
 * 列表上方的搜索 / 筛选 / 列显示。
 *
 * 搜索是"失焦或回车后执行"（产品要求），不是实时过滤 ——
 * 因为每一行都可能对应一个正在跑的代理，实时抖动反而晃眼。
 */
import { computed, ref, watch } from 'vue'
import {
  ArrowDown,
  Close,
  Filter,
  Grid,
  Odometer,
  Search,
  Timer,
} from '@element-plus/icons-vue'
import { useStore } from '../store'
import { useUi } from '../ui'
import { useActions } from '../actions'
import { ALL_COLUMNS, DEFAULT_VISIBLE } from '../columns'
import api, { notify } from '../api'

const store = useStore()
const ui = useUi()
const actions = useActions()

const columnPanel = ref(false)
const selectedCount = computed(() => store.selectedIds.length)

const statusOptions = [
  { value: 'all', label: '全部状态' },
  { value: 'active', label: '激活状态' },
  { value: 'inactive', label: '未激活' },
]

/** 列显示勾选值（直接绑定 store，改动即持久化）。 */
const columns = computed({
  get: () => store.visibleColumns,
  set: (v) => store.setVisibleColumns(v),
})

function onTestCommand(cmd) {
  if (cmd === 'cancel') {
    actions.cancelTest()
    return
  }
  // 没选节点时测试全部（更省事），选了就只测选中的
  const ids = selectedCount.value ? store.selectedIds : store.nodes.map((n) => n.id)
  if (!ids.length) {
    notify('warning', '没有可测试的节点', '')
    return
  }
  actions.runTest(cmd, ids)
}

const testing = computed(() => store.testing)
const testingLabel = computed(() => {
  const map = { tcp: 'TCP 延迟', real: '真连接延迟', speed: '速度', exit: '出口 IP' }
  return map[store.testKind] || '测试'
})

// 搜索框失焦时如果内容有变，也执行一次（产品要求"失焦或回车"）
const searchRef = ref(null)
watch(
  () => store.searchText,
  (v) => {
    if ((v || '') === '') store.applySearch()
  },
)
</script>

<template>
  <div class="cp-filterbar">
    <el-input
      ref="searchRef"
      v-model="store.searchText"
      placeholder="搜索别名…"
      clearable
      style="width: 208px"
      :prefix-icon="Search"
      @keyup.enter="store.applySearch()"
      @blur="store.applySearch()"
      @clear="store.applySearch()"
    />

    <el-select
      :model-value="store.statusFilter"
      style="width: 124px"
      @update:model-value="store.setStatusFilter"
    >
      <el-option
        v-for="o in statusOptions"
        :key="o.value"
        :label="o.label"
        :value="o.value"
      />
    </el-select>

    <!-- 列显示：用 popover 而不是 multiple select，后者的标签堆在一起很乱 -->
    <el-popover v-model:visible="columnPanel" :width="252" trigger="click" placement="bottom-start">
      <template #reference>
        <el-button :icon="Grid">
          列显示
          <span class="cp-dim" style="margin-left: 2px">
            {{ columns.length }}/{{ ALL_COLUMNS.length }}
          </span>
        </el-button>
      </template>
      <div class="cp-col-head">
        <span>勾选要显示的列</span>
        <span>
          <el-button link type="primary" size="small" @click="store.setVisibleColumns(ALL_COLUMNS.map((c) => c.key))">
            全选
          </el-button>
          <el-button link size="small" @click="store.resetColumns()">恢复默认</el-button>
        </span>
      </div>
      <el-checkbox-group v-model="columns" class="cp-col-group">
        <el-checkbox v-for="c in ALL_COLUMNS" :key="c.key" :value="c.key" :label="c.key">
          {{ c.label }}
        </el-checkbox>
      </el-checkbox-group>
      <div class="cp-hint" style="margin-top: 6px">
        默认显示的就是产品要求的 8 列；其余列（今日统计、最近使用、创建时间、备注…）按需打开。
      </div>
    </el-popover>

    <el-divider direction="vertical" />

    <el-dropdown trigger="click" @command="onTestCommand">
      <el-button :icon="Odometer">
        测试<el-icon class="el-icon--right"><ArrowDown /></el-icon>
      </el-button>
      <template #dropdown>
        <el-dropdown-menu>
          <el-dropdown-item command="tcp" :icon="Timer">测试 TCP 延迟</el-dropdown-item>
          <el-dropdown-item command="real" :icon="Timer">测试真连接延迟</el-dropdown-item>
          <el-dropdown-item command="speed" :icon="Odometer">测试速度</el-dropdown-item>
          <el-dropdown-item command="exit" :icon="Filter">检测出口 IP</el-dropdown-item>
          <el-dropdown-item v-if="testing" divided command="cancel">停止当前测试</el-dropdown-item>
        </el-dropdown-menu>
      </template>
    </el-dropdown>

    <el-tag v-if="testing" type="warning" size="small" effect="plain">
      <span class="cp-dot" style="background: var(--cp-warning); margin-right: 4px" />
      {{ testingLabel }}测试中…
    </el-tag>

    <div class="cp-spacer" />

    <span class="cp-dim" style="font-size: 12px">
      共 {{ store.visibleNodes.length }} 个
      <template v-if="store.nodes.length !== store.visibleNodes.length">
        （总 {{ store.nodes.length }}）
      </template>
    </span>

    <el-tag v-if="selectedCount" type="primary" size="small" effect="light" closable @close="store.clearSelection()">
      已选 {{ selectedCount }} 个
    </el-tag>
    <el-button
      v-if="selectedCount"
      link
      size="small"
      :icon="Close"
      @click="store.clearSelection()"
    >
      取消选择
    </el-button>
  </div>
</template>

<style scoped>
.cp-col-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 6px;
  font-size: 12.5px;
  font-weight: 600;
  color: var(--cp-text-2);
}
.cp-col-group {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 2px 10px;
  max-height: 300px;
  overflow-y: auto;
}
.cp-col-group :deep(.el-checkbox) {
  height: 26px;
  margin-right: 0;
}
.cp-col-group :deep(.el-checkbox__label) {
  font-size: 12.5px;
}
</style>
