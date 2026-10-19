package cli

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/Goblin-Master/goblin/internal/agent"
	"github.com/Goblin-Master/goblin/internal/config"
	"github.com/Goblin-Master/goblin/internal/db"
	"github.com/Goblin-Master/goblin/internal/llm"
	"github.com/Goblin-Master/goblin/internal/memory"
	"github.com/Goblin-Master/goblin/internal/session"
	"github.com/Goblin-Master/goblin/internal/tools"
	"github.com/Goblin-Master/goblin/internal/trace"
	"github.com/spf13/cobra"
)

// Main builds the Cobra command tree, executes one invocation, and returns an
// exit code. stdout/stderr are injected so command behavior is testable.
func Main(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	root := newRootCommand(stdout, stderr)
	root.SetArgs(args)
	root.SetContext(ctx)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(stderr, "错误:", err)
		return 1
	}
	return 0
}

type rootOptions struct {
	configPath string
	envPath    string
}

func newRootCommand(stdout, stderr io.Writer) *cobra.Command {
	opts := &rootOptions{}
	root := &cobra.Command{
		Use:   "goblin",
		Short: "从零实现的最小可用 CLI Agent",
		Long: `Goblin 是一个不依赖 Agent 框架的 CLI Agent。

它使用 DeepSeek 原生 tool_calls 完成工具决策，使用 MySQL 持久化
Session、消息、摘要与待办，并在上下文超预算时自动压缩。`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.PersistentFlags().StringVar(&opts.configPath, "config", "", "配置文件路径，默认 config.yaml")
	root.PersistentFlags().StringVar(&opts.envPath, "env-file", "", "环境文件路径，默认 .env")

	root.AddCommand(
		newChatCommand(opts, stdout),
		newRunCommand(opts, stdout),
		newSessionsCommand(opts, stdout),
	)
	return root
}

func newChatCommand(opts *rootOptions, stdout io.Writer) *cobra.Command {
	var sessionID string
	var newSession bool
	var verbose bool
	cmd := &cobra.Command{
		Use:   "chat",
		Short: "新建或恢复 Session，进入交互模式",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runChat(cmd.Context(), opts, sessionID, newSession, verbose, stdout)
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "恢复已存在的 UUID 会话")
	cmd.Flags().BoolVar(&newSession, "new", false, "强制新建会话")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "打印工具调用摘要")
	return cmd
}

func newRunCommand(opts *rootOptions, stdout io.Writer) *cobra.Command {
	var sessionID string
	var verbose bool
	cmd := &cobra.Command{
		Use:   "run --session <uuid> <问题>",
		Short: "在已存在的 Session 中执行一次输入",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runOnce(cmd.Context(), opts, sessionID, strings.Join(args, " "), verbose, stdout)
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "已存在的 UUID 会话")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "打印工具调用摘要")
	return cmd
}

func newSessionsCommand(opts *rootOptions, stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "sessions",
		Short: "列出所有 Session",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runSessions(opts, stdout)
		},
	}
}

type appOptions struct {
	configPath string
	envPath    string
	verbose    bool
}

type app struct {
	cfg     config.Config
	conn    *sql.DB
	store   *session.MySQLStore
	engine  *agent.Engine
	tracer  *trace.JSONL
	logFile *os.File
	verbose bool
}

func newApp(opts appOptions) (*app, error) {
	cfg, err := config.Load(config.LoadOptions{ConfigPath: opts.configPath, EnvPath: opts.envPath})
	if err != nil {
		return nil, err
	}
	conn, err := db.Open(context.Background(), cfg.MySQL.DSN)
	if err != nil {
		return nil, err
	}
	cleanup := func() { _ = conn.Close() }

	store, err := session.NewMySQLStore(conn)
	if err != nil {
		cleanup()
		return nil, err
	}
	todoStore, err := tools.NewMySQLTodoStore(conn)
	if err != nil {
		cleanup()
		return nil, err
	}
	root, err := os.Getwd()
	if err != nil {
		cleanup()
		return nil, err
	}
	readDocs, err := tools.NewReadDocs(root, 64*1024)
	if err != nil {
		cleanup()
		return nil, err
	}
	registry, err := tools.NewRegistryWithTimeout(cfg.Agent.ToolTimeout,
		tools.NewCalculator(), tools.NewSearch(), readDocs, tools.NewTodo(todoStore))
	if err != nil {
		cleanup()
		return nil, err
	}
	client, err := llm.NewDeepSeek(llm.Options{
		BaseURL: cfg.LLM.BaseURL, APIKey: cfg.LLM.APIKey, Model: cfg.LLM.Model,
		Timeout: cfg.LLM.Timeout, MaxRetries: cfg.LLM.MaxRetries, Temperature: cfg.LLM.Temperature,
	})
	if err != nil {
		cleanup()
		return nil, err
	}
	memoryManager, err := memory.New(memory.Options{
		Client: client, Model: cfg.LLM.Model,
		MaxContextTokens: cfg.Agent.MaxContextTokens, ReserveOutputTokens: cfg.Agent.ReserveOutputToken,
		KeepRecent: cfg.Agent.KeepRecent, SummaryMaxTokens: cfg.Agent.SummaryMaxTokens,
		CompactRatio: cfg.Agent.CompactRatio,
	})
	if err != nil {
		cleanup()
		return nil, err
	}
	tracer, err := trace.NewJSONL(cfg.Logging.Dir, "trace.jsonl")
	if err != nil {
		cleanup()
		return nil, err
	}
	logFile, err := openLog(cfg.Logging)
	if err != nil {
		_ = tracer.Close()
		cleanup()
		return nil, err
	}
	engine, err := agent.New(agent.Options{
		Client: client, Store: store, Tools: registry, Memory: memoryManager,
		Tracer: tracer, Model: cfg.LLM.Model, MaxSteps: cfg.Agent.MaxSteps,
		Temperature: cfg.LLM.Temperature,
	})
	if err != nil {
		_ = logFile.Close()
		_ = tracer.Close()
		cleanup()
		return nil, err
	}
	return &app{
		cfg: cfg, conn: conn, store: store, engine: engine,
		tracer: tracer, logFile: logFile, verbose: opts.verbose,
	}, nil
}

func (a *app) Close() {
	if a == nil {
		return
	}
	if a.tracer != nil {
		_ = a.tracer.Close()
	}
	if a.logFile != nil {
		_ = a.logFile.Close()
	}
	if a.conn != nil {
		_ = a.conn.Close()
	}
}

func openLog(cfg config.LoggingConfig) (*os.File, error) {
	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(cfg.Dir, "goblin.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	level := slog.LevelInfo
	if cfg.Level == "debug" {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(f, &slog.HandlerOptions{Level: level})))
	return f, nil
}

func runChat(ctx context.Context, opts *rootOptions, sessionID string, newSession, verbose bool, stdout io.Writer) error {
	if sessionID != "" && newSession {
		return errors.New("--session 与 --new 不能同时使用")
	}
	a, err := newApp(appOptions{configPath: opts.configPath, envPath: opts.envPath, verbose: verbose})
	if err != nil {
		return err
	}
	defer a.Close()

	var id string
	if sessionID != "" {
		id, err = session.ParseID(sessionID)
		if err != nil {
			return err
		}
		if _, err := a.store.Load(id); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("会话 %s 不存在；用 goblin sessions 查看，或运行 goblin chat 新建", id)
			}
			return err
		}
	} else {
		id, err = a.createSession()
		if err != nil {
			return err
		}
	}
	return a.repl(ctx, id, stdout)
}

func (a *app) createSession() (string, error) {
	id := session.NewID()
	if _, err := a.store.Create(id); err != nil {
		return "", err
	}
	return id, nil
}

func (a *app) repl(ctx context.Context, initialID string, out io.Writer) error {
	id := initialID
	fmt.Fprintf(out, "Goblin | model=%s | tools=%s\n", a.cfg.LLM.Model, strings.Join(a.toolsNames(), ", "))
	fmt.Fprintf(out, "Session: %s\n输入 /help 查看命令，/quit 退出。\n", id)
	reader := bufio.NewReader(os.Stdin)
	var last agent.Reply
	for {
		fmt.Fprint(out, "\n你> ")
		line, err := reader.ReadString('\n')
		if err != nil && len(line) == 0 {
			if errors.Is(err, io.EOF) {
				fmt.Fprintln(out)
				return nil
			}
			return err
		}
		input := strings.TrimSpace(line)
		if input == "" {
			continue
		}
		if strings.HasPrefix(input, "/") {
			quit, newID, err := a.command(ctx, id, input, out, last)
			if err != nil {
				fmt.Fprintln(out, "错误:", err)
				continue
			}
			if quit {
				return nil
			}
			if newID != "" {
				id = newID
				last = agent.Reply{}
			}
			continue
		}
		reply, err := a.engine.Run(ctx, id, input)
		if err != nil {
			fmt.Fprintln(out, "错误:", err)
			continue
		}
		last = reply
		fmt.Fprintf(out, "\nAgent> %s\n", reply.Text)
		if a.verbose {
			a.printTools(out, reply.ToolCalls)
		}
	}
}

func (a *app) toolsNames() []string {
	return []string{"calculator", "search", "read_docs", "todo"}
}

func (a *app) printTools(out io.Writer, calls []agent.ToolCallRecord) {
	for _, call := range calls {
		status := "ok"
		if call.IsError {
			status = "error"
		}
		fmt.Fprintf(out, "  → %s(%s) %dms %s\n", call.Name, trace.Preview(call.Arguments, 80), call.DurationMS, status)
	}
}

func (a *app) command(ctx context.Context, id, input string, out io.Writer, last agent.Reply) (bool, string, error) {
	fields := strings.Fields(input)
	switch fields[0] {
	case "/quit", "/exit":
		return true, "", nil
	case "/help":
		fmt.Fprintln(out, "/help  /new  /sessions  /switch <uuid>  /history [n]  /compact  /trace  /quit")
	case "/new":
		newID, err := a.createSession()
		if err != nil {
			return false, "", err
		}
		fmt.Fprintf(out, "已切换新会话: %s\n", newID)
		return false, newID, nil
	case "/sessions":
		return false, "", a.listSessions(out)
	case "/switch":
		if len(fields) != 2 {
			return false, "", errors.New("用法: /switch <uuid>")
		}
		newID, err := session.ParseID(fields[1])
		if err != nil {
			return false, "", err
		}
		if _, err := a.store.Load(newID); err != nil {
			return false, "", err
		}
		fmt.Fprintf(out, "已切换会话: %s\n", newID)
		return false, newID, nil
	case "/history":
		n := 10
		if len(fields) == 2 {
			_, _ = fmt.Sscanf(fields[1], "%d", &n)
		}
		sess, err := a.store.Load(id)
		if err != nil {
			return false, "", err
		}
		start := len(sess.Messages) - n
		if start < 0 {
			start = 0
		}
		for _, msg := range sess.Messages[start:] {
			fmt.Fprintf(out, "[%s] %s\n", msg.Role, trace.Preview(msg.Content, 300))
		}
	case "/compact":
		compacted, err := a.engine.Compact(ctx, id)
		if err != nil {
			return false, "", err
		}
		if compacted {
			fmt.Fprintln(out, "已压缩上下文。")
		} else {
			fmt.Fprintln(out, "当前上下文不需要压缩。")
		}
	case "/trace":
		a.printTools(out, last.ToolCalls)
		if len(last.ToolCalls) == 0 {
			fmt.Fprintln(out, "上次运行没有工具调用。")
		}
	default:
		return false, "", fmt.Errorf("未知命令 %s", fields[0])
	}
	return false, "", nil
}

func (a *app) listSessions(out io.Writer) error {
	metas, err := a.store.List()
	if err != nil {
		return err
	}
	if len(metas) == 0 {
		fmt.Fprintln(out, "暂无会话。")
		return nil
	}
	for _, meta := range metas {
		fmt.Fprintf(out, "%s  %s  msgs=%d  %s\n",
			meta.ID, meta.UpdatedAt.Format("2006-01-02 15:04"), meta.Messages, meta.Title)
	}
	return nil
}

func runOnce(ctx context.Context, opts *rootOptions, sessionID, input string, verbose bool, stdout io.Writer) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("run 需要 --session <uuid>")
	}
	if strings.TrimSpace(input) == "" {
		return errors.New("run 需要一个问题")
	}
	a, err := newApp(appOptions{configPath: opts.configPath, envPath: opts.envPath, verbose: verbose})
	if err != nil {
		return err
	}
	defer a.Close()
	reply, err := a.engine.Run(ctx, sessionID, input)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, reply.Text)
	if verbose {
		a.printTools(stdout, reply.ToolCalls)
	}
	return nil
}

func runSessions(opts *rootOptions, stdout io.Writer) error {
	a, err := newApp(appOptions{configPath: opts.configPath, envPath: opts.envPath})
	if err != nil {
		return err
	}
	defer a.Close()
	return a.listSessions(stdout)
}
