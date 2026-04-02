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

// Copy README from repo root so vsce includes it in the .vsix.
const readmeSrc = path.join(ROOT, "README.md");
const readmeDst = path.join(EXT_DIR, "README.md");
if (fs.existsSync(readmeSrc)) {
  fs.copyFileSync(readmeSrc, readmeDst);
}

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

// Clean up build artifacts.
if (fs.existsSync(BIN_DIR)) {
  fs.rmSync(BIN_DIR, { recursive: true });
}
if (fs.existsSync(readmeDst)) {
  fs.unlinkSync(readmeDst);
}

console.log("\nDone. .vsix files:");
const vsixFiles = fs.readdirSync(EXT_DIR).filter((f) => f.endsWith(".vsix"));
for (const f of vsixFiles) {
  const stat = fs.statSync(path.join(EXT_DIR, f));
  console.log(`  ${f} (${(stat.size / 1024 / 1024).toFixed(1)} MB)`);
}

// Publish to VS Code Marketplace.
if (process.env.VSCODE_PUBLISH_TOKEN) {
  console.log("\nPublishing to VS Code Marketplace...");
  for (const f of vsixFiles) {
    const vsixPath = path.join(EXT_DIR, f);
    console.log(`  ${f}`);
    execSync(`npx vsce publish --pat ${process.env.VSCODE_PUBLISH_TOKEN} --packagePath ${vsixPath}`, {
      cwd: EXT_DIR,
      stdio: "inherit",
    });
  }
} else {
  console.log("\nSkipping VS Code Marketplace publish (no VSCODE_PUBLISH_TOKEN).");
}

// Publish to Open VSX.
if (process.env.OPVSX_PUBLISH_TOKEN) {
  console.log("\nPublishing to Open VSX...");
  for (const f of vsixFiles) {
    const vsixPath = path.join(EXT_DIR, f);
    console.log(`  ${f}`);
    try {
      execSync(`npx ovsx publish ${vsixPath} -p ${process.env.OPVSX_PUBLISH_TOKEN}`, {
        cwd: EXT_DIR,
        stdio: ["ignore", "inherit", "inherit"],
        timeout: 120_000,
      });
    } catch (err) {
      console.error(`  Failed to publish ${f} to Open VSX: ${err.message}`);
    }
  }
} else {
  console.log("\nSkipping Open VSX publish (no OPVSX_PUBLISH_TOKEN).");
}
