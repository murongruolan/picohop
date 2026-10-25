<script setup>
/**
 * 顶部控制栏。
 *
 * 只放"全局"操作：总览、前置状态、导入/添加、全部启停、日志、设置。
 * 单节点的操作一律在列表的右键菜单里，避免两条路都能做同一件事。
 */
import { computed } from 'vue'
import {
  ArrowDown,
  Connection,
  Document,
  EditPen,
  Link,
  Monitor,
  Moon,
  Plus,
  Setting,
  Sunny,
  SwitchButton,
  Upload,
  View,
  VideoPlay,
} from '@element-plus/icons-vue'
import { useStore } from '../store'
import { useUi } from '../ui'
import { useActions } from '../actions'

const store = useStore()
const ui = useUi()
const actions = useActions()

const running = computed(() => store.summary?.running || 0)
const total = computed(() => store.summary?.total || 0)
const todayText = computed(() => store.summary?.todayText || '0 KB')
const activeConn = computed(() => store.summary?.activeConn || 0)
const frontDesc = computed(() => store.summary?.frontDesc || '未配置')
const frontOk = computed(() => !!store.summary?.frontOk)
const busy = computed(() => ui.busy)

function onImportCommand(cmd) {
  if (cmd === 'clipboard') actions.importFromClipboard()
  else if (cmd === 'screen') actions.scanScreen()
  else if (cmd === 'text') ui.importDialog = true
  else if (cmd === 'subscribe') ui.subDialog = true
}

function toggleTheme() {
  // 三态循环：跟随系统 → 深色 → 浅色
  if (store.theme === 'system') store.setTheme('dark')
  else if (store.theme === 'dark') store.setTheme('light')
  else store.setTheme('system')
}

const themeIcon = computed(() => {
  if (store.theme === 'dark') return Moon
  if (store.theme === 'light') return Sunny
  return Monitor
})
const themeTip = computed(() => {
  if (store.theme === 'dark') return '当前：深色（点击切浅色）'
  if (store.theme === 'light') return '当前：浅色（点击切跟随系统）'
  return '当前：跟随系统（点击切深色）'
})
</script>

<template>
  <div class="cp-toolbar">
    <div class="cp-brand">
      <span class="cp-brand-dot" :class="{ 'is-on': running > 0 }" />
      <span>PicoHop</span>
    </div>

    <div class="cp-stat">
      <span class="cp-stat-label">节点</span>
      <span class="cp-stat-value cp-num">
        <span :style="{ color: running > 0 ? 'var(--cp-success)' : 'inherit' }">{{ running }}</span>
        <span class="cp-dim" style="font-weight: 400"> / {{ total }}</span>
      </span>
    </div>

    <div class="cp-stat cp-stat-today">
      <span class="cp-stat-label">今日流量</span>
      <span class="cp-stat-value cp-num">{{ todayText }}</span>
    </div>

    <div class="cp-stat cp-stat-conn">
      <span class="cp-stat-label">活动连接</span>
      <span class="cp-stat-value cp-num">{{ activeConn }}</span>
    </div>

    <!-- 前置状态：点一下就进设置，是最常需要确认的一件事 -->
    <el-tooltip :content="`全局前置代理：${frontDesc}`" placement="bottom" :show-after="400">
      <div
        class="cp-front-chip"
        :class="{ 'is-off': !frontOk }"
        @click="ui.settingsDialog = true"
      >
        <el-icon><Connection /></el-icon>
        <span class="cp-ellipsis">{{ frontDesc }}</span>
      </div>
    </el-tooltip>

    <div class="cp-spacer" />

    <el-dropdown trigger="click" popper-class="cp-dropdown" @command="onImportCommand">
      <el-button :icon="Upload" :disabled="!!busy">
        导入<el-icon class="el-icon--right"><ArrowDown /></el-icon>
      </el-button>
      <template #dropdown>
        <el-dropdown-menu>
          <el-dropdown-item command="clipboard" :icon="Document">
            从剪贴板导入
          </el-dropdown-item>
          <el-dropdown-item command="screen" :icon="Monitor">
            从屏幕扫描二维码
          </el-dropdown-item>
          <el-dropdown-item command="text" :icon="EditPen" divided>
            手动粘贴链接…
          </el-dropdown-item>
          <!-- 订阅与"粘一条分享链接"是两件事，所以单独一项、单独一个弹窗 -->
          <el-dropdown-item command="subscribe" :icon="Link">
            添加订阅链接…
          </el-dropdown-item>
        </el-dropdown-menu>
      </template>
    </el-dropdown>

    <el-button type="primary" :icon="Plus" :disabled="!!busy" @click="actions.addNode()">
      添加节点
    </el-button>

    <el-divider direction="vertical" />

    <el-tooltip content="激活所有节点（会自动按前置顺序启动）" placement="bottom" :show-after="400">
      <el-button
        :icon="VideoPlay"
        :loading="busy === 'activateAll'"
        :disabled="!!busy || total === 0"
        @click="actions.activateAll()"
      >
        全部激活
      </el-button>
    </el-tooltip>
    <el-tooltip content="停止所有节点并释放全部本地端口" placement="bottom" :show-after="400">
      <el-button
        :icon="SwitchButton"
        :loading="busy === 'deactivateAll'"
        :disabled="!!busy || running === 0"
        @click="actions.deactivateAll()"
      >
        全部停止
      </el-button>
    </el-tooltip>

    <el-divider direction="vertical" />

    <el-tooltip :content="themeTip" placement="bottom" :show-after="400">
      <el-button :icon="themeIcon" circle @click="toggleTheme" />
    </el-tooltip>
    <el-tooltip content="运行日志" placement="bottom" :show-after="400">
      <el-button :icon="View" circle @click="ui.logDrawer = true" />
    </el-tooltip>
    <el-tooltip content="设置" placement="bottom" :show-after="400">
      <el-button :icon="Setting" circle @click="ui.settingsDialog = true" />
    </el-tooltip>
  </div>
</template>

<style scoped>
.cp-front-chip {
  display: flex;
  align-items: center;
  gap: 6px;
  /* 可伸缩：窗口变窄时先压缩它，而不是把右边的按钮挤出去 */
  flex: 0 1 auto;
  min-width: 0;
  max-width: 196px;
  height: 28px;
  padding: 0 10px;
  margin-left: 4px;
  border: 1px solid var(--cp-border);
  border-radius: 999px;
  background: var(--cp-panel-2);
  color: var(--cp-text-2);
  font-size: 12px;
  cursor: pointer;
  transition: all 0.15s;
}
.cp-front-chip:hover {
  border-color: var(--cp-brand);
  color: var(--cp-brand);
  background: var(--cp-brand-soft);
}
.cp-front-chip.is-off {
  border-style: dashed;
}
.cp-front-chip .el-icon {
  flex: 0 0 auto;
}
</style>
