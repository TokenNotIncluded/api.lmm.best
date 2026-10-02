const assert = require("node:assert/strict");
const vscode = require("vscode");
exports.run = async () => {
  const extension = vscode.extensions.getExtension(
    "TokenNotIncluded.lmm-copilot-provider",
  );
  assert.ok(
    extension,
    "LMM extension is loaded in the actual VS Code extension host",
  );
  await extension.activate();
  assert.ok(
    extension.isActive,
    "Stable LanguageModelChatProvider registration succeeds",
  );
  const commands = await vscode.commands.getCommands();
  for (const id of ["lmm.signIn", "lmm.signOut", "lmm.refreshModels"])
    assert.ok(commands.includes(id));
  await vscode.commands.executeCommand("lmm.refreshModels");
  const models = await vscode.lm.selectChatModels({ vendor: "lmm" });
  assert.deepEqual(
    models,
    [],
    "Unauthenticated discovery must not show models or trigger browser OAuth",
  );
  console.log("LMM_REAL_HOST_SMOKE_PASS");
};
