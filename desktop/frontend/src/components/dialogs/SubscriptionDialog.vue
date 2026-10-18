<script setup>
/**
 * 添加机场订阅。
 *
 * 【为什么单独一个弹窗】订阅与"粘一条分享链接"是两件事：前者要填链接、分组名、
 * User-Agent、更新间隔，还要先预览一次"会进来什么"，塞在导入对话框里两边都挤得难用。
 *
 * 【User-Agent 的规矩】（使用者定的三条）
 *   1. 每次打开这个弹窗都从**设置**里取当前值来显示，不记住上次在这里临时改过的值；
 *   2. 改了它就等于改设置：发请求时用新值，并同步写回设置；
 *   3. 判断"有没有改动"要**去掉首尾空格**再比 —— 手滑多打一个空格不该算改动。
 *
 * 预览仍然是不写配置的：只看"会导入什么"，确认后再导入。
 */
import { computed, ref, watch } from 'vue'
import { useUi } from '../../ui'
import { useStore } from '../../store'
import { formatBytes, formatWhen } from '../../columns'
import api, { notify } from '../../api'

const ui = useUi()
const store = useStore()

/** 与后端 config.DefaultSubscriptionUA 保持一致（设置里为空时用它兜底显示）。 */
const DEFAULT_UA = 'clash.meta/mihomo-1.19.32'

const visible = computed({
  get: () => ui.subDialog,
  set: (v) => {
    ui.subDialog = v
  },
})

const url = ref('')
const name = ref('')
const ua = ref(DEFAULT_UA)
const viaProxy = ref(false)
const minutes = ref(0)
const busy = ref('') // '' | 'preview' | 'import'
const preview = ref(null)

/** 打开时重新从设置取一遍（规矩 1）。 */
watch(visible, async (open) => {
  if (!open) return
  url.value = ''
  name.value = ''
  viaProxy.value = false
  minutes.value = 0
  preview.value = null
  busy.value = ''
  // 从**后端**重读一次设置：用户刚在「设置 → 订阅」里改过 UA，界面缓存里还是旧值，
  // 直接拿缓存显示就违背了"每次打开都显示现在设置里的值"。
  try {
    const s = await api.getSettings()
    if (s) store.settings = s
  } catch {
    /* 拿不到就用缓存里的值兜底 */
  }
  ua.value = (store.settings?.subscriptionUserAgent || DEFAULT_UA).trim() || DEFAULT_UA
})

/**
 * UA 变了就连同设置一起写回，返回这次请求实际该用的 UA（规矩 2、3）。
 *
 * 【为什么把 UA 也发给后端】后端本来就会用设置里的 UA；这里显式带上，是为了
 * "写设置失败"这种极端情况下请求仍然用用户刚填的值，而不是悄悄退回旧值。
 */
async function syncUA() {
  const want = ua.value.trim()
  const cur = String(store.settings?.subscriptionUserAgent || '').trim()
  if (!want || want === cur) return want || cur || DEFAULT_UA
  if (!store.settingsLoaded) return want
  try {
    await api.saveSettings({ ...store.settings, subscriptionUserAgent: want })
    store.settings = { ...store.settings, subscriptionUserAgent: want }
    notify('info', 'User-Agent 已同步到设置', want)
  } catch (e) {
    notify('warning', 'User-Agent 没能写进设置', `本次请求仍会用它：${String(e?.message || e)}`)
  }
  return want
}

async function doPreview() {
  if (!url.value.trim()) return
  busy.value = 'preview'
  try {
    const uaUsed = await syncUA()
    // 显式带上这个 UA：即使"写设置"失败，这次请求也一定用用户刚填的值
    preview.value = await store.previewSubscription({
      url: url.value.trim(),
      name: name.value.trim(),
      userAgent: uaUsed,
      viaProxy: viaProxy.value,
      updateMinutes: Number(minutes.value) || 0,
    })
  } catch (e) {
    preview.value = null
    notify('error', '订阅预览失败', String(e?.message || e))
  } finally {
    busy.value = ''
  }
}

async function doImport() {
  if (!url.value.trim()) return
  busy.value = 'import'
  try {
    const uaUsed = await syncUA()
    const rep =
      (await store.importSubscription({
        url: url.value.trim(),
        name: name.value.trim(),
        // 与设置里的值相同时，后端不会把它记进订阅（见 App.ImportSubscription），
        // 于是之后改设置仍然对所有订阅生效
        userAgent: uaUsed,
        viaProxy: viaProxy.value,
        updateMinutes: Number(minutes.value) || 0,
      })) || {}
    notify(
      'success',
      '订阅已导入',
      `新增 ${rep.added || 0}、刷新 ${rep.updated || 0}（共 ${rep?.preview?.usable ?? 0} 个可用节点）`,
    )
    ui.subDialog = false
  } catch (e) {
    notify('error', '导入订阅失败', String(e?.message || e))
  } finally {
    busy.value = ''
  }
}

const previewTraffic = computed(() => {
  const info = preview.value?.userInfo || {}
  const used = (info.upload || 0) + (info.download || 0)
  if (info.total > 0) return `剩余 ${formatBytes(Math.max(0, info.total - used))} / 总 ${formatBytes(info.total)}`
  if (used > 0) return `已用 ${formatBytes(used)}`
  return '流量：机场未提供'
})

const previewExpire = computed(() => {
  const info = preview.value?.userInfo || {}
  return info.expire > 0 ? `到期 ${formatWhen(new Date(info.expire * 1000))}` : '到期时间：机场未提供'
})

/** 没改过 UA 就不显示"同步到设置"的提示（避免无意义的噪音）。 */
const uaChanged = computed(
  () => ua.value.trim() !== String(store.settings?.subscriptionUserAgent || '').trim(),
)
</script>

<template>
  <el-dialog
    v-model="visible"
    class="cp-dialog"
    title="添加订阅链接"
    width="600px"
    top="6vh"
    draggable
    :close-on-click-modal="false"
    append-to-body
  >
    <el-form class="cp-form" label-width="120px">
      <el-form-item label="订阅链接">
        <el-input
          v-model="url"
          placeholder="https://…/api/v1/client/subscribe?token=…"
          clearable
          @input="preview = null"
        />
      </el-form-item>
      <el-form-item label="分组名">
        <el-input v-model="name" placeholder="留空则用机场给的名字" />
      </el-form-item>
      <el-form-item label="User-Agent">
        <div style="display: flex; align-items: center; gap: 4px; width: 100%">
          <el-input v-model="ua" placeholder="clash.meta/mihomo-1.19.32" />
          <el-tooltip
            content="机场按它决定给你什么格式、甚至给哪些节点：同一家机场，clash 系 UA 拿到的是含 hysteria2 的超集，v2ray 系 UA 只给一部分；UA 里含 clash 还是拿到流量/到期响应头的前提。改这里会同步写回设置（发送时自动去掉首尾空格）"
            placement="top"
            :show-after="400"
          ><span class="cp-tip-icon">?</span></el-tooltip>
        </div>
      </el-form-item>
      <el-row :gutter="14">
        <el-col :span="14">
          <el-form-item label="自动更新间隔">
            <el-input v-model="minutes" type="number" style="width: 100%">
              <template #append>分钟</template>
            </el-input>
          </el-form-item>
        </el-col>
        <el-col :span="10">
          <el-form-item label="经代理更新" label-width="96px">
            <el-switch v-model="viaProxy" />
          </el-form-item>
        </el-col>
      </el-row>
    </el-form>
    <div v-if="uaChanged" class="cp-hint" style="margin: -6px 0 8px 120px">
      User-Agent 改了：预览/导入时会用它，并同步到「设置 → 订阅」
    </div>

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
        实际使用的 User-Agent：<span class="cp-mono">{{ preview.uaUsed }}</span>
      </div>
    </div>

    <template #footer>
      <el-button @click="ui.subDialog = false">关闭</el-button>
      <el-button :loading="busy === 'preview'" :disabled="!url.trim()" @click="doPreview">
        先预览
      </el-button>
      <el-button
        type="primary"
        :loading="busy === 'import'"
        :disabled="!url.trim()"
        @click="doImport"
      >
        导入订阅
      </el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.cp-sub-preview {
  margin-top: 4px;
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
