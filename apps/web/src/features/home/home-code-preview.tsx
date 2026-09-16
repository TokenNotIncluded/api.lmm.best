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
import { CODE_TABS, codeForTab, type CodeTab } from './home-code-examples'

type CodePreviewProps = {
  t: (key: string) => string
  tab: CodeTab
  copied: boolean
  onTabChange: (tab: CodeTab) => void
  onCopy: () => void
}

export function CodePreview({
  t,
  tab,
  copied,
  onTabChange,
  onCopy,
}: CodePreviewProps) {
  return (
    <div className='forge-home-code-card'>
      <div
        className='forge-home-code-tabs'
        role='tablist'
        aria-label={t('API Endpoints')}
      >
        {CODE_TABS.map((name, index) => (
          <button
            key={name}
            type='button'
            role='tab'
            id={`home-code-tab-${name}`}
            aria-controls='home-code-panel'
            tabIndex={tab === name ? 0 : -1}
            aria-selected={tab === name}
            className={tab === name ? 'is-active' : undefined}
            onClick={() => onTabChange(name)}
            onKeyDown={(event) => {
              const next =
                event.key === 'ArrowRight'
                  ? (index + 1) % CODE_TABS.length
                  : event.key === 'ArrowLeft'
                    ? (index + CODE_TABS.length - 1) % CODE_TABS.length
                    : event.key === 'Home'
                      ? 0
                      : event.key === 'End'
                        ? CODE_TABS.length - 1
                        : null
              if (next === null) return
              event.preventDefault()
              onTabChange(CODE_TABS[next])
              event.currentTarget.parentElement
                ?.querySelectorAll<HTMLButtonElement>('[role="tab"]')
                [next]?.focus()
            }}
          >
            {t(name)}
          </button>
        ))}
      </div>
      <div
        id='home-code-panel'
        role='tabpanel'
        aria-labelledby={`home-code-tab-${tab}`}
      >
        <div className='forge-home-code-label'>
          <span>{t('API Requests')}</span>
          <button
            className='lmm-copy-button'
            type='button'
            onClick={onCopy}
            aria-label={t('Copy')}
          >
            <span aria-live='polite'>{copied ? t('Copied') : t('Copy')}</span>
          </button>
        </div>
        <pre
          className='forge-home-code-block'
          tabIndex={0}
          aria-label={t('API Requests')}
        >
          <code>{codeForTab(tab)}</code>
        </pre>
        <p className='forge-home-code-help'>
          {t(
            'Replace model-name with an available model ID for the selected API. Set LMM_API_KEY locally; never put your key in browser code.'
          )}
        </p>
        {tab === 'API' && (
          <p className='forge-home-code-help'>
            {t('For server-side JavaScript, install the SDK first:')}

            <code>npm install openai</code>
          </p>
        )}
      </div>
    </div>
  )
}
