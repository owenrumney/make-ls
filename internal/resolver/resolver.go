package resolver

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/owenrumney/go-lsp/lsp"
	"github.com/owenrumney/make-ls/internal/model"
	"github.com/owenrumney/make-ls/internal/parser"
)

// Resolve parses the given Makefile and recursively resolves all include
// directives, returning a merged model. Circular includes are detected and
// skipped. Optional includes (-include / sinclude) silently skip missing files.
func Resolve(uri lsp.DocumentURI, text string) *model.Makefile {
	r := &resolver{
		seen: map[string]bool{},
	}
	root := parser.Parse(uri, text)
	dir := dirFromURI(uri)
	r.seen[string(uri)] = true
	r.resolve(root, dir)
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
	return Resolve(uri, string(data)), nil
}

type resolver struct {
	seen map[string]bool // visited URIs to detect circular includes
}

func (r *resolver) resolve(mf *model.Makefile, baseDir string) {
	for _, inc := range mf.Includes {
		incPath := inc.Path

		// Resolve relative to the including Makefile's directory.
		if !filepath.IsAbs(incPath) {
			incPath = filepath.Join(baseDir, incPath)
		}
		incPath = filepath.Clean(incPath)

		incURI := uriFromPath(incPath)
		if r.seen[string(incURI)] {
			continue // circular include
		}
		r.seen[string(incURI)] = true

		data, err := os.ReadFile(incPath)
		if err != nil {
			if inc.Optional {
				continue // -include / sinclude: silently skip
			}
			continue // non-optional but missing: skip (diagnostics will catch this)
		}

		child := parser.Parse(incURI, string(data))
		r.resolve(child, filepath.Dir(incPath))
		merge(mf, child)
	}
}

// merge folds the child Makefile's contents into the parent.
func merge(parent, child *model.Makefile) {
	parent.Targets = append(parent.Targets, child.Targets...)
	parent.Variables = append(parent.Variables, child.Variables...)
	parent.Defines = append(parent.Defines, child.Defines...)
	parent.Directives = append(parent.Directives, child.Directives...)
	parent.Conditionals = append(parent.Conditionals, child.Conditionals...)
	parent.Comments = append(parent.Comments, child.Comments...)
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
			return u.Path
		}
	}
	return s
}

func uriFromPath(path string) lsp.DocumentURI {
	return lsp.DocumentURI("file://" + path)
}
