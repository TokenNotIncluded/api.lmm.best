import type { KnipConfig } from 'knip'

const config: KnipConfig = {
  entry: [
    'src/main.tsx',
    'src/**/*.test.{ts,tsx}',
    'src/**/__tests__/**/*.{ts,tsx}',
    'scripts/**/*.test.mjs',
  ],
  ignore: ['src/components/ui/**', 'src/routeTree.gen.ts'],
  ignoreDependencies: ['tailwindcss', 'tw-animate-css'],
}

export default config
