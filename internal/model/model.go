package model

import "github.com/owenrumney/go-lsp/lsp"

// Makefile is the top-level AST for a parsed Makefile.
type Makefile struct {
	URI          lsp.DocumentURI
	Targets      []*Target
	Variables    []*Variable
	Includes     []*Include
	Conditionals []*Conditional
	Directives   []*Directive
	Defines      []*Define
	Phonies      map[string]bool
	Comments     []*Comment
}

// Target represents a make target rule.
type Target struct {
	Name          string
	Deps          []*DepRef
	OrderOnlyDeps []*DepRef
	RecipeLines   []string
	DocComment    string
	Range         lsp.Range
	NameRange     lsp.Range

	// Pattern rule fields
	IsPattern    bool
	IsDoubleColon bool

	// Static pattern rule fields (e.g. $(OBJ): %.o: %.c)
	TargetPattern string
	PrereqPattern string

	// Target-specific variables
	Variables []*Variable
}

// Variable represents a variable assignment.
type Variable struct {
	Name     string
	Value    string
	Op       VarOp
	Flavour  VarFlavour
	Range    lsp.Range
	NameRange lsp.Range

	// Non-empty when this is a target-specific variable.
	TargetScope string

	// Override is true when prefixed with the override directive.
	Override bool

	// Export is true when prefixed with export.
	Export bool

	// VarRefs found in the value.
	Refs []*VarRef
}

// VarOp is the assignment operator.
type VarOp string

const (
	OpRecursive VarOp = "="
	OpSimple    VarOp = ":="
	OpConditional VarOp = "?="
	OpAppend    VarOp = "+="
	OpShell     VarOp = "!="
)

// VarFlavour distinguishes recursive from simply-expanded variables.
type VarFlavour int

const (
	FlavourRecursive VarFlavour = iota
	FlavourSimple
)

// DepRef is a reference to a dependency with positional info.
type DepRef struct {
	Name  string
	Range lsp.Range
}

// VarRef is a reference to a variable (e.g. $(FOO) or ${FOO}).
type VarRef struct {
	Name  string
	Range lsp.Range
}

// Include represents an include or -include directive.
type Include struct {
	Path     string
	Range    lsp.Range
	Optional bool // true for -include / sinclude
}

// Conditional represents an ifeq/ifneq/ifdef/ifndef block.
type Conditional struct {
	Type       ConditionalType
	Args       string
	Range      lsp.Range
	ThenNodes  []Node
	ElseNodes  []Node
}

// ConditionalType identifies the conditional directive.
type ConditionalType int

const (
	CondIfeq ConditionalType = iota
	CondIfneq
	CondIfdef
	CondIfndef
)

// Node is a union type for items that can appear inside conditional branches.
type Node struct {
	Target      *Target
	Variable    *Variable
	Include     *Include
	Conditional *Conditional
	Directive   *Directive
	Define      *Define
}

// Directive represents export, unexport, vpath, or override directives.
type Directive struct {
	Type  DirectiveType
	Args  string
	Range lsp.Range
}

// DirectiveType identifies the directive.
type DirectiveType int

const (
	DirExport DirectiveType = iota
	DirUnexport
	DirVpath
)

// Define represents a multi-line variable definition (define ... endef).
type Define struct {
	Name  string
	Op    VarOp
	Body  string
	Range lsp.Range
}

// Comment represents a comment line with its range.
type Comment struct {
	Text  string
	Range lsp.Range
}

// FlavourForOp returns the flavour implied by the given operator.
func FlavourForOp(op VarOp) VarFlavour {
	switch op {
	case OpSimple, OpShell:
		return FlavourSimple
	default:
		return FlavourRecursive
	}
}
