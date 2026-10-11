<script setup>
/**
 * 分享对话框：二维码 + 链接文本 + 下载图片 / 复制文本。
 *
 * 布局按产品要求：上方正方形二维码，中间文本框展示协议文本，下方两个按钮。
 */
import { computed, ref, watch } from 'vue'
import { CopyDocument, Download, Loading, Warning } from '@element-plus/icons-vue'
import { useUi } from '../../ui'
import api, { notify } from '../../api'

const ui = useUi()

const dlg = computed(() => ui.shareDialog)
const visible = computed({
  get: () => dlg.value.open,
  set: (v) => {
    if (!v) ui.closeShare()
  },
})

const result = computed(() => dlg.value.result)
const link = ref('')

watch(result, (r) => {
  link.value = r?.link || ''
})

async function copyLink() {
  try {
    await api.copyText(link.value)
    notify('success', '已复制分享链接', '')
  } catch (e) {
    notify('error', '复制失败', String(e?.message || e))
  }
}

async function download() {
  try {
    const path = await api.saveShareImage(dlg.value.nodeId)
    if (!path) notify('info', '已取消保存', '')
  } catch (e) {
    notify('error', '保存失败', String(e?.message || e))
  }
}
</script>

<template>
  <el-dialog
    v-model="visible"
    class="cp-dialog"
    title="分享节点"
    width="420px"
    top="4vh"
    draggable
    :close-on-click-modal="false"
    append-to-body
  >
    <template v-if="dlg.loading">
      <div class="cp-empty" style="padding: 40px 0">
        <el-icon class="is-loading" :size="22"><Loading /></el-icon>
        <span class="cp-dim">正在生成二维码…</span>
      </div>
    </template>

    <template v-else-if="result">
      <div style="display: flex; align-items: center; gap: 8px; margin-bottom: 12px">
        <span class="cp-tag is-brand">{{ result.protocolLabel }}</span>
        <span style="font-weight: 600">{{ result.name }}</span>
        <span class="cp-dim" style="font-size: 12px">{{ result.transport }}</span>
      </div>

      <div class="cp-share-qr">
        <img v-if="result.qrDataUrl" :src="result.qrDataUrl" alt="分享二维码" />
        <div v-else class="cp-empty" style="padding: 60px 0; width: 232px">
          <el-icon :size="20"><Warning /></el-icon>
          <span class="cp-dim">二维码生成失败</span>
        </div>
      </div>

      <el-alert
        v-if="result.error"
        :title="result.error"
        type="warning"
        :closable="false"
        show-icon
        style="margin-top: 10px"
      />

      <el-input
        v-model="link"
        class="cp-share-text"
        type="textarea"
        :rows="4"
        readonly
        resize="none"
        placeholder="分享链接"
      />

      <div class="cp-hint" style="margin-top: 6px">
        这个链接里包含节点的全部连接凭据，请只分享给自己信任的人。
        部分协议（例如 VMess）的分享格式无法携带「跳过证书校验」这一位。
      </div>
    </template>

    <template #footer>
      <el-button :icon="Download" :disabled="!result?.link" @click="download">
        下载图片
      </el-button>
      <el-button type="primary" :icon="CopyDocument" :disabled="!link" @click="copyLink">
        复制文本
      </el-button>
    </template>
  </el-dialog>
</template>
