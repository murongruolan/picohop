import { createApp } from 'vue'
import { createPinia } from 'pinia'
import ElementPlus from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'

import 'element-plus/dist/index.css'
// element-plus 的暗色变量，配合 styles.css 里的 html.dark
import 'element-plus/theme-chalk/dark/css-vars.css'
import './styles.css'

import App from './App.vue'

// 【同一个前端，两种挂载方式】
//
// 主窗口：正常挂载，显示整套界面。
// 浮动窗口：URL 带 ?float=<kind>，挂的是**同一个 App 组件**，只是加一个 body class
// 并把 kind 传进去 —— App.vue 会据此只打开那一个对话框。
//
// 为什么不单独做一个"浮动窗口专用入口"：对话框组件、它的校验、保存逻辑
// 全都可以原样复用，只有"关掉时把结果回传给 Go"这一点不同。
// 少一份入口就少一份会走样的地方。
const params = new URLSearchParams(window.location.search)
const floatKind = params.get('float') || ''
if (floatKind) {
  // 这个 class 让 styles.css 把主界面藏起来、让对话框铺满整个窗口，
  // 并在标题栏上打开无边框窗口的拖动区。
  document.body.classList.add('cp-float-window')
}

const app = createApp(App, { floatKind })
app.use(createPinia())
app.use(ElementPlus, {
  locale: zhCn,
  // 桌面端信息密集，默认用 small 更合适
  size: 'small',
  // 通知/弹层的基准 z-index
  zIndex: 2000,
})
app.mount('#app')
