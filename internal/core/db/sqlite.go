package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	// 引入纯 Go 实现的 SQLite 驱动
	_ "modernc.org/sqlite"
)

type sqliteDB struct {
	conn *sql.DB
}

// NewSQLiteDB 初始化数据库，执行高并发核心优化并建表
func NewSQLiteDB(dbPath string) (Database, error) {
	// 连接 SQLite
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	// ==========================================
	// 极致性能与并发安全配置 (PRAGMA Tuning)
	// ==========================================
	pragmas :=[]string{
		"PRAGMA journal_mode = WAL;",         // 开启预写日志：允许多个读操作和一个写操作并发，彻底解决 database is locked 痛点
		"PRAGMA synchronous = NORMAL;",       // 配合 WAL 模式，在安全性与极速之间取得完美平衡
		"PRAGMA busy_timeout = 5000;",        // 锁超时机制：若发生写冲突，自动重试等待至多 5000 毫秒，对 Go 的高频协程非常友好
		"PRAGMA temp_store = MEMORY;",        // 将临时表和索引放在内存中，加快对账等复杂查询
		"PRAGMA cache_size = -64000;",        // 启用 64MB 的页面缓存 (负数代表 KB)
		"PRAGMA foreign_keys = ON;",          // 开启外键约束（按需）
	}

	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			return nil, fmt.Errorf("failed to execute pragma %s: %w", p, err)
		}
	}

	// 配置 Go 侧连接池
	// 对于 SQLite 的 WAL 模式，设为 1 个写连接和几个读连接是最佳实践
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)

	instance := &sqliteDB{conn: db}

	// 初始化数据表架构
	if err := instance.migrate(); err != nil {
		return nil, err
	}

	return instance, nil
}

func (db *sqliteDB) Close() error {
	return db.conn.Close()
}

// migrate 创建所需的双表结构及索引
func (db *sqliteDB) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS messages (
		id TEXT PRIMARY KEY,
		hlc TEXT NOT NULL,
		sender_id TEXT NOT NULL,
		receiver_id TEXT NOT NULL DEFAULT '',
		content BLOB NOT NULL,
		type INTEGER NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	-- 按照 HLC 时钟排序是最高频操作，必须建立索引
	CREATE INDEX IF NOT EXISTS idx_messages_hlc ON messages(hlc);

	CREATE TABLE IF NOT EXISTS file_logs (
		task_id TEXT PRIMARY KEY,
		file_path TEXT NOT NULL,
		peer_id TEXT NOT NULL,
		direction INTEGER NOT NULL,
		total_size INTEGER NOT NULL,
		transferred INTEGER NOT NULL,
		status INTEGER NOT NULL,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	`
	_, err := db.conn.Exec(schema)
	if err != nil {
		return fmt.Errorf("failed to init database schema: %w", err)
	}
	return nil
}

// ---- 事务支持 ----
// RunInTx 封装了安全的事务机制。
// 为什么需要？在网络对账时如果收到了 1000 条消息，一条条 Insert 会触发 1000 次磁盘 I/O。
// 将其包裹在事务中，会打包成一次 I/O 写入磁盘，性能提升可达百倍。
func (db *sqliteDB) RunInTx(ctx context.Context, fn func(tx DBTx) error) error {
	sqlTx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	txWrapper := &dbTxWrapper{tx: sqlTx}
	if err := fn(txWrapper); err != nil {
		sqlTx.Rollback() // 发生错误立刻回滚
		return err
	}

	return sqlTx.Commit() // 成功则一并提交
}

type dbTxWrapper struct {
	tx *sql.Tx
}

// ---- 消息模块实现 ----

func (db *sqliteDB) InsertMessage(ctx context.Context, msg *Message) error {
	query := `INSERT OR IGNORE INTO messages (id, hlc, sender_id, receiver_id, content, type, created_at) 
	          VALUES (?, ?, ?, ?, ?, ?, ?)`
	_, err := db.conn.ExecContext(ctx, query,
		msg.ID, msg.HLC, msg.SenderID, msg.ReceiverID, msg.Content, msg.Type, msg.CreatedAt,
	)
	return err
}

func (db *sqliteDB) GetMessagesSinceHLC(ctx context.Context, hlc string, limit int) ([]*Message, error) {
	query := `SELECT id, hlc, sender_id, receiver_id, content, type, created_at 
	          FROM messages WHERE hlc > ? ORDER BY hlc ASC LIMIT ?`
	rows, err := db.conn.QueryContext(ctx, query, hlc, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs[]*Message
	for rows.Next() {
		msg, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, msg)
	}
	return msgs, nil
}

func (db *sqliteDB) GetRecentMessages(ctx context.Context, limit int, offset int) ([]*Message, error) {
	query := `SELECT id, hlc, sender_id, receiver_id, content, type, created_at 
	          FROM messages ORDER BY hlc DESC LIMIT ? OFFSET ?`
	rows, err := db.conn.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
// ... 接上一段 GetRecentMessages
	var msgs []*Message
	for rows.Next() {
		msg, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, msg)
	}
	return msgs, nil
}

// ---- 文件传输模块实现 ----

func (db *sqliteDB) UpsertFileLog(ctx context.Context, log *FileLog) error {
	query := `INSERT INTO file_logs (task_id, file_path, peer_id, direction, total_size, transferred, status, updated_at)
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	          ON CONFLICT(task_id) DO UPDATE SET 
	          transferred=excluded.transferred, 
	          status=excluded.status, 
	          updated_at=excluded.updated_at`
	_, err := db.conn.ExecContext(ctx, query,
		log.TaskID, log.FilePath, log.PeerID, log.Direction, log.TotalSize, log.Transferred, log.Status, log.UpdatedAt,
	)
	return err
}

func (db *sqliteDB) GetFileLog(ctx context.Context, taskID string) (*FileLog, error) {
	query := `SELECT task_id, file_path, peer_id, direction, total_size, transferred, status, updated_at 
	          FROM file_logs WHERE task_id = ?`
	row := db.conn.QueryRowContext(ctx, query, taskID)
	
	log := &FileLog{}
	err := row.Scan(&log.TaskID, &log.FilePath, &log.PeerID, &log.Direction, &log.TotalSize, &log.Transferred, &log.Status, &log.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return log, err
}

// ---- 事务包装器方法实现 ----

func (w *dbTxWrapper) InsertMessageTx(ctx context.Context, msg *Message) error {
	query := `INSERT OR IGNORE INTO messages (id, hlc, sender_id, receiver_id, content, type, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`
	_, err := w.tx.ExecContext(ctx, query, msg.ID, msg.HLC, msg.SenderID, msg.ReceiverID, msg.Content, msg.Type, msg.CreatedAt)
	return err
}

func (w *dbTxWrapper) UpsertFileLogTx(ctx context.Context, log *FileLog) error {
	query := `INSERT INTO file_logs (task_id, file_path, peer_id, direction, total_size, transferred, status, updated_at)
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	          ON CONFLICT(task_id) DO UPDATE SET transferred=excluded.transferred, status=excluded.status, updated_at=excluded.updated_at`
	_, err := w.tx.ExecContext(ctx, query, log.TaskID, log.FilePath, log.PeerID, log.Direction, log.TotalSize, log.Transferred, log.Status, log.UpdatedAt)
	return err
}

// ---- 私有辅助函数 ----

func scanMessage(rows *sql.Rows) (*Message, error) {
	m := &Message{}
	err := rows.Scan(&m.ID, &m.HLC, &m.SenderID, &m.ReceiverID, &m.Content, &m.Type, &m.CreatedAt)
	return m, err
}
