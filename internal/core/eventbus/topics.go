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
	TopicLogEvent          = "system:log_event"
	TopicPeerOnline        = "network:peer_online"
	TopicPeerOffline       = "network:peer_offline"
	TopicConvSyncRequest   = "network:conv_sync_request"
)

// ---- Payload 结构定义 (载荷字典) ----

// PeerEventPayload 对应节点加入/离开事件
type PeerEventPayload struct {
	PubKeyBase64 string
	IPAddress    string
}

// MsgReceivedPayload 对应收到消息事件
type MsgReceivedPayload struct {
	HLC            string
	SenderID       string
	Content        []byte
	Type           int
	ReceiverID     string
	TeamID         string
	ChannelID      string
	ConversationID string
}

// FileProgressPayload 对应文件进度事件
type FileProgressPayload struct {
	TaskID      string
	FilePath    string
	Transferred int64
	TotalSize   int64
	IsUpload    bool
}

// ConversationSyncPayload carries a per-conversation sync request/response.
type ConversationSyncPayload struct {
	RequesterPubKey string
	ConversationID  string
	SinceHLC        string
	Messages        []byte // JSON-serialized []*db.Message for sync response
}
