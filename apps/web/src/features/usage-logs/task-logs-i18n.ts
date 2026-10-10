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
import type { i18n } from 'i18next'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

const namespace = 'task-drawing-logs'
const languages = ['en', 'zhCN', 'zhTW', 'fr', 'ja', 'ru', 'vi'] as const
const rows = [
  [
    'Drawing history covers Midjourney / MjProxy image and video tasks. Other image calls appear in Common Logs.',
    '这里记录 Midjourney / MjProxy 的绘图与视频任务。其他图片调用请查看通用日志。',
    '這裡記錄 Midjourney / MjProxy 的繪圖與影片任務。其他圖片呼叫請查看一般日誌。',
    'Cet historique couvre les images et vidéos Midjourney / MjProxy. Les autres appels image figurent dans les journaux généraux.',
    'Midjourney / MjProxy の画像・動画タスクを表示します。その他の画像リクエストは共通ログをご確認ください。',
    'Здесь показаны задания изображений и видео Midjourney / MjProxy. Другие запросы изображений — в общих журналах.',
    'Lịch sử này gồm tác vụ ảnh và video Midjourney / MjProxy. Xem các yêu cầu ảnh khác trong nhật ký chung.',
  ],
  [
    'Task history covers asynchronous audio and video jobs. Other API calls appear in Common Logs.',
    '这里记录异步音频与视频任务。其他 API 调用请查看通用日志。',
    '這裡記錄非同步音訊與影片任務。其他 API 呼叫請查看一般日誌。',
    'Cet historique couvre les tâches audio et vidéo asynchrones. Les autres appels API figurent dans les journaux généraux.',
    '非同期の音声・動画タスクを表示します。その他の API リクエストは共通ログをご確認ください。',
    'Здесь показаны асинхронные задания аудио и видео. Другие вызовы API — в общих журналах.',
    'Lịch sử này gồm tác vụ âm thanh và video bất đồng bộ. Xem các yêu cầu API khác trong nhật ký chung.',
  ],
  [
    'This page only',
    '仅当前页',
    '僅目前頁面',
    'Cette page uniquement',
    'このページのみ',
    'Только эта страница',
    'Chỉ trang này',
  ],
  [
    'Refreshes every 5 seconds while listed tasks are pending. Pauses in the background.',
    '当前页有未完成任务时，每 5 秒刷新。页面在后台时暂停。',
    '目前頁面有未完成任務時，每 5 秒重新整理。頁面在背景時暫停。',
    'Actualise toutes les 5 secondes tant que des tâches sont en attente. Pause en arrière-plan.',
    '未完了のタスクがある間は 5 秒ごとに更新します。バックグラウンドでは停止します。',
    'Обновление каждые 5 секунд, пока есть незавершённые задания. В фоне приостанавливается.',
    'Làm mới mỗi 5 giây khi còn tác vụ chưa xong. Tạm dừng khi trang ở nền.',
  ],
  [
    'Start time must be before end time.',
    '开始时间不能晚于结束时间。',
    '開始時間不能晚於結束時間。',
    'Le début ne peut pas être après la fin.',
    '開始日時は終了日時以前にしてください。',
    'Время начала не может быть позже времени окончания.',
    'Thời gian bắt đầu không được sau thời gian kết thúc.',
  ],
  [
    'Last updated',
    '更新于',
    '更新於',
    'Mis à jour',
    '更新日時',
    'Обновлено',
    'Cập nhật lúc',
  ],
] as const

const registered = new WeakSet<i18n>()
export function registerTaskLogTranslations(instance: i18n): void {
  if (registered.has(instance)) return
  for (const [index, language] of languages.entries()) {
    instance.addResourceBundle(
      language,
      namespace,
      Object.fromEntries(rows.map((values) => [values[0], values[index]])),
      true,
      false
    )
  }
  registered.add(instance)
}

export function useTaskLogsTranslation() {
  const translation = useTranslation()
  const { i18n } = translation
  registerTaskLogTranslations(i18n)
  const language = i18n.resolvedLanguage || i18n.language
  const t = useMemo(
    () => i18n.getFixedT(language, [namespace, 'translation']),
    [i18n, language]
  )
  return { ...translation, t }
}
