/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import {
  normalizeInterfaceLanguage,
  type InterfaceLanguageCode,
} from '@/i18n/languages'

// Keep the two device-specific instructions together; neither is an auth gate.
const hints = {
  en: ['Scroll up to explore', 'Swipe up here to explore'],
  zhCN: ['继续向上滚动，进入导航', '在这里继续上滑，进入导航'],
  zhTW: ['繼續向上捲動，進入導覽', '在這裡繼續上滑，進入導覽'],
  fr: [
    'Défilez vers le haut pour explorer',
    'Balayez ici vers le haut pour explorer',
  ],
  ru: [
    'Прокрутите вверх, чтобы открыть каталог',
    'Проведите здесь вверх, чтобы открыть каталог',
  ],
  ja: ['上にスクロールして一覧へ', 'ここを上にスワイプして一覧へ'],
  vi: ['Cuộn lên để khám phá', 'Vuốt lên tại đây để khám phá'],
} satisfies Record<InterfaceLanguageCode, readonly [string, string]>

export function directoryGestureHints(language: string) {
  const [wheel, touch] = hints[normalizeInterfaceLanguage(language)]
  return { wheel, touch }
}
