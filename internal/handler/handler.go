package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/owenrumney/go-lsp/lsp"
	"github.com/owenrumney/go-lsp/server"
	"github.com/owenrumney/make-ls/internal/analysis"
	"github.com/owenrumney/make-ls/internal/completion"
	"github.com/owenrumney/make-ls/internal/model"
	"github.com/owenrumney/make-ls/internal/parser"
	"github.com/owenrumney/make-ls/internal/position"
	"github.com/owenrumney/make-ls/internal/resolver"
)

// Handler implements the LSP handler interfaces for Makefiles.
type Handler struct {
	client   *server.Client
	mu       sync.Mutex
	docs     map[lsp.DocumentURI]string
	parsed   map[lsp.DocumentURI]*model.Makefile
	encoding lsp.PositionEncodingKind

	// includedBy maps an included file to the roots whose model contains it.
	includedBy map[lsp.DocumentURI]map[lsp.DocumentURI]bool

	// diags is the last set each root produced, keyed by the file that owns
	// each finding and already encoded against that file's text.
	diags map[lsp.DocumentURI]map[lsp.DocumentURI][]lsp.Diagnostic

	// watchDynamic is set when the client can register watchers at runtime.
	// watchRegistered latches only once the client has accepted the request.
	watchDynamic    bool
	watchPending    bool
	watchRegistered bool
}

// watchRegistrationTimeout bounds the client/registerCapability round trip.
const watchRegistrationTimeout = 5 * time.Second

// New creates a new Handler.
func New() *Handler {
	return &Handler{
		docs:       make(map[lsp.DocumentURI]string),
		parsed:     make(map[lsp.DocumentURI]*model.Makefile),
		includedBy: make(map[lsp.DocumentURI]map[lsp.DocumentURI]bool),
		diags:      make(map[lsp.DocumentURI]map[lsp.DocumentURI][]lsp.Diagnostic),
	}
}

func boolPtr(b bool) *bool { return &b }

func pickPositionEncoding(client *lsp.ClientCapabilities) lsp.PositionEncodingKind {
	if client != nil && client.General != nil {
		var hasUTF16 bool
		for _, enc := range client.General.PositionEncodings {
			if enc == lsp.PositionEncodingUTF8 {
				return lsp.PositionEncodingUTF8
			}
			if enc == lsp.PositionEncodingUTF16 {
				hasUTF16 = true
			}
		}
		if hasUTF16 {
			return lsp.PositionEncodingUTF16
		}
		if len(client.General.PositionEncodings) > 0 {
			return client.General.PositionEncodings[0]
		}
	}
	return lsp.PositionEncodingUTF16
}

func supportsWatchedFiles(c *lsp.ClientCapabilities) bool {
	return c != nil && c.Workspace != nil && c.Workspace.DidChangeWatchedFiles != nil &&
		c.Workspace.DidChangeWatchedFiles.DynamicRegistration != nil &&
		*c.Workspace.DidChangeWatchedFiles.DynamicRegistration
}

// Initialize handles the initialize request.
func (h *Handler) Initialize(_ context.Context, params *lsp.InitializeParams) (*lsp.InitializeResult, error) {
	var clientCaps *lsp.ClientCapabilities
	if params != nil {
		clientCaps = &params.Capabilities
	}
	positionEncoding := pickPositionEncoding(clientCaps)
	h.mu.Lock()
	h.encoding = positionEncoding
	h.watchDynamic = supportsWatchedFiles(clientCaps)
	h.mu.Unlock()
	return &lsp.InitializeResult{
		Capabilities: lsp.ServerCapabilities{
			PositionEncoding: &positionEncoding,
			TextDocumentSync: &lsp.TextDocumentSyncOptions{
				OpenClose: boolPtr(true),
				Change:    lsp.SyncFull,
				Save:      &lsp.SaveOptions{IncludeText: boolPtr(false)},
			},
			HoverProvider:          boolPtr(true),
			DocumentSymbolProvider: &lsp.DocumentSymbolOptions{},
			CompletionProvider: &lsp.CompletionOptions{
				TriggerCharacters: []string{"$", "("},
			},
			DefinitionProvider: boolPtr(true),
			ReferencesProvider: boolPtr(true),
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

// encoderFor returns a position encoder for the given document text using
// the negotiated wire encoding. Callers should fetch text under h.mu.
func (h *Handler) encoderFor(text string) *position.Encoder {
	return position.New(text, h.encoding)
}

// DidOpen handles textDocument/didOpen.
func (h *Handler) DidOpen(ctx context.Context, params *lsp.DidOpenTextDocumentParams) error {
	h.mu.Lock()
	uri := params.TextDocument.URI
	text := params.TextDocument.Text
	h.docs[uri] = text
	h.setParsed(uri, text)
	dependents := h.reresolveDependents(uri)
	h.mu.Unlock()

	h.registerFileWatchers()
	// The opened document is a root in its own right; without it nothing is
	// reported until the first save.
	return h.publishAll(ctx, append(dependents, uri))
}

// DidChange handles textDocument/didChange.
func (h *Handler) DidChange(ctx context.Context, params *lsp.DidChangeTextDocumentParams) error {
	if len(params.ContentChanges) == 0 {
		return nil
	}
	h.mu.Lock()
	uri := params.TextDocument.URI
	text := params.ContentChanges[len(params.ContentChanges)-1].Text
	h.docs[uri] = text
	h.setParsed(uri, text)
	dependents := h.reresolveDependents(uri)
	h.mu.Unlock()

	return h.publishAll(ctx, append(dependents, uri))
}

// DidClose handles textDocument/didClose.
func (h *Handler) DidClose(ctx context.Context, params *lsp.DidCloseTextDocumentParams) error {
	h.mu.Lock()
	uri := params.TextDocument.URI
	delete(h.docs, uri)
	delete(h.parsed, uri)
	h.forgetRoot(uri)
	// The buffer is gone, so dependents must fall back to disk.
	dependents := h.reresolveDependents(uri)
	h.mu.Unlock()

	// uri has no model now, so publishing it clears what it last reported —
	// including findings it had routed to files it included.
	return h.publishAll(ctx, append(dependents, uri))
}

// DidChangeWatchedFiles handles workspace/didChangeWatchedFiles — an include
// that is not open in the editor changed on disk.
func (h *Handler) DidChangeWatchedFiles(ctx context.Context, params *lsp.DidChangeWatchedFilesParams) error {
	h.mu.Lock()
	// A batch rewriting a whole project names the same root over and over; each
	// re-resolve re-reads its include tree from disk, so collect first.
	seen := make(map[lsp.DocumentURI]bool)
	var dependents []lsp.DocumentURI
	for _, ev := range params.Changes {
		for _, root := range h.dependentsOf(ev.URI) {
			if !seen[root] {
				seen[root] = true
				dependents = append(dependents, root)
			}
		}
	}
	h.reresolve(dependents)
	h.mu.Unlock()

	return h.publishAll(ctx, dependents)
}

// DidSave handles textDocument/didSave — runs diagnostics and publishes them.
// It also re-resolves the saved document: without a file watcher this is the
// only point at which an include edited outside the editor is picked up.
func (h *Handler) DidSave(ctx context.Context, params *lsp.DidSaveTextDocumentParams) error {
	uri := params.TextDocument.URI
	h.mu.Lock()
	if text, ok := h.docs[uri]; ok {
		h.setParsed(uri, text)
	}
	h.mu.Unlock()

	return h.publishDiagnostics(ctx, uri)
}

// setParsed reparses uri and refreshes the include edges pointing at it.
// Callers must hold h.mu.
func (h *Handler) setParsed(uri lsp.DocumentURI, text string) {
	mf := h.parseAndResolve(uri, text)
	h.parsed[uri] = mf
	h.forgetRoot(uri)
	for _, inc := range includedURIs(mf) {
		if inc == uri {
			continue
		}
		if h.includedBy[inc] == nil {
			h.includedBy[inc] = make(map[lsp.DocumentURI]bool)
		}
		h.includedBy[inc][uri] = true
	}
}

// includedURIs lists every file merged into mf, plus includes that resolved to
// a path but could not be read — creating one later must reach the root too.
func includedURIs(mf *model.Makefile) []lsp.DocumentURI {
	seen := make(map[lsp.DocumentURI]bool, len(mf.Sources))
	var uris []lsp.DocumentURI
	add := func(u lsp.DocumentURI) {
		if u != "" && !seen[u] {
			seen[u] = true
			uris = append(uris, u)
		}
	}
	for u := range mf.Sources {
		add(u)
	}
	// Unresolved paths come from the resolver, which sees every depth: a file
	// an include names is in neither Sources nor mf.Includes.
	for _, u := range mf.UnresolvedIncludes {
		add(u)
	}
	return uris
}

// forgetRoot drops every include edge pointing at root. Callers must hold h.mu.
func (h *Handler) forgetRoot(root lsp.DocumentURI) {
	for inc, roots := range h.includedBy {
		delete(roots, root)
		if len(roots) == 0 {
			delete(h.includedBy, inc)
		}
	}
}

// reresolveDependents rebuilds every open root that includes uri and returns
// them. Callers must hold h.mu.
func (h *Handler) reresolveDependents(uri lsp.DocumentURI) []lsp.DocumentURI {
	roots := h.dependentsOf(uri)
	h.reresolve(roots)
	return roots
}

// dependentsOf lists the open roots whose model includes uri, in a stable
// order. Callers must hold h.mu.
func (h *Handler) dependentsOf(uri lsp.DocumentURI) []lsp.DocumentURI {
	roots := make([]lsp.DocumentURI, 0, len(h.includedBy[uri]))
	for root := range h.includedBy[uri] {
		if root != uri {
			roots = append(roots, root)
		}
	}
	slices.Sort(roots)
	return roots
}

// reresolve reparses each root from its open buffer. Callers must hold h.mu.
func (h *Handler) reresolve(roots []lsp.DocumentURI) {
	for _, root := range roots {
		if text, ok := h.docs[root]; ok {
			h.setParsed(root, text)
		}
	}
}

func (h *Handler) publishAll(ctx context.Context, uris []lsp.DocumentURI) error {
	var errs []error
	for _, uri := range uris {
		if err := h.publishDiagnostics(ctx, uri); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// publishDiagnostics diagnoses the model rooted at uri and publishes each
// finding under the file that owns it, encoded against that file's own text.
// Encoding an include's range against the root's text is meaningless.
func (h *Handler) publishDiagnostics(ctx context.Context, uri lsp.DocumentURI) error {
	if h.client == nil {
		return nil
	}

	h.mu.Lock()
	owners := h.recordDiagnostics(uri)
	published := h.mergedDiagnostics(owners)
	h.mu.Unlock()

	for _, owner := range owners {
		err := h.client.PublishDiagnostics(ctx, &lsp.PublishDiagnosticsParams{
			URI:         owner,
			Diagnostics: published[owner],
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// recordDiagnostics re-diagnoses the model rooted at root and stores the result
// against it, returning every file that needs republishing — the ones it owns
// now plus the ones it owned last time. A root with no model (closed, or never
// parsed) drops out, which is what clears its findings. Callers must hold h.mu.
func (h *Handler) recordDiagnostics(root lsp.DocumentURI) []lsp.DocumentURI {
	seen := make(map[lsp.DocumentURI]bool)
	var owners []lsp.DocumentURI
	add := func(u lsp.DocumentURI) {
		if !seen[u] {
			seen[u] = true
			owners = append(owners, u)
		}
	}
	for owner := range h.diags[root] {
		add(owner)
	}

	mf := h.parsed[root]
	if mf == nil {
		delete(h.diags, root)
		slices.Sort(owners)
		return owners
	}

	byURI := analysis.DiagnoseByURI(mf)
	// Every file in the model reports, so a fixed file gets its warnings cleared.
	for owner := range mf.Sources {
		if _, ok := byURI[owner]; !ok {
			byURI[owner] = nil
		}
	}
	for owner, diags := range byURI {
		enc := position.New(mf.Sources[owner], h.encoding)
		for i := range diags {
			diags[i].Range = enc.RangeToWire(diags[i].Range)
		}
		add(owner)
	}
	h.diags[root] = byURI
	slices.Sort(owners)
	return owners
}

// mergedDiagnostics unions each owner's findings across every root that holds
// it. publishDiagnostics is a full replace per URI, so an include two roots
// pull in must carry both roots' findings or the last publish wins.
// Callers must hold h.mu.
func (h *Handler) mergedDiagnostics(owners []lsp.DocumentURI) map[lsp.DocumentURI][]lsp.Diagnostic {
	roots := make([]lsp.DocumentURI, 0, len(h.diags))
	for root := range h.diags {
		roots = append(roots, root)
	}
	slices.Sort(roots)

	merged := make(map[lsp.DocumentURI][]lsp.Diagnostic, len(owners))
	for _, owner := range owners {
		// Never nil: the wire form of "nothing here" is an empty list, and a
		// null would leave the last publish standing.
		diags := []lsp.Diagnostic{}
		seen := make(map[string]bool)
		for _, root := range roots {
			for _, d := range h.diags[root][owner] {
				if key := diagKey(d); !seen[key] {
					seen[key] = true
					diags = append(diags, d)
				}
			}
		}
		merged[owner] = diags
	}
	return merged
}

// diagKey identifies a finding across roots: the same node diagnosed twice
// through two include paths is one problem.
func diagKey(d lsp.Diagnostic) string {
	var sev any
	if d.Severity != nil {
		sev = *d.Severity
	}
	return fmt.Sprintf("%v|%v|%s|%s", d.Range, sev, d.Source, d.Message)
}

// registerFileWatchers asks the client to watch Makefiles once, lazily: go-lsp
// swallows `initialized`, so the first DidOpen is the earliest safe hook.
func (h *Handler) registerFileWatchers() {
	if h.client == nil || !h.claimWatchRegistration() {
		return
	}

	// The extension contributes GNUmakefile as a makefile too, so it needs a
	// glob of its own.
	opts, err := json.Marshal(lsp.DidChangeWatchedFilesRegistrationOptions{
		Watchers: []lsp.FileSystemWatcher{
			{GlobPattern: "**/*.{mk,mak}"},
			{GlobPattern: "**/[Mm]akefile"},
			{GlobPattern: "**/GNUmakefile"},
		},
	})
	if err != nil {
		h.finishWatchRegistration(false)
		return
	}
	// DidOpen is a notification the client is waiting on; a request back to it
	// from this goroutine would deadlock.
	go func() {
		// A client that never answers must not latch watchPending for the life
		// of the session: the deadline is what makes the retry real.
		ctx, cancel := context.WithTimeout(context.Background(), watchRegistrationTimeout)
		defer cancel()
		err := h.client.RegisterCapability(ctx, &lsp.RegistrationParams{
			Registrations: []lsp.Registration{{
				ID:              "make-ls-watched-files",
				Method:          "workspace/didChangeWatchedFiles",
				RegisterOptions: opts,
			}},
		})
		if err != nil {
			slog.Warn("registering file watchers failed", "err", err)
		}
		h.finishWatchRegistration(err == nil)
	}()
}

// claimWatchRegistration reports whether this caller should send the request.
// Only one caller wins until the attempt finishes.
func (h *Handler) claimWatchRegistration() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.watchDynamic || h.watchRegistered || h.watchPending {
		return false
	}
	h.watchPending = true
	return true
}

// finishWatchRegistration latches only on success, so a client that refused or
// dropped the request is retried on the next DidOpen instead of leaving the
// session without a watcher for its whole life.
func (h *Handler) finishWatchRegistration(ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.watchPending = false
	h.watchRegistered = ok
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

	enc := h.encoderFor(text)
	pos := enc.FromWire(params.Position)

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

	// Check .PHONY target references.
	for _, phony := range mf.PhonyRefs {
		if inRange(pos, phony.Range) {
			if target := findTarget(mf, phony.Name); target != nil {
				return targetHover(target), nil
			}
		}
	}

	// Check variable references in the text at cursor position.
	if varName, _ := varRefAtPosition(text, pos); varName != "" {
		// Auto variable.
		for _, av := range completion.AutoVars {
			if av.Name == varName {
				return &lsp.Hover{
					Contents: lsp.NewHoverContents(lsp.Markdown, fmt.Sprintf("**`$%s`** — %s", av.Name, av.Doc)),
				}, nil
			}
		}
		// Function.
		for _, fn := range completion.Functions {
			if fn.Name == varName {
				return &lsp.Hover{
					Contents: lsp.NewHoverContents(lsp.Markdown, fmt.Sprintf("**`%s`**\n\n%s", fn.Args, fn.Doc)),
				}, nil
			}
		}
		// Builtin variable.
		for _, bv := range completion.BuiltinVars {
			if bv.Name == varName {
				return &lsp.Hover{
					Contents: lsp.NewHoverContents(lsp.Markdown, fmt.Sprintf("**`%s`** — %s", bv.Name, bv.Doc)),
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
					Contents: lsp.NewHoverContents(lsp.Markdown, detail),
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
				Contents: lsp.NewHoverContents(lsp.Markdown, detail),
			}, nil
		}
	}

	return nil, nil
}

// DocumentSymbol handles textDocument/documentSymbol.
func (h *Handler) DocumentSymbol(_ context.Context, params *lsp.DocumentSymbolParams) ([]lsp.DocumentSymbol, error) {
	h.mu.Lock()
	mf := h.parsed[params.TextDocument.URI]
	text := h.docs[params.TextDocument.URI]
	h.mu.Unlock()

	if mf == nil {
		return nil, nil
	}

	enc := h.encoderFor(text)
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
			Range:          enc.RangeToWire(t.Range),
			SelectionRange: enc.RangeToWire(t.NameRange),
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
			Range:          enc.RangeToWire(v.Range),
			SelectionRange: enc.RangeToWire(v.NameRange),
		})
	}

	for _, c := range mf.Conditionals {
		name := conditionalName(c)
		wire := enc.RangeToWire(c.Range)
		symbols = append(symbols, lsp.DocumentSymbol{
			Name:           name,
			Kind:           lsp.SymbolKindNamespace,
			Range:          wire,
			SelectionRange: wire,
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
		Contents: lsp.NewHoverContents(lsp.Markdown, sb.String()),
	}
}

func preferEarlierVariableDefinition(mf *model.Makefile, current *model.Variable) *model.Variable {
	if !current.Override {
		return current
	}
	for i, v := range mf.Variables {
		if v != current {
			continue
		}
		for j := i - 1; j >= 0; j-- {
			candidate := mf.Variables[j]
			if candidate.Name == current.Name {
				return candidate
			}
		}
		break
	}
	return current
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

// parseAndResolve parses the text and resolves include directives, preferring
// an open buffer over disk. Callers must hold h.mu: the source provider reads
// h.docs directly and must not re-lock.
func (h *Handler) parseAndResolve(uri lsp.DocumentURI, text string) *model.Makefile {
	mf := parser.Parse(uri, text)
	if len(mf.Includes) > 0 && strings.HasPrefix(string(uri), "file://") {
		return resolver.Resolve(uri, text, func(inc lsp.DocumentURI) (string, bool) {
			s, ok := h.docs[inc]
			return s, ok
		})
	}
	return mf
}

func uriForPath(path string) lsp.DocumentURI {
	path = filepath.ToSlash(path)
	if len(path) >= 2 && path[1] == ':' {
		// VS Code canonicalizes drive letters and escapes their colon in file
		// URIs; matching that spelling keeps URI-keyed maps coherent on Windows.
		path = strings.ToLower(path[:1]) + path[1:]
		u := &url.URL{Scheme: "file", Path: "/" + path}
		u.RawPath = strings.Replace(u.EscapedPath(), ":", "%3A", 1)
		return lsp.DocumentURI(u.String())
	}
	return lsp.DocumentURI((&url.URL{Scheme: "file", Path: path}).String())
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

// varRefAtPosition extracts a variable/function name from $(NAME) or ${NAME} at
// pos. call is true when arguments followed the name, which is what separates a
// builtin call $(dir a/b) from a use of a variable that happens to be named dir.
func varRefAtPosition(text string, pos lsp.Position) (name string, call bool) {
	lines := strings.Split(text, "\n")
	if pos.Line >= len(lines) {
		return "", false
	}
	line := lines[pos.Line]
	col := pos.Character
	if col >= len(line) {
		return "", false
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
		return "", false
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
		return "", false
	}

	name = line[start:end]
	// Only return if cursor is actually within the name.
	if col >= start && col < end {
		// Could be a function call like $(patsubst ...) — take first word.
		if idx := strings.IndexAny(name, " ,"); idx >= 0 {
			return name[:idx], true
		}
		return name, false
	}
	return "", false
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

	enc := h.encoderFor(text)
	items := completion.Complete(mf, text, enc.FromWire(params.Position))
	return &lsp.CompletionList{
		IsIncomplete: false,
		Items:        items,
	}, nil
}

// locEncoder converts a node's range to a Location, encoding it against the
// text that node's own file was parsed from.
type locEncoder struct {
	mf       *model.Makefile
	encoding lsp.PositionEncodingKind
	cache    map[lsp.DocumentURI]*position.Encoder
}

func newLocEncoder(mf *model.Makefile, encoding lsp.PositionEncodingKind) *locEncoder {
	return &locEncoder{mf: mf, encoding: encoding, cache: map[lsp.DocumentURI]*position.Encoder{}}
}

func (e *locEncoder) loc(uri lsp.DocumentURI, r lsp.Range) lsp.Location {
	// A URI with no recorded source cannot be encoded: position.New("") would
	// hand back raw byte columns dressed as UTF-16 ones. Fall back to the root,
	// which is always present, so URI and coordinate space stay in step.
	if _, ok := e.mf.Sources[uri]; !ok {
		uri = e.mf.URI
	}
	enc, ok := e.cache[uri]
	if !ok {
		enc = position.New(e.mf.Sources[uri], e.encoding)
		e.cache[uri] = enc
	}
	return lsp.Location{URI: uri, Range: enc.RangeToWire(r)}
}

func (e *locEncoder) one(uri lsp.DocumentURI, r lsp.Range) []lsp.Location {
	return []lsp.Location{e.loc(uri, r)}
}

// Definition handles textDocument/definition.
func (h *Handler) Definition(_ context.Context, params *lsp.DefinitionParams) ([]lsp.Location, error) {
	h.mu.Lock()
	uri := params.TextDocument.URI
	mf := h.parsed[uri]
	text := h.docs[uri]
	encoding := h.encoding
	models := h.modelsFor(uri)
	h.mu.Unlock()

	if mf == nil {
		return nil, nil
	}

	enc := position.New(text, encoding)
	pos := enc.FromWire(params.Position)
	le := newLocEncoder(mf, encoding)

	// Cursor on a dependency name → jump to target definition.
	for _, dep := range allDepRefs(mf) {
		if dep.URI == uri && inRange(pos, dep.Range) {
			for _, candidate := range models {
				if target := findTarget(candidate, dep.Name); target != nil {
					return newLocEncoder(candidate, encoding).one(target.URI, target.NameRange), nil
				}
			}
		}
	}

	// Cursor on a variable name → jump to its definition.
	for _, v := range mf.Variables {
		if v.URI == uri && inRange(pos, v.NameRange) {
			target := preferEarlierVariableDefinition(mf, v)
			return le.one(target.URI, target.NameRange), nil
		}
	}
	for _, d := range mf.Defines {
		if d.URI == uri && inRange(pos, d.Range) {
			return le.one(d.URI, d.Range), nil
		}
	}

	// Cursor on a variable reference → jump to variable definition. A builtin
	// call is not a variable use, even where a variable shares its name.
	definitions := func(name string) []lsp.Location {
		for _, candidate := range models {
			if locs := definitionsFor(candidate, newLocEncoder(candidate, encoding), name); locs != nil {
				return locs
			}
		}
		return nil
	}
	if varName, call := varRefAtPosition(text, pos); varName != "" && !isBuiltinCall(varName, call) {
		if locs := definitions(varName); locs != nil {
			return locs, nil
		}
	}

	// Cursor on a bare variable name in a directive or conditional header.
	for _, ref := range mf.VarRefs {
		if ref.URI == uri && inRange(pos, ref.Range) {
			if locs := definitions(ref.Name); locs != nil {
				return locs, nil
			}
		}
	}

	// Cursor on an include path → jump to a successfully resolved file.
	for _, inc := range mf.Includes {
		if inRange(pos, inc.Range) && inc.ResolvedPath != "" {
			return []lsp.Location{{URI: uriForPath(inc.ResolvedPath), Range: lsp.Range{}}}, nil
		}
	}

	return nil, nil
}

func definitionsFor(mf *model.Makefile, le *locEncoder, name string) []lsp.Location {
	for _, v := range mf.Variables {
		if v.Name == name {
			return le.one(v.URI, v.NameRange)
		}
	}
	for _, d := range mf.Defines {
		if d.Name == name {
			return le.one(d.URI, d.Range)
		}
	}
	return nil
}

// References handles textDocument/references.
func (h *Handler) References(_ context.Context, params *lsp.ReferenceParams) ([]lsp.Location, error) {
	h.mu.Lock()
	uri := params.TextDocument.URI
	mf := h.parsed[uri]
	text := h.docs[uri]
	encoding := h.encoding
	models := h.modelsFor(uri)
	h.mu.Unlock()

	if mf == nil {
		return nil, nil
	}

	enc := position.New(text, encoding)
	pos := enc.FromWire(params.Position)
	includeDecl := params.Context.IncludeDeclaration

	varRefs := func(name string) []lsp.Location {
		return collectRefs(models, encoding, func(m *model.Makefile, le *locEncoder) []lsp.Location {
			return findVarReferences(m, name, includeDecl, le)
		})
	}
	targetRefs := func(name string) []lsp.Location {
		return collectRefs(models, encoding, func(m *model.Makefile, le *locEncoder) []lsp.Location {
			return findTargetReferences(m, name, includeDecl, le)
		})
	}

	// A $(VAR) use is resolved first: it can sit inside a target name or a
	// prerequisite, and there the variable is the symbol under the cursor. A
	// builtin call falls through instead: $(call NAME,...) names a macro, and
	// the indexed refs below carry its span.
	if name, call := varRefAtPosition(text, pos); name != "" && !isBuiltinCall(name, call) {
		return varRefs(name), nil
	}

	// Cursor on a target name → find all deps referencing it.
	for _, t := range mf.Targets {
		if t.URI == uri && inRange(pos, t.NameRange) {
			return targetRefs(t.Name), nil
		}
	}

	// Cursor on a prerequisite or .PHONY entry — a use of a target.
	for _, dep := range allDepRefs(mf) {
		if dep.URI == uri && inRange(pos, dep.Range) {
			return targetRefs(dep.Name), nil
		}
	}

	// Cursor on a variable or define name → find all variable refs.
	for _, v := range mf.Variables {
		if v.URI == uri && inRange(pos, v.NameRange) {
			return varRefs(v.Name), nil
		}
	}
	for _, d := range mf.Defines {
		if d.URI == uri && inRange(pos, d.Range) {
			return varRefs(d.Name), nil
		}
	}

	// Cursor on a bare name in export/unexport or an ifdef header.
	for _, ref := range mf.VarRefs {
		if ref.URI == uri && inRange(pos, ref.Range) {
			return varRefs(ref.Name), nil
		}
	}

	return nil, nil
}

// modelsFor returns uri's own model followed by every open root that includes
// it. Without the roots, find-references from inside an include would miss the
// uses in the files that pull it in, and the answer would depend on where the
// cursor sits. Callers must hold h.mu.
func (h *Handler) modelsFor(uri lsp.DocumentURI) []*model.Makefile {
	var models []*model.Makefile
	if mf := h.parsed[uri]; mf != nil {
		models = append(models, mf)
	}
	for _, root := range h.dependentsOf(uri) {
		if mf := h.parsed[root]; mf != nil {
			models = append(models, mf)
		}
	}
	return models
}

// collectRefs runs find over every model and drops duplicates: an include's own
// model and a root that includes it hold the same nodes.
func collectRefs(models []*model.Makefile, encoding lsp.PositionEncodingKind,
	find func(*model.Makefile, *locEncoder) []lsp.Location) []lsp.Location {
	var locs []lsp.Location
	seen := make(map[lsp.Location]bool)
	for _, mf := range models {
		le := newLocEncoder(mf, encoding)
		for _, loc := range find(mf, le) {
			if !seen[loc] {
				seen[loc] = true
				locs = append(locs, loc)
			}
		}
	}
	return locs
}

// allDepRefs is every prerequisite and .PHONY entry in the file.
func allDepRefs(mf *model.Makefile) []*model.DepRef {
	refs := make([]*model.DepRef, 0, len(mf.PhonyRefs))
	for _, t := range mf.Targets {
		refs = append(refs, t.Deps...)
		refs = append(refs, t.OrderOnlyDeps...)
	}
	return append(refs, mf.PhonyRefs...)
}

// isBuiltinCall reports whether the cursor sits on a builtin function call
// rather than a variable use. Only the name with arguments after it qualifies:
// bare $(dir) is a use of a variable named dir, even though dir is a builtin.
func isBuiltinCall(name string, call bool) bool {
	if !call {
		return false
	}
	for _, fn := range completion.Functions {
		if fn.Name == name {
			return true
		}
	}
	return false
}

func findTargetReferences(mf *model.Makefile, name string, includeDecl bool, le *locEncoder) []lsp.Location {
	var locs []lsp.Location

	if includeDecl {
		for _, t := range mf.Targets {
			if t.Name == name {
				locs = append(locs, le.loc(t.URI, t.NameRange))
			}
		}
	}

	for _, dep := range allDepRefs(mf) {
		if dep.Name == name {
			locs = append(locs, le.loc(dep.URI, dep.Range))
		}
	}

	return locs
}

func findVarReferences(mf *model.Makefile, name string, includeDecl bool, le *locEncoder) []lsp.Location {
	var locs []lsp.Location

	if includeDecl {
		for _, v := range mf.Variables {
			if v.Name == name {
				locs = append(locs, le.loc(v.URI, v.NameRange))
			}
		}
		for _, d := range mf.Defines {
			if d.Name == name {
				locs = append(locs, le.loc(d.URI, d.Range))
			}
		}
	}

	for _, ref := range mf.VarRefs {
		if ref.Name == name {
			locs = append(locs, le.loc(ref.URI, ref.Range))
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

	enc := h.encoderFor(text)
	return []lsp.TextEdit{
		{
			Range: enc.RangeToWire(lsp.Range{
				Start: lsp.Position{Line: 0, Character: 0},
				End:   lsp.Position{Line: lastLine, Character: lastChar},
			}),
			NewText: formatted,
		},
	}, nil
}

// formatMakefile applies formatting rules: tabs in recipes, trim trailing
// whitespace, ensure final newline.
func formatMakefile(text string) string {
	lines := strings.Split(text, "\n")
	inRecipe := false
	continued := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Empty line ends recipe context.
		if trimmed == "" {
			inRecipe = false
			continued = false
			lines[i] = ""
			continue
		}

		// Comment lines — just trim trailing whitespace.
		if strings.HasPrefix(trimmed, "#") {
			lines[i] = strings.TrimRight(line, " \t")
			continued = false
			continue
		}

		// Recipe lines — ensure tab prefix, trim trailing whitespace.
		if inRecipe && (line[0] == '\t' || line[0] == ' ') {
			if continued {
				// Part of the previous logical line. Everything after the
				// leading tab is passed to the shell verbatim, so preserve the
				// author's indentation and only guarantee the tab.
				lines[i] = "\t" + strings.TrimRight(strings.TrimPrefix(line, "\t"), " \t")
			} else {
				// Normalize: ensure tab, trim trailing spaces.
				content := strings.TrimLeft(line, " \t")
				lines[i] = "\t" + strings.TrimRight(content, " \t")
			}
			continued = endsWithContinuation(lines[i])
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
		continued = endsWithContinuation(lines[i])
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

// endsWithContinuation reports whether the line ends in an unescaped backslash,
// making the following line part of the same logical line.
func endsWithContinuation(line string) bool {
	backslashes := 0
	for i := len(line) - 1; i >= 0 && line[i] == '\\'; i-- {
		backslashes++
	}
	return backslashes%2 == 1
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
