/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
const rows = [
  [
    'Reference preset',
    '参考预设',
    '參考預設',
    'Référence prédéfinie',
    '参考プリセット',
    'Справочный набор',
    'Nguồn tham khảo có sẵn',
  ],
  [
    'Read channel prices or third-party reference data from basellm and models.dev. The models.dev preset selects OpenAI; edit its endpoint to choose another provider.',
    '读取渠道报价，或 basellm、models.dev 的第三方参考数据。models.dev 预设选择 OpenAI；可编辑接口地址来选择其他提供商。',
    '讀取渠道報價，或 basellm、models.dev 的第三方參考資料。models.dev 預設選擇 OpenAI；可編輯介面位址來選擇其他供應商。',
    'Lisez les tarifs des canaux ou les références tierces de basellm et models.dev. Le préréglage models.dev sélectionne OpenAI ; modifiez son adresse pour choisir un autre fournisseur.',
    'チャンネルの料金、または basellm と models.dev の第三者参考データを読み込みます。models.dev プリセットは OpenAI を選択します。別の提供元を選ぶにはエンドポイントを編集してください。',
    'Загрузите цены каналов или справочные данные basellm и models.dev. Набор models.dev выбирает OpenAI; измените адрес, чтобы выбрать другого поставщика.',
    'Đọc giá kênh hoặc dữ liệu tham khảo bên thứ ba từ basellm và models.dev. Nguồn models.dev chọn OpenAI; sửa địa chỉ để chọn nhà cung cấp khác.',
  ],
  [
    'Upstream sync requires a server with USD pricing snapshots. Upgrade the server and fetch again.',
    '上游同步需要服务器支持美元价格快照。请升级服务器后重新读取。',
    '上游同步需要伺服器支援美元價格快照。請升級伺服器後重新讀取。',
    'La synchronisation exige un serveur prenant en charge les instantanés de prix en USD. Mettez le serveur à jour, puis relancez la lecture.',
    '上流の同期には USD 料金スナップショット対応サーバーが必要です。サーバーを更新してから再取得してください。',
    'Для синхронизации сервер должен поддерживать снимки цен в USD. Обновите сервер и загрузите цены снова.',
    'Đồng bộ nguồn yêu cầu máy chủ hỗ trợ ảnh chụp giá USD. Hãy nâng cấp máy chủ rồi đọc lại.',
  ],
  [
    'Fetch upstream prices again before applying sync. Your selections have been kept.',
    '请重新读取上游报价后再应用同步。已保留你的选择。',
    '請重新讀取上游報價後再套用同步。已保留你的選擇。',
    'Relisez les prix des fournisseurs avant de synchroniser. Vos sélections ont été conservées.',
    '同期を適用する前に上流の料金を再取得してください。選択内容は保持されています。',
    'Перед синхронизацией загрузите цены снова. Ваш выбор сохранён.',
    'Hãy đọc lại giá nguồn trước khi đồng bộ. Các lựa chọn của bạn đã được giữ lại.',
  ],
  [
    'The following models will change billing mode. Confirm to apply the selected prices and replace the previous billing mode.',
    '以下模型将更改计费方式。确认后会应用所选价格，并替换原计费方式。',
    '以下模型將變更計費方式。確認後會套用所選價格，並取代原計費方式。',
    'Le mode de facturation des modèles suivants changera. Confirmez pour appliquer les prix choisis et remplacer le mode précédent.',
    '次のモデルの課金方式が変わります。確認すると、選択した料金が適用され、以前の課金方式が置き換わります。',
    'У следующих моделей изменится режим расчёта. Подтвердите применение выбранных цен и замену прежнего режима.',
    'Các mô hình sau sẽ đổi cách tính phí. Xác nhận để áp dụng giá đã chọn và thay cách tính phí cũ.',
  ],
  [
    'Invalid pricing snapshot',
    '价格快照无效',
    '價格快照無效',
    'Instantané de prix invalide',
    '料金スナップショットが無効です',
    'Некорректный снимок цен',
    'Ảnh chụp giá không hợp lệ',
  ],
  [
    'Invalid upstream price selection',
    '所选上游价格无效',
    '所選上游價格無效',
    'Sélection de prix fournisseur invalide',
    '選択した上流の料金が無効です',
    'Некорректный выбор цены поставщика',
    'Giá nguồn đã chọn không hợp lệ',
  ],
  [
    'Select a complete upstream billing configuration',
    '请选择完整的上游计费配置',
    '請選擇完整的上游計費設定',
    'Sélectionnez une configuration de facturation fournisseur complète',
    '完全な上流の課金設定を選択してください',
    'Выберите полную конфигурацию расчёта поставщика',
    'Hãy chọn cấu hình tính phí nguồn đầy đủ',
  ],
  [
    'Skipped pricing entries ({{count}})',
    '已跳过的价格条目（{{count}}）',
    '已略過的價格項目（{{count}}）',
    'Tarifs ignorés ({{count}})',
    'スキップした料金項目（{{count}}）',
    'Пропущенные цены ({{count}})',
    'Mục giá đã bỏ qua ({{count}})',
  ],
  [
    'Some models were skipped because their complete pricing could not be imported. Existing prices are unchanged.',
    '部分模型的完整计费无法导入，因此已跳过，现有价格保持不变。',
    '部分模型的完整計費無法匯入，因此已略過，現有價格維持不變。',
    'Certains modèles ont été ignorés, car leur tarification complète ne peut pas être importée. Les prix existants sont conservés.',
    '完全な料金設定をインポートできないモデルはスキップしました。既存の料金は変更されていません。',
    'Некоторые модели пропущены: их полные тарифы нельзя импортировать. Существующие цены не изменены.',
    'Một số mô hình bị bỏ qua vì không thể nhập đầy đủ cách tính phí. Giá hiện có không đổi.',
  ],
]
const locales = ['en', 'zh', 'zh-TW', 'fr', 'ja', 'ru', 'vi']
export const upstreamPricingCopy = Object.fromEntries(
  locales.map((locale, index) => [
    locale,
    Object.fromEntries(
      rows.map((row) => [row[0], row[index === 0 ? 0 : index]])
    ),
  ])
)
