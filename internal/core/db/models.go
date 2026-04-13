package db

import "time"

// Message 对应消息表（聊天记录）
type Message struct {
	ID         string    `json:"id"`          // 唯一消息ID (UUID)
	HLC        string    `json:"hlc"`         // 混合逻辑时钟，格式如 "2026-04-13T15:58:00.000Z_001_NodeA" (可进行字典序绝对排序)
	SenderID   string    `json:"sender_id"`   // 发送者公钥Hash
	ReceiverID string    `json:"receiver_id"` // 接收者公钥Hash (若为空表示局域网群聊)
	Content[]byte    `json:"content"`     // 加密后的载荷 (落盘加密)
	Type       int       `json:"type"`        // 消息类型：0=纯文本, 1=系统通知, 2=文件元数据
	CreatedAt  time.Time `json:"created_at"`  // 本地物理入库时间
}

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
