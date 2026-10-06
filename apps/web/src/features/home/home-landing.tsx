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

import { BrandLogo } from '@/components/brand-logo'
import { LMM_BRAND_NAME } from '@/components/lmm-brand-mark'
import { LmmBrandWordmark } from '@/components/lmm-brand-wordmark'
import { Button } from '@/components/ui/button'
import type { ConnectionMethod } from '@/features/onboarding/next-step'
import { RepositoryLink } from '@/features/repositories/repository-link'
import { DEFAULT_LOGO, DEFAULT_SYSTEM_NAME } from '@/lib/constants'

import { segmentMovingText } from './home-text-segmentation'

type HomeLandingProps = {
  rootRef: Ref<HTMLElement>
  brandName: string
  brandLogo: string
  t: (key: string) => string
  language: string
  primaryAction: ReactNode
  pricingAction: ReactNode
  topUpAction?: ReactNode
  assistant: ReactNode
  oauthClients: ReactNode
  code: ReactNode
  explore: ReactNode
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

function movingText(text: string, language: string) {
  let index = 0
  return segmentMovingText(text, language).map((part, wordIndex) =>
    part.whitespace ? (
      part.word
    ) : (
      <span
        className='lmm-moving-word'
        key={`${wordIndex}-${part.word}`}
        aria-hidden='true'
      >
        {part.letters.map((letter, letterIndex) => (
          <span
            className='lmm-moving-letter'
            data-gravity-glyph
            data-gravity-index={index++}
            key={`${letterIndex}-${letter}`}
          >
            {letter}
          </span>
        ))}
      </span>
    )
  )
}

function GravityDescription({
  text,
  language,
  className,
}: {
  text: string
  language: string
  className?: string
}) {
  return (
    <p className={className} data-gravity-description>
      {movingText(text, language)}
      <span className='sr-only'>{text}</span>
    </p>
  )
}

/** Presentation only. Account state and actions stay in ForgeHome. */
export function HomeLanding({
  rootRef,
  brandName,
  brandLogo,
  t,
  language,
  primaryAction,
  pricingAction,
  topUpAction,
  assistant,
  oauthClients,
  code,
  explore,
  scripts,
  purchase,
  connectionMethod,
  onConnectionMethodChange,
}: HomeLandingProps) {
  const headline = t('AI models. One connection.')
  const usesDefaultBrand =
    brandLogo === DEFAULT_LOGO && brandName === DEFAULT_SYSTEM_NAME
  const steps = connectionMethod === 'oauth' ? OAUTH_STEPS : STEPS
  return (
    <main className='lmm-home' ref={rootRef} data-motion='loading'>
      <section
        className='lmm-cinema'
        data-cinema
        aria-label={t('One endpoint')}
      >
        <div className='lmm-cinema-inner' data-cinema-inner>
          <div className='lmm-visual' data-home-visual>
            <div
              className='lmm-token-cloud'
              data-token-cloud
              role='group'
              aria-label={t('Tokens to try')}
            />
            <canvas className='lmm-film' data-film aria-hidden='true' />
            <canvas
              className='lmm-world-film'
              data-world-film
              aria-hidden='true'
            />
            <div
              className='lmm-token-input'
              data-token-input
              role='region'
              aria-label={t('Token input')}
            >
              <output
                className='lmm-token-result'
                data-token-result
                aria-label={t('Closest match')}
                hidden
              >
                <span className='sr-only' data-selected-token />
                <span aria-hidden='true'>→</span>
                <strong data-predicted-token />
              </output>
            </div>
            <details className='lmm-simulation-info'>
              <summary aria-label={t('About this visualization')}>?</summary>
              <p>{t('Local word matching. No model requests.')}</p>
            </details>
          </div>
          <span className='sr-only' id='lmm-gravity-instruction'>
            {t('Drag this title into the input, or press Enter.')}
          </span>
          <section
            className='lmm-intro lmm-scene-panel'
            data-cinema-panel='0'
            data-active
            aria-labelledby='lmm-home-title'
          >
            <h1
              id='lmm-home-title'
              data-gravity-title
              tabIndex={0}
              aria-describedby='lmm-gravity-instruction'
            >
              {headline.split(/(?<=[，,])\s*/u).map((phrase) => (
                <span className='lmm-title-phrase' key={phrase}>
                  {phrase}
                </span>
              ))}
            </h1>
            <GravityDescription
              className='lmm-intro-description'
              language={language}
              text={t(
                'Chat, images, and audio through one API. Compare prices, then connect your app.'
              )}
            />
            <div className='lmm-intro-actions'>
              {primaryAction}
              {topUpAction}
              {pricingAction}
            </div>
            <ul className='lmm-access-note'>
              <li>{t('Pay for what you use.')}</li>
              <li>{t('No manual API key required in Pi.')}</li>
            </ul>
          </section>

          <section className='lmm-scene-panel' data-cinema-panel='1'>
            <canvas
              className='lmm-chapter-film'
              data-chapter-film='1'
              aria-hidden='true'
            />
            <h2>{t('A store for our community.')}</h2>
            <p>
              {t(
                'Discover digital goods from community sellers. Browse, order, and collect in one place.'
              )}
            </p>
            <a className='lmm-core-link lmm-world-primary' href='/store'>
              {t('Browse products')} <Arrow />
            </a>
          </section>
          <section className='lmm-scene-panel' data-cinema-panel='2'>
            <canvas
              className='lmm-chapter-film'
              data-chapter-film='2'
              aria-hidden='true'
            />
            <h2>{t('Put more tools in your AI’s hands.')}</h2>
            <p>
              {t(
                'Explore MCP tool services in the tool market. Connect the capabilities your next idea needs.'
              )}
            </p>
            <a className='lmm-core-link lmm-world-primary' href='/tool-market'>
              {t('Browse MCP tools')} <Arrow />
            </a>
          </section>
          <section className='lmm-scene-panel' data-cinema-panel='3'>
            <canvas
              className='lmm-chapter-film'
              data-chapter-film='3'
              aria-hidden='true'
            />
            <h2>{t('One ecosystem. Many ways in.')}</h2>
            <p>
              {t(
                'Pi and dsh plugins, useful websites, and new connections. Find your place in the LMM ecosystem.'
              )}
            </p>
            <div className='lmm-world-links'>
              <a className='lmm-core-link' href='/guide#client-setup'>
                {t('Explore plugins')} <Arrow />
              </a>
              <a className='lmm-core-link' href='/ai-directory'>
                {t('AI directory')} <Arrow />
              </a>
            </div>
          </section>
          <section className='lmm-scene-panel' data-cinema-panel='4'>
            <canvas
              className='lmm-chapter-film'
              data-chapter-film='4'
              aria-hidden='true'
            />
            <h2>{t('The next world is ours to build.')}</h2>
            <p>
              {t(
                'An idea becomes a tool. A tool opens a new path. Bring your imagination, and help shape what comes next.'
              )}
            </p>
            <a className='lmm-core-link lmm-world-primary' href='/about'>
              {t('Explore the project')} <Arrow />
            </a>
          </section>
          <ol className='lmm-core-steps' aria-label={t('Explore LMM')}>
            {['Home', 'Store', 'Tool market', 'Ecosystem', 'Future'].map(
              (label, index) => (
                <li
                  key={label}
                  data-cinema-step
                  data-active={index === 0 || undefined}
                >
                  <button
                    type='button'
                    data-cinema-jump={index}
                    aria-pressed={index === 0}
                  >
                    {t(label)}
                  </button>
                </li>
              )
            )}
          </ol>
          <a className='lmm-core-continue' href='#lmm-connect-title'>
            {t('Continue')} <Arrow />
          </a>
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
          <h2 id='lmm-connect-title'>{t('Connect in three steps')}</h2>
          <a className='lmm-text-link' href='/guide'>
            {t('Read the guide')}
            <Arrow />
          </a>
        </div>
        <div
          className='lmm-connection-method'
          role='group'
          aria-label={t('Connection method')}
        >
          <Button
            variant={connectionMethod === 'oauth' ? 'default' : 'outline'}
            aria-pressed={connectionMethod === 'oauth'}
            onClick={() => onConnectionMethodChange('oauth')}
          >
            LMM OAuth
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
                    {t('Point your client at this base URL.')}
                  </p>
                </div>
                <div
                  className='lmm-story-panel lmm-endpoint-panel'
                  data-story-panel='1'
                >
                  <span className='lmm-endpoint-mark' aria-hidden='true'>
                    <Arrow diagonal />
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
            <div className='lmm-story-preview'>
              <h3 className='mb-4 text-xl font-semibold'>LMM OAuth</h3>
              {oauthClients}
            </div>
          )}
        </div>
      </section>

      <section
        className='lmm-section lmm-assistant-section'
        aria-labelledby='lmm-assistant-title'
      >
        <div>
          <h2 id='lmm-assistant-title'>{t('Need a hand connecting?')}</h2>
          <p className='lmm-assistant-description'>
            {t('Ask about models, clients, or connection errors.')}
          </p>
        </div>
        <div className='lmm-assistant-surface'>{assistant}</div>
      </section>

      <section
        className='lmm-section lmm-explore'
        aria-labelledby='lmm-explore-title'
      >
        <div className='lmm-section-intro'>
          <h2 id='lmm-explore-title'>{t('Explore LMM')}</h2>
        </div>
        <div className='lmm-destinations'>{explore}</div>
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
          <RepositoryLink kind='project' className='lmm-home-repository' />
          <a className='lmm-text-link' href='/guide'>
            {t('Read the guide')}
            <Arrow />
          </a>
        </div>
        <span className='lmm-footer-brand'>
          {usesDefaultBrand ? (
            <LmmBrandWordmark
              className='lmm-footer-wordmark'
              title={LMM_BRAND_NAME}
            />
          ) : (
            <span className='lmm-footer-custom-brand'>
              <BrandLogo
                src={brandLogo}
                className='lmm-footer-custom-logo'
                alt=''
              />
              <span>{brandName}</span>
            </span>
          )}
        </span>
      </footer>
    </main>
  )
}
