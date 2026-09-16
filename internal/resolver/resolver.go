package resolver

import (
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/owenrumney/go-lsp/lsp"
	"github.com/owenrumney/make-ls/internal/model"
	"github.com/owenrumney/make-ls/internal/parser"
)

// SourceOf returns the text of uri and whether the server has it open. It lets
// an include resolve from an unsaved buffer rather than from disk.
type SourceOf func(lsp.DocumentURI) (string, bool)

// Resolve parses the given Makefile and recursively resolves all include
// directives, returning a merged model. Circular includes are detected and
// skipped. Optional includes (-include / sinclude) silently skip missing files.
// A nil src means disk only.
func Resolve(uri lsp.DocumentURI, text string, src SourceOf) *model.Makefile {
	dir := dirFromURI(uri)
	r := &resolver{
		seen:    map[string]bool{},
		src:     src,
		rootDir: canonicalPath(dir),
	}
	root := parser.Parse(uri, text)
	r.seen[string(uri)] = true
	r.resolve(root, dir)
	root.UnresolvedIncludes = r.unresolved
	return root
}

// ResolveFromDisk parses the Makefile at the given URI by reading it from disk,
// then recursively resolves includes.
func ResolveFromDisk(uri lsp.DocumentURI) (*model.Makefile, error) {
	path := pathFromURI(uri)
	// #nosec G304
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Resolve(uri, string(data), nil), nil
}

type resolver struct {
	seen    map[string]bool // visited URIs to detect circular includes
	src     SourceOf
	rootDir string

	// unresolved collects includes at any depth that named a file with no
	// readable content; merge drops child.Includes, so nothing else carries
	// them up to the root.
	unresolved []lsp.DocumentURI
}

func (r *resolver) resolve(mf *model.Makefile, baseDir string) {
	for _, inc := range mf.Includes {
		paths := resolveIncludePaths(mf, baseDir, inc)
		for _, incPath := range paths {
			if !r.pathAllowed(incPath) {
				continue
			}
			if inc.ResolvedPath == "" {
				inc.ResolvedPath = incPath
			}
			incURI := uriFromPath(incPath)
			if r.seen[string(incURI)] {
				continue // circular include
			}
			r.seen[string(incURI)] = true

			text, ok := r.sourceFor(incURI, incPath)
			if !ok {
				// Missing: optional includes skip silently, the rest are left
				// for diagnostics. Either way the path is worth watching.
				r.unresolved = append(r.unresolved, incURI)
				continue
			}

			child := parser.Parse(incURI, text)
			r.resolve(child, filepath.Dir(incPath))
			merge(mf, child)
		}
	}
}

// maxIncludeSize bounds the text retained for any one included file.
const maxIncludeSize = 4 << 20

// sourceFor prefers an open buffer over disk, so ranges and text always come
// from the same bytes. Includes are confined to the root Makefile's directory
// tree and disk reads accept only bounded regular files.
func (r *resolver) sourceFor(uri lsp.DocumentURI, path string) (string, bool) {
	if !r.pathAllowed(path) {
		return "", false
	}
	if r.src != nil {
		if text, ok := r.src(uri); ok {
			return text, true
		}
	}

	// #nosec G304 -- path has been confined to the root Makefile tree above.
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxIncludeSize {
		return "", false
	}
	data, err := io.ReadAll(io.LimitReader(file, maxIncludeSize+1))
	if err != nil || len(data) > maxIncludeSize {
		return "", false
	}
	return string(data), true
}

// canonicalPath resolves symlinks when possible so an include cannot escape
// through a symlink beneath the root directory.
func canonicalPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

func (r *resolver) pathAllowed(path string) bool {
	return pathWithin(r.rootDir, canonicalPath(path))
}

func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func resolveIncludePaths(mf *model.Makefile, baseDir string, inc *model.Include) []string {
	ctx := newEvalContext(baseDir, mf, int(inc.Range.Start.Line))
	expanded := ctx.expandText(inc.Path)
	if expanded == "" {
		expanded = inc.Path
	}
	fields := strings.Fields(expanded)
	if len(fields) == 0 {
		return nil
	}
	paths := make([]string, 0, len(fields))
	for _, p := range fields {
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(baseDir, p)
		}
		paths = append(paths, filepath.Clean(p))
	}
	return paths
}

// merge folds the child Makefile's contents into the parent.
func merge(parent, child *model.Makefile) {
	parent.Targets = append(parent.Targets, child.Targets...)
	parent.Variables = append(parent.Variables, child.Variables...)
	parent.Defines = append(parent.Defines, child.Defines...)
	parent.Directives = append(parent.Directives, child.Directives...)
	parent.Conditionals = append(parent.Conditionals, child.Conditionals...)
	parent.Comments = append(parent.Comments, child.Comments...)
	parent.PhonyRefs = append(parent.PhonyRefs, child.PhonyRefs...)
	parent.VarRefs = append(parent.VarRefs, child.VarRefs...)
	for uri, text := range child.Sources {
		parent.Sources[uri] = text
	}
	for k, v := range child.Phonies {
		if v {
			parent.Phonies[k] = true
		}
	}
	// Don't merge child.Includes — they've already been resolved.
}

func dirFromURI(uri lsp.DocumentURI) string {
	return filepath.Dir(pathFromURI(uri))
}

func pathFromURI(uri lsp.DocumentURI) string {
	s := string(uri)
	if strings.HasPrefix(s, "file://") {
		u, err := url.Parse(s)
		if err == nil {
			path := u.Path
			// file URIs on Windows use /c%3A/... while filesystem paths use C:\\....
			if runtime.GOOS == "windows" && len(path) >= 3 && path[0] == '/' && path[2] == ':' {
				path = path[1:]
			}
			return filepath.FromSlash(path)
		}
	}
	return s
}

func uriFromPath(path string) lsp.DocumentURI {
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
