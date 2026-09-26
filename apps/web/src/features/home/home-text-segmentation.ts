/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */

/** Keep word wrapping natural while animating complete Unicode graphemes. */
export function segmentMovingText(text: string, language: string) {
  const available = typeof Intl.Segmenter === 'function'
  let locale: string | undefined
  try {
    locale = Intl.getCanonicalLocales(
      language.replace(/^([a-z]{2})([A-Z]{2})$/u, '$1-$2')
    )[0]
  } catch {
    locale = undefined
  }
  const words = available
    ? [
        ...new Intl.Segmenter(locale, { granularity: 'word' }).segment(text),
      ].map((part) => part.segment)
    : text.split(/(\s+)/u)
  const graphemes = available
    ? new Intl.Segmenter(locale, { granularity: 'grapheme' })
    : null
  // Closing punctuation travels with its word, so inline-block animation
  // never leaves a Chinese full stop or comma stranded at the next line.
  const wrappingWords: string[] = []
  for (const word of words) {
    const previous = wrappingWords.at(-1)
    if (
      previous &&
      !/^\s+$/u.test(previous) &&
      /^[\p{Pe}\p{Pf},.!?;:，。！？；：、…]+$/u.test(word)
    ) {
      wrappingWords[wrappingWords.length - 1] += word
    } else {
      wrappingWords.push(word)
    }
  }
  return wrappingWords.map((word) => ({
    word,
    whitespace: /^\s+$/u.test(word),
    letters: graphemes
      ? [...graphemes.segment(word)].map((part) => part.segment)
      : Array.from(word.normalize('NFC')),
  }))
}
