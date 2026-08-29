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
import { Liquid } from 'liquid-gooey'

import styles from './forge-liquid-accent.module.css'

// Runtime-only liquid layer; the static host imports this module on capable desktops.
export function ForgeLiquidAccentRuntime() {
  return (
    <Liquid
      blur={8}
      className={styles.liquid}
      contrast={22}
      fill='var(--forge-liquid-fill)'
      filterPadding={16}
      style={{ inset: 0, position: 'absolute' }}
      waviness={0}
    >
      <Liquid.Item
        className={`${styles.blob} ${styles.blobPrimary}`}
        radius={999}
      >
        <span className={styles.blobSurface} />
      </Liquid.Item>
      <Liquid.Item
        className={`${styles.blob} ${styles.blobBridge}`}
        radius={999}
      >
        <span className={styles.blobSurface} />
      </Liquid.Item>
      <Liquid.Item
        className={`${styles.blob} ${styles.blobSmall}`}
        radius={999}
      >
        <span className={styles.blobSurface} />
      </Liquid.Item>
    </Liquid>
  )
}
