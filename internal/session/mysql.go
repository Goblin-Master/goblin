package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	mysql "github.com/go-sql-driver/mysql"
)

type MySQLStore struct {
	db *sql.DB
}

func NewMySQLStore(conn *sql.DB) (*MySQLStore, error) {
	if conn == nil {
		return nil, errors.New("MySQL 连接不能为空")
	}
	return &MySQLStore{db: conn}, nil
}

func (s *MySQLStore) Create(id string) (*Session, error) {
	id, err := ParseID(id)
	if err != nil {
		return nil, err
	}
	now := time.Now().Truncate(time.Microsecond)
	_, err = s.db.ExecContext(context.Background(),
		`INSERT INTO sessions (id, title, summary, summary_up_to, created_at, updated_at) VALUES (?, '', '', 0, ?, ?)`,
		id, now, now)
	if err != nil {
		if isDuplicate(err) {
			return nil, fmt.Errorf("会话 %q 已存在", id)
		}
		return nil, fmt.Errorf("创建会话失败: %w", err)
	}
	return &Session{Version: Version, ID: id, CreatedAt: now, UpdatedAt: now, Messages: []Message{}}, nil
}

func (s *MySQLStore) Load(id string) (*Session, error) {
	id, err := ParseID(id)
	if err != nil {
		return nil, err
	}
	var sess Session
	err = s.db.QueryRowContext(context.Background(),
		`SELECT title, summary, summary_up_to, created_at, updated_at FROM sessions WHERE id = ?`, id).
		Scan(&sess.Title, &sess.Summary, &sess.SummaryUpTo, &sess.CreatedAt, &sess.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, fmt.Errorf("读取会话失败: %w", err)
	}
	sess.Version = Version
	sess.ID = id

	rows, err := s.db.QueryContext(context.Background(),
		`SELECT role, content, reasoning, tool_calls, tool_call_id, created_at
		 FROM messages WHERE session_id = ? ORDER BY seq`, id)
	if err != nil {
		return nil, fmt.Errorf("读取会话消息失败: %w", err)
	}
	defer rows.Close()
	sess.Messages = []Message{}
	for rows.Next() {
		var msg Message
		var toolCalls []byte
		if err := rows.Scan(&msg.Role, &msg.Content, &msg.Reasoning, &toolCalls, &msg.ToolCallID, &msg.CreatedAt); err != nil {
			return nil, fmt.Errorf("扫描会话消息失败: %w", err)
		}
		if len(toolCalls) > 0 {
			if err := json.Unmarshal(toolCalls, &msg.ToolCalls); err != nil {
				return nil, fmt.Errorf("解析工具调用失败: %w", err)
			}
		}
		sess.Messages = append(sess.Messages, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历会话消息失败: %w", err)
	}
	return &sess, nil
}

func (s *MySQLStore) Save(sess *Session) error {
	if sess == nil {
		return errors.New("session 不能为空")
	}
	id, err := ParseID(sess.ID)
	if err != nil {
		return err
	}
	sess.ID = id
	now := time.Now().Truncate(time.Microsecond)
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = now
	}
	sess.UpdatedAt = now
	sess.Version = Version

	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("开始事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(context.Background(),
		`INSERT INTO sessions (id, title, summary, summary_up_to, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE title=VALUES(title), summary=VALUES(summary),
		   summary_up_to=VALUES(summary_up_to), updated_at=VALUES(updated_at)`,
		sess.ID, sess.Title, sess.Summary, sess.SummaryUpTo, sess.CreatedAt, sess.UpdatedAt); err != nil {
		return fmt.Errorf("保存会话失败: %w", err)
	}
	if _, err := tx.ExecContext(context.Background(), `DELETE FROM messages WHERE session_id = ?`, sess.ID); err != nil {
		return fmt.Errorf("清理旧消息失败: %w", err)
	}
	for i, msg := range sess.Messages {
		if msg.CreatedAt.IsZero() {
			msg.CreatedAt = now
			sess.Messages[i].CreatedAt = now
		}
		var toolCalls any
		if len(msg.ToolCalls) > 0 {
			data, err := json.Marshal(msg.ToolCalls)
			if err != nil {
				return fmt.Errorf("编码工具调用失败: %w", err)
			}
			toolCalls = data
		}
		if _, err := tx.ExecContext(context.Background(),
			`INSERT INTO messages (session_id, seq, role, content, reasoning, tool_calls, tool_call_id, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			sess.ID, i, msg.Role, msg.Content, msg.Reasoning, toolCalls, msg.ToolCallID, msg.CreatedAt); err != nil {
			return fmt.Errorf("保存消息失败: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交事务失败: %w", err)
	}
	return nil
}

func (s *MySQLStore) List() ([]Meta, error) {
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT s.id, s.title, s.updated_at, (SELECT COUNT(*) FROM messages m WHERE m.session_id = s.id)
		 FROM sessions s ORDER BY s.updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("列出会话失败: %w", err)
	}
	defer rows.Close()
	metas := []Meta{}
	for rows.Next() {
		var meta Meta
		if err := rows.Scan(&meta.ID, &meta.Title, &meta.UpdatedAt, &meta.Messages); err != nil {
			return nil, fmt.Errorf("扫描会话列表失败: %w", err)
		}
		metas = append(metas, meta)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return metas, nil
}

func (s *MySQLStore) Exists(id string) bool {
	id, err := ParseID(id)
	if err != nil {
		return false
	}
	var one int
	if err := s.db.QueryRowContext(context.Background(), `SELECT 1 FROM sessions WHERE id = ?`, id).Scan(&one); err != nil {
		return false
	}
	return true
}

func isDuplicate(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}

var _ Store = (*MySQLStore)(nil)
