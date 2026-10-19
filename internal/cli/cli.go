package cli

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/Goblin-Master/goblin/internal/agent"
	"github.com/Goblin-Master/goblin/internal/config"
	"github.com/Goblin-Master/goblin/internal/contextmgr"
	"github.com/Goblin-Master/goblin/internal/db"
	"github.com/Goblin-Master/goblin/internal/llm"
	"github.com/Goblin-Master/goblin/internal/session"
	"github.com/Goblin-Master/goblin/internal/tools"
	"github.com/Goblin-Master/goblin/internal/trace"
)

// Main dispatches one CLI invocation and returns a process exit code.
func Main(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stdout)
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var err error
	switch args[0] {
	case "chat":
		err = runChat(ctx, args[1:], stdout, stderr)
	case "run":
		err = runOnce(ctx, args[1:], stdout, stderr)
	case "sessions":
		err = runSessions(args[1:], stdout)
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0
	default:
		err = fmt.Errorf("未知命令 %q（可用：chat / run / sessions / help）", args[0])
	}
	if err != nil {
		fmt.Fprintln(stderr, "错误:", err)
		return 1
	}
	return 0
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, `Goblin - 最小可用 CLI Agent

用法:
  goblin chat [--session UUID] [--new] [--verbose]
  goblin run --session UUID "问题"
  goblin sessions
  goblin help

通用选项:
  --config PATH       配置文件，默认 config.yaml
  --env-file PATH     环境文件，默认 .env
  --verbose           打印工具调用摘要`)
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
	ctxmgr  *contextmgr.Manager
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
	ctxmgr, err := contextmgr.New(contextmgr.Options{
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
		Client: client, Store: store, Tools: registry, Context: ctxmgr,
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
		ctxmgr: ctxmgr, tracer: tracer, logFile: logFile, verbose: opts.verbose,
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

func addCommonFlags(fs *flag.FlagSet) (*string, *string) {
	configPath := fs.String("config", "", "配置文件路径")
	envPath := fs.String("env-file", "", "环境文件路径")
	return configPath, envPath
}

func runChat(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("chat", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath, envPath := addCommonFlags(fs)
	sessionID := fs.String("session", "", "恢复已存在的 UUID 会话")
	newSession := fs.Bool("new", false, "强制新建会话")
	verbose := fs.Bool("verbose", false, "打印工具调用摘要")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sessionID != "" && *newSession {
		return errors.New("--session 与 --new 不能同时使用")
	}
	a, err := newApp(appOptions{configPath: *configPath, envPath: *envPath, verbose: *verbose})
	if err != nil {
		return err
	}
	defer a.Close()

	var id string
	if *sessionID != "" {
		id, err = session.ParseID(*sessionID)
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

func runOnce(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath, envPath := addCommonFlags(fs)
	sessionID := fs.String("session", "", "已存在的 UUID 会话")
	verbose := fs.Bool("verbose", false, "打印工具调用摘要")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sessionID == "" {
		return errors.New("run 需要 --session <uuid>")
	}
	input := strings.Join(fs.Args(), " ")
	if strings.TrimSpace(input) == "" {
		return errors.New("run 需要一个问题")
	}
	a, err := newApp(appOptions{configPath: *configPath, envPath: *envPath, verbose: *verbose})
	if err != nil {
		return err
	}
	defer a.Close()
	reply, err := a.engine.Run(ctx, *sessionID, input)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, reply.Text)
	if *verbose {
		a.printTools(stdout, reply.ToolCalls)
	}
	return nil
}

func runSessions(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("sessions", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath, envPath := addCommonFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	a, err := newApp(appOptions{configPath: *configPath, envPath: *envPath})
	if err != nil {
		return err
	}
	defer a.Close()
	return a.listSessions(stdout)
}
