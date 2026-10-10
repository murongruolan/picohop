package app

import (
	"context"
	"fmt"
	"net"

	"chainproxy/internal/config"
	"chainproxy/internal/proxy"
	"chainproxy/internal/tester"
)

// TestFront 直接经前置代理访问公网 IP 查询服务，返回前置的出口 IP。
//
// 无论链路是否处于运行状态都可以测试——它只用配置，不占用本地端口。
func (a *App) TestFront(ctx context.Context) tester.Result {
	a.mu.RLock()
	fc := a.cfg.Front
	url := a.cfg.TestURL
	a.mu.RUnlock()

	if fc.Type == config.FrontTypeDirect {
		return tester.Result{Via: "直连", Err: fmt.Errorf("前置代理类型为「直连」，请先配置主流客户端的 socks5 前置")}
	}
	front := proxy.NewFront(fc)
	defer front.Close()
	return tester.FetchIP(ctx, func(c context.Context, network, addr string) (net.Conn, error) {
		return front.DialTCP(c, addr)
	}, url, "前置代理 "+front.Describe())
}

// TestUpstream 经指定住宅代理链路测试出口 IP。
// 链路未运行时也可以测试（只创建临时 Front，不占用本地端口）。
func (a *App) TestUpstream(ctx context.Context, id string) tester.Result {
	u, ok := a.GetUpstream(id)
	if !ok {
		return tester.Result{Err: fmt.Errorf("未找到该住宅代理")}
	}
	a.mu.RLock()
	url := a.cfg.TestURL
	a.mu.RUnlock()

	up, err := proxy.NewUpstream(u)
	if err != nil {
		return tester.Result{Err: err}
	}
	front := proxy.NewFront(a.GetFront())
	defer front.Close()

	if err := up.Start(ctx, front); err != nil {
		return tester.Result{Via: u.Name, Err: err}
	}
	defer up.Stop()

	return tester.FetchIP(ctx, func(c context.Context, network, addr string) (net.Conn, error) {
		conn, derr := up.Dial(c, front, addr)
		if derr != nil {
			return nil, fmt.Errorf("经「%s」连接 %s 失败: %w", u.Name, addr, derr)
		}
		return conn, nil
	}, url, u.Name+" · "+u.Describe())
}
