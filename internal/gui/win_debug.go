//go:build windows

package gui

import (
	"os"
	"runtime"
	"time"
)

// dumpStacksOnDemand 是排障看门狗：
// 设置 CHAINPROXY_STACK_DUMP=<文件路径> 后，每 3 秒把所有 goroutine 的栈写进去。
// 界面"卡住不动"时，用它可以一眼看出是哪个 goroutine 被阻塞在哪一行。
func dumpStacksOnDemand() {
	path := os.Getenv("CHAINPROXY_STACK_DUMP")
	if path == "" {
		return
	}
	go func() {
		for {
			time.Sleep(3 * time.Second)
			buf := make([]byte, 1<<21)
			n := runtime.Stack(buf, true)
			_ = os.WriteFile(path, buf[:n], 0o644)
		}
	}()
}
