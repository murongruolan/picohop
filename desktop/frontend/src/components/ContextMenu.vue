<script setup>
/**
 * 列表的右键菜单。
 *
 * 手写而不是用 el-dropdown：需要"在鼠标位置弹出、贴着屏幕边缘不溢出、
 * 点任意处即关"这套行为，用 dropdown 反而绕。
 */
import { computed } from 'vue'
import {
  Aim,
  CopyDocument,
  Delete,
  Edit,
  Link,
  Odometer,
  Refresh,
  SwitchButton,
  Timer,
  VideoPlay,
  Warning,
  Star,
} from '@element-plus/icons-vue'
import { useStore } from '../store'
import { useUi } from '../ui'
import { useActions } from '../actions'
import api, { notify } from '../api'

const store = useStore()
const ui = useUi()
const actions = useActions()

const menu = computed(() => ui.contextMenu)
const node = computed(() => menu.value.node)
const count = computed(() => store.selectedIds.length)

const isRunning = computed(() => node.value?.state === 'running')
const activateLabel = computed(() => {
  if (count.value > 1) return isRunning.value ? `停止所选 ${count.value} 个` : `激活所选 ${count.value} 个`
  return isRunning.value ? '停止节点' : '激活节点'
})

const subtitle = computed(() => {
  if (!node.value) return ''
  if (count.value > 1) return `已选 ${count.value} 个节点`
  return `${node.value.protocolLabel} · 127.0.0.1:${node.value.listenPort}`
})

function close() {
  ui.closeContextMenu()
}

/** 统一的"执行并关闭菜单"包装。 */
function run(fn) {
  return async (...args) => {
    close()
    await fn(...args)
  }
}

const doToggle = run(async () => {
  if (isRunning.value || store.selectedNodes.some((n) => n.state === 'running')) {
    await actions.deactivate(store.selectedIds, node.value)
  } else {
    await actions.activate(store.selectedIds, node.value)
  }
})

// 当前这个节点是不是"全局节点"（用来切换菜单文案）
const isGlobal = computed(() => !!node.value && store.settings?.globalNodeId === node.value.id)
const doSetGlobal = run(() => actions.setGlobalNode(node.value))

const doEdit = run(() => actions.editNode(node.value))
const doClone = run(() => actions.cloneNode(node.value))
const doRemove = run(() => actions.removeNodes(store.selectedIds, node.value))
const doRemoveInvalid = run(() => actions.removeInvalid())
const doShare = run(() => actions.share(node.value))
const doTestTCP = run(() => actions.runTest('tcp', store.selectedIds, node.value))
const doTestReal = run(() => actions.runTest('real', store.selectedIds, node.value))
const doTestSpeed = run(() => actions.runTest('speed', store.selectedIds, node.value))
const doTestExit = run(() => actions.runTest('exit', store.selectedIds, node.value))
const doCancelTest = run(() => actions.cancelTest())

const doCopyAddress = run(async () => {
  const list = store.selectedNodes.length ? store.selectedNodes : [node.value]
  const text = list.map((n) => `127.0.0.1:${n.listenPort}`).join('\n')
  try {
    await api.copyText(text)
    notify('success', '已复制本地代理地址', text)
  } catch (e) {
    notify('error', '复制失败', String(e?.message || e))
  }
})

const doResetStats = run(async () => {
  try {
    await api.resetTodayStats(store.selectedIds)
  } catch (e) {
    notify('error', '清零失败', String(e?.message || e))
  }
})
</script>

<template>
  <div
    v-if="menu.open && node"
    class="cp-menu"
    :style="{ left: menu.x + 'px', top: menu.y + 'px' }"
    @click.stop
    @contextmenu.prevent
  >
    <div class="cp-menu-title">
      <span class="cp-ellipsis">{{ node.name }}</span>
      <span class="cp-dim" style="font-size: 11px">{{ subtitle }}</span>
    </div>

    <div class="cp-menu-item" @click="doToggle">
      <el-icon><component :is="isRunning ? SwitchButton : VideoPlay" /></el-icon>
      <span>{{ activateLabel }}</span>
      <span class="cp-menu-key">回车</span>
    </div>
    <div class="cp-menu-item" @click="doSetGlobal">
      <el-icon><Star /></el-icon>
      <span>{{ isGlobal ? '取消全局节点' : '设为全局节点' }}</span>
    </div>

    <div class="cp-menu-item" @click="doEdit">
      <el-icon><Edit /></el-icon>
      <span>编辑</span>
      <span class="cp-menu-key">双击</span>
    </div>
    <div class="cp-menu-item" @click="doClone">
      <el-icon><CopyDocument /></el-icon>
      <span>克隆此项</span>
    </div>
    <div class="cp-menu-item" @click="doCopyAddress">
      <el-icon><Link /></el-icon>
      <span>复制本地代理地址</span>
    </div>

    <div class="cp-menu-sep" />

    <div class="cp-menu-item" @click="doTestTCP">
      <el-icon><Timer /></el-icon>
      <span>测试所选 TCP 延迟</span>
    </div>
    <div class="cp-menu-item" @click="doTestReal">
      <el-icon><Timer /></el-icon>
      <span>测试所选真连接延迟</span>
    </div>
    <div class="cp-menu-item" @click="doTestSpeed">
      <el-icon><Odometer /></el-icon>
      <span>测试所选速度</span>
    </div>
    <div class="cp-menu-item" @click="doTestExit">
      <el-icon><Aim /></el-icon>
      <span>检测所选出口 IP</span>
    </div>
    <div v-if="store.testing" class="cp-menu-item" @click="doCancelTest">
      <el-icon><Warning /></el-icon>
      <span>停止当前测试</span>
    </div>

    <div class="cp-menu-sep" />

    <div class="cp-menu-item" @click="doShare">
      <el-icon><Link /></el-icon>
      <span>分享</span>
    </div>
    <div class="cp-menu-item" @click="doResetStats">
      <el-icon><Refresh /></el-icon>
      <span>清零今日统计</span>
    </div>

    <div class="cp-menu-sep" />

    <div class="cp-menu-item is-danger" @click="doRemove">
      <el-icon><Delete /></el-icon>
      <span>移除所选{{ count > 1 ? `（${count} 个）` : '' }}</span>
      <span class="cp-menu-key">Del</span>
    </div>
    <div class="cp-menu-item is-danger" @click="doRemoveInvalid">
      <el-icon><Warning /></el-icon>
      <span>移除测试无效项</span>
    </div>
  </div>
</template>

<style scoped>
.cp-menu-title {
  display: flex;
  flex-direction: column;
  gap: 1px;
  padding: 5px 9px 7px;
  margin-bottom: 4px;
  border-bottom: 1px solid var(--cp-border);
  font-size: 12.5px;
  font-weight: 600;
  color: var(--cp-text);
  max-width: 100%;
}
</style>
