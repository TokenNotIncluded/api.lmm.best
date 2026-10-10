export const CALLBACK_PATH = "/oauth/lmm/callback";
export class LmmError extends Error {
  constructor(
    readonly code: string,
    message: string,
  ) {
    super(message);
    this.name = "LmmError";
  }
}
export const ISSUER = "https://api.lmm.best";
export const RESOURCE = `${ISSUER}/api/oauth2`;
export const CLIENT = "lmm-vscode";
export const SCOPES = [
  "catalog:read",
  "balance:read",
  "usage:read",
  "models:invoke",
];
export function assert(
  value: unknown,
  message = "Invalid LMM response.",
): asserts value {
  if (!value) throw new LmmError("protocol", message);
}
export function record(value: unknown): Record<string, any> {
  assert(value && typeof value === "object" && !Array.isArray(value));
  return value as Record<string, any>;
}
export function safeError(error: unknown): string {
  return error instanceof LmmError
    ? error.message
    : "LMM request failed. Check your connection or sign in again.";
}
