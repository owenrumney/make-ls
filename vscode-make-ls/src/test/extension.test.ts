import * as assert from "assert";
import * as path from "path";
import * as vscode from "vscode";

const fixtures = path.resolve(__dirname, "..", "..", "src", "test", "fixtures");

// The client starts asynchronously, so the first request can land before the
// server is listening. Retry until it answers or the deadline passes.
async function waitForServer(uri: vscode.Uri, pos: vscode.Position) {
  const deadline = Date.now() + 60_000;
  for (;;) {
    const locs = await vscode.commands.executeCommand<vscode.Location[]>(
      "vscode.executeReferenceProvider",
      uri,
      pos,
    );
    if (locs && locs.length > 0) {
      return locs;
    }
    if (Date.now() > deadline) {
      throw new Error("language server did not answer within 60s");
    }
    await new Promise((r) => setTimeout(r, 500));
  }
}

async function open(file: string): Promise<vscode.TextDocument> {
  const doc = await vscode.workspace.openTextDocument(
    vscode.Uri.file(path.join(fixtures, file)),
  );
  await vscode.window.showTextDocument(doc);
  return doc;
}

suite("make-ls extension", () => {
  test("activates and answers a references request", async () => {
    const doc = await open("Makefile");
    // line 0 "FIGURES = \" — the declaration.
    const locs = await waitForServer(doc.uri, new vscode.Position(0, 2));

    const lines = locs.map((l) => l.range.start.line).sort();
    assert.deepStrictEqual(lines, [0, 4], "declaration and the $(FIGURES) use");
  });

  test("references reach a use split across continuations", async () => {
    const doc = await open("Makefile");
    // line 4 "build/main.pdf: main.tex $(FIGURES)" — cursor inside the use.
    await waitForServer(doc.uri, new vscode.Position(0, 2));
    const defs = await vscode.commands.executeCommand<vscode.Location[]>(
      "vscode.executeDefinitionProvider",
      doc.uri,
      new vscode.Position(4, 28),
    );

    assert.strictEqual(defs.length, 1);
    assert.strictEqual(defs[0].range.start.line, 0);
  });

  test("definition follows an include above the Makefile's directory", async () => {
    const doc = await open(path.join("sub", "Makefile"));
    // line 3 "\t$(CC) -v" — defined in ../common.mk, outside this directory.
    await waitForServer(doc.uri, new vscode.Position(3, 4));
    const defs = await vscode.commands.executeCommand<vscode.Location[]>(
      "vscode.executeDefinitionProvider",
      doc.uri,
      new vscode.Position(3, 4),
    );

    assert.strictEqual(defs.length, 1);
    assert.strictEqual(path.basename(defs[0].uri.fsPath), "common.mk");
  });
});
