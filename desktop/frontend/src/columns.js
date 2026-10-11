/**
 * 列表列的元信息。
 *
 * 默认显示的就是产品要求的 8 列（类型、别名、本地端口、地址、远程端口、
 * 传输协议、延迟、速度）；其余的通过「列显示」多选打开。
 *
 * 宽度有两套：
 *   - `width`     固定初始宽度（可拖拽调整）
 *   - `minWidth`  弹性列：不写死宽度，而是吸收窗口多出来的空间
 *                 （地址/备注/前置这类内容长度差异大的列适合弹性）
 * 两者都不写死的话表格右侧会留一大片空白，所以每列至少要有一个。
 */

/**
 * 拖拽列宽的下限。
 *
 * Element Plus 自己在拖拽时只做了 `Math.max(列左边界 + 30, ...)` 的钳制，
 * 也就是**允许把一列拖到只剩 30px**。30px 在左右各 10px 的单元格内边距下
 * 只剩 10px 文字区，一个汉字（约 13px）会被切掉一半 —— 看起来就是"列叠在一起"。
 * 所以下限由我们自己定，并在 header-dragend 里钳制（见 NodeTable.vue）。
 */
export const MIN_COL_WIDTH = 56
/** 上限：避免手滑把一列拖到把其它列全挤出屏幕。 */
export const MAX_COL_WIDTH = 900

/** 默认可见的列，顺序即产品要求顺序。 */
export const DEFAULT_VISIBLE = [
  'protocol',
  'name',
  'listenPort',
  'server',
  'port',
  'transport',
  'latency',
  'speed',
]

/** 全部可选列。 */
export const ALL_COLUMNS = [
  { key: 'state', label: '状态', width: 88, min: 56, align: 'center' },
  { key: 'protocol', label: '类型', width: 106, min: 56 },
  { key: 'name', label: '别名', width: 208, min: 72 },
  { key: 'listenPort', label: '本地端口', width: 100, min: 64, align: 'right' },
  { key: 'server', label: '地址', minWidth: 300, min: 80 },
  { key: 'port', label: '远程端口', width: 96, min: 64, align: 'right' },
  { key: 'transport', label: '传输协议', width: 122, min: 64 },
  { key: 'latency', label: '延迟(ms)', width: 94, min: 64, align: 'right' },
  { key: 'real', label: '真连接(ms)', width: 106, min: 64, align: 'right' },
  { key: 'speed', label: '速度(MB/s)', width: 106, min: 64, align: 'right' },
  { key: 'exitIp', label: '出口 IP', width: 134, min: 72 },
  { key: 'today', label: '今日统计', width: 106, min: 64, align: 'right' },
  { key: 'lastUsed', label: '最近使用', width: 152, min: 76 },
  { key: 'createdAt', label: '创建时间', width: 152, min: 76 },
  { key: 'remark', label: '备注', minWidth: 180, min: 72 },
  { key: 'front', label: '前置', minWidth: 200, min: 72 },
  { key: 'tls', label: 'TLS', width: 64, min: 56, align: 'center' },
  { key: 'activeConn', label: '活动连接', width: 94, min: 64, align: 'right' },
  { key: 'totalConn', label: '累计连接', width: 94, min: 64, align: 'right' },
]

/** 按 key 取列定义。 */
export function columnByKey(key) {
  return ALL_COLUMNS.find((c) => c.key === key)
}

// ---------------------------------------------------------------------------
// 格式化
// ---------------------------------------------------------------------------

/** 字节数 -> KB/MB/GB/TB/PB（最小单位 KB，和后端 FormatBytes 口径一致）。 */
export function formatBytes(n) {
  if (!n || n <= 0) return '0 KB'
  const units = ['KB', 'MB', 'GB', 'TB', 'PB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  if (v >= 100) return `${v.toFixed(0)} ${units[i]}`
  if (v >= 10) return `${v.toFixed(1)} ${units[i]}`
  return `${v.toFixed(2)} ${units[i]}`
}

/** 毫秒 -> 带单位的文本，0 视为"没有数据"。 */
export function formatMs(v) {
  if (!v || v <= 0) return ''
  return String(v)
}

/** 速度 -> 保留两位。 */
export function formatSpeed(v) {
  if (!v || v <= 0) return ''
  return v >= 100 ? v.toFixed(0) : v.toFixed(2)
}

/** 时间戳 -> 相对时间（刚刚 / 3 分钟前 / 昨天 12:03 ...）。 */
export function formatWhen(ts) {
  if (!ts) return ''
  const t = new Date(ts)
  if (Number.isNaN(t.getTime()) || t.getFullYear() < 2000) return ''
  const now = Date.now()
  const diff = now - t.getTime()
  if (diff < 60_000) return '刚刚'
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)} 分钟前`
  const sameDay = new Date(now).toDateString() === t.toDateString()
  const hhmm = `${String(t.getHours()).padStart(2, '0')}:${String(t.getMinutes()).padStart(2, '0')}`
  if (sameDay) return `今天 ${hhmm}`
  const yesterday = new Date(now - 86_400_000)
  if (yesterday.toDateString() === t.toDateString()) return `昨天 ${hhmm}`
  const y = t.getFullYear() === new Date(now).getFullYear() ? '' : `${t.getFullYear()}-`
  return `${y}${String(t.getMonth() + 1).padStart(2, '0')}-${String(t.getDate()).padStart(2, '0')} ${hhmm}`
}

/** 时间戳 -> 绝对时间（悬停提示用）。 */
export function formatFullTime(ts) {
  if (!ts) return ''
  const t = new Date(ts)
  if (Number.isNaN(t.getTime()) || t.getFullYear() < 2000) return ''
  const p = (n) => String(n).padStart(2, '0')
  return `${t.getFullYear()}-${p(t.getMonth() + 1)}-${p(t.getDate())} ${p(t.getHours())}:${p(t.getMinutes())}:${p(t.getSeconds())}`
}
