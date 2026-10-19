package tools

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Goblin-Master/goblin/internal/session"
)

type MySQLTodoStore struct {
	db *sql.DB
}

func NewMySQLTodoStore(conn *sql.DB) (*MySQLTodoStore, error) {
	if conn == nil {
		return nil, errors.New("MySQL 连接不能为空")
	}
	return &MySQLTodoStore{db: conn}, nil
}

func (s *MySQLTodoStore) List(ctx context.Context, sessionID string) ([]Todo, error) {
	id, err := session.ParseID(sessionID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT todo_id, text, done, created_at FROM todos WHERE session_id = ? ORDER BY created_at, todo_id`, id)
	if err != nil {
		return nil, fmt.Errorf("读取待办失败: %w", err)
	}
	defer rows.Close()
	items := []Todo{}
	for rows.Next() {
		var item Todo
		if err := rows.Scan(&item.ID, &item.Text, &item.Done, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("扫描待办失败: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历待办失败: %w", err)
	}
	return items, nil
}

func (s *MySQLTodoStore) Add(ctx context.Context, sessionID, text string) ([]Todo, error) {
	id, err := session.ParseID(sessionID)
	if err != nil {
		return nil, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("待办内容不能为空")
	}
	if len([]rune(text)) > 500 {
		return nil, errors.New("待办内容不能超过 500 个字符")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开始待办事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var sessionIDCheck string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM sessions WHERE id = ? FOR UPDATE`, id).Scan(&sessionIDCheck); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, os.ErrNotExist
		}
		return nil, fmt.Errorf("锁定会话失败: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT todo_id FROM todos WHERE session_id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("读取待办编号失败: %w", err)
	}
	next := 1
	for rows.Next() {
		var todoID string
		if err := rows.Scan(&todoID); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("扫描待办编号失败: %w", err)
		}
		if n, err := strconv.Atoi(strings.TrimPrefix(todoID, "t")); err == nil && n >= next {
			next = n + 1
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	now := time.Now().Truncate(time.Microsecond)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO todos (session_id, todo_id, text, done, created_at) VALUES (?, ?, ?, FALSE, ?)`,
		id, fmt.Sprintf("t%d", next), text, now); err != nil {
		return nil, fmt.Errorf("新增待办失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交待办事务失败: %w", err)
	}
	return s.List(ctx, id)
}

func (s *MySQLTodoStore) Complete(ctx context.Context, sessionID, todoID string) ([]Todo, error) {
	id, err := session.ParseID(sessionID)
	if err != nil {
		return nil, err
	}
	todoID = strings.TrimSpace(todoID)
	if todoID == "" {
		return nil, errors.New("待办 ID 不能为空")
	}
	res, err := s.db.ExecContext(ctx, `UPDATE todos SET done = TRUE WHERE session_id = ? AND todo_id = ?`, id, todoID)
	if err != nil {
		return nil, fmt.Errorf("完成待办失败: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("读取待办更新结果失败: %w", err)
	}
	if affected == 0 {
		var one int
		if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM todos WHERE session_id = ? AND todo_id = ?`, id, todoID).Scan(&one); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("待办 %q 不存在", todoID)
			}
			return nil, fmt.Errorf("检查待办失败: %w", err)
		}
	}
	return s.List(ctx, id)
}

func (s *MySQLTodoStore) Remove(ctx context.Context, sessionID, todoID string) ([]Todo, error) {
	return s.removeOrUpdate(ctx, sessionID, todoID, `DELETE FROM todos WHERE session_id = ? AND todo_id = ?`)
}

func (s *MySQLTodoStore) removeOrUpdate(ctx context.Context, sessionID, todoID, query string) ([]Todo, error) {
	id, err := session.ParseID(sessionID)
	if err != nil {
		return nil, err
	}
	todoID = strings.TrimSpace(todoID)
	if todoID == "" {
		return nil, errors.New("待办 ID 不能为空")
	}
	res, err := s.db.ExecContext(ctx, query, id, todoID)
	if err != nil {
		return nil, fmt.Errorf("更新待办失败: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("读取待办更新结果失败: %w", err)
	}
	if affected == 0 {
		return nil, fmt.Errorf("待办 %q 不存在", todoID)
	}
	return s.List(ctx, id)
}

var _ TodoStore = (*MySQLTodoStore)(nil)
