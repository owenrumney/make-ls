package resolver

import (
	"path/filepath"
	"strings"

	"github.com/owenrumney/make-ls/internal/model"
)

type evalContext struct {
	baseDir string
	mf      *model.Makefile
	line    int
	stack   map[string]bool
}

func newEvalContext(baseDir string, mf *model.Makefile, line int) *evalContext {
	return &evalContext{baseDir: baseDir, mf: mf, line: line, stack: map[string]bool{}}
}

func (e *evalContext) expandText(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '$' && i+1 < len(s) && (s[i+1] == '(' || s[i+1] == '{') {
			end, inner, ok := extractDelimited(s, i+1)
			if !ok {
				out.WriteByte(s[i])
				i++
				continue
			}
			out.WriteString(e.evalExpr(strings.TrimSpace(inner)))
			i = end + 1
			continue
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

func (e *evalContext) evalExpr(expr string) string {
	if expr == "" {
		return ""
	}
	name, rest, hasSpace := splitExprHead(expr)
	if hasSpace {
		switch name {
		case "abspath":
			arg := strings.TrimSpace(rest)
			if arg == "" {
				return ""
			}
			v := e.expandText(arg)
			if !filepath.IsAbs(v) {
				v = filepath.Join(e.baseDir, v)
			}
			v = filepath.Clean(v)
			if abs, err := filepath.Abs(v); err == nil {
				return abs
			}
			return v
		case "realpath":
			arg := strings.TrimSpace(rest)
			if arg == "" {
				return ""
			}
			v := e.expandText(arg)
			if !filepath.IsAbs(v) {
				v = filepath.Join(e.baseDir, v)
			}
			v = filepath.Clean(v)
			if resolved, err := filepath.EvalSymlinks(v); err == nil {
				return resolved
			}
			if abs, err := filepath.Abs(v); err == nil {
				return abs
			}
			return v
		case "addprefix":
			parts := splitArgs(rest)
			if len(parts) < 2 {
				return ""
			}
			prefix := e.expandText(parts[0])
			items := strings.Fields(e.expandText(parts[1]))
			out := make([]string, 0, len(items))
			for _, item := range items {
				out = append(out, prefix+item)
			}
			return strings.Join(out, " ")
		case "if":
			parts := splitArgs(rest)
			if len(parts) < 2 {
				return ""
			}
			if e.expandText(parts[0]) != "" {
				return e.expandText(parts[1])
			}
			if len(parts) > 2 {
				return e.expandText(parts[2])
			}
			return ""
		case "wildcard":
			arg := e.expandText(rest)
			matches, _ := filepath.Glob(filepath.Join(e.baseDir, arg))
			for i := range matches {
				matches[i] = filepath.Clean(matches[i])
			}
			return strings.Join(matches, " ")
		case "foreach":
			parts := splitArgs(rest)
			if len(parts) < 3 {
				return ""
			}
			varName := strings.TrimSpace(parts[0])
			list := strings.Fields(e.expandText(parts[1]))
			body := parts[2]
			var out []string
			for _, item := range list {
				expanded := strings.ReplaceAll(body, "$("+varName+")", item)
				expanded = strings.ReplaceAll(expanded, "${"+varName+"}", item)
				out = append(out, strings.TrimSpace(e.expandText(expanded)))
			}
			return strings.Join(filterEmpty(out), " ")
		}
	}
	return e.expandVar(name)
}

func (e *evalContext) expandVar(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || e.stack[name] {
		return ""
	}
	v, ok := e.lookupRaw(name)
	if !ok {
		return ""
	}
	e.stack[name] = true
	defer delete(e.stack, name)
	if v.Flavour == model.FlavourSimple {
		return e.expandText(v.Value)
	}
	return e.expandText(v.Value)
}

func (e *evalContext) lookupRaw(name string) (*model.Variable, bool) {
	for i := len(e.mf.Variables) - 1; i >= 0; i-- {
		v := e.mf.Variables[i]
		if v.Name != name {
			continue
		}
		if int(v.Range.Start.Line) > e.line {
			continue
		}
		return v, true
	}
	return nil, false
}

func extractDelimited(s string, start int) (int, string, bool) {
	stack := []byte{')'}
	if s[start] == '{' {
		stack[0] = '}'
	}
	for i := start + 1; i < len(s); i++ {
		if s[i] == '$' && i+1 < len(s) && (s[i+1] == '(' || s[i+1] == '{') {
			if s[i+1] == '(' {
				stack = append(stack, ')')
			} else {
				stack = append(stack, '}')
			}
			i++
			continue
		}
		if len(stack) > 0 && s[i] == stack[len(stack)-1] {
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				return i, s[start+1 : i], true
			}
		}
	}
	return 0, "", false
}

func splitExprHead(s string) (string, string, bool) {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '{':
			depth++
		case ')', '}':
			if depth > 0 {
				depth--
			}
		case ' ', '\t':
			if depth == 0 {
				return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:]), true
			}
		}
	}
	return strings.TrimSpace(s), "", false
}

func splitArgs(s string) []string {
	var parts []string
	start := 0
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '{':
			depth++
		case ')', '}':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				parts = append(parts, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	parts = append(parts, strings.TrimSpace(s[start:]))
	return parts
}

func filterEmpty(in []string) []string {
	out := in[:0]
	for _, s := range in {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
