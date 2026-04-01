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
	return lsp.DocumentURI("file://" + path)
}

func TestResolveNoIncludes(t *testing.T) {
	input := "CC := gcc\nall:\n\t$(CC) -o app\n"
	mf := Resolve("file:///test/Makefile", input)

	require.Len(t, mf.Variables, 1)
	require.Len(t, mf.Targets, 1)
}

func TestResolveWithInclude(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "config.mk", "CC := gcc\nCFLAGS := -Wall\n")

	main := "include config.mk\nall:\n\t$(CC) $(CFLAGS) -o app\n"
	mainPath := writeFile(t, dir, "Makefile", main)

	mf := Resolve(uriFor(mainPath), main)

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

	mf := Resolve(uriFor(mainPath), main)

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

	mf := Resolve(uriFor(mainPath), main)

	assert.True(t, mf.Phonies["all"])
	assert.True(t, mf.Phonies["clean"])
}

func TestResolveOptionalIncludeMissing(t *testing.T) {
	dir := t.TempDir()

	main := "-include nonexistent.mk\nall:\n\techo done\n"
	mainPath := writeFile(t, dir, "Makefile", main)

	mf := Resolve(uriFor(mainPath), main)

	// Should not panic or error — just skip.
	require.Len(t, mf.Targets, 1)
	assert.Equal(t, "all", mf.Targets[0].Name)
}

func TestResolveSincludeMissing(t *testing.T) {
	dir := t.TempDir()

	main := "sinclude nonexistent.mk\nall:\n\techo done\n"
	mainPath := writeFile(t, dir, "Makefile", main)

	mf := Resolve(uriFor(mainPath), main)

	require.Len(t, mf.Targets, 1)
}

func TestResolveCircularInclude(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "a.mk", "include b.mk\nA_VAR := a\n")
	writeFile(t, dir, "b.mk", "include a.mk\nB_VAR := b\n")

	main := "include a.mk\nall:\n\techo done\n"
	mainPath := writeFile(t, dir, "Makefile", main)

	mf := Resolve(uriFor(mainPath), main)

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

	mf := Resolve(uriFor(mainPath), main)

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

	mf := Resolve(uriFor(mainPath), main)

	names := map[string]bool{}
	for _, v := range mf.Variables {
		names[v.Name] = true
	}
	assert.True(t, names["EXTRA"])
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

func TestResolveFromDiskMissing(t *testing.T) {
	_, err := ResolveFromDisk("file:///nonexistent/Makefile")
	assert.Error(t, err)
}
