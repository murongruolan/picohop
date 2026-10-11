<script setup>
/**
 * 应用外壳。
 *
 * 结构很直白：顶部控制栏 + 筛选栏 + 列表 + 一堆对话框。
 * 所有对话框由 ui store 控制显隐，业务动作在 actions.js。
 */
import { onMounted, onBeforeUnmount } from 'vue'
// v3 运行时：这个自测入口要真去请求关窗，验证"关窗收进托盘"的钩子生效
import { Window } from '@wailsio/runtime'
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

    // 验证「拖拽列宽 + 下限钳制 + 右端不留白」。
    //
    // 手法：在表头右边缘（拖拽热区就是右边缘 8px 内）派发一串**合成鼠标事件**，
    // 走一遍真实代码路径。三次操作都要在截图里看到可核对的数字：
    //
    //   1. 把「别名」往左拖 400px  → 验证下限被钳到 56px（库内只保证 30px）
    //   2. 再拖「填充列」往左 60px → 验证它松手后仍然铺满右端（右侧留白 = 0）
    //   3. 全程量「右侧留白」= 表格容器右边缘 − 最后一列表头右边缘，期望 0px
    case 'resize': {
      const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
      await sleep(400)
      const wrap = document.querySelector('.cp-table-wrap')
      const ths = () => [...document.querySelectorAll('.cp-table-wrap .el-table__header th')]
      // 右侧留白：容器右边 − 最后一列表头右边。>0 就说明右端空了一条。
      const gapRight = () => {
        const list = ths()
        if (!wrap || !list.length) return -1
        return Math.round(wrap.getBoundingClientRect().right - list[list.length - 1].getBoundingClientRect().right)
      }
      const dragHeader = async (th, dx) => {
        const r = th.getBoundingClientRect()
        const y = Math.round(r.top + r.height / 2)
        const x0 = Math.round(r.right - 3)
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
        fire('mousemove', x0, th)
        fire('mousedown', x0, th)
        fire('mousemove', x0 + dx, document)
        fire('mouseup', x0 + dx, document)
        await sleep(400)
      }

      let list = ths()
      if (list.length < 3) {
        notify('error', '列宽拖拽测试', '表头列数不足', 0)
        break
      }

      // 1) 中间列往左拖 400px（远超列宽）→ 下限钳制
      const midTh = list[1]
      const midName = midTh.querySelector('.cell')?.textContent?.trim() || '第2列'
      const midBefore = Math.round(midTh.getBoundingClientRect().width)
      await dragHeader(midTh, -400)
      list = ths()
      const midAfter = Math.round(midTh.getBoundingClientRect().width)
      const gapAfterMid = gapRight()

      // 2) 填充列（最右一列）往左拖 60px → 松手后应当仍然铺满
      const tailTh = list[list.length - 1]
      const tailName = tailTh.querySelector('.cell')?.textContent?.trim() || '最后一列'
      const tailBefore = Math.round(tailTh.getBoundingClientRect().width)
      await dragHeader(tailTh, -60)
      list = ths()
      const tailAfter = Math.round(tailTh.getBoundingClientRect().width)
      const gapAfterTail = gapRight()

      const ok = midAfter >= 56 && midAfter <= 80 && gapAfterMid === 0 && gapAfterTail === 0
      notify(
        ok ? 'success' : 'error',
        '列宽拖拽测试',
        `1) 「${midName}」${midBefore}px → 往左拖 400px 后 ${midAfter}px` +
          `（库内下限仅 30px，我们钳到 56px）\n` +
          `2) 填充列「${tailName}」${tailBefore}px → 拖后再量 ${tailAfter}px` +
          `（松手后回到"占满剩余"，所以宽度基本不变）\n` +
          `3) 右侧留白：拖中间列后 ${gapAfterMid}px，拖填充列后 ${gapAfterTail}px（都应为 0）\n` +
          `store 记录：${JSON.stringify(store.columnWidths)}`,
        0,
      )
      break
    }

    // 验证两件事：
    //   a) 长表单（VLESS + Reality，本对话框最长的形态）底部按钮不再被窗口裁掉
    //   b) 对话框可拖动"飘开"，而且拖动被钳制在窗口内（不会被边界裁切）
    //
    // 拖拽用**合成鼠标事件**走 Element Plus 的真实拖拽实现
    // （mousedown 挂在 .el-dialog__header 上，mousemove/mouseup 挂在 document）。
    // 它的 use-draggable 在 overflow=false（默认）时会对位移做 clamp，
    // 所以往窗口外硬拖 1200x900 也应当停在窗口内。
    case 'dialogdrag': {
      const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
      ui.openNodeDialog('add', {
        protocol: 'vless',
        name: '',
        server: '',
        port: 443,
        uuid: '',
        flow: '',
        transport: { network: 'ws', path: '/p', host: 'h' },
        tls: { enabled: true, serverName: 's', alpn: [], fingerprint: 'chrome' },
      })
      await sleep(800)

      // 注意：cp-dialog 这个 class 落在 .el-dialog 自身上（属性透传），
      // 所以要写成复合选择器，不能写成祖先-后代
      const dlg = document.querySelector('.el-dialog.cp-dialog')
      const header = dlg?.querySelector('.el-dialog__header')
      if (!dlg || !header) {
        notify('error', '对话框拖动测试', '找不到对话框或标题栏', 0)
        break
      }
      const drag = async (dx, dy) => {
        const r = header.getBoundingClientRect()
        const x = Math.round(r.left + r.width / 2)
        const y = Math.round(r.top + r.height / 2)
        const fire = (type, cx, cy, target) =>
          target.dispatchEvent(
            new MouseEvent(type, {
              clientX: cx,
              clientY: cy,
              bubbles: true,
              cancelable: true,
              button: 0,
              buttons: type === 'mouseup' ? 0 : 1,
            }),
          )
        fire('mousedown', x, y, header)
        fire('mousemove', x + dx, y + dy, document)
        fire('mouseup', x + dx, y + dy, document)
        await sleep(320)
      }

      // 1) 往右下硬拖一个远超窗口的量 → 应当被钳制在窗口内
      await drag(1200, 900)
      let r = dlg.getBoundingClientRect()
      const inside =
        r.left >= -1 && r.top >= -1 && r.right <= innerWidth + 1 && r.bottom <= innerHeight + 1
      const line1 =
        `硬拖 +1200,+900 后：left=${Math.round(r.left)} top=${Math.round(r.top)} ` +
        `right=${Math.round(r.right)} bottom=${Math.round(r.bottom)}（视口 ${innerWidth}x${innerHeight}）\n` +
        `→ ${inside ? '整框仍在窗口内 ✓ 不会被裁切' : '超出窗口 ✗'}`

      // 2) 再拖到一个偏左上的位置，让截图看得出它确实"飘"离了居中位置
      await drag(-300, -30)
      r = dlg.getBoundingClientRect()
      const line2 =
        `再拖 -300,-30 后：left=${Math.round(r.left)} top=${Math.round(r.top)} ` +
        `right=${Math.round(r.right)} bottom=${Math.round(r.bottom)}`
      const footer = dlg.querySelector('.el-dialog__footer')?.getBoundingClientRect()
      const footerVisible = !!footer && footer.bottom <= innerHeight + 1 && footer.top >= -1
      const line3 = `底部按钮区 bottom=${footer ? Math.round(footer.bottom) : '?'}（视口高 ${innerHeight}）` +
        ` → ${footerVisible ? '完整可见 ✓' : '被裁掉 ✗'}`

      notify(inside && footerVisible ? 'success' : 'error', '对话框拖动 + 长表单测试',
        `${line1}\n${line2}\n${line3}`, 0)
      break
    }

    // 验证"托盘菜单那几个动作"。
    //
    // 为什么不直接点托盘菜单：那需要真的去动鼠标，而输入注入早就因为会误点到
    // 用户的其它程序而被彻底删掉了。菜单项本身只是一行 wrapper，
    // 背后就是这几个 API 方法 —— 直接调它们，等价于验证了菜单的功能。
    case 'trayactions': {
      const sleepMs = (ms) => new Promise((r) => setTimeout(r, ms))
      const lines = []

      // 1) 「复制本地代理地址」——内容我随后从程序外面用 Get-Clipboard 核对
      try {
        await api.copyFirstLocalAddress()
        lines.push('① 复制本地代理地址：已调用（剪贴板内容由外部核对）')
      } catch (e) {
        lines.push(`① 复制本地代理地址失败：${String(e?.message || e)}`)
      }

      // 2) 「停止全部节点」→「激活全部节点」
      try {
        await api.deactivateAll()
        await sleepMs(900)
        const s1 = await api.getSummary()
        await api.activateAll()
        await sleepMs(1500)
        const s2 = await api.getSummary()
        lines.push(`② 停止全部 → 运行中 ${s1.running}/${s1.total}`)
        lines.push(`③ 激活全部 → 运行中 ${s2.running}/${s2.total}`)
      } catch (e) {
        lines.push(`②③ 激活/停止失败：${String(e?.message || e)}`)
      }

      notify('primary', '托盘菜单动作测试', lines.join('\n'), 0)
      break
    }

    // 验证"关窗不退出（收进托盘）"：真心请求关闭窗口，然后由 Go 侧的
    // WindowClosing 钩子拦下、改成隐藏。预期结果是"窗口没了、进程还在、
    // 代理端口仍在监听" —— 这三件事从窗口外面都能查。
    case 'closetotray': {
      const sleepMs = (ms) => new Promise((r) => setTimeout(r, ms))
      notify('info', '关窗测试',
        '6 秒后向系统请求关闭窗口。若托盘常驻生效：窗口会消失，但进程与代理继续运行。', 0)
      await sleepMs(6000)
      await Window.Close()
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
