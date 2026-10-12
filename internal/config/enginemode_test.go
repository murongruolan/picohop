package config

import (
	"encoding/json"
	"testing"
)

// TestEngineModeJSON 锁死 EngineMode 的序列化名字与默认值。
//
// 默认是空串（等价 sing-box）：老配置文件里没有这个字段时，
// 读出来必须是空的 —— 否则换装开关会"自动打开"，那是灾难性的默认值。
func TestEngineModeJSON(t *testing.T) {
	var s Settings
	if err := json.Unmarshal([]byte(`{"portStart":10810}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.EngineMode != "" {
		t.Fatalf("老配置没写 engineMode 时应当是空串（=sing-box），实际 %q", s.EngineMode)
	}

	b, err := json.Marshal(Settings{EngineMode: "mihomo"})
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(b) || !contains(string(b), `"engineMode":"mihomo"`) {
		t.Fatalf("序列化名字应当是 engineMode，实际 %s", string(b))
	}
}

func contains(hay, needle string) bool {
	return len(hay) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(hay); i++ {
			if hay[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
