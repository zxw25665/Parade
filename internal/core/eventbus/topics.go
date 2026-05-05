package eventbus

// ---- Topic 定义 (主题字典) ----
const (
	TopicPeerJoined        = "network:peer_joined"
	TopicPeerLeft          = "network:peer_left"
	TopicMsgReceived       = "network:msg_received"
	TopicPrivateMsgReceived = "network:private_msg_received"
	TopicFileProgress      = "file:progress"
	TopicFileCompleted     = "file:completed"
	TopicDirChanged        = "fs:dir_changed"
)

// ---- Payload 结构定义 (载荷字典) ----

// PeerEventPayload 对应节点加入/离开事件
type PeerEventPayload struct {
	PubKeyBase64 string
	IPAddress    string
}

// MsgReceivedPayload 对应收到消息事件 (直接复用 DB 的模型，此处简化处理)
type MsgReceivedPayload struct {
	HLC        string
	SenderID   string
	Content    []byte
	Type       int
	ReceiverID string
}

// FileProgressPayload 对应文件进度事件
type FileProgressPayload struct {
	TaskID      string
	FilePath    string
	Transferred int64
	TotalSize   int64
	IsUpload    bool
}
