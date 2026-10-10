package config

import (
	"crypto/rand"
	"time"
)

// randRead 抽取出来是为了让 NewID 保持无依赖、好测试。
func randRead(p []byte) (int, error) { return rand.Read(p) }

func nowNano() int64 { return time.Now().UnixNano() }

func nowTime() time.Time { return time.Now() }
