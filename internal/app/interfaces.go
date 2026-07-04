package app

import (
	"parade/internal/core/db"
	"parade/internal/core/sync"
	"parade/internal/network"
)

// NetworkEngine 定义了网络层的行为契约
type NetworkEngine interface {
	Start(port int) error
	Stop() error
	BroadcastTeam(payload []byte) error
	UnicastPrivate(targetUUID string, payload []byte) error
	Peers() []map[string]string
	StartDownload(targetUUID, virtualPath, localSavePath string) error
	ConnectToPeer(ipAddress string) (*network.PeerConnectResult, error)
	BrowseRemoteDirectory(targetUUID, path string) ([]*network.BrowseEntry, error)
	OnForeground()
	SendConvSyncRequest(targetUUID, convID, sinceHLC string) error
	SendConvSyncResponse(targetUUID, convID string, messagesJSON []byte) error
	SendMerkleRootRequest(targetUUID, convID string) ([]byte, error)
	SendBucketCompareRequest(targetUUID, convID string, level int, paths []string) ([]sync.BucketInfo, error)
	SendFetchMessagesRequest(targetUUID, convID, bucketPath, sinceHLC string) ([]*db.Message, error)
	SendPushMessages(targetUUID, convID string, messages []*db.Message) error
	SetMerkleSyncHandler(handler sync.MerkleSyncHandler)
	SavePeers() error
	PeersWithStatus() []network.PeerStatus
	// ResolveUUID resolves a Parade UUID to the Curve25519 pubkey for crypto operations.
	ResolveUUID(uuid string) (string, error)
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
