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
	// 获取指定团队的最新历史消息
	GetRecentMessagesByTeam(ctx context.Context, teamID string, limit int, offset int) ([]*Message, error)
	// 获取指定频道的最新历史消息
	GetRecentMessagesByChannel(ctx context.Context, channelID string, limit int, offset int) ([]*Message, error)
	// 获取指定团队中自某个 HLC 时钟之后的消息（增量同步）
	GetMessagesSinceHLCByTeam(ctx context.Context, teamID string, hlc string, limit int) ([]*Message, error)

	// ---- 文件传输状态模块 ----
	// 更新或插入文件传输进度（断点续传核心）
	UpsertFileLog(ctx context.Context, log *FileLog) error
	// 获取指定文件的传输记录
	GetFileLog(ctx context.Context, taskID string) (*FileLog, error)

	// ---- 团队模块 ----
	InsertTeam(ctx context.Context, team *Team) error
	GetTeam(ctx context.Context, id string) (*Team, error)
	GetTeamByHash(ctx context.Context, teamHash string) (*Team, error)
	ListTeams(ctx context.Context) ([]*Team, error)
	DeleteTeam(ctx context.Context, id string) error

	// ---- 共享目录模块 ----
	InsertSharedDirectory(ctx context.Context, dir *SharedDirectory) error
	DeleteSharedDirectory(ctx context.Context, path string) error
	ListSharedDirectories(ctx context.Context) ([]*SharedDirectory, error)

	// ---- 共享组模块 ----
	CreateShareGroup(ctx context.Context, sg *ShareGroup) error
	GetShareGroup(ctx context.Context, id string) (*ShareGroup, error)
	ListShareGroupsByTeam(ctx context.Context, teamID string) ([]*ShareGroup, error)
	DeleteShareGroup(ctx context.Context, id string) error
	AddDirectoryToShareGroup(ctx context.Context, groupID, dirPath string) error
	RemoveDirectoryFromShareGroup(ctx context.Context, groupID, dirPath string) error
	ListShareGroupDirs(ctx context.Context, groupID string) ([]*ShareGroupDir, error)

	// ---- 对话模块 ----
	UpsertConversation(ctx context.Context, conv *Conversation) error
	GetConversation(ctx context.Context, id string) (*Conversation, error)
	ListConversations(ctx context.Context, teamID string) ([]*ConversationView, error)
	GetConversationMessages(ctx context.Context, convID string, limit int, offset int) ([]*Message, error)
	GetConversationMessagesSinceHLC(ctx context.Context, convID string, sinceHLC string, limit int) ([]*Message, error)
	UpdateConversationLastHLC(ctx context.Context, convID string, hlc string) error
	ListAllConversations(ctx context.Context) ([]*Conversation, error)

	UpsertMerkleNode(ctx context.Context, node *MerkleNode) error
	GetMerkleNode(ctx context.Context, convID, bucketPath string) (*MerkleNode, error)
	GetMerkleNodesByLevel(ctx context.Context, convID string, level int) ([]*MerkleNode, error)
	GetMerkleNodesByParent(ctx context.Context, convID, parentPath string) ([]*MerkleNode, error)
	DeleteMerkleNodesByParent(ctx context.Context, convID, parentPath string) error
	DeleteMerkleNodesByConv(ctx context.Context, convID string) error

	GetFrozenState(ctx context.Context, convID string) (*FreezeState, error)
	UpsertFrozenState(ctx context.Context, state *FreezeState) error

	GetMessagesInBucket(ctx context.Context, convID, bucketPath string, level int) ([]*Message, error)
}

// DBTx 暴露了在事务中可用的操作
type DBTx interface {
	InsertMessageTx(ctx context.Context, msg *Message) error
	UpsertFileLogTx(ctx context.Context, log *FileLog) error
	UpsertConversationTx(ctx context.Context, conv *Conversation) error
	UpsertMerkleNodeTx(ctx context.Context, node *MerkleNode) error
	UpsertFrozenStateTx(ctx context.Context, state *FreezeState) error
}
