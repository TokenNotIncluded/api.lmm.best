import * as vscode from "vscode";
import { Auth, jsonRequest } from "./auth";
import { balanceLabel } from "./balance";

export class AccountStatus implements vscode.Disposable {
  private readonly item = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Right, 50);
  private epoch = 0;
  constructor(private readonly auth: Auth) {
    this.item.command = "lmm.refreshAccount";
    this.clear();
  }
  clear(): void {
    this.epoch++;
    this.item.text = "$(account) LMM · 未登录";
    this.item.tooltip = "LMM wallet balance. Sign in using LMM: Sign In.";
    this.item.show();
  }
  async refresh(): Promise<void> {
    const epoch = ++this.epoch;
    try {
      if (!(await this.auth.current())) {
        if (epoch === this.epoch) this.clear();
        return;
      }
      const signal = AbortSignal.timeout(15000);
      const credential = await this.auth.token(signal);
      if (epoch !== this.epoch) return;
      if (!credential.scope.split(" ").includes("balance:read")) {
        this.item.text = "$(account) LMM · 未授权读取余额";
        this.item.tooltip = "Sign in again to grant balance:read. Click to refresh.";
        return;
      }
      const label = balanceLabel(await jsonRequest("/api/oauth2/balance", {
        headers: { authorization: `Bearer ${credential.access}` }, signal,
      }));
      if (epoch !== this.epoch) return;
      this.item.text = `$(account) LMM · ${label}`;
      this.item.tooltip = `钱包余额，非本次 OAuth 授权消费上限。\nUpdated ${new Date().toLocaleTimeString()}. Click to refresh; refreshed every 5 minutes and after requests.\n分组和倍率显示在 Chat 模型选择器中。`;
    } catch {
      if (epoch === this.epoch) {
        this.item.text = "$(account) LMM · 余额不可用";
        this.item.tooltip = "Balance refresh failed. Click to retry or use LMM: Sign In. No stale amount is displayed.";
      }
    }
  }
  dispose(): void { this.epoch++; this.item.dispose(); }
}
