package completion

import (
	"strings"

	"github.com/owenrumney/go-lsp/lsp"
	"github.com/owenrumney/make-ls/internal/model"
)

// Complete returns completion items for the given position in a Makefile.
func Complete(mf *model.Makefile, text string, pos lsp.Position) []lsp.CompletionItem {
	lines := strings.Split(text, "\n")
	if pos.Line >= len(lines) {
		return nil
	}
	line := lines[pos.Line]
	col := pos.Character
	if col > len(line) {
		col = len(line)
	}
	prefix := line[:col]

	ctx := classifyContext(prefix)

	switch ctx {
	case contextVarRef:
		return completeVarRef(mf, prefix)
	case contextRecipe:
		return completeAutoVars()
	case contextDeps:
		return completeTargets(mf)
	case contextLineStart:
		return completeDirectives()
	default:
		return nil
	}
}

type completionContext int

const (
	contextUnknown   completionContext = iota
	contextVarRef    // inside $( or ${
	contextRecipe    // in a recipe line
	contextDeps      // in a dependency list
	contextLineStart // at the start of a line
)

func classifyContext(prefix string) completionContext {
	trimmed := strings.TrimSpace(prefix)

	// Inside a variable reference — look for unclosed $( or ${.
	if inVarRef(prefix) {
		return contextVarRef
	}

	// Recipe line (tab-prefixed).
	if len(prefix) > 0 && prefix[0] == '\t' {
		return contextRecipe
	}

	// Check if we're in a dep list: line contains ":" before cursor and no "=" before ":".
	if colonIdx := strings.Index(prefix, ":"); colonIdx >= 0 {
		beforeColon := prefix[:colonIdx]
		if !strings.ContainsAny(beforeColon, "=") {
			return contextDeps
		}
	}

	// Line start (empty or just started typing).
	if trimmed == "" || !strings.ContainsAny(trimmed, ":=\t") {
		return contextLineStart
	}

	return contextUnknown
}

func inVarRef(prefix string) bool {
	// Walk backwards to find unclosed $( or ${.
	depth := 0
	for i := len(prefix) - 1; i >= 0; i-- {
		switch prefix[i] {
		case ')', '}':
			depth++
		case '(':
			if i > 0 && prefix[i-1] == '$' {
				if depth == 0 {
					return true
				}
				depth--
			}
		case '{':
			if i > 0 && prefix[i-1] == '$' {
				if depth == 0 {
					return true
				}
				depth--
			}
		}
	}
	return false
}

func completeVarRef(mf *model.Makefile, prefix string) []lsp.CompletionItem {
	var items []lsp.CompletionItem

	// Extract partial name typed so far after $( or ${.
	partial := varRefPartial(prefix)

	// User-defined variables.
	seen := map[string]bool{}
	for _, v := range mf.Variables {
		if seen[v.Name] {
			continue
		}
		seen[v.Name] = true
		if !matchesPrefix(v.Name, partial) {
			continue
		}
		kind := lsp.CompletionItemKindVariable
		items = append(items, lsp.CompletionItem{
			Label:  v.Name,
			Kind:   &kind,
			Detail: string(v.Op) + " " + v.Value,
		})
	}

	// Define variables.
	for _, d := range mf.Defines {
		if seen[d.Name] {
			continue
		}
		seen[d.Name] = true
		if !matchesPrefix(d.Name, partial) {
			continue
		}
		kind := lsp.CompletionItemKindVariable
		items = append(items, lsp.CompletionItem{
			Label:  d.Name,
			Kind:   &kind,
			Detail: "define",
		})
	}

	// Built-in variables.
	for _, bv := range BuiltinVars {
		if seen[bv.Name] {
			continue
		}
		seen[bv.Name] = true
		if !matchesPrefix(bv.Name, partial) {
			continue
		}
		kind := lsp.CompletionItemKindConstant
		items = append(items, lsp.CompletionItem{
			Label:         bv.Name,
			Kind:          &kind,
			Detail:        bv.Doc,
			Documentation: &lsp.MarkupContent{Kind: "plaintext", Value: bv.Doc},
		})
	}

	// Functions.
	for _, fn := range Functions {
		if seen[fn.Name] {
			continue
		}
		seen[fn.Name] = true
		if !matchesPrefix(fn.Name, partial) {
			continue
		}
		kind := lsp.CompletionItemKindFunction
		items = append(items, lsp.CompletionItem{
			Label:         fn.Name,
			Kind:          &kind,
			Detail:        fn.Args,
			Documentation: &lsp.MarkupContent{Kind: "plaintext", Value: fn.Doc},
		})
	}

	return items
}

func completeAutoVars() []lsp.CompletionItem {
	items := make([]lsp.CompletionItem, 0, len(AutoVars))
	for _, av := range AutoVars {
		kind := lsp.CompletionItemKindVariable
		// Present as $@ etc for auto vars.
		label := "$" + av.Name
		if len(av.Name) > 1 {
			label = "$(" + av.Name + ")"
		}
		items = append(items, lsp.CompletionItem{
			Label:         label,
			Kind:          &kind,
			Detail:        av.Doc,
			Documentation: &lsp.MarkupContent{Kind: "plaintext", Value: av.Doc},
		})
	}
	return items
}

func completeTargets(mf *model.Makefile) []lsp.CompletionItem {
	var items []lsp.CompletionItem
	seen := map[string]bool{}
	for _, t := range mf.Targets {
		if t.IsPattern || seen[t.Name] {
			continue
		}
		seen[t.Name] = true
		kind := lsp.CompletionItemKindValue
		detail := ""
		if t.DocComment != "" {
			detail = t.DocComment
		}
		items = append(items, lsp.CompletionItem{
			Label:  t.Name,
			Kind:   &kind,
			Detail: detail,
		})
	}
	return items
}

func completeDirectives() []lsp.CompletionItem {
	directives := []struct {
		label  string
		detail string
	}{
		{"include", "Include another makefile"},
		{"-include", "Include another makefile (no error if missing)"},
		{".PHONY:", "Declare phony targets"},
		{"ifeq", "Conditional: equal"},
		{"ifneq", "Conditional: not equal"},
		{"ifdef", "Conditional: defined"},
		{"ifndef", "Conditional: not defined"},
		{"define", "Multi-line variable definition"},
		{"export", "Export variable to sub-makes"},
		{"unexport", "Unexport variable"},
		{"override", "Override command-line variable"},
		{"vpath", "Search path for prerequisites"},
	}
	items := make([]lsp.CompletionItem, 0, len(directives))
	for _, d := range directives {
		kind := lsp.CompletionItemKindKeyword
		items = append(items, lsp.CompletionItem{
			Label:  d.label,
			Kind:   &kind,
			Detail: d.detail,
		})
	}
	return items
}

func varRefPartial(prefix string) string {
	// Find the last unclosed $( or ${.
	for i := len(prefix) - 1; i >= 1; i-- {
		if (prefix[i] == '(' || prefix[i] == '{') && prefix[i-1] == '$' {
			return prefix[i+1:]
		}
	}
	return ""
}

func matchesPrefix(name, partial string) bool {
	if partial == "" {
		return true
	}
	return strings.HasPrefix(strings.ToUpper(name), strings.ToUpper(partial))
}
