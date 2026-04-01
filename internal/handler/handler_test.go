package handler

import (
	"context"
	"strings"
	"testing"

	"github.com/owenrumney/go-lsp/lsp"
	"github.com/owenrumney/go-lsp/servertest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testURI = lsp.DocumentURI("file:///test/Makefile")

func newHarness(t *testing.T) *servertest.Harness {
	t.Helper()
	h := New()
	return servertest.New(t, h)
}

func TestInitializeCapabilities(t *testing.T) {
	harness := newHarness(t)
	caps := harness.InitResult.Capabilities

	require.NotNil(t, caps.TextDocumentSync)
	assert.Equal(t, lsp.SyncFull, caps.TextDocumentSync.Change)
	require.NotNil(t, caps.HoverProvider)
	assert.True(t, *caps.HoverProvider)
	require.NotNil(t, caps.DocumentSymbolProvider)
	assert.True(t, *caps.DocumentSymbolProvider)
}

func TestDidOpenAndSymbols(t *testing.T) {
	harness := newHarness(t)

	input := `.PHONY: all clean

CC := gcc

# Build everything
all: main.o
	$(CC) -o app $^

clean:
	rm -f app
`
	require.NoError(t, harness.DidOpen(testURI, "makefile", input))

	symbols, err := harness.DocumentSymbol(testURI)
	require.NoError(t, err)

	// Expect: targets (all, clean) + variable (CC)
	require.Len(t, symbols, 3)

	names := map[string]lsp.SymbolKind{}
	for _, s := range symbols {
		names[s.Name] = s.Kind
	}
	assert.Equal(t, lsp.SymbolKindFunction, names["all"])
	assert.Equal(t, lsp.SymbolKindFunction, names["clean"])
	assert.Equal(t, lsp.SymbolKindVariable, names["CC"])
}

func TestDidChangeUpdatesSymbols(t *testing.T) {
	harness := newHarness(t)

	require.NoError(t, harness.DidOpen(testURI, "makefile", "all:\n\techo done\n"))

	symbols, err := harness.DocumentSymbol(testURI)
	require.NoError(t, err)
	require.Len(t, symbols, 1)

	// Change to add a variable.
	require.NoError(t, harness.DidChange(testURI, 2, "CC := gcc\nall:\n\techo done\n"))

	symbols, err = harness.DocumentSymbol(testURI)
	require.NoError(t, err)
	require.Len(t, symbols, 2)
}

func TestHoverOnTarget(t *testing.T) {
	harness := newHarness(t)

	input := `# Build the project
build:
	go build ./...
`
	require.NoError(t, harness.DidOpen(testURI, "makefile", input))

	hover, err := harness.Hover(testURI, 1, 0)
	require.NoError(t, err)
	require.NotNil(t, hover)
	assert.Contains(t, hover.Contents.Value, "build")
	assert.Contains(t, hover.Contents.Value, "Build the project")
	assert.Contains(t, hover.Contents.Value, "go build ./...")
}

func TestHoverOnVariable(t *testing.T) {
	harness := newHarness(t)

	input := `CC := gcc`
	require.NoError(t, harness.DidOpen(testURI, "makefile", input))

	hover, err := harness.Hover(testURI, 0, 0)
	require.NoError(t, err)
	require.NotNil(t, hover)
	assert.Contains(t, hover.Contents.Value, "CC")
	assert.Contains(t, hover.Contents.Value, "gcc")
}

func TestHoverOnVarRef(t *testing.T) {
	harness := newHarness(t)

	input := `CC := gcc
OUT := $(CC) -o app
`
	require.NoError(t, harness.DidOpen(testURI, "makefile", input))

	// Hover on $(CC) — "C" of CC starts at column 9
	hover, err := harness.Hover(testURI, 1, 9)
	require.NoError(t, err)
	require.NotNil(t, hover)
	assert.Contains(t, hover.Contents.Value, "CC")
}

func TestHoverOnBuiltinVar(t *testing.T) {
	harness := newHarness(t)

	input := `OUT := $(MAKE) -C subdir`
	require.NoError(t, harness.DidOpen(testURI, "makefile", input))

	// Hover on $(MAKE)
	hover, err := harness.Hover(testURI, 0, 9)
	require.NoError(t, err)
	require.NotNil(t, hover)
	assert.Contains(t, hover.Contents.Value, "MAKE")
}

func TestHoverOnFunction(t *testing.T) {
	harness := newHarness(t)

	input := `SRC := $(wildcard *.c)`
	require.NoError(t, harness.DidOpen(testURI, "makefile", input))

	// Hover on $(wildcard *.c) — "wildcard" starts at column 8
	hover, err := harness.Hover(testURI, 0, 10)
	require.NoError(t, err)
	require.NotNil(t, hover)
	assert.Contains(t, hover.Contents.Value, "wildcard")
}

func TestHoverNoResult(t *testing.T) {
	harness := newHarness(t)

	input := `# just a comment`
	require.NoError(t, harness.DidOpen(testURI, "makefile", input))

	hover, err := harness.Hover(testURI, 0, 0)
	require.NoError(t, err)
	assert.Nil(t, hover)
}

func TestDiagnosticsOnSave(t *testing.T) {
	harness := newHarness(t)

	input := `all: build test

build:
	go build
`
	require.NoError(t, harness.DidOpen(testURI, "makefile", input))
	require.NoError(t, harness.DidSave(testURI))

	ctx := context.Background()
	diags, err := harness.WaitForDiagnostics(ctx, testURI)
	require.NoError(t, err)
	require.NotEmpty(t, diags)

	// Should have an undefined target warning for "test" and phony hints.
	var messages []string
	for _, d := range diags {
		messages = append(messages, d.Message)
	}
	assert.Contains(t, messages, "undefined target: test")
}

func TestSymbolsWithConditionals(t *testing.T) {
	harness := newHarness(t)

	input := `ifeq ($(OS),Linux)
CC := gcc
endif
`
	require.NoError(t, harness.DidOpen(testURI, "makefile", input))

	symbols, err := harness.DocumentSymbol(testURI)
	require.NoError(t, err)

	kinds := map[lsp.SymbolKind]int{}
	for _, s := range symbols {
		kinds[s.Kind]++
	}
	assert.Equal(t, 1, kinds[lsp.SymbolKindNamespace])  // conditional
	assert.Equal(t, 1, kinds[lsp.SymbolKindVariable])    // CC
}

func TestSymbolsPatternRuleDetail(t *testing.T) {
	harness := newHarness(t)

	input := `%.o: %.c
	$(CC) -c $<
`
	require.NoError(t, harness.DidOpen(testURI, "makefile", input))

	symbols, err := harness.DocumentSymbol(testURI)
	require.NoError(t, err)
	require.Len(t, symbols, 1)
	assert.Equal(t, "pattern rule", symbols[0].Detail)
}

func TestDidCloseRemovesDocument(t *testing.T) {
	harness := newHarness(t)

	require.NoError(t, harness.DidOpen(testURI, "makefile", "all:\n\techo done\n"))
	require.NoError(t, harness.DidClose(testURI))

	symbols, err := harness.DocumentSymbol(testURI)
	require.NoError(t, err)
	assert.Nil(t, symbols)
}

// Phase 2 tests: Completion, Definition, References

func TestCompletionCapability(t *testing.T) {
	harness := newHarness(t)
	caps := harness.InitResult.Capabilities
	require.NotNil(t, caps.CompletionProvider)
	assert.Contains(t, caps.CompletionProvider.TriggerCharacters, "$")
	assert.Contains(t, caps.CompletionProvider.TriggerCharacters, "(")
}

func TestCompletionInVarRef(t *testing.T) {
	harness := newHarness(t)

	input := "CC := gcc\nOUT := $("
	require.NoError(t, harness.DidOpen(testURI, "makefile", input))

	list, err := harness.Completion(testURI, 1, len("OUT := $("))
	require.NoError(t, err)
	require.NotNil(t, list)
	require.NotEmpty(t, list.Items)

	labels := completionLabels(list.Items)
	assert.Contains(t, labels, "CC")
}

func TestCompletionInDeps(t *testing.T) {
	harness := newHarness(t)

	input := "build:\n\techo done\n\nall: "
	require.NoError(t, harness.DidOpen(testURI, "makefile", input))

	list, err := harness.Completion(testURI, 3, len("all: "))
	require.NoError(t, err)
	require.NotNil(t, list)

	labels := completionLabels(list.Items)
	assert.Contains(t, labels, "build")
}

func TestDefinitionFromDepToTarget(t *testing.T) {
	harness := newHarness(t)

	// line 0: "all: build"
	// line 1: ""
	// line 2: "build:"
	input := "all: build\n\nbuild:\n\tgo build\n"
	require.NoError(t, harness.DidOpen(testURI, "makefile", input))

	// Cursor on "build" in "all: build" — col 5 is the 'b' of build.
	locs, err := harness.Definition(testURI, 0, 5)
	require.NoError(t, err)
	require.Len(t, locs, 1)
	assert.Equal(t, 2, locs[0].Range.Start.Line) // build target is on line 2
}

func TestDefinitionFromVarRef(t *testing.T) {
	harness := newHarness(t)

	// line 0: "CC := gcc"
	// line 1: "OUT := $(CC) -o app"
	input := "CC := gcc\nOUT := $(CC) -o app\n"
	require.NoError(t, harness.DidOpen(testURI, "makefile", input))

	// Cursor on "CC" inside $(CC) — col 9 is the first 'C'
	locs, err := harness.Definition(testURI, 1, 9)
	require.NoError(t, err)
	require.Len(t, locs, 1)
	assert.Equal(t, 0, locs[0].Range.Start.Line) // CC defined on line 0
}

func TestDefinitionNoResult(t *testing.T) {
	harness := newHarness(t)

	input := "# just a comment\n"
	require.NoError(t, harness.DidOpen(testURI, "makefile", input))

	locs, err := harness.Definition(testURI, 0, 0)
	require.NoError(t, err)
	assert.Nil(t, locs)
}

func TestReferencesForTarget(t *testing.T) {
	harness := newHarness(t)

	// line 0: "all: build"
	// line 1: ""
	// line 2: "build:"
	// line 3: "\tgo build"
	input := "all: build\n\nbuild:\n\tgo build\n"
	require.NoError(t, harness.DidOpen(testURI, "makefile", input))

	// Cursor on "build" target def at line 2, col 0.
	// Include declaration = true.
	locs, err := harness.References(testURI, 2, 0, true)
	require.NoError(t, err)
	require.Len(t, locs, 2) // declaration + reference in "all: build"
}

func TestReferencesForTargetExcludeDecl(t *testing.T) {
	harness := newHarness(t)

	input := "all: build\n\nbuild:\n\tgo build\n"
	require.NoError(t, harness.DidOpen(testURI, "makefile", input))

	locs, err := harness.References(testURI, 2, 0, false)
	require.NoError(t, err)
	require.Len(t, locs, 1) // only the reference in "all: build"
}

func TestReferencesForVariable(t *testing.T) {
	harness := newHarness(t)

	// line 0: "CC := gcc"
	// line 1: "OUT := $(CC) -o app"
	input := "CC := gcc\nOUT := $(CC) -o app\n"
	require.NoError(t, harness.DidOpen(testURI, "makefile", input))

	// Cursor on CC definition at line 0, col 0.
	locs, err := harness.References(testURI, 0, 0, true)
	require.NoError(t, err)
	require.Len(t, locs, 2) // declaration + $(CC) ref
}

func TestReferencesNoResult(t *testing.T) {
	harness := newHarness(t)

	input := "# comment\n"
	require.NoError(t, harness.DidOpen(testURI, "makefile", input))

	locs, err := harness.References(testURI, 0, 0, true)
	require.NoError(t, err)
	assert.Nil(t, locs)
}

// Phase 3 tests: Code Actions + Formatting

func TestCodeActionAddPhony(t *testing.T) {
	harness := newHarness(t)

	input := "all: build\n\tbuild stuff\n"
	require.NoError(t, harness.DidOpen(testURI, "makefile", input))
	require.NoError(t, harness.DidSave(testURI))

	ctx := context.Background()
	diags, err := harness.WaitForDiagnostics(ctx, testURI)
	require.NoError(t, err)

	// Find the missing-phony diagnostic for "all".
	var phonyDiag *lsp.Diagnostic
	for _, d := range diags {
		if strings.Contains(d.Message, "all") && strings.Contains(d.Message, ".PHONY") {
			phonyDiag = &d
			break
		}
	}
	require.NotNil(t, phonyDiag, "expected missing-phony diagnostic for 'all'")

	// Request code actions with that diagnostic.
	actions, err := harness.CodeAction(&lsp.CodeActionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: testURI},
		Range:        phonyDiag.Range,
		Context:      lsp.CodeActionContext{Diagnostics: []lsp.Diagnostic{*phonyDiag}},
	})
	require.NoError(t, err)
	require.NotEmpty(t, actions)

	found := false
	for _, a := range actions {
		if strings.Contains(a.Title, ".PHONY") && strings.Contains(a.Title, "all") {
			found = true
			require.NotNil(t, a.Edit)
			edits := a.Edit.Changes[testURI]
			require.NotEmpty(t, edits)
			assert.Contains(t, edits[0].NewText, ".PHONY: all")
		}
	}
	assert.True(t, found, "expected a code action to add .PHONY for 'all'")
}

func TestCodeActionNoActionsForCleanDiags(t *testing.T) {
	harness := newHarness(t)

	input := ".PHONY: all\nall:\n\techo done\n"
	require.NoError(t, harness.DidOpen(testURI, "makefile", input))

	actions, err := harness.CodeAction(&lsp.CodeActionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: testURI},
		Range:        lsp.Range{},
		Context:      lsp.CodeActionContext{Diagnostics: nil},
	})
	require.NoError(t, err)
	assert.Empty(t, actions)
}

func TestFormattingTrimsTrailingWhitespace(t *testing.T) {
	harness := newHarness(t)

	input := "CC := gcc   \nall:   \n\techo done   \n"
	require.NoError(t, harness.DidOpen(testURI, "makefile", input))

	edits, err := harness.Formatting(testURI)
	require.NoError(t, err)
	require.NotEmpty(t, edits)

	// The formatted text should have no trailing whitespace.
	assert.NotContains(t, edits[0].NewText, "gcc   ")
	assert.NotContains(t, edits[0].NewText, "all:   ")
	assert.NotContains(t, edits[0].NewText, "done   ")
}

func TestFormattingEnsuresFinalNewline(t *testing.T) {
	harness := newHarness(t)

	input := "all:\n\techo done"
	require.NoError(t, harness.DidOpen(testURI, "makefile", input))

	edits, err := harness.Formatting(testURI)
	require.NoError(t, err)
	require.NotEmpty(t, edits)
	assert.True(t, strings.HasSuffix(edits[0].NewText, "\n"))
}

func TestFormattingNoChangeOnCleanFile(t *testing.T) {
	harness := newHarness(t)

	input := "all:\n\techo done\n"
	require.NoError(t, harness.DidOpen(testURI, "makefile", input))

	edits, err := harness.Formatting(testURI)
	require.NoError(t, err)
	assert.Empty(t, edits)
}

func TestFormattingNormalizesSpacesToTabs(t *testing.T) {
	harness := newHarness(t)

	input := "all:\n    echo done\n"
	require.NoError(t, harness.DidOpen(testURI, "makefile", input))

	edits, err := harness.Formatting(testURI)
	require.NoError(t, err)
	require.NotEmpty(t, edits)
	assert.Contains(t, edits[0].NewText, "\techo done")
}

func completionLabels(items []lsp.CompletionItem) []string {
	labels := make([]string, len(items))
	for i, item := range items {
		labels[i] = item.Label
	}
	return labels
}
