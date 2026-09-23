/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/

// Local-only prompt ideas. Nothing here talks to the network: the dice simply
// recombines these fragments so a first-time visitor has something to press.

type InspirationFragment = readonly string[]

const SUBJECTS: InspirationFragment = [
  'a glass whale drifting above a rain-soaked city',
  'a lighthouse built from stacked vinyl records',
  'a fox curled asleep inside a teapot',
  'a subway station reclaimed by mangrove roots',
  'an astronaut hanging laundry on the moon',
  'a tiny bookstore squeezed between two skyscrapers',
  'a koi pond shaped like a spiral staircase',
  'a snow leopard strolling through a neon market',
  'an orrery made of fruit peel and brass',
  'a paper crane the size of a cathedral',
  'a diver exploring a submerged library',
  'a mountain village lit only by fireflies',
]

const STYLES: InspirationFragment = [
  'in the style of a soft watercolor children’s book',
  'as a bold 1970s travel poster',
  'rendered in clean vector shapes',
  'as a grainy 35mm film photograph',
  'like a Japanese ukiyo-e woodblock print',
  'in flat risograph tones',
  'as an intricate ink line drawing',
  'in the style of a stained-glass window',
  'as a hand-thrown clay sculpture',
  'rendered as low-poly papercraft',
]

const MOODS: InspirationFragment = [
  'calm and hopeful',
  'quietly eerie',
  'warm and nostalgic',
  'bright and playful',
  'solemn and cinematic',
  'dreamy and weightless',
]

const DETAILS: InspirationFragment = [
  'shot from a low angle',
  'with deep teal shadows',
  'against a wide empty sky',
  'with a single warm light source',
  'framed through a circular window',
  'fading into morning fog',
]

/**
 * Compose a random prompt from the local fragment pools. Deterministic given a
 * `random` source, so tests can pin the output. Fragments are joined into one
 * sentence that image models read naturally.
 */
export function composePromptIdea(random: () => number = Math.random): string {
  const pick = (pool: InspirationFragment) =>
    pool[Math.min(pool.length - 1, Math.floor(random() * pool.length))]

  return [
    pick(SUBJECTS),
    pick(STYLES),
    `${pick(MOODS)}, ${pick(DETAILS)}`,
  ].join(', ')
}

/**
 * Return a shuffled batch of distinct ideas. The shuffle is Fisher-Yates so
 * repeated presses rarely repeat a prompt before the whole pool runs dry.
 */
export function drawPromptIdeas(
  count: number,
  random: () => number = Math.random
): string[] {
  const size = Math.max(1, Math.floor(count))
  const ideas: string[] = []
  const seen = new Set<string>()
  for (
    let attempt = 0;
    attempt < size * 8 && ideas.length < size;
    attempt += 1
  ) {
    const idea = composePromptIdea(random)
    if (seen.has(idea)) continue
    seen.add(idea)
    ideas.push(idea)
  }
  return ideas
}

/**
 * Roll one idea that differs from `previous`, so a repeated press always
 * visibly changes the composer instead of silently re-rolling the same text.
 */
export function nextPromptIdea(
  previous: string | undefined,
  random: () => number = Math.random
): string {
  const first = composePromptIdea(random)
  if (first !== previous) return first
  // The first roll matched. Re-roll with the attempt index mixed into the
  // source, so the retry explores the pool instead of returning the same
  // sentence whenever the source happens to be constant.
  for (let attempt = 1; attempt <= 12; attempt += 1) {
    const idea = composePromptIdea(() => (random() + attempt * 0.37) % 1)
    if (idea !== previous) return idea
  }
  return first
}

/**
 * How the dice applies an idea to the composer. An empty prompt is filled;
 * an existing prompt is appended on a new line so the dice never destroys
 * what the user already typed. Trimmed first, so trailing blank lines from
 * repeated presses do not accumulate.
 */
export function applyPromptIdea(current: string, idea: string): string {
  const trimmed = current.trimEnd()
  if (!trimmed) return idea
  return `${trimmed}\n${idea}`
}
