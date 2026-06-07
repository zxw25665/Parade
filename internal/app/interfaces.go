package app

import (
	"parade/internal/network"
	pb "parade/internal/network/pb"
)

// NetworkEngine 定义了网络层的行为契约
type NetworkEngine interface {
	Start(port int) error
	Stop() error
	BroadcastTeam(payload []byte) error
	UnicastPrivate(targetPubKey string, payload []byte) error
	Peers() []map[string]string
	StartDownload(targetPubKey, virtualPath, localSavePath string) error
	ConnectToPeer(ipAddress string) (*network.PeerConnectResult, error)
	BrowseRemoteDirectory(targetPubKey, path string) ([]*pb.BrowseEntry, error)
	OnForeground()
	SendConvSyncRequest(targetPubKey, convID, sinceHLC string) error
	SendConvSyncResponse(targetPubKey, convID string, messagesJSON []byte) error
	SavePeers() error
	PeersWithStatus() []network.PeerStatus
}

// FileEngine 定义了文件层的行为契约
type FileEngine interface {
	GetVirtualTree(rootPath string) (interface{}, error)
	ShareDirectory(absPath string) error
	UnshareDirectory(absPath string) error
	GetDirectoryChildren(absPath string) (interface{}, error)
	GetSharedRoots() []string
}

// Frontend 定义了后端主动向前端推送消息的接口
type Frontend interface {
	Notify(eventName string, data interface{})
}
