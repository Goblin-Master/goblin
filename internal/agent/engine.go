package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Goblin-Master/goblin/internal/llm"
	"github.com/Goblin-Master/goblin/internal/logging"
	"github.com/Goblin-Master/goblin/internal/memory"
	"github.com/Goblin-Master/goblin/internal/session"
	"github.com/Goblin-Master/goblin/internal/tools"
	"github.com/google/uuid"
)

type Options struct {
	Client      llm.ChatClient
	Store       session.Store
	Tools       *tools.Registry
	Memory      *memory.Manager
	Logger      *slog.Logger
	Model       string
	MaxSteps    int
	Temperature float64
}

type Engine struct {
	client      llm.ChatClient
	store       session.Store
	tools       *tools.Registry
	memory      *memory.Manager
	logger      *slog.Logger
	model       string
	maxSteps    int
	temperature float64
	locks       sync.Map
}

type Reply struct {
	SessionID string
	TraceID   string
	Text      string
	Steps     int
	ToolCalls []ToolCallRecord
	Usage     llm.Usage
}

type ToolCallRecord struct {
	ID         string
	Name       string
	Arguments  string
	Result     string
	IsError    bool
	DurationMS int64
}

func New(opts Options) (*Engine, error) {
	if opts.Client == nil {
		return nil, errors.New("LLM client 不能为空")
	}
	if opts.Store == nil {
		return nil, errors.New("session store 不能为空")
	}
	if opts.Tools == nil {
		return nil, errors.New("tool registry 不能为空")
	}
	if opts.Memory == nil {
		return nil, errors.New("memory manager 不能为空")
	}
	if strings.TrimSpace(opts.Model) == "" {
		return nil, errors.New("model 不能为空")
	}
	if opts.MaxSteps < 1 {
		return nil, errors.New("max_steps 必须至少为 1")
	}
	if opts.Logger == nil {
		opts.Logger = logging.Discard()
	}
	return &Engine{
		client:      opts.Client,
		store:       opts.Store,
		tools:       opts.Tools,
		memory:      opts.Memory,
		logger:      opts.Logger,
		model:       opts.Model,
		maxSteps:    opts.MaxSteps,
		temperature: opts.Temperature,
	}, nil
}

func (e *Engine) Run(ctx context.Context, sessionID, input string) (Reply, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return Reply{}, errors.New("输入不能为空")
	}
	id, err := session.ParseID(sessionID)
	if err != nil {
		return Reply{}, err
	}
	lock := e.lockFor(id)
	lock.Lock()
	defer lock.Unlock()

	sess, err := e.store.Load(id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Reply{}, fmt.Errorf("会话 %s 不存在；先运行 goblin chat 创建，或用 goblin sessions 查看", id)
		}
		return Reply{}, fmt.Errorf("加载会话失败: %w", err)
	}
	if sess.Title == "" {
		sess.Title = session.TitleFromInput(input)
	}
	now := time.Now()
	sess.Messages = append(sess.Messages, session.Message{
		Role:      llm.RoleUser,
		Content:   input,
		CreatedAt: now,
	})

	traceID := uuid.NewString()
	reply := Reply{SessionID: id, TraceID: traceID}
	log := e.logger.With("session_id", id, "trace_id", traceID)
	log.Info("run.start", "input", logging.Preview(input, 200))
	finish := func(r Reply, runErr error) (Reply, error) {
		if runErr != nil {
			if saveErr := e.store.Save(sess); saveErr != nil {
				runErr = fmt.Errorf("%w（保存会话也失败: %v）", runErr, saveErr)
			}
			log.Error("run.end", "ok", false, "error", runErr.Error())
			return Reply{}, runErr
		}
		if err := e.store.Save(sess); err != nil {
			log.Error("run.end", "ok", false, "error", err.Error())
			return Reply{}, fmt.Errorf("保存会话失败: %w", err)
		}
		log.Info("run.end", "ok", true, "steps", r.Steps, "tool_calls", len(r.ToolCalls))
		return r, nil
	}

	system := buildSystemPrompt(now)
	emptyRetries := 0
	for step := 1; step <= e.maxSteps; step++ {
		before := memory.EstimateTokens(e.memory.Build(sess, system))
		compacted, compactErr := e.memory.MaybeCompact(ctx, sess, system)
		if compactErr != nil {
			return finish(reply, fmt.Errorf("压缩上下文失败: %w", compactErr))
		}
		if compacted {
			after := memory.EstimateTokens(e.memory.Build(sess, system))
			log.Info("context.compact",
				"step", step, "tokens_before", before, "tokens_after", after,
				"summary_up_to", sess.SummaryUpTo)
		}

		req := llm.ChatRequest{
			Model:       e.model,
			Messages:    e.memory.Build(sess, system),
			Temperature: e.temperature,
		}
		if step < e.maxSteps {
			req.Tools = e.tools.Definitions()
		} else if len(req.Messages) > 0 {
			req.Messages[0].Content += "\n\n这是最后一步：不要再请求工具，直接基于已有信息回答；如果信息不足，明确说明。"
		}
		log.Info("llm.request", "step", step, "messages", len(req.Messages), "tools", len(req.Tools))
		started := time.Now()
		resp, err := e.client.Chat(ctx, req)
		if err != nil {
			return finish(reply, fmt.Errorf("LLM 调用失败: %w", err))
		}
		log.Info("llm.response",
			"step", step,
			"duration_ms", time.Since(started).Milliseconds(),
			"finish_reason", resp.FinishReason,
			"request_id", resp.RequestID,
			"prompt_tokens", resp.Usage.PromptTokens,
			"completion_tokens", resp.Usage.CompletionTokens,
			"content", logging.Preview(resp.Content, 200))
		reply.Usage = resp.Usage
		reply.Steps = step

		decision, parseErr := Parse(resp)
		if parseErr != nil {
			if errors.Is(parseErr, ErrUnparsedToolMarkup) {
				log.Warn("llm.unparsed_tool_call", "step", step)
				if step == e.maxSteps {
					return finish(reply, fmt.Errorf("达到最大轮次 %d：%w", e.maxSteps, parseErr))
				}
				continue
			}
			if emptyRetries == 0 && step < e.maxSteps {
				emptyRetries++
				continue
			}
			return finish(reply, parseErr)
		}
		if len(decision.ToolCalls) == 0 {
			sess.Messages = append(sess.Messages, session.Message{
				Role:      llm.RoleAssistant,
				Content:   decision.Reply,
				Reasoning: decision.Thought,
				CreatedAt: time.Now(),
			})
			reply.Text = decision.Reply
			return finish(reply, nil)
		}
		if step == e.maxSteps {
			return finish(reply, errors.New("达到最大轮次，模型仍请求调用工具"))
		}

		sess.Messages = append(sess.Messages, session.Message{
			Role:      llm.RoleAssistant,
			Content:   decision.Thought,
			Reasoning: decision.Thought,
			ToolCalls: decision.ToolCalls,
			CreatedAt: time.Now(),
		})
		for _, call := range decision.ToolCalls {
			started := time.Now()
			log.Info("tool.call",
				"step", step, "tool_call_id", call.ID, "tool", call.Function.Name,
				"arguments", logging.Preview(call.Function.Arguments, 500))
			result := e.tools.Execute(ctx, tools.Call{
				Name:      call.Function.Name,
				SessionID: id,
				Arguments: json.RawMessage(call.Function.Arguments),
			})
			duration := time.Since(started).Milliseconds()
			log.Info("tool.result",
				"step", step, "tool_call_id", call.ID, "tool", call.Function.Name,
				"duration_ms", duration, "ok", !result.IsError,
				"result", logging.Preview(result.Content, 500))
			sess.Messages = append(sess.Messages, session.Message{
				Role:       llm.RoleTool,
				Content:    result.Content,
				ToolCallID: call.ID,
				CreatedAt:  time.Now(),
			})
			reply.ToolCalls = append(reply.ToolCalls, ToolCallRecord{
				ID: call.ID, Name: call.Function.Name,
				Arguments: call.Function.Arguments, Result: result.Content,
				IsError: result.IsError, DurationMS: duration,
			})
		}
	}
	return finish(reply, fmt.Errorf("达到最大轮次 %d", e.maxSteps))
}

// Compact forces the same compaction policy used by Run.
func (e *Engine) Compact(ctx context.Context, sessionID string) (bool, error) {
	id, err := session.ParseID(sessionID)
	if err != nil {
		return false, err
	}
	lock := e.lockFor(id)
	lock.Lock()
	defer lock.Unlock()
	sess, err := e.store.Load(id)
	if err != nil {
		return false, err
	}
	compacted, err := e.memory.MaybeCompact(ctx, sess, buildSystemPrompt(time.Now()))
	if err != nil {
		return false, err
	}
	if compacted {
		if err := e.store.Save(sess); err != nil {
			return false, err
		}
	}
	return compacted, nil
}

func (e *Engine) lockFor(id string) *sync.Mutex {
	value, _ := e.locks.LoadOrStore(id, &sync.Mutex{})
	return value.(*sync.Mutex)
}

func buildSystemPrompt(now time.Time) string {
	return fmt.Sprintf(`你是 Goblin，一个简洁、诚实的中文 CLI Agent。

当前时间：%s

规则：
1. 需要计算、检索资料、读取项目文档或管理待办时，优先调用工具，不要编造工具结果。
2. 调用工具前，用一句简短中文说明你要做什么。
3. 工具报错时先阅读错误；不要用完全相同的参数重复调用超过两次。
4. 最终回答基于工具结果；mock 数据要明确标注。
5. 回答保持简洁；没有工具需要时直接回答。`, now.Format("2006-01-02 15:04:05 -07:00"))
}
