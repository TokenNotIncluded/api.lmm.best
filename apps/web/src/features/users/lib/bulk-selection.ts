/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import type { User } from '../types'

/**
 * Bulk actions on users are read-only (copy) because the admin API exposes no
 * bulk mutation endpoint; each destructive action must go through a per-user
 * confirmation dialog. These helpers build the plain-text payloads the bulk
 * toolbar copies to the clipboard.
 */

/** One value per line, in the order the rows appear in the table. */
export function joinLines(values: string[]): string {
  return values.join('\n')
}

export function collectUsernames(users: User[]): string[] {
  return users
    .map((user) => user.username?.trim())
    .filter((username): username is string => Boolean(username))
}

export function collectEmails(users: User[]): string[] {
  return users
    .map((user) => user.email?.trim())
    .filter((email): email is string => Boolean(email))
}

/** `username <email>` per line, so a pasted list stays attributable. */
export function collectContacts(users: User[]): string[] {
  const contacts: string[] = []
  for (const user of users) {
    const username = user.username?.trim()
    const email = user.email?.trim()
    if (username && email) {
      contacts.push(`${username} <${email}>`)
    } else if (username) {
      contacts.push(username)
    } else if (email) {
      contacts.push(email)
    }
  }
  return contacts
}

/** Counts rows that carry no email, so the copy result can be explained. */
export function countMissingEmails(users: User[]): number {
  return users.filter((user) => !user.email?.trim()).length
}
