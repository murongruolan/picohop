<script setup>
/**
 * 「只有端口」的小弹窗。
 *
 * 从剪贴板 / 二维码识别出来的节点，协议参数已经齐了，唯独缺本地代理端口。
 * 产品要求这时候只弹这一个设置项，所以这里刻意不做成完整的编辑表单：
 * 一个端口输入 + 随机按钮 + 占用检测，确定了就入库。
 *
 * 多个节点时会显示成一个可逐行改端口的小列表。
 */
import { computed, reactive, ref, watch } from 'vue'
import { MagicStick } from '@element-plus/icons-vue'
import { useStore } from '../../store'
import { useUi } from '../../ui'
import api, { notify } from '../../api'

const store = useStore()
const ui = useUi()

const dlg = computed(() => ui.portDialog)
const visible = computed({
  get: () => dlg.value.open,
  set: (v) => {
    if (!v) ui.closePortDialog()
  },
})

/** 每一行：{pending, port, status, error} */
const rows = reactive([])
const saving = ref(false)

const sourceLabel = computed(() => {
  const map = { clipboard: '剪贴板', screen: '屏幕二维码', text: '文本' }
  return map[dlg.value.source] || '导入'
})

watch(visible, async (v) => {
  if (!v) return
  rows.splice(0, rows.length)
  const list = dlg.value.pending || []
  for (const p of list) {
    rows.push({
      pending: p,
      port: p.suggestedPort || store.meta?.defaultPortStart || 10810,
      status: null,
      error: '',
    })
  }
  // 逐个复核端口（后端算的建议值也可能刚好被别人抢走）
  for (const r of rows) await checkRow(r)
})

async function checkRow(row) {
  row.error = ''
  const p = Number(row.port)
  if (!(p >= 1024 && p <= 65535)) {
    row.status = null
    row.error = '端口应在 1024-65535 之间'
    return
  }
  try {
    const st = await api.checkPort(p, '')
    row.status = st
    if (!st.free) row.error = st.message || '端口不可用'
  } catch (e) {
    row.status = null
    row.error = String(e?.message || e)
  }
}

/**
 * 随机端口：后端已实现"随机 → 检测 → 占用则重随机，最多 5 次"，
 * 抽不到空闲端口时会返回固定文案「多次随机仍未随机到空闲端口」。
 */
async function randomRow(row) {
  try {
    const p = await api.randomPort('')
    row.port = p
    await checkRow(row)
  } catch (e) {
    const msg = String(e?.message || e)
    row.error = msg
    notify('warning', '随机端口失败', msg)
  }
}

/** 同一批里不允许两行填同一个端口。 */
function duplicatePorts() {
  const seen = new Map()
  for (const r of rows) {
    const p = Number(r.port)
    if (seen.has(p)) return p
    seen.set(p, r)
  }
  return 0
}

async function confirm() {
  for (const r of rows) {
    if (r.error) {
      notify('warning', '端口有问题', r.error)
      return
    }
  }
  const dup = duplicatePorts()
  if (dup) {
    notify('warning', '端口重复', `本批里有多个节点都填了 ${dup}`)
    return
  }

  saving.value = true
  let ok = 0
  const failed = []
  for (const r of rows) {
    const node = JSON.parse(JSON.stringify(r.pending.node))
    node.listenPort = Number(r.port)
    // 导入进来的节点默认走全局前置会更好用吗？不 —— 产品要求"不选就是直连"，
    // 所以这里保持后端解析出来的原样（空 = 直连）。
    try {
      await api.addNode(node)
      ok++
    } catch (e) {
      failed.push(`「${node.name || node.server}」：${e?.message || e}`)
    }
  }
  saving.value = false

  ui.closePortDialog()
  if (ok) notify('success', '已添加节点', `成功 ${ok} 个${failed.length ? `，失败 ${failed.length} 个` : ''}`)
  if (failed.length) notify('error', '部分节点添加失败', failed.join('\n'))
}
</script>

<template>
  <el-dialog
    v-model="visible"
    class="cp-dialog"
    :title="`设置本地代理端口（来自${sourceLabel}）`"
    width="520px"
    :close-on-click-modal="false"
    append-to-body
  >
    <div class="cp-hint" style="margin-bottom: 12px">
      已经识别到 {{ rows.length }} 个节点。协议参数不需要再确认，
      只需要给每个节点指定一个本地代理端口 —— 端口就是以后在别的软件里要填的代理地址。
    </div>

    <div v-for="(r, i) in rows" :key="i" class="cp-port-line">
      <div class="cp-port-line-head">
        <span class="cp-tag is-brand">{{ r.pending.protocolLabel }}</span>
        <span class="cp-ellipsis" style="flex: 1; font-weight: 600">
          {{ r.pending.node.name || r.pending.server }}
        </span>
        <span class="cp-dim cp-mono" style="font-size: 11.5px">
          {{ r.pending.server }}:{{ r.pending.port }} · {{ r.pending.transport }}
        </span>
      </div>
      <div class="cp-port-row">
        <el-input
          v-model="r.port"
          style="width: 178px"
          type="number"
          @blur="checkRow(r)"
        >
          <template #prepend>127.0.0.1:</template>
        </el-input>
        <el-button :icon="MagicStick" @click="randomRow(r)">随机</el-button>
        <span v-if="r.error" class="cp-error-text">{{ r.error }}</span>
        <span v-else-if="r.status && r.status.free" class="cp-ok-text">端口可用</span>
      </div>
    </div>

    <template #footer>
      <el-button @click="ui.closePortDialog()">取消</el-button>
      <el-button type="primary" :loading="saving" @click="confirm">
        添加{{ rows.length > 1 ? `这 ${rows.length} 个节点` : '节点' }}
      </el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.cp-port-line {
  padding: 10px 12px;
  margin-bottom: 10px;
  border: 1px solid var(--cp-border);
  border-radius: var(--cp-radius);
  background: var(--cp-panel-2);
}
.cp-port-line-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}
</style>
