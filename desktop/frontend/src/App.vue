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
import { hasBridge, notify } from './api'

import TopBar from './components/TopBar.vue'
import FilterBar from './components/FilterBar.vue'
import NodeTable from './components/NodeTable.vue'
import ContextMenu from './components/ContextMenu.vue'
import LogDrawer from './components/LogDrawer.vue'
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

  // 开发期自测：-open <面板名> 时直接把面板打开（方便截图检查排版）
  handleDevOpen(store.meta?.devOpen)

  // 跟随系统主题变化
  mediaQuery = window.matchMedia('(prefers-color-scheme: dark)')
  mediaQuery.addEventListener('change', () => {
    if (store.theme === 'system') store.applyTheme()
  })

  window.addEventListener('keydown', onKeydown)
  window.addEventListener('blur', () => ui.closeContextMenu())
})

/**
 * 开发期自测开关的处理（见 desktop/main.go 的 -open 参数）。
 *
 * 生产环境下 meta.devOpen 恒为空，这段不会执行。
 */
function handleDevOpen(name) {
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

    <NodeDialog />
    <PortOnlyDialog />
    <ShareDialog />
    <SettingsDialog />
    <ImportDialog />
  </div>
</template>
