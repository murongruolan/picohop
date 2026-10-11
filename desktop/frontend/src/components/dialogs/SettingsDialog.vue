<script setup>
/**
 * 设置。
 *
 * 刻意保持精简：只放主流客户端/主流客户端里**我们真的会用到**的项。
 * 没有订阅、没有开机自启、没有一堆用不上的开关。
 */
import { computed, reactive, ref, watch } from 'vue'
import { Folder, Refresh, RefreshRight } from '@element-plus/icons-vue'
import { useStore } from '../../store'
import { useUi } from '../../ui'
import api, { notify } from '../../api'

const store = useStore()
const ui = useUi()

const visible = computed({
  get: () => ui.settingsDialog,
  set: (v) => {
    ui.settingsDialog = v
  },
})

const tab = ref('front')
const saving = ref(false)
const testingFront = ref(false)
const frontTest = ref(null)

const form = reactive(blankSettings())
const front = reactive(blankFront())

let originalFront = ''
let originalSettings = ''

function blankSettings() {
  return {
    defaultListenHost: '127.0.0.1',
    allowLan: false,
    portStart: 10810,
    idleTimeoutSec: 300,
    autoActivateOnLaunch: true,
    tcpTestTimeoutMs: 5000,
    testConcurrency: 8,
    realTestUrl: '',
    speedTestUrl: '',
    speedTestSeconds: 8,
    theme: 'system',
    notificationDurationMs: 4000,
    logLines: 500,
  }
}

function blankFront() {
  return { type: 'socks5', host: '127.0.0.1', port: 10808, username: '', password: '', timeoutMs: 10000 }
}

watch(visible, async (v) => {
  if (!v) return
  frontTest.value = null
  try {
    const [s, f] = await Promise.all([api.getSettings(), api.getFront()])
    Object.assign(form, blankSettings(), s || {})
    Object.assign(front, blankFront(), f || {})
    originalFront = JSON.stringify(front)
    originalSettings = JSON.stringify(form)
  } catch (e) {
    notify('error', '读取设置失败', String(e?.message || e))
  }
})

const frontDirty = computed(() => JSON.stringify(front) !== originalFront)
const settingsDirty = computed(() => JSON.stringify(form) !== originalSettings)

const frontTypeOptions = [
  { value: 'socks5', label: 'SOCKS5' },
  { value: 'http', label: 'HTTP' },
  { value: 'direct', label: '直连（不使用前置）' },
]

const themeOptions = [
  { value: 'system', label: '跟随系统' },
  { value: 'dark', label: '深色' },
  { value: 'light', label: '浅色' },
]

async function save() {
  saving.value = true
  try {
    if (frontDirty.value) await api.saveFront({ ...front })
    if (settingsDirty.value) await api.saveSettings({ ...form })
    if (!frontDirty.value && !settingsDirty.value) notify('info', '没有改动', '')
    else notify('success', '设置已保存', '')
    // 主题即时生效，不用等重启
    store.setTheme(form.theme)
    ui.settingsDialog = false
  } catch (e) {
    notify('error', '保存失败', String(e?.message || e))
  } finally {
    saving.value = false
  }
}

async function testFront() {
  testingFront.value = true
  frontTest.value = null
  try {
    // 先保存前置，否则测的还是旧的
    if (frontDirty.value) {
      await api.saveFront({ ...front })
      originalFront = JSON.stringify(front)
    }
    const r = await api.testFront()
    frontTest.value = r
  } catch (e) {
    frontTest.value = { ok: false, error: String(e?.message || e) }
  } finally {
    testingFront.value = false
  }
}

async function resetAll() {
  const { ElMessageBox } = await import('element-plus')
  try {
    await ElMessageBox.confirm('把「设置」恢复为默认值？节点与前置不会被删除。', '恢复默认设置', {
      type: 'warning',
    })
  } catch {
    return
  }
  try {
    const s = await api.resetSettings()
    Object.assign(form, blankSettings(), s || {})
    originalSettings = JSON.stringify(form)
    store.setTheme(form.theme)
  } catch (e) {
    notify('error', '恢复失败', String(e?.message || e))
  }
}

async function openPath(fn, label) {
  try {
    await fn()
  } catch (e) {
    notify('error', `打开${label}失败`, String(e?.message || e))
  }
}

const meta = computed(() => store.meta || {})
</script>

<template>
  <el-dialog
    v-model="visible"
    class="cp-dialog"
    title="设置"
    width="680px"
    top="4vh"
    draggable
    :close-on-click-modal="false"
    append-to-body
  >
    <el-tabs v-model="tab">
      <!-- ------------------------------------------------------------ -->
      <el-tab-pane label="前置与网络" name="front">
        <div class="cp-section">全局前置代理</div>
        <div class="cp-hint" style="margin-bottom: 12px">
          节点在「前置节点」里选「全局前置代理」时会走这里。最常见的用法是填本机前置代理 的
          SOCKS5 端口（默认 127.0.0.1:10808）。
        </div>
        <el-form class="cp-form" label-width="96px">
          <el-row :gutter="14">
            <el-col :span="9">
              <el-form-item label="类型">
                <el-select v-model="front.type" style="width: 100%">
                  <el-option v-for="o in frontTypeOptions" :key="o.value" :label="o.label" :value="o.value" />
                </el-select>
              </el-form-item>
            </el-col>
            <el-col :span="10">
              <el-form-item label="地址">
                <el-input v-model="front.host" :disabled="front.type === 'direct'" placeholder="127.0.0.1" />
              </el-form-item>
            </el-col>
            <el-col :span="5">
              <el-form-item label="端口" label-width="52px">
                <el-input v-model="front.port" type="number" :disabled="front.type === 'direct'" />
              </el-form-item>
            </el-col>
          </el-row>
          <el-row :gutter="14" v-if="front.type !== 'direct'">
            <el-col :span="12">
              <el-form-item label="用户名">
                <el-input v-model="front.username" placeholder="可选" />
              </el-form-item>
            </el-col>
            <el-col :span="12">
              <el-form-item label="密码">
                <el-input v-model="front.password" type="password" show-password placeholder="可选" />
              </el-form-item>
            </el-col>
          </el-row>
          <el-form-item label=" ">
            <div style="display: flex; align-items: center; gap: 10px">
              <el-button :icon="Refresh" :loading="testingFront" @click="testFront">
                测试前置代理连通性
              </el-button>
              <span v-if="frontTest?.ok" class="cp-ok-text">
                可用 · 出口 IP {{ frontTest.ip }}（{{ frontTest.ms }} ms）
              </span>
              <span v-else-if="frontTest && !frontTest.ok" class="cp-error-text">
                {{ frontTest.error }}
              </span>
            </div>
          </el-form-item>
        </el-form>

        <div class="cp-section">本地监听</div>
        <el-form class="cp-form" label-width="96px">
          <el-form-item label="允许局域网">
            <el-switch v-model="form.allowLan" />
            <span class="cp-hint" style="margin-left: 10px">
              打开后节点会监听 0.0.0.0，局域网内其它设备也能用
            </span>
          </el-form-item>
          <el-alert
            v-if="form.allowLan"
            type="warning"
            :closable="false"
            show-icon
            title="注意安全"
            description="打开后局域网内任何人都能通过你的电脑上网。建议在「编辑节点 → 高级」里给本地端口加上用户名密码。同时 Windows 防火墙可能会弹出提示。"
            style="margin-bottom: 12px"
          />
          <el-form-item label="起始端口">
            <el-input v-model="form.portStart" type="number" style="width: 132px" />
            <span class="cp-hint" style="margin-left: 10px">
              新增节点时的默认端口从这里开始排（默认 10810）
            </span>
          </el-form-item>
        </el-form>
      </el-tab-pane>

      <!-- ------------------------------------------------------------ -->
      <el-tab-pane label="测试" name="test">
        <div class="cp-section">测试参数</div>
        <el-form class="cp-form" label-width="120px">
          <el-row :gutter="14">
            <el-col :span="12">
              <el-form-item label="TCP 超时">
                <el-input v-model="form.tcpTestTimeoutMs" type="number" style="width: 120px">
                  <template #append>ms</template>
                </el-input>
              </el-form-item>
            </el-col>
            <el-col :span="12">
              <el-form-item label="并发数">
                <el-input v-model="form.testConcurrency" type="number" style="width: 120px" />
              </el-form-item>
            </el-col>
          </el-row>
          <el-form-item label="真连接地址">
            <el-input v-model="form.realTestUrl" placeholder="http://cp.cloudflare.com/generate_204" />
          </el-form-item>
          <el-form-item label="测速地址">
            <el-input v-model="form.speedTestUrl" placeholder="https://speed.cloudflare.com/__down?bytes=104857600" />
          </el-form-item>
          <el-form-item label="测速时长">
            <el-input v-model="form.speedTestSeconds" type="number" style="width: 120px">
              <template #append>秒</template>
            </el-input>
            <span class="cp-hint" style="margin-left: 10px">
              超时会主动断开，避免把流量白跑掉
            </span>
          </el-form-item>
        </el-form>
        <div class="cp-hint">
          「真连接延迟」是走完整链路去请求一个极小的 204 页面，最接近真实使用体验；
          「TCP 延迟」只对服务器地址做一次 TCP 握手，不经代理，用来快速筛掉不可达的节点。
        </div>
      </el-tab-pane>

      <!-- ------------------------------------------------------------ -->
      <el-tab-pane label="运行与界面" name="appearance">
        <div class="cp-section">运行</div>
        <el-form class="cp-form" label-width="140px">
          <el-form-item label="空闲自动断开">
            <el-input v-model="form.idleTimeoutSec" type="number" style="width: 120px">
              <template #append>秒</template>
            </el-input>
          </el-form-item>
          <el-form-item label="启动时自动激活">
            <el-switch v-model="form.autoActivateOnLaunch" />
            <span class="cp-hint" style="margin-left: 10px">
              自动激活上次处于激活状态的节点
            </span>
          </el-form-item>
        </el-form>

        <div class="cp-section">界面</div>
        <el-form class="cp-form" label-width="140px">
          <el-form-item label="主题">
            <el-radio-group v-model="form.theme">
              <el-radio-button v-for="t in themeOptions" :key="t.value" :value="t.value">
                {{ t.label }}
              </el-radio-button>
            </el-radio-group>
          </el-form-item>
          <el-form-item label="通知停留时长">
            <el-input v-model="form.notificationDurationMs" type="number" style="width: 120px">
              <template #append>ms</template>
            </el-input>
          </el-form-item>
          <el-form-item label="日志保留行数">
            <el-input v-model="form.logLines" type="number" style="width: 120px" />
          </el-form-item>
        </el-form>
      </el-tab-pane>

      <!-- ------------------------------------------------------------ -->
      <el-tab-pane label="关于" name="about">
        <div class="cp-section">ChainProxy</div>
        <el-descriptions :column="1" border size="small">
          <el-descriptions-item label="版本">{{ meta.version || 'dev' }}</el-descriptions-item>
          <el-descriptions-item label="内嵌引擎">
            <span v-if="meta.engineOk" class="cp-ok-text">
              sing-box（with_utls / with_quic），支持 VMess、VLESS+Reality、Shadowsocks、Trojan、Hysteria2
            </span>
            <span v-else class="cp-error-text">{{ meta.engineError }}</span>
          </el-descriptions-item>
          <el-descriptions-item label="开机自启动">
            <span>不注册（本程序不会开机自启）</span>
          </el-descriptions-item>
          <el-descriptions-item label="配置文件">
            <span class="cp-mono cp-selectable">{{ meta.configPath }}</span>
          </el-descriptions-item>
          <el-descriptions-item label="数据目录">
            <span class="cp-mono cp-selectable">{{ meta.dataDir }}</span>
          </el-descriptions-item>
        </el-descriptions>

        <div style="display: flex; gap: 8px; margin-top: 14px">
          <el-button :icon="Folder" @click="openPath(api.openDataDir, '数据目录')">打开数据目录</el-button>
          <el-button :icon="Folder" @click="openPath(api.openConfigFile, '配置文件')">定位配置文件</el-button>
          <el-button :icon="Folder" @click="openPath(api.openLogFile, '日志文件')">定位日志文件</el-button>
        </div>

        <div class="cp-hint" style="margin-top: 14px">
          本程序是纯用户态实现：不改系统路由表、不装驱动、不需要管理员权限，
          也因此不会注册任何开机自启动项。
        </div>
      </el-tab-pane>
    </el-tabs>

    <template #footer>
      <el-button :icon="RefreshRight" @click="resetAll">恢复默认设置</el-button>
      <div class="cp-spacer" />
      <el-button type="warning" @click="ui.settingsDialog = false">取消</el-button>
      <el-button type="primary" :loading="saving" @click="save">保存</el-button>
    </template>
  </el-dialog>
</template>
