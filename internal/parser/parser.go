package parser

import (
	"regexp"
	"strings"

	"github.com/owenrumney/go-lsp/lsp"
	"github.com/owenrumney/make-ls/internal/model"
)

var (
	// Variable assignment: NAME = VALUE (with optional export/override prefix)
	varAssignRe = regexp.MustCompile(`^(\s*)([\w.\-]+)\s*(\?=|\+?=|::?=|!=)\s*(.*)$`)

	// Target rule: targets: [deps] or targets:: [deps]
	// Must not match variable assignments (= after :).
	targetRuleRe = regexp.MustCompile(`^([^:=\t][^=]*?)(\s*::?\s*)(.*)$`)

	// Static pattern rule: targets: target-pattern: prereq-pattern
	staticPatternRe = regexp.MustCompile(`^([^:=\t][^=]*?)\s*:\s*([^:]*%[^:]*)\s*:\s*(.*)$`)

	// Target-specific variable: target: VAR = VALUE
	targetVarRe = regexp.MustCompile(`^([^:=\t][^=]*?)\s*:\s*([\w.\-]+)\s*(\?=|\+?=|::?=|!=)\s*(.*)$`)

	// include / -include / sinclude
	includeRe = regexp.MustCompile(`^(-?\s*include|sinclude)\s+(.+)$`)

	// Conditional directives
	conditionalStartRe = regexp.MustCompile(`^(ifeq|ifneq|ifdef|ifndef)\s+(.*)$`)

	// define / endef
	defineRe = regexp.MustCompile(`^define\s+([\w.\-]+)(?:\s*(\?=|\+?=|::?=|!=|=))?\s*$`)

	// export / unexport
	exportRe = regexp.MustCompile(`^(export|unexport)(?:\s+(.*))?$`)

	// vpath
	vpathRe = regexp.MustCompile(`^vpath\s+(.*)$`)

	// Variable references $(VAR) or ${VAR}
	varRefRe = regexp.MustCompile(`\$[({]([\w.\-]+)[)}]`)

	// .PHONY declaration
	phonyRe = regexp.MustCompile(`^\.PHONY\s*:\s*(.*)$`)
)

// Parse parses a Makefile from its text content and returns the AST.
func Parse(uri lsp.DocumentURI, text string) *model.Makefile {
	p := &parser{
		uri:     uri,
		lines:   splitLines(text),
		phonies: make(map[string]bool),
	}
	p.parse()
	return &model.Makefile{
		URI:          uri,
		Targets:      p.targets,
		Variables:    p.variables,
		Includes:     p.includes,
		Conditionals: p.conditionals,
		Directives:   p.directives,
		Defines:      p.defines,
		Phonies:      p.phonies,
		Comments:     p.comments,
	}
}

type parser struct {
	uri   lsp.DocumentURI
	lines []string
	pos   int

	targets      []*model.Target
	variables    []*model.Variable
	includes     []*model.Include
	conditionals []*model.Conditional
	directives   []*model.Directive
	defines      []*model.Define
	phonies      map[string]bool
	comments     []*model.Comment

	// Currently active target for recipe collection.
	currentTarget *model.Target

	// Accumulated comment block (for doc comments).
	commentBlock []string
	commentStart int
}

func (p *parser) parse() {
	for p.pos < len(p.lines) {
		p.parseLine()
	}
	p.flushTarget()
}

func (p *parser) parseLine() {
	line := p.lines[p.pos]

	// Join continuation lines.
	for strings.HasSuffix(line, "\\") && p.pos+1 < len(p.lines) {
		line = line[:len(line)-1] + p.lines[p.pos+1]
		p.pos++
	}

	startLine := p.pos

	// Empty line resets comment block and current target.
	if strings.TrimSpace(line) == "" {
		p.commentBlock = nil
		p.flushTarget()
		p.pos++
		return
	}

	// Recipe line (tab-prefixed) — must check before anything else.
	if len(line) > 0 && line[0] == '\t' && p.currentTarget != nil {
		p.currentTarget.RecipeLines = append(p.currentTarget.RecipeLines, line[1:])
		p.currentTarget.Range.End = lsp.Position{Line: startLine, Character: len(line)}
		p.pos++
		return
	}

	trimmed := strings.TrimSpace(line)

	// Comment
	if strings.HasPrefix(trimmed, "#") {
		p.comments = append(p.comments, &model.Comment{
			Text:  trimmed,
			Range: lineRange(startLine, 0, len(line)),
		})
		if p.commentBlock == nil {
			p.commentStart = startLine
		}
		text := strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
		p.commentBlock = append(p.commentBlock, text)
		p.flushTarget()
		p.pos++
		return
	}

	// From here on, non-comment non-recipe lines flush the target context.
	p.flushTarget()

	// .PHONY
	if m := phonyRe.FindStringSubmatch(trimmed); m != nil {
		for _, name := range splitFields(m[1]) {
			p.phonies[name] = true
		}
		p.commentBlock = nil
		p.pos++
		return
	}

	// define ... endef
	if m := defineRe.FindStringSubmatch(trimmed); m != nil {
		p.parseDefine(m, startLine)
		return
	}

	// Conditional directives
	if m := conditionalStartRe.FindStringSubmatch(trimmed); m != nil {
		cond := p.parseConditional(m, startLine)
		p.conditionals = append(p.conditionals, cond)
		p.commentBlock = nil
		return
	}

	if trimmed == "else" || trimmed == "endif" || strings.HasPrefix(trimmed, "else ") {
		// Stray else/endif outside of a conditional parse — skip.
		p.pos++
		return
	}

	// include / -include / sinclude
	if m := includeRe.FindStringSubmatch(trimmed); m != nil {
		optional := strings.HasPrefix(m[1], "-") || strings.HasPrefix(m[1], "sinclude")
		for _, path := range splitFields(m[2]) {
			p.includes = append(p.includes, &model.Include{
				Path:     path,
				Range:    lineRange(startLine, 0, len(line)),
				Optional: optional,
			})
		}
		p.commentBlock = nil
		p.pos++
		return
	}

	// export / unexport (may also be a variable assignment: export FOO = bar)
	if m := exportRe.FindStringSubmatch(trimmed); m != nil {
		if p.parseExportVar(m, startLine, line) {
			return
		}
		dirType := model.DirExport
		if m[1] == "unexport" {
			dirType = model.DirUnexport
		}
		p.directives = append(p.directives, &model.Directive{
			Type:  dirType,
			Args:  strings.TrimSpace(m[2]),
			Range: lineRange(startLine, 0, len(line)),
		})
		p.commentBlock = nil
		p.pos++
		return
	}

	// vpath
	if m := vpathRe.FindStringSubmatch(trimmed); m != nil {
		p.directives = append(p.directives, &model.Directive{
			Type:  model.DirVpath,
			Args:  strings.TrimSpace(m[1]),
			Range: lineRange(startLine, 0, len(line)),
		})
		p.commentBlock = nil
		p.pos++
		return
	}

	// override directive — strip prefix and re-parse as variable assignment.
	if strings.HasPrefix(trimmed, "override ") {
		rest := strings.TrimPrefix(trimmed, "override ")
		if v := p.tryParseVarAssign(rest, startLine, line); v != nil {
			v.Override = true
			p.variables = append(p.variables, v)
			p.commentBlock = nil
			p.pos++
			return
		}
	}

	// Target-specific variable: target: VAR = value
	if m := targetVarRe.FindStringSubmatch(trimmed); m != nil {
		targetName := strings.TrimSpace(m[1])
		v := &model.Variable{
			Name:        strings.TrimSpace(m[2]),
			Value:       strings.TrimSpace(m[4]),
			Op:          model.VarOp(m[3]),
			Flavour:     model.FlavourForOp(model.VarOp(m[3])),
			TargetScope: targetName,
			Range:       lineRange(startLine, 0, len(line)),
			NameRange:   nameRange(startLine, trimmed, m[2]),
			Refs:        extractVarRefs(m[4], startLine),
		}
		p.variables = append(p.variables, v)
		// Also attach to any matching target.
		for _, t := range p.targets {
			if t.Name == targetName {
				t.Variables = append(t.Variables, v)
			}
		}
		p.commentBlock = nil
		p.pos++
		return
	}

	// Variable assignment (simple, no target scope)
	if v := p.tryParseVarAssign(trimmed, startLine, line); v != nil {
		p.variables = append(p.variables, v)
		p.commentBlock = nil
		p.pos++
		return
	}

	// Static pattern rule: targets: target-pattern: prereq-pattern
	if m := staticPatternRe.FindStringSubmatch(trimmed); m != nil {
		t := &model.Target{
			Name:          strings.TrimSpace(m[1]),
			TargetPattern: strings.TrimSpace(m[2]),
			PrereqPattern: strings.TrimSpace(m[3]),
			IsPattern:     true,
			DocComment:    p.buildDocComment(),
			Range:         lineRange(startLine, 0, len(line)),
			NameRange:     nameRange(startLine, trimmed, strings.TrimSpace(m[1])),
		}
		prereqOffset := strings.LastIndex(trimmed, m[3])
		if prereqOffset < 0 {
			prereqOffset = 0
		}
		t.Deps = parseDeps(m[3], startLine, prereqOffset)
		p.targets = append(p.targets, t)
		p.currentTarget = t
		p.commentBlock = nil
		p.pos++
		return
	}

	// Target rule
	if m := targetRuleRe.FindStringSubmatch(trimmed); m != nil {
		namesPart := strings.TrimSpace(m[1])
		separator := m[2]
		depsPart := strings.TrimSpace(m[3])

		isDouble := strings.Contains(separator, "::")
		isPattern := strings.Contains(namesPart, "%")

		// Calculate column offset for deps in the full line.
		depsColOffset := len(m[1]) + len(m[2])
		// Account for leading whitespace trimmed from depsPart.
		if raw := m[3]; len(raw) > 0 {
			depsColOffset += len(raw) - len(strings.TrimLeft(raw, " \t"))
		}

		// Parse deps, splitting on | for order-only.
		deps, orderOnly := splitDepsOrderOnly(depsPart, startLine, depsColOffset)

		t := &model.Target{
			Name:          namesPart,
			Deps:          deps,
			OrderOnlyDeps: orderOnly,
			IsPattern:     isPattern,
			IsDoubleColon: isDouble,
			DocComment:    p.buildDocComment(),
			Range:         lineRange(startLine, 0, len(line)),
			NameRange:     nameRange(startLine, trimmed, namesPart),
		}
		p.targets = append(p.targets, t)
		p.currentTarget = t
		p.commentBlock = nil
		p.pos++
		return
	}

	// Unknown line — skip.
	p.commentBlock = nil
	p.pos++
}

func (p *parser) flushTarget() {
	p.currentTarget = nil
}

func (p *parser) tryParseVarAssign(s string, startLine int, fullLine string) *model.Variable {
	m := varAssignRe.FindStringSubmatch(s)
	if m == nil {
		return nil
	}
	op := model.VarOp(m[3])
	return &model.Variable{
		Name:      strings.TrimSpace(m[2]),
		Value:     strings.TrimSpace(m[4]),
		Op:        op,
		Flavour:   model.FlavourForOp(op),
		Range:     lineRange(startLine, 0, len(fullLine)),
		NameRange: nameRange(startLine, s, strings.TrimSpace(m[2])),
		Refs:      extractVarRefs(m[4], startLine),
	}
}

func (p *parser) parseExportVar(m []string, startLine int, fullLine string) bool {
	rest := strings.TrimSpace(m[2])
	if rest == "" {
		return false
	}
	v := p.tryParseVarAssign(rest, startLine, fullLine)
	if v == nil {
		return false
	}
	v.Export = true
	p.variables = append(p.variables, v)
	p.commentBlock = nil
	p.pos++
	return true
}

func (p *parser) parseDefine(m []string, startLine int) {
	name := m[1]
	op := model.VarOp("=")
	if m[2] != "" {
		op = model.VarOp(m[2])
	}

	p.pos++
	var body []string
	for p.pos < len(p.lines) {
		if strings.TrimSpace(p.lines[p.pos]) == "endef" {
			break
		}
		body = append(body, p.lines[p.pos])
		p.pos++
	}

	endLine := p.pos
	p.pos++ // skip endef

	p.defines = append(p.defines, &model.Define{
		Name:  name,
		Op:    op,
		Body:  strings.Join(body, "\n"),
		Range: lsp.Range{
			Start: lsp.Position{Line: startLine, Character: 0},
			End:   lsp.Position{Line: endLine, Character: len("endef")},
		},
	})
	p.commentBlock = nil
}

func (p *parser) parseConditional(m []string, startLine int) *model.Conditional {
	condType := parseCondType(m[1])
	cond := &model.Conditional{
		Type: condType,
		Args: strings.TrimSpace(m[2]),
		Range: lsp.Range{
			Start: lsp.Position{Line: startLine, Character: 0},
		},
	}

	p.pos++
	inElse := false

	for p.pos < len(p.lines) {
		trimmed := strings.TrimSpace(p.lines[p.pos])

		if trimmed == "endif" {
			cond.Range.End = lsp.Position{Line: p.pos, Character: len(p.lines[p.pos])}
			p.pos++
			return cond
		}

		if trimmed == "else" || strings.HasPrefix(trimmed, "else ") {
			inElse = true
			p.pos++
			continue
		}

		// Nested conditional
		if cm := conditionalStartRe.FindStringSubmatch(trimmed); cm != nil {
			nested := p.parseConditional(cm, p.pos)
			node := model.Node{Conditional: nested}
			if inElse {
				cond.ElseNodes = append(cond.ElseNodes, node)
			} else {
				cond.ThenNodes = append(cond.ThenNodes, node)
			}
			continue
		}

		// Parse content inside conditional branches — we collect nodes for
		// variables and targets found inside.
		node := p.parseConditionalLine(trimmed, p.pos)
		if node != nil {
			if inElse {
				cond.ElseNodes = append(cond.ElseNodes, *node)
			} else {
				cond.ThenNodes = append(cond.ThenNodes, *node)
			}
		}
		p.pos++
	}

	// Unterminated conditional — set end to last line.
	cond.Range.End = lsp.Position{Line: p.pos - 1, Character: 0}
	return cond
}

func (p *parser) parseConditionalLine(trimmed string, lineNum int) *model.Node {
	fullLine := p.lines[lineNum]

	// Variable assignment
	if v := p.tryParseVarAssign(trimmed, lineNum, fullLine); v != nil {
		p.variables = append(p.variables, v)
		return &model.Node{Variable: v}
	}

	// Include
	if m := includeRe.FindStringSubmatch(trimmed); m != nil {
		optional := strings.HasPrefix(m[1], "-") || strings.HasPrefix(m[1], "sinclude")
		inc := &model.Include{
			Path:     strings.TrimSpace(m[2]),
			Range:    lineRange(lineNum, 0, len(fullLine)),
			Optional: optional,
		}
		p.includes = append(p.includes, inc)
		return &model.Node{Include: inc}
	}

	return nil
}

func parseCondType(s string) model.ConditionalType {
	switch s {
	case "ifeq":
		return model.CondIfeq
	case "ifneq":
		return model.CondIfneq
	case "ifdef":
		return model.CondIfdef
	case "ifndef":
		return model.CondIfndef
	default:
		return model.CondIfeq
	}
}

func (p *parser) buildDocComment() string {
	if len(p.commentBlock) == 0 {
		return ""
	}
	return strings.Join(p.commentBlock, "\n")
}

// splitDepsOrderOnly splits a dep string on | into normal and order-only deps.
// colOffset is the character offset where the dep string starts in the full line.
func splitDepsOrderOnly(s string, line, colOffset int) ([]*model.DepRef, []*model.DepRef) {
	parts := strings.SplitN(s, "|", 2)
	deps := parseDeps(parts[0], line, colOffset)
	var orderOnly []*model.DepRef
	if len(parts) > 1 {
		pipeIdx := strings.Index(s, "|")
		orderOnly = parseDeps(parts[1], line, colOffset+pipeIdx+1)
	}
	return deps, orderOnly
}

func parseDeps(s string, line, colOffset int) []*model.DepRef {
	var deps []*model.DepRef
	offset := 0
	for _, name := range splitFields(s) {
		idx := strings.Index(s[offset:], name)
		if idx >= 0 {
			col := colOffset + offset + idx
			deps = append(deps, &model.DepRef{
				Name: name,
				Range: lsp.Range{
					Start: lsp.Position{Line: line, Character: col},
					End:   lsp.Position{Line: line, Character: col + len(name)},
				},
			})
			offset = offset + idx + len(name)
		}
	}
	return deps
}

func extractVarRefs(s string, line int) []*model.VarRef {
	matches := varRefRe.FindAllStringSubmatchIndex(s, -1)
	if matches == nil {
		return nil
	}
	var refs []*model.VarRef
	for _, m := range matches {
		name := s[m[2]:m[3]]
		refs = append(refs, &model.VarRef{
			Name: name,
			Range: lsp.Range{
				Start: lsp.Position{Line: line, Character: m[0]},
				End:   lsp.Position{Line: line, Character: m[1]},
			},
		})
	}
	return refs
}

func splitFields(s string) []string {
	return strings.Fields(strings.TrimSpace(s))
}

func lineRange(line, startChar, endChar int) lsp.Range {
	return lsp.Range{
		Start: lsp.Position{Line: line, Character: startChar},
		End:   lsp.Position{Line: line, Character: endChar},
	}
}

func nameRange(line int, fullLine, name string) lsp.Range {
	idx := strings.Index(fullLine, name)
	if idx < 0 {
		idx = 0
	}
	return lsp.Range{
		Start: lsp.Position{Line: line, Character: idx},
		End:   lsp.Position{Line: line, Character: idx + len(name)},
	}
}

func splitLines(text string) []string {
	lines := strings.Split(text, "\n")
	// Remove trailing empty line from final newline.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
