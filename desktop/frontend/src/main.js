import { createApp } from 'vue'
import { createPinia } from 'pinia'
import ElementPlus from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'

import 'element-plus/dist/index.css'
// element-plus 的暗色变量，配合 styles.css 里的 html.dark
import 'element-plus/theme-chalk/dark/css-vars.css'
import './styles.css'

import App from './App.vue'

const app = createApp(App)
app.use(createPinia())
app.use(ElementPlus, {
  locale: zhCn,
  // 桌面端信息密集，默认用 small 更合适
  size: 'small',
  // 通知/弹层的基准 z-index
  zIndex: 2000,
})
app.mount('#app')
