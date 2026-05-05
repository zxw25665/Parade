package app

import "parade/internal/network"

// NetworkEngine 定义了网络层的行为契约
type NetworkEngine interface {
	Start(port int) error
	Stop() error
	BroadcastTeam(payload []byte) error                       // 局域网广播群聊/信令
	UnicastPrivate(targetPubKey string, payload []byte) error // 定点私聊
	Peers() []map[string]string                               // 返回已发现节点的公钥和IP
	StartDownload(targetPubKey, virtualPath, localSavePath string) error
	ConnectToPeer(ipAddress string) (*network.PeerConnectResult, error) // 三阶段直连测试
	OnForeground() // 前台恢复时刷新发现和连接状态
}

// FileEngine 定义了文件层的行为契约
type FileEngine interface {
	GetVirtualTree(rootPath string) (interface{}, error)
	ShareDirectory(absPath string) error
	UnshareDirectory(absPath string) error
	GetDirectoryChildren(absPath string) (interface{}, error)
}

// Frontend 定义了后端主动向前端推送消息的接口
type Frontend interface {
	Notify(eventName string, data interface{})
}
