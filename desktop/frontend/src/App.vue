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
import BottomPanel from './components/BottomPanel.vue'
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

/**
 * 浮动窗口模式：非空表示"这个窗口是某个对话框的独立窗口"。
 *
 * 同一个 App 组件被两种方式挂载（见 main.js）：
 *   - 主窗口：floatKind 为空，显示整套界面
 *   - 浮动窗口：floatKind = 'node' 之类，只打开那一个对话框，
 *     并由 CSS（body.cp-float-window）把主界面藏起来、让对话框铺满窗口
 */
const props = defineProps({
  floatKind: { type: String, default: '' },
})

let unbind = null
let mediaQuery = null

/**
 * 浮动窗口模式下的初始化：只做"打开指定对话框 + 结束时把结果交回 Go"。
 *
 * 之所以能这么短，是因为复用了同一个对话框组件 —— 它的校验、端口检测、
 * 保存逻辑统统不用重写，差别只有"关掉时通知 Go"这一步。
 */
async function enterFloatMode(kind) {
  // 告诉对话框"拖动由整个窗口负责"：浮动窗口里必须关掉 Element Plus 的
  //   draggable，否则两套位移叠加 —— 卡片跟着鼠标跑、松开还黏在鼠标上。
  ui.floatMode = true
  let payload = {}
  try {
    const raw = await api.getFloatPayload()
    if (raw) payload = JSON.parse(raw)
  } catch (e) {
    // 没有初始数据也能开（例如"新增"）
    console.warn('读取浮动对话框初始数据失败：', e)
  }

  if (kind !== 'node') {
    // 未知类型：立刻回传，别让主窗口那侧一直 await 着
    api.submitFloatDialog('').catch(() => {})
    return
  }

  // 对话框只要一关（确定或取消）就回传结果。
  // 必须"两条路都回传"，否则 Go 侧的 <-result 会永远挂着。
  ui.onNodeDialogClosed = (result) => {
    const payloadOut = result || { submitted: false }
    api.submitFloatDialog(JSON.stringify(payloadOut)).catch(() => {})
  }
  ui.openNodeDialog(payload.mode === 'edit' ? 'edit' : 'add', payload.node || null)
}

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

  // 【对话框右上角的关闭按钮不能被"拖动对话框"吃掉】
  //
  // Element Plus 的 draggable 把 mousedown 挂在**标题栏**上，而关闭按钮是标题栏的
  // 子元素 —— 按住 X 的时候鼠标只要有一两个像素的位移，就被当成"拖动标题栏"，
  // 结果是"点了关闭却关不掉、弹窗自己往下跑"（用户实报，偶发就是因为那点位移）。
  //
  // 用一个**捕获阶段**的监听在事件到达标题栏之前截住它（捕获先于冒泡），
  // 只对关闭按钮生效，标题栏其余部分照常可以拖动。
  // 放在 App.vue 里是对所有对话框生效 —— 带 draggable 的都有这个隐患。
  document.addEventListener(
    'mousedown',
    (e) => {
      if (e.target?.closest?.('.el-dialog__headerbtn')) e.stopPropagation()
    },
    true,
  )

  // 浮动窗口：只打开那一个对话框就够了
  if (props.floatKind) {
    await enterFloatMode(props.floatKind)

    // 自测参数（由主窗口透传过来）：等对话框挂好之后跑一遍，
    // 用来验证"在浮动窗口里结束对话框 → 结果回传 → Go 唤醒等待方"这条链路。
    const devParam = new URLSearchParams(window.location.search).get('devopen')
    const runDev = async () => {
      if (!devParam) return
      await new Promise((r) => setTimeout(r, 900))
      try {
        await handleDevOpen(devParam)
      } catch (e) {
        notify('error', '浮动窗口自测出错', `${devParam}：${String(e?.message || e)}`, 0)
      }
    }

    // 【复用同一个浮动窗口时的入口】
    //
    // 窗口建一次就留着（关闭只隐藏），下次打开时 Go 侧调这个全局函数唤醒我们，
    // 而不是重新建窗口、重新加载整个前端 —— 那两步正是"打开卡一下"的来源。
    // 这里重新走一遍 enterFloatMode：它会重置浮动模式、重新取新的 payload、
    // 再把对话框打开，等于"这个窗口又弹了一次"。
    //
    // 自测参数也要重跑一遍：否则复用打开时没人去结束这个对话框，
    // 主窗口那侧就会一直 await 着（自测会表现为"卡住不返回"）。
    window.__cpFloatReopen = () => {
      enterFloatMode(props.floatKind)
        .then(runDev)
        .catch((e) => notify('error', '浮动窗口复用失败', String(e?.message || e), 0))
    }

    await runDev()
    return
  }

  // 开发期自测（-open <面板名>）放在**最后**：它可能依赖上面刚注册的监听器。
  // 踩过一次：enterkey 那一项要在合成回车事件之前就装好 keydown 监听，
  // 放前面会导致事件派发出去却没有任何人接。
  //
  // 自测出错必须**看得见**：否则 onMounted 的 promise 静默 reject，
  // 表现成"界面起来了但什么也没发生"，排查起来毫无线索。真踩过。
  try {
    await handleDevOpen(store.meta?.devOpen)
  } catch (e) {
    notify('error', '自测用例出错', `${store.meta?.devOpen}：${String(e?.message || e)}`, 0)
  }
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
      // 走真实路径：现在「添加节点」是开在**独立浮动窗口**里的
      actions.addNode()
      break
    case 'edit':
      if (first) actions.editNode(first)
      break
    case 'settings':
      ui.settingsDialog = true
      break
    case 'shortcuts':
      // 直接停在「快捷键」页，方便截图核对
      ui.settingsInitialTab = 'shortcut'
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

    // 验证"运行期切换标题栏明暗"。
    //
    // 这一条在 v3 下必须自己实现：v3 只在**创建窗口时**用 WindowsWindow{Theme}
    // 指定明暗，没有运行期 setter（v2 有 runtime.WindowSetDarkTheme）。
    // 所以后端直接调 DwmSetWindowAttribute，这个入口就是去看它到底有没有生效。
    case 'theme': {
      const sleepMs = (ms) => new Promise((r) => setTimeout(r, ms))
      const ok1 = await api.setWindowTheme('dark')
      store.theme = 'dark'
      store.applyTheme()
      await sleepMs(800)
      const ok2 = await api.setWindowTheme('light')
      await sleepMs(400)
      // 留在深色上，方便截图核对标题栏
      const ok3 = await api.setWindowTheme('dark')
      await sleepMs(600)
      notify(ok1 && ok2 && ok3 ? 'success' : 'error', '标题栏明暗切换测试',
        `setWindowTheme(dark)=${ok1} → (light)=${ok2} → (dark)=${ok3}\n` +
        `（v3 无运行期 setter，后端用 DwmSetWindowAttribute 实现；返回 false 说明没找到窗口）`, 0)
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

    // 【在浮动窗口里跑】模拟用户点了对话框的"取消"：
    // 走的是对话框自己的关闭路径 → ui.onNodeDialogClosed → SubmitFloatDialog。
    // 主窗口那边应当立刻收到结果，并且这个窗口会自己关掉。
    case 'floatsubmit':
      ui.closeNodeDialog({ submitted: false, via: 'float-self-test' })
      break

    // 验证「快捷键录制」这条交互：合成按键事件走真实录制逻辑。
    // 包括用户明确要求的那条规则 —— 只按一个键不算，必须两个以上。
    case 'keyrecord': {
      const wait = (ms) => new Promise((r) => setTimeout(r, ms))
      ui.settingsInitialTab = 'shortcut'
      ui.settingsDialog = true
      await wait(900)
      const box = () => document.querySelector('.cp-shortcut-input')
      const el = box()
      if (!el) {
        notify('error', '快捷键录制测试', '找不到录制框', 0)
        break
      }
      const lines = [`初始显示：${el.textContent.trim()}`]

      // ① 只按一个普通键 —— 应当被拒绝（"需要两个以上组合键"）
      el.click()
      await wait(150)
      el.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', bubbles: true, cancelable: true }))
      await wait(250)
      lines.push(`① 只按 K → 仍是「${box().textContent.trim()}」`)

      // ②' 空格：必须记成 Space，不能是一个裸空格
      box().dispatchEvent(
        new KeyboardEvent('keydown', { key: ' ', ctrlKey: true, altKey: true, bubbles: true, cancelable: true }),
      )
      await wait(300)
      const spaceShown = box().textContent.trim()
      lines.push(`②' Ctrl+Alt+空格 → 「${spaceShown}」（应为 Ctrl+Alt+Space）`)

      // ② 按 Ctrl+Shift+K —— 应当被记下来
      //    注意：上一步（空格）结束时录制已经结束，这里必须重新点一下录制框，
      //    否则这次按键根本没人接 —— 我第一版就是漏了这一步，自测误报成失败。
      box().click()
      await wait(150)
      box().dispatchEvent(
        new KeyboardEvent('keydown', { key: 'k', ctrlKey: true, shiftKey: true, bubbles: true, cancelable: true }),
      )
      await wait(300)
      const after = box().textContent.trim()
      lines.push(`② 按 Ctrl+Shift+K → 「${after}」`)

      // ③ 清除 —— 应当变成"未启用"（后端约定的 none）
      const clearBtn = document.querySelectorAll('.cp-shortcut-row button')[0]
      clearBtn?.click()
      await wait(300)
      const cleared = box().textContent.trim()
      lines.push(`③ 点清除 → 「${cleared}」`)

      const ok = after === 'Ctrl+Shift+K' && cleared.includes('未启用') && spaceShown === 'Ctrl+Alt+Space'
      notify(ok ? 'success' : 'error', '快捷键录制测试', lines.join('\n'), 0)
      break
    }

    // 打开「导入」下拉，方便截图核对条目的大小与间距（Element 下拉是 teleport 的，
    // 没法用 scoped 样式改，必须看实际渲染）
    case 'importmenu': {
      const wait = (ms) => new Promise((r) => setTimeout(r, ms))
      await wait(400)
      const trigger = document.querySelector('.cp-toolbar .el-dropdown .el-tooltip__trigger')
      if (!trigger) {
        notify('error', '导入菜单自测', '找不到触发按钮', 0)
        break
      }
      trigger.click()
      await wait(300)
      const items = document.querySelectorAll('.cp-dropdown .el-dropdown-menu__item')
      notify(
        items.length ? 'success' : 'error',
        '导入菜单自测',
        `下拉条目数 = ${items.length}；首项高度 = ${items[0]?.getBoundingClientRect().height ?? '?'}px`,
        0,
      )
      break
    }

    // 底部栏目自测：展开到日志视图（截图核对用）
    case 'bottompanel': {
      // 先切到别的视图再切日志：直接设 "logs" 时若本来就停在日志视图，
      // 面板会按"点当前视图 = 收起"的逻辑收起来（我第一次自测就栽在这里）。
      ui.bottomPanelView = 'traffic'
      await new Promise((r) => setTimeout(r, 500))
      ui.bottomPanelView = 'logs'
      await new Promise((r) => setTimeout(r, 1200))
      break
    }

    // 底部栏目：展开到流量统计（截图核对用）
    case 'trafficpanel':
    case 'trafficnode': {
      const wait = (ms) => new Promise((r) => setTimeout(r, ms))
      ui.bottomPanelView = 'logs'
      await wait(400)
      ui.bottomPanelView = 'traffic'
      await wait(2000)
      break
    }

    // 底部栏：展开到「日志 → 网络日志」（截图核对用）
    case 'netlogpanel':
      ui.bottomPanelView = 'traffic'
      await new Promise((r) => setTimeout(r, 400))
      ui.bottomPanelView = 'logs'
      await new Promise((r) => setTimeout(r, 300))
      ui.bottomLogTab = 'net'
      await new Promise((r) => setTimeout(r, 1500))
      break

    // 底部栏：展开到「网站测试」（截图核对用）
    case 'sitespanel': {
      ui.bottomPanelView = 'logs'
      await new Promise((r) => setTimeout(r, 400))
      ui.bottomPanelView = 'sites'
      await new Promise((r) => setTimeout(r, 2500))
      // 真的点一下「全部测试」：只有跑过才知道 SOCKS5 客户端与整条链路通不通
      // 先把「矩阵」打开（让测试跑遍所有节点）再全部测试
      const sw = document.querySelector('.cp-sites-bar .el-switch')
      if (sw && !sw.classList.contains('is-checked')) sw.click()
      await new Promise((r) => setTimeout(r, 500))
      const btn = [...document.querySelectorAll('.cp-sites-bar button')].find((b) =>
        b.textContent.includes('全部测试'),
      )
      if (btn) btn.click()
      await new Promise((r) => setTimeout(r, 16000))
      break
    }

    // 底部栏：展开到「连接」（截图核对用）。
    // 参数 closeall 时再点一下"关闭全部"，验证按钮真的会关连接而不只是清列表。
    case 'connpanel':
    case 'connpanel-closeall': {
      ui.bottomPanelView = 'logs'
      await new Promise((r) => setTimeout(r, 400))
      ui.bottomPanelView = 'connections'
      await new Promise((r) => setTimeout(r, 4000))
      if (name === 'connpanel-closeall') {
        const btn = [...document.querySelectorAll('.cp-conn-bar button')].find((b) =>
          b.textContent.includes('关闭全部'),
        )
        if (btn) btn.click()
        await new Promise((r) => setTimeout(r, 2500))
        // 关完之后切到「已关闭」，看看最终字节与时长有没有落下来
        const closedTab = [...document.querySelectorAll('.cp-conn-tab')].find((b) =>
          b.textContent.includes('已关闭'),
        )
        if (closedTab) closedTab.click()
        await new Promise((r) => setTimeout(r, 2500))
      }
      break
    }

    // 底部栏：展开到「网络设置」，改一项并保存（验证真的写进配置了）
    case 'netpanel': {
      const wait = (ms) => new Promise((r) => setTimeout(r, ms))
      ui.bottomPanelView = 'logs'
      await wait(400)
      ui.bottomPanelView = 'network'
      await wait(1500)

      const inputs = [...document.querySelectorAll('.cp-net-form input')]
      if (inputs.length < 2) {
        notify('error', '网络设置自测', `只找到 ${inputs.length} 个输入框`, 0)
        break
      }
      // 用原生 setter 再派发 input 事件 —— 直接改 .value 的话 Vue 的 v-model 收不到
      const setVal = (el, v) => {
        const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set
        setter.call(el, String(v))
        el.dispatchEvent(new Event('input', { bubbles: true }))
      }
      // 注意取**最后一个**输入框：el-switch 内部也有一个 input（勾选框），
      // 所以 inputs[0] 是它、inputs[1] 是起始端口、最后一个才是空闲自动断开。
      // 我第一版就打在了起始端口上 —— 反倒验证了后端的校验会拦住非法端口。
      const idleInput = inputs[inputs.length - 1]
      const before = idleInput.value
      setVal(idleInput, 240)
      await wait(400)
      const btn = [...document.querySelectorAll('.cp-net-form button')].find((b) =>
        b.textContent.includes('保存'),
      )
      if (!btn) {
        notify('error', '网络设置自测', '找不到保存按钮', 0)
        break
      }
      notify('info', '网络设置自测', `空闲超时 ${before} → 240，正在保存…`, 0)
      btn.click()
      await wait(2000)
      break
    }

    // 浮动窗口：把"标题栏上方那条白边"的来源查出来（量计算样式，不猜）
    case 'floatlook': {
      const wait = (ms) => new Promise((r) => setTimeout(r, ms))
      await wait(1200)
      const g = (el, ...props) =>
        el ? props.map((p) => `${p}=${getComputedStyle(el)[p]}`).join(' ') : '(无)'
      const rect = (el) => {
        if (!el) return '(无)'
        const r = el.getBoundingClientRect()
        return `top=${Math.round(r.top)} left=${Math.round(r.left)} h=${Math.round(r.height)} w=${Math.round(r.width)}`
      }
      const body = document.body
      const overlayDialog = document.querySelector('.el-overlay-dialog')
      const dlg = document.querySelector('.el-dialog.cp-dialog')
      const head = document.querySelector('.el-dialog.cp-dialog .el-dialog__header')
      const lines = [
        `body: margin=${getComputedStyle(body).margin} padding=${getComputedStyle(body).padding} ${rect(body)}`,
        `#app: ${rect(document.getElementById('app'))}`,
        `.el-overlay-dialog: ${g(overlayDialog, 'display', 'padding', 'alignItems')} ${rect(overlayDialog)}`,
        `.el-dialog: ${g(dlg, 'padding', 'margin', 'background')} ${rect(dlg)}`,
        `.el-dialog__header: ${g(head, 'margin', 'padding', 'background', 'height')} ${rect(head)}`,
      ]
      notify('info', '浮动窗口尺寸诊断', lines.join('\n'), 0)
      break
    }

    // 全局节点标签自测：设第一个节点为全局节点 + 展开底栏（截图核对用）
    case 'globaltag': {
      const wait = (ms) => new Promise((r) => setTimeout(r, ms))
      const n = (store.nodes || [])[0]
      if (!n) {
        notify('error', '全局节点自测', '没有节点', 0)
        break
      }
      await actions.setGlobalNode(n)
      await wait(600)
      ui.bottomPanelView = 'logs'
      await wait(1200)
      break
    }

    // 系统代理自测入口：由 scripts/test-sysproxy.ps1 驱动。
    // 拆成两个独立动作（而不是一个"跑完整流程"），因为**强杀**必须由脚本在进程外做。
    case 'sysproxyon':
    case 'sysproxyoff': {
      const wait = (ms) => new Promise((r) => setTimeout(r, ms))
      if (name === 'sysproxyon') {
        // 开之前先满足前置条件：有全局节点、且它在运行。
        // 不满足时后端会明确报错（而不是"点了没反应"），这里把错误原样弹出来。
        const n = (store.nodes || [])[0]
        if (n && store.settings?.globalNodeId !== n.id) {
          await actions.setGlobalNode(n)
          await wait(800)
        }
      }
      try {
        await api.setSystemProxyMode(name === 'sysproxyon')
        notify(
          'success',
          '系统代理自测',
          name === 'sysproxyon' ? '已开启（系统代理已指向全局节点）' : '已关闭（已还原）',
          0,
        )
      } catch (e) {
        notify('error', '系统代理自测', String(e?.message || e), 0)
      }
      await wait(800)
      break
    }

    // 日志复制自测：点「复制」后读回剪贴板，验证真的复制到了。
    // 顺便把日志区的 computed user-select 报出来（选不中就是它在作怪）。
    case 'logcopy': {
      const wait = (ms) => new Promise((r) => setTimeout(r, ms))
      // 先切到别的视图再切回日志：直接设 "logs" 时若本来就在日志视图，
      // 会走成"点当前视图 = 收起"（老陷阱，别的入口改过、这个漏了）。
      ui.bottomPanelView = 'traffic'
      await wait(400)
      ui.bottomPanelView = 'logs'
      await wait(2000)
      const box = document.querySelector('.cp-logs-box')
      const us = box ? getComputedStyle(box).userSelect : '(no box)'
      const btn = [...document.querySelectorAll('.cp-logs-bar button')].find((b) =>
        b.textContent.includes('\u590d\u5236'),
      )
      if (!btn) {
        notify('error', '日志复制自测', `找不到复制按钮（user-select=${us}）`, 0)
        break
      }
      btn.click()
      await wait(600)
      const got = String((await api.getClipboard()) || '')
      notify(
        got ? 'success' : 'error',
        '日志复制自测',
        `user-select=${us}
剪贴板长度=${got.length}
前 60 字：${got.slice(0, 60)}`,
        0,
      )
      break
    }

    // 连开两次浮动对话框：第一次是新建（要建 WebView2 + 加载前端），
    // 第二次应该走**复用**（窗口已在、不重新加载）。
    // 这是用户报的“打开还是卡”那条，两边的耗时在 Go 日志里（"~前端就绪 Xms"~
    case 'floatreuse': {
      const wait = (ms) => new Promise((r) => setTimeout(r, ms))
      const payload = JSON.stringify({ mode: 'add', node: null })
      const out = []
      for (let i = 1; i <= 2; i++) {
        const t0 = performance.now()
        // devopen=floatsubmit：浮动窗口里的前端会自动提交，所以这一次能自己结束
        const r = await api.openFloatDialog('node', payload, 'floatsubmit')
        out.push(`第 ${i} 次：${Math.round(performance.now() - t0)}ms → ${String(r).slice(0, 40)}`)
        if (i === 1) {
          // 第一次结束后把窗口挪到左上角，看看第二次打开会不会复位到主窗口中央。
          // 用户实报："关闭后再次打开，不要在上次关闭的位置打开"。
          await api.moveFloatWindow(60, 60)
          await wait(400)
          const moved = await api.floatWindowBounds()
          out.push(`  挪到：(${moved.x},${moved.y})`)
        }
      }
      const main = await api.mainWindowBounds()
      const f = await api.floatWindowBounds()
      const wantX = Math.round(main.x + (main.width - f.width) / 2)
      const wantY = Math.round(main.y + (main.height - f.height) / 2)
      const ok = Math.abs(f.x - wantX) <= 2 && Math.abs(f.y - wantY) <= 2
      out.push(`主窗口：(${main.x},${main.y}) ${main.width}x${main.height}`)
      out.push(`最终位置：(${f.x},${f.y})，居中应为：(${wantX},${wantY})`)
      notify(ok ? 'success' : 'error', '浮动窗口复用 + 居中', out.join('\n'), 0)
      break
    }

    // 验证「设置面板每次打开都回到默认位置」。
    // 手法：打开 → 合成一次标题栏拖动（真的把它拖走）→ 读位移 → 关闭 → 重开 → 再读位移。
    case 'settingsreset': {
      const wait = (ms) => new Promise((r) => setTimeout(r, ms))
      const dlgEl = () => document.querySelector('.el-dialog.cp-dlg-settings')
      const tf = () => dlgEl()?.style.transform || '（空）'

      ui.settingsDialog = true
      await wait(900)
      const before = tf()
      const header = dlgEl()?.querySelector('.el-dialog__header')
      if (!header) {
        notify('error', '设置面板位置重置测试', '找不到标题栏', 0)
        break
      }
      const r = header.getBoundingClientRect()
      const x = Math.round(r.left + 80)
      const y = Math.round(r.top + r.height / 2)
      const fire = (type, cx, cy, target) =>
        target.dispatchEvent(
          new MouseEvent(type, {
            clientX: cx, clientY: cy, bubbles: true, cancelable: true,
            button: 0, buttons: type === 'mouseup' ? 0 : 1,
          }),
        )
      fire('mousedown', x, y, header)
      fire('mousemove', x + 160, y + 90, document)
      fire('mouseup', x + 160, y + 90, document)
      await wait(400)
      const dragged = tf()

      ui.settingsDialog = false
      await wait(600)
      ui.settingsDialog = true
      await wait(900)
      const reopened = tf()

      // 【重开后第一次轻拖不能"跳"】用户实报："稍微动一下，他自己就会有一个
      // 往拖动方向大范围动作"。原因是 Element Plus 把位移存在自己的闭包里，
      // 而我刚开始只清了 DOM 的 transform —— 内部旧值还在，于是第一次拖动先弹回旧位置。
      // 现在改成 destroy-on-close（组件重挂载、闭包重建），所以这里应该只有 1px 的位移。
      const header2 = dlgEl()?.querySelector('.el-dialog__header')
      let firstDrag = '（找不到标题栏）'
      let jump = false
      if (header2) {
        const hr = header2.getBoundingClientRect()
        const hx = Math.round(hr.left + 120)
        const hy = Math.round(hr.top + hr.height / 2)
        fire('mousedown', hx, hy, header2)
        fire('mousemove', hx + 1, hy + 1, document)
        fire('mouseup', hx + 1, hy + 1, document)
        await wait(300)
        firstDrag = tf()
        const m = /translate\((-?\d+(?:\.\d+)?)px,\s*(-?\d+(?:\.\d+)?)px\)/.exec(firstDrag)
        if (m) jump = Math.abs(Number(m[1])) > 3 || Math.abs(Number(m[2])) > 3
        else if (firstDrag !== '（空）') jump = true
      }

      // 再验一件用户实报的事：按住关闭按钮时**不能**被当成拖动。
      // 手法：在关闭按钮上合成 mousedown + 轻微移动 + mouseup，看位移是否仍为空。
      const btn = dlgEl()?.querySelector('.el-dialog__headerbtn')
      // 断言要跟"微拖前"比，而不是跟"空"比 —— 上面那步"重开后轻拖 1px"
      // 已经让弹窗带上了 1px 位移（我第一次就写成了跟空比，于是误报）。
      const beforeBtnDrag = tf()
      let btnMoved = '（找不到按钮）'
      if (btn) {
        const br = btn.getBoundingClientRect()
        const bx = Math.round(br.left + br.width / 2)
        const by = Math.round(br.top + br.height / 2)
        fire('mousedown', bx, by, btn)
        fire('mousemove', bx + 6, by + 12, document)
        fire('mouseup', bx + 6, by + 12, document)
        await wait(300)
        btnMoved = tf()
      }

      const ok =
        dragged !== '（空）' &&
        reopened === '（空）' &&
        btnMoved === beforeBtnDrag && // 微拖前后一致 = 没被当成拖动
        !jump
      notify(ok ? 'success' : 'error', '设置面板位置重置测试',
        `拖动前 transform = ${before}\n拖动后 = ${dragged}（应非空）\n` +
        `关闭再打开 = ${reopened}（应为空）\n` +
        `按住关闭按钮微拖：${beforeBtnDrag} → ${btnMoved}（应不变，即没被当成拖动）\n` +
        `重开后第一次轻拖 1px = ${firstDrag}（应只有 1px，不能跳到旧位置）\n` +
        `→ ${ok ? '回到默认位置 + 关闭按钮不被拖动吃掉 + 重开不跳 ✓' : '有问题，看上面几项'}`,
        0)
      ui.settingsDialog = false
      break
    }

    // 触发一次"从屏幕圈选二维码"，用来复现/验证那条链路。
    // 不 await：它会一直挂着直到用户圈选完或取消。
    case 'screenscan': {
      notify('info', '屏幕圈选自测', '即将隐藏主窗口并创建全屏遮罩；日志里能看到每一步。', 0)
      api
        .beginScreenScan()
        .then((r) => notify(r?.ok ? 'success' : 'warning', '屏幕圈选结果', String(r?.message || ''), 0))
        .catch((e) => notify('error', '屏幕圈选失败', String(e?.message || e), 0))
      break
    }

    // 【主窗口】发起一条"拖动归属"测试：打开浮动对话框并把 floatdrag 透传进去。
    // 注意名字必须和浮动窗口里的那个自测项不同（switch 重复 case 只有第一个命中）。
    case 'floatdragtest': {
      const wait = (ms) => new Promise((r) => setTimeout(r, ms))
      api
        .openFloatDialog('node', JSON.stringify({ mode: 'add' }), 'floatdrag')
        .catch((e) => notify('error', '拖动归属测试失败', String(e?.message || e), 0))
      await wait(1500)
      notify('info', '拖动归属测试', '已在浮动窗口里合成一次标题栏拖动，结果见那边窗口的通知。', 0)
      break
    }

    // 【在浮动窗口里跑】验证"拖动的归属"。
    //
    // 这条测试是针对一个真实 bug 的回归：浮动窗口里曾经同时开着两套拖动 ——
    // Element Plus 的对话框 draggable（用 transform 移动卡片）
    // 和 v3 的 --wails-draggable（移动整个 OS 窗口）。
    // 表现是"按住标题栏后卡片跟着鼠标跑、松开还黏在鼠标上，窗口内的页面上下滚"。
    // 修法是浮动模式下关掉对话框的 draggable，由整个窗口负责移动。
    //
    // 所以这里断言：合成一次标题栏拖动之后，.el-dialog 的 inline transform 必须仍是空。
    case 'floatdrag': {
      const wait = (ms) => new Promise((r) => setTimeout(r, ms))
      const dlg = document.querySelector('.el-dialog.cp-dialog')
      const header = dlg?.querySelector('.el-dialog__header')
      if (!dlg || !header) {
        notify('error', '拖动归属测试', '找不到对话框或标题栏', 0)
        break
      }
      const before = dlg.style.transform || '（空）'
      const r = header.getBoundingClientRect()
      const x = Math.round(r.left + 60)
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
      fire('mousemove', x + 140, y + 70, document)
      fire('mouseup', x + 140, y + 70, document)
      await wait(300)
      const after = dlg.style.transform || '（空）'
      // 卡片被拖出可视区就会冒横向滚动条 —— 这也是那个 bug 的伴随症状
      const overflowX = document.documentElement.scrollWidth > window.innerWidth + 1
      const ok = after === '（空）' && !overflowX
      notify(ok ? 'success' : 'error', '浮动窗口拖动归属测试',
        `标题栏拖动前后 .el-dialog 的 transform：${before} → ${after}\n` +
        `是否出现横向溢出 = ${overflowX}\n` +
        `→ ${ok
          ? '拖动只归窗口管，卡片没被 Element Plus 拖走 ✓'
          : '卡片仍被 Element Plus 拖动（transform 非空）✗ 两套拖动又打架了'}`, 0)
      break
    }

    // 验证「浮动弹窗的结果回传」。
    //
    // 注意名字必须和浮动窗口里那个自测项区分开（那边叫 floatsubmit）：
    // JS 的 switch 里**重复的 case 只有第一个会命中**，两处同名会让这里变成死代码
    // —— 实测踩过，表现是"跑完什么也没发生、也没有报错"。
    //
    // 主窗口发起浮动对话框，并把自测参数 floatsubmit 透传给浮动窗口；
    // 浮动窗口加载后会自己按参数跑一遍"取消"，于是：
    //   浮动窗口的对话框关闭 → ui.onNodeDialogClosed → SubmitFloatDialog
    //   → Go 唤醒这里 await 的调用 → 关掉浮动窗口
    // 这三步全走真实代码路径，最后这里检查"有没有收到结果 + 窗口有没有关掉"。
    case 'floatpassthrough': {
      const sleepMs = (ms) => new Promise((r) => setTimeout(r, ms))
      const got = []
      api
        .openFloatDialog('node', JSON.stringify({ mode: 'add' }), 'floatsubmit')
        .then((r) => got.push(r))
        .catch((e) => got.push('ERR:' + String(e?.message || e)))
      await sleepMs(4000)
      const b = await api.floatWindowBounds()
      const closed = Object.keys(b).length === 0
      const result = got[0]
      const ok = closed && !!result && result.includes('float-self-test')
      notify(ok ? 'success' : 'error', '浮动窗口结果回传测试',
        `主窗口 await 收到的结果 = ${result === undefined ? '（没收到，可能还挂着）' : result}\n` +
        `浮动窗口是否已关闭 = ${closed}${closed ? '' : ' 仍在：' + JSON.stringify(b)}\n` +
        `→ ${ok ? '结果回传 + 自动关窗都正常 ✓' : '有问题，看上面两项'}\n` +
        `（链路：对话框关闭 → ui.onNodeDialogClosed → SubmitFloatDialog → Go 唤醒等待方）`, 0)
      break
    }

    // 验证「浮动弹窗能跑到主窗口之外」。
    //
    // 做法：打开浮动节点对话框，然后**把它挪到明显超出主窗口右/下边缘的位置**，
    // 再整屏截图 —— 如果它还被主窗口裁剪，就不可能出现在主窗口之外。
    // 挪窗用的是 v3 runtime 的 Window.Get('名字').SetPosition()，
    // 也就是主窗口可以直接操作另一个窗口 —— 这正是 v2 做不到的那件事。
    case 'floatoutside': {
      const sleepMs = (ms) => new Promise((r) => setTimeout(r, ms))
      // 不 await：这个调用会一直挂着直到用户在浮动窗口里确定/取消
      actions.addNode()
      await sleepMs(2200)
      // 把主窗口叫到前台，好让整屏截图里两个窗口都看得见
      await api.showWindow()
      await sleepMs(400)
      // 挪到"明显超出主窗口右下角、但仍在屏幕内"的位置。
      // 几何信息一律由 Go 侧回答：从主窗口用前端 Window.Get(name) 去查另一个窗口
      // 会拿到主窗口自己的数据（实测踩过），不能作为证据。
      await api.moveFloatWindow(1000, 300)
      await sleepMs(500)
      const m = await api.mainWindowBounds()
      const f = await api.floatWindowBounds()
      if (!f.width) {
        notify('error', '浮动窗口越界测试', '没有浮动窗口（FloatWindowBounds 返回空）', 0)
        break
      }
      const outsideRight = f.x + f.width > m.x + m.width
      const outsideBottom = f.y + f.height > m.y + m.height
      const onScreen = f.x + f.width <= screen.width && f.y + f.height <= screen.height
      notify(outsideRight && outsideBottom && onScreen ? 'success' : 'error', '浮动窗口越界测试',
        `主窗口：位置 (${m.x},${m.y}) 尺寸 ${m.width}x${m.height}\n` +
        `浮动窗口：位置 (${f.x},${f.y}) 尺寸 ${f.width}x${f.height}\n` +
        `右边缘超出主窗口 = ${outsideRight}；下边缘超出主窗口 = ${outsideBottom}\n` +
        `整框仍在屏幕内 = ${onScreen}（视口 ${screen.width}x${screen.height}）\n` +
        `→ 它没被主窗口裁剪，因为它是独立 OS 窗口，不是主窗口里的浮层`, 0)
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
    <BottomPanel />

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
