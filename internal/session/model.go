package session

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Goblin-Master/goblin/internal/llm"
	"github.com/google/uuid"
)

const Version = 1

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
	Save(*Session) error
	List() ([]Meta, error)
	Exists(id string) bool
}

// NewID returns a UUID v4 session identifier.
func NewID() string {
	return uuid.NewString()
}

// ParseID validates and canonicalizes a session UUID.
func ParseID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("session UUID 不能为空")
	}
	parsed, err := uuid.Parse(id)
	if err != nil || len(id) != 36 {
		return "", fmt.Errorf("非法 session UUID %q：需要标准 36 位 UUID", id)
	}
	return parsed.String(), nil
}

func ValidID(id string) bool {
	_, err := ParseID(id)
	return err == nil
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
