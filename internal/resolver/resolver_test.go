package resolver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/owenrumney/go-lsp/lsp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func uriFor(path string) lsp.DocumentURI {
	return uriFromPath(path)
}

func TestURIFromWindowsPath(t *testing.T) {
	assert.Equal(t, lsp.DocumentURI("file:///c%3A/Users/owen/Makefile"), uriFromPath("C:/Users/owen/Makefile"))
}

func TestResolveNoIncludes(t *testing.T) {
	input := "CC := gcc\nall:\n\t$(CC) -o app\n"
	mf := Resolve("file:///test/Makefile", input, nil)

	require.Len(t, mf.Variables, 1)
	require.Len(t, mf.Targets, 1)
}

func TestResolveWithInclude(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "config.mk", "CC := gcc\nCFLAGS := -Wall\n")

	main := "include config.mk\nall:\n\t$(CC) $(CFLAGS) -o app\n"
	mainPath := writeFile(t, dir, "Makefile", main)

	mf := Resolve(uriFor(mainPath), main, nil)

	// Should have CC and CFLAGS from config.mk plus no extra from main.
	assert.Len(t, mf.Variables, 2)
	names := map[string]bool{}
	for _, v := range mf.Variables {
		names[v.Name] = true
	}
	assert.True(t, names["CC"])
	assert.True(t, names["CFLAGS"])
}

func TestResolveWithTargetsFromInclude(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "targets.mk", "clean:\n\trm -f app\n")

	main := "include targets.mk\nall: clean\n\techo done\n"
	mainPath := writeFile(t, dir, "Makefile", main)

	mf := Resolve(uriFor(mainPath), main, nil)

	assert.Len(t, mf.Targets, 2) // all + clean
	targetNames := map[string]bool{}
	for _, tgt := range mf.Targets {
		targetNames[tgt.Name] = true
	}
	assert.True(t, targetNames["all"])
	assert.True(t, targetNames["clean"])
}

func TestResolvePhoniesFromInclude(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "phony.mk", ".PHONY: clean\nclean:\n\trm -f app\n")

	main := "include phony.mk\n.PHONY: all\nall:\n\techo done\n"
	mainPath := writeFile(t, dir, "Makefile", main)

	mf := Resolve(uriFor(mainPath), main, nil)

	assert.True(t, mf.Phonies["all"])
	assert.True(t, mf.Phonies["clean"])
}

func TestResolveOptionalIncludeMissing(t *testing.T) {
	dir := t.TempDir()

	main := "-include nonexistent.mk\nall:\n\techo done\n"
	mainPath := writeFile(t, dir, "Makefile", main)

	mf := Resolve(uriFor(mainPath), main, nil)

	// Should not panic or error — just skip.
	require.Len(t, mf.Targets, 1)
	assert.Equal(t, "all", mf.Targets[0].Name)
}

func TestResolveSincludeMissing(t *testing.T) {
	dir := t.TempDir()

	main := "sinclude nonexistent.mk\nall:\n\techo done\n"
	mainPath := writeFile(t, dir, "Makefile", main)

	mf := Resolve(uriFor(mainPath), main, nil)

	require.Len(t, mf.Targets, 1)
}

func TestResolveCircularInclude(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "a.mk", "include b.mk\nA_VAR := a\n")
	writeFile(t, dir, "b.mk", "include a.mk\nB_VAR := b\n")

	main := "include a.mk\nall:\n\techo done\n"
	mainPath := writeFile(t, dir, "Makefile", main)

	mf := Resolve(uriFor(mainPath), main, nil)

	// Should not infinite loop. Both variables should be present.
	names := map[string]bool{}
	for _, v := range mf.Variables {
		names[v.Name] = true
	}
	assert.True(t, names["A_VAR"])
	assert.True(t, names["B_VAR"])
}

func TestResolveNestedIncludes(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "level2.mk", "DEEP := yes\n")
	writeFile(t, dir, "level1.mk", "include level2.mk\nMID := yes\n")

	main := "include level1.mk\nall:\n\techo done\n"
	mainPath := writeFile(t, dir, "Makefile", main)

	mf := Resolve(uriFor(mainPath), main, nil)

	names := map[string]bool{}
	for _, v := range mf.Variables {
		names[v.Name] = true
	}
	assert.True(t, names["DEEP"])
	assert.True(t, names["MID"])
}

func TestResolveRelativePath(t *testing.T) {
	dir := t.TempDir()

	subdir := filepath.Join(dir, "sub")
	writeFile(t, subdir, "extra.mk", "EXTRA := yes\n")

	main := "include sub/extra.mk\nall:\n\techo done\n"
	mainPath := writeFile(t, dir, "Makefile", main)

	mf := Resolve(uriFor(mainPath), main, nil)

	names := map[string]bool{}
	for _, v := range mf.Variables {
		names[v.Name] = true
	}
	assert.True(t, names["EXTRA"])
}

func TestResolveRejectsIncludeOutsideRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "project")
	outside := writeFile(t, parent, "outside.mk", "SECRET := value\n")
	main := "include ../outside.mk\n"
	mainPath := writeFile(t, root, "Makefile", main)

	mf := Resolve(uriFor(mainPath), main, nil)

	assert.Empty(t, mf.Variables)
	assert.Empty(t, mf.UnresolvedIncludes)
	assert.Empty(t, mf.Includes[0].ResolvedPath)
	assert.NotContains(t, mf.Sources, uriFor(outside))
}

func TestResolveRejectsOversizedInclude(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "large.mk"), make([]byte, maxIncludeSize+1), 0o600))
	main := "include large.mk\n"
	mainPath := writeFile(t, dir, "Makefile", main)

	mf := Resolve(uriFor(mainPath), main, nil)

	assert.Empty(t, mf.Variables)
	assert.Equal(t, []lsp.DocumentURI{uriFor(filepath.Join(dir, "large.mk"))}, mf.UnresolvedIncludes)
}

func TestResolveFromDisk(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "config.mk", "CC := gcc\n")
	mainPath := writeFile(t, dir, "Makefile", "include config.mk\nall:\n\techo done\n")

	mf, err := ResolveFromDisk(uriFor(mainPath))
	require.NoError(t, err)

	require.Len(t, mf.Variables, 1)
	assert.Equal(t, "CC", mf.Variables[0].Name)
}

func TestResolveComputedIncludePath(t *testing.T) {
	dir := t.TempDir()
	scriptsDir := filepath.Join(dir, "scripts")
	included := writeFile(t, scriptsDir, "Kbuild.include", "CC := gcc\n")
	main := "srctree := " + dir + "\ninclude $(srctree)/scripts/Kbuild.include\n"
	mainPath := writeFile(t, dir, "Makefile", main)

	mf := Resolve(uriFor(mainPath), main, nil)

	require.Len(t, mf.Includes, 1)
	assert.Equal(t, included, mf.Includes[0].ResolvedPath)
	require.Len(t, mf.Variables, 2)
	assert.Equal(t, "CC", mf.Variables[1].Name)
}

func TestResolveAddprefixIncludePath(t *testing.T) {
	dir := t.TempDir()
	scriptsDir := filepath.Join(dir, "scripts")
	writeFile(t, scriptsDir, "one.mk", "ONE := yes\n")
	writeFile(t, scriptsDir, "two.mk", "TWO := yes\n")
	main := "srctree := " + dir + "\ninclude-y := scripts/one.mk scripts/two.mk\ninclude $(addprefix $(srctree)/, $(include-y))\n"
	mainPath := writeFile(t, dir, "Makefile", main)

	mf := Resolve(uriFor(mainPath), main, nil)

	names := map[string]bool{}
	for _, v := range mf.Variables {
		names[v.Name] = true
	}
	assert.True(t, names["ONE"])
	assert.True(t, names["TWO"])
	assert.Equal(t, filepath.Join(dir, "scripts", "one.mk"), mf.Includes[0].ResolvedPath)
}

func TestResolveWhitespaceComputedIncludePath(t *testing.T) {
	dir := t.TempDir()
	main := "EMPTY :=\ninclude $(EMPTY)\nall:\n\techo done\n"
	mainPath := writeFile(t, dir, "Makefile", main)

	mf := Resolve(uriFor(mainPath), main, nil)

	require.Len(t, mf.Targets, 1)
	assert.Equal(t, "all", mf.Targets[0].Name)
}

func TestExtractDelimitedMixedNesting(t *testing.T) {
	end, inner, ok := extractDelimited("$(foo ${bar})", 1)
	require.True(t, ok)
	assert.Equal(t, len("$(foo ${bar})")-1, end)
	assert.Equal(t, "foo ${bar}", inner)
}

func TestResolveFromDiskMissing(t *testing.T) {
	_, err := ResolveFromDisk("file:///nonexistent/Makefile")
	assert.Error(t, err)
}

func TestResolvePrefersSourceProviderOverDisk(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.mk", "CC := gcc\n")

	main := "include config.mk\nOUT := $(CC)\n"
	mainPath := writeFile(t, dir, "Makefile", main)
	incURI := uriFor(filepath.Join(dir, "config.mk"))

	buffer := "CC := clang\nLD := ld\n"
	mf := Resolve(uriFor(mainPath), main, func(u lsp.DocumentURI) (string, bool) {
		if u == incURI {
			return buffer, true
		}
		return "", false
	})

	names := map[string]string{}
	for _, v := range mf.Variables {
		names[v.Name] = v.Value
	}
	assert.Equal(t, "clang", names["CC"])
	assert.Contains(t, names, "LD")

	// Sources must hold exactly what was parsed, or ranges encode wrongly.
	assert.Equal(t, buffer, mf.Sources[incURI])
	assert.Equal(t, main, mf.Sources[uriFor(mainPath)])
}

func TestResolveFallsBackToDiskWhenProviderMisses(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.mk", "CC := gcc\n")

	main := "include config.mk\n"
	mainPath := writeFile(t, dir, "Makefile", main)

	mf := Resolve(uriFor(mainPath), main, func(lsp.DocumentURI) (string, bool) {
		return "", false
	})

	require.Len(t, mf.Variables, 1)
	assert.Equal(t, "gcc", mf.Variables[0].Value)
	assert.Equal(t, "CC := gcc\n", mf.Sources[uriFor(filepath.Join(dir, "config.mk"))])
}
