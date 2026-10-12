// Package ports 负责本地代理端口的分配、随机与占用检测。
//
// 这里集中了界面上那三条和端口有关的规则：
//   - 默认端口：从 10810 起排，取"本项目内所有 10800+ 端口"的最大值 + 1
//   - 随机端口：从大于 10000 的合法端口里随机，随机到之后必须**检测已占用才覆盖**，
//     占用就重随机，最多 5 次
//   - 手工输入：失焦时检测，占用则红字提示
package ports

import (
	"fmt"
	"math/rand"
	"net"
	"sort"
	"strconv"
	"time"
)

const (
	// DefaultStart 是默认起始端口。
	DefaultStart = 10810
	// BaselineFloor 是"参与取最大值"的下界。
	//
	// 规则是"取所有 10800+ 端口的最大值+1"：低于 10800 的端口
	// （比如 1080、8080）不参与，否则一个 8080 就会把起始值拉低。
	BaselineFloor = 10800

	// RandomMin 是随机端口的下界（要求"大于 10000"）。
	RandomMin = 10001
	// RandomMax 是随机端口的上界。
	//
	// 没有取到 65535 是因为 Windows 默认的动态端口范围是 49152-65535，
	// 系统会自己从这段里发临时端口，随机到那段等于在跟操作系统抢端口，
	// 命中率低、并且偶尔会撞上刚被回收的 TIME_WAIT 端口。
	// 10001-49151 仍有近 4 万个可选端口，完全够用。
	RandomMax = 49151

	// MaxRandomRetries 是"随机到空闲端口"的最大重试次数。
	// 超出后界面显示"多次随机仍未随机到空闲端口"。
	MaxRandomRetries = 5

	// MaxScanAhead 是"顺延找空闲端口"时的最大探测次数，避免极端情况下死循环。
	MaxScanAhead = 512

	// NoPort 是"没找到空闲端口"的返回值（审查 #21）。
	//
	// Next() / AllocateFor 只返回 int，没有 error 出口；用 0 当哨兵是因为
	// 0 本来就不是合法端口（Check 直接判非法），调用方一眼就能判出来。
	// 【别改成"返回最后一个候选"】那个候选的 Check 结果恰恰是"不空闲"，
	// 或者已经越过 65535 —— 调用方会把它当成功结果写进节点配置，
	// 用户看到的是"节点启动失败：端口绑定失败"，且坏端口已经落盘。
	NoPort = 0
)

// Status 是一次端口检测的完整结果。
//
// 界面直接拿 Message 显示红字提示，不需要自己拼文案。
type Status struct {
	Port int `json:"port"`
	// Valid 表示端口号本身合法（1-65535）。
	Valid bool `json:"valid"`
	// InProject 表示被本项目里另一个节点占用。
	InProject bool `json:"inProject"`
	// InProjectBy 是占用它的节点名（InProject 为真时有效）。
	InProjectBy string `json:"inProjectBy"`
	// Busy 表示本机上已经有别的程序在监听这个端口。
	Busy bool `json:"busy"`
	// Free 表示既不冲突也未被占用 —— 只有它为真才能放心用。
	Free bool `json:"free"`
	// Message 是要显示给用户的提示（Free 为真时为空）。
	Message string `json:"message"`
}

// Allocator 是端口分配器。
type Allocator struct {
	// Start 是默认起始端口，默认 10810。
	Start int
	// Host 是占用检测使用的本地地址。
	Host string
	// Project 记录本项目已经占用的端口 -> 占用它的节点名。
	Project map[int]string
	// RandMin/RandMax 是随机端口的范围。
	RandMin, RandMax int
	// MaxRetries 是随机重试上限。
	MaxRetries int

	rnd *rand.Rand
}

// New 构造一个分配器。
func New() *Allocator {
	return &Allocator{
		Start:      DefaultStart,
		Host:       "127.0.0.1",
		Project:    map[int]string{},
		RandMin:    RandomMin,
		RandMax:    RandomMax,
		MaxRetries: MaxRandomRetries,
		rnd:        rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// MarkProject 登记一个"本项目内已占用"的端口。
func (a *Allocator) MarkProject(port int, name string) {
	if a.Project == nil {
		a.Project = map[int]string{}
	}
	a.Project[port] = name
}

// Next 返回下一个建议使用的端口。
//
// 规则：max(所有 >= 10800 的端口, Start-1) + 1，然后顺延到第一个真正空闲的端口。
//
// 顺延不到就返回 NoPort(0) —— **只有确认空闲的端口才会被返回**。
// 【审查 #21】老实现在循环结束后 `return cand`，有两个坏出口：
//   - 占用表里已经有 65535（或 Start 就是 65535）时 cand = 65536，
//     循环条件一次都不成立，直接把 65536 返回出去；
//   - 连扫 MaxScanAhead 次都还占着时，返回的是最后一个**没验证过**的候选，
//     它可能正被本机别的程序占着。
//
// 这两种返回值都会被调用方当成"分配成功"写进节点配置，最终表现为
// "节点启动失败：端口绑定失败"，而且坏端口已经落盘。
// 宁可返回 0 让界面明确提示"没有可用端口"，也不要猜一个端口。
func (a *Allocator) Next() int {
	start := a.Start
	if start < 1024 {
		start = DefaultStart
	}

	max := 0
	for p := range a.Project {
		if p >= BaselineFloor && p > max {
			max = p
		}
	}

	cand := max + 1
	if cand < start {
		cand = start
	}
	for i := 0; i < MaxScanAhead && cand <= 65535; i++ {
		if a.Check(cand).Free {
			return cand
		}
		cand++
	}
	// 扫到区间尽头（cand > 65535）或扫描预算用尽：没有可用端口
	return NoPort
}

// Check 检测端口是否可用。
func (a *Allocator) Check(port int) Status {
	st := Status{Port: port}

	if port < 1 || port > 65535 {
		st.Message = fmt.Sprintf("端口 %d 非法，应在 1-65535 之间", port)
		return st
	}
	if port < 1024 {
		st.Message = fmt.Sprintf("端口 %d 属于系统保留端口（1-1023），请换一个", port)
		return st
	}
	st.Valid = true

	if name, ok := a.Project[port]; ok {
		st.InProject = true
		st.InProjectBy = name
		if name == "" {
			name = "本项目其它节点"
		}
		st.Message = fmt.Sprintf("端口 %d 已被「%s」占用", port, name)
		return st
	}

	if a.systemBusy(port) {
		st.Busy = true
		st.Message = fmt.Sprintf("端口 %d 已被本机其它程序占用", port)
		return st
	}

	st.Free = true
	return st
}

// systemBusy 通过"真的去 bind 一下"来判断端口是否被占用。
//
// 为什么不用 netstat：netstat 的输出要解析、还受权限影响；
// 直接 bind 是操作系统给出的权威答案。
//
// Windows 上 Go 不会给监听套接字设置 SO_REUSEADDR
// （setDefaultListenerSockopts 在 Windows 是空实现），
// 所以这里的 bind 确实会因端口被占而失败 —— 判断是可靠的。
func (a *Allocator) systemBusy(port int) bool {
	host := a.Host
	if host == "" {
		host = "127.0.0.1"
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return true
	}
	_ = ln.Close()
	return false
}

// RandomFree 随机一个空闲端口。
//
// 语义严格对齐界面上的要求：
//   - 随机范围是"大于 10000 的合法端口"
//   - 随机出来后**先检测占用**，只有确认空闲才返回（调用方再覆盖进输入框）
//   - 已占用就重新随机
//   - 最多重试 MaxRandomRetries(5) 次，仍失败则返回错误，
//     界面据此提示"多次随机仍未随机到空闲端口"
func (a *Allocator) RandomFree() (int, error) {
	lo, hi := a.RandMin, a.RandMax
	if lo < 1025 {
		lo = RandomMin
	}
	if hi > 65535 {
		hi = 65535
	}
	if hi < lo {
		lo, hi = RandomMin, RandomMax
	}
	span := hi - lo + 1

	retries := a.MaxRetries
	if retries <= 0 {
		retries = MaxRandomRetries
	}
	if a.rnd == nil {
		a.rnd = rand.New(rand.NewSource(time.Now().UnixNano()))
	}

	for i := 0; i < retries; i++ {
		p := lo + a.rnd.Intn(span)
		if a.Check(p).Free {
			return p, nil
		}
	}
	return 0, ErrNoFreePort
}

// ErrNoFreePort 表示多次随机都没抽到空闲端口。
var ErrNoFreePort = fmt.Errorf("多次随机仍未随机到空闲端口")

// AllocateFor 为"新增一个节点"分配默认端口，并把它登记进占用表。
//
// 没有可用端口时返回 NoPort(0)，并且**不登记任何东西**：
// 登记 0 会让 ProjectPorts/UsedPorts 里多出一个"0 号端口"的幽灵占用项。
func (a *Allocator) AllocateFor(name string) int {
	p := a.Next()
	if p == NoPort {
		return NoPort
	}
	a.MarkProject(p, name)
	return p
}

// ProjectPorts 返回当前登记的所有端口（升序），便于测试与展示。
func (a *Allocator) ProjectPorts() []int {
	out := make([]int, 0, len(a.Project))
	for p := range a.Project {
		out = append(out, p)
	}
	sort.Ints(out)
	return out
}
