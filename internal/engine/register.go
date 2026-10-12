package engine

import "github.com/murongruolan/picohop/internal/proxy"

// init 把 mihomo 出口的构造器注册给 proxy 包。
//
// 和 wgproxy 用同一套套路：proxy 定义接口，实现方在 init 里注册。
// 这样 proxy 包本身不需要 import mihomo（编译更快、依赖方向也保持单向），
// 而主程序只要 import 本包就能获得 vmess/vless/ss/trojan/hysteria2 五种出口。
//
// 【只有一个内核】这里曾经还注册过 sing-box 作回退，随 sing-box 一起删掉了：
// 回退会掩盖"新内核接不了某个节点"，而那种掩盖的代价是节点静默变不可用、
// 甚至（如果退化成直连）本机 IP 暴露 —— 现在宁可让这个节点失败并说清原因。
func init() {
	proxy.RegisterMihomoFactory(NewMihomo)
}
