<script setup>
/**
 * 设置。
 *
 * 刻意保持精简：只放主流客户端里**我们真的会用到**的项。
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

// 打开时停在哪一页：自测用 ui.settingsInitialTab 指定（-open shortcuts 用）
const tab = ref(ui.settingsInitialTab || 'front')
const saving = ref(false)
const testingFront = ref(false)
const frontTest = ref(null)

const form = reactive(blankSettings())
const front = reactive(blankFront())

// ---------------------------------------------------------------------------
// 快捷键录制
//
// 自己做而不是拉一个键盘录制组件：需求很简单 —— 点一下开始录，
// 按下组合键就记下来，并且强制"至少两个键"。
// ---------------------------------------------------------------------------
const recording = ref('') // 正在录制哪个字段；空表示没在录
const shortcutRows = [
  { key: 'shortcutToggleWindow', label: '显示 / 隐藏主窗口', hint: '按一下收回托盘，再按一下叫回来' },
  { key: 'shortcutToggleAll', label: '激活全部 / 停止全部', hint: '有节点在跑就全停，否则全开' },
]
const MODIFIER_KEYS = ['Control', 'Alt', 'Shift', 'Meta']

/**
 * 浏览器 e.key → 加速键名。
 *
 * 【为什么必须有这张表】Wails 的加速键解析器**按 "+" 切分**，并且只认它自己那张
 * 键名表（global_shortcut_windows.go 的 winKeyCodes）。而空格键在浏览器里
 * e.key === ' ' —— 直接拼出来就是 "Ctrl+Alt+ "，解析器拿到一个空格当键名，
 * 报 "' ' is not a valid key"，于是"设置看着保存了、按下去毫无反应"（用户实报）。
 * 它认的名字是 "Space"；方向键同理要写成 Up/Down/Left/Right 而不是 ArrowUp 之类。
 */
const KEY_ALIASES = {
  ' ': 'Space',
  Spacebar: 'Space',
  ArrowUp: 'Up',
  ArrowDown: 'Down',
  ArrowLeft: 'Left',
  ArrowRight: 'Right',
  PageUp: 'Page Up',
  PageDown: 'Page Down',
  Enter: 'Enter',
  Tab: 'Tab',
  Backspace: 'Backspace',
  Delete: 'Delete',
  Home: 'Home',
  End: 'End',
  // 加号不能当键名：加速键本身是用 "+" 拼的，写进去会被切碎。
  '+': '',
}

/** 把一次按键换算成加速键名；返回空串表示这个键不能用作快捷键。 */
function keyNameOf(e) {
  if (Object.prototype.hasOwnProperty.call(KEY_ALIASES, e.key)) return KEY_ALIASES[e.key]
  if (/^F\d{1,2}$/.test(e.key)) return e.key // F1–F24
  if (e.key.length === 1 && /[A-Za-z0-9]/.test(e.key)) return e.key.toUpperCase()
  return ''
}

function startRecord(key, ev) {
  recording.value = key
  // 让这个 div 拿到焦点，否则收不到 keydown
  ev?.currentTarget?.focus?.()
}

function clearShortcut(key) {
  // "none" 是后端约定的"显式关闭"（空字符串在配置里表示"没设过、用默认值"）
  form[key] = 'none'
}

function onRecordKey(e) {
  if (!recording.value) return
  // 录制期间把按键吃掉，免得触发对话框自己的快捷键逻辑
  e.preventDefault()
  e.stopPropagation()

  if (e.key === 'Escape') {
    recording.value = ''
    return
  }
  // 只按了修饰键：继续等他按真正的键，不当作结束
  if (MODIFIER_KEYS.includes(e.key)) return

  const mods = []
  if (e.ctrlKey) mods.push('Ctrl')
  if (e.altKey) mods.push('Alt')
  if (e.shiftKey) mods.push('Shift')
  if (mods.length === 0) {
    notify('warning', '需要两个以上组合键', '请按住 Ctrl / Alt / Shift 之后再按一个普通键，例如 Ctrl+Alt+C')
    return
  }
  // 键名统一成后端认识的写法：单字符大写，其余原样（F9 / Space / ArrowUp …）
  const key = keyNameOf(e)
  if (!key) {
    // 明确拒绝，而不是注册一个"看着有、按了没用"的组合
    notify(
      'warning',
      '这个键不能用作快捷键',
      `「${e.key}」不在支持的键名里。请用字母、数字、F1–F24，或空格 / 方向键 / Home / End 这类功能键。`,
    )
    return
  }
  form[recording.value] = [...mods, key].join('+')
  recording.value = ''
}

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
    realTestAttempts: 2,
    speedTestUrl: '',
    speedTestSeconds: 8,
    theme: 'system',
    notificationDurationMs: 4000,
    logLines: 500,
    shortcutToggleWindow: 'Ctrl+Alt+C',
    shortcutToggleAll: 'Ctrl+Alt+Shift+A',
    // 订阅下载用的 UA。默认值与后端 config.DefaultSubscriptionUA 保持一致。
    subscriptionUserAgent: 'clash.meta/mihomo-1.19.32',
  }
}

function blankFront() {
  return { type: 'socks5', host: '127.0.0.1', port: 10808, username: '', password: '', timeoutMs: 10000 }
}

watch(visible, async (v) => {
  if (!v) return
  // 每次打开都从第一页开始（或自测指定的那一页）。
  // 不能只在 setup 里初始化：组件在应用启动时就创建了，那时自测入口还没跑，
  // 设置不进去 —— 踩过一次，截图里看到的仍然是第一页。
  tab.value = ui.settingsInitialTab || 'front'
  ui.settingsInitialTab = ''
  frontTest.value = null
  // 【每次打开都回到默认位置 —— 靠 destroy-on-close，不要自己清 transform】
  //
  // 走过的弯路：一开始我在打开时把 .el-dialog 的 inline transform 清成空。
  // 位置确实回去了，但**第一次拖动会"跳"一下**：Element Plus 把拖动位移存在
  // 自己的闭包里（useDraggable 的 transform.offsetX/offsetY），我只清了 DOM，
  // 没清它内部的值 —— 下次按住移动时它算的是"内部旧值 + 位移"，于是弹窗先弹回
  // 上次的位置、之后才跟手（用户实报："稍微动一下就往拖动方向大动作一下"）。
  //
  // destroy-on-close 让内容在关闭时真的销毁、下次打开重新挂载，拖动组件的闭包
  // 跟着重建 → DOM 位移和内部值一起归零：既回默认位置，也不会跳。
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
    class="cp-dialog cp-dlg-settings"
    title="设置"
    width="680px"
    top="4vh"
    draggable
    align-center
    destroy-on-close
    :close-on-click-modal="false"
    append-to-body
  >
    <el-tabs v-model="tab">
      <!-- ------------------------------------------------------------ -->
      <el-tab-pane label="前置与网络" name="front">
        <div class="cp-section">
          全局前置代理
          <el-tooltip
            content="节点在「前置节点」里选「全局前置代理」时走这里；常见填法：本机已有代理客户端，如 127.0.0.1:10808"
            placement="top"
            :show-after="400"
          >
            <span class="cp-tip-icon">?</span>
          </el-tooltip>
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
        <div class="cp-moved">
          允许局域网、起始端口、空闲自动断开已移到
          <a @click="ui.bottomPanelView = 'network'">底部栏 → 网络设置</a>
        </div>
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
            <el-input v-model="form.realTestUrl" placeholder="https://www.google.com/generate_204" />
          </el-form-item>
          <el-form-item label="取样次数">
            <el-input v-model="form.realTestAttempts" type="number" style="width: 120px" />
            <el-tooltip content="取多次里的最小值：第 2 次起复用连接，量到的是隧道建好之后的往返；设为 1 只测冷连接" placement="top" :show-after="400"><span class="cp-tip-icon">?</span></el-tooltip>
          </el-form-item>
          <el-form-item label="测速地址">
            <el-input v-model="form.speedTestUrl" placeholder="https://speed.cloudflare.com/__down?bytes=104857600" />
          </el-form-item>
          <el-form-item label="测速时长">
            <el-input v-model="form.speedTestSeconds" type="number" style="width: 120px">
              <template #append>秒</template>
            </el-input>
            <el-tooltip content="超时即主动断开，避免把流量白跑掉" placement="top" :show-after="400"><span class="cp-tip-icon">?</span></el-tooltip>
          </el-form-item>
        </el-form>
        <el-tooltip content="真连接延迟：走完整链路请求一个极小的 204 页面，最接近实际体验。TCP 延迟：只做一次 TCP 握手、不经代理，用来快速筛掉不可达节点" placement="top" :show-after="400"><span class="cp-tip-icon">?</span></el-tooltip>
      </el-tab-pane>

      <!-- ------------------------------------------------------------ -->
      <el-tab-pane label="订阅" name="subscribe">
        <div class="cp-section">机场订阅</div>
        <el-form class="cp-form" label-width="140px">
          <el-form-item label="下载 User-Agent">
            <el-input v-model="form.subscriptionUserAgent" placeholder="clash.meta/mihomo-1.19.32" />
            <el-tooltip
              content="机场按它决定给你什么格式、甚至给哪些节点：同一家机场，clash 系 UA 拿到的是含 hysteria2 的超集，v2ray 系 UA 只给一部分；UA 里含 clash 还是拿到流量/到期响应头的前提。导入后发现节点明显偏少时，换成别家客户端在用的 UA（例如 clash-verge/v1.6.0）通常就正常了"
              placement="top"
              :show-after="400"
            ><span class="cp-tip-icon">?</span></el-tooltip>
          </el-form-item>
        </el-form>
        <div class="cp-hint" style="margin-top: 6px">
          导入、更新、分组与流量到期都在节点列表里：组头右键菜单。每条订阅还可以单独覆盖 UA。
        </div>
      </el-tab-pane>

      <!-- ------------------------------------------------------------ -->
      <el-tab-pane label="运行与界面" name="appearance">
        <div class="cp-section">运行</div>
        <el-form class="cp-form" label-width="140px">
          <el-form-item label="启动时自动激活">
            <el-switch v-model="form.autoActivateOnLaunch" />
            
          </el-form-item>
          <!-- 「代理内核」开关已删除：sing-box 摘除后只剩 mihomo 一个内核，
               没有第二个可选，留着只会让人以为还能切（理由见
               docs/MIHOMO_MIGRATION.md 的结项报告）。 -->
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
      <el-tab-pane label="快捷键" name="shortcut">
        <div class="cp-section">全局快捷键</div>
        <div class="cp-hint" style="margin-bottom: 12px">在任何程序里都有效，不用先切回本程序</div>

        <div class="cp-shortcut-row" v-for="sc in shortcutRows" :key="sc.key">
          <el-tooltip :content="sc.hint" placement="top" :show-after="400">
            <span class="cp-shortcut-label">{{ sc.label }}</span>
          </el-tooltip>
          <div
            class="cp-shortcut-input"
            :class="{ recording: recording === sc.key }"
            tabindex="0"
            @click="startRecord(sc.key, $event)"
            @keydown="onRecordKey"
          >
            <template v-if="recording === sc.key">请点击键盘两个以上组合键进行记录…</template>
            <template v-else-if="form[sc.key] === 'none'">未启用（点击可重新设置）</template>
            <template v-else>{{ form[sc.key] || '（未设置）' }}</template>
          </div>
          <el-button
            v-if="form[sc.key] && form[sc.key] !== 'none'"
            link
            size="small"
            @click="clearShortcut(sc.key)"
          >
            清除
          </el-button>
        </div>

        <div class="cp-hint" style="margin-top: 12px">
          <code>Ctrl+Alt+A</code> 被微信 / QQ 截图占用，故默认用 <code>Ctrl+Alt+Shift+A</code>
        </div>
      </el-tab-pane>

      <!-- ------------------------------------------------------------ -->
      <el-tab-pane label="关于" name="about">
        <div class="cp-section">PicoHop</div>
        <el-descriptions :column="1" border size="small">
          <el-descriptions-item label="版本">{{ meta.version || 'dev' }}</el-descriptions-item>
          <el-descriptions-item label="内嵌引擎">
            <span class="cp-ok-text">
              mihomo（唯一内核），支持 VMess、VLESS+Reality、Shadowsocks、Trojan、Hysteria2
            </span>
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

<style scoped>
/* 快捷键录制行：标签 / 录制框 / 清除 / 说明 */
.cp-shortcut-row {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 10px;
}
.cp-shortcut-label {
  width: 168px;
  flex: 0 0 auto;
  color: var(--cp-text-2);
  font-size: 13px;
}
.cp-shortcut-input {
  width: 220px;
  flex: 0 0 auto;
  height: 32px;
  line-height: 30px;
  padding: 0 10px;
  border: 1px solid var(--cp-border-strong);
  border-radius: var(--cp-radius-sm);
  background: var(--cp-panel-2);
  font-family: Consolas, monospace;
  font-size: 12.5px;
  color: var(--cp-text);
  cursor: pointer;
  outline: none;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
.cp-shortcut-input:hover {
  border-color: var(--cp-brand);
}
.cp-shortcut-input:focus,
.cp-shortcut-input.recording {
  border-color: var(--cp-brand);
  box-shadow: 0 0 0 2px var(--cp-brand-soft);
  color: var(--cp-brand);
}
</style>