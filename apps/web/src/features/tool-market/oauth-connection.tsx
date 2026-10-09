/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { marketEndpoint } from "./connection-utils";

export function marketOAuthCommand(endpoint: string): string {
  const url = new URL(endpoint);
  if (
    url.href !== marketEndpoint(url.origin, url.pathname) ||
    !/^https?:\/\/[a-zA-Z0-9.:[\]-]+\/[a-zA-Z0-9/_-]+$/.test(endpoint)
  )
    throw new Error("Invalid MCP endpoint");
  return `codex mcp add lmm --url ${endpoint}\n# Complete browser authorization. If needed, run:\ncodex mcp login lmm`;
}

export function MarketOAuthConnection({ endpoint }: { endpoint: string }) {
  const { t } = useTranslation();
  const [status, setStatus] = useState<"copied" | "error" | null>(null);
  const command = marketOAuthCommand(endpoint);
  return (
    <div className="space-y-4">
      <h4 className="font-medium">{t("Browser login (recommended)")}</h4>
      <p className="text-muted-foreground text-sm leading-6">
        {t(
          "Run the command, sign in to LMM, and approve access. Your provider key is not sent to the client.",
        )}
      </p>
      <pre
        className="bg-muted overflow-x-auto rounded-lg p-4 text-xs leading-6"
        tabIndex={0}
      >
        {command}
      </pre>
      <Button
        variant="outline"
        onClick={async () => {
          try {
            await navigator.clipboard.writeText(command);
            setStatus("copied");
          } catch {
            setStatus("error");
          }
        }}
      >
        {t("Copy command")}
      </Button>
      {status && (
        <p role={status === "error" ? "alert" : "status"}>
          {t(status === "copied" ? "Copied" : "Copy failed")}
        </p>
      )}
      <p className="text-muted-foreground text-xs leading-5">
        {t(
          "After login, select this OAuth client and set tool access and spending limits. Login alone does not authorize spending.",
        )}
      </p>
    </div>
  );
}
