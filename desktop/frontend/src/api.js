/**
 * 与 Go 后端的桥接层。
 *
 * 前端只通过这里访问后端，好处是：组件里不出现任何 Wails 路径，改名只需改一处
 * —— 这一点在 v2 → v3 迁移时兑现了：v2 是 window.go.main.API.*，
 * v3 换成了 @wailsio/runtime 的 Call.ByName，本文件之外一行都没改。
 *
 * v3 的桥与 v2 的差别：
 *   - 调用：Call.ByName('main.API.方法名', ...args)（v3 用 <包>.<类型>.<方法>）
 *   - 事件：Events.On(name, e => ...)，回调收到的是 {name, data, sender} 对象，
 *     所以这里统一解包出 e.data，下游处理函数签名保持不变
 *   - 判定"是否在 Wails 里"：v3 的宿主通道是 window.chrome.webview.postMessage
 */
import { Call, Events } from '@wailsio/runtime'

/** 后端服务的全限定名前缀：v3 的格式是 `<包名>.<类型名>.<方法名>`。 */
const SERVICE = 'main.API'

/** 是否运行在 Wails v3 宿主里。
 *
 * 判据用宿主通道本身，而不是 window._wails —— 后者被 runtime 包在导入时就
 * 赋成了 {}，拿它判断会永远为真，那样"后端未连接"的提示就永远不会出现了。
 */
export function hasBridge() {
  return typeof window !== 'undefined' && !!window.chrome?.webview?.postMessage
}

/** 调用后端方法；失败时把后端的 error 原样抛出（v3 会把 Go 的 error 变成 RuntimeError）。 */
async function call(name, ...args) {
  if (!hasBridge()) {
    throw new Error('后端未连接：请通过桌面程序运行')
  }
  try {
    return await Call.ByName(`${SERVICE}.${name}`, ...args)
  } catch (e) {
    // v3 抛的是 RuntimeError（继承自 Error），message 就是 Go 的 error 文本。
    // 上层统一用 String(e?.message || e) 展示，这里不用额外加工。
    throw e
  }
}

/* -------------------------------------------------------------------------
 * 通知
 *
 * 实现搬到了 ./notify.js 与 components/Notifications.vue —— 为什么不用
 * element-plus 的 ElNotification，理由写在 notify.js 顶部（同一 tick 连发多条
 * 会重叠）。这里只做转发，保持 notify(level, title, message, duration)
 * 这个签名不变，调用方一行都不用改。
 * ---------------------------------------------------------------------- */
export { notify } from './notify'

/* -------------------------------------------------------------------------
 * 事件订阅
 * ---------------------------------------------------------------------- */

/**
 * 订阅后端事件。
 *
 * 后端事件分几种（见 internal/app/app.go 的 Event* 常量）：
 *   log / nodes / stats / notify / testDone
 * notify 的文本用 NUL 分隔成 "level\0title\0text"。
 */
export function onEvent(kind, handler) {
  if (!hasBridge()) return () => {}
  // v3 的回调收到 {name, data, sender}，这里解包出 data，
  // 于是所有 handler 仍然只拿到后端 Emit 的那个字符串。
  return Events.On(`cp:${kind}`, (e) => handler(e?.data))
}

/** 统一注册全部后端事件。 */
export function subscribeAll(handlers = {}) {
  const offs = []
  const bind = (kind, fn) => {
    if (!fn) return
    offs.push(onEvent(kind, fn))
  }
  bind('log', handlers.log)
  bind('nodes', handlers.nodes)
  bind('stats', handlers.stats)
  bind('testDone', handlers.testDone)
  // notify 特殊：在这里就解析成 5 段并弹通知，业务侧不用关心编码格式
  bind('notify', (text) => {
    const parts = String(text ?? '').split('\u0000')
    const level = parts[0] || 'info'
    const title = parts[1] || ''
    const body = parts[2] || ''
    notify(level, title, body)
    if (handlers.notify) handlers.notify(level, title, body)
  })
  return () => offs.forEach((off) => {
    try {
      off()
    } catch {
      /* 忽略 */
    }
  })
}

/* -------------------------------------------------------------------------
 * 后端方法（与 desktop/api*.go 一一对应）
 * ---------------------------------------------------------------------- */

export const api = {
  // 元信息 / 状态
  getMeta: () => call('GetMeta'),
  getNodes: () => call('GetNodes'),
  getSummary: () => call('GetSummary'),
  getLogs: () => call('GetLogs'),
  validate: () => call('Validate'),

  // 设置
  getSettings: () => call('GetSettings'),
  saveSettings: (s) => call('SaveSettings', s),
  resetSettings: () => call('ResetSettings'),
  getFront: () => call('GetFront'),
  saveFront: (f) => call('SaveFront', f),
  testFront: () => call('TestFront'),

  // 节点
  addNode: (n) => call('AddNode', n),
  updateNode: (n) => call('UpdateNode', n),
  deleteNodes: (ids) => call('DeleteNodes', ids),
  cloneNode: (id) => call('CloneNode', id),
  getNode: (id) => call('GetNode', id),

  // 激活
  activate: (ids) => call('Activate', ids),
  deactivate: (ids) => call('Deactivate', ids),
  activateAll: () => call('ActivateAll'),
  deactivateAll: () => call('DeactivateAll'),
  planActivation: (ids) => call('PlanActivation', ids),

  // 端口
  suggestPort: (excludeId) => call('SuggestPort', excludeId || ''),
  randomPort: (excludeId) => call('RandomPort', excludeId || ''),
  checkPort: (port, excludeId) => call('CheckPort', port, excludeId || ''),
  usedPorts: (excludeId) => call('UsedPorts', excludeId || ''),

  // 导入 / 分享
  importFromClipboard: () => call('ImportFromClipboard'),
  parseShareText: (text) => call('ParseShareText', text),
  beginScreenScan: () => call('BeginScreenScan'),
  scanning: () => call('Scanning'),
  shareNode: (id) => call('ShareNode', id),
  saveShareImage: (id) => call('SaveShareImage', id),
  copyText: (t) => call('CopyText', t),

  // 测试
  runTest: (ids, kind) => call('RunTest', ids, kind),
  cancelTest: () => call('CancelTest'),
  isTesting: () => call('IsTesting'),

  // 杂项
  setWindowTheme: (t) => call('SetWindowTheme', t),
  genWireGuardKey: () => call('GenWireGuardKey'),
  resetTodayStats: (ids) => call('ResetTodayStats', ids),
  removeInvalidNodes: () => call('RemoveInvalidNodes'),
  openDataDir: () => call('OpenDataDir'),
  openConfigFile: () => call('OpenConfigFile'),
  openLogFile: () => call('OpenLogFile'),
  openPath: (p) => call('OpenPath', p),
  quit: () => call('Quit'),

  // 托盘菜单里那几个动作。托盘回调直接调同名 Go 方法，
  // 这里暴露出来是为了能在自测里验证"菜单动作本身是对的"。
  copyFirstLocalAddress: () => call('CopyFirstLocalAddress'),

  // ---- 浮动对话框（独立窗口）----
  // OpenFloatDialog 会一直阻塞到用户在浮动窗口里确定/取消，
  // 所以主窗口这边就是一个普通的 await。
  openFloatDialog: (kind, payload, devOpen) =>
    call('OpenFloatDialog', kind, payload || '', devOpen || ''),
  getFloatPayload: () => call('GetFloatPayload'),
  submitFloatDialog: (resultJSON) => call('SubmitFloatDialog', resultJSON || ''),
  // 几何信息由 Go 侧回答：从前端查"另一个窗口"会拿到当前窗口自己的数据
  floatWindowBounds: () => call('FloatWindowBounds'),
  mainWindowBounds: () => call('MainWindowBounds'),
  moveFloatWindow: (x, y) => call('MoveFloatWindow', x, y),
  // 把主窗口叫到前台（托盘单击、全局快捷键也走同一个 Go 方法）
  showWindow: () => call('ShowWindow'),
}

export default api
