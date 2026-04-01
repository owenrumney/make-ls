package analysis

import (
	"testing"

	"github.com/owenrumney/go-lsp/lsp"
	"github.com/owenrumney/make-ls/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testURI = lsp.DocumentURI("file:///test/Makefile")

func TestDiagnoseCleanMakefile(t *testing.T) {
	input := `.PHONY: all clean

CC := gcc

all: main.o
	$(CC) -o app $^

clean:
	rm -f app
`
	mf := parser.Parse(testURI, input)
	diags := Diagnose(mf)
	assert.Empty(t, diags)
}

func TestDiagnoseUndefinedTargetDep(t *testing.T) {
	input := `all: build test

build:
	go build
`
	mf := parser.Parse(testURI, input)
	diags := Diagnose(mf)

	// "test" is undefined as a target, "build" is defined.
	// "all" and "test" also trigger missing .PHONY hints.
	var undefinedTarget []lsp.Diagnostic
	for _, d := range diags {
		if d.Severity != nil && *d.Severity == lsp.SeverityWarning {
			undefinedTarget = append(undefinedTarget, d)
		}
	}
	require.Len(t, undefinedTarget, 1)
	assert.Contains(t, undefinedTarget[0].Message, "test")
}

func TestDiagnoseSkipsPatternDeps(t *testing.T) {
	input := `%.o: %.c
	$(CC) -c $<
`
	mf := parser.Parse(testURI, input)
	diags := Diagnose(mf)

	// Pattern deps with % should not trigger undefined target warnings.
	for _, d := range diags {
		if d.Severity != nil && *d.Severity == lsp.SeverityWarning {
			assert.NotContains(t, d.Message, "undefined target")
		}
	}
}

func TestDiagnoseSkipsFileDeps(t *testing.T) {
	input := `all: main.o utils.o
	$(CC) -o app $^
`
	mf := parser.Parse(testURI, input)
	diags := Diagnose(mf)

	// .o files should not trigger undefined target warnings.
	for _, d := range diags {
		if d.Severity != nil && *d.Severity == lsp.SeverityWarning {
			assert.NotContains(t, d.Message, "undefined target")
		}
	}
}

func TestDiagnoseSkipsVarRefDeps(t *testing.T) {
	input := `all: $(OBJECTS)
	$(CC) -o app $^
`
	mf := parser.Parse(testURI, input)
	diags := Diagnose(mf)

	for _, d := range diags {
		if d.Severity != nil && *d.Severity == lsp.SeverityWarning {
			assert.NotContains(t, d.Message, "undefined target")
		}
	}
}

func TestDiagnoseUndefinedVarRef(t *testing.T) {
	input := `OUT := $(BUILD_DIR)/app`
	mf := parser.Parse(testURI, input)
	diags := Diagnose(mf)

	var undefinedVar []lsp.Diagnostic
	for _, d := range diags {
		if d.Severity != nil && *d.Severity == lsp.SeverityWarning {
			undefinedVar = append(undefinedVar, d)
		}
	}
	require.Len(t, undefinedVar, 1)
	assert.Contains(t, undefinedVar[0].Message, "BUILD_DIR")
}

func TestDiagnoseSkipsBuiltinVarRef(t *testing.T) {
	input := `OUT := $(CC) -o app`
	mf := parser.Parse(testURI, input)
	diags := Diagnose(mf)

	for _, d := range diags {
		assert.NotContains(t, d.Message, "CC")
	}
}

func TestDiagnoseSkipsRecursiveVarRefs(t *testing.T) {
	input := `OUT = $(WHATEVER)/app`
	mf := parser.Parse(testURI, input)
	diags := Diagnose(mf)

	// Recursive vars (=) resolve lazily, so don't warn about undefined refs.
	for _, d := range diags {
		assert.NotContains(t, d.Message, "WHATEVER")
	}
}

func TestDiagnoseSkipsDefinedVarRef(t *testing.T) {
	input := `BUILD_DIR := build
OUT := $(BUILD_DIR)/app
`
	mf := parser.Parse(testURI, input)
	diags := Diagnose(mf)

	for _, d := range diags {
		assert.NotContains(t, d.Message, "BUILD_DIR")
	}
}

func TestDiagnoseSkipsConditionallyDefinedVar(t *testing.T) {
	input := `ifdef USE_CLANG
CC := clang
endif

OUT := $(CC)
`
	mf := parser.Parse(testURI, input)
	diags := Diagnose(mf)

	// CC is defined inside a conditional, so it should be considered defined.
	for _, d := range diags {
		assert.NotContains(t, d.Message, "undefined variable: CC")
	}
}

func TestDiagnoseMissingPhony(t *testing.T) {
	input := `all: build
	echo done

clean:
	rm -rf build
`
	mf := parser.Parse(testURI, input)
	diags := Diagnose(mf)

	var hints []lsp.Diagnostic
	for _, d := range diags {
		if d.Severity != nil && *d.Severity == lsp.SeverityHint {
			hints = append(hints, d)
		}
	}
	require.Len(t, hints, 2)

	names := map[string]bool{}
	for _, h := range hints {
		if assert.Contains(t, h.Message, ".PHONY") {
			// Extract the target name from the message.
			for _, name := range []string{"all", "clean"} {
				if assert.True(t, true) && contains(h.Message, name) {
					names[name] = true
				}
			}
		}
	}
	assert.True(t, names["all"])
	assert.True(t, names["clean"])
}

func TestDiagnoseNoMissingPhonyWhenDeclared(t *testing.T) {
	input := `.PHONY: all clean

all:
	echo done

clean:
	rm -rf build
`
	mf := parser.Parse(testURI, input)
	diags := Diagnose(mf)

	for _, d := range diags {
		if d.Severity != nil && *d.Severity == lsp.SeverityHint {
			t.Errorf("unexpected hint: %s", d.Message)
		}
	}
}

func TestDiagnoseNoPhonyHintForPatternRules(t *testing.T) {
	input := `%.o: %.c
	$(CC) -c $<
`
	mf := parser.Parse(testURI, input)
	diags := Diagnose(mf)

	for _, d := range diags {
		if d.Severity != nil && *d.Severity == lsp.SeverityHint {
			t.Errorf("unexpected hint for pattern rule: %s", d.Message)
		}
	}
}

func TestDiagnoseVarDefinedViaDefine(t *testing.T) {
	input := `define HELP_MSG
usage info
endef

OUT := $(HELP_MSG)
`
	mf := parser.Parse(testURI, input)
	diags := Diagnose(mf)

	for _, d := range diags {
		assert.NotContains(t, d.Message, "HELP_MSG")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
