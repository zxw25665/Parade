package db

import "time"

// Message 对应消息表（聊天记录）
type Message struct {
	ID             string    `json:"id"`
	HLC            string    `json:"hlc"`
	SenderID       string    `json:"sender_id"`
	ReceiverID     string    `json:"receiver_id"`
	TeamID         string    `json:"team_id"`
	ChannelID      string    `json:"channel_id"`
	ConversationID string    `json:"conversation_id"`
	Content        []byte    `json:"content"`
	Type           int       `json:"type"`
	CreatedAt      time.Time `json:"created_at"`
}

// ReceiverIDGroupChat 是群聊消息的接收者标识（空字符串）
const ReceiverIDGroupChat = ""

// FileLog 对应文件传输对账表（断点续传）
type FileLog struct {
	TaskID      string    `json:"task_id"`      // 任务ID (文件 Hash 或路径 Hash)
	FilePath    string    `json:"file_path"`    // 虚拟文件路径
	PeerID      string    `json:"peer_id"`      // 传输对端ID
	Direction   int       `json:"direction"`    // 传输方向：0=上传，1=下载
	TotalSize   int64     `json:"total_size"`   // 文件总大小
	Transferred int64     `json:"transferred"`  // 已传输的字节数 (Offset 偏移量)
	Status      int       `json:"status"`       // 状态：0=传输中, 1=已完成, 2=已暂停, 3=中断出错
	UpdatedAt   time.Time `json:"updated_at"`   // 最后活跃时间
}

// Team 对应团队表
type Team struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	TeamHash  string    `json:"team_hash"`
	CreatedAt time.Time `json:"created_at"`
}

// SharedDirectory 对应共享目录表
type SharedDirectory struct {
	ID        string    `json:"id"`
	Path      string    `json:"path"`
	TeamID    string    `json:"team_id"`
	CreatedAt time.Time `json:"created_at"`
}

// ShareGroup represents a named group of shared directories
type ShareGroup struct {
	ID        string    `json:"id"`
	TeamID    string    `json:"team_id"`
	Name      string    `json:"name"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

// ShareGroupDir represents a directory belonging to a share group
type ShareGroupDir struct {
	GroupID string    `json:"group_id"`
	DirPath string    `json:"dir_path"`
	AddedAt time.Time `json:"added_at"`
}

// Conversation represents a chat conversation entity.
// ID is deterministically derived (DeriveTeamConvID/DerivePrivateConvID) — same secret/pair → same ID across devices.
type Conversation struct {
	ID          string    `json:"id"`
	TeamID      string    `json:"team_id"`
	Type        string    `json:"type"` // "team" or "private"
	DisplayName string    `json:"display_name"`
	PeerPubkey  string    `json:"peer_pubkey"`
	MyPubkey    string    `json:"my_pubkey"`
	LastHLC     string    `json:"last_hlc"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ConversationView extends Conversation with the last message preview.
type ConversationView struct {
	Conversation
	LastMessage string    `json:"last_message"`
	LastMsgTime time.Time `json:"last_msg_time"`
}
