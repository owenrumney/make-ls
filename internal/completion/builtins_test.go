package completion

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsAutoVar(t *testing.T) {
	tests := []struct {
		name   string
		expect bool
	}{
		{"@", true},
		{"<", true},
		{"^", true},
		{"@D", true},
		{"*F", true},
		{"FOO", false},
		{"CC", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, IsAutoVar(tt.name))
		})
	}
}

func TestIsBuiltinVar(t *testing.T) {
	tests := []struct {
		name   string
		expect bool
	}{
		{"CC", true},
		{"CXX", true},
		{"MAKE", true},
		{"SHELL", true},
		{"CFLAGS", true},
		{"FOO", false},
		{"@", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, IsBuiltinVar(tt.name))
		})
	}
}

func TestIsFunction(t *testing.T) {
	tests := []struct {
		name   string
		expect bool
	}{
		{"wildcard", true},
		{"patsubst", true},
		{"shell", true},
		{"error", true},
		{"FOO", false},
		{"CC", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, IsFunction(tt.name))
		})
	}
}

func TestIsKnownName(t *testing.T) {
	assert.True(t, IsKnownName("@"))       // auto var
	assert.True(t, IsKnownName("CC"))      // builtin var
	assert.True(t, IsKnownName("shell"))   // function
	assert.False(t, IsKnownName("FOOBAR")) // unknown
}

func TestAutoVarsNotEmpty(t *testing.T) {
	assert.NotEmpty(t, AutoVars)
	for _, v := range AutoVars {
		assert.NotEmpty(t, v.Name, "auto var has empty name")
		assert.NotEmpty(t, v.Doc, "auto var %s has empty doc", v.Name)
	}
}

func TestFunctionsNotEmpty(t *testing.T) {
	assert.NotEmpty(t, Functions)
	for _, f := range Functions {
		assert.NotEmpty(t, f.Name, "function has empty name")
		assert.NotEmpty(t, f.Args, "function %s has empty args", f.Name)
		assert.NotEmpty(t, f.Doc, "function %s has empty doc", f.Name)
	}
}

func TestBuiltinVarsNotEmpty(t *testing.T) {
	assert.NotEmpty(t, BuiltinVars)
	for _, v := range BuiltinVars {
		assert.NotEmpty(t, v.Name, "builtin var has empty name")
		assert.NotEmpty(t, v.Doc, "builtin var %s has empty doc", v.Name)
	}
}
