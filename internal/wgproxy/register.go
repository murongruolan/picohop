package wgproxy

import "chainproxy/internal/proxy"

// init 把 WireGuard 上游的构造器注册给 proxy 包。
//
// 用注册函数而不是让 proxy 直接 import wgproxy，是为了避免
// proxy ↔ wgproxy 的循环依赖：proxy 定义接口，wgproxy 实现并注册。
func init() {
	proxy.RegisterWireGuardFactory(New)
}
