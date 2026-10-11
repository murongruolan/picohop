<script setup>
/**
 * 应用外壳。
 *
 * 结构很直白：顶部控制栏 + 筛选栏 + 列表 + 一堆对话框。
 * 所有对话框由 ui store 控制显隐，业务动作在 actions.js。
 */
import { onMounted, onBeforeUnmount } from 'vue'
import { useStore } from './store'
import { useUi } from './ui'
import { useActions } from './actions'
import api, { hasBridge, notify } from './api'

import TopBar from './components/TopBar.vue'
import FilterBar from './components/FilterBar.vue'
import NodeTable from './components/NodeTable.vue'
import ContextMenu from './components/ContextMenu.vue'
import LogDrawer from './components/LogDrawer.vue'
import Notifications from './components/Notifications.vue'
import NodeDialog from './components/dialogs/NodeDialog.vue'
import PortOnlyDialog from './components/dialogs/PortOnlyDialog.vue'
import ShareDialog from './components/dialogs/ShareDialog.vue'
import SettingsDialog from './components/dialogs/SettingsDialog.vue'
import ImportDialog from './components/dialogs/ImportDialog.vue'

const store = useStore()
const ui = useUi()
const actions = useActions()

let unbind = null
let mediaQuery = null

onMounted(async () => {
  if (!hasBridge()) {
    notify(
      'error',
      '后端未连接',
      '界面没有拿到 Go 后端。请用桌面程序启动，或在 Wails dev 模式下打开。',
    )
    return
  }
  await store.init()
  unbind = store.bindEvents()

  // 跟随系统主题变化
  mediaQuery = window.matchMedia('(prefers-color-scheme: dark)')
  mediaQuery.addEventListener('change', () => {
    if (store.theme === 'system') store.applyTheme()
  })

  window.addEventListener('keydown', onKeydown)
  window.addEventListener('blur', () => ui.closeContextMenu())

  // 开发期自测（-open <面板名>）放在**最后**：它可能依赖上面刚注册的监听器。
  // 踩过一次：enterkey 那一项要在合成回车事件之前就装好 keydown 监听，
  // 放前面会导致事件派发出去却没有任何人接。
  await handleDevOpen(store.meta?.devOpen)
})

/**
 * 开发期自测开关的处理（见 desktop/main.go 的 -open 参数）。
 *
 * 生产环境下 meta.devOpen 恒为空，这段不会执行。
 * 声明成 async 是因为 enterkey 那一项要先停掉节点再合成回车事件。
 */
async function handleDevOpen(name) {
  if (!name) return
  const first = store.nodes[0]
  switch (name) {
    case 'addnode':
      ui.openNodeDialog('add')
      break
    case 'edit':
      if (first) actions.editNode(first)
      break
    case 'settings':
      ui.settingsDialog = true
      break
    case 'import':
      ui.importDialog = true
      break
    case 'logs':
      ui.logDrawer = true
      break
    case 'share': {
      // 挑第一个"有通用分享格式"的节点（socks5/http/wireguard 没有）
      const shareable = store.nodes.find(
        (n) => !['socks5', 'http', 'wireguard'].includes(n.protocol),
      )
      if (shareable) actions.share(shareable)
      break
    }
    case 'portonly':
      if (first) {
        ui.openPortDialog(
          [
            {
              node: first,
              protocol: first.protocol,
              protocolLabel: first.protocolLabel,
              transport: first.transport,
              server: first.server,
              port: first.port,
              suggestedPort: first.listenPort,
              listenPortFree: true,
            },
          ],
          'clipboard',
        )
      }
      break
    case 'contextmenu':
      if (first) {
        store.selectForContext(first)
        ui.openContextMenu(170, 150, first)
      }
      break
    // 一次把 5 种通知都弹出来，用来检查类型映射、位置与层级。
    // duration 传 0 = 不自动关闭：截图工具需要几秒才就绪，默认 4 秒会先关掉。
    case 'notify':
      notify('primary', 'primary · 主色通知', '一般性的成功/中性提示', 0)
      notify('success', 'success · 成功', '节点已激活', 0)
      notify('warning', 'warning · 警告', '端口被占用，已自动改到 10813', 0)
      notify('info', 'info · 信息', '将连带激活前置节点', 0)
      notify('error', 'error · 错误', '握手失败：连接超时', 0)
      break
    // 换一套自定义列 + 打开状态筛选 + 输入搜索词，用来检查这三项筛选是否真的生效
    case 'columns':
      store.setVisibleColumns(['protocol', 'name', 'listenPort', 'state', 'today', 'lastUsed', 'remark'])
      store.setStatusFilter('active')
      store.searchText = '2'
      store.applySearch()
      break

    // 验证"回车激活"这条路径：先把节点全停掉，选中一行，
    // 再派发一个**合成的键盘事件**（纯 JS，不涉及任何系统级输入注入）。
    //
    // 开头那 1.5 秒等待是必须的：后端启动时会延迟 300ms 做一次"自动激活上次
    // 激活的节点"，不等它跑完就停节点，它随后又会把节点全部拉起来，
    // 于是截图里看不出"回车到底有没有生效"（第一次就是这么误判的）。
    case 'enterkey':
      if (first) {
        await new Promise((r) => setTimeout(r, 1500))
        await api.deactivateAll()
        store.clearSelection()
        await store.refresh()
        store.selectRow(store.nodes[0], {})
        window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
      }
      break

    // 验证"拖拽列宽 + 下限钳制"。
    //
    // 手法：在表头右边缘（拖拽热区是右边缘 8px 内）派发一串**合成鼠标事件**，
    // 把某一列往左拖 400px —— 远超该列宽度，不钳制的话会被拖成 30px 甚至更窄。
    // Element Plus 内部只保证不低于 30px，我们自己钳到 56px，
    // 所以期望结果是 56px。测完把实际宽度打到通知里，方便看截图核对。
    case 'resize': {
      await new Promise((r) => setTimeout(r, 400))
      const ths = [...document.querySelectorAll('.cp-table-wrap .el-table__header th')]
      const th = ths[1] // 第二列 = 别名
      if (!th) {
        notify('error', '列宽拖拽测试', '找不到表头单元格', 0)
        break
      }
      const before = Math.round(th.getBoundingClientRect().width)
      const rect = th.getBoundingClientRect()
      const y = Math.round(rect.top + rect.height / 2)
      const startX = Math.round(rect.right - 3)
      const fire = (type, x, target) =>
        target.dispatchEvent(
          new MouseEvent(type, {
            clientX: x,
            clientY: y,
            bubbles: true,
            cancelable: true,
            button: 0,
            buttons: type === 'mouseup' ? 0 : 1,
          }),
        )
      fire('mousemove', startX, th) // 让库把 draggingColumn 记上
      fire('mousedown', startX, th)
      fire('mousemove', startX - 400, document) // 往左拖 400px
      fire('mouseup', startX - 400, document)
      await new Promise((r) => setTimeout(r, 400))
      const after = Math.round(th.getBoundingClientRect().width)
      const saved = store.columnWidths?.name

      // 再试**最后一列**：Element Plus 默认不允许拖最后一列（allowDragLastColumn），
      // 我们显式开了它，所以这里应该也能变窄。
      const lastTh = ths[ths.length - 1]
      const lastKey = lastTh?.querySelector('.cell')?.textContent?.trim() || '最后一列'
      let lastBefore = 0
      let lastAfter = 0
      if (lastTh) {
        const lr = lastTh.getBoundingClientRect()
        lastBefore = Math.round(lr.width)
        const ly = Math.round(lr.top + lr.height / 2)
        const lx = Math.round(lr.right - 3)
        const fire2 = (type, x, target) =>
          target.dispatchEvent(
            new MouseEvent(type, {
              clientX: x,
              clientY: ly,
              bubbles: true,
              cancelable: true,
              button: 0,
              buttons: type === 'mouseup' ? 0 : 1,
            }),
          )
        fire2('mousemove', lx, lastTh)
        fire2('mousedown', lx, lastTh)
        fire2('mousemove', lx - 60, document) // 只缩 60px，验证"正常拖拽"也能用
        fire2('mouseup', lx - 60, document)
        await new Promise((r) => setTimeout(r, 400))
        lastAfter = Math.round(lastTh.getBoundingClientRect().width)
      }

      const ok = after >= 56 && after <= 80 && lastTh && lastAfter < lastBefore
      notify(
        ok ? 'success' : 'error',
        '列宽拖拽测试',
        `别名列：${before}px → 往左拖 400px 后 ${after}px\n` +
          `（库内下限 30px，我们钳到 56px；store 记录 = ${saved}）\n` +
          `最后一列「${lastKey}」：${lastBefore}px → ${lastAfter}px（验证 allow-drag-last-column）`,
        0,
      )
      break
    }
  }
}

onBeforeUnmount(() => {
  if (unbind) unbind()
  window.removeEventListener('keydown', onKeydown)
})

/**
 * 全局快捷键。
 *
 * 回车激活是产品明确要求的操作方式；另外补几个顺手的（Ctrl+A 全选、
 * Delete 移除、Esc 关菜单）。
 */
function onKeydown(e) {
  // 光标在输入框里时不要抢键
  const tag = (e.target?.tagName || '').toLowerCase()
  const typing = tag === 'input' || tag === 'textarea' || e.target?.isContentEditable

  if (e.key === 'Escape') {
    ui.closeContextMenu()
    return
  }
  if (typing) return

  if (e.key === 'Enter') {
    // 有对话框开着时不触发，否则会在点"确定"的同时又去激活节点
    if (ui.nodeDialog.open || ui.portDialog.open || ui.shareDialog.open || ui.settingsDialog) return
    if (ui.contextMenu.open) return
    if (store.selectedIds.length) {
      e.preventDefault()
      actions.activate(store.selectedIds)
    }
    return
  }
  if (e.key === 'Delete') {
    if (store.selectedIds.length) {
      e.preventDefault()
      actions.removeNodes(store.selectedIds)
    }
    return
  }
  if (e.key === 'a' && (e.ctrlKey || e.metaKey)) {
    e.preventDefault()
    store.selectAllVisible()
    return
  }
  if (e.key === 'F5') {
    e.preventDefault()
    store.refresh()
  }
}
</script>

<template>
  <div class="cp-app" @click="ui.closeContextMenu()">
    <TopBar />
    <FilterBar />
    <NodeTable />

    <ContextMenu />
    <LogDrawer />
    <Notifications />

    <NodeDialog />
    <PortOnlyDialog />
    <ShareDialog />
    <SettingsDialog />
    <ImportDialog />
  </div>
</template>
