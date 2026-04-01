package analysis

import (
	"strings"

	"github.com/owenrumney/go-lsp/lsp"
	"github.com/owenrumney/make-ls/internal/completion"
	"github.com/owenrumney/make-ls/internal/model"
)

// conventionalPhonies are target names that are almost always phony.
var conventionalPhonies = map[string]bool{
	"all": true, "clean": true, "install": true, "uninstall": true,
	"test": true, "check": true, "dist": true, "distclean": true,
	"lint": true, "fmt": true, "help": true, "run": true,
}

// Diagnose runs all diagnostic checks on a parsed Makefile and returns LSP diagnostics.
func Diagnose(mf *model.Makefile) []lsp.Diagnostic {
	//nolint:prealloc
	var diags []lsp.Diagnostic
	diags = append(diags, checkSpacesInRecipes(mf)...)
	diags = append(diags, checkUndefinedTargetDeps(mf)...)
	diags = append(diags, checkUndefinedVarRefs(mf)...)
	diags = append(diags, checkMissingPhony(mf)...)
	return diags
}

// checkSpacesInRecipes flags recipe lines that start with spaces instead of tabs.
func checkSpacesInRecipes(mf *model.Makefile) []lsp.Diagnostic {
	var diags []lsp.Diagnostic
	for _, t := range mf.Targets {
		recipeLine := t.Range.Start.Line + 1
		for i, line := range t.RecipeLines {
			_ = line
			// The parser strips the leading tab. We need to check the original
			// lines if available, but the parser already requires a tab to
			// collect recipe lines. However, lines that have spaces instead of
			// tabs won't be collected as recipe lines at all — they'll be
			// unknown lines. So this check looks at what the parser DID collect
			// but we flag if the original content (before the tab) had issues.
			// Since the parser only collects tab-prefixed lines, any collected
			// recipe line is fine. This check is a no-op for now but we keep
			// the structure for future raw-line access.
			_ = recipeLine + i
		}
	}
	return diags
}

// checkUndefinedTargetDeps warns about deps that reference undefined targets.
func checkUndefinedTargetDeps(mf *model.Makefile) []lsp.Diagnostic {
	targetSet := make(map[string]bool, len(mf.Targets))
	for _, t := range mf.Targets {
		targetSet[t.Name] = true
	}

	var diags []lsp.Diagnostic
	for _, t := range mf.Targets {
		for _, dep := range append(t.Deps, t.OrderOnlyDeps...) {
			if shouldSkipDepCheck(dep.Name) {
				continue
			}
			if !targetSet[dep.Name] {
				sev := lsp.SeverityWarning
				diags = append(diags, lsp.Diagnostic{
					Range:    dep.Range,
					Severity: &sev,
					Source:   "make-ls",
					Message:  "undefined target: " + dep.Name,
				})
			}
		}
	}
	return diags
}

// shouldSkipDepCheck returns true for deps that shouldn't trigger undefined warnings.
func shouldSkipDepCheck(name string) bool {
	if strings.Contains(name, "%") || strings.Contains(name, "*") {
		return true
	}
	if strings.Contains(name, "$(") || strings.Contains(name, "${") {
		return true
	}
	// File-like deps (with extensions) are not target references.
	if strings.Contains(name, ".") {
		return true
	}
	return false
}

// checkUndefinedVarRefs warns about references to undefined variables.
func checkUndefinedVarRefs(mf *model.Makefile) []lsp.Diagnostic {
	defined := collectDefinedVars(mf)

	var diags []lsp.Diagnostic

	for _, v := range mf.Variables {
		for _, ref := range v.Refs {
			if shouldSkipVarRefCheck(ref.Name, defined, v.Flavour) {
				continue
			}
			sev := lsp.SeverityWarning
			diags = append(diags, lsp.Diagnostic{
				Range:    ref.Range,
				Severity: &sev,
				Source:   "make-ls",
				Message:  "undefined variable: " + ref.Name,
			})
		}
	}
	return diags
}

// collectDefinedVars builds a set of all variable names defined in the Makefile.
func collectDefinedVars(mf *model.Makefile) map[string]bool {
	defined := make(map[string]bool)
	for _, v := range mf.Variables {
		defined[v.Name] = true
	}
	for _, d := range mf.Defines {
		defined[d.Name] = true
	}
	collectConditionalVars(mf.Conditionals, defined)
	return defined
}

func collectConditionalVars(conds []*model.Conditional, defined map[string]bool) {
	for _, c := range conds {
		for _, n := range append(c.ThenNodes, c.ElseNodes...) {
			if n.Variable != nil {
				defined[n.Variable.Name] = true
			}
			if n.Conditional != nil {
				collectConditionalVars([]*model.Conditional{n.Conditional}, defined)
			}
		}
	}
}

// shouldSkipVarRefCheck returns true for var refs that shouldn't trigger warnings.
func shouldSkipVarRefCheck(name string, defined map[string]bool, flavour model.VarFlavour) bool {
	if defined[name] {
		return true
	}
	if completion.IsKnownName(name) {
		return true
	}
	// Recursive variables resolve lazily; suppress warnings for refs inside them.
	if flavour == model.FlavourRecursive {
		return true
	}
	return false
}

// checkMissingPhony hints when conventional phony targets lack .PHONY declarations.
func checkMissingPhony(mf *model.Makefile) []lsp.Diagnostic {
	var diags []lsp.Diagnostic
	for _, t := range mf.Targets {
		if t.IsPattern {
			continue
		}
		if conventionalPhonies[t.Name] && !mf.Phonies[t.Name] {
			sev := lsp.SeverityHint
			diags = append(diags, lsp.Diagnostic{
				Range:    t.NameRange,
				Severity: &sev,
				Source:   "make-ls",
				Message:  t.Name + " looks like a phony target; consider adding .PHONY: " + t.Name,
			})
		}
	}
	return diags
}
