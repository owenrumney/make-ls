.PHONY: build test clean extension extension-target extension-install

GO_MODULE := github.com/owenrumney/make-ls
BINARY := make-ls
EXT_DIR := vscode-make-ls

build:
	go build -o bin/$(BINARY) ./cmd/make-ls

test:
	go test ./...

clean:
	rm -rf bin/
	rm -rf $(EXT_DIR)/bin/ $(EXT_DIR)/out/ $(EXT_DIR)/*.vsix

# Build all platform .vsix files.
extension: test
	cd $(EXT_DIR) && npm install && npm run compile
	cd $(EXT_DIR) && node scripts/package.js

# Build a single platform .vsix (e.g. make extension-target VSCE_TARGET=darwin-arm64).
extension-target: test
	cd $(EXT_DIR) && npm install && npm run compile
	cd $(EXT_DIR) && VSCE_TARGET=$(VSCE_TARGET) node scripts/package.js

# Install the extension for the current platform into VS Code.
extension-install: build
	cd $(EXT_DIR) && npm install && npm run compile
	mkdir -p $(EXT_DIR)/bin
	cp bin/$(BINARY) $(EXT_DIR)/bin/$(BINARY)
	cd $(EXT_DIR) && npx vsce package
	code --install-extension $(EXT_DIR)/*.vsix
