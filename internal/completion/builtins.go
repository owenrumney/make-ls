package completion

// AutoVar describes a Make automatic variable.
type AutoVar struct {
	Name string
	Doc  string
}

// Function describes a Make built-in function.
type Function struct {
	Name    string
	Args    string
	Doc     string
}

// BuiltinVar describes a commonly used Make built-in variable.
type BuiltinVar struct {
	Name string
	Doc  string
}

// AutoVars lists all automatic variables and their documentation.
var AutoVars = []AutoVar{
	{"@", "The file name of the target of the rule."},
	{"<", "The name of the first prerequisite."},
	{"^", "The names of all prerequisites, with spaces between them (duplicates removed)."},
	{"?", "The names of all prerequisites that are newer than the target."},
	{"*", "The stem with which an implicit rule matches."},
	{"+", "The names of all prerequisites, with spaces between them (duplicates kept)."},
	{"|", "The names of all order-only prerequisites."},
	{"@D", "The directory part of $@."},
	{"@F", "The file-within-directory part of $@."},
	{"<D", "The directory part of $<."},
	{"<F", "The file-within-directory part of $<."},
	{"^D", "The directory part of $^."},
	{"^F", "The file-within-directory part of $^."},
	{"?D", "The directory part of $?."},
	{"?F", "The file-within-directory part of $?."},
	{"*D", "The directory part of $*."},
	{"*F", "The file-within-directory part of $*."},
}

// Functions lists all built-in Make functions.
var Functions = []Function{
	{"subst", "$(subst from,to,text)", "Replace occurrences of from with to in text."},
	{"patsubst", "$(patsubst pattern,replacement,text)", "Pattern substitution: replace words matching pattern."},
	{"strip", "$(strip string)", "Remove leading/trailing whitespace and collapse internal whitespace."},
	{"findstring", "$(findstring find,in)", "Search for find in in; returns find if found, else empty."},
	{"filter", "$(filter pattern...,text)", "Keep words in text matching any of the patterns."},
	{"filter-out", "$(filter-out pattern...,text)", "Remove words in text matching any of the patterns."},
	{"sort", "$(sort list)", "Sort words lexicographically and remove duplicates."},
	{"word", "$(word n,text)", "Return the nth word of text (1-indexed)."},
	{"wordlist", "$(wordlist s,e,text)", "Return words s through e of text."},
	{"words", "$(words text)", "Return the number of words in text."},
	{"firstword", "$(firstword names...)", "Return the first word."},
	{"lastword", "$(lastword names...)", "Return the last word."},
	{"dir", "$(dir names...)", "Extract the directory part of each file name."},
	{"notdir", "$(notdir names...)", "Extract the non-directory part of each file name."},
	{"suffix", "$(suffix names...)", "Extract the suffix of each file name."},
	{"basename", "$(basename names...)", "Extract the base name (without suffix) of each file name."},
	{"addsuffix", "$(addsuffix suffix,names...)", "Append suffix to each word."},
	{"addprefix", "$(addprefix prefix,names...)", "Prepend prefix to each word."},
	{"join", "$(join list1,list2)", "Join two lists word by word."},
	{"wildcard", "$(wildcard pattern)", "Expand file name wildcards."},
	{"realpath", "$(realpath names...)", "Return the canonical absolute name for each name."},
	{"abspath", "$(abspath names...)", "Return the absolute name (not resolving symlinks)."},
	{"if", "$(if condition,then-part[,else-part])", "Conditional evaluation."},
	{"or", "$(or condition1[,condition2[,...]])", "Short-circuit OR evaluation."},
	{"and", "$(and condition1[,condition2[,...]])", "Short-circuit AND evaluation."},
	{"foreach", "$(foreach var,list,text)", "Expand text for each word in list with var set to that word."},
	{"file", "$(file op filename[,text])", "Read from or write to a file."},
	{"call", "$(call variable,param1,param2,...)", "Expand a user-defined function."},
	{"value", "$(value variable)", "Return the unexpanded value of a variable."},
	{"eval", "$(eval text)", "Parse text as makefile syntax."},
	{"origin", "$(origin variable)", "Return the origin of a variable (undefined, default, environment, etc.)."},
	{"flavor", "$(flavor variable)", "Return the flavor of a variable (undefined, recursive, simple)."},
	{"shell", "$(shell command)", "Execute a shell command and return its stdout."},
	{"error", "$(error text...)", "Generate a fatal error with text."},
	{"warning", "$(warning text...)", "Generate a warning message."},
	{"info", "$(info text...)", "Print an informational message."},
}

// BuiltinVars lists commonly used built-in variables.
var BuiltinVars = []BuiltinVar{
	{"AR", "Archive-maintaining program; default 'ar'."},
	{"AS", "Program for compiling assembly files; default 'as'."},
	{"CC", "Program for compiling C programs; default 'cc'."},
	{"CXX", "Program for compiling C++ programs; default 'g++'."},
	{"CPP", "Program for running the C preprocessor; default '$(CC) -E'."},
	{"FC", "Program for compiling Fortran programs; default 'f77'."},
	{"GET", "Program to extract a file from SCCS; default 'get'."},
	{"LEX", "Program to turn Lex grammars into source code; default 'lex'."},
	{"YACC", "Program to turn Yacc grammars into source code; default 'yacc'."},
	{"LINT", "Program to run lint on source code; default 'lint'."},
	{"MAKEFLAGS", "Flags passed to sub-makes automatically."},
	{"MAKECMDGOALS", "The targets given on the command line."},
	{"CURDIR", "The current working directory (after -C processing)."},
	{"MAKE", "The name of the make program being run."},
	{"MAKEFILE_LIST", "List of makefiles currently being read."},
	{"MAKEOVERRIDES", "Variable definitions from the command line."},
	{".DEFAULT_GOAL", "The default goal target."},
	{".RECIPEPREFIX", "The character used to introduce recipe lines (default tab)."},
	{".VARIABLES", "List of all global variables defined so far."},
	{"SHELL", "The shell program to use; default '/bin/sh'."},
	{"VPATH", "Search path for prerequisites."},
	{"ARFLAGS", "Flags for the archive program; default 'rv'."},
	{"ASFLAGS", "Extra flags for the assembler."},
	{"CFLAGS", "Extra flags for the C compiler."},
	{"CXXFLAGS", "Extra flags for the C++ compiler."},
	{"CPPFLAGS", "Extra flags for the C preprocessor."},
	{"FFLAGS", "Extra flags for the Fortran compiler."},
	{"LDFLAGS", "Extra flags for the linker."},
	{"LDLIBS", "Libraries to link."},
	{"LFLAGS", "Extra flags for Lex."},
	{"YFLAGS", "Extra flags for Yacc."},
}

// autoVarSet is a set of automatic variable names for fast lookup.
var autoVarSet map[string]bool

// builtinVarSet is a set of built-in variable names for fast lookup.
var builtinVarSet map[string]bool

// functionSet is a set of built-in function names for fast lookup.
var functionSet map[string]bool

func init() {
	autoVarSet = make(map[string]bool, len(AutoVars))
	for _, v := range AutoVars {
		autoVarSet[v.Name] = true
	}
	builtinVarSet = make(map[string]bool, len(BuiltinVars))
	for _, v := range BuiltinVars {
		builtinVarSet[v.Name] = true
	}
	functionSet = make(map[string]bool, len(Functions))
	for _, f := range Functions {
		functionSet[f.Name] = true
	}
}

// IsAutoVar reports whether name is an automatic variable.
func IsAutoVar(name string) bool {
	return autoVarSet[name]
}

// IsBuiltinVar reports whether name is a known built-in variable.
func IsBuiltinVar(name string) bool {
	return builtinVarSet[name]
}

// IsFunction reports whether name is a built-in function.
func IsFunction(name string) bool {
	return functionSet[name]
}

// IsKnownName reports whether name is any known automatic variable,
// built-in variable, or function.
func IsKnownName(name string) bool {
	return autoVarSet[name] || builtinVarSet[name] || functionSet[name]
}
