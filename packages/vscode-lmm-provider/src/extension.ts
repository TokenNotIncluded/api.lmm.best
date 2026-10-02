import * as vscode from "vscode";
import { Auth, jsonRequest } from "./auth";
import { assert, ISSUER, LmmError, safeError } from "./protocol";
import { CatalogModel, catalogModels, streamCompletion } from "./wire";
interface Model extends vscode.LanguageModelChatInformation {
  entry: CatalogModel;
}
function cancellation(token: vscode.CancellationToken, timeout = 180000) {
  const controller = new AbortController();
  const disposable = token.onCancellationRequested(() => controller.abort());
  if (token.isCancellationRequested) controller.abort();
  return {
    signal: AbortSignal.any([controller.signal, AbortSignal.timeout(timeout)]),
    dispose: () => disposable.dispose(),
  };
}
export function messagesToWire(
  messages: readonly vscode.LanguageModelChatRequestMessage[],
) {
  const result: Record<string, unknown>[] = [];
  for (const message of messages) {
    const role =
      message.role === vscode.LanguageModelChatMessageRole.Assistant
        ? "assistant"
        : "user";
    let text = "";
    const calls: Record<string, unknown>[] = [],
      results: Record<string, unknown>[] = [];
    for (const part of message.content) {
      if (part instanceof vscode.LanguageModelTextPart) text += part.value;
      else if (part instanceof vscode.LanguageModelToolCallPart)
        calls.push({
          id: part.callId,
          type: "function",
          function: { name: part.name, arguments: JSON.stringify(part.input) },
        });
      else if (part instanceof vscode.LanguageModelToolResultPart) {
        assert(
          part.content.every((p) => p instanceof vscode.LanguageModelTextPart),
          "This LMM provider supports text tool results only.",
        );
        results.push({
          role: "tool",
          tool_call_id: part.callId,
          content: part.content
            .map((p) => (p as vscode.LanguageModelTextPart).value)
            .join("\n"),
        });
      } else
        throw new LmmError(
          "content",
          "This LMM provider supports text and tool messages only.",
        );
    }
    assert(!calls.length || role === "assistant");
    if (text || calls.length || !results.length)
      result.push({
        role,
        content: text || (calls.length ? null : ""),
        ...(calls.length ? { tool_calls: calls } : {}),
      });
    result.push(...results);
  }
  return result;
}
class Provider implements vscode.LanguageModelChatProvider<Model> {
  readonly changes = new vscode.EventEmitter<void>();
  readonly onDidChangeLanguageModelChatInformation = this.changes.event;
  constructor(
    private readonly auth: Auth,
    private readonly signIn: () => Promise<void>,
  ) {}
  async provideLanguageModelChatInformation(
    options: vscode.PrepareLanguageModelChatModelOptions,
    token: vscode.CancellationToken,
  ): Promise<Model[]> {
    const cancel = cancellation(token);
    try {
      if (!(await this.auth.current())) {
        if (options.silent) return [];
        await this.signIn();
      }
      const c = await this.auth.token(cancel.signal);
      const models = catalogModels(
        await jsonRequest("/api/oauth2/catalog", {
          headers: { authorization: `Bearer ${c.access}` },
          signal: cancel.signal,
        }),
        c.scope,
      );
      const config = vscode.workspace.getConfiguration("lmm");
      const toolModels = config.get<string[]>("toolModels", []);
      return models.map((entry) => ({
        entry,
        id: entry.id,
        name: entry.upstream_model,
        family: entry.upstream_model,
        version: "1",
        detail: entry.group,
        tooltip:
          "LMM OAuth · Usage is billed to your LMM account. Token budgets are configurable estimates.",
        maxInputTokens: config.get<number>("maxInputTokens", 32768),
        maxOutputTokens: config.get<number>("maxOutputTokens", 4096),
        capabilities: {
          imageInput: false,
          toolCalling: toolModels.includes(entry.upstream_model),
        },
      }));
    } catch (error) {
      if (token.isCancellationRequested) throw new vscode.CancellationError();
      throw new Error(safeError(error));
    } finally {
      cancel.dispose();
    }
  }
  async provideTokenCount(
    _model: Model,
    text: string | vscode.LanguageModelChatRequestMessage,
    token: vscode.CancellationToken,
  ): Promise<number> {
    if (token.isCancellationRequested) throw new vscode.CancellationError();
    // Conservative UTF-8-byte estimate: no paid generation/token-count network call.
    return Buffer.byteLength(
      typeof text === "string" ? text : JSON.stringify(messagesToWire([text])),
      "utf8",
    );
  }
  async provideLanguageModelChatResponse(
    model: Model,
    messages: readonly vscode.LanguageModelChatRequestMessage[],
    options: vscode.ProvideLanguageModelChatResponseOptions,
    progress: vscode.Progress<vscode.LanguageModelResponsePart>,
    token: vscode.CancellationToken,
  ): Promise<void> {
    const cancel = cancellation(token, 300000);
    try {
      const credential = await this.auth.token(cancel.signal);
      const current = catalogModels(
        await jsonRequest("/api/oauth2/catalog", {
          headers: { authorization: `Bearer ${credential.access}` },
          signal: cancel.signal,
        }),
        credential.scope,
      ).find((entry) => entry.id === model.id);
      assert(
        current,
        "This model is no longer in your LMM catalog. Refresh the model list.",
      );
      const wireMessages = messagesToWire(messages);
      const tools = options.tools ?? [];
      assert(
        !tools.length || model.capabilities.toolCalling,
        "Enable this verified model in lmm.toolModels to use tools.",
      );
      assert(
        !wireMessages.some((m) => m.tool_calls || m.role === "tool") ||
          model.capabilities.toolCalling,
        "Tool messages require a verified model in lmm.toolModels.",
      );
      const body = {
        model: current.upstream_model,
        messages: wireMessages,
        stream: true,
        max_tokens: model.maxOutputTokens,
        ...(tools.length
          ? {
              tools: tools.map((t) => ({
                type: "function",
                function: {
                  name: t.name,
                  description: t.description,
                  parameters: t.inputSchema,
                },
              })),
              tool_choice:
                options.toolMode === vscode.LanguageModelChatToolMode.Required
                  ? "required"
                  : "auto",
            }
          : {}),
      };
      const response = await fetch(`${ISSUER}/v1/chat/completions`, {
        method: "POST",
        redirect: "error",
        credentials: "omit",
        signal: cancel.signal,
        headers: {
          authorization: `Bearer ${credential.access}`,
          "x-lmm-group": current.group_id,
          "content-type": "application/json",
          accept: "text/event-stream",
        },
        body: JSON.stringify(body),
      });
      await streamCompletion(
        response,
        (part) => {
          if (part.type === "text")
            progress.report(new vscode.LanguageModelTextPart(part.text));
          else {
            assert(
              tools.some((t) => t.name === part.name),
              "LMM returned an unknown tool.",
            );
            progress.report(
              new vscode.LanguageModelToolCallPart(
                part.id,
                part.name,
                part.input,
              ),
            );
          }
        },
        cancel.signal,
      );
    } catch (error) {
      if (token.isCancellationRequested) throw new vscode.CancellationError();
      throw new Error(safeError(error));
    } finally {
      cancel.dispose();
    }
  }
}
export function activate(context: vscode.ExtensionContext): void {
  const auth = new Auth(
    context.secrets,
    fetch,
    context.globalStorageUri.fsPath,
  );
  let provider: Provider;
  let login: Promise<void> | undefined;
  const signIn = (): Promise<void> => {
    if (login) return login;
    login = Promise.resolve(
      vscode.window.withProgress(
        {
          location: vscode.ProgressLocation.Notification,
          title: "Signing in to LMM in your browser…",
          cancellable: true,
        },
        async (_progress, token) => {
          const cancel = cancellation(token);
          try {
            await auth.login(async (url) => {
              assert(
                await vscode.env.openExternal(vscode.Uri.parse(url)),
                "Could not open the browser.",
              );
            }, cancel.signal);
            provider.changes.fire();
            vscode.window.showInformationMessage(
              "Signed in to LMM. Select an LMM model in Chat.",
            );
          } catch (error) {
            if (!token.isCancellationRequested)
              vscode.window.showErrorMessage(safeError(error));
            throw error;
          } finally {
            cancel.dispose();
          }
        },
      ),
    ).then(() => {});
    void login.catch(() => {});
    void login
      .finally(() => {
        login = undefined;
      })
      .catch(() => {});
    return login;
  };
  provider = new Provider(auth, signIn);
  context.subscriptions.push(
    provider.changes,
    vscode.lm.registerLanguageModelChatProvider("lmm", provider),
    vscode.commands.registerCommand("lmm.signIn", signIn),
    vscode.commands.registerCommand("lmm.signOut", async () => {
      try {
        await auth.logout();
        vscode.window.showInformationMessage("Signed out of LMM.");
      } catch (error) {
        vscode.window.showWarningMessage(
          `${safeError(error)} Local login was removed.`,
        );
      } finally {
        provider.changes.fire();
      }
    }),
    vscode.commands.registerCommand("lmm.refreshModels", () =>
      provider.changes.fire(),
    ),
    vscode.workspace.onDidChangeConfiguration((event) => {
      if (event.affectsConfiguration("lmm")) provider.changes.fire();
    }),
  );
}
