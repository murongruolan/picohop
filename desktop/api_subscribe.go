package main

import (
	"context"
	"errors"
	"time"

	"github.com/murongruolan/picohop/internal/app"
	"github.com/murongruolan/picohop/internal/config"
)

// 订阅相关的前后端绑定。
//
// 【为什么下载类接口要带超时】这些调用会真的去网络上抓订阅（默认 20 秒超时），
// 界面是同步等的；这里再包一层总时限，保证最坏情况下也会返回一个明确错误，
// 而不是让界面一直转圈。
const subscriptionCallTimeout = 60 * time.Second

// errNoApp 与项目其它 API 的文案保持一致。
var errNoApp = errors.New("应用尚未就绪")

func (a *API) subscriptionCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), subscriptionCallTimeout)
}

// PreviewSubscription 抓一次并解析，返回"会导入什么"，**不写配置**。
func (a *API) PreviewSubscription(req app.ImportSubscriptionRequest) (app.SubscriptionPreview, error) {
	if a.app == nil {
		return app.SubscriptionPreview{}, errNoApp
	}
	ctx, cancel := a.subscriptionCtx()
	defer cancel()
	return a.app.PreviewSubscription(ctx, req)
}

// ImportSubscription 导入一条订阅：建分组 + 把节点以"未分配端口"的状态加进来。
func (a *API) ImportSubscription(req app.ImportSubscriptionRequest) (app.SubscriptionReport, error) {
	if a.app == nil {
		return app.SubscriptionReport{}, errNoApp
	}
	ctx, cancel := a.subscriptionCtx()
	defer cancel()
	return a.app.ImportSubscription(ctx, req)
}

// RefreshSubscription 更新一条订阅（增量合并：新增/刷新/标记下架）。
func (a *API) RefreshSubscription(id string) (app.SubscriptionReport, error) {
	if a.app == nil {
		return app.SubscriptionReport{}, errNoApp
	}
	ctx, cancel := a.subscriptionCtx()
	defer cancel()
	return a.app.RefreshSubscription(ctx, id)
}

// SaveSubscriptionMeta 改订阅的名字/UA/更新间隔/开关（不动节点）。
func (a *API) SaveSubscriptionMeta(sub config.Subscription) (config.Subscription, error) {
	if a.app == nil {
		return config.Subscription{}, errNoApp
	}
	return a.app.SaveSubscriptionMeta(sub)
}

// DeleteSubscription 删除订阅；alsoNodes 为真时连它的节点一起删。
func (a *API) DeleteSubscription(id string, alsoNodes bool) error {
	if a.app == nil {
		return errNoApp
	}
	return a.app.DeleteSubscription(id, alsoNodes)
}

// ClearMissingNodes 清理某条订阅里"机场已移除"的节点，返回清理数量。
func (a *API) ClearMissingNodes(id string) (int, error) {
	if a.app == nil {
		return 0, errNoApp
	}
	return a.app.ClearMissingNodes(id)
}

// EnableSubscriptionNode 给订阅节点分配本地端口并启动（界面在端口列上点「未分配」时用）。
func (a *API) EnableSubscriptionNode(id string) (config.Node, error) {
	if a.app == nil {
		return config.Node{}, errNoApp
	}
	return a.app.EnableSubscriptionNode(id)
}

// GetSubscriptions 返回全部订阅（含节点计数），界面画分组头用它。
func (a *API) GetSubscriptions() []app.SubscriptionsView {
	if a.app == nil {
		return nil
	}
	return a.app.Subscriptions()
}
