package proxy

import (
	"fmt"
	"io"
)

// readFull 是 io.ReadFull 的短别名，方便测试里少写一次错误处理。
func readFull(r io.Reader, p []byte) (int, error) { return io.ReadFull(r, p) }

// errf 是 fmt.Errorf 的短别名。
func errf(format string, args ...any) error { return fmt.Errorf(format, args...) }