package engine

import "chainproxy/internal/proxy"

// init 把 sing-box 出口的构造器注册给 proxy 包。
//
// 和 wgproxy 用同一套套路：proxy 定义接口，实现方在 init 里注册。
// 这样 proxy 包本身不需要 import sing-box（编译更快、依赖也更干净），
// 而主程序只要 import 本包就能获得这五种协议的能力。
func init() {
	proxy.RegisterSingBoxFactory(New)
}
