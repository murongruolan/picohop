<script setup>
/**
 * 导入对话框：分享链接 + 机场订阅。
 *
 * 分享链接这条路与「从剪贴板导入」是同一套解析逻辑，只是把输入权交给用户 ——
 * 有时候链接在网页里、聊天记录里，剪贴板里那条不是想要的。
 *
 * 订阅这条路**先预览再导入**：机场订阅最容易出的三类问题（链接过期返回网页、
 * UA 不被机场认导致只给几个节点、格式我们不认识）在用户眼里长得一模一样。
 * 预览能把"能拿到多少个节点、过滤掉多少条机场信息行、跳过哪些协议"摆出来，
 * 确认之后再真正写进配置。
 */
import { computed, ref } from 'vue'
import { Monitor, Upload } from '@element-plus/icons-vue'
import { useUi } from '../../ui'
import { useStore } from '../../store'
import { useActions } from '../../actions'
import { formatBytes, formatWhen } from '../../columns'
import { notify } from '../../api'

const ui = useUi()
const store = useStore()
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

// ------------------------------------------------------------------
// 机场订阅
// ------------------------------------------------------------------
const subURL = ref('')
const subName = ref('')
const subUA = ref('')
const subViaProxy = ref(false)
const subMinutes = ref(0)
const subBusy = ref('') // '' | 'preview' | 'import'
const preview = ref(null)
const advanced = ref(false)

function subReq() {
  return {
    url: subURL.value.trim(),
    name: subName.value.trim(),
    userAgent: subUA.value.trim(),
    viaProxy: subViaProxy.value,
    updateMinutes: Number(subMinutes.value) || 0,
  }
}

function resetSub() {
  subURL.value = ''
  subName.value = ''
  subUA.value = ''
  subViaProxy.value = false
  subMinutes.value = 0
  preview.value = null
  advanced.value = false
}

async function doPreview() {
  if (!subURL.value.trim()) return
  subBusy.value = 'preview'
  try {
    preview.value = await store.previewSubscription(subReq())
  } catch (e) {
    preview.value = null
    notify('error', '订阅预览失败', String(e?.message || e))
  } finally {
    subBusy.value = ''
  }
}

async function doImport() {
  if (!subURL.value.trim()) return
  subBusy.value = 'import'
  try {
    const rep = await store.importSubscription(subReq())
    const total = rep?.preview?.usable ?? 0
    notify(
      'success',
      '订阅已导入',
      `新增 ${rep?.added || 0}、刷新 ${rep?.updated || 0}（共 ${total} 个可用节点）`,
    )
    resetSub()
    ui.importDialog = false
  } catch (e) {
    notify('error', '导入订阅失败', String(e?.message || e))
  } finally {
    subBusy.value = ''
  }
}

/** 预览里的流量一行：机场没给 total 就退回"已用"，都没给就 —。 */
const previewTraffic = computed(() => {
  const info = preview.value?.userInfo || {}
  const used = (info.upload || 0) + (info.download || 0)
  if (info.total > 0) return `剩余 ${formatBytes(Math.max(0, info.total - used))} / 总 ${formatBytes(info.total)}`
  if (used > 0) return `已用 ${formatBytes(used)}`
  return '流量：机场未提供'
})

const previewExpire = computed(() => {
  const info = preview.value?.userInfo || {}
  if (info.expire > 0) return `到期 ${formatWhen(new Date(info.expire * 1000))}`
  return '到期时间：机场未提供'
})
</script>

<template>
  <el-dialog
    v-model="visible"
    class="cp-dialog"
    title="导入"
    width="600px"
    top="4vh"
    draggable
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
      :rows="5"
      resize="none"
      placeholder="支持 vless:// / vmess:// / ss:// / trojan:// / hysteria2://&#10;可以一次粘贴多条，每行一条"
    />
    <div class="cp-hint" style="margin-top: 6px">
      例如：<span class="cp-mono cp-selectable">{{ example }}</span>
    </div>
    <div style="display: flex; justify-content: flex-end; margin-top: 8px">
      <el-button type="primary" :loading="parsing" :disabled="!text.trim()" @click="parse">
        解析并添加
      </el-button>
    </div>

    <div class="cp-section">机场订阅</div>
    <el-input
      v-model="subURL"
      placeholder="https://…/api/v1/client/subscribe?token=…"
      clearable
      @input="preview = null"
    />
    <div style="display: flex; align-items: center; gap: 10px; margin-top: 8px">
      <el-input v-model="subName" placeholder="分组名（留空用机场给的名字）" style="max-width: 300px" />
      <el-checkbox v-model="subViaProxy">经代理更新</el-checkbox>
      <a class="cp-hint" style="margin-left: auto; cursor: pointer" @click="advanced = !advanced">
        {{ advanced ? '收起高级' : '高级' }}
      </a>
    </div>

    <template v-if="advanced">
      <div style="display: flex; align-items: center; gap: 10px; margin-top: 8px">
        <el-input v-model="subUA" placeholder="User-Agent（留空用设置里的默认值）" />
      </div>
      <div style="display: flex; align-items: center; gap: 8px; margin-top: 8px">
        <span class="cp-hint">自动更新间隔</span>
        <el-input v-model="subMinutes" type="number" style="width: 110px">
          <template #append>分钟</template>
        </el-input>
        <span class="cp-hint">填 0 表示不自动更新</span>
      </div>
    </template>

    <!-- 预览结果：把"会导入什么"摆出来，确认之后再写配置 -->
    <div v-if="preview" class="cp-sub-preview">
      <div class="cp-sub-line">
        <span class="cp-tag is-brand">{{ preview.format }}</span>
        <span>可用 <b>{{ preview.usable }}</b> / {{ preview.total }}</span>
        <span v-if="preview.fakeRows" class="cp-dim">过滤机场信息行 {{ preview.fakeRows }}</span>
        <span v-if="preview.duplicated" class="cp-dim">重复 {{ preview.duplicated }}</span>
        <span v-if="preview.invalid" class="cp-dim">字段不全 {{ preview.invalid }}</span>
        <span v-if="preview.skipped?.length" class="cp-dim">跳过 {{ preview.skipped.join(' ') }}</span>
      </div>
      <div class="cp-sub-line cp-dim">{{ previewTraffic }} · {{ previewExpire }}</div>
      <div v-if="preview.sampleNames?.length" class="cp-sub-line cp-hint">
        例如：{{ preview.sampleNames.join('、') }}
      </div>
      <div v-if="preview.notes?.length" class="cp-sub-line cp-error-text">
        {{ preview.notes.join('；') }}
      </div>
      <div class="cp-sub-line cp-hint">
        下载用的 User-Agent：<span class="cp-mono">{{ preview.uaUsed }}</span>
      </div>
    </div>

    <template #footer>
      <el-button @click="ui.importDialog = false">关闭</el-button>
      <el-button :loading="subBusy === 'preview'" :disabled="!subURL.trim()" @click="doPreview">
        先预览
      </el-button>
      <el-button
        type="primary"
        :loading="subBusy === 'import'"
        :disabled="!subURL.trim()"
        @click="doImport"
      >
        导入订阅
      </el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.cp-sub-preview {
  margin-top: 10px;
  padding: 9px 11px;
  border: 1px solid var(--cp-border);
  border-radius: var(--cp-radius);
  background: var(--cp-panel-2);
}
.cp-sub-line {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  font-size: 12.5px;
  color: var(--cp-text);
  line-height: 1.7;
}
.cp-sub-line + .cp-sub-line {
  margin-top: 2px;
}
</style>
