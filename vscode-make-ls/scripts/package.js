const { execSync } = require("child_process");
const fs = require("fs");
const path = require("path");

const ROOT = path.resolve(__dirname, "..", "..");
const EXT_DIR = path.resolve(__dirname, "..");
const BIN_DIR = path.join(EXT_DIR, "bin");

const targets = [
  { goos: "darwin", goarch: "amd64", vscodeTarget: "darwin-x64" },
  { goos: "darwin", goarch: "arm64", vscodeTarget: "darwin-arm64" },
  { goos: "linux", goarch: "amd64", vscodeTarget: "linux-x64" },
  { goos: "linux", goarch: "arm64", vscodeTarget: "linux-arm64" },
];

// Allow building a single target via env var.
const only = process.env.VSCE_TARGET;

for (const t of targets) {
  if (only && t.vscodeTarget !== only) {
    continue;
  }

  console.log(`\n=== ${t.vscodeTarget} (${t.goos}/${t.goarch}) ===`);

  // Clean and create bin dir.
  if (fs.existsSync(BIN_DIR)) {
    fs.rmSync(BIN_DIR, { recursive: true });
  }
  fs.mkdirSync(BIN_DIR, { recursive: true });

  const binaryName = "make-ls";
  const outPath = path.join(BIN_DIR, binaryName);

  // Cross-compile.
  console.log(`Compiling ${t.goos}/${t.goarch}...`);
  execSync(
    `GOOS=${t.goos} GOARCH=${t.goarch} CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o ${outPath} ./cmd/make-ls`,
    { cwd: ROOT, stdio: "inherit" }
  );

  // Make executable.
  fs.chmodSync(outPath, 0o755);

  // Package .vsix.
  console.log(`Packaging ${t.vscodeTarget}...`);
  execSync(`npx vsce package --target ${t.vscodeTarget}`, {
    cwd: EXT_DIR,
    stdio: "inherit",
  });
}

// Clean up bin dir after packaging.
if (fs.existsSync(BIN_DIR)) {
  fs.rmSync(BIN_DIR, { recursive: true });
}

console.log("\nDone. .vsix files:");
const vsixFiles = fs.readdirSync(EXT_DIR).filter((f) => f.endsWith(".vsix"));
for (const f of vsixFiles) {
  const stat = fs.statSync(path.join(EXT_DIR, f));
  console.log(`  ${f} (${(stat.size / 1024 / 1024).toFixed(1)} MB)`);
}
