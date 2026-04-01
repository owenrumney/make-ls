package parser

import (
	"testing"

	"github.com/owenrumney/go-lsp/lsp"
	"github.com/owenrumney/make-ls/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testURI = lsp.DocumentURI("file:///test/Makefile")

func TestParseSimpleTarget(t *testing.T) {
	input := `all: build test
	echo "done"
`
	m := Parse(testURI, input)

	require.Len(t, m.Targets, 1)
	tgt := m.Targets[0]
	assert.Equal(t, "all", tgt.Name)
	require.Len(t, tgt.Deps, 2)
	assert.Equal(t, "build", tgt.Deps[0].Name)
	assert.Equal(t, "test", tgt.Deps[1].Name)
	require.Len(t, tgt.RecipeLines, 1)
	assert.Equal(t, `echo "done"`, tgt.RecipeLines[0])
}

func TestParseMultipleTargets(t *testing.T) {
	input := `build:
	go build ./...

test:
	go test ./...
`
	m := Parse(testURI, input)

	require.Len(t, m.Targets, 2)
	assert.Equal(t, "build", m.Targets[0].Name)
	assert.Equal(t, "test", m.Targets[1].Name)
	assert.Equal(t, []string{"go build ./..."}, m.Targets[0].RecipeLines)
	assert.Equal(t, []string{"go test ./..."}, m.Targets[1].RecipeLines)
}

func TestParseVariableAssignments(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		varName string
		value   string
		op      model.VarOp
		flavour model.VarFlavour
	}{
		{"recursive", "CC = gcc", "CC", "gcc", model.OpRecursive, model.FlavourRecursive},
		{"simple", "CC := gcc", "CC", "gcc", model.OpSimple, model.FlavourSimple},
		{"conditional", "CC ?= gcc", "CC", "gcc", model.OpConditional, model.FlavourRecursive},
		{"append", "CFLAGS += -Wall", "CFLAGS", "-Wall", model.OpAppend, model.FlavourRecursive},
		{"shell", "DATE != date", "DATE", "date", model.OpShell, model.FlavourSimple},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Parse(testURI, tt.input)
			require.Len(t, m.Variables, 1)
			v := m.Variables[0]
			assert.Equal(t, tt.varName, v.Name)
			assert.Equal(t, tt.value, v.Value)
			assert.Equal(t, tt.op, v.Op)
			assert.Equal(t, tt.flavour, v.Flavour)
		})
	}
}

func TestParseVarRefs(t *testing.T) {
	input := `OUT = $(BUILD_DIR)/$(NAME)`
	m := Parse(testURI, input)

	require.Len(t, m.Variables, 1)
	refs := m.Variables[0].Refs
	require.Len(t, refs, 2)
	assert.Equal(t, "BUILD_DIR", refs[0].Name)
	assert.Equal(t, "NAME", refs[1].Name)
}

func TestParseVarRefsBraces(t *testing.T) {
	input := `OUT = ${BUILD_DIR}/${NAME}`
	m := Parse(testURI, input)

	require.Len(t, m.Variables, 1)
	refs := m.Variables[0].Refs
	require.Len(t, refs, 2)
	assert.Equal(t, "BUILD_DIR", refs[0].Name)
	assert.Equal(t, "NAME", refs[1].Name)
}

func TestParsePhony(t *testing.T) {
	input := `.PHONY: all clean test`
	m := Parse(testURI, input)

	assert.True(t, m.Phonies["all"])
	assert.True(t, m.Phonies["clean"])
	assert.True(t, m.Phonies["test"])
	assert.False(t, m.Phonies["build"])
}

func TestParseInclude(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		path     string
		optional bool
	}{
		{"include", "include config.mk", "config.mk", false},
		{"dash include", "-include config.mk", "config.mk", true},
		{"sinclude", "sinclude config.mk", "config.mk", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Parse(testURI, tt.input)
			require.Len(t, m.Includes, 1)
			assert.Equal(t, tt.path, m.Includes[0].Path)
			assert.Equal(t, tt.optional, m.Includes[0].Optional)
		})
	}
}

func TestParseMultipleIncludes(t *testing.T) {
	input := `include foo.mk bar.mk`
	m := Parse(testURI, input)

	require.Len(t, m.Includes, 2)
	assert.Equal(t, "foo.mk", m.Includes[0].Path)
	assert.Equal(t, "bar.mk", m.Includes[1].Path)
}

func TestParseDocComment(t *testing.T) {
	input := `# Build the project
# with optimizations
build:
	go build ./...
`
	m := Parse(testURI, input)

	require.Len(t, m.Targets, 1)
	assert.Equal(t, "Build the project\nwith optimizations", m.Targets[0].DocComment)
}

func TestParseNoDocCommentAfterBlankLine(t *testing.T) {
	input := `# This is a standalone comment

build:
	go build ./...
`
	m := Parse(testURI, input)

	require.Len(t, m.Targets, 1)
	assert.Equal(t, "", m.Targets[0].DocComment)
}

func TestParsePatternRule(t *testing.T) {
	input := `%.o: %.c
	$(CC) -c $< -o $@
`
	m := Parse(testURI, input)

	require.Len(t, m.Targets, 1)
	tgt := m.Targets[0]
	assert.True(t, tgt.IsPattern)
	assert.Equal(t, "%.o", tgt.Name)
}

func TestParseDoubleColonRule(t *testing.T) {
	input := `all:: build
	echo "first"
`
	m := Parse(testURI, input)

	require.Len(t, m.Targets, 1)
	assert.True(t, m.Targets[0].IsDoubleColon)
}

func TestParseStaticPatternRule(t *testing.T) {
	input := `$(OBJECTS): %.o: %.c
	$(CC) -c $< -o $@
`
	m := Parse(testURI, input)

	require.Len(t, m.Targets, 1)
	tgt := m.Targets[0]
	assert.True(t, tgt.IsPattern)
	assert.Equal(t, "$(OBJECTS)", tgt.Name)
	assert.Equal(t, "%.o", tgt.TargetPattern)
	assert.Equal(t, "%.c", tgt.PrereqPattern)
}

func TestParseOrderOnlyDeps(t *testing.T) {
	input := `build: main.o utils.o | builddir
	$(CC) -o $@ $^
`
	m := Parse(testURI, input)

	require.Len(t, m.Targets, 1)
	tgt := m.Targets[0]
	require.Len(t, tgt.Deps, 2)
	assert.Equal(t, "main.o", tgt.Deps[0].Name)
	assert.Equal(t, "utils.o", tgt.Deps[1].Name)
	require.Len(t, tgt.OrderOnlyDeps, 1)
	assert.Equal(t, "builddir", tgt.OrderOnlyDeps[0].Name)
}

func TestParseTargetSpecificVar(t *testing.T) {
	input := `build: CC = clang`
	m := Parse(testURI, input)

	require.Len(t, m.Variables, 1)
	v := m.Variables[0]
	assert.Equal(t, "CC", v.Name)
	assert.Equal(t, "clang", v.Value)
	assert.Equal(t, "build", v.TargetScope)
}

func TestParseExportVar(t *testing.T) {
	input := `export PATH := /usr/bin`
	m := Parse(testURI, input)

	require.Len(t, m.Variables, 1)
	v := m.Variables[0]
	assert.Equal(t, "PATH", v.Name)
	assert.True(t, v.Export)
}

func TestParseExportDirective(t *testing.T) {
	input := `export FOO`
	m := Parse(testURI, input)

	// "export FOO" without assignment is a directive, not a variable.
	assert.Empty(t, m.Variables)
	require.Len(t, m.Directives, 1)
	assert.Equal(t, model.DirExport, m.Directives[0].Type)
	assert.Equal(t, "FOO", m.Directives[0].Args)
}

func TestParseUnexportDirective(t *testing.T) {
	input := `unexport FOO`
	m := Parse(testURI, input)

	require.Len(t, m.Directives, 1)
	assert.Equal(t, model.DirUnexport, m.Directives[0].Type)
}

func TestParseOverrideVar(t *testing.T) {
	input := `override CC = gcc`
	m := Parse(testURI, input)

	require.Len(t, m.Variables, 1)
	v := m.Variables[0]
	assert.Equal(t, "CC", v.Name)
	assert.True(t, v.Override)
}

func TestParseVpathDirective(t *testing.T) {
	input := `vpath %.c src`
	m := Parse(testURI, input)

	require.Len(t, m.Directives, 1)
	d := m.Directives[0]
	assert.Equal(t, model.DirVpath, d.Type)
	assert.Equal(t, "%.c src", d.Args)
}

func TestParseDefine(t *testing.T) {
	input := `define HELP_MSG
Usage: make [target]
Targets: all, clean, test
endef
`
	m := Parse(testURI, input)

	require.Len(t, m.Defines, 1)
	d := m.Defines[0]
	assert.Equal(t, "HELP_MSG", d.Name)
	assert.Equal(t, model.VarOp("="), d.Op)
	assert.Equal(t, "Usage: make [target]\nTargets: all, clean, test", d.Body)
}

func TestParseDefineWithOp(t *testing.T) {
	input := `define HELP_MSG :=
some text
endef
`
	m := Parse(testURI, input)

	require.Len(t, m.Defines, 1)
	assert.Equal(t, model.OpSimple, m.Defines[0].Op)
}

func TestParseConditionalIfeq(t *testing.T) {
	input := `ifeq ($(OS),Linux)
CC = gcc
else
CC = clang
endif
`
	m := Parse(testURI, input)

	require.Len(t, m.Conditionals, 1)
	c := m.Conditionals[0]
	assert.Equal(t, model.CondIfeq, c.Type)

	// Variables inside conditionals are also collected at top level.
	require.Len(t, m.Variables, 2)
	assert.Equal(t, "CC", m.Variables[0].Name)
	assert.Equal(t, "gcc", m.Variables[0].Value)
	assert.Equal(t, "CC", m.Variables[1].Name)
	assert.Equal(t, "clang", m.Variables[1].Value)

	// Then/else branches have nodes.
	require.Len(t, c.ThenNodes, 1)
	require.Len(t, c.ElseNodes, 1)
	assert.NotNil(t, c.ThenNodes[0].Variable)
	assert.NotNil(t, c.ElseNodes[0].Variable)
}

func TestParseConditionalIfdef(t *testing.T) {
	input := `ifdef DEBUG
CFLAGS += -g
endif
`
	m := Parse(testURI, input)

	require.Len(t, m.Conditionals, 1)
	assert.Equal(t, model.CondIfdef, m.Conditionals[0].Type)
	require.Len(t, m.Variables, 1)
}

func TestParseNestedConditional(t *testing.T) {
	input := `ifeq ($(OS),Linux)
ifdef DEBUG
CFLAGS += -g
endif
endif
`
	m := Parse(testURI, input)

	require.Len(t, m.Conditionals, 1)
	outer := m.Conditionals[0]
	require.Len(t, outer.ThenNodes, 1)
	assert.NotNil(t, outer.ThenNodes[0].Conditional)
	nested := outer.ThenNodes[0].Conditional
	assert.Equal(t, model.CondIfdef, nested.Type)
}

func TestParseLineContinuation(t *testing.T) {
	input := "SOURCES = foo.c \\\nbar.c \\\nbaz.c"
	m := Parse(testURI, input)

	require.Len(t, m.Variables, 1)
	assert.Equal(t, "SOURCES", m.Variables[0].Name)
	assert.Equal(t, "foo.c bar.c baz.c", m.Variables[0].Value)
}

func TestParseComments(t *testing.T) {
	input := `# first comment
# second comment
`
	m := Parse(testURI, input)

	require.Len(t, m.Comments, 2)
	assert.Equal(t, "# first comment", m.Comments[0].Text)
	assert.Equal(t, "# second comment", m.Comments[1].Text)
}

func TestParseTargetNoDeps(t *testing.T) {
	input := `clean:
	rm -rf build/
`
	m := Parse(testURI, input)

	require.Len(t, m.Targets, 1)
	assert.Equal(t, "clean", m.Targets[0].Name)
	assert.Empty(t, m.Targets[0].Deps)
	assert.Equal(t, []string{"rm -rf build/"}, m.Targets[0].RecipeLines)
}

func TestParseMultiLineRecipe(t *testing.T) {
	input := `build:
	mkdir -p out
	go build -o out/app ./cmd/app
	echo "done"
`
	m := Parse(testURI, input)

	require.Len(t, m.Targets, 1)
	require.Len(t, m.Targets[0].RecipeLines, 3)
}

func TestParseTargetPositions(t *testing.T) {
	input := `build: deps
	go build
`
	m := Parse(testURI, input)

	require.Len(t, m.Targets, 1)
	tgt := m.Targets[0]
	assert.Equal(t, 0, tgt.Range.Start.Line)
	assert.Equal(t, 0, tgt.NameRange.Start.Line)
	assert.Equal(t, 0, tgt.NameRange.Start.Character)
	assert.Equal(t, 5, tgt.NameRange.End.Character) // len("build")
}

func TestParseDepPositions(t *testing.T) {
	input := `all: build test`
	m := Parse(testURI, input)

	require.Len(t, m.Targets, 1)
	require.Len(t, m.Targets[0].Deps, 2)

	buildDep := m.Targets[0].Deps[0]
	assert.Equal(t, 0, buildDep.Range.Start.Line)

	testDep := m.Targets[0].Deps[1]
	assert.Equal(t, 0, testDep.Range.Start.Line)
	// "test" starts after "build "
	assert.True(t, testDep.Range.Start.Character > buildDep.Range.Start.Character)
}

func TestParseEmptyInput(t *testing.T) {
	m := Parse(testURI, "")
	assert.Empty(t, m.Targets)
	assert.Empty(t, m.Variables)
	assert.NotNil(t, m.Phonies)
}

func TestParseConditionalInclude(t *testing.T) {
	input := `ifdef USE_EXTRA
include extra.mk
endif
`
	m := Parse(testURI, input)

	require.Len(t, m.Conditionals, 1)
	require.Len(t, m.Conditionals[0].ThenNodes, 1)
	assert.NotNil(t, m.Conditionals[0].ThenNodes[0].Include)
	assert.Equal(t, "extra.mk", m.Conditionals[0].ThenNodes[0].Include.Path)

	// Also collected at top level.
	require.Len(t, m.Includes, 1)
}

func TestParseTargetSpecificVarAttachesToTarget(t *testing.T) {
	input := `build:
	go build

build: GOFLAGS = -v
`
	m := Parse(testURI, input)

	require.Len(t, m.Targets, 1)
	require.Len(t, m.Variables, 1)
	assert.Equal(t, "build", m.Variables[0].TargetScope)

	// Variable should also be attached to the target.
	require.Len(t, m.Targets[0].Variables, 1)
	assert.Equal(t, "GOFLAGS", m.Targets[0].Variables[0].Name)
}

func TestParseComplexMakefile(t *testing.T) {
	input := `.PHONY: all clean test

CC := gcc
CFLAGS = -Wall -Werror

# Build everything
all: main.o utils.o
	$(CC) $(CFLAGS) -o app $^

%.o: %.c
	$(CC) $(CFLAGS) -c $< -o $@

clean:
	rm -f *.o app

ifeq ($(DEBUG),1)
CFLAGS += -g
endif

include config.mk
`
	m := Parse(testURI, input)

	assert.True(t, m.Phonies["all"])
	assert.True(t, m.Phonies["clean"])
	assert.True(t, m.Phonies["test"])

	assert.Len(t, m.Variables, 3) // CC, CFLAGS, CFLAGS (in conditional)
	assert.Len(t, m.Targets, 3)  // all, %.o, clean
	assert.Len(t, m.Includes, 1)
	assert.Len(t, m.Conditionals, 1)

	// "all" target has doc comment.
	allTarget := m.Targets[0]
	assert.Equal(t, "all", allTarget.Name)
	assert.Equal(t, "Build everything", allTarget.DocComment)

	// Pattern rule.
	patternTarget := m.Targets[1]
	assert.True(t, patternTarget.IsPattern)
}
