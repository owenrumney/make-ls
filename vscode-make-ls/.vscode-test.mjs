import { defineConfig } from "@vscode/test-cli";
import * as os from "os";
import * as path from "path";

// VS Code opens an IPC socket under the user data dir; the repo path blows the
// 107-char limit, so keep it in the temp directory.
const userDataDir = path.join(os.tmpdir(), "make-ls-vscode-test");

export default defineConfig({
  files: "out/test/**/*.test.js",
  workspaceFolder: "./src/test/fixtures",
  launchArgs: ["--user-data-dir", userDataDir, "--disable-gpu"],
  mocha: { timeout: 120_000 },
});
