/**
 * 通知（自己实现，没有用 element-plus 的 ElNotification）。
 *
 * 为什么不用 ElNotification：
 * 它的垂直位置由每条通知自己的 offset 决定，而**同一个 tick 里连发多条时**
 * 它们互相看不到对方，于是拿到同一个 offset 直接叠在一起 ——
 * 实测一次弹 5 条只看得见最后一条。要"右上角控制栏下方"这个固定位置，
 * 又要求多条能正确堆叠，自己用 flex 列布局最省心。
 *
 * 对外只暴露 notify()，签名与调用方无关（原来的调用一行都不用改）。
 */
import { reactive } from 'vue'

/** 5 种类型，与需求一致。 */
export const LEVELS = ['primary', 'success', 'warning', 'info', 'error']

export const notifications = reactive([])

let seq = 0

/**
 * 弹一条通知。
 *
 * level  primary / success / warning / info / error
 * duration  毫秒；传 0 表示不自动关闭（开发自测时会用到）
 */
export function notify(level = 'info', title = '', message = '', duration) {
  const item = {
    id: ++seq,
    level: LEVELS.includes(level) ? level : 'info',
    title: title || '',
    body: message || '',
    timer: null,
  }
  notifications.push(item)

  // 最多同时显示 6 条，超了就把最旧的挤掉（避免堆满半个屏幕）
  while (notifications.length > 6) {
    dismiss(notifications[0].id)
  }

  const ms = duration === undefined ? 4000 : duration
  if (ms > 0) {
    item.timer = setTimeout(() => dismiss(item.id), ms)
  }
  return item.id
}

export function dismiss(id) {
  const i = notifications.findIndex((n) => n.id === id)
  if (i < 0) return
  const [item] = notifications.splice(i, 1)
  if (item?.timer) clearTimeout(item.timer)
}

export function clearAll() {
  notifications.splice(0, notifications.length)
}
