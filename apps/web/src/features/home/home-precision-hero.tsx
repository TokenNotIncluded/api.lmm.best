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
import { type ReactNode, useEffect, useId, useRef } from 'react'
import { useStatus } from '@/hooks/use-status'
import { PRECISION_CONNECTIONS, PRECISION_PROTOCOLS } from './home-precision-copy'
import { mountPrecisionMotion } from './home-precision-motion'
import './home-precision.css'

type Props = {
  t: (key: string) => string
  primaryAction: ReactNode
  pricingAction: ReactNode
}

export function HomePrecisionHero({ t, primaryAction, pricingAction }: Props) {
  const root = useRef<HTMLElement>(null)
  const id = useId()
  const { capabilitiesReady, error } = useStatus()
  useEffect(() => {
    if (root.current) return mountPrecisionMotion(root.current)
  }, [])
  return (
    <section className='lmm-precision' ref={root} aria-labelledby={`${id}-title`}>
      <div className='lmm-precision-backdrop' aria-hidden='true'>
        <video data-precision-video muted loop playsInline preload='none' tabIndex={-1} />
        <svg className='lmm-precision-sculpture' viewBox='0 0 720 720' fill='none'>
          <defs>
            <linearGradient id={`${id}-chrome`} x1='115' y1='80' x2='570' y2='560' gradientUnits='userSpaceOnUse'>
              <stop stopColor='#080a0d' />
              <stop offset='.19' stopColor='#525b65' />
              <stop offset='.28' stopColor='#f6f9ff' />
              <stop offset='.33' stopColor='#858e99' />
              <stop offset='.46' stopColor='#13171d' />
              <stop offset='.61' stopColor='#adb7c3' />
              <stop offset='.68' stopColor='#f5f7fb' />
              <stop offset='.75' stopColor='#4d5762' />
              <stop offset='1' stopColor='#090b0e' />
            </linearGradient>
            <linearGradient id={`${id}-edge`} x1='100' y1='90' x2='570' y2='640' gradientUnits='userSpaceOnUse'>
              <stop stopColor='#f4f8ff' stopOpacity='.85' />
              <stop offset='.47' stopColor='#e6edff' stopOpacity='0' />
              <stop offset='1' stopColor='#f4f8ff' stopOpacity='.6' />
            </linearGradient>
          </defs>
          <g stroke={`url(#${id}-chrome)`} strokeWidth='48'>
            <ellipse cx='360' cy='350' rx='252' ry='116' transform='rotate(-42 360 350)' />
            <ellipse cx='360' cy='350' rx='252' ry='116' transform='rotate(42 360 350)' />
            <path d='M437.6 436.2A252 116 -42 0 1 282.4 263.8' />
          </g>
          <g stroke={`url(#${id}-edge)`} strokeWidth='1.3'>
            <ellipse cx='360' cy='350' rx='274' ry='138' transform='rotate(-42 360 350)' />
            <ellipse cx='360' cy='350' rx='274' ry='138' transform='rotate(42 360 350)' />
          </g>
          <path d='M342 350h36m-18-18v36' stroke='#10b981' strokeWidth='1' />
          <circle cx='360' cy='350' r='6' stroke='#10b981' />
          <g stroke='#a7b6c5' strokeOpacity='.25' strokeWidth='.8'>
            <path d='M517 185h104l25-25M226 535h-92l-30 30' />
            <circle cx='517' cy='185' r='3' />
            <circle cx='226' cy='535' r='3' />
          </g>
        </svg>
        <div className='lmm-precision-vignette' />
        <div className='lmm-precision-grid' />
      </div>
      <div className='lmm-precision-topline' aria-hidden='true'>
        <span>LMM / INTELLIGENCE INTERFACE</span><span>EST. CONNECTION / 001</span>
      </div>
      <div className='lmm-precision-body'>
        <div className='lmm-precision-copy'>
          <p className='lmm-precision-kicker'><i /> {t('One endpoint')} <b /></p>
          <h1 id={`${id}-title`} className='lmm-precision-title'>
            <span>IDEAS.</span><span>CONNECTED.</span><span>MODELS.</span><span>UNIFIED.</span>
          </h1>
          <p className='lmm-precision-description'>
            {t('Chat, reasoning, vision, and audio models behind one endpoint.')}
          </p>
          <div className='lmm-precision-actions'>
            {primaryAction}
            <button type='button' className='lmm-precision-demo-button' data-precision-open>
              <span aria-hidden='true'>▷</span>{t('Motion preview')}
            </button>
          </div>
          <div className='lmm-precision-secondary'>{pricingAction}</div>
          <p className='lmm-precision-access'>
            {t('Developer access requires approval. Payment does not unlock access.')}
          </p>
        </div>
        <aside className='lmm-precision-hud' aria-label={t('Connection profile')}>
          <div className='lmm-precision-hud-header'>
            <span data-confirmed={capabilitiesReady} role='status'>
              <i />{t(error ? 'Status unavailable' : capabilitiesReady ? 'Configuration loaded' : 'Checking configuration')}
            </span>
            <span>01 / API</span>
          </div>
          <p className='lmm-precision-hud-label'>{t('API Endpoints')}</p>
          <dl className='lmm-precision-routes'>
            {PRECISION_PROTOCOLS.map(([label, path]) => (
              <div key={path}><dt>{label}</dt><dd><code>{path}</code><span aria-hidden='true'>↗</span></dd></div>
            ))}
          </dl>
          <div className='lmm-precision-auth'>
            <span>{t('Connection method')}</span><span>OAuth 2.0 / API KEY</span>
          </div>
          <p className='lmm-precision-hud-note'>
            {t('Configuration snapshot, not a service-health measurement.')}
          </p>
          <a href='/guide' className='lmm-precision-hud-link'>{t('Read the guide')}<span aria-hidden='true'>↗</span></a>
        </aside>
      </div>
      <div className='lmm-precision-metrics'>
        {[
          [1, t('One endpoint'), 'api.lmm.best'],
          [PRECISION_PROTOCOLS.length, t('API Endpoints'), 'Chat · Messages · Gemini'],
          [PRECISION_CONNECTIONS.length, t('Connection method'), 'OAuth · API key'],
        ].map(([value, label, detail]) => (
          <div className='lmm-precision-metric' key={label}>
            <span className='lmm-precision-number' data-precision-count={value} aria-hidden='true'>{value}</span>
            <span className='lmm-precision-sr-only'>{value}</span>
            <div><strong>{label}</strong><span>{detail}</span></div>
          </div>
        ))}
      </div>
      <button type='button' className='lmm-precision-pause' data-precision-pause aria-pressed='false'>
        <span className='lmm-precision-pause-label'>Ⅱ {t('Pause')}</span>
        <span className='lmm-precision-resume-label'>▷ {t('Resume')}</span>
      </button>
      <dialog className='lmm-precision-dialog' data-precision-dialog aria-labelledby={`${id}-demo-title`} aria-describedby={`${id}-demo-note`}>
        <div className='lmm-precision-dialog-heading'>
          <h2 id={`${id}-demo-title`}>{t('Motion preview')}</h2>
          <button type='button' data-precision-close aria-label={t('Close')}>×</button>
        </div>
        <video data-precision-demo controls playsInline preload='none' />
        <p className='lmm-precision-demo-error' role='status'>{t('Video unavailable. You can still explore the setup guide.')}</p>
        <p id={`${id}-demo-note`}>{t('Abstract brand film. Not an API performance demonstration.')}</p>
        <a href='/guide'>{t('Read the guide')} ↗</a>
      </dialog>
    </section>
  )
}
