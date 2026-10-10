/**
 * 与 Go 后端的桥接层。
 *
 * 前端只通过这里访问后端，好处是：
 *  - 组件里不出现 window.go.xxx 这种路径，改名只需改一处
 *  - 后端未就绪 / 不在 Wails 里运行时，能给出明确错误而不是 undefined 崩一片
 *  - 通知、事件订阅这类"通用行为"集中在一处
 */
/** 后端绑定的命名空间（Wails 自动生成在 window.go.main.API）。 */
function bridge() {
  return window?.go?.main?.API
}

/** 是否运行在 Wails 里。 */
export function hasBridge() {
  return !!bridge()
}

/** 调用后端方法；失败时把后端的 error 原样抛出（Wails 会把 Go 的 error 变成 reject）。 */
async function call(name, ...args) {
  const b = bridge()
  if (!b) {
    throw new Error('后端未连接：请通过桌面程序运行（或在 Wails dev 模式下打开）')
  }
  const fn = b[name]
  if (typeof fn !== 'function') {
    throw new Error(`后端方法不存在：${name}`)
  }
  return await fn(...args)
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
  const rt = window?.runtime
  if (!rt?.EventsOn) return () => {}
  return rt.EventsOn(`cp:${kind}`, handler)
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
}

export default api
