<script setup>
/**
 * 通知的展示层。
 *
 * 固定在右上角、顶栏下方；多条纵向堆叠；z-index 高于所有对话框。
 * 样式见 styles.css 的 .cp-notify-* 一节。
 */
import { CircleCheck, CircleClose, InfoFilled, Notification, Warning } from '@element-plus/icons-vue'
import { notifications, dismiss } from '../notify'

const icons = {
  primary: Notification,
  success: CircleCheck,
  warning: Warning,
  info: InfoFilled,
  error: CircleClose,
}
</script>

<template>
  <Teleport to="body">
    <div class="cp-notify-stack">
      <TransitionGroup name="cp-notify">
        <div
          v-for="n in notifications"
          :key="n.id"
          class="cp-notify"
          :class="`cp-notify-${n.level}`"
        >
          <el-icon class="cp-notify-icon"><component :is="icons[n.level]" /></el-icon>
          <div class="cp-notify-body">
            <div v-if="n.title" class="cp-notify-title">{{ n.title }}</div>
            <div v-if="n.body" class="cp-notify-text">{{ n.body }}</div>
          </div>
          <button class="cp-notify-close" title="关闭" @click="dismiss(n.id)">×</button>
        </div>
      </TransitionGroup>
    </div>
  </Teleport>
</template>

<style scoped>
.cp-notify-stack {
  position: fixed;
  /* 顶栏 52px + 一点间距：产品要求的"右上角控制栏下面的位置" */
  top: 60px;
  right: 14px;
  /* 比对话框（Element Plus 默认 2000 档）更高，保证通知永远看得见 */
  z-index: 3200;
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: 340px;
  pointer-events: none;
}
.cp-notify {
  display: flex;
  align-items: flex-start;
  gap: 9px;
  padding: 11px 12px;
  background: var(--cp-panel);
  border: 1px solid var(--cp-border);
  border-left-width: 3px;
  border-radius: var(--cp-radius);
  box-shadow: var(--cp-shadow);
  pointer-events: auto;
}
.cp-notify-icon {
  margin-top: 1px;
  font-size: 15px;
  flex: 0 0 auto;
}
.cp-notify-body {
  flex: 1 1 auto;
  min-width: 0;
}
.cp-notify-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--cp-text);
  line-height: 1.4;
}
.cp-notify-text {
  margin-top: 2px;
  font-size: 12.5px;
  color: var(--cp-text-2);
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-word;
}
.cp-notify-close {
  flex: 0 0 auto;
  width: 18px;
  height: 18px;
  padding: 0;
  border: none;
  background: transparent;
  color: var(--cp-text-3);
  font-size: 15px;
  line-height: 1;
  cursor: pointer;
  border-radius: 4px;
}
.cp-notify-close:hover {
  background: var(--cp-panel-2);
  color: var(--cp-text);
}

/* 5 种类型的配色 */
.cp-notify-primary {
  border-left-color: var(--cp-brand);
}
.cp-notify-primary .cp-notify-icon,
.cp-notify-primary .cp-notify-title {
  color: var(--cp-brand);
}
.cp-notify-success {
  border-left-color: var(--cp-success);
}
.cp-notify-success .cp-notify-icon,
.cp-notify-success .cp-notify-title {
  color: var(--cp-success);
}
.cp-notify-warning {
  border-left-color: var(--cp-warning);
}
.cp-notify-warning .cp-notify-icon,
.cp-notify-warning .cp-notify-title {
  color: var(--cp-warning);
}
.cp-notify-info {
  border-left-color: var(--cp-info);
}
.cp-notify-info .cp-notify-icon,
.cp-notify-info .cp-notify-title {
  color: var(--cp-info);
}
.cp-notify-error {
  border-left-color: var(--cp-danger);
}
.cp-notify-error .cp-notify-icon,
.cp-notify-error .cp-notify-title {
  color: var(--cp-danger);
}

/* 进出场：从右侧滑入，避免"啪一下出现"的突兀感 */
.cp-notify-enter-active,
.cp-notify-leave-active {
  transition: all 0.22s cubic-bezier(0.22, 1, 0.36, 1);
}
.cp-notify-enter-from,
.cp-notify-leave-to {
  opacity: 0;
  transform: translateX(16px);
}
.cp-notify-leave-active {
  position: absolute;
  right: 14px;
  width: 340px;
}
</style>
