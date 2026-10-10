<script setup>
/**
 * 手动粘贴分享链接。
 *
 * 和「从剪贴板导入」是同一套解析逻辑，只是把输入权交给用户 ——
 * 有时候链接在网页里、聊天记录里，剪贴板里那条不是想要的。
 */
import { computed, ref } from 'vue'
import { Monitor, Upload } from '@element-plus/icons-vue'
import { useUi } from '../../ui'
import { useActions } from '../../actions'

const ui = useUi()
const actions = useActions()

const visible = computed({
  get: () => ui.importDialog,
  set: (v) => {
    ui.importDialog = v
  },
})

const text = ref('')
const parsing = ref(false)

const example = 'vless://uuid@host:443?security=reality&sni=example.com&type=tcp#节点名'

async function parse() {
  const v = text.value.trim()
  if (!v) return
  parsing.value = true
  try {
    await actions.importFromText(v)
    text.value = ''
  } finally {
    parsing.value = false
  }
}

async function fromClipboard() {
  await actions.importFromClipboard()
}

async function fromScreen() {
  await actions.scanScreen()
}
</script>

<template>
  <el-dialog
    v-model="visible"
    class="cp-dialog"
    title="导入节点"
    width="560px"
    :close-on-click-modal="false"
    append-to-body
  >
    <div style="display: flex; gap: 8px; margin-bottom: 12px">
      <el-button :icon="Upload" @click="fromClipboard">从剪贴板导入</el-button>
      <el-button :icon="Monitor" @click="fromScreen">从屏幕扫描二维码</el-button>
    </div>

    <div class="cp-section">手动粘贴分享链接</div>
    <el-input
      v-model="text"
      type="textarea"
      :rows="6"
      resize="none"
      placeholder="支持 vless:// / vmess:// / ss:// / trojan:// / hysteria2://&#10;可以一次粘贴多条，每行一条"
    />
    <div class="cp-hint" style="margin-top: 6px">
      例如：<span class="cp-mono cp-selectable">{{ example }}</span>
    </div>

    <template #footer>
      <el-button @click="ui.importDialog = false">关闭</el-button>
      <el-button type="primary" :loading="parsing" :disabled="!text.trim()" @click="parse">
        解析并添加
      </el-button>
    </template>
  </el-dialog>
</template>
