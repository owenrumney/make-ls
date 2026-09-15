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

	// Target-specific variable: target: [private|export|override] VAR = VALUE
	targetVarRe = regexp.MustCompile(`^([^:=\t][^=]*?)\s*:\s*((?:(?:private|export|override)\s+)*)([\w.\-]+)\s*(\?=|\+?=|::?=|!=)\s*(.*)$`)

	// Leading private/override/export modifier on an assignment.
	varModifierRe = regexp.MustCompile(`^(private|override|export)\s+`)

	// include / -include / sinclude
	includeRe = regexp.MustCompile(`^(-?\s*include|sinclude)\s+(.+)$`)

	// Conditional directives
	conditionalStartRe = regexp.MustCompile(`^(ifeq|ifneq|ifdef|ifndef)\s+(.*)$`)

	// define / endef, with optional private/export/override modifier
	defineRe = regexp.MustCompile(`^(?:(private|export|override)\s+)?define\s+([\w.\-]+)(?:\s*(\?=|\+?=|::?=|!=|=))?\s*$`)

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
		PhonyRefs:    p.phonyRefs,
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
	phonyRefs    []*model.DepRef
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
	p.attachTargetVars()
}

// attachTargetVars links target-specific variables to their targets. Runs after
// parsing so an assignment may precede the rule it scopes.
func (p *parser) attachTargetVars() {
	byName := make(map[string][]*model.Target, len(p.targets))
	for _, t := range p.targets {
		for _, name := range splitFields(t.Name) {
			byName[name] = append(byName[name], t)
		}
	}
	for _, v := range p.variables {
		if v.TargetScope == "" {
			continue
		}
		// A scope or a rule may name several targets, so match name by name.
		seen := map[*model.Target]bool{}
		for _, scope := range splitFields(v.TargetScope) {
			for _, t := range byName[scope] {
				if seen[t] {
					continue
				}
				seen[t] = true
				t.Variables = append(t.Variables, v)
			}
		}
	}
}

func (p *parser) parseLine() {
	startLine := p.pos
	line, endLine, spans := p.joinContinuations(startLine)
	p.pos = endLine
	at := logicalPos(startLine, spans)

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
		p.currentTarget.Range.End = lsp.Position{Line: endLine, Character: len(p.lines[endLine])}
		p.pos++
		return
	}

	trimmed := strings.TrimSpace(line)

	// Comment
	if strings.HasPrefix(trimmed, "#") {
		p.comments = append(p.comments, &model.Comment{
			Text:  trimmed,
			Range: p.logicalRange(startLine, endLine),
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
		colonIdx := strings.Index(line, ":")
		if colonIdx < 0 {
			colonIdx = strings.Index(trimmed, ":")
		}
		depsPart := m[1]
		if colonIdx >= 0 && colonIdx+1 <= len(line) {
			depsPart = line[colonIdx+1:]
		}
		refs := parseDeps(depsPart, colonIdx+1, at)
		for _, ref := range refs {
			p.phonies[ref.Name] = true
			p.phonyRefs = append(p.phonyRefs, ref)
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
		for _, path := range splitIncludeArgs(m[2]) {
			p.includes = append(p.includes, &model.Include{
				Path:     path,
				Range:    p.logicalRange(startLine, endLine),
				Optional: optional,
			})
		}
		p.commentBlock = nil
		p.pos++
		return
	}

	// export / unexport (may also be a variable assignment: export FOO = bar)
	if m := exportRe.FindStringSubmatch(trimmed); m != nil {
		if p.parseExportVar(m, startLine, endLine, line, at) {
			return
		}
		dirType := model.DirExport
		if m[1] == "unexport" {
			dirType = model.DirUnexport
		}
		argsStr := strings.TrimSpace(m[2])
		argsOffset := 0
		if argsStr != "" {
			if idx := strings.Index(line, argsStr); idx >= 0 {
				argsOffset = idx
			}
		}
		p.directives = append(p.directives, &model.Directive{
			Type:    dirType,
			Args:    argsStr,
			Range:   p.logicalRange(startLine, endLine),
			VarRefs: parseVarNames(argsStr, argsOffset, at),
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
			Range: p.logicalRange(startLine, endLine),
		})
		p.commentBlock = nil
		p.pos++
		return
	}

	// private / override / export modifiers — strip prefix and re-parse as variable assignment.
	if rest, private, override, export := stripVarModifiers(trimmed); rest != trimmed {
		if v := p.tryParseVarAssign(rest, startLine, endLine, line, at); v != nil {
			v.Private = private
			v.Override = override
			v.Export = export
			p.variables = append(p.variables, v)
			p.commentBlock = nil
			p.pos++
			return
		}
	}

	// Target-specific variable: target: VAR = value
	if m := targetVarRe.FindStringSubmatch(trimmed); m != nil {
		v := p.newTargetVar(m, startLine, endLine, line, trimmed, at)
		p.variables = append(p.variables, v)
		p.commentBlock = nil
		p.pos++
		return
	}

	// Variable assignment (simple, no target scope)
	if v := p.tryParseVarAssign(trimmed, startLine, endLine, line, at); v != nil {
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
			Range:         p.logicalRange(startLine, endLine),
			NameRange:     nameRange(trimmed, strings.TrimSpace(m[1]), at),
		}
		prereqOffset := strings.LastIndex(trimmed, m[3])
		if prereqOffset < 0 {
			prereqOffset = 0
		}
		t.Deps = parseDeps(m[3], indentOf(line)+prereqOffset, at)
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
		depsColOffset := indentOf(line) + len(m[1]) + len(m[2])
		// Account for leading whitespace trimmed from depsPart.
		if raw := m[3]; len(raw) > 0 {
			depsColOffset += len(raw) - len(strings.TrimLeft(raw, " \t"))
		}

		var comments string
		var hasComments bool
		depsPart, comments, hasComments = strings.Cut(depsPart, "#")

		// Parse deps, splitting on | for order-only.
		deps, orderOnly := splitDepsOrderOnly(depsPart, depsColOffset, at)

		t := &model.Target{
			Name:          namesPart,
			Deps:          deps,
			OrderOnlyDeps: orderOnly,
			IsPattern:     isPattern,
			IsDoubleColon: isDouble,
			DocComment:    p.buildDocComment(),
			Range:         p.logicalRange(startLine, endLine),
			NameRange:     nameRangeInSegment(line, trimmed, namesPart, at),
		}
		if hasComments {
			t.LineComment = strings.TrimSpace(strings.TrimLeft(comments, "#"))
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

// joinContinuations joins backslash-continued lines starting at start. It
// returns the joined line, the last physical line consumed, and the offset in
// the joined line at which each physical line begins.
func (p *parser) joinContinuations(start int) (string, int, []int) {
	line := p.lines[start]
	if !strings.HasSuffix(line, "\\") || start+1 >= len(p.lines) {
		return line, start, []int{0}
	}

	var b strings.Builder
	b.WriteString(line[:len(line)-1])
	spans := []int{0}
	pos := start + 1
	for {
		next := p.lines[pos]
		spans = append(spans, b.Len())
		if strings.HasSuffix(next, "\\") && pos+1 < len(p.lines) {
			b.WriteString(next[:len(next)-1])
			pos++
			continue
		}
		b.WriteString(next)
		return b.String(), pos, spans
	}
}

func (p *parser) flushTarget() {
	p.currentTarget = nil
}

func (p *parser) tryParseVarAssign(s string, startLine, endLine int, fullLine string, at posFunc) *model.Variable {
	loc := varAssignRe.FindStringSubmatchIndex(s)
	if loc == nil {
		return nil
	}
	name := strings.TrimSpace(s[loc[4]:loc[5]])
	value := s[loc[8]:loc[9]]
	op := model.VarOp(s[loc[6]:loc[7]])
	return &model.Variable{
		Name:      name,
		Value:     strings.TrimSpace(value),
		Op:        op,
		Flavour:   model.FlavourForOp(op),
		Range:     p.logicalRange(startLine, endLine),
		NameRange: nameRangeInSegment(fullLine, s, name, at),
		Refs:      extractVarRefsAtOffset(value, offsetInLine(fullLine, s)+loc[8], at),
	}
}

// logicalRange spans a logical line from its first physical line to its last,
// which differ when continuations were joined.
func (p *parser) logicalRange(startLine, endLine int) lsp.Range {
	return lsp.Range{
		Start: lsp.Position{Line: startLine, Character: 0},
		End:   lsp.Position{Line: endLine, Character: len(p.lines[endLine])},
	}
}

func (p *parser) parseExportVar(m []string, startLine, endLine int, fullLine string, at posFunc) bool {
	rest := strings.TrimSpace(m[2])
	if rest == "" {
		return false
	}
	rest, private, override, _ := stripVarModifiers(rest)
	v := p.tryParseVarAssign(rest, startLine, endLine, fullLine, at)
	if v == nil {
		return false
	}
	v.Export = true
	v.Private = private
	v.Override = override
	p.variables = append(p.variables, v)
	p.commentBlock = nil
	p.pos++
	return true
}

func (p *parser) parseDefine(m []string, startLine int) {
	modifier := m[1]
	name := m[2]
	op := model.VarOp("=")
	if m[3] != "" {
		op = model.VarOp(m[3])
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
		Name:     name,
		Op:       op,
		Body:     strings.Join(body, "\n"),
		Private:  modifier == "private",
		Export:   modifier == "export",
		Override: modifier == "override",
		Range: lsp.Range{
			Start: lsp.Position{Line: startLine, Character: 0},
			End:   lsp.Position{Line: endLine, Character: len("endef")},
		},
	})
	p.commentBlock = nil
}

// newTargetVar builds a target-specific variable from a targetVarRe match.
func (p *parser) newTargetVar(m []string, line, endLine int, fullLine, trimmed string, at posFunc) *model.Variable {
	op := model.VarOp(m[4])
	name := strings.TrimSpace(m[3])
	valueOffset := offsetInLine(fullLine, trimmed) + len(trimmed) - len(m[5])
	return &model.Variable{
		Name:        name,
		Value:       strings.TrimSpace(m[5]),
		Op:          op,
		Flavour:     model.FlavourForOp(op),
		TargetScope: strings.TrimSpace(m[1]),
		Private:     strings.Contains(m[2], "private"),
		Override:    strings.Contains(m[2], "override"),
		Export:      strings.Contains(m[2], "export"),
		Range:       p.logicalRange(line, endLine),
		NameRange:   nameRangeInSegment(fullLine, trimmed, name, at),
		Refs:        extractVarRefsAtOffset(m[5], valueOffset, at),
	}
}

func (p *parser) parseConditional(m []string, startLine int) *model.Conditional {
	condType := parseCondType(m[1])
	args := strings.TrimSpace(m[2])
	return p.parseConditionalBlock(condType, args, startLine, true)
}

func (p *parser) parseConditionalBlock(condType model.ConditionalType, args string, startLine int, consumeEndif bool) *model.Conditional {
	cond := &model.Conditional{
		Type:    condType,
		Args:    args,
		VarRefs: parseConditionalVarRefs(condType, args, p.lines[startLine], linePos(startLine)),
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
			if consumeEndif {
				p.pos++
			}
			return cond
		}

		if trimmed == "else" {
			inElse = true
			p.pos++
			continue
		}
		if strings.HasPrefix(trimmed, "else ") {
			if cm := conditionalStartRe.FindStringSubmatch(strings.TrimSpace(strings.TrimPrefix(trimmed, "else "))); cm != nil {
				inElse = true
				nested := p.parseConditionalBlock(parseCondType(cm[1]), strings.TrimSpace(cm[2]), p.pos, false)
				cond.ElseNodes = append(cond.ElseNodes, model.Node{Conditional: nested})
				continue
			}
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
		line, endLine, spans := p.joinContinuations(p.pos)
		node := p.parseConditionalLine(line, p.pos, endLine, logicalPos(p.pos, spans))
		if node != nil {
			if inElse {
				cond.ElseNodes = append(cond.ElseNodes, *node)
			} else {
				cond.ThenNodes = append(cond.ThenNodes, *node)
			}
		}
		p.pos = endLine + 1
	}

	// Unterminated conditional — set end to last line.
	cond.Range.End = lsp.Position{Line: p.pos - 1, Character: 0}
	return cond
}

func (p *parser) parseConditionalLine(fullLine string, startLine, endLine int, at posFunc) *model.Node {
	trimmed := strings.TrimSpace(fullLine)

	// export / unexport (may also be a variable assignment: export FOO = bar)
	if m := exportRe.FindStringSubmatch(trimmed); m != nil {
		rest := strings.TrimSpace(m[2])
		if rest != "" {
			if v := p.tryParseVarAssign(rest, startLine, endLine, fullLine, at); v != nil {
				v.Export = true
				p.variables = append(p.variables, v)
				return &model.Node{Variable: v}
			}
		}
		dirType := model.DirExport
		if m[1] == "unexport" {
			dirType = model.DirUnexport
		}
		argsStr := strings.TrimSpace(m[2])
		argsOffset := 0
		if argsStr != "" {
			if idx := strings.Index(fullLine, argsStr); idx >= 0 {
				argsOffset = idx
			}
		}
		d := &model.Directive{
			Type:    dirType,
			Args:    argsStr,
			Range:   p.logicalRange(startLine, endLine),
			VarRefs: parseVarNames(argsStr, argsOffset, at),
		}
		p.directives = append(p.directives, d)
		return &model.Node{Directive: d}
	}

	// private / override / export modifiers — strip prefix and re-parse as variable assignment.
	if rest, private, override, export := stripVarModifiers(trimmed); rest != trimmed {
		if v := p.tryParseVarAssign(rest, startLine, endLine, fullLine, at); v != nil {
			v.Private = private
			v.Override = override
			v.Export = export
			p.variables = append(p.variables, v)
			return &model.Node{Variable: v}
		}
	}

	// Target-specific variable: target: VAR = value
	if m := targetVarRe.FindStringSubmatch(trimmed); m != nil {
		v := p.newTargetVar(m, startLine, endLine, fullLine, trimmed, at)
		p.variables = append(p.variables, v)
		return &model.Node{Variable: v}
	}

	// Variable assignment
	if v := p.tryParseVarAssign(trimmed, startLine, endLine, fullLine, at); v != nil {
		p.variables = append(p.variables, v)
		return &model.Node{Variable: v}
	}

	// Include
	if m := includeRe.FindStringSubmatch(trimmed); m != nil {
		optional := strings.HasPrefix(m[1], "-") || strings.HasPrefix(m[1], "sinclude")
		path := strings.TrimSpace(m[2])
		if path == "" {
			return nil
		}
		inc := &model.Include{
			Path:     path,
			Range:    p.logicalRange(startLine, endLine),
			Optional: optional,
		}
		p.includes = append(p.includes, inc)
		return &model.Node{Include: inc}
	}

	return nil
}

func parseConditionalVarRefs(condType model.ConditionalType, args, fullLine string, at posFunc) []*model.VarRef {
	idx := strings.Index(fullLine, args)
	if idx < 0 {
		idx = 0
	}
	switch condType {
	case model.CondIfdef, model.CondIfndef:
		return parseVarNames(args, idx, at)
	case model.CondIfeq, model.CondIfneq:
		return extractVarRefsAtOffset(args, idx, at)
	default:
		return nil
	}
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
func splitDepsOrderOnly(s string, colOffset int, at posFunc) ([]*model.DepRef, []*model.DepRef) {
	parts := strings.SplitN(s, "|", 2)
	deps := parseDeps(parts[0], colOffset, at)
	var orderOnly []*model.DepRef
	if len(parts) > 1 {
		pipeIdx := strings.Index(s, "|")
		orderOnly = parseDeps(parts[1], colOffset+pipeIdx+1, at)
	}
	return deps, orderOnly
}

func parseDeps(s string, colOffset int, at posFunc) []*model.DepRef {
	var deps []*model.DepRef
	offset := 0
	for _, name := range splitFields(s) {
		idx := strings.Index(s[offset:], name)
		if idx >= 0 {
			col := colOffset + offset + idx
			deps = append(deps, &model.DepRef{
				Name:  name,
				Range: spanRange(at, col, len(name)),
			})
			offset = offset + idx + len(name)
		}
	}
	return deps
}

// parseVarNames parses a space-separated list of variable names and returns
// VarRef entries with their positions in the source line.
// It uses the same offset-advancing approach as parseDeps: because
// strings.Fields returns tokens in left-to-right order and we advance offset
// past each matched token, a name that is a prefix of another (e.g. "FOO" vs
// "FOOBAR") is always found at its correct position.
func parseVarNames(s string, colOffset int, at posFunc) []*model.VarRef {
	var refs []*model.VarRef
	offset := 0
	for _, name := range splitFields(s) {
		idx := strings.Index(s[offset:], name)
		if idx >= 0 {
			col := colOffset + offset + idx
			refs = append(refs, &model.VarRef{
				Name:  name,
				Range: spanRange(at, col, len(name)),
			})
			offset = offset + idx + len(name)
		}
	}
	return refs
}

func extractVarRefsAtOffset(s string, colOffset int, at posFunc) []*model.VarRef {
	matches := varRefRe.FindAllStringSubmatchIndex(s, -1)
	if matches == nil {
		return nil
	}
	var refs []*model.VarRef
	for _, m := range matches {
		name := s[m[2]:m[3]]
		refs = append(refs, &model.VarRef{
			Name:  name,
			Range: spanRange(at, colOffset+m[0], m[1]-m[0]),
		})
	}
	return refs
}

func splitFields(s string) []string {
	return strings.Fields(strings.TrimSpace(s))
}

func splitIncludeArgs(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var parts []string
	start := 0
	depth := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '$' && i+1 < len(s) && (s[i+1] == '(' || s[i+1] == '{') {
			depth++
			i++
			continue
		}
		switch s[i] {
		case ')', '}':
			if depth > 0 {
				depth--
			}
		case ' ', '\t':
			if depth == 0 {
				if part := strings.TrimSpace(s[start:i]); part != "" {
					parts = append(parts, part)
				}
				for i+1 < len(s) && (s[i+1] == ' ' || s[i+1] == '\t') {
					i++
				}
				start = i + 1
			}
		}
	}
	if part := strings.TrimSpace(s[start:]); part != "" {
		parts = append(parts, part)
	}
	return parts
}

// posFunc maps an offset in a logical line to a position in the document.
type posFunc func(offset int) lsp.Position

// logicalPos maps offsets in a joined logical line back to the physical line
// they came from. spans holds each physical line's start offset in the join.
func logicalPos(startLine int, spans []int) posFunc {
	if len(spans) == 1 {
		return linePos(startLine)
	}
	return func(offset int) lsp.Position {
		i := len(spans) - 1
		for i > 0 && spans[i] > offset {
			i--
		}
		return lsp.Position{Line: startLine + i, Character: offset - spans[i]}
	}
}

func linePos(line int) posFunc {
	return func(offset int) lsp.Position {
		return lsp.Position{Line: line, Character: offset}
	}
}

// spanRange positions a token of length n. The end stays on the start's line:
// a token never straddles a continuation, since the break splits it.
func spanRange(at posFunc, offset, n int) lsp.Range {
	start := at(offset)
	return lsp.Range{
		Start: start,
		End:   lsp.Position{Line: start.Line, Character: start.Character + n},
	}
}

// offsetInLine returns where segment starts within fullLine, 0 if absent.
func offsetInLine(fullLine, segment string) int {
	if idx := strings.Index(fullLine, segment); idx >= 0 {
		return idx
	}
	return 0
}

func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

// stripVarModifiers removes leading private/override/export prefixes from an assignment.
func stripVarModifiers(s string) (rest string, private, override, export bool) {
	rest = s
	for {
		m := varModifierRe.FindStringSubmatch(rest)
		if m == nil {
			return rest, private, override, export
		}
		switch m[1] {
		case "private":
			private = true
		case "override":
			override = true
		case "export":
			export = true
		}
		rest = rest[len(m[0]):]
	}
}

func nameRange(fullLine, name string, at posFunc) lsp.Range {
	return nameRangeInSegment(fullLine, fullLine, name, at)
}

func nameRangeInSegment(fullLine, segment, name string, at posFunc) lsp.Range {
	base := strings.Index(fullLine, segment)
	if base < 0 {
		base = 0
	}
	idx := strings.Index(segment, name)
	if idx < 0 {
		idx = strings.Index(fullLine, name)
		base = 0
		if idx < 0 {
			idx = 0
		}
	}
	return spanRange(at, base+idx, len(name))
}

func splitLines(text string) []string {
	lines := strings.Split(text, "\n")
	// Remove trailing empty line from final newline.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
