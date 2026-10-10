/**
 * 列表列的元信息。
 *
 * 默认显示的就是产品要求的 8 列（类型、别名、本地端口、地址、远程端口、
 * 传输协议、延迟、速度）；其余的通过「列显示」多选打开。
 *
 * 每一列的值都在这里用 render 语义描述好，表格组件只负责摆位置，
 * 这样"某列怎么算、怎么显示"不会散落在模板里。
 */

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
  { key: 'state', label: '状态', width: 76, align: 'center' },
  { key: 'protocol', label: '类型', width: 104 },
  { key: 'name', label: '别名', width: 190, minWidth: 130 },
  { key: 'listenPort', label: '本地端口', width: 92, align: 'right' },
  { key: 'server', label: '地址', minWidth: 168 },
  { key: 'port', label: '远程端口', width: 90, align: 'right' },
  { key: 'transport', label: '传输协议', width: 118 },
  { key: 'latency', label: '延迟(ms)', width: 92, align: 'right' },
  { key: 'real', label: '真连接(ms)', width: 104, align: 'right' },
  { key: 'speed', label: '速度(MB/s)', width: 104, align: 'right' },
  { key: 'exitIp', label: '出口 IP', width: 132 },
  { key: 'today', label: '今日统计', width: 104, align: 'right' },
  { key: 'lastUsed', label: '最近使用', width: 152 },
  { key: 'createdAt', label: '创建时间', width: 152 },
  { key: 'remark', label: '备注', minWidth: 130 },
  { key: 'front', label: '前置', minWidth: 156 },
  { key: 'tls', label: 'TLS', width: 62, align: 'center' },
  { key: 'activeConn', label: '活动连接', width: 92, align: 'right' },
  { key: 'totalConn', label: '累计连接', width: 92, align: 'right' },
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
