<script setup>
/**
 * 网络设置：本机对外暴露代理的方式（本地监听 / 端口 / 空闲断开）。
 *
 * 这几项原先埋在设置弹窗的「前置与网络」「运行」两页里 —— 按 docs/BOTTOM_PANEL.md
 * 第 6 步**迁到这里**（是搬家，不是复制：同一件事有两个入口，迟早改漏一处）。
 *
 * 保存走 SaveSettings，后端会整体重启节点让监听地址生效（见 app.SetSettings）。
 * 文案规则：只放标签与值，解释一律进 tooltip。
 */
import { computed, ref, watch } from 'vue'
import { useStore } from '../../store'
import api, { notify } from '../../api'

const store = useStore()

const form = ref({ allowLan: false, portStart: 10810, idleTimeoutSec: 300 })
const saving = ref(false)

function reset() {
  const s = store.settings || {}
  form.value = {
    allowLan: !!s.allowLan,
    portStart: s.portStart || 10810,
    idleTimeoutSec: s.idleTimeoutSec || 300,
  }
}
// 设置是异步加载的（store.init 在 onMounted 里 await），所以要等它到位再填一次
watch(() => store.settings, reset, { immediate: true, deep: false })
reset()

const dirty = computed(() => {
  const s = store.settings || {}
  return (
    form.value.allowLan !== !!s.allowLan ||
    Number(form.value.portStart) !== (s.portStart || 10810) ||
    Number(form.value.idleTimeoutSec) !== (s.idleTimeoutSec || 300)
  )
})

/**
 * 没配齐「本地端口鉴权」（用户名 + 密码）的节点名。
 *
 * 【为什么设置页要自己点名】审查 #22：打开「允许局域网」= 监听 0.0.0.0，
 * 此时没有凭据就等于给整条局域网开匿名开放代理，所以后端（proxy.NewServer）
 * 会**拒绝启动**这类节点。与其让用户一个个去点激活看报错，不如在这里
 * 提前把要改的节点列出来 —— 判据 listenAuth 由后端算好（config.Node.HasListenAuth），
 * 前端只负责显示，不重复实现规则。
 */
const noAuthNames = computed(() =>
  (store.nodes || []).filter((n) => !n.listenAuth).map((n) => n.name),
)

const lanAlert = computed(() => {
  if (!noAuthNames.value.length) {
    return {
      type: 'warning',
      title: '局域网内其它设备可以使用这台机器上网',
      desc: '全部节点都已配齐本地端口鉴权（用户名 + 密码），对方需要在代理设置里填上才能用。',
    }
  }
  return {
    type: 'error',
    title: '局域网内任何人都能匿名借这台机器上网',
    desc:
      `以下节点没有配齐本地端口鉴权（用户名和密码都要填），打开「允许局域网」后会被拒绝启动：` +
      `${noAuthNames.value.join('、')}。` +
      `请到「编辑节点 → 高级 → 本地端口鉴权」补上，或关掉这个开关。`,
  }
})

async function save() {
  saving.value = true
  try {
    const next = {
      ...store.settings,
      allowLan: form.value.allowLan,
      portStart: Number(form.value.portStart) || 10810,
      idleTimeoutSec: Number(form.value.idleTimeoutSec) || 300,
    }
    await api.saveSettings(next)
    store.settings = next
    notify('success', '网络设置已保存', '')
  } catch (e) {
    notify('error', '保存失败', String(e?.message || e), 0)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="cp-net">
    <div class="cp-net-form">
      <div class="cp-net-row">
        <span class="cp-net-label">允许局域网</span>
        <el-tooltip
          content="打开后节点监听 0.0.0.0，局域网内其它设备也能用；这些节点必须先配好本地端口鉴权（用户名 + 密码），否则会拒绝启动"
          placement="top"
          :show-after="400"
        >
          <el-switch v-model="form.allowLan" size="small" />
        </el-tooltip>
      </div>

      <el-alert
        v-if="form.allowLan"
        :type="lanAlert.type"
        :closable="false"
        show-icon
        :title="lanAlert.title"
        :description="lanAlert.desc"
        class="cp-net-alert"
      />

      <div class="cp-net-row">
        <span class="cp-net-label">起始端口</span>
        <el-input v-model="form.portStart" size="small" type="number" class="cp-net-num" />
      </div>

      <div class="cp-net-row">
        <span class="cp-net-label">空闲自动断开</span>
        <el-input v-model="form.idleTimeoutSec" size="small" type="number" class="cp-net-num">
          <template #append>秒</template>
        </el-input>
      </div>

      <div class="cp-net-row">
        <el-button size="small" type="primary" plain :loading="saving" :disabled="!dirty" @click="save">
          保存
        </el-button>
        <el-tooltip content="保存后会重启节点让监听设置生效" placement="top" :show-after="400">
          <span class="cp-dim">?</span>
        </el-tooltip>
      </div>
    </div>
  </div>
</template>

<style scoped>
.cp-net {
  height: 100%;
  display: flex;
  min-height: 0;
  padding: 8px 10px;
  overflow: auto;
}
.cp-net-form {
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-width: 320px;
  max-width: 460px;
}
.cp-net-row {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 12.5px;
  color: var(--cp-text-2);
}
.cp-net-label {
  flex: 0 0 96px;
  color: var(--cp-text-2);
}
.cp-net-num {
  width: 140px;
}
.cp-net-alert {
  margin: 0;
}
</style>
