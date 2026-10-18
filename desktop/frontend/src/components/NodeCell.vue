<script setup>
/**
 * 单个单元格的渲染。
 *
 * 单独抽出来，是为了让表格组件只管"摆位置"，而"每列显示成什么样"
 * 集中在这里 —— 否则一大串 v-if 会把表格模板淹没。
 */
import { computed } from 'vue'
import { Loading } from '@element-plus/icons-vue'
import {
  formatBytes,
  formatMs,
  formatSpeed,
  formatWhen,
  formatFullTime,
} from '../columns'
import { useStore } from '../store'
import api, { notify } from '../api'

const props = defineProps({
  row: { type: Object, required: true },
  col: { type: String, required: true },
})

const store = useStore()

/** 这个节点正在被测试（且是对应这一列的那种测试）。 */
function testingFor(kind) {
  return store.testing && store.testKind === kind && store.testingIds.includes(props.row.id)
}

const stateClass = computed(() => {
  if (props.row.state === 'running') return 'is-run'
  if (props.row.state === 'error') return 'is-err'
  return 'is-idle'
})

const stateLabel = computed(() => {
  if (props.row.state === 'running') return '已激活'
  if (props.row.state === 'error') return '错误'
  return props.row.active ? '待启动' : '未激活'
})

/** 延迟分档着色：<300 绿 / <800 橙 / 其余红，一眼看出快慢。 */
// 「延迟」列显示最后一次测试的结果：测真连接就显示真连接、测 TCP 就显示 TCP。
// 用后端给的 latencyKind 判断哪次在后（每次测试都会更新它）。
function latestLatency(r) {
  if (!r) return 0
  return r.latencyKind === 'real' ? r.realMs || 0 : r.latencyMs || 0
}

/**
 * "上次成功值（已过期）"的提示语。
 *
 * 【为什么要专门说一句】测试失败时后端**不再**把旧数值写成 0（审查 #19），
 * 于是这个数字是"以前测出来的"而不是"这次测出来的"。不解释的话，用户会以为
 * 它是刚刚的结果 —— 那还不如显示空。所以变灰 + 悬停说明来源时间。
 */
function staleNote(what, at) {
  const when = at ? `（${formatWhen(at)}）` : ''
  return `最近一次${what}没有测出数值，这里是上次成功的结果${when}`
}

function latencyClass(v) {
  if (!v || v <= 0) return 'cp-dim'
  if (v < 300) return 'cp-lat-good'
  if (v < 800) return 'cp-lat-mid'
  return 'cp-lat-bad'
}

async function copyPort() {
  try {
    await api.copyText(`127.0.0.1:${props.row.listenPort}`)
    notify('success', '已复制本地代理地址', `127.0.0.1:${props.row.listenPort}`)
  } catch (e) {
    notify('error', '复制失败', String(e?.message || e))
  }
}
</script>

<template>
  <!-- 状态 -->
  <template v-if="col === 'state'">
    <span class="cp-dot" :class="stateClass" />
    <span :class="{ 'cp-dim': row.state !== 'running' }">{{ stateLabel }}</span>
  </template>

  <!-- 类型 -->
  <template v-else-if="col === 'protocol'">
    <span class="cp-tag is-brand">{{ row.protocolLabel }}</span>
  </template>

  <!-- 别名：前面带状态点，这里也承担"测试中"的指示 -->
  <template v-else-if="col === 'name'">
    <span class="cp-name">
      <span class="cp-dot" :class="stateClass" />
      <span :title="row.name">{{ row.name }}</span>
      <!-- 全局节点标记：列表里只能有一个，红底白字（用户要求，和主流客户端一样） -->
      <span v-if="store.settings?.globalNodeId === row.id" class="cp-tag-global">全局</span>
      <el-icon v-if="testingFor('tcp') || testingFor('real')" class="cp-spin" style="margin-left: 6px">
        <Loading />
      </el-icon>
    </span>
  </template>

  <!-- 本地端口：点一下就复制地址 -->
  <template v-else-if="col === 'listenPort'">
    <!--
      订阅节点默认不占端口（一次导入可能上千个，不可能一人一个），这时显示
      「未分配」而不是会误导人的 0；点它就是"启用" —— 后端 App.Activate 会先分配
      一个空闲端口再启动（见那里的注释）。
    -->
    <el-tooltip
      v-if="row.portPending"
      content="这个订阅节点还没分配本地端口，点击启用"
      placement="top"
      :show-after="400"
    >
      <span class="cp-linklike cp-unassigned" @click.stop="store.enableSubscriptionNode(row)">
        未分配
      </span>
    </el-tooltip>
    <el-tooltip v-else content="点击复制 127.0.0.1:端口" placement="top" :show-after="500">
      <span class="cp-mono cp-num cp-linklike" @click.stop="copyPort()">
        {{ row.listenPort }}
      </span>
    </el-tooltip>
  </template>

  <template v-else-if="col === 'server'">
    <span class="cp-mono cp-ellipsis" :title="row.server">{{ row.server }}</span>
  </template>

  <template v-else-if="col === 'port'">
    <span class="cp-mono cp-num">{{ row.port }}</span>
  </template>

  <template v-else-if="col === 'transport'">
    <span class="cp-tag">{{ row.transport }}</span>
  </template>

  <!-- 延迟 -->
  <!-- 三态：没测过（—）/ 有效（正常着色）/ 上次成功值（变灰 + 悬停说明来源时间） -->
  <template v-else-if="col === 'latency'">
    <el-icon v-if="testingFor('tcp') || testingFor('real')" class="cp-spin"><Loading /></el-icon>
    <el-tooltip
      v-else-if="latestLatency(row)"
      :disabled="!row.latencyFailed"
      :content="staleNote('延迟测试', row.latencyAt)"
      placement="top"
      :show-after="300"
    >
      <span
        class="cp-num"
        :class="row.latencyFailed ? 'cp-stale' : latencyClass(latestLatency(row))"
      >
        {{ formatMs(latestLatency(row)) }}
      </span>
    </el-tooltip>
    <!-- 测过但没测出数值、且从来没成功过：是"失败"，不是"没测过" -->
    <span v-else-if="row.latencyFailed" class="cp-lat-mid" title="最近一次延迟测试失败">
      失败
    </span>
    <span v-else class="cp-dim">—</span>
  </template>

  <template v-else-if="col === 'real'">
    <el-icon v-if="testingFor('real')" class="cp-spin"><Loading /></el-icon>
    <el-tooltip
      v-else-if="row.realMs"
      :disabled="!row.latencyFailed"
      :content="staleNote('延迟测试', row.latencyAt)"
      placement="top"
      :show-after="300"
    >
      <span class="cp-num" :class="row.latencyFailed ? 'cp-stale' : latencyClass(row.realMs)">
        {{ formatMs(row.realMs) }}
      </span>
    </el-tooltip>
    <span v-else class="cp-dim">—</span>
  </template>

  <template v-else-if="col === 'speed'">
    <el-icon v-if="testingFor('speed')" class="cp-spin"><Loading /></el-icon>
    <el-tooltip
      v-else-if="row.speedMbps"
      :disabled="!row.speedFailed"
      :content="staleNote('测速', row.speedAt)"
      placement="top"
      :show-after="300"
    >
      <span class="cp-num" :class="row.speedFailed ? 'cp-stale' : 'cp-lat-good'">
        {{ formatSpeed(row.speedMbps) }}
      </span>
    </el-tooltip>
    <span v-else-if="row.speedFailed" class="cp-lat-mid" title="最近一次测速失败">失败</span>
    <span v-else class="cp-dim">—</span>
  </template>

  <template v-else-if="col === 'exitIp'">
    <el-icon v-if="testingFor('exit')" class="cp-spin"><Loading /></el-icon>
    <span v-else-if="row.exitIp" class="cp-mono">{{ row.exitIp }}</span>
    <span v-else class="cp-dim">—</span>
  </template>

  <template v-else-if="col === 'today'">
    <el-tooltip
      v-if="row.todayTotal"
      :content="`上传 ${formatBytes(row.todayUp)} · 下载 ${formatBytes(row.todayDown)}`"
      placement="top"
      :show-after="300"
    >
      <span class="cp-num">{{ formatBytes(row.todayTotal) }}</span>
    </el-tooltip>
    <span v-else class="cp-dim">0 KB</span>
  </template>

  <template v-else-if="col === 'lastUsed'">
    <el-tooltip v-if="row.lastUsed" :content="formatFullTime(row.lastUsed)" placement="top" :show-after="300">
      <span class="cp-dim cp-num">{{ formatWhen(row.lastUsed) }}</span>
    </el-tooltip>
    <span v-else class="cp-dim">从未使用</span>
  </template>

  <template v-else-if="col === 'createdAt'">
    <span class="cp-dim cp-num">{{ formatFullTime(row.createdAt) }}</span>
  </template>

  <template v-else-if="col === 'remark'">
    <span v-if="row.remark" class="cp-ellipsis" :title="row.remark">{{ row.remark }}</span>
    <span v-else class="cp-dim">—</span>
  </template>

  <template v-else-if="col === 'front'">
    <span class="cp-ellipsis cp-dim" style="font-size: 12px" :title="row.frontLabel">
      {{ row.frontLabel }}
    </span>
  </template>

  <template v-else-if="col === 'tls'">
    <span v-if="row.tlsEnabled" class="cp-lat-good">TLS</span>
    <span v-else class="cp-dim">—</span>
  </template>

  <template v-else-if="col === 'activeConn'">
    <span class="cp-num" :class="{ 'cp-dim': !row.activeConn }">{{ row.activeConn || 0 }}</span>
  </template>

  <template v-else-if="col === 'totalConn'">
    <span class="cp-num cp-dim">{{ row.totalConn || 0 }}</span>
  </template>

  <template v-else>
    <span class="cp-dim">—</span>
  </template>
</template>

<style scoped>
.cp-linklike {
  cursor: pointer;
  border-bottom: 1px dashed transparent;
}
.cp-linklike:hover {
  color: var(--cp-brand);
  border-bottom-color: var(--cp-brand);
}
/* 「未分配」：订阅节点还没启用时的状态 —— 既不是错误也不是空值。
   压暗 + 虚线下划线表示"可以点"，免得被当成真实的端口号。 */
.cp-unassigned {
  color: var(--cp-text-3);
  cursor: pointer;
  border-bottom: 1px dashed var(--cp-text-3);
}
.cp-unassigned:hover {
  color: var(--cp-brand);
  border-bottom-color: var(--cp-brand);
}
.cp-lat-good {
  color: var(--cp-success);
}
.cp-lat-mid {
  color: var(--cp-warning);
}
.cp-lat-bad {
  color: var(--cp-danger);
}
/* "上次成功值"：变灰表示它不是这次测出来的（悬停有来源时间），
   但底下留一条虚线下划线，免得被当成"没有数据"。 */
.cp-stale {
  color: var(--cp-text-3);
  border-bottom: 1px dashed var(--cp-text-3);
  cursor: help;
}
.cp-spin {
  animation: cp-rotate 1s linear infinite;
  color: var(--cp-brand);
}
@keyframes cp-rotate {
  to {
    transform: rotate(360deg);
  }
}
</style>
