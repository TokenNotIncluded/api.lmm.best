#!/usr/bin/env node
import { join } from "node:path";
import { homedir } from "node:os";
import { readFile } from "node:fs/promises";
import { spawn } from "node:child_process";
import { randomBytes } from "node:crypto";
import { LmmHttp } from "./vendor/http.ts";
import { LmmOAuth } from "./vendor/oauth.ts";
import { safeMessage, requireValue } from "./vendor/protocol.ts";
import { Store } from "./store.ts";
import {
  parseCatalog,
  zedSettings,
  defaultConfiguration,
  type ModelConfiguration,
} from "./catalog.ts";
import { startBridge } from "./bridge.ts";
import { configureSettings } from "./settings.ts";

async function main() {
  const [command, ...args] = process.argv.slice(2);
  const argument = (name: string) => {
    const i = args.indexOf(name);
    return i < 0 ? undefined : args[i + 1];
  };
  requireValue(
    args.every((value, i) =>
      i % 2 === 0
        ? [
            "--models",
            "--port",
            "--issuer",
            "--data-dir",
            "--settings",
          ].includes(value)
        : typeof value === "string",
    ) && args.length % 2 === 0,
    "Invalid options.",
  );
  const http = new LmmHttp({ issuer: argument("--issuer") });
  const directory =
    argument("--data-dir") ??
    join(
      process.env.XDG_STATE_HOME ?? join(homedir(), ".local", "state"),
      "lmm-zed",
    );
  const store = new Store(directory, http.issuer);
  const oauth = new LmmOAuth(
    http,
    180000,
    join(directory, "refresh-journal"),
    "lmm-zed",
    "Zed",
  );
  if (command === "login") {
    const auth = await oauth.login({
      notify: (event) => {
        if (event.url) {
          console.log("Open this URL to sign in:\n" + event.url);
          const child = spawn(
            process.platform === "darwin"
              ? "open"
              : process.platform === "win32"
                ? "explorer.exe"
                : "xdg-open",
            [event.url],
            { stdio: "ignore" },
          );
          child.on("error", () => {});
        }
      },
    });
    await store.write(auth);
    console.log("LMM OAuth login saved.");
    return;
  }
  if (command === "logout") {
    const auth = await store.read();
    await oauth.revoke(auth.access, AbortSignal.timeout(15000));
    await store.remove();
    console.log("LMM session revoked and removed.");
    return;
  }
  if (!["catalog", "settings", "configure", "start"].includes(command)) {
    console.log(
      "Usage: lmm-zed login | logout | catalog | settings [--models models.json] | configure | start [--models models.json] [--port 7391]\nOptional: --issuer https://api.lmm.best --data-dir PATH",
    );
    return;
  }
  // Serial refresh and durable one-shot journal prevent concurrent token replay.
  let pending: Promise<Awaited<ReturnType<Store["read"]>>> | undefined;
  const current = async () => {
    if (pending) return pending;
    pending = (async () => {
      let auth = await store.read();
      if (auth.expires <= Date.now() + 30000) {
        auth = await oauth.refresh(auth, AbortSignal.timeout(15000));
        await store.write(auth);
      }
      return auth;
    })();
    try {
      return await pending;
    } finally {
      pending = undefined;
    }
  };
  const auth = await current();
  const models = parseCatalog(
    await http.bearer("/api/oauth2/catalog", auth.access),
    http.resource,
    auth.scope,
  );
  if (command === "catalog") {
    console.log(JSON.stringify(models, null, 2));
    return;
  }
  const modelPath = argument("--models");
  const configuration = modelPath
    ? (JSON.parse(await readFile(modelPath, "utf8")) as ModelConfiguration[])
    : defaultConfiguration(models);
  requireValue(Array.isArray(configuration));
  const port = Number(argument("--port") ?? "7391");
  const settings = zedSettings(models, configuration, port);
  if (command === "settings") {
    console.log(JSON.stringify(settings, null, 2));
    return;
  }
  const settingsPath =
    argument("--settings") ??
    join(
      process.platform === "win32"
        ? (process.env.APPDATA ?? join(homedir(), "AppData", "Roaming"))
        : (process.env.XDG_CONFIG_HOME ?? join(homedir(), ".config")),
      "zed",
      "settings.json",
    );
  const backup = await configureSettings(
    settingsPath,
    settings.language_models.openai_compatible.lmm,
    modelPath === undefined,
  );
  console.log(
    `Zed LMM provider configured: ${settingsPath}${backup ? "\nBackup: " + backup : ""}`,
  );
  if (command === "configure") return;
  const admitted = new Set(configuration.map((model) => model.id));
  const key = randomBytes(32).toString("base64url");
  const bridge = await startBridge({
    http,
    models: models.filter((model) => admitted.has(model.id)),
    key,
    port,
    credential: current,
  });
  console.log(
    `LMM bridge listening on 127.0.0.1:${bridge.port}. Quit existing Zed before starting so it receives LMM_API_KEY.`,
  );
  const zed = spawn("zed", [], {
    env: { ...process.env, LMM_API_KEY: key },
    stdio: "inherit",
  });
  zed.on("error", () => {
    console.error("Could not start Zed. Install the zed CLI in PATH.");
    bridge.close();
    process.exitCode = 1;
  });
  const stop = () => {
    bridge.close();
  };
  process.once("SIGINT", stop);
  process.once("SIGTERM", stop);
}
main().catch((error) => {
  console.error(safeMessage(error));
  process.exitCode = 1;
});
