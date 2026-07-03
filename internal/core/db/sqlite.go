package db

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"

	// 引入纯 Go 实现的 SQLite 驱动
	_ "modernc.org/sqlite"
)

type sqliteDB struct {
	conn *sql.DB
}

type Migration struct {
	Version int
	Name    string
	Up      func(tx *sql.Tx) error
}

var migrations = []Migration{
	{
		Version: 1,
		Name:    "baseline",
		Up: func(tx *sql.Tx) error {
			_, err := tx.Exec(`
				CREATE TABLE IF NOT EXISTS messages (
					id TEXT PRIMARY KEY,
					hlc TEXT NOT NULL,
					sender_id TEXT NOT NULL,
					receiver_id TEXT NOT NULL DEFAULT '',
					content BLOB NOT NULL,
					type INTEGER NOT NULL,
					created_at DATETIME DEFAULT CURRENT_TIMESTAMP
				);
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
			`)
			return err
		},
	},
	{
		Version: 2,
		Name:    "add_team_id_to_messages",
		Up: func(tx *sql.Tx) error {
			_, err := tx.Exec(`
				ALTER TABLE messages ADD COLUMN team_id TEXT NOT NULL DEFAULT '';
				CREATE INDEX IF NOT EXISTS idx_messages_team_hlc ON messages(team_id, hlc);
			`)
			return err
		},
	},
	{
		Version: 3,
		Name:    "add_teams_table",
		Up: func(tx *sql.Tx) error {
			_, err := tx.Exec(`
				CREATE TABLE IF NOT EXISTS teams (
					id TEXT PRIMARY KEY,
					name TEXT NOT NULL,
					team_hash TEXT NOT NULL,
					created_at DATETIME DEFAULT CURRENT_TIMESTAMP
				);
				CREATE INDEX IF NOT EXISTS idx_teams_hash ON teams(team_hash);
			`)
			return err
		},
	},
	{
		Version: 4,
		Name:    "add_shared_directories_table",
		Up: func(tx *sql.Tx) error {
			_, err := tx.Exec(`
				CREATE TABLE IF NOT EXISTS shared_directories (
					id TEXT PRIMARY KEY,
					path TEXT NOT NULL UNIQUE,
					team_id TEXT NOT NULL DEFAULT '',
					created_at DATETIME DEFAULT CURRENT_TIMESTAMP
				);
			`)
			return err
		},
	},
	{
		Version: 5,
		Name:    "add_channels_tables",
		Up: func(tx *sql.Tx) error {
			_, err := tx.Exec(`
				CREATE TABLE IF NOT EXISTS channels (
					id TEXT PRIMARY KEY,
					team_id TEXT NOT NULL,
					name TEXT NOT NULL,
					created_by TEXT NOT NULL,
					created_at DATETIME DEFAULT CURRENT_TIMESTAMP
				);
				CREATE INDEX IF NOT EXISTS idx_channels_team ON channels(team_id);

				CREATE TABLE IF NOT EXISTS channel_members (
					channel_id TEXT NOT NULL,
					pubkey TEXT NOT NULL,
					joined_at DATETIME DEFAULT CURRENT_TIMESTAMP,
					PRIMARY KEY(channel_id, pubkey)
				);
				CREATE INDEX IF NOT EXISTS idx_channel_members_channel ON channel_members(channel_id);

				ALTER TABLE messages ADD COLUMN channel_id TEXT NOT NULL DEFAULT '';
			`)
			return err
		},
	},
		{
			Version: 6,
			Name:    "add_share_groups_tables",
			Up: func(tx *sql.Tx) error {
				_, err := tx.Exec(`
				CREATE TABLE IF NOT EXISTS share_groups (
					id TEXT PRIMARY KEY,
					team_id TEXT NOT NULL,
					name TEXT NOT NULL,
					created_by TEXT NOT NULL,
					created_at DATETIME DEFAULT CURRENT_TIMESTAMP
				);
				CREATE INDEX IF NOT EXISTS idx_share_groups_team ON share_groups(team_id);

				CREATE TABLE IF NOT EXISTS share_group_dirs (
					group_id TEXT NOT NULL,
					dir_path TEXT NOT NULL,
					added_at DATETIME DEFAULT CURRENT_TIMESTAMP,
					PRIMARY KEY(group_id, dir_path)
				);
				CREATE INDEX IF NOT EXISTS idx_share_group_dirs_group ON share_group_dirs(group_id);
			`)
				return err
			},
		},
		{
			Version: 7,
			Name:    "add_conversations_table",
			Up: func(tx *sql.Tx) error {
				_, err := tx.Exec(`
				CREATE TABLE IF NOT EXISTS conversations (
					id TEXT PRIMARY KEY,
					team_id TEXT NOT NULL,
					type TEXT NOT NULL,
					display_name TEXT,
					peer_pubkey TEXT,
					my_pubkey TEXT,
					last_hlc TEXT,
					created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
					updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
				);
				ALTER TABLE messages ADD COLUMN conversation_id TEXT NOT NULL DEFAULT '';
				CREATE INDEX IF NOT EXISTS idx_messages_conv_hlc ON messages(conversation_id, hlc);
				CREATE INDEX IF NOT EXISTS idx_conversations_team ON conversations(team_id);
			`)
				return err
			},
		},
		{
			Version: 8,
			Name:    "drop_channels_tables",
			Up: func(tx *sql.Tx) error {
				_, err := tx.Exec(`DROP TABLE IF EXISTS channel_members; DROP TABLE IF EXISTS channels;`)
				return err
			},
		},
		{
			Version: 9,
			Name:    "unique_hlc",
			Up: func(tx *sql.Tx) error {
				_, err := tx.Exec(`
					DELETE FROM messages WHERE rowid NOT IN (
						SELECT MIN(rowid) FROM messages GROUP BY hlc
					);
					CREATE UNIQUE INDEX IF NOT EXISTS idx_messages_hlc_unique ON messages(hlc);
				`)
				return err
			},
		},
		{
			Version: 10,
			Name:    "add_peer_crypto_key",
			Up: func(tx *sql.Tx) error {
				_, err := tx.Exec(`ALTER TABLE conversations ADD COLUMN peer_crypto_key TEXT NOT NULL DEFAULT '';`)
				return err
			},
		},
		{
			Version: 11,
			Name:    "merkle_tree_nodes",
			Up: func(tx *sql.Tx) error {
				_, err := tx.Exec(`
					CREATE TABLE IF NOT EXISTS merkle_tree_nodes (
						conv_id TEXT NOT NULL,
						bucket_path TEXT NOT NULL,
						level INTEGER NOT NULL,
						hash BLOB NOT NULL,
						frozen INTEGER DEFAULT 0,
						frozen_at TEXT,
						message_count INTEGER DEFAULT 0,
						PRIMARY KEY (conv_id, bucket_path)
					);
					CREATE INDEX IF NOT EXISTS idx_merkle_conv_level ON merkle_tree_nodes(conv_id, level);
				`)
				if err != nil {
					return err
				}
				_, err = tx.Exec(`
					CREATE TABLE IF NOT EXISTS merkle_freeze_state (
						conv_id TEXT NOT NULL,
						last_frozen_bucket TEXT NOT NULL,
						last_frozen_at TEXT NOT NULL,
						PRIMARY KEY (conv_id)
					);
				`)
				return err
			},
		},
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

func (db *sqliteDB) migrate() error {
	if _, err := db.conn.Exec(`CREATE TABLE IF NOT EXISTS schema_meta (key TEXT PRIMARY KEY, value TEXT)`); err != nil {
		return fmt.Errorf("failed to create schema_meta table: %w", err)
	}

	var currentVersion int
	row := db.conn.QueryRow(`SELECT value FROM schema_meta WHERE key = 'version'`)
	var versionStr string
	if err := row.Scan(&versionStr); err == nil {
		if v, err := strconv.Atoi(versionStr); err == nil {
			currentVersion = v
		} else if versionStr != "" {
			return fmt.Errorf("invalid schema version %q: %w", versionStr, err)
		}
	}

	for _, m := range migrations {
		if m.Version <= currentVersion {
			continue
		}

		tx, err := db.conn.Begin()
		if err != nil {
			return fmt.Errorf("migration %d (%s): failed to begin transaction: %w", m.Version, m.Name, err)
		}

		if err := m.Up(tx); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d (%s): %w", m.Version, m.Name, err)
		}

		if _, err := tx.Exec(`INSERT OR REPLACE INTO schema_meta(key, value) VALUES('version', ?)`, strconv.Itoa(m.Version)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d (%s): failed to record version: %w", m.Version, m.Name, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration %d (%s): failed to commit: %w", m.Version, m.Name, err)
		}
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

	err = sqlTx.Commit()
	if err != nil {
		sqlTx.Rollback()
		return err
	}
	return nil
}

type dbTxWrapper struct {
	tx *sql.Tx
}

// ---- 消息模块实现 ----

func (db *sqliteDB) InsertMessage(ctx context.Context, msg *Message) error {
	query := `INSERT INTO messages (id, hlc, sender_id, receiver_id, team_id, channel_id, conversation_id, content, type, created_at) 
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	          ON CONFLICT(hlc) DO NOTHING`
	_, err := db.conn.ExecContext(ctx, query,
		msg.ID, msg.HLC, msg.SenderID, msg.ReceiverID, msg.TeamID, msg.ChannelID, msg.ConversationID, msg.Content, msg.Type, msg.CreatedAt,
	)
	return err
}

func (db *sqliteDB) GetMessagesSinceHLC(ctx context.Context, hlc string, limit int) ([]*Message, error) {
	query := `SELECT id, hlc, sender_id, receiver_id, team_id, channel_id, conversation_id, content, type, created_at 
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return msgs, nil
}

func (db *sqliteDB) GetRecentMessages(ctx context.Context, limit int, offset int) ([]*Message, error) {
	query := `SELECT id, hlc, sender_id, receiver_id, team_id, channel_id, conversation_id, content, type, created_at 
	          FROM messages ORDER BY hlc DESC LIMIT ? OFFSET ?`
	rows, err := db.conn.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []*Message
	for rows.Next() {
		msg, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return msgs, nil
}

func (db *sqliteDB) GetRecentMessagesByTeam(ctx context.Context, teamID string, limit int, offset int) ([]*Message, error) {
	query := `SELECT id, hlc, sender_id, receiver_id, team_id, channel_id, conversation_id, content, type, created_at 
	          FROM messages WHERE team_id = ? ORDER BY hlc DESC LIMIT ? OFFSET ?`
	rows, err := db.conn.QueryContext(ctx, query, teamID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []*Message
	for rows.Next() {
		msg, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return msgs, nil
}

func (db *sqliteDB) GetRecentMessagesByChannel(ctx context.Context, channelID string, limit int, offset int) ([]*Message, error) {
	query := `SELECT id, hlc, sender_id, receiver_id, team_id, channel_id, conversation_id, content, type, created_at 
	          FROM messages WHERE channel_id = ? ORDER BY hlc DESC LIMIT ? OFFSET ?`
	rows, err := db.conn.QueryContext(ctx, query, channelID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []*Message
	for rows.Next() {
		msg, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return msgs, nil
}

func (db *sqliteDB) GetMessagesSinceHLCByTeam(ctx context.Context, teamID string, hlc string, limit int) ([]*Message, error) {
	query := `SELECT id, hlc, sender_id, receiver_id, team_id, channel_id, conversation_id, content, type, created_at 
	          FROM messages WHERE team_id = ? AND hlc > ? ORDER BY hlc ASC LIMIT ?`
	rows, err := db.conn.QueryContext(ctx, query, teamID, hlc, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []*Message
	for rows.Next() {
		msg, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
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

// ---- 团队模块实现 ----

func (db *sqliteDB) InsertTeam(ctx context.Context, team *Team) error {
	query := `INSERT OR IGNORE INTO teams (id, name, team_hash, created_at) VALUES (?, ?, ?, ?)`
	_, err := db.conn.ExecContext(ctx, query, team.ID, team.Name, team.TeamHash, team.CreatedAt)
	return err
}

func (db *sqliteDB) GetTeam(ctx context.Context, id string) (*Team, error) {
	query := `SELECT id, name, team_hash, created_at FROM teams WHERE id = ?`
	row := db.conn.QueryRowContext(ctx, query, id)

	t := &Team{}
	err := row.Scan(&t.ID, &t.Name, &t.TeamHash, &t.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return t, err
}

func (db *sqliteDB) GetTeamByHash(ctx context.Context, teamHash string) (*Team, error) {
	query := `SELECT id, name, team_hash, created_at FROM teams WHERE team_hash = ?`
	row := db.conn.QueryRowContext(ctx, query, teamHash)

	t := &Team{}
	err := row.Scan(&t.ID, &t.Name, &t.TeamHash, &t.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return t, err
}

func (db *sqliteDB) ListTeams(ctx context.Context) ([]*Team, error) {
	query := `SELECT id, name, team_hash, created_at FROM teams ORDER BY created_at ASC`
	rows, err := db.conn.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var teams []*Team
	for rows.Next() {
		t := &Team{}
		if err := rows.Scan(&t.ID, &t.Name, &t.TeamHash, &t.CreatedAt); err != nil {
			return nil, err
		}
		teams = append(teams, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return teams, nil
}

func (db *sqliteDB) DeleteTeam(ctx context.Context, id string) error {
	query := `DELETE FROM teams WHERE id = ?`
	_, err := db.conn.ExecContext(ctx, query, id)
	return err
}

// ---- 共享目录模块实现 ----

func (db *sqliteDB) InsertSharedDirectory(ctx context.Context, dir *SharedDirectory) error {
	query := `INSERT OR IGNORE INTO shared_directories (id, path, team_id, created_at) VALUES (?, ?, ?, ?)`
	_, err := db.conn.ExecContext(ctx, query, dir.ID, dir.Path, dir.TeamID, dir.CreatedAt)
	return err
}

func (db *sqliteDB) DeleteSharedDirectory(ctx context.Context, path string) error {
	query := `DELETE FROM shared_directories WHERE path = ?`
	_, err := db.conn.ExecContext(ctx, query, path)
	return err
}

func (db *sqliteDB) ListSharedDirectories(ctx context.Context) ([]*SharedDirectory, error) {
	query := `SELECT id, path, team_id, created_at FROM shared_directories ORDER BY path ASC`
	rows, err := db.conn.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var dirs []*SharedDirectory
	for rows.Next() {
		d, err := scanSharedDir(rows)
		if err != nil {
			return nil, err
		}
		dirs = append(dirs, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return dirs, nil
}

// ---- 共享组模块实现 ----

func (db *sqliteDB) CreateShareGroup(ctx context.Context, sg *ShareGroup) error {
	query := `INSERT OR IGNORE INTO share_groups (id, team_id, name, created_by, created_at) VALUES (?, ?, ?, ?, ?)`
	_, err := db.conn.ExecContext(ctx, query, sg.ID, sg.TeamID, sg.Name, sg.CreatedBy, sg.CreatedAt)
	return err
}

func (db *sqliteDB) GetShareGroup(ctx context.Context, id string) (*ShareGroup, error) {
	query := `SELECT id, team_id, name, created_by, created_at FROM share_groups WHERE id = ?`
	row := db.conn.QueryRowContext(ctx, query, id)
	sg := &ShareGroup{}
	err := row.Scan(&sg.ID, &sg.TeamID, &sg.Name, &sg.CreatedBy, &sg.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return sg, err
}

func (db *sqliteDB) ListShareGroupsByTeam(ctx context.Context, teamID string) ([]*ShareGroup, error) {
	query := `SELECT id, team_id, name, created_by, created_at FROM share_groups WHERE team_id = ? ORDER BY created_at ASC`
	rows, err := db.conn.QueryContext(ctx, query, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups []*ShareGroup
	for rows.Next() {
		sg := &ShareGroup{}
		if err := rows.Scan(&sg.ID, &sg.TeamID, &sg.Name, &sg.CreatedBy, &sg.CreatedAt); err != nil {
			return nil, err
		}
		groups = append(groups, sg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return groups, nil
}

func (db *sqliteDB) DeleteShareGroup(ctx context.Context, id string) error {
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM share_group_dirs WHERE group_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM share_groups WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (db *sqliteDB) AddDirectoryToShareGroup(ctx context.Context, groupID, dirPath string) error {
	query := `INSERT OR IGNORE INTO share_group_dirs (group_id, dir_path, added_at) VALUES (?, ?, ?)`
	_, err := db.conn.ExecContext(ctx, query, groupID, dirPath, time.Now())
	return err
}

func (db *sqliteDB) RemoveDirectoryFromShareGroup(ctx context.Context, groupID, dirPath string) error {
	query := `DELETE FROM share_group_dirs WHERE group_id = ? AND dir_path = ?`
	_, err := db.conn.ExecContext(ctx, query, groupID, dirPath)
	return err
}

func (db *sqliteDB) ListShareGroupDirs(ctx context.Context, groupID string) ([]*ShareGroupDir, error) {
	query := `SELECT group_id, dir_path, added_at FROM share_group_dirs WHERE group_id = ? ORDER BY added_at ASC`
	rows, err := db.conn.QueryContext(ctx, query, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var dirs []*ShareGroupDir
	for rows.Next() {
		d := &ShareGroupDir{}
		if err := rows.Scan(&d.GroupID, &d.DirPath, &d.AddedAt); err != nil {
			return nil, err
		}
		dirs = append(dirs, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return dirs, nil
}

// ---- 对话模块实现 ----

func (db *sqliteDB) UpsertConversation(ctx context.Context, conv *Conversation) error {
	query := `INSERT INTO conversations (id, team_id, type, display_name, peer_pubkey, my_pubkey, peer_crypto_key, last_hlc, created_at, updated_at)
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	          ON CONFLICT(id) DO UPDATE SET
	          last_hlc = COALESCE(NULLIF(excluded.last_hlc, ''), conversations.last_hlc),
	          updated_at = excluded.updated_at,
	          display_name = COALESCE(NULLIF(excluded.display_name, ''), conversations.display_name),
	          peer_crypto_key = COALESCE(NULLIF(excluded.peer_crypto_key, ''), conversations.peer_crypto_key)`
	_, err := db.conn.ExecContext(ctx, query,
		conv.ID, conv.TeamID, conv.Type, conv.DisplayName, conv.PeerPubkey, conv.MyPubkey, conv.PeerCryptoKey,
		conv.LastHLC, conv.CreatedAt, conv.UpdatedAt,
	)
	return err
}

func (db *sqliteDB) GetConversation(ctx context.Context, id string) (*Conversation, error) {
	query := `SELECT id, team_id, type, display_name, peer_pubkey, my_pubkey, peer_crypto_key, last_hlc, created_at, updated_at
	          FROM conversations WHERE id = ?`
	row := db.conn.QueryRowContext(ctx, query, id)
	c := &Conversation{}
	err := row.Scan(&c.ID, &c.TeamID, &c.Type, &c.DisplayName, &c.PeerPubkey, &c.MyPubkey, &c.PeerCryptoKey, &c.LastHLC, &c.CreatedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return c, err
}

func (db *sqliteDB) ListConversations(ctx context.Context, teamID string) ([]*ConversationView, error) {
	query := `SELECT c.id, c.team_id, c.type, c.display_name, c.peer_pubkey, c.my_pubkey, c.peer_crypto_key, c.last_hlc, c.created_at, c.updated_at,
	                 m.content, m.created_at
	          FROM conversations c
	          LEFT JOIN messages m ON m.conversation_id = c.id AND m.hlc = (
	              SELECT MAX(hlc) FROM messages WHERE conversation_id = c.id
	          )
	          WHERE c.team_id = ?
	          ORDER BY c.updated_at DESC`
	rows, err := db.conn.QueryContext(ctx, query, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var convs []*ConversationView
	for rows.Next() {
		cv := &ConversationView{}
		var lastContent []byte
		var lastTime sql.NullTime
		if err := rows.Scan(&cv.ID, &cv.TeamID, &cv.Type, &cv.DisplayName, &cv.PeerPubkey, &cv.MyPubkey, &cv.PeerCryptoKey,
			&cv.LastHLC, &cv.CreatedAt, &cv.UpdatedAt, &lastContent, &lastTime); err != nil {
			return nil, err
		}
		cv.LastMessage = string(lastContent)
		if lastTime.Valid {
			cv.LastMsgTime = lastTime.Time
		}
		convs = append(convs, cv)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return convs, nil
}

func (db *sqliteDB) GetConversationMessages(ctx context.Context, convID string, limit int, offset int) ([]*Message, error) {
	query := `SELECT id, hlc, sender_id, receiver_id, team_id, channel_id, conversation_id, content, type, created_at
	          FROM messages WHERE conversation_id = ? ORDER BY hlc DESC LIMIT ? OFFSET ?`
	rows, err := db.conn.QueryContext(ctx, query, convID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

func (db *sqliteDB) GetConversationMessagesSinceHLC(ctx context.Context, convID string, sinceHLC string, limit int) ([]*Message, error) {
	query := `SELECT id, hlc, sender_id, receiver_id, team_id, channel_id, conversation_id, content, type, created_at
	          FROM messages WHERE conversation_id = ? AND hlc > ? ORDER BY hlc ASC LIMIT ?`
	rows, err := db.conn.QueryContext(ctx, query, convID, sinceHLC, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

func (db *sqliteDB) UpdateConversationLastHLC(ctx context.Context, convID string, hlc string) error {
	_, err := db.conn.ExecContext(ctx, `UPDATE conversations SET last_hlc = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, hlc, convID)
	return err
}

func (db *sqliteDB) ListAllConversations(ctx context.Context) ([]*Conversation, error) {
	rows, err := db.conn.QueryContext(ctx,
		`SELECT id, team_id, type, display_name, peer_pubkey, my_pubkey, peer_crypto_key, last_hlc, created_at, updated_at
		 FROM conversations ORDER BY type ASC, updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var convs []*Conversation
	for rows.Next() {
		c := &Conversation{}
		if err := rows.Scan(&c.ID, &c.TeamID, &c.Type, &c.DisplayName, &c.PeerPubkey, &c.MyPubkey, &c.PeerCryptoKey, &c.LastHLC, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		convs = append(convs, c)
	}
	return convs, rows.Err()
}

func (db *sqliteDB) UpsertMerkleNode(ctx context.Context, node *MerkleNode) error {
	var frozenAt interface{}
	if node.FrozenAt != nil {
		frozenAt = node.FrozenAt.Format(time.RFC3339)
	}
	frozenVal := 0
	if node.Frozen {
		frozenVal = 1
	}
	query := `INSERT OR REPLACE INTO merkle_tree_nodes (conv_id, bucket_path, level, hash, frozen, frozen_at, message_count)
	          VALUES (?, ?, ?, ?, ?, ?, ?)`
	_, err := db.conn.ExecContext(ctx, query,
		node.ConvID, node.BucketPath, node.Level, node.Hash, frozenVal, frozenAt, node.MessageCount)
	return err
}

func (db *sqliteDB) GetMerkleNode(ctx context.Context, convID, bucketPath string) (*MerkleNode, error) {
	query := `SELECT conv_id, bucket_path, level, hash, frozen, frozen_at, message_count
	          FROM merkle_tree_nodes WHERE conv_id = ? AND bucket_path = ?`
	row := db.conn.QueryRowContext(ctx, query, convID, bucketPath)
	return scanMerkleNode(row)
}

func (db *sqliteDB) GetMerkleNodesByLevel(ctx context.Context, convID string, level int) ([]*MerkleNode, error) {
	query := `SELECT conv_id, bucket_path, level, hash, frozen, frozen_at, message_count
	          FROM merkle_tree_nodes WHERE conv_id = ? AND level = ? ORDER BY bucket_path ASC`
	rows, err := db.conn.QueryContext(ctx, query, convID, level)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMerkleNodes(rows)
}

func (db *sqliteDB) GetMerkleNodesByParent(ctx context.Context, convID, parentPath string) ([]*MerkleNode, error) {
	query := `SELECT conv_id, bucket_path, level, hash, frozen, frozen_at, message_count
	          FROM merkle_tree_nodes WHERE conv_id = ? AND bucket_path LIKE ? ORDER BY bucket_path ASC`
	rows, err := db.conn.QueryContext(ctx, query, convID, parentPath+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMerkleNodes(rows)
}

func (db *sqliteDB) DeleteMerkleNodesByConv(ctx context.Context, convID string) error {
	_, err := db.conn.ExecContext(ctx, `DELETE FROM merkle_tree_nodes WHERE conv_id = ?`, convID)
	return err
}

func (db *sqliteDB) GetFrozenState(ctx context.Context, convID string) (*FreezeState, error) {
	query := `SELECT conv_id, last_frozen_bucket, last_frozen_at FROM merkle_freeze_state WHERE conv_id = ?`
	row := db.conn.QueryRowContext(ctx, query, convID)
	state := &FreezeState{}
	var frozenAt string
	err := row.Scan(&state.ConvID, &state.LastFrozenBucket, &frozenAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	state.LastFrozenAt, err = time.Parse(time.RFC3339, frozenAt)
	if err != nil {
		return nil, err
	}
	return state, nil
}

func (db *sqliteDB) UpsertFrozenState(ctx context.Context, state *FreezeState) error {
	query := `INSERT OR REPLACE INTO merkle_freeze_state (conv_id, last_frozen_bucket, last_frozen_at)
	          VALUES (?, ?, ?)`
	_, err := db.conn.ExecContext(ctx, query, state.ConvID, state.LastFrozenBucket, state.LastFrozenAt.Format(time.RFC3339))
	return err
}

func (db *sqliteDB) GetMessagesInBucket(ctx context.Context, convID, bucketPath string, level int) ([]*Message, error) {
	startHLC, endHLC, err := bucketTimeRange(bucketPath, level)
	if err != nil {
		return nil, err
	}
	query := `SELECT id, hlc, sender_id, receiver_id, team_id, channel_id, conversation_id, content, type, created_at
	          FROM messages WHERE conversation_id = ? AND hlc >= ? AND hlc < ? ORDER BY hlc ASC`
	rows, err := db.conn.QueryContext(ctx, query, convID, startHLC, endHLC)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

// ---- 事务包装器方法实现 ----

func (w *dbTxWrapper) UpsertMerkleNodeTx(ctx context.Context, node *MerkleNode) error {
	var frozenAt interface{}
	if node.FrozenAt != nil {
		frozenAt = node.FrozenAt.Format(time.RFC3339)
	}
	frozenVal := 0
	if node.Frozen {
		frozenVal = 1
	}
	query := `INSERT OR REPLACE INTO merkle_tree_nodes (conv_id, bucket_path, level, hash, frozen, frozen_at, message_count)
	          VALUES (?, ?, ?, ?, ?, ?, ?)`
	_, err := w.tx.ExecContext(ctx, query,
		node.ConvID, node.BucketPath, node.Level, node.Hash, frozenVal, frozenAt, node.MessageCount)
	return err
}

func (w *dbTxWrapper) UpsertFrozenStateTx(ctx context.Context, state *FreezeState) error {
	query := `INSERT OR REPLACE INTO merkle_freeze_state (conv_id, last_frozen_bucket, last_frozen_at)
	          VALUES (?, ?, ?)`
	_, err := w.tx.ExecContext(ctx, query, state.ConvID, state.LastFrozenBucket, state.LastFrozenAt.Format(time.RFC3339))
	return err
}

// ---- 私有辅助函数 ----

func scanMerkleNode(row interface{ Scan(...interface{}) error }) (*MerkleNode, error) {
	node := &MerkleNode{}
	var frozenVal int
	var frozenAt sql.NullString
	err := row.Scan(&node.ConvID, &node.BucketPath, &node.Level, &node.Hash, &frozenVal, &frozenAt, &node.MessageCount)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	node.Frozen = frozenVal == 1
	if frozenAt.Valid && frozenAt.String != "" {
		t, err := time.Parse(time.RFC3339, frozenAt.String)
		if err == nil {
			node.FrozenAt = &t
		}
	}
	return node, nil
}

func scanMerkleNodes(rows *sql.Rows) ([]*MerkleNode, error) {
	var nodes []*MerkleNode
	for rows.Next() {
		node, err := scanMerkleNode(rows)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return nodes, nil
}

func bucketTimeRange(bucketPath string, level int) (startHLC, endHLC string, err error) {
	var t time.Time
	switch level {
	case 0: // year
		t, err = time.Parse("2006", bucketPath)
		if err != nil {
			return "", "", err
		}
		startHLC = t.Format("2006-01-02T15:04:05.000Z") + "_0000_"
		endHLC = t.AddDate(1, 0, 0).Format("2006-01-02T15:04:05.000Z") + "_0000_"
	case 1: // month
		t, err = time.Parse("2006-01", bucketPath)
		if err != nil {
			return "", "", err
		}
		startHLC = t.Format("2006-01-02T15:04:05.000Z") + "_0000_"
		endHLC = t.AddDate(0, 1, 0).Format("2006-01-02T15:04:05.000Z") + "_0000_"
	case 2: // day
		t, err = time.Parse("2006-01-02", bucketPath)
		if err != nil {
			return "", "", err
		}
		startHLC = t.Format("2006-01-02T15:04:05.000Z") + "_0000_"
		endHLC = t.AddDate(0, 0, 1).Format("2006-01-02T15:04:05.000Z") + "_0000_"
	case 3: // hour
		t, err = time.Parse("2006-01-02T15", bucketPath)
		if err != nil {
			return "", "", err
		}
		startHLC = t.Format("2006-01-02T15:04:05.000Z") + "_0000_"
		endHLC = t.Add(1 * time.Hour).Format("2006-01-02T15:04:05.000Z") + "_0000_"
	default:
		return "", "", fmt.Errorf("invalid bucket level %d", level)
	}
	return startHLC, endHLC, nil
}

// scanMessages reads Message rows from a *sql.Rows.
func scanMessages(rows *sql.Rows) ([]*Message, error) {
	var msgs []*Message
	for rows.Next() {
		m := &Message{}
		if err := rows.Scan(&m.ID, &m.HLC, &m.SenderID, &m.ReceiverID, &m.TeamID, &m.ChannelID, &m.ConversationID, &m.Content, &m.Type, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return msgs, nil
}

// ---- 事务包装器方法实现 ----

func (w *dbTxWrapper) InsertMessageTx(ctx context.Context, msg *Message) error {
	query := `INSERT INTO messages (id, hlc, sender_id, receiver_id, team_id, channel_id, conversation_id, content, type, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(hlc) DO NOTHING`
	_, err := w.tx.ExecContext(ctx, query, msg.ID, msg.HLC, msg.SenderID, msg.ReceiverID, msg.TeamID, msg.ChannelID, msg.ConversationID, msg.Content, msg.Type, msg.CreatedAt)
	return err
}

func (w *dbTxWrapper) UpsertFileLogTx(ctx context.Context, log *FileLog) error {
	query := `INSERT INTO file_logs (task_id, file_path, peer_id, direction, total_size, transferred, status, updated_at)
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	          ON CONFLICT(task_id) DO UPDATE SET transferred=excluded.transferred, status=excluded.status, updated_at=excluded.updated_at`
	_, err := w.tx.ExecContext(ctx, query, log.TaskID, log.FilePath, log.PeerID, log.Direction, log.TotalSize, log.Transferred, log.Status, log.UpdatedAt)
	return err
}

func (w *dbTxWrapper) UpsertConversationTx(ctx context.Context, conv *Conversation) error {
	query := `INSERT INTO conversations (id, team_id, type, display_name, peer_pubkey, my_pubkey, peer_crypto_key, last_hlc, created_at, updated_at)
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	          ON CONFLICT(id) DO UPDATE SET
	          last_hlc = COALESCE(NULLIF(excluded.last_hlc, ''), conversations.last_hlc),
	          updated_at = excluded.updated_at,
	          display_name = COALESCE(NULLIF(excluded.display_name, ''), conversations.display_name),
	          peer_crypto_key = COALESCE(NULLIF(excluded.peer_crypto_key, ''), conversations.peer_crypto_key)`
	_, err := w.tx.ExecContext(ctx, query, conv.ID, conv.TeamID, conv.Type, conv.DisplayName, conv.PeerPubkey, conv.MyPubkey, conv.PeerCryptoKey, conv.LastHLC, conv.CreatedAt, conv.UpdatedAt)
	return err
}

// ---- 私有辅助函数 ----

func scanMessage(rows *sql.Rows) (*Message, error) {
	m := &Message{}
	err := rows.Scan(&m.ID, &m.HLC, &m.SenderID, &m.ReceiverID, &m.TeamID, &m.ChannelID, &m.ConversationID, &m.Content, &m.Type, &m.CreatedAt)
	return m, err
}

func scanSharedDir(row interface{ Scan(...interface{}) error }) (*SharedDirectory, error) {
	d := &SharedDirectory{}
	err := row.Scan(&d.ID, &d.Path, &d.TeamID, &d.CreatedAt)
	return d, err
}
