package completion

import (
	"testing"

	"github.com/owenrumney/go-lsp/lsp"
	"github.com/owenrumney/make-ls/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testURI = lsp.DocumentURI("file:///test/Makefile")

func TestCompleteVarRef(t *testing.T) {
	input := "CC := gcc\nOUT := $("
	mf := parser.Parse(testURI, input)
	items := Complete(mf, input, lsp.Position{Line: 1, Character: len("OUT := $(")})

	require.NotEmpty(t, items)
	// Should include user-defined CC.
	found := false
	for _, item := range items {
		if item.Label == "CC" {
			found = true
			break
		}
	}
	assert.True(t, found, "should suggest CC")
}

func TestCompleteVarRefWithPartial(t *testing.T) {
	input := "CC := gcc\nCXX := g++\nOUT := $(CX"
	mf := parser.Parse(testURI, input)
	items := Complete(mf, input, lsp.Position{Line: 2, Character: len("OUT := $(CX")})

	// Should include CXX but not CC.
	labels := itemLabels(items)
	assert.Contains(t, labels, "CXX")
	assert.NotContains(t, labels, "CC")
}

func TestCompleteVarRefIncludesBuiltins(t *testing.T) {
	input := "OUT := $(MA"
	mf := parser.Parse(testURI, input)
	items := Complete(mf, input, lsp.Position{Line: 0, Character: len("OUT := $(MA")})

	labels := itemLabels(items)
	assert.Contains(t, labels, "MAKE")
	assert.Contains(t, labels, "MAKECMDGOALS")
}

func TestCompleteVarRefIncludesFunctions(t *testing.T) {
	input := "OUT := $(wild"
	mf := parser.Parse(testURI, input)
	items := Complete(mf, input, lsp.Position{Line: 0, Character: len("OUT := $(wild")})

	labels := itemLabels(items)
	assert.Contains(t, labels, "wildcard")
}

func TestCompleteInRecipe(t *testing.T) {
	input := "build:\n\t"
	mf := parser.Parse(testURI, input)
	items := Complete(mf, input, lsp.Position{Line: 1, Character: 1})

	require.NotEmpty(t, items)
	// Should suggest automatic variables.
	labels := itemLabels(items)
	assert.Contains(t, labels, "$@")
	assert.Contains(t, labels, "$<")
	assert.Contains(t, labels, "$^")
}

func TestCompleteInDeps(t *testing.T) {
	input := "build:\n\techo done\n\nall: "
	mf := parser.Parse(testURI, input)
	items := Complete(mf, input, lsp.Position{Line: 3, Character: len("all: ")})

	labels := itemLabels(items)
	assert.Contains(t, labels, "build")
}

func TestCompleteAtLineStart(t *testing.T) {
	input := ""
	mf := parser.Parse(testURI, input)
	items := Complete(mf, input, lsp.Position{Line: 0, Character: 0})

	labels := itemLabels(items)
	assert.Contains(t, labels, "include")
	assert.Contains(t, labels, ".PHONY:")
	assert.Contains(t, labels, "ifeq")
}

func TestCompleteEmptyVarRef(t *testing.T) {
	input := "CC := gcc\nOUT := $("
	mf := parser.Parse(testURI, input)
	items := Complete(mf, input, lsp.Position{Line: 1, Character: len("OUT := $(")})

	// Should include everything: user vars, builtins, functions.
	require.True(t, len(items) > 10, "expected many completions, got %d", len(items))
}

func TestCompleteNoTargetPatternRules(t *testing.T) {
	input := "%.o: %.c\n\t$(CC) -c $<\n\nbuild:\n\techo done\n\nall: "
	mf := parser.Parse(testURI, input)
	items := Complete(mf, input, lsp.Position{Line: 6, Character: len("all: ")})

	labels := itemLabels(items)
	assert.Contains(t, labels, "build")
	assert.NotContains(t, labels, "%.o") // Pattern rules excluded.
}

func itemLabels(items []lsp.CompletionItem) []string {
	labels := make([]string, len(items))
	for i, item := range items {
		labels[i] = item.Label
	}
	return labels
}
