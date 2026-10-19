package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

type Calculator struct{}

func NewCalculator() *Calculator { return &Calculator{} }

func (c *Calculator) Definition() Definition {
	return Definition{
		Name:        "calculator",
		Description: "计算算术表达式，支持 + - * / % ^、括号和小数。不要用它做非算术推理。",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "expression": {"type": "string", "description": "要计算的算术表达式，例如 (12+3)*4"}
  },
  "required": ["expression"],
  "additionalProperties": false
}`),
	}
}

func (c *Calculator) Execute(_ context.Context, call Call) (Result, error) {
	var args struct {
		Expression string `json:"expression"`
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		return Result{}, fmt.Errorf("参数解析失败: %w", err)
	}
	args.Expression = strings.TrimSpace(args.Expression)
	if args.Expression == "" {
		return Result{}, errors.New("expression 不能为空")
	}
	value, err := EvalExpression(args.Expression)
	if err != nil {
		return Result{}, err
	}
	return Result{Content: JSONResult(map[string]any{
		"ok":         true,
		"expression": args.Expression,
		"result":     value,
	})}, nil
}

// EvalExpression evaluates a small arithmetic grammar. It never uses eval.
func EvalExpression(input string) (float64, error) {
	tokens, err := tokenize(input)
	if err != nil {
		return 0, err
	}
	p := &exprParser{tokens: tokens}
	value, err := p.parseExpression()
	if err != nil {
		return 0, err
	}
	if p.pos != len(p.tokens) {
		return 0, fmt.Errorf("表达式末尾有多余内容: %q", tokenText(p.tokens[p.pos]))
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, errors.New("计算结果不是有限数值")
	}
	return value, nil
}

type tokenKind int

const (
	tokenNumber tokenKind = iota
	tokenPlus
	tokenMinus
	tokenStar
	tokenSlash
	tokenPercent
	tokenCaret
	tokenLParen
	tokenRParen
)

type token struct {
	kind  tokenKind
	value float64
	text  string
}

func tokenText(t token) string {
	if t.kind == tokenNumber {
		return strconv.FormatFloat(t.value, 'g', -1, 64)
	}
	return t.text
}

func tokenize(s string) ([]token, error) {
	var tokens []token
	runes := []rune(s)
	for i := 0; i < len(runes); {
		r := runes[i]
		if unicode.IsSpace(r) {
			i++
			continue
		}
		if unicode.IsDigit(r) || r == '.' {
			start := i
			dot := false
			for i < len(runes) && (unicode.IsDigit(runes[i]) || runes[i] == '.') {
				if runes[i] == '.' {
					if dot {
						return nil, fmt.Errorf("非法数字：%q", string(runes[start:i+1]))
					}
					dot = true
				}
				i++
			}
			text := string(runes[start:i])
			value, err := strconv.ParseFloat(text, 64)
			if err != nil {
				return nil, fmt.Errorf("非法数字 %q", text)
			}
			tokens = append(tokens, token{kind: tokenNumber, value: value, text: text})
			continue
		}
		kind := tokenKind(-1)
		switch r {
		case '+':
			kind = tokenPlus
		case '-':
			kind = tokenMinus
		case '*':
			kind = tokenStar
		case '/':
			kind = tokenSlash
		case '%':
			kind = tokenPercent
		case '^':
			kind = tokenCaret
		case '(':
			kind = tokenLParen
		case ')':
			kind = tokenRParen
		default:
			return nil, fmt.Errorf("不支持的字符 %q", string(r))
		}
		tokens = append(tokens, token{kind: kind, text: string(r)})
		i++
	}
	if len(tokens) == 0 {
		return nil, errors.New("表达式为空")
	}
	return tokens, nil
}

type exprParser struct {
	tokens []token
	pos    int
}

func (p *exprParser) peek() (token, bool) {
	if p.pos >= len(p.tokens) {
		return token{}, false
	}
	return p.tokens[p.pos], true
}

func (p *exprParser) parseExpression() (float64, error) {
	left, err := p.parseTerm()
	if err != nil {
		return 0, err
	}
	for {
		t, ok := p.peek()
		if !ok || (t.kind != tokenPlus && t.kind != tokenMinus) {
			return left, nil
		}
		p.pos++
		right, err := p.parseTerm()
		if err != nil {
			return 0, err
		}
		if t.kind == tokenPlus {
			left += right
		} else {
			left -= right
		}
	}
}

func (p *exprParser) parseTerm() (float64, error) {
	left, err := p.parseUnary()
	if err != nil {
		return 0, err
	}
	for {
		t, ok := p.peek()
		if !ok || (t.kind != tokenStar && t.kind != tokenSlash && t.kind != tokenPercent) {
			return left, nil
		}
		p.pos++
		right, err := p.parseUnary()
		if err != nil {
			return 0, err
		}
		switch t.kind {
		case tokenStar:
			left *= right
		case tokenSlash:
			if right == 0 {
				return 0, errors.New("除数不能为 0")
			}
			left /= right
		case tokenPercent:
			if right == 0 {
				return 0, errors.New("取模的除数不能为 0")
			}
			left = math.Mod(left, right)
		}
	}
}

func (p *exprParser) parseUnary() (float64, error) {
	t, ok := p.peek()
	if ok && (t.kind == tokenPlus || t.kind == tokenMinus) {
		p.pos++
		value, err := p.parseUnary()
		if err != nil {
			return 0, err
		}
		if t.kind == tokenMinus {
			return -value, nil
		}
		return value, nil
	}
	return p.parsePower()
}

func (p *exprParser) parsePower() (float64, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return 0, err
	}
	t, ok := p.peek()
	if !ok || t.kind != tokenCaret {
		return left, nil
	}
	p.pos++
	right, err := p.parseUnary() // right-associative, allows 2^-3
	if err != nil {
		return 0, err
	}
	return math.Pow(left, right), nil
}

func (p *exprParser) parsePrimary() (float64, error) {
	t, ok := p.peek()
	if !ok {
		return 0, errors.New("表达式不完整")
	}
	switch t.kind {
	case tokenNumber:
		p.pos++
		return t.value, nil
	case tokenLParen:
		p.pos++
		value, err := p.parseExpression()
		if err != nil {
			return 0, err
		}
		closing, ok := p.peek()
		if !ok || closing.kind != tokenRParen {
			return 0, errors.New("缺少右括号")
		}
		p.pos++
		return value, nil
	default:
		return 0, fmt.Errorf("位置 %d 缺少数字或左括号", p.pos+1)
	}
}
