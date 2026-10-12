/**
 * 业务动作。
 *
 * 所有"点一下会发生什么"都收敛在这里：右侧组件只负责把事件转成
 * 对这里某个函数的调用，不自己写业务逻辑。
 */
import api, { notify } from './api'
import { useStore } from './store'
import { useUi } from './ui'

export function useActions() {
  const store = useStore()
  const ui = useUi()

  /** 把后端抛出的错误统一显示成通知。 */
  function fail(title, e) {
    const msg = typeof e === 'string' ? e : e?.message || String(e)
    notify('error', title, msg)
    return msg
  }

  /** 目标节点：优先用"当前选中"，没有就用右键点中的那个。 */
  function targets(fallbackNode) {
    if (store.selectedIds.length) return [...store.selectedIds]
    if (fallbackNode) return [fallbackNode.id]
    return []
  }

  // -----------------------------------------------------------------
  // 激活 / 停止
  // -----------------------------------------------------------------
  async function activate(ids, fallbackNode) {
    const list = ids?.length ? ids : targets(fallbackNode)
    if (!list.length) {
      notify('warning', '请先选择节点', '单击一行选中，或按住 Ctrl 多选')
      return
    }
    // 先看会不会连带启动前置节点，提前告诉用户一声
    try {
      const plan = await api.planActivation(list)
      if (plan?.error) {
        notify('error', '无法激活', plan.error)
        return
      }
      if (plan?.auto?.length) {
        notify('info', '将连带激活前置节点', plan.auto.join('、'))
      }
    } catch {
      /* 计划失败不阻塞激活，真正的错误在 Activate 里会报出来 */
    }
    try {
      await api.activate(list)
    } catch (e) {
      fail('激活失败', e)
    }
  }

  async function deactivate(ids, fallbackNode) {
    const list = ids?.length ? ids : targets(fallbackNode)
    if (!list.length) {
      notify('warning', '请先选择节点', '')
      return
    }
    try {
      await api.deactivate(list)
    } catch (e) {
      fail('停止失败', e)
    }
  }

  async function toggleActive(node) {
    if (!node) return
    if (node.state === 'running') await deactivate([node.id])
    else await activate([node.id])
  }

  async function activateAll() {
    if (!store.nodes.length) return
    ui.notifyBusy('activateAll')
    try {
      await api.activateAll()
    } catch (e) {
      fail('激活失败', e)
    } finally {
      ui.notifyBusy('')
    }
  }

  async function deactivateAll() {
    ui.notifyBusy('deactivateAll')
    try {
      await api.deactivateAll()
    } catch (e) {
      fail('停止失败', e)
    } finally {
      ui.notifyBusy('')
    }
  }

  // -----------------------------------------------------------------
  // 增删改
  // -----------------------------------------------------------------
  /**
   * 打开「添加/编辑节点」对话框。
   *
   * 它现在开在**独立的浮动窗口**里：可以拖到主窗口之外、永远压在主窗口上面
   * （Wails v3 的多窗口能力，v2 做不到）。主窗口这边 await 到用户确定/取消，
   * 期间主界面依然可用 —— 不再是"模态挡住一切"。
   *
   * 复用同一个对话框组件，所以校验、端口检测、保存逻辑都没变。
   */
  async function openNodeDialogFloating(payload) {
    try {
      const result = await api.openFloatDialog('node', JSON.stringify(payload || {}))
      if (result) {
        try {
          const r = JSON.parse(result)
          if (r.submitted) await store.refresh()
        } catch {
          /* 结果不是 JSON 就当没发生 */
        }
      }
    } catch (e) {
      // 已经开着一个浮动窗口时会走到这里，提示一下就够，不必当成错误弹窗
      fail('打开节点对话框失败', e)
    }
  }

  function addNode() {
    openNodeDialogFloating({ mode: 'add' })
  }

  async function editNode(node) {
    if (!node) return
    try {
      const full = await api.getNode(node.id)
      await openNodeDialogFloating({ mode: 'edit', node: full })
    } catch (e) {
      fail('读取节点失败', e)
    }
  }

  async function cloneNode(node) {
    if (!node) return
    try {
      await api.cloneNode(node.id)
    } catch (e) {
      fail('克隆失败', e)
    }
  }

  async function removeNodes(ids, fallbackNode) {
    const list = ids?.length ? ids : targets(fallbackNode)
    if (!list.length) {
      notify('warning', '请先选择要移除的节点', '')
      return
    }
    const names = store.nodes.filter((n) => list.includes(n.id)).map((n) => n.name)
    const { ElMessageBox } = await import('element-plus')
    try {
      await ElMessageBox.confirm(
        `将移除 ${list.length} 个节点：\n${names.join('、')}\n\n正在运行的会先被停止并释放端口。`,
        '确认移除',
        { type: 'warning', confirmButtonText: '移除', cancelButtonText: '取消' },
      )
    } catch {
      return
    }
    try {
      await api.deleteNodes(list)
    } catch (e) {
      fail('移除失败', e)
    }
  }

  async function removeInvalid() {
    try {
      const n = await api.removeInvalidNodes()
      if (n === 0) return
      notify('success', '已移除测试无效项', `${n} 个节点`)
    } catch (e) {
      fail('移除失败', e)
    }
  }

  // -----------------------------------------------------------------
  // 分享
  // -----------------------------------------------------------------
  async function share(node) {
    if (!node) return
    ui.openShare(node.id)
    try {
      const res = await api.shareNode(node.id)
      ui.shareDialog.result = res
    } catch (e) {
      ui.closeShare()
      fail('生成分享失败', e)
    } finally {
      ui.shareDialog.loading = false
    }
  }

  // -----------------------------------------------------------------
  // 测试
  // -----------------------------------------------------------------
  async function runTest(kind, ids, fallbackNode) {
    const list = ids?.length ? ids : targets(fallbackNode)
    if (!list.length) {
      notify('warning', '请先选择要测试的节点', '')
      return
    }
    if (store.testing) {
      notify('warning', '已有测试在进行', '请等它结束，或先停止测试')
      return
    }
    store.testKind = kind
    store.testingIds = list
    try {
      await api.runTest(list, kind)
    } catch (e) {
      store.testingIds = []
      fail('启动测试失败', e)
    }
  }

  async function cancelTest() {
    try {
      await api.cancelTest()
    } catch (e) {
      fail('停止测试失败', e)
    }
  }

  function testSelection(kind, fallbackNode) {
    return runTest(kind, null, fallbackNode)
  }

  // -----------------------------------------------------------------
  // 导入
  // -----------------------------------------------------------------
  async function importFromClipboard() {
    try {
      const res = await api.importFromClipboard()
      handleImportResult(res)
    } catch (e) {
      fail('读取剪贴板失败', e)
    }
  }

  async function scanScreen() {
    if (store.scanning) return
    store.scanning = true
    try {
      const res = await api.beginScreenScan()
      handleImportResult(res, { silentCancel: true })
    } catch (e) {
      fail('屏幕扫描失败', e)
    } finally {
      store.scanning = false
    }
  }

  async function importFromText(text) {
    try {
      const res = await api.parseShareText(text)
      handleImportResult(res)
    } catch (e) {
      fail('解析失败', e)
    }
  }

  /**
   * 导入结果统一处理。
   *
   * 识别成功后**先暂存，不直接入库** —— 因为还没有本地代理端口，
   * 而端口必须由用户确认（默认值 / 随机 / 占用检测都在那一步）。
   */
  function handleImportResult(res, opts = {}) {
    if (!res || !res.ok) {
      const msg = res?.message || '没有识别到可用的节点'
      if (opts.silentCancel && /取消/.test(msg)) {
        notify('info', '已取消', '')
        return
      }
      notify('warning', '导入失败', msg)
      return
    }
    ui.closeContextMenu()
    ui.importDialog = false
    if (res.nodes.length === 1) {
      // 只有一个：直接进"只有端口"的小弹窗，比先弹列表再弹端口少一步
      ui.openPortDialog(res.nodes, res.source)
    } else {
      ui.openPortDialog(res.nodes, res.source)
    }
  }

  return {
    activate,
    deactivate,
    toggleActive,
    activateAll,
    deactivateAll,
    // 设为 / 取消全局节点（整个列表只能一个）。
    // 只改"指定用哪个节点"，不改系统设置 —— 改系统代理是下一轮的事。
    async setGlobalNode(node) {
      const cur = store.settings?.globalNodeId || ''
      const next = cur === node.id ? '' : node.id
      try {
        await api.setGlobalNode(next)
        store.settings = await api.getSettings()
        notify(
          'success',
          next ? `已把「${node.name}」设为全局节点` : '已取消全局节点',
          '',
        )
      } catch (e) {
        notify('error', '设置全局节点失败', String(e?.message || e), 0)
      }
    },

    addNode,
    editNode,
    cloneNode,
    removeNodes,
    removeInvalid,
    share,
    runTest,
    cancelTest,
    testSelection,
    importFromClipboard,
    scanScreen,
    importFromText,
    targets,
  }
}
