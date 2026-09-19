/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import type { ReactNode, Ref } from 'react'

import { Button } from '@/components/ui/button'
import type { ConnectionMethod } from '@/features/onboarding/next-step'

type HomeLandingProps = {
  rootRef: Ref<HTMLElement>
  t: (key: string) => string
  primaryAction: ReactNode
  pricingAction: ReactNode
  assistant: ReactNode
  pi: ReactNode
  code: ReactNode
  destinations: ReactNode
  scripts: ReactNode
  purchase: ReactNode
  connectionMethod: ConnectionMethod
  onConnectionMethodChange: (method: ConnectionMethod) => void
}

function Arrow({ diagonal = false }: { diagonal?: boolean }) {
  return (
    <svg
      viewBox='0 0 24 24'
      fill='none'
      stroke='currentColor'
      strokeWidth='1.5'
      aria-hidden='true'
    >
      {diagonal ? (
        <path d='M5 19 19 5M5 5h14v14' />
      ) : (
        <path d='M4 12h16m-6-6 6 6-6 6' />
      )}
    </svg>
  )
}

const STEPS = [
  [
    'Download an AI app',
    'Find official downloads and instructions for your device.',
  ],
  [
    'Connect your API key',
    'After access approval, create a key and configure your app.',
  ],
  [
    'Send your first message',
    'Choose a model, test the connection, and check your usage.',
  ],
] as const

const OAUTH_STEPS = [
  [
    'Sign in and authorize',
    'Use Pi or a client that supports LMM OAuth. No manual API key is needed.',
  ],
  [
    'Choose a model',
    'After access approval, choose a model available to your account.',
  ],
  [
    'Send your first message',
    'Choose a model, test the connection, and check your usage.',
  ],
] as const

/** Presentation only. Account state and actions stay in ForgeHome. */
export function HomeLanding({
  rootRef,
  t,
  primaryAction,
  pricingAction,
  assistant,
  pi,
  code,
  destinations,
  scripts,
  purchase,
  connectionMethod,
  onConnectionMethodChange,
}: HomeLandingProps) {
  const headline = t('Make room for your next idea.')
  const steps = connectionMethod === 'oauth' ? OAUTH_STEPS : STEPS
  return (
    <main className='lmm-home' ref={rootRef}>
      <section className='lmm-intro' aria-labelledby='lmm-home-title'>
        <p className='lmm-eyebrow'>
          <span className='lmm-wordmark'>
            lmm<span>↗</span>
          </span>
          <span>{t('One endpoint')}</span>
        </p>
        <h1 id='lmm-home-title'>
          {headline.split(/(?<=[，,])\s*/u).map((phrase) => (
            <span className='lmm-title-phrase' key={phrase}>
              {phrase}
            </span>
          ))}
        </h1>
        <p className='lmm-intro-description'>
          {t(
            'Choose your client to get started. Available models and account pricing are shown after access approval.'
          )}
        </p>
        <div className='lmm-intro-actions'>
          {primaryAction}
          {pricingAction}
        </div>
        <p className='lmm-access-note'>
          {t(
            'Developer access requires approval. Payment does not unlock access.'
          )}
        </p>
      </section>

      <section
        className='lmm-cinema'
        data-cinema
        aria-label={t('One endpoint')}
      >
        <div className='lmm-cinema-inner' data-cinema-inner>
          <canvas className='lmm-film' data-film aria-hidden='true' />
          <div className='lmm-cinema-grain' aria-hidden='true' />
          <div className='lmm-cinema-topline'>
            <span>LMM / CONNECTION STUDY</span>
            <span>01 — ∞</span>
          </div>
          <div className='lmm-cinema-caption'>
            <p>{t('One endpoint')}</p>
            <span>
              api.lmm.best<span className='lmm-caption-dot'>.</span>
            </span>
          </div>
          <div className='lmm-request-sample' data-depth-card>
            <div className='lmm-sample-heading'>
              <span>{t('API Requests')}</span>
              <span>{t('Example')}</span>
            </div>
            <div className='lmm-sample-route'>
              <span>POST</span>
              <code>/v1/chat/completions</code>
            </div>
            <pre>
              <code>
                {
                  '{\n  "model": "model-name",\n  "messages": [{\n    "role": "user",\n    "content": "Hello"\n  }]\n}'
                }
              </code>
            </pre>
            <div className='lmm-sample-bottom'>
              <span>{t('One endpoint')}</span>
              <span aria-hidden='true'>↗</span>
            </div>
          </div>
          <button
            className='lmm-motion-toggle'
            type='button'
            data-motion-toggle
            aria-pressed='false'
            aria-label={t('Pause')}
          >
            <span data-pause-icon>
              <svg viewBox='0 0 20 20' aria-hidden='true'>
                <path d='M7 5v10M13 5v10' />
              </svg>
            </span>
            <span data-play-icon hidden>
              <svg viewBox='0 0 20 20' aria-hidden='true'>
                <path d='m7 4 9 6-9 6z' />
              </svg>
            </span>
            <span data-pause-label>{t('Pause')}</span>
            <span data-play-label hidden>
              {t('Resume')}
            </span>
          </button>
        </div>
      </section>
      <div className='lmm-protocols' aria-label={t('API Endpoints')}>
        <span>Chat Completions</span>
        <span>Claude Messages</span>
        <span>Gemini</span>
        <span>Pi / OAuth</span>
      </div>

      <section
        className='lmm-section lmm-connect'
        aria-labelledby='lmm-connect-title'
      >
        <div className='lmm-section-intro'>
          <p className='lmm-eyebrow'>01 / {t('Guide')}</p>
          <h2 id='lmm-connect-title'>
            {t('From download to your first conversation')}
          </h2>
          <a className='lmm-text-link' href='/guide'>
            {t('Read the guide')}
            <Arrow />
          </a>
        </div>
        <div
          className='flex flex-wrap gap-3 py-5'
          role='group'
          aria-label={t('Connection method')}
        >
          <Button
            variant={connectionMethod === 'oauth' ? 'default' : 'outline'}
            aria-pressed={connectionMethod === 'oauth'}
            onClick={() => onConnectionMethodChange('oauth')}
          >
            {t('Pi / LMM OAuth')}
          </Button>
          <Button
            variant={connectionMethod === 'api-key' ? 'default' : 'outline'}
            aria-pressed={connectionMethod === 'api-key'}
            onClick={() => onConnectionMethodChange('api-key')}
          >
            {t('Other clients / API key')}
          </Button>
        </div>
        <div className='lmm-story' data-story>
          <div className='lmm-story-steps'>
            {steps.map(([title, description], index) => (
              <article
                id={`lmm-step-${index + 1}`}
                className='lmm-story-step'
                data-story-step
                key={title}
              >
                <div className='lmm-step-number' aria-hidden='true'>
                  0{index + 1}
                </div>
                <div>
                  <h3>{t(title)}</h3>
                  <p>{t(description)}</p>
                  <a className='lmm-text-link' href='/guide'>
                    {t('Read the guide')}
                    <Arrow />
                  </a>
                </div>
              </article>
            ))}
          </div>
          {connectionMethod === 'api-key' ? (
            <div className='lmm-story-preview'>
              <nav className='lmm-story-controls' aria-label={t('Guide')}>
                {steps.map(([title], index) => (
                  <a
                    key={title}
                    href={`#lmm-step-${index + 1}`}
                    data-step-link={index}
                    aria-label={t(title)}
                  >
                    0{index + 1}
                  </a>
                ))}
                <i aria-hidden='true' />
              </nav>
              <div className='lmm-code-surface lmm-story-panels'>
                <div
                  className='lmm-story-panel lmm-model-panel'
                  data-story-panel='0'
                >
                  <p className='lmm-panel-eyebrow'>{t('Models and pricing')}</p>
                  <h3>{t('API Endpoints')}</h3>
                  <div className='lmm-protocol-row'>
                    <span>C</span>
                    <div>
                      Chat Completions<small>/v1/chat/completions</small>
                    </div>
                    <Arrow diagonal />
                  </div>
                  <div className='lmm-protocol-row'>
                    <span>A</span>
                    <div>
                      Claude Messages<small>/v1/messages</small>
                    </div>
                    <Arrow diagonal />
                  </div>
                  <div className='lmm-protocol-row'>
                    <span>G</span>
                    <div>
                      Gemini<small>/v1beta/models</small>
                    </div>
                    <Arrow diagonal />
                  </div>
                  <p className='lmm-panel-note'>
                    {t(
                      'Find official downloads and instructions for your device.'
                    )}
                  </p>
                </div>
                <div
                  className='lmm-story-panel lmm-endpoint-panel'
                  data-story-panel='1'
                >
                  <p className='lmm-panel-eyebrow'>
                    {t('Connect your API key')}
                  </p>
                  <span className='lmm-endpoint-mark' aria-hidden='true'>
                    ↗
                  </span>
                  <h3>{t('One endpoint')}</h3>
                  <code>api.lmm.best</code>
                  <p className='lmm-panel-note'>
                    {t(
                      'After access approval, create a key and configure your app.'
                    )}
                  </p>
                </div>
                <div className='lmm-story-panel' data-story-panel='2'>
                  {code}
                </div>
              </div>
            </div>
          ) : (
            <div className='lmm-story-preview'><h3 className='mb-4 text-xl font-semibold'>{t('Use Pi without manually creating an API key')}</h3>{pi}</div>
          )}
        </div>
      </section>

      <section
        className='lmm-section lmm-assistant-section'
        aria-labelledby='lmm-assistant-title'
      >
        <div>
          <p className='lmm-eyebrow'>03 / {t('Guide')}</p>
          <h2 id='lmm-assistant-title'>{t('Tell us what you want to do')}</h2>
          <p className='lmm-assistant-description'>
            {t('New here? Start with the setup guide')}
          </p>
        </div>
        <div className='lmm-assistant-surface'>{assistant}</div>
      </section>

      <section
        className='lmm-section lmm-explore'
        aria-labelledby='lmm-explore-title'
      >
        <div className='lmm-section-intro'>
          <p className='lmm-eyebrow'>04 / LMM</p>
          <h2 id='lmm-explore-title'>{t('Make room for your next idea.')}</h2>
        </div>
        <div className='lmm-destinations'>{destinations}</div>
      </section>
      <section
        className='lmm-section lmm-resources'
        aria-label={t('Account and access')}
      >
        <details>
          <summary>
            {t('Scripts')}
            <Arrow diagonal />
          </summary>
          <div className='lmm-resource-body'>{scripts}</div>
        </details>
        <details>
          <summary>
            {t('Account and access')}
            <Arrow diagonal />
          </summary>
          <div className='lmm-resource-body'>{purchase}</div>
        </details>
      </section>
      <footer className='lmm-home-footer'>
        <div>
          <span>api.lmm.best</span>
          <a className='lmm-text-link' href='/guide'>
            {t('Read the guide')}
            <Arrow />
          </a>
        </div>
        <span className='lmm-footer-type' aria-hidden='true'>
          lmm<span>↗</span>
        </span>
      </footer>
    </main>
  )
}
