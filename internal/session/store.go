package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Goblin-Master/goblin/internal/fileutil"
	"github.com/Goblin-Master/goblin/internal/llm"
	"github.com/google/uuid"
)

const Version = 1

var validID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type Message struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	Reasoning  string         `json:"reasoning,omitempty"`
	ToolCalls  []llm.ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}

type Session struct {
	Version     int       `json:"version"`
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Summary     string    `json:"summary"`
	SummaryUpTo int       `json:"summary_up_to"`
	Messages    []Message `json:"messages"`
}

type Meta struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	UpdatedAt time.Time `json:"updated_at"`
	Messages  int       `json:"messages"`
}

// Store is the persistence seam used by the Agent and CLI.
type Store interface {
	Create(id string) (*Session, error)
	Load(id string) (*Session, error)
	Save(s *Session) error
	List() ([]Meta, error)
	Exists(id string) bool
}

type JSONStore struct {
	dir string
	mu  sync.Mutex
}

func NewJSONStore(baseDir string) (*JSONStore, error) {
	if strings.TrimSpace(baseDir) == "" {
		return nil, errors.New("storage directory 不能为空")
	}
	dir := filepath.Join(baseDir, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create sessions directory: %w", err)
	}
	return &JSONStore{dir: dir}, nil
}

func ValidID(id string) bool {
	return validID.MatchString(id)
}

// NewID returns a UUID v4 session identifier. Generated IDs are always UUIDs;
// ValidID also accepts short readable IDs for file-store tests and tooling.
func NewID() string {
	return uuid.NewString()
}

func IsUUID(id string) bool {
	_, err := uuid.Parse(id)
	return err == nil
}

func (s *JSONStore) Create(id string) (*Session, error) {
	if !ValidID(id) {
		return nil, fmt.Errorf("非法会话 ID %q：只允许字母、数字、下划线和连字符，长度 1-64", id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.path(id)
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("会话 %q 已存在", id)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("stat session %q: %w", id, err)
	}
	now := time.Now()
	sess := &Session{
		Version:   Version,
		ID:        id,
		CreatedAt: now,
		UpdatedAt: now,
		Messages:  []Message{},
	}
	if err := s.saveLocked(sess); err != nil {
		return nil, err
	}
	return sess, nil
}

func (s *JSONStore) Load(id string) (*Session, error) {
	if !ValidID(id) {
		return nil, fmt.Errorf("非法会话 ID %q：只允许字母、数字、下划线和连字符，长度 1-64", id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, fmt.Errorf("read session %q: %w", id, err)
	}
	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("会话文件 %q 损坏: %w（请备份后删除该文件）", id, err)
	}
	if sess.ID != id {
		return nil, fmt.Errorf("会话文件 %q 内部 ID 为 %q，拒绝加载", id, sess.ID)
	}
	if sess.Version == 0 {
		sess.Version = Version
	}
	if sess.Messages == nil {
		sess.Messages = []Message{}
	}
	return &sess, nil
}

func (s *JSONStore) LoadOrCreate(id string) (*Session, error) {
	sess, err := s.Load(id)
	if err == nil {
		return sess, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return s.Create(id)
}

func (s *JSONStore) Save(sess *Session) error {
	if sess == nil {
		return errors.New("session 不能为空")
	}
	if !ValidID(sess.ID) {
		return fmt.Errorf("非法会话 ID %q", sess.ID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(sess)
}

func (s *JSONStore) saveLocked(sess *Session) error {
	sess.Version = Version
	sess.UpdatedAt = time.Now()
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = sess.UpdatedAt
	}
	if sess.Messages == nil {
		sess.Messages = []Message{}
	}
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return fmt.Errorf("encode session %q: %w", sess.ID, err)
	}
	data = append(data, '\n')
	if err := fileutil.WriteAtomic(s.path(sess.ID), data, 0o600); err != nil {
		return fmt.Errorf("save session %q: %w", sess.ID, err)
	}
	return nil
}

func (s *JSONStore) List() ([]Meta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	metas := make([]Meta, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !ValidID(id) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read session %q: %w", id, err)
		}
		var sess Session
		if err := json.Unmarshal(data, &sess); err != nil {
			return nil, fmt.Errorf("会话文件 %q 损坏: %w", id, err)
		}
		metas = append(metas, Meta{ID: sess.ID, Title: sess.Title, UpdatedAt: sess.UpdatedAt, Messages: len(sess.Messages)})
	}
	sort.Slice(metas, func(i, j int) bool {
		return metas[i].UpdatedAt.After(metas[j].UpdatedAt)
	})
	return metas, nil
}

func (s *JSONStore) Exists(id string) bool {
	if !ValidID(id) {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := os.Stat(s.path(id))
	return err == nil
}

func (s *JSONStore) path(id string) string {
	return filepath.Join(s.dir, id+".json")
}

// TitleFromInput returns a short deterministic title for a new session.
func TitleFromInput(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return "新会话"
	}
	r := []rune(input)
	if len(r) > 30 {
		return string(r[:30]) + "…"
	}
	return string(r)
}

// TruncateRunes truncates s to at most n runes and appends an ellipsis.
func TruncateRunes(s string, n int) string {
	if n <= 0 || utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}
