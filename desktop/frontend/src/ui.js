/**
 * 界面状态（对话框、右键菜单、忙碌标记）。
 *
 * 单独一个 store，是为了让"业务动作"（actions.js）能直接打开对话框，
 * 不必把一堆 props/emit 一层层传下去。
 */
import { defineStore } from 'pinia'

export const useUi = defineStore('cp-ui', {
  state: () => ({
    /**
     * 当前窗口是不是"浮动对话框窗口"（由 App.vue 的 enterFloatMode 置位）。
     *
     * 它决定对话框**由谁来拖**：窗口内浮层靠 Element Plus 的 draggable
     * （移动对话框卡片），浮动窗口里则靠整个 OS 窗口移动。
     * 两者绝不能同时开启 —— 同时开会出现"卡片跟着鼠标跑、松开还黏住"，
     * 因为两套位移叠加在一起。详见 NodeDialog 里 :draggable 的注释。
     */
    floatMode: false,
  // 由外部（自测入口）指定底部栏要展开到哪个视图 / 日志页签；
  // 组件内部状态没法从外面驱动，所以走"意向"再让组件消费一次。
  bottomLogTab: '',
    /**
     * 底部栏要展开到哪个视图（外部意向，幂等：见 BottomPanel 的 show()）。
     *
     * 必须在这里声明成初始 state：以前它是"用到时才被赋上"的，
     * 靠 Proxy 也能触发 watcher，但没人看得出一共有哪些界面意向。
     */
    bottomPanelView: '',
    /**
     * devDriven 表示"当前这一轮是自测入口在驱动界面"（启动参数 -open <面板名>）。
     *
     * 【为什么要有这个标记】自测为了截图会去点/切各种界面元素，其中底部栏的
     * 展开与页签是**用户的持久偏好** —— 让脚手架把它写进配置，等于验证行为本身
     * 擦掉了用户状态（实测：-open globaltag 会把 bottomPanelOpen 写成 false）。
     * 所以带这个标记时，界面照常变，但不落盘。
     */
    devDriven: false,
    /** 添加/编辑节点对话框 */
    nodeDialog: { open: false, mode: 'add', node: null, fromImport: false },
    /**
     * 「添加/编辑节点」对话框关闭后的回调。
     *
     * 浮动窗口模式用它把结果回传给 Go（见 App.vue 的 enterFloatMode）。
     * 放在 store 顶层而不是塞进 nodeDialog：openNodeDialog 会整个替换
     * nodeDialog 对象，挂在里面会被冲掉。
     */
    onNodeDialogClosed: null,
    /** 只有端口的小弹窗（导入后） */
    portDialog: { open: false, pending: [], source: '' },
    /** 分享对话框 */
    shareDialog: { open: false, result: null, nodeId: '', loading: false },
    /** 设置对话框打开时停在哪一页（仅自测用，正常为空） */
    settingsInitialTab: '',
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
    closeNodeDialog(result) {
      this.nodeDialog.open = false
      // 关闭回调放在 store 顶层而不是 nodeDialog 里：openNodeDialog 会整个替换
      // nodeDialog 对象，挂在里面会被冲掉。浮动窗口模式靠它把结果回传给 Go。
      const cb = this.onNodeDialogClosed
      if (cb) cb(result)
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
