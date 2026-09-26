/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
/** Only the directory page is public; never grant access to its descendants. */
export function isPublicDirectoryPath(pathname: string): boolean {
  return pathname === '/ai-directory' || pathname === '/ai-directory/'
}
