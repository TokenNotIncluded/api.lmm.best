/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/

type ClipboardWriter = Pick<Clipboard, 'writeText'>

type CopyNotifications = {
  success: () => void
  error: () => void
}

export function notifyAssistantCopyResult(
  copied: boolean,
  notifications: CopyNotifications
): void {
  if (copied) {
    notifications.success()
    return
  }
  notifications.error()
}

export async function copyAssistantText(
  value: string,
  clipboard: ClipboardWriter | undefined | null
): Promise<boolean> {
  if (!clipboard?.writeText) return false

  try {
    await clipboard.writeText(value)
    return true
  } catch {
    return false
  }
}
