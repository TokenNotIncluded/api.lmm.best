/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
/** Compare the content-addressed entry, not API version or runtime timestamps. */
export function frontendEntry(doc: Document, origin: string): string | null {
  for (const script of doc.querySelectorAll<HTMLScriptElement>('script[src]')) {
    try {
      const url = new URL(script.getAttribute('src') || '', origin)
      if (
        url.origin === origin &&
        /^\/static\/js\/index\.[\w-]+\.js$/.test(url.pathname)
      ) {
        return url.pathname
      }
    } catch {
      // Ignore malformed URLs, diagnostic scripts, and error documents.
    }
  }
  return null
}
