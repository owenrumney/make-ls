# make-ls

A language server for Makefiles. Provides real IDE features — not just syntax highlighting.

![Demo](./.github/images/demo.gif)

## Features

| Feature | What it does |
|---|---|
| **Hover** | Target docs, recipe preview, variable values, auto-variable docs (`$@`, `$<`, ...), function docs (`$(wildcard ...)`, ...) |
| **Completion** | Context-aware: variables + functions after `$(`, targets in dep lists, auto-variables in recipes, directives at line start |
| **Go to Definition** | Jump from dep → target, `$(VAR)` → assignment, include path → file |
| **Find References** | All deps referencing a target, all `$(VAR)` usages for a variable |
| **Document Symbols** | Targets, variables, conditionals — outline view and Cmd+Shift+O |
| **Diagnostics** | Undefined targets, undefined variables (respects builtins, recursive vars, conditionals), missing `.PHONY` hints |
| **Code Actions** | Quick-fix to add `.PHONY` declarations |
| **Formatting** | Tabs in recipes, trim trailing whitespace, ensure final newline |
| **Include Resolution** | Follows `include` / `-include` / `sinclude` directives, merges symbols across files |

## Parser coverage

The custom parser handles the full breadth of GNU Make syntax:

- Variable assignments (`=` `:=` `?=` `+=` `!=`) with flavour tracking
- Pattern rules (`%.o: %.c`), static pattern rules, double-colon rules
- Target-specific variables (`target: CC = clang`)
- Order-only prerequisites (`target: deps | order-only`)
- Conditional blocks (`ifeq`/`ifneq`/`ifdef`/`ifndef` with nesting)
- Multi-line `define`/`endef` blocks
- `export`/`unexport`/`override`/`vpath` directives
- Line continuations, doc comments, `.PHONY`
- Precise source positions for all tokens

## Why?

There's no real Makefile language server. The existing options:

- **Microsoft Makefile Tools** (VS Code) — focused on build/debug configuration, not language intelligence
- **checkmake** — a standalone linter with style warnings, no LSP
- **efm-langserver** — generic wrapper that can shell out to checkmake, but no hover/completion/symbols
- **tree-sitter-make** — syntax highlighting only

make-ls fills the gap with actual LSP features: hover, completion, go-to-definition, references, diagnostics, symbols, code actions, and formatting. Works in any editor that speaks LSP.

## Install

### Install with Go

```sh
go install github.com/owenrumney/make-ls/cmd/make-ls@latest
```

Now you can set up Neovim with the path set to `$GOPATH/bin/make-ls`

### VS Code

Install from a `.vsix` file (see [Releases](https://github.com/owenrumney/make-ls/releases)):

```sh
code --install-extension make-ls-darwin-arm64-0.1.0.vsix
```

Or build and install locally:

```sh
make extension-install
```

### Neovim

No existing Makefile LSP is available in nvim-lspconfig. To use make-ls, add a custom config:

```lua
-- In your Neovim config (init.lua or after/ftplugin/make.lua)
vim.api.nvim_create_autocmd("FileType", {
  pattern = "make",
  callback = function()
    vim.lsp.start({
      name = "make-ls",
      cmd = { "/path/to/make-ls" },
      root_dir = vim.fs.root(0, { "Makefile", "makefile", "GNUmakefile" }),
    })
  end,
})
```

This gives you hover, completion, go-to-definition, references, diagnostics, and symbols — none of which were previously available for Makefiles in Neovim.

### Helix

Add to `languages.toml`:

```toml
[[language]]
name = "make"
language-servers = ["make-ls"]

[language-server.make-ls]
command = "/path/to/make-ls"
```

### Other editors

`make-ls` is a standard LSP server communicating over stdio. Build the binary and point your editor at it:

```sh
make build
# binary is at bin/make-ls
```

## Build

Requires Go 1.23+ and Node 18+.

```sh
# Run tests
make test

# Build binary
make build

# Build VS Code extension for all platforms
make extension

# Build for a single platform
make extension-target VSCE_TARGET=darwin-arm64
```

### Supported platforms

| Target | GOOS/GOARCH |
|---|---|
| `darwin-x64` | darwin/amd64 |
| `darwin-arm64` | darwin/arm64 |
| `linux-x64` | linux/amd64 |
| `linux-arm64` | linux/arm64 |

## Project structure

```
cmd/make-ls/          Entry point — stdio LSP server
internal/
  model/              AST types (Target, Variable, Conditional, ...)
  parser/             Line-oriented Makefile parser
  completion/         Context-aware completion + builtin docs
  analysis/           Diagnostic checks
  resolver/           Include file resolution + model merging
  handler/            LSP handler (wires everything together)
vscode-make-ls/       VS Code extension (TypeScript thin client)
```

## License

MIT
