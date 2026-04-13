package eventbus

// ---- Topic 定义 (主题字典) ----
const (
	TopicPeerJoined     = "network:peer_joined"     // 节点加入局域网
	TopicPeerLeft       = "network:peer_left"       // 节点离开局域网
	TopicMsgReceived    = "network:msg_received"    // 收到网络消息/信令
	TopicFileProgress   = "file:progress"           // 文件传输进度更新
	TopicFileCompleted  = "file:completed"          // 文件传输完成
	TopicDirChanged     = "fs:dir_changed"          // 监控的本地目录发生变动
)

// ---- Payload 结构定义 (载荷字典) ----

// PeerEventPayload 对应节点加入/离开事件
type PeerEventPayload struct {
	PubKeyBase64 string
	IPAddress    string
}

// MsgReceivedPayload 对应收到消息事件 (直接复用 DB 的模型，此处简化处理)
type MsgReceivedPayload struct {
	HLC      string
	SenderID string
	Content[]byte // 解密后的明文
	Type     int
}

// FileProgressPayload 对应文件进度事件
type FileProgressPayload struct {
	TaskID      string
	FilePath    string
	Transferred int64
	TotalSize   int64
	IsUpload    bool
}
