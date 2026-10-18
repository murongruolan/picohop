<script setup>
/**
 * 列表的右键菜单。
 *
 * 手写而不是用 el-dropdown：需要"在鼠标位置弹出、贴着屏幕边缘不溢出、
 * 点任意处即关"这套行为，用 dropdown 反而绕。
 *
 * 两种菜单共用同一份坐标与开关状态（ui.contextMenu）：
 * 节点菜单（node）与订阅组头菜单（group）—— 同时只可能有一个开着。
 */
import { computed } from 'vue'
import {
  Aim,
  Bottom,
  CopyDocument,
  Delete,
  Edit,
  Link,
  Odometer,
  Refresh,
  SwitchButton,
  Timer,
  Top,
  VideoPlay,
  Warning,
  Star,
} from '@element-plus/icons-vue'
import { ElMessageBox } from 'element-plus'
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

// ------------------------------------------------------------------
// 组头（订阅）菜单
// ------------------------------------------------------------------
const group = computed(() => menu.value.group)
const groupSub = computed(() => group.value?.sub?.subscription || null)
const groupOpen = computed(() => (group.value ? store.isGroupOpen(group.value.id) : false))
const groupCount = computed(() => group.value?.nodes?.length || 0)

/** 确认框：取消不当作错误。 */
async function confirmTo(message, title) {
  try {
    await ElMessageBox.confirm(message, title, {
      type: 'warning',
      confirmButtonText: '确定',
      cancelButtonText: '取消',
    })
    return true
  } catch {
    return false
  }
}

const doGroupToggle = run(() => {
  store.toggleGroup(group.value.id)
})

const doGroupShowAll = run(() => {
  store.toggleGroupShowAll(group.value.id)
})

const doGroupRefresh = run(async () => {
  await store.refreshSubscription(group.value.id)
})

const doGroupRename = run(async () => {
  let name
  try {
    const r = await ElMessageBox.prompt('给这个分组起个名字', '重命名订阅', {
      inputValue: groupSub.value?.name || '',
      confirmButtonText: '保存',
      cancelButtonText: '取消',
      inputValidator: (v) => (String(v || '').trim() ? true : '名字不能为空'),
    })
    name = String(r.value || '').trim()
  } catch {
    return
  }
  try {
    await store.saveSubscriptionMeta({ ...groupSub.value, name })
    notify('success', '已重命名', name)
  } catch (e) {
    notify('error', '重命名失败', String(e?.message || e))
  }
})

const doGroupInterval = run(async () => {
  let v
  try {
    const r = await ElMessageBox.prompt('多久自动更新一次（分钟）。填 0 表示不自动更新。', '自动更新间隔', {
      inputValue: String(groupSub.value?.updateMinutes || 0),
      confirmButtonText: '保存',
      cancelButtonText: '取消',
      inputValidator: (x) => (/^\d+$/.test(String(x ?? '').trim()) ? true : '请填 0 或正整数'),
    })
    v = Number(String(r.value || '').trim())
  } catch {
    return
  }
  try {
    await store.saveSubscriptionMeta({ ...groupSub.value, updateMinutes: v })
    notify('success', '已保存更新间隔', v > 0 ? `每 ${v} 分钟` : '不自动更新')
  } catch (e) {
    notify('error', '保存失败', String(e?.message || e))
  }
})

const doGroupClearMissing = run(async () => {
  const n = groupSub.value?.missing || 0
  if (!(await confirmTo(`这条订阅里有 ${n} 个节点已被机场移除，确定清理掉？`, '清理已下架的节点'))) return
  try {
    const removed = await store.clearMissingNodes(group.value.id)
    notify('success', '已清理', `${removed} 个已下架的节点`)
  } catch (e) {
    notify('error', '清理失败', String(e?.message || e))
  }
})

const doGroupOpenPanel = run(async () => {
  const u = groupSub.value?.webPageUrl
  if (!u) return
  try {
    await api.openPath(u)
  } catch (e) {
    notify('error', '打开失败', String(e?.message || e))
  }
})

const doGroupCopyURL = run(async () => {
  const u = groupSub.value?.url
  if (!u) return
  try {
    await api.copySensitiveText(u)
    notify('success', '已复制订阅链接', '链接含你的账号信息，别发给别人')
  } catch (e) {
    notify('error', '复制失败', String(e?.message || e))
  }
})

const doGroupDelete = (alsoNodes) =>
  run(async () => {
    const msg = alsoNodes
      ? `「${group.value.name}」和它的 ${groupCount.value} 个节点都会被删除，不可撤销。`
      : `只删除「${group.value.name}」这条订阅记录，${groupCount.value} 个节点会保留成手动节点。`
    if (!(await confirmTo(msg, alsoNodes ? '删除订阅并移除节点' : '删除订阅'))) return
    try {
      await store.deleteSubscription(group.value.id, alsoNodes)
      notify('success', '已删除订阅', group.value.name)
    } catch (e) {
      notify('error', '删除失败', String(e?.message || e))
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

  <!-- 组头（订阅）菜单 -->
  <div
    v-else-if="menu.open && group"
    class="cp-menu"
    :style="{ left: menu.x + 'px', top: menu.y + 'px' }"
    @click.stop
    @contextmenu.prevent
  >
    <div class="cp-menu-title">
      <span class="cp-ellipsis">{{ group.name }}</span>
      <span class="cp-dim" style="font-size: 11px">
        {{ group.isManual ? '手工添加的节点' : `${groupCount} 个节点` }}
      </span>
    </div>

    <div class="cp-menu-item" @click="doGroupToggle">
      <el-icon><component :is="groupOpen ? Bottom : Top" /></el-icon>
      <span>{{ groupOpen ? '收起这一组' : '展开这一组' }}</span>
    </div>
    <div v-if="groupCount > 200" class="cp-menu-item" @click="doGroupShowAll">
      <el-icon><Bottom /></el-icon>
      <span>{{ store.groupShowAll.includes(group.id) ? '只看前 200 个' : `显示全部 ${groupCount} 个` }}</span>
    </div>

    <template v-if="!group.isManual">
      <div class="cp-menu-sep" />

      <div class="cp-menu-item" @click="doGroupRefresh">
        <el-icon><Refresh /></el-icon>
        <span>更新订阅</span>
      </div>
      <div class="cp-menu-item" @click="doGroupRename">
        <el-icon><Edit /></el-icon>
        <span>重命名…</span>
      </div>
      <div class="cp-menu-item" @click="doGroupInterval">
        <el-icon><Timer /></el-icon>
        <span>自动更新间隔…</span>
      </div>
      <div v-if="groupSub?.webPageUrl" class="cp-menu-item" @click="doGroupOpenPanel">
        <el-icon><Link /></el-icon>
        <span>打开机场面板</span>
      </div>
      <div class="cp-menu-item" @click="doGroupCopyURL">
        <el-icon><CopyDocument /></el-icon>
        <span>复制订阅链接</span>
      </div>

      <template v-if="groupSub?.missing">
        <div class="cp-menu-sep" />
        <div class="cp-menu-item" @click="doGroupClearMissing">
          <el-icon><Warning /></el-icon>
          <span>清理已下架的 {{ groupSub.missing }} 个</span>
        </div>
      </template>

      <div class="cp-menu-sep" />

      <div class="cp-menu-item is-danger" @click="doGroupDelete(false)">
        <el-icon><Delete /></el-icon>
        <span>删除订阅（保留节点）</span>
      </div>
      <div class="cp-menu-item is-danger" @click="doGroupDelete(true)">
        <el-icon><Delete /></el-icon>
        <span>删除订阅并移除节点</span>
      </div>
    </template>
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
