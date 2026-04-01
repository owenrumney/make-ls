package handler

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/owenrumney/go-lsp/lsp"
	"github.com/owenrumney/go-lsp/server"
	"github.com/owenrumney/make-ls/internal/analysis"
	"github.com/owenrumney/make-ls/internal/completion"
	"github.com/owenrumney/make-ls/internal/model"
	"github.com/owenrumney/make-ls/internal/parser"
	"github.com/owenrumney/make-ls/internal/resolver"
)

// Handler implements the LSP handler interfaces for Makefiles.
type Handler struct {
	client *server.Client
	mu     sync.Mutex
	docs   map[lsp.DocumentURI]string
	parsed map[lsp.DocumentURI]*model.Makefile
}

// New creates a new Handler.
func New() *Handler {
	return &Handler{
		docs:   make(map[lsp.DocumentURI]string),
		parsed: make(map[lsp.DocumentURI]*model.Makefile),
	}
}

func boolPtr(b bool) *bool { return &b }

// Initialize handles the initialize request.
func (h *Handler) Initialize(_ context.Context, _ *lsp.InitializeParams) (*lsp.InitializeResult, error) {
	return &lsp.InitializeResult{
		Capabilities: lsp.ServerCapabilities{
			TextDocumentSync: &lsp.TextDocumentSyncOptions{
				OpenClose: boolPtr(true),
				Change:    lsp.SyncFull,
				Save:      &lsp.SaveOptions{IncludeText: boolPtr(false)},
			},
			HoverProvider:          boolPtr(true),
			DocumentSymbolProvider: boolPtr(true),
			CompletionProvider: &lsp.CompletionOptions{
				TriggerCharacters: []string{"$", "("},
			},
			DefinitionProvider:   boolPtr(true),
			ReferencesProvider:   boolPtr(true),
			CodeActionProvider: &lsp.CodeActionOptions{
				CodeActionKinds: []lsp.CodeActionKind{lsp.CodeActionQuickFix},
			},
			DocumentFormattingProvider: boolPtr(true),
		},
		ServerInfo: &lsp.ServerInfo{
			Name:    "make-ls",
			Version: "0.1.0",
		},
	}, nil
}

// Shutdown handles the shutdown request.
func (h *Handler) Shutdown(_ context.Context) error {
	return nil
}

// SetClient stores the client for sending notifications.
func (h *Handler) SetClient(client *server.Client) {
	h.client = client
}

// DidOpen handles textDocument/didOpen.
func (h *Handler) DidOpen(_ context.Context, params *lsp.DidOpenTextDocumentParams) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	uri := params.TextDocument.URI
	text := params.TextDocument.Text
	h.docs[uri] = text
	h.parsed[uri] = h.parseAndResolve(uri, text)
	return nil
}

// DidChange handles textDocument/didChange.
func (h *Handler) DidChange(_ context.Context, params *lsp.DidChangeTextDocumentParams) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	uri := params.TextDocument.URI
	if len(params.ContentChanges) > 0 {
		text := params.ContentChanges[len(params.ContentChanges)-1].Text
		h.docs[uri] = text
		h.parsed[uri] = h.parseAndResolve(uri, text)
	}
	return nil
}

// DidClose handles textDocument/didClose.
func (h *Handler) DidClose(_ context.Context, params *lsp.DidCloseTextDocumentParams) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	uri := params.TextDocument.URI
	delete(h.docs, uri)
	delete(h.parsed, uri)
	return nil
}

// DidSave handles textDocument/didSave — runs diagnostics and publishes them.
func (h *Handler) DidSave(ctx context.Context, params *lsp.DidSaveTextDocumentParams) error {
	h.mu.Lock()
	uri := params.TextDocument.URI
	mf := h.parsed[uri]
	h.mu.Unlock()

	if mf == nil || h.client == nil {
		return nil
	}

	diags := analysis.Diagnose(mf)
	if diags == nil {
		diags = []lsp.Diagnostic{}
	}
	return h.client.PublishDiagnostics(ctx, &lsp.PublishDiagnosticsParams{
		URI:         uri,
		Diagnostics: diags,
	})
}

// Hover handles textDocument/hover.
func (h *Handler) Hover(_ context.Context, params *lsp.HoverParams) (*lsp.Hover, error) {
	h.mu.Lock()
	mf := h.parsed[params.TextDocument.URI]
	text := h.docs[params.TextDocument.URI]
	h.mu.Unlock()

	if mf == nil {
		return nil, nil
	}

	pos := params.Position

	// Check targets.
	for _, t := range mf.Targets {
		if inRange(pos, t.NameRange) {
			return targetHover(t), nil
		}
		// Check deps.
		for _, dep := range t.Deps {
			if inRange(pos, dep.Range) {
				if target := findTarget(mf, dep.Name); target != nil {
					return targetHover(target), nil
				}
			}
		}
	}

	// Check variable references in the text at cursor position.
	if varName := varRefAtPosition(text, pos); varName != "" {
		// Auto variable.
		for _, av := range completion.AutoVars {
			if av.Name == varName {
				return &lsp.Hover{
					Contents: lsp.MarkupContent{
						Kind:  "markdown",
						Value: fmt.Sprintf("**`$%s`** — %s", av.Name, av.Doc),
					},
				}, nil
			}
		}
		// Function.
		for _, fn := range completion.Functions {
			if fn.Name == varName {
				return &lsp.Hover{
					Contents: lsp.MarkupContent{
						Kind:  "markdown",
						Value: fmt.Sprintf("**`%s`**\n\n%s", fn.Args, fn.Doc),
					},
				}, nil
			}
		}
		// Builtin variable.
		for _, bv := range completion.BuiltinVars {
			if bv.Name == varName {
				return &lsp.Hover{
					Contents: lsp.MarkupContent{
						Kind:  "markdown",
						Value: fmt.Sprintf("**`%s`** — %s", bv.Name, bv.Doc),
					},
				}, nil
			}
		}
		// User-defined variable.
		for _, v := range mf.Variables {
			if v.Name == varName {
				detail := fmt.Sprintf("**`%s`** `%s` `%s`", v.Name, v.Op, v.Value)
				if v.TargetScope != "" {
					detail += fmt.Sprintf("\n\n*Target-specific for* `%s`", v.TargetScope)
				}
				return &lsp.Hover{
					Contents: lsp.MarkupContent{
						Kind:  "markdown",
						Value: detail,
					},
				}, nil
			}
		}
	}

	// Check variables at cursor.
	for _, v := range mf.Variables {
		if inRange(pos, v.NameRange) {
			detail := fmt.Sprintf("**`%s`** `%s` `%s`", v.Name, v.Op, v.Value)
			if v.TargetScope != "" {
				detail += fmt.Sprintf("\n\n*Target-specific for* `%s`", v.TargetScope)
			}
			return &lsp.Hover{
				Contents: lsp.MarkupContent{
					Kind:  "markdown",
					Value: detail,
				},
			}, nil
		}
	}

	return nil, nil
}

// DocumentSymbol handles textDocument/documentSymbol.
func (h *Handler) DocumentSymbol(_ context.Context, params *lsp.DocumentSymbolParams) ([]lsp.DocumentSymbol, error) {
	h.mu.Lock()
	mf := h.parsed[params.TextDocument.URI]
	h.mu.Unlock()

	if mf == nil {
		return nil, nil
	}

	var symbols []lsp.DocumentSymbol

	for _, t := range mf.Targets {
		detail := ""
		if t.IsPattern {
			detail = "pattern rule"
		} else if t.IsDoubleColon {
			detail = "double-colon rule"
		}
		symbols = append(symbols, lsp.DocumentSymbol{
			Name:           t.Name,
			Detail:         detail,
			Kind:           lsp.SymbolKindFunction,
			Range:          t.Range,
			SelectionRange: t.NameRange,
		})
	}

	for _, v := range mf.Variables {
		detail := string(v.Op) + " " + v.Value
		if v.TargetScope != "" {
			detail = v.TargetScope + ": " + detail
		}
		symbols = append(symbols, lsp.DocumentSymbol{
			Name:           v.Name,
			Detail:         detail,
			Kind:           lsp.SymbolKindVariable,
			Range:          v.Range,
			SelectionRange: v.NameRange,
		})
	}

	for _, c := range mf.Conditionals {
		name := conditionalName(c)
		symbols = append(symbols, lsp.DocumentSymbol{
			Name:           name,
			Kind:           lsp.SymbolKindNamespace,
			Range:          c.Range,
			SelectionRange: c.Range,
		})
	}

	return symbols, nil
}

func targetHover(t *model.Target) *lsp.Hover {
	var sb strings.Builder
	fmt.Fprintf(&sb, "**`%s`**", t.Name)

	if t.DocComment != "" {
		sb.WriteString("\n\n")
		sb.WriteString(t.DocComment)
	}

	if len(t.Deps) > 0 {
		sb.WriteString("\n\n**Prerequisites:** ")
		names := make([]string, len(t.Deps))
		for i, d := range t.Deps {
			names[i] = "`" + d.Name + "`"
		}
		sb.WriteString(strings.Join(names, ", "))
	}

	if len(t.RecipeLines) > 0 {
		sb.WriteString("\n\n```makefile\n")
		for _, line := range t.RecipeLines {
			sb.WriteString("\t")
			sb.WriteString(line)
			sb.WriteString("\n")
		}
		sb.WriteString("```")
	}

	return &lsp.Hover{
		Contents: lsp.MarkupContent{
			Kind:  "markdown",
			Value: sb.String(),
		},
	}
}

func findTarget(mf *model.Makefile, name string) *model.Target {
	for _, t := range mf.Targets {
		if t.Name == name {
			return t
		}
	}
	return nil
}

func conditionalName(c *model.Conditional) string {
	switch c.Type {
	case model.CondIfeq:
		return "ifeq " + c.Args
	case model.CondIfneq:
		return "ifneq " + c.Args
	case model.CondIfdef:
		return "ifdef " + c.Args
	case model.CondIfndef:
		return "ifndef " + c.Args
	default:
		return "conditional"
	}
}

// parseAndResolve parses the text and resolves include directives from disk.
func (h *Handler) parseAndResolve(uri lsp.DocumentURI, text string) *model.Makefile {
	mf := parser.Parse(uri, text)
	if len(mf.Includes) > 0 && strings.HasPrefix(string(uri), "file://") {
		return resolver.Resolve(uri, text)
	}
	return mf
}

func inRange(pos lsp.Position, r lsp.Range) bool {
	if pos.Line < r.Start.Line || pos.Line > r.End.Line {
		return false
	}
	if pos.Line == r.Start.Line && pos.Character < r.Start.Character {
		return false
	}
	if pos.Line == r.End.Line && pos.Character >= r.End.Character {
		return false
	}
	return true
}

// varRefAtPosition extracts a variable/function name from $(NAME) or ${NAME} at pos.
func varRefAtPosition(text string, pos lsp.Position) string {
	lines := strings.Split(text, "\n")
	if pos.Line >= len(lines) {
		return ""
	}
	line := lines[pos.Line]
	col := pos.Character
	if col >= len(line) {
		return ""
	}

	// Search backwards for $( or ${
	start := -1
	for i := col; i >= 1; i-- {
		if line[i] == '(' || line[i] == '{' {
			if i > 0 && line[i-1] == '$' {
				start = i + 1
				break
			}
		}
		// Stop at closing delimiters.
		if line[i] == ')' || line[i] == '}' {
			break
		}
	}
	if start < 0 {
		return ""
	}

	// Search forward for ) or }
	end := -1
	closer := byte(')')
	if line[start-1] == '{' {
		closer = '}'
	}
	for i := start; i < len(line); i++ {
		if line[i] == closer {
			end = i
			break
		}
	}
	if end < 0 {
		return ""
	}

	name := line[start:end]
	// Only return if cursor is actually within the name.
	if col >= start && col < end {
		// Could be a function call like $(patsubst ...) — take first word.
		if idx := strings.IndexAny(name, " ,"); idx >= 0 {
			name = name[:idx]
		}
		return name
	}
	return ""
}

// Completion handles textDocument/completion.
func (h *Handler) Completion(_ context.Context, params *lsp.CompletionParams) (*lsp.CompletionList, error) {
	h.mu.Lock()
	uri := params.TextDocument.URI
	mf := h.parsed[uri]
	text := h.docs[uri]
	h.mu.Unlock()

	if mf == nil {
		return nil, nil
	}

	items := completion.Complete(mf, text, params.Position)
	return &lsp.CompletionList{
		IsIncomplete: false,
		Items:        items,
	}, nil
}

// Definition handles textDocument/definition.
func (h *Handler) Definition(_ context.Context, params *lsp.DefinitionParams) ([]lsp.Location, error) {
	h.mu.Lock()
	uri := params.TextDocument.URI
	mf := h.parsed[uri]
	text := h.docs[uri]
	h.mu.Unlock()

	if mf == nil {
		return nil, nil
	}

	pos := params.Position

	// Cursor on a dependency name → jump to target definition.
	for _, t := range mf.Targets {
		for _, dep := range append(t.Deps, t.OrderOnlyDeps...) {
			if inRange(pos, dep.Range) {
				if target := findTarget(mf, dep.Name); target != nil {
					return []lsp.Location{{
						URI:   uri,
						Range: target.NameRange,
					}}, nil
				}
			}
		}
	}

	// Cursor on a variable reference → jump to variable definition.
	if varName := varRefAtPosition(text, pos); varName != "" {
		for _, v := range mf.Variables {
			if v.Name == varName {
				return []lsp.Location{{
					URI:   uri,
					Range: v.NameRange,
				}}, nil
			}
		}
		for _, d := range mf.Defines {
			if d.Name == varName {
				return []lsp.Location{{
					URI:   uri,
					Range: d.Range,
				}}, nil
			}
		}
	}

	// Cursor on an include path → jump to file.
	for _, inc := range mf.Includes {
		if inRange(pos, inc.Range) {
			return []lsp.Location{{
				URI:   lsp.DocumentURI("file://" + inc.Path),
				Range: lsp.Range{},
			}}, nil
		}
	}

	return nil, nil
}

// References handles textDocument/references.
func (h *Handler) References(_ context.Context, params *lsp.ReferenceParams) ([]lsp.Location, error) {
	h.mu.Lock()
	uri := params.TextDocument.URI
	mf := h.parsed[uri]
	h.mu.Unlock()

	if mf == nil {
		return nil, nil
	}

	pos := params.Position

	// Cursor on a target name → find all deps referencing it.
	for _, t := range mf.Targets {
		if inRange(pos, t.NameRange) {
			return findTargetReferences(mf, uri, t.Name, params.Context.IncludeDeclaration), nil
		}
	}

	// Cursor on a variable name → find all $(VAR) refs.
	for _, v := range mf.Variables {
		if inRange(pos, v.NameRange) {
			return findVarReferences(mf, uri, v.Name, params.Context.IncludeDeclaration), nil
		}
	}

	return nil, nil
}

func findTargetReferences(mf *model.Makefile, uri lsp.DocumentURI, name string, includeDecl bool) []lsp.Location {
	var locs []lsp.Location

	if includeDecl {
		for _, t := range mf.Targets {
			if t.Name == name {
				locs = append(locs, lsp.Location{URI: uri, Range: t.NameRange})
			}
		}
	}

	for _, t := range mf.Targets {
		for _, dep := range append(t.Deps, t.OrderOnlyDeps...) {
			if dep.Name == name {
				locs = append(locs, lsp.Location{URI: uri, Range: dep.Range})
			}
		}
	}

	return locs
}

func findVarReferences(mf *model.Makefile, uri lsp.DocumentURI, name string, includeDecl bool) []lsp.Location {
	var locs []lsp.Location

	if includeDecl {
		for _, v := range mf.Variables {
			if v.Name == name {
				locs = append(locs, lsp.Location{URI: uri, Range: v.NameRange})
			}
		}
	}

	for _, v := range mf.Variables {
		for _, ref := range v.Refs {
			if ref.Name == name {
				locs = append(locs, lsp.Location{URI: uri, Range: ref.Range})
			}
		}
	}

	return locs
}

// CodeAction handles textDocument/codeAction.
func (h *Handler) CodeAction(_ context.Context, params *lsp.CodeActionParams) ([]lsp.CodeAction, error) {
	h.mu.Lock()
	uri := params.TextDocument.URI
	mf := h.parsed[uri]
	h.mu.Unlock()

	if mf == nil {
		return nil, nil
	}

	var actions []lsp.CodeAction
	kind := lsp.CodeActionQuickFix

	for _, diag := range params.Context.Diagnostics {
		if diag.Source != "make-ls" {
			continue
		}

		// "Add .PHONY" quickfix for missing-phony hint.
		if strings.Contains(diag.Message, "looks like a phony target") {
			targetName := extractPhonyTarget(diag.Message)
			if targetName == "" {
				continue
			}
			edit := addPhonyEdit(uri, targetName)
			actions = append(actions, lsp.CodeAction{
				Title:       fmt.Sprintf("Add .PHONY: %s", targetName),
				Kind:        &kind,
				Diagnostics: []lsp.Diagnostic{diag},
				IsPreferred: boolPtr(true),
				Edit:        edit,
			})
		}
	}

	return actions, nil
}

func extractPhonyTarget(msg string) string {
	// Message format: "X looks like a phony target; consider adding .PHONY: X"
	idx := strings.Index(msg, " looks like a phony target")
	if idx < 0 {
		return ""
	}
	return msg[:idx]
}

func addPhonyEdit(uri lsp.DocumentURI, targetName string) *lsp.WorkspaceEdit {
	// Check if there's already a .PHONY line we can extend.
	// For simplicity, insert ".PHONY: targetName\n" at the top of the file.
	insertPos := lsp.Position{Line: 0, Character: 0}

	// If there are existing targets, insert before the first one.
	// But convention is .PHONY at the top, so line 0 is fine.
	newText := ".PHONY: " + targetName + "\n"

	return &lsp.WorkspaceEdit{
		Changes: map[lsp.DocumentURI][]lsp.TextEdit{
			uri: {
				{
					Range:   lsp.Range{Start: insertPos, End: insertPos},
					NewText: newText,
				},
			},
		},
	}
}

// Formatting handles textDocument/formatting.
func (h *Handler) Formatting(_ context.Context, params *lsp.DocumentFormattingParams) ([]lsp.TextEdit, error) {
	h.mu.Lock()
	uri := params.TextDocument.URI
	text := h.docs[uri]
	mf := h.parsed[uri]
	h.mu.Unlock()

	if mf == nil {
		return nil, nil
	}

	formatted := formatMakefile(text)
	if formatted == text {
		return nil, nil
	}

	// Replace entire document.
	lines := strings.Split(text, "\n")
	lastLine := len(lines) - 1
	lastChar := len(lines[lastLine])

	return []lsp.TextEdit{
		{
			Range: lsp.Range{
				Start: lsp.Position{Line: 0, Character: 0},
				End:   lsp.Position{Line: lastLine, Character: lastChar},
			},
			NewText: formatted,
		},
	}, nil
}

// formatMakefile applies formatting rules: tabs in recipes, trim trailing
// whitespace, ensure final newline.
func formatMakefile(text string) string {
	lines := strings.Split(text, "\n")
	inRecipe := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Empty line ends recipe context.
		if trimmed == "" {
			inRecipe = false
			lines[i] = ""
			continue
		}

		// Comment lines — just trim trailing whitespace.
		if strings.HasPrefix(trimmed, "#") {
			lines[i] = strings.TrimRight(line, " \t")
			continue
		}

		// Recipe lines — ensure tab prefix, trim trailing whitespace.
		if inRecipe && (line[0] == '\t' || line[0] == ' ') {
			// Normalize: ensure tab, trim trailing spaces.
			content := strings.TrimLeft(line, " \t")
			lines[i] = "\t" + strings.TrimRight(content, " \t")
			continue
		}

		// Check if this line starts a target rule (has : but not =).
		if isTargetLine(trimmed) {
			inRecipe = true
		} else {
			inRecipe = false
		}

		// Trim trailing whitespace on all other lines.
		lines[i] = strings.TrimRight(line, " \t")
	}

	result := strings.Join(lines, "\n")

	// Ensure final newline.
	if !strings.HasSuffix(result, "\n") {
		result += "\n"
	}

	// Remove excessive trailing blank lines (keep at most one).
	for strings.HasSuffix(result, "\n\n\n") {
		result = result[:len(result)-1]
	}

	return result
}

func isTargetLine(trimmed string) bool {
	// A target line has a colon but no = before the colon.
	colonIdx := strings.Index(trimmed, ":")
	if colonIdx < 0 {
		return false
	}
	beforeColon := trimmed[:colonIdx]
	return !strings.ContainsAny(beforeColon, "=")
}
