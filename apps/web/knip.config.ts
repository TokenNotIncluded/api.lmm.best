import type { KnipConfig } from 'knip'

const config: KnipConfig = {
  entry: [
    'src/main.tsx',
    'src/debug-main.tsx',
    'src/**/*.test.{ts,tsx}',
    'src/**/__tests__/**/*.{ts,tsx}',
    'scripts/add-missing-keys.mjs',
    'scripts/operator-persona-suite.mjs',
    'scripts/production-acceptance.mjs',
    'scripts/**/*.test.mjs',
  ],
  ignore: [
    'src/components/ui/**',
    'src/i18n/static-keys.ts',
    'src/routeTree.gen.ts',
  ],
  ignoreDependencies: ['playwright', 'tailwindcss', 'tw-animate-css'],
}

export default config
