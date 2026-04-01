import * as path from "path";
import * as os from "os";
import { ExtensionContext, workspace } from "vscode";
import {
  LanguageClient,
  LanguageClientOptions,
  ServerOptions,
} from "vscode-languageclient/node";

let client: LanguageClient | undefined;

export function activate(context: ExtensionContext): void {
  const serverPath = getServerPath(context);

  const serverOptions: ServerOptions = {
    run: { command: serverPath },
    debug: { command: serverPath },
  };

  const clientOptions: LanguageClientOptions = {
    documentSelector: [{ scheme: "file", language: "makefile" }],
    synchronize: {
      fileEvents: workspace.createFileSystemWatcher("**/{Makefile,makefile,GNUmakefile,*.mk,*.mak}"),
    },
  };

  client = new LanguageClient(
    "make-ls",
    "Makefile Language Server",
    serverOptions,
    clientOptions
  );

  client.start();
}

export function deactivate(): Thenable<void> | undefined {
  if (!client) {
    return undefined;
  }
  return client.stop();
}

function getServerPath(context: ExtensionContext): string {
  // Check for a user-configured path first.
  const config = workspace.getConfiguration("make-ls");
  const customPath = config.get<string>("serverPath");
  if (customPath) {
    return customPath;
  }

  // Use the bundled binary.
  const platform = os.platform();
  const arch = os.arch();

  let binaryName = "make-ls";
  if (platform === "win32") {
    binaryName = "make-ls.exe";
  }

  // The binary is at bin/make-ls relative to the extension root.
  // Platform-specific extensions bundle exactly one binary.
  const bundled = path.join(context.extensionPath, "bin", binaryName);
  return bundled;
}
