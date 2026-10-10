/**
 * 界面状态（对话框、右键菜单、忙碌标记）。
 *
 * 单独一个 store，是为了让"业务动作"（actions.js）能直接打开对话框，
 * 不必把一堆 props/emit 一层层传下去。
 */
import { defineStore } from 'pinia'

export const useUi = defineStore('cp-ui', {
  state: () => ({
    /** 添加/编辑节点对话框 */
    nodeDialog: { open: false, mode: 'add', node: null, fromImport: false },
    /** 只有端口的小弹窗（导入后） */
    portDialog: { open: false, pending: [], source: '' },
    /** 分享对话框 */
    shareDialog: { open: false, result: null, nodeId: '', loading: false },
    /** 设置对话框 */
    settingsDialog: false,
    /** 导入对话框 */
    importDialog: false,
    /** 日志抽屉 */
    logDrawer: false,
    /** 右键菜单 */
    contextMenu: { open: false, x: 0, y: 0, node: null },
    /** 正在进行的耗时操作（用于按钮 loading） */
    busy: '',
  }),

  actions: {
    openNodeDialog(mode, node = null) {
      this.nodeDialog = { open: true, mode, node, fromImport: false }
    },
    openNodeDialogFromImport(node) {
      this.nodeDialog = { open: true, mode: 'add', node, fromImport: true }
    },
    closeNodeDialog() {
      this.nodeDialog.open = false
    },

    openPortDialog(pending, source) {
      this.portDialog = { open: true, pending, source }
    },
    closePortDialog() {
      this.portDialog.open = false
    },

    openShare(nodeId) {
      this.shareDialog = { open: true, result: null, nodeId, loading: true }
    },
    closeShare() {
      this.shareDialog.open = false
    },

    openContextMenu(x, y, node) {
      this.contextMenu = { open: true, x, y, node }
    },
    closeContextMenu() {
      this.contextMenu.open = false
    },

    notifyBusy(what) {
      this.busy = what
    },
  },
})
