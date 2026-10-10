package wgproxy

import (
	"fmt"
	"os"
)

// dbg 是排障用的临时输出，设置 CHAINPROXY_UDP_DEBUG 才生效。
func dbg(format string, args ...any) {
	if os.Getenv("CHAINPROXY_UDP_DEBUG") == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "[socksBind] "+format+"\n", args...)
}
