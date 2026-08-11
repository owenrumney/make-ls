package handler

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormatMakefileContinuations(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "preserves extra indentation on a continuation line",
			input: "target:\n\techo a \\\n\t\techo b\n",
			want:  "target:\n\techo a \\\n\t\techo b\n",
		},
		{
			name:  "preserves indentation across multiple continuations",
			input: "target:\n\tdocker run \\\n\t\t--rm \\\n\t\t-v $(PWD):/src \\\n\t\timage\n",
			want:  "target:\n\tdocker run \\\n\t\t--rm \\\n\t\t-v $(PWD):/src \\\n\t\timage\n",
		},
		{
			name:  "preserves varying indentation depths within one logical line",
			input: "target:\n\tcmd \\\n\t\t--one \\\n\t\t\t--two \\\n\t--three\n",
			want:  "target:\n\tcmd \\\n\t\t--one \\\n\t\t\t--two \\\n\t--three\n",
		},
		{
			name:  "handles several continued commands in one recipe",
			input: "target:\n\tfirst \\\n\t\ta\n\tplain\n\tsecond \\\n\t\tb\n",
			want:  "target:\n\tfirst \\\n\t\ta\n\tplain\n\tsecond \\\n\t\tb\n",
		},
		{
			name:  "does not add indentation when the author used none",
			input: "target:\n\techo a \\\n\techo b\n",
			want:  "target:\n\techo a \\\n\techo b\n",
		},
		{
			name:  "adds the missing tab but keeps space indentation on continuations",
			input: "target:\n\techo a \\\n    echo b\n",
			want:  "target:\n\techo a \\\n\t    echo b\n",
		},
		{
			name:  "still normalises space indentation on non-continuation recipe lines",
			input: "target:\n    echo a\n        echo b\n",
			want:  "target:\n\techo a\n\techo b\n",
		},
		{
			name:  "trims trailing whitespace after a continuation backslash",
			input: "target:\n\techo a \\\n\t\techo b   \n",
			want:  "target:\n\techo a \\\n\t\techo b\n",
		},
		{
			name:  "escaped backslash does not start a continuation",
			input: "target:\n\tprintf 'a\\\\\\\\'\n\t\techo b\n",
			want:  "target:\n\tprintf 'a\\\\\\\\'\n\techo b\n",
		},
		{
			name:  "odd run of backslashes does start a continuation",
			input: "target:\n\tprintf 'a\\\\\\\\\\\n\t\techo b\n",
			want:  "target:\n\tprintf 'a\\\\\\\\\\\n\t\techo b\n",
		},
		{
			name:  "blank line ends the recipe and the continuation state",
			input: "target:\n\techo a \\\n\t\techo b\n\nother:\n        echo c\n",
			want:  "target:\n\techo a \\\n\t\techo b\n\nother:\n\techo c\n",
		},
		{
			name:  "comment inside a recipe ends the continuation state",
			input: "target:\n\techo a \\\n\t\techo b\n\t# note\n        echo c\n",
			want:  "target:\n\techo a \\\n\t\techo b\n\t# note\n\techo c\n",
		},
		{
			name:  "preserves indented heredoc bodies inside a continued command",
			input: "target:\n\tcat <<-EOF \\\n\t\t  indented body \\\n\t\tEOF\n",
			want:  "target:\n\tcat <<-EOF \\\n\t\t  indented body \\\n\t\tEOF\n",
		},
		{
			name:  "leaves variable assignment continuations alone",
			input: "SRCS = a.go \\\n\tb.go \\\n\tc.go\n",
			want:  "SRCS = a.go \\\n\tb.go \\\n\tc.go\n",
		},
		{
			name:  "preserves indentation on continued prerequisite lists",
			input: "target: dep1 \\\n\t\tdep2\n\techo a\n",
			want:  "target: dep1 \\\n\t\tdep2\n\techo a\n",
		},
		{
			name:  "first recipe line after a continued prerequisite list is normalised",
			input: "target: dep1 \\\n\t\tdep2\n        echo a\n",
			want:  "target: dep1 \\\n\t\tdep2\n\techo a\n",
		},
		{
			name:  "adds a final newline",
			input: "target:\n\techo a \\\n\t\techo b",
			want:  "target:\n\techo a \\\n\t\techo b\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, formatMakefile(tt.input))
		})
	}
}

func TestFormatMakefileIsIdempotent(t *testing.T) {
	inputs := []string{
		"target:\n\tdocker run \\\n\t\t--rm \\\n\t\timage\n",
		"target:\n    echo a \\\n        echo b\n",
		"SRCS = a.go \\\n\tb.go\n\nall: $(SRCS)\n\techo done\n",
		"target:\n\tprintf 'a\\\\\\\\'\n        echo b\n",
	}

	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			once := formatMakefile(input)
			assert.Equal(t, once, formatMakefile(once))
		})
	}
}

func TestEndsWithContinuation(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"echo a", false},
		{`echo a \`, true},
		{`echo a \\`, false},
		{`echo a \\\`, true},
		{`echo a \\\\`, false},
		{`\`, true},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			assert.Equal(t, tt.want, endsWithContinuation(tt.line))
		})
	}
}
