/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
import { LmmBrandMark } from '@/components/lmm-brand-mark'
import { useSystemConfig } from '@/hooks/use-system-config'

import { ErrorArtwork } from './error-artwork'

import './error-page-frame.css'

type ErrorPageFrameProps = {
  status?: string | number
  title: React.ReactNode
  description: React.ReactNode
  actions?: React.ReactNode
  note?: React.ReactNode
  showStatus?: boolean
  artSrc?: string
}

export function ErrorPageFrame(props: ErrorPageFrameProps) {
  const { systemName } = useSystemConfig()
  return (
    <main className='error-editorial min-h-svh' data-status={props.status}>
      <div className='error-editorial-shell'>
        <a href='/' className='error-editorial-brand'>
          <LmmBrandMark className='size-7' title={systemName} />
          <span>{systemName}</span>
        </a>
        <div className='error-editorial-layout'>
          <section className='error-editorial-copy'>
            {props.showStatus !== false && props.status != null && (
              <p
                className='error-editorial-status'
                aria-label={String(props.status)}
              >
                {props.status}
              </p>
            )}
            <h1 className='error-editorial-title'>{props.title}</h1>
            <p className='error-editorial-description'>{props.description}</p>
            {props.actions && (
              <div className='error-editorial-actions'>{props.actions}</div>
            )}
            {props.note && (
              <div className='error-editorial-note'>{props.note}</div>
            )}
          </section>
          <aside className='error-editorial-art-column' aria-hidden='true'>
            {props.artSrc ? (
              <img
                className='error-editorial-image'
                src={props.artSrc}
                alt=''
              />
            ) : (
              <ErrorArtwork status={props.status} />
            )}
          </aside>
        </div>
      </div>
    </main>
  )
}
