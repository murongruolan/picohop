<script setup>
/**
 * 运行日志抽屉。
 */
import { computed, nextTick, ref, watch } from 'vue'
import { Delete, Document, Download } from '@element-plus/icons-vue'
import { useStore } from '../store'
import { useUi } from '../ui'
import api, { notify } from '../api'

const store = useStore()
const ui = useUi()

const scroller = ref(null)
let timer = null

const visible = computed({
  get: () => ui.logDrawer,
  set: (v) => {
    ui.logDrawer = v
  },
})

watch(visible, async (v) => {
  if (v) {
    await store.refreshLogs()
    scrollToBottom()
    timer = setInterval(async () => {
      await store.refreshLogs()
      scrollToBottom()
    }, 2000)
  } else if (timer) {
    clearInterval(timer)
    timer = null
  }
})

watch(
  () => store.logs.length,
  () => scrollToBottom(),
)

async function scrollToBottom() {
  await nextTick()
  const el = scroller.value
  if (el) el.scrollTop = el.scrollHeight
}

async function copyAll() {
  try {
    await api.copyText(store.logs.join('\n'))
    notify('success', '日志已复制到剪贴板', '')
  } catch (e) {
    notify('error', '复制失败', String(e?.message || e))
  }
}

function clearView() {
  store.logs = []
}

async function openFile() {
  try {
    await api.openLogFile()
  } catch (e) {
    notify('error', '打开失败', String(e?.message || e))
  }
}
</script>

<template>
  <el-drawer v-model="visible" title="运行日志" size="620px" :with-header="true">
    <template #header>
      <div style="display: flex; align-items: center; gap: 10px; width: 100%">
        <span style="font-weight: 600">运行日志</span>
        <span class="cp-dim" style="font-size: 12px">{{ store.logs.length }} 行</span>
        <div class="cp-spacer" />
        <el-button size="small" :icon="Download" @click="copyAll">复制全部</el-button>
        <el-button size="small" :icon="Delete" @click="clearView">清空显示</el-button>
        <el-button size="small" :icon="Document" @click="openFile">打开日志文件</el-button>
      </div>
    </template>

    <div ref="scroller" class="cp-log" style="height: 100%; overflow-y: auto">
      <div v-if="!store.logs.length" class="cp-dim">（暂无日志）</div>
      <div v-for="(line, i) in store.logs" :key="i" class="cp-log-line cp-selectable">
        {{ line }}
      </div>
    </div>
  </el-drawer>
</template>
