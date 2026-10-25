package main

// 节点本地端口的操作绑定。
//
// 单独一个文件而不是塞进 api.go：这一组只有"把端口置于未分配"这一件事，
// 放一起以后加同类操作（批量改端口等）也好找。

// UnassignNodePorts 把所选节点的本地端口置于未分配（会先停止激活中的节点）。
func (a *API) UnassignNodePorts(ids []string) (int, error) {
	if a.app == nil {
		return 0, errNoApp
	}
	return a.app.UnassignNodePorts(ids)
}
