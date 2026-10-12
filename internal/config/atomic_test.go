package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestWriteAtomicConcurrent 锁死"并发保存不会把配置写坏"。
//
// 背景（用户审查 #3）：原先 Save 固定用 path+".tmp" 作临时文件，
// 两个并发保存会互相截断同一个临时文件，随后 rename 把**损坏的 JSON**
// 发布成正式配置 —— 最坏情况是节点全丢。
//
// 判据：N 个 goroutine 并发写同一个 path，各自内容不同；
// 结束后正式文件必须是**其中某一次的完整内容**（能解析、且与某个 payload 完全相等），
// 并且目录里不残留 .tmp 文件。
func TestWriteAtomicConcurrent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	const n = 16
	payloads := make([][]byte, n)
	for i := 0; i < n; i++ {
		// 每个 payload 都是合法 JSON，且内容互不相同
		b, err := json.Marshal(map[string]any{
			"version": 1,
			"marker":  strings.Repeat("x", 100+i),
			"index":   i,
		})
		if err != nil {
			t.Fatal(err)
		}
		payloads[i] = append(b, '\n')
	}

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = WriteAtomic(path, payloads[i]) // 竞态下的失败可以容忍，文件不能被写坏
		}(i)
	}
	wg.Wait()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("正式配置读不到：%v", err)
	}
	if !json.Valid(got) {
		t.Fatalf("并发保存后配置不是合法 JSON（被截断/撕裂）：%q", string(got))
	}
	matched := false
	for _, p := range payloads {
		if string(p) == string(got) {
			matched = true
			break
		}
	}
	if !matched {
		t.Fatalf("正式文件内容既不是合法 JSON 的完整某次写入：%q", string(got))
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Fatalf("不该残留临时文件：%s", e.Name())
		}
	}
}
