package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ReadDocs struct {
	root     string
	realRoot string
	maxBytes int64
}

func NewReadDocs(root string, maxBytes int64) (*ReadDocs, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("read_docs root 不能为空")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve root: %w", err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve root symlink: %w", err)
	}
	if maxBytes <= 0 {
		maxBytes = 64 * 1024
	}
	return &ReadDocs{root: abs, realRoot: real, maxBytes: maxBytes}, nil
}

func (r *ReadDocs) Definition() Definition {
	return Definition{
		Name:        "read_docs",
		Description: "列出或读取项目文档。只允许读取 docs/ 目录和根目录 README.md，只读。",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "action": {"type": "string", "enum": ["list", "read"]},
    "path": {"type": "string", "description": "相对仓库根目录的路径，read 时必填，例如 docs/spec.md"}
  },
  "required": ["action"],
  "additionalProperties": false
}`),
	}
}

func (r *ReadDocs) Execute(_ context.Context, call Call) (Result, error) {
	var args struct {
		Action string `json:"action"`
		Path   string `json:"path"`
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		return Result{}, fmt.Errorf("参数解析失败: %w", err)
	}
	switch args.Action {
	case "list":
		return r.list()
	case "read":
		return r.read(args.Path)
	default:
		return Result{}, fmt.Errorf("action 必须是 list 或 read，收到 %q", args.Action)
	}
}

func (r *ReadDocs) list() (Result, error) {
	entries, err := os.ReadDir(filepath.Join(r.root, "docs"))
	if err != nil {
		return Result{}, fmt.Errorf("读取 docs 目录失败: %w", err)
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, "docs/"+entry.Name())
	}
	return Result{Content: JSONResult(map[string]any{"ok": true, "files": files})}, nil
}

func (r *ReadDocs) read(requested string) (Result, error) {
	rel, err := r.allowedPath(requested)
	if err != nil {
		return Result{}, err
	}
	target := filepath.Join(r.root, filepath.FromSlash(rel))
	realTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		return Result{}, fmt.Errorf("读取文件 %q 失败: %w", rel, err)
	}
	inside, err := filepath.Rel(r.realRoot, realTarget)
	if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return Result{}, fmt.Errorf("拒绝读取仓库外文件 %q", requested)
	}
	info, err := os.Stat(realTarget)
	if err != nil {
		return Result{}, fmt.Errorf("读取文件 %q 失败: %w", rel, err)
	}
	if info.IsDir() {
		return Result{}, fmt.Errorf("%q 是目录，不能直接读取", rel)
	}
	if info.Size() > r.maxBytes {
		return Result{}, fmt.Errorf("文件 %q 超过 %d 字节上限", rel, r.maxBytes)
	}
	data, err := os.ReadFile(realTarget)
	if err != nil {
		return Result{}, fmt.Errorf("读取文件 %q 失败: %w", rel, err)
	}
	return Result{Content: JSONResult(map[string]any{
		"ok":      true,
		"path":    rel,
		"content": string(data),
	})}, nil
}

func (r *ReadDocs) allowedPath(requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return "", errors.New("path 不能为空")
	}
	requested = strings.ReplaceAll(requested, `\`, "/")
	if filepath.IsAbs(requested) || filepath.VolumeName(requested) != "" {
		return "", errors.New("只允许相对路径")
	}
	clean := filepath.Clean(filepath.FromSlash(requested))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("路径不能包含目录穿越")
	}
	rel := filepath.ToSlash(clean)
	if rel != "README.md" {
		if !strings.HasPrefix(rel, "docs/") || !strings.HasSuffix(strings.ToLower(rel), ".md") {
			return "", errors.New("只允许读取 docs/ 下的 Markdown 文件和根目录 README.md")
		}
	}
	return rel, nil
}
