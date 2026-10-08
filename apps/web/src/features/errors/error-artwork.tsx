/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */

// Decorative only: recovery actions and the readable status belong to the frame.
const marks: Record<string, string> = {
  '401': 'M28 21a9 9 0 1 0-7 7L9 40H3v-6l12-12 M28 14h.01',
  '403': 'M24 4 40 11v12c0 10-7 17-16 21C15 40 8 33 8 23V11Z M17 24h14',
  '404': 'M17 17a7 7 0 1 1 12 5c-4 3-5 4-5 9 M24 38h.01',
  '429': 'M17 14v20 M31 14v20 M8 8a23 23 0 1 1-5 24 M3 7v9h9',
  '503':
    'M31 5a12 12 0 0 0-14 16L5 33a7 7 0 0 0 10 10l12-12A12 12 0 0 0 43 17l-8 8-12-12Z',
}
const disconnected =
  'M16 4v10 M32 4v10 M12 14h24v5a12 12 0 0 1-24 0Z M24 31v5 M18 42l6-6 6 6'

export function ErrorArtwork({ status }: { status?: string | number }) {
  return (
    <svg
      className='error-editorial-art'
      viewBox='0 0 520 440'
      fill='none'
      aria-hidden='true'
      focusable='false'
    >
      <circle className='error-art-halo' cx='260' cy='220' r='172' />
      <g className='error-art-orbits'>
        <ellipse
          cx='260'
          cy='220'
          rx='214'
          ry='116'
          transform='rotate(-32 260 220)'
        />
        <ellipse
          cx='260'
          cy='220'
          rx='174'
          ry='158'
          transform='rotate(28 260 220)'
        />
        <path d='M44 220h92m248 0h92M260 26v62m0 264v62' />
      </g>
      <g className='error-art-trace'>
        <path d='M75 280h63a24 24 0 0 0 24-24v-12a24 24 0 0 1 24-24h24' />
        <path d='M310 220h24a24 24 0 0 0 24-24v-12a24 24 0 0 1 24-24h63' />
      </g>
      <g className='error-art-packets'>
        <rect
          className='error-art-token'
          x='58'
          y='263'
          width='34'
          height='34'
          rx='10'
        />
        <path className='error-art-token-mark' d='m79 273-8 7 8 7' />
        <rect
          className='error-art-token'
          x='428'
          y='143'
          width='34'
          height='34'
          rx='10'
        />
        <path className='error-art-token-mark' d='m441 153 8 7-8 7' />
        <circle className='error-art-dot' cx='169' cy='79' r='6' />
        <circle className='error-art-dot' cx='353' cy='361' r='4' />
      </g>
      <rect
        className='error-art-core-shadow'
        x='202'
        y='166'
        width='116'
        height='116'
        rx='32'
      />
      <rect
        className='error-art-core'
        x='202'
        y='158'
        width='116'
        height='116'
        rx='32'
      />
      <svg x='236' y='192' width='48' height='48' viewBox='0 0 48 48'>
        <path
          className='error-art-symbol'
          d={marks[String(status)] ?? disconnected}
        />
      </svg>
      <path className='error-art-spark' d='M369 78v16m-8-8h16M125 344v12m-6-6h12' />
    </svg>
  )
}
