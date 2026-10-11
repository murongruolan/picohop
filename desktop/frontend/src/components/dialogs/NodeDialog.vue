<script setup>
/**
 * 添加 / 编辑节点。
 *
 * 布局上有一条刻意的顺序：**本地代理端口放在最前面**（紧跟别名）。
 * 原因是绝大多数节点的协议参数一次填好就再也不动了，
 * 而端口是天天要看的 —— 用户改端口时不该先滚过一整屏协议配置。
 */
import { computed, reactive, ref, watch } from 'vue'
import { CopyDocument, Key, MagicStick, Refresh } from '@element-plus/icons-vue'
import { useStore } from '../../store'
import { useUi } from '../../ui'
import { useActions } from '../../actions'
import api, { notify } from '../../api'

const store = useStore()
const ui = useUi()
const actions = useActions()

const dlg = computed(() => ui.nodeDialog)
const isEdit = computed(() => dlg.value.mode === 'edit')
const meta = computed(() => store.meta || {})

const visible = computed({
  get: () => dlg.value.open,
  set: (v) => {
    if (!v) ui.closeNodeDialog()
  },
})

/** 空表单。字段与 config.Node 一一对应。 */
function blankForm() {
  return {
    id: '',
    name: '',
    protocol: 'socks5',
    remark: '',
    listenHost: '127.0.0.1',
    listenPort: 0,
    listenUsername: '',
    listenPassword: '',
    front: '',
    server: '',
    port: 0,
    username: '',
    password: '',
    uuid: '',
    alterId: 0,
    security: 'auto',
    flow: '',
    method: 'aes-128-gcm',
    upMbps: 0,
    downMbps: 0,
    obfs: '',
    obfsPassword: '',
    pinSHA256: '',
    transport: { network: 'tcp', path: '', host: '', serviceName: '', headerType: '' },
    tls: {
      enabled: false,
      serverName: '',
      alpn: [],
      allowInsecure: false,
      fingerprint: '',
      realityPublicKey: '',
      realityShortId: '',
      realitySpiderX: '',
    },
    wireguard: {
      privateKey: '',
      address: '',
      peerPublicKey: '',
      presharedKey: '',
      allowedIPs: '0.0.0.0/0, ::/0',
      persistentKeepalive: 25,
      mtu: 1420,
      dnsServers: [],
    },
  }
}

const form = reactive(blankForm())
const advanced = ref([])

const portStatus = ref(null)
const portError = ref('')
const saving = ref(false)

/** 当前协议的元信息，决定显示哪些字段。 */
const pinfo = computed(
  () => (meta.value.protocols || []).find((p) => p.value === form.protocol) || {},
)

const protocolOptions = computed(() =>
  (meta.value.protocols || []).map((p) => ({ value: p.value, label: p.label })),
)

/**
 * 可选的前置。
 *
 * 排除"自己"，也排除"前置链会绕回自己"的节点 —— 否则会配出一个环路，
 * 激活时无限递归。
 *
 * 注意这里用 'direct' 这个哨兵值表示直连，而不是空字符串：
 * el-select 会把空字符串当成"没选"，于是永远显示占位符"请选择"，
 * 用户根本看不出当前是直连。
 */
const DIRECT = 'direct'

const frontOptions = computed(() => {
  const list = [
    { value: DIRECT, label: '直连（不使用前置）' },
    { value: 'global', label: `全局前置代理 · ${store.summary?.frontDesc || '未配置'}` },
  ]
  for (const n of store.nodes) {
    if (n.id === form.id) continue
    if (wouldCycle(n.id)) continue
    list.push({ value: n.id, label: `节点「${n.name}」 · 127.0.0.1:${n.listenPort}` })
  }
  return list
})

/** 界面上用 'direct'，配置里存空字符串。 */
const frontChoice = computed({
  get: () => (form.front ? form.front : DIRECT),
  set: (v) => {
    form.front = v === DIRECT ? '' : v
  },
})

/** 把 candidate 当前置，是否会绕回正在编辑的这个节点。 */
function wouldCycle(candidateId) {
  if (!form.id) return false
  const byId = new Map(store.nodes.map((n) => [n.id, n]))
  let cur = byId.get(candidateId)
  const seen = new Set()
  while (cur) {
    if (cur.id === form.id) return true
    if (seen.has(cur.id)) return true
    seen.add(cur.id)
    const fid = cur.front && cur.front !== 'global' ? cur.front : ''
    cur = fid ? byId.get(fid) : null
  }
  return false
}

// ---------------------------------------------------------------------------
// 打开时初始化
// ---------------------------------------------------------------------------
watch(visible, async (v) => {
  if (!v) return
  Object.assign(form, blankForm())
  portStatus.value = null
  portError.value = ''
  advanced.value = []

  const src = dlg.value.node
  if (src) {
    // 深拷贝，避免直接改到 store 里的对象
    const copy = JSON.parse(JSON.stringify(src))
    Object.assign(form, copy)
    // 补齐可能缺失的嵌套对象
    form.transport = Object.assign(blankForm().transport, copy.transport || {})
    form.tls = Object.assign(blankForm().tls, copy.tls || {})
    form.tls.alpn = copy.tls?.alpn || []
    form.wireguard = Object.assign(blankForm().wireguard, copy.wireguard || {})
  }
  if (!form.transport.network) form.transport.network = 'tcp'

  // 新增时给一个默认端口
  if (!isEdit.value || !form.listenPort) {
    try {
      form.listenPort = await api.suggestPort(form.id)
      await checkPort()
    } catch {
      form.listenPort = store.meta?.defaultPortStart || 10810
    }
  }
})

// 切换协议时补默认值（例如把 TLS 默认打开），不去覆盖用户已经填的内容
watch(
  () => form.protocol,
  (p) => {
    const info = (meta.value.protocols || []).find((x) => x.value === p)
    if (!info) return
    if (info.tlsRequired || (info.tlsDefault && !form.tls.enabled && !form.server)) {
      form.tls.enabled = true
    }
    if (!form.port) form.port = info.defaultPort || 0
    if (!form.transport.network) form.transport.network = 'tcp'
  },
)

// ---------------------------------------------------------------------------
// 端口
// ---------------------------------------------------------------------------
async function checkPort() {
  portError.value = ''
  const p = Number(form.listenPort)
  if (!p) {
    portStatus.value = null
    portError.value = '请填写本地代理端口'
    return
  }
  try {
    const st = await api.checkPort(p, form.id)
    portStatus.value = st
    if (!st.free) portError.value = st.message || '端口不可用'
  } catch (e) {
    portStatus.value = null
    portError.value = String(e?.message || e)
  }
}

/**
 * 随机端口。
 *
 * 后端会"随机 → 检测占用 → 占用就重随机"，最多 5 次；
 * 抽不到空闲端口时返回那句固定的提示文案。
 * 前端只在确认空闲之后才覆盖输入框。
 */
async function randomPort() {
  portError.value = ''
  try {
    const p = await api.randomPort(form.id)
    form.listenPort = p
    await checkPort()
  } catch (e) {
    const msg = String(e?.message || e)
    portError.value = msg
    notify('warning', '随机端口失败', msg)
  }
}

// ---------------------------------------------------------------------------
// WireGuard 密钥
// ---------------------------------------------------------------------------
async function genKeys() {
  try {
    const k = await api.genWireGuardKey()
    form.wireguard.privateKey = k.privateKey || ''
    form.wireguard.peerPublicKey = form.wireguard.peerPublicKey || ''
    await api.copyText(k.publicKey || '')
    notify(
      'success',
      '已生成密钥对',
      `本端公钥已复制到剪贴板，交给服务端登记：${k.publicKey}`,
    )
  } catch (e) {
    notify('error', '生成失败', String(e?.message || e))
  }
}

async function copyText(t, label) {
  try {
    await api.copyText(t || '')
    notify('success', `已复制${label}`, '')
  } catch (e) {
    notify('error', '复制失败', String(e?.message || e))
  }
}

// ---------------------------------------------------------------------------
// 保存
// ---------------------------------------------------------------------------
async function save() {
  // 轻量前端校验，真正的校验以后端为准（那里有完整的协议规则）
  if (!form.name.trim()) {
    notify('warning', '请填写别名', '别名用于在列表里区分节点')
    return
  }
  if (!form.server.trim()) {
    notify('warning', '请填写服务器地址', '')
    return
  }
  if (!(Number(form.port) >= 1 && Number(form.port) <= 65535)) {
    notify('warning', '远程端口不合法', '应在 1-65535 之间')
    return
  }
  if (!(Number(form.listenPort) >= 1024 && Number(form.listenPort) <= 65535)) {
    notify('warning', '本地端口不合法', '建议使用 1024 以上、且未被占用的端口')
    return
  }
  const st = portStatus.value
  if (st && !st.free) {
    notify('warning', '本地端口被占用', st.message || '')
    return
  }

  const payload = JSON.parse(JSON.stringify(form))
  payload.listenPort = Number(payload.listenPort)
  payload.port = Number(payload.port)
  payload.alterId = Number(payload.alterId) || 0
  payload.upMbps = Number(payload.upMbps) || 0
  payload.downMbps = Number(payload.downMbps) || 0
  payload.wireguard.persistentKeepalive = Number(payload.wireguard.persistentKeepalive) || 0
  payload.wireguard.mtu = Number(payload.wireguard.mtu) || 0
  if (payload.protocol !== 'wireguard') payload.wireguard = null

  saving.value = true
  try {
    if (isEdit.value) await api.updateNode(payload)
    else await api.addNode(payload)
    // 把结果一并交出去：浮动窗口模式下要靠它告诉 Go"用户确认了"
    ui.closeNodeDialog({ submitted: true, edit: isEdit.value, name: payload.name })
  } catch (e) {
    notify('error', isEdit.value ? '保存失败' : '添加失败', String(e?.message || e))
  } finally {
    saving.value = false
  }
}


const alpnPresets = ['http/1.1', 'h2', 'h3']
const obfsOptions = [
  { value: '', label: '不使用混淆' },
  { value: 'salamander', label: 'salamander' },
  { value: 'gecko', label: 'gecko' },
]
const flowOptions = [
  { value: '', label: '不使用（推荐）' },
  { value: 'xtls-rprx-vision', label: 'xtls-rprx-vision' },
]
</script>

<template>
  <el-dialog
    v-model="visible"
    class="cp-dialog"
    :title="isEdit ? '编辑节点' : '添加节点'"
    width="760px"
    top="4vh"
    draggable
    :close-on-click-modal="false"
    append-to-body
  >
    <el-form class="cp-form" label-width="104px" label-position="right" @submit.prevent>
      <div class="cp-section">基本信息</div>

      <el-row :gutter="14">
        <el-col :span="12">
          <el-form-item label="别名" required>
            <el-input v-model="form.name" placeholder="给这个节点起个名字" maxlength="60" />
          </el-form-item>
        </el-col>
        <el-col :span="12">
          <el-form-item label="类型" required>
            <el-select v-model="form.protocol" style="width: 100%">
              <el-option
                v-for="p in protocolOptions"
                :key="p.value"
                :label="p.label"
                :value="p.value"
              />
            </el-select>
          </el-form-item>
        </el-col>
      </el-row>

      <!-- 端口：刻意排在协议参数之前 -->
      <el-form-item label="本地代理端口" required>
        <div class="cp-port-row">
          <el-input
            v-model="form.listenPort"
            style="width: 178px"
            type="number"
            placeholder="10810"
            @blur="checkPort"
          >
            <template #prepend>127.0.0.1:</template>
          </el-input>
          <el-button :icon="MagicStick" @click="randomPort">随机</el-button>
          <span v-if="portError" class="cp-error-text">{{ portError }}</span>
          <span v-else-if="portStatus && portStatus.free" class="cp-ok-text">端口可用</span>
        </div>
      </el-form-item>

      <el-form-item label="前置节点">
        <el-select v-model="frontChoice" style="width: 100%" filterable>
          <el-option
            v-for="o in frontOptions"
            :key="o.value"
            :label="o.label"
            :value="o.value"
          />
        </el-select>
      </el-form-item>
      <div class="cp-hint" style="margin: -8px 0 14px 104px">
        不选就是直连；选「全局前置代理」走设置里的前置；选某个节点则经它的本地端口转发
        （会形成一个链路：前置节点 → 本节点）。
      </div>

      <div class="cp-section">服务器</div>
      <el-row :gutter="14">
        <el-col :span="16">
          <el-form-item :label="form.protocol === 'wireguard' ? '服务端地址' : '服务器地址'" required>
            <el-input v-model="form.server" placeholder="域名或 IP" />
          </el-form-item>
        </el-col>
        <el-col :span="8">
          <el-form-item :label="form.protocol === 'wireguard' ? 'Endpoint 端口' : '远程端口'" required>
            <!--
              用 :model-value 而不是 v-model：远程端口为 0 时要显示占位符而不是"0"，
              否则用户会以为已经填好了。
            -->
            <el-input
              :model-value="form.port === 0 ? '' : form.port"
              type="number"
              :placeholder="String(pinfo.defaultPort || 443)"
              @update:model-value="(v) => (form.port = v === '' || v === null ? 0 : Number(v))"
            />
          </el-form-item>
        </el-col>
      </el-row>

      <!-- ------------------------------------------------------------ -->
      <template v-if="pinfo.showUsername">
        <el-row :gutter="14">
          <el-col :span="12">
            <el-form-item label="用户名">
              <el-input v-model="form.username" placeholder="可选" />
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item label="密码">
              <el-input v-model="form.password" type="password" show-password placeholder="可选" />
            </el-form-item>
          </el-col>
        </el-row>
      </template>

      <!-- VMess / VLESS -->
      <template v-if="pinfo.showUuid || pinfo.showFlow">
        <el-form-item v-if="pinfo.showUuid" label="UUID" required>
          <el-input v-model="form.uuid" placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx">
            <template #append>
              <el-button :icon="CopyDocument" @click="copyText(form.uuid, 'UUID')" />
            </template>
          </el-input>
        </el-form-item>
        <el-row :gutter="14">
          <el-col v-if="pinfo.showAlterId" :span="12">
            <el-form-item label="AlterId">
              <el-input v-model="form.alterId" type="number" placeholder="0" />
            </el-form-item>
          </el-col>
          <el-col v-if="pinfo.showSecurity" :span="12">
            <el-form-item label="加密方式">
              <el-select v-model="form.security" style="width: 100%">
                <el-option
                  v-for="s in meta.vmessSecurities || []"
                  :key="s"
                  :label="s"
                  :value="s"
                />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col v-if="pinfo.showFlow" :span="12">
            <el-form-item label="流控 Flow">
              <el-select v-model="form.flow" style="width: 100%">
                <el-option v-for="f in flowOptions" :key="f.value" :label="f.label" :value="f.value" />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>
      </template>

      <!-- Shadowsocks -->
      <el-row v-if="pinfo.showMethod" :gutter="14">
        <el-col :span="12">
          <el-form-item label="加密方法" required>
            <el-select v-model="form.method" style="width: 100%" filterable allow-create>
              <el-option
                v-for="m in meta.shadowsocksMethods || []"
                :key="m"
                :label="m"
                :value="m"
              />
            </el-select>
          </el-form-item>
        </el-col>
        <el-col :span="12">
          <el-form-item label="密码" required>
            <el-input v-model="form.password" type="password" show-password />
          </el-form-item>
        </el-col>
      </el-row>

      <!-- Trojan / Hysteria2 密码（socks5/http 的密码已经在上面那一行里了） -->
      <el-form-item
        v-if="!pinfo.showMethod && !pinfo.showUsername && pinfo.showPassword"
        label="密码"
        required
      >
        <el-input v-model="form.password" type="password" show-password />
      </el-form-item>

      <!-- ------------------------------------------------------------ -->
      <template v-if="pinfo.showTransport">
        <div class="cp-section">传输层</div>
        <el-row :gutter="14">
          <el-col :span="8">
            <el-form-item label="网络">
              <el-select v-model="form.transport.network" style="width: 100%">
                <el-option
                  v-for="n in meta.transportNetworks || []"
                  :key="n"
                  :label="n"
                  :value="n"
                />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col v-if="form.transport.network === 'grpc'" :span="16">
            <el-form-item label="服务名">
              <el-input v-model="form.transport.serviceName" placeholder="gRPC serviceName" />
            </el-form-item>
          </el-col>
          <template v-else>
            <el-col :span="8">
              <el-form-item label="路径">
                <el-input v-model="form.transport.path" placeholder="/path" />
              </el-form-item>
            </el-col>
            <el-col :span="8">
              <el-form-item label="Host">
                <el-input v-model="form.transport.host" placeholder="伪装域名" />
              </el-form-item>
            </el-col>
          </template>
        </el-row>
      </template>

      <!-- ------------------------------------------------------------ -->
      <template v-if="pinfo.showTls">
        <div class="cp-section">TLS</div>
        <el-row :gutter="14">
          <el-col :span="8">
            <el-form-item label="启用 TLS">
              <el-switch v-model="form.tls.enabled" :disabled="pinfo.tlsRequired" />
            </el-form-item>
          </el-col>
          <el-col :span="16">
            <el-form-item label="SNI">
              <el-input v-model="form.tls.serverName" placeholder="留空则用服务器地址" />
            </el-form-item>
          </el-col>
        </el-row>
        <el-row :gutter="14">
          <el-col :span="8">
            <el-form-item label="指纹">
              <el-select v-model="form.tls.fingerprint" style="width: 100%" clearable placeholder="不使用">
                <el-option
                  v-for="f in meta.utlsFingerprints || []"
                  :key="f"
                  :label="f"
                  :value="f"
                />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="10">
            <el-form-item label="ALPN">
              <el-select
                v-model="form.tls.alpn"
                style="width: 100%"
                multiple
                filterable
                allow-create
                default-first-option
                placeholder="可选"
              >
                <el-option v-for="a in alpnPresets" :key="a" :label="a" :value="a" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="6">
            <el-form-item label="跳过校验" label-width="72px">
              <el-tooltip content="允许不安全证书（自签证书时需要）" placement="top">
                <el-switch v-model="form.tls.allowInsecure" />
              </el-tooltip>
            </el-form-item>
          </el-col>
        </el-row>

        <!-- Reality -->
        <template v-if="pinfo.showReality">
          <div class="cp-section">Reality</div>
          <el-row :gutter="14">
            <el-col :span="12">
              <el-form-item label="公钥 pbk">
                <el-input v-model="form.tls.realityPublicKey" placeholder="服务端的 publicKey" />
              </el-form-item>
            </el-col>
            <el-col :span="6">
              <el-form-item label="ShortId">
                <el-input v-model="form.tls.realityShortId" placeholder="sid" />
              </el-form-item>
            </el-col>
            <el-col :span="6">
              <el-form-item label="SpiderX">
                <el-input v-model="form.tls.realitySpiderX" placeholder="仅用于导出" />
              </el-form-item>
            </el-col>
          </el-row>
          <div class="cp-hint" style="margin: -8px 0 14px 104px">
            填了公钥即为 Reality：此时必须启用 TLS，并建议把「指纹」设成 chrome。
          </div>
        </template>
      </template>

      <!-- ------------------------------------------------------------ -->
      <template v-if="pinfo.showHysteria2">
        <div class="cp-section">Hysteria2</div>
        <el-row :gutter="14">
          <el-col :span="8">
            <el-form-item label="上行 Mbps">
              <el-input v-model="form.upMbps" type="number" placeholder="0 = 不限" />
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="下行 Mbps">
              <el-input v-model="form.downMbps" type="number" placeholder="0 = 不限" />
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="混淆">
              <el-select v-model="form.obfs" style="width: 100%">
                <el-option v-for="o in obfsOptions" :key="o.value" :label="o.label" :value="o.value" />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>
        <el-row :gutter="14">
          <el-col :span="12">
            <el-form-item label="混淆密码">
              <el-input
                v-model="form.obfsPassword"
                type="password"
                show-password
                :disabled="!form.obfs"
              />
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item label="PinSHA256">
              <el-input v-model="form.pinSHA256" placeholder="证书公钥固定（可选）" />
            </el-form-item>
          </el-col>
        </el-row>
      </template>

      <!-- ------------------------------------------------------------ -->
      <template v-if="pinfo.showWireGuard">
        <div class="cp-section">
          WireGuard
          <el-button link type="primary" size="small" :icon="Key" @click="genKeys">
            生成密钥对
          </el-button>
        </div>
        <el-form-item label="本端私钥" required>
          <el-input v-model="form.wireguard.privateKey" placeholder="base64 私钥">
            <template #append>
              <el-button :icon="CopyDocument" @click="copyText(form.wireguard.privateKey, '私钥')" />
            </template>
          </el-input>
        </el-form-item>
        <el-row :gutter="14">
          <el-col :span="12">
            <el-form-item label="对端公钥" required>
              <el-input v-model="form.wireguard.peerPublicKey" placeholder="服务端公钥" />
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item label="预共享密钥">
              <el-input v-model="form.wireguard.presharedKey" placeholder="可选" />
            </el-form-item>
          </el-col>
        </el-row>
        <el-row :gutter="14">
          <el-col :span="12">
            <el-form-item label="隧道地址" required>
              <el-input v-model="form.wireguard.address" placeholder="10.7.0.2/32" />
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item label="AllowedIPs">
              <el-input v-model="form.wireguard.allowedIPs" placeholder="0.0.0.0/0, ::/0" />
            </el-form-item>
          </el-col>
        </el-row>
        <el-row :gutter="14">
          <el-col :span="8">
            <el-form-item label="Keepalive">
              <el-input v-model="form.wireguard.persistentKeepalive" type="number" placeholder="25" />
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="MTU">
              <el-input v-model="form.wireguard.mtu" type="number" placeholder="1420" />
            </el-form-item>
          </el-col>
        </el-row>
        <div class="cp-hint" style="margin: -8px 0 14px 104px">
          本程序用纯用户态实现 WireGuard：不改系统路由表、不需要管理员权限。
          若「前置节点」是 SOCKS5，外层 UDP 会经它的 UDP ASSOCIATE 送出。
        </div>
      </template>

      <!-- ------------------------------------------------------------ -->
      <div class="cp-section">其它</div>
      <el-form-item label="备注">
        <el-input v-model="form.remark" placeholder="可选，方便以后认出来" maxlength="200" />
      </el-form-item>

      <el-collapse v-model="advanced">
        <el-collapse-item name="adv" title="高级：本地端口鉴权">
          <el-row :gutter="14">
            <el-col :span="12">
              <el-form-item label="用户名">
                <el-input v-model="form.listenUsername" placeholder="默认为空" />
              </el-form-item>
            </el-col>
            <el-col :span="12">
              <el-form-item label="密码">
                <el-input v-model="form.listenPassword" placeholder="默认为空" />
              </el-form-item>
            </el-col>
          </el-row>
          <div class="cp-hint" style="margin-left: 104px">
            本地端口默认只监听 127.0.0.1，不需要鉴权。
            只有在设置里打开了「允许局域网连接」时才建议填写，否则局域网内任何人都能用。
          </div>
        </el-collapse-item>
      </el-collapse>
    </el-form>

    <template #footer>
      <el-button type="warning" @click="ui.closeNodeDialog()">取消</el-button>
      <el-button type="primary" :loading="saving" @click="save">
        {{ isEdit ? '保存' : '添加' }}
      </el-button>
    </template>
  </el-dialog>
</template>


