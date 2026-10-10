//go:build !with_utls || !with_quic

package engine

// 这个文件在**缺少必要构建标签**时参与编译。
//
// sing-box 把 Reality(uTLS) 与 Hysteria2(QUIC) 这类特性放在构建标签后面，
// 漏掉标签的后果是编译一切正常、但运行时才报
// "uTLS, which is required by reality is not included in this build"。
// 那种报错很难联想到是编译参数的问题，所以这里提前把它变成一句明确的话。
const buildTagsOK = false

func missingBuildTags() []string { return []string{"with_utls", "with_quic"} }
