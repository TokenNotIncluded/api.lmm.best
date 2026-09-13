/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
const keys = {
  en: {
    'Add starter': 'Add starter',
    'Delete starter': 'Delete starter',
    'Move starter up': 'Move starter up',
    'Move starter down': 'Move starter down',
    'Button label': 'Button label',
    'Prompt text': 'Prompt text',
    'Starter {{number}}': 'Starter {{number}}',
    'Restore defaults': 'Restore defaults',
    'Conversation starter prompts': 'Conversation starter prompts',
    'These starter buttons use exactly the custom text you save. They are not translated automatically.':
      'These starter buttons use exactly the custom text you save. They are not translated automatically.',
    'Optional JSON list of starter buttons. Leave empty for reviewed defaults; use [] to hide them.':
      'Optional JSON list of starter buttons. Leave empty for reviewed defaults; use [] to hide them.',
  },
  zh: {
    'Add starter': '新增提问',
    'Delete starter': '删除提问',
    'Move starter up': '上移提问',
    'Move starter down': '下移提问',
    'Button label': '按钮文案',
    'Prompt text': '实际提问',
    'Starter {{number}}': '第 {{number}} 项',
    'Restore defaults': '恢复默认',
    'Conversation starter prompts': '对话开场提问',
    'These starter buttons use exactly the custom text you save. They are not translated automatically.':
      '这些提问按钮会使用你保存的原文，不会自动翻译。',
    'Optional JSON list of starter buttons. Leave empty for reviewed defaults; use [] to hide them.':
      '可选的提问按钮列表。留空使用审核后的默认值，填写 [] 可隐藏全部。',
  },
  'zh-TW': {
    'Add starter': '新增提問',
    'Delete starter': '刪除提問',
    'Move starter up': '上移提問',
    'Move starter down': '下移提問',
    'Button label': '按鈕文案',
    'Prompt text': '實際提問',
    'Starter {{number}}': '第 {{number}} 項',
    'Restore defaults': '恢復預設',
    'Conversation starter prompts': '對話開場提問',
    'These starter buttons use exactly the custom text you save. They are not translated automatically.':
      '這些提問按鈕會使用你儲存的原文，不會自動翻譯。',
    'Optional JSON list of starter buttons. Leave empty for reviewed defaults; use [] to hide them.':
      '可選的提問按鈕清單。留空使用審核後的預設值，填寫 [] 可隱藏全部。',
  },
  fr: {
    'Add starter': 'Ajouter une question',
    'Delete starter': 'Supprimer la question',
    'Move starter up': 'Monter la question',
    'Move starter down': 'Descendre la question',
    'Button label': 'Libellé du bouton',
    'Prompt text': 'Question envoyée',
    'Starter {{number}}': 'Question {{number}}',
    'Restore defaults': 'Restaurer les valeurs par défaut',
    'Conversation starter prompts': 'Questions de démarrage',
    'These starter buttons use exactly the custom text you save. They are not translated automatically.':
      'Ces boutons utilisent exactement le texte enregistré et ne sont pas traduits automatiquement.',
    'Optional JSON list of starter buttons. Leave empty for reviewed defaults; use [] to hide them.':
      'Liste JSON facultative. Laissez vide pour les valeurs par défaut validées, ou utilisez [] pour tout masquer.',
  },
  ja: {
    'Add starter': '質問を追加',
    'Delete starter': '質問を削除',
    'Move starter up': '質問を上へ',
    'Move starter down': '質問を下へ',
    'Button label': 'ボタンの文言',
    'Prompt text': '実際の質問',
    'Starter {{number}}': '質問 {{number}}',
    'Restore defaults': 'デフォルトに戻す',
    'Conversation starter prompts': '会話の開始質問',
    'These starter buttons use exactly the custom text you save. They are not translated automatically.':
      '保存した文面をそのまま使い、自動翻訳は行いません。',
    'Optional JSON list of starter buttons. Leave empty for reviewed defaults; use [] to hide them.':
      '任意の JSON リストです。空欄で承認済みの既定値、[] ですべて非表示になります。',
  },
  ru: {
    'Add starter': 'Добавить вопрос',
    'Delete starter': 'Удалить вопрос',
    'Move starter up': 'Переместить выше',
    'Move starter down': 'Переместить ниже',
    'Button label': 'Текст кнопки',
    'Prompt text': 'Текст вопроса',
    'Starter {{number}}': 'Вопрос {{number}}',
    'Restore defaults': 'Восстановить исходные значения',
    'Conversation starter prompts': 'Начальные вопросы диалога',
    'These starter buttons use exactly the custom text you save. They are not translated automatically.':
      'Кнопки используют сохранённый текст без автоматического перевода.',
    'Optional JSON list of starter buttons. Leave empty for reviewed defaults; use [] to hide them.':
      'Необязательный JSON-список. Пусто — проверенные значения по умолчанию, [] — скрыть всё.',
  },
  vi: {
    'Add starter': 'Thêm câu hỏi',
    'Delete starter': 'Xóa câu hỏi',
    'Move starter up': 'Đưa lên',
    'Move starter down': 'Đưa xuống',
    'Button label': 'Nhãn nút',
    'Prompt text': 'Câu hỏi thực tế',
    'Starter {{number}}': 'Câu hỏi {{number}}',
    'Restore defaults': 'Khôi phục mặc định',
    'Conversation starter prompts': 'Câu hỏi mở đầu cuộc trò chuyện',
    'These starter buttons use exactly the custom text you save. They are not translated automatically.':
      'Các nút dùng đúng nội dung bạn lưu và không tự động dịch.',
    'Optional JSON list of starter buttons. Leave empty for reviewed defaults; use [] to hide them.':
      'Danh sách JSON tùy chọn. Để trống dùng mặc định đã duyệt, hoặc dùng [] để ẩn tất cả.',
  },
}
export const assistantSettingsCopy = Object.fromEntries(Object.entries(keys))
