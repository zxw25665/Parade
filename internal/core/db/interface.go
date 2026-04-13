package db

import "context"

// Database 是数据持久化层的核心接口，逻辑层只能通过此接口操作数据
type Database interface {
	// 生命周期管理
	Close() error
	
	// 事务执行器（用于对账时高并发拉取大批量数据入库，极大提升性能）
	RunInTx(ctx context.Context, fn func(tx DBTx) error) error

	// ---- 消息模块 ----
	// 插入单条消息
	InsertMessage(ctx context.Context, msg *Message) error
	// 获取自某个 HLC 时钟之后的聊天记录（用于断网重连后的对账同步）
	GetMessagesSinceHLC(ctx context.Context, hlc string, limit int) ([]*Message, error)
	// 获取最新的历史消息（UI 面板展示用）
	GetRecentMessages(ctx context.Context, limit int, offset int) ([]*Message, error)

	// ---- 文件传输状态模块 ----
	// 更新或插入文件传输进度（断点续传核心）
	UpsertFileLog(ctx context.Context, log *FileLog) error
	// 获取指定文件的传输记录
	GetFileLog(ctx context.Context, taskID string) (*FileLog, error)
}

// DBTx 暴露了在事务中可用的操作
type DBTx interface {
	InsertMessageTx(ctx context.Context, msg *Message) error
	UpsertFileLogTx(ctx context.Context, log *FileLog) error
}
