//go:build with_utls && with_quic

package engine

// buildTagsOK 由构建标签决定，见 tags_missing.go。
const buildTagsOK = true

// missingBuildTags 返回缺失的构建标签（这里恒为空）。
func missingBuildTags() []string { return nil }
