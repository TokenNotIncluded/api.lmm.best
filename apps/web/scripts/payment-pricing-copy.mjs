/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
const keys = [
  'Platform credits',
  'Estimated USD cost',
  'Unfiltered prices are starting prices. Choose a group to see its rate; Auto follows your configured group order.',
  'Base prices exclude usage discounts. USD estimates also exclude payment discounts and fees; checkout confirms the payable amount.',
  'Select for a quote',
  'Preset amount: {{credit}}. Actual payment: {{payment}}.',
  'Preset amount: {{credit}}. Select to get the current payment quote.',
]
const values = {
  en: keys,
  zh: [
    '平台额度',
    '美元成本估算',
    '未筛选分组时显示起价。选择分组可查看对应费率；Auto 按你配置的分组顺序尝试。',
    '基础价格未计入用量折扣。美元估算也未计入支付优惠和手续费，实付金额以结算报价为准。',
    '选择后获取报价',
    '充值额度：{{credit}}。实际付款：{{payment}}。',
    '充值额度：{{credit}}。选择后获取当前付款报价。',
  ],
  'zh-TW': [
    '平台額度',
    '美元成本估算',
    '未篩選群組時顯示起價。選擇群組可查看對應費率；Auto 依你設定的群組順序嘗試。',
    '基礎價格未計入用量折扣。美元估算也未計入付款優惠和手續費，實付金額以結帳報價為準。',
    '選擇後取得報價',
    '儲值額度：{{credit}}。實際付款：{{payment}}。',
    '儲值額度：{{credit}}。選擇後取得目前付款報價。',
  ],
  fr: [
    'Crédits plateforme',
    'Coût estimé en USD',
    'Sans filtre de groupe, les prix affichés sont des prix de départ. Sélectionnez un groupe pour voir son tarif ; Auto suit l’ordre de vos groupes configurés.',
    'Les prix de base excluent les remises d’utilisation. Les estimations en USD excluent aussi les remises et frais de paiement ; le montant à régler est confirmé au paiement.',
    'Sélectionner pour un devis',
    'Crédits : {{credit}}. Paiement réel : {{payment}}.',
    'Crédits : {{credit}}. Sélectionnez pour obtenir le devis de paiement actuel.',
  ],
  ja: [
    'プラットフォーム残高',
    '米ドル費用の概算',
    'グループ未選択時は最低価格を表示します。グループを選ぶと料金を確認できます。Auto は設定されたグループの順に試行します。',
    '基本価格には利用割引を含みません。米ドル概算にも支払い割引や手数料は含まれず、実際の支払額は決済時の見積もりで確定します。',
    '選択して見積もりを取得',
    'チャージ額：{{credit}}。実際の支払額：{{payment}}。',
    'チャージ額：{{credit}}。選択すると現在の支払い見積もりを取得します。',
  ],
  ru: [
    'Кредиты платформы',
    'Оценка стоимости в USD',
    'Без фильтра группы показаны минимальные цены. Выберите группу, чтобы увидеть её тариф; Auto перебирает группы в заданном вами порядке.',
    'Базовые цены не включают скидки за использование. Оценки в USD также не включают платёжные скидки и комиссии; сумма к оплате подтверждается при оформлении.',
    'Выберите для расчёта',
    'Пополнение: {{credit}}. К оплате: {{payment}}.',
    'Пополнение: {{credit}}. Выберите для получения текущей суммы к оплате.',
  ],
  vi: [
    'Tín dụng nền tảng',
    'Chi phí USD ước tính',
    'Khi chưa lọc nhóm, giá hiển thị là giá khởi điểm. Chọn nhóm để xem mức giá; Auto thử theo thứ tự nhóm bạn đã cấu hình.',
    'Giá cơ bản chưa gồm giảm giá sử dụng. Ước tính USD cũng chưa gồm ưu đãi và phí thanh toán; số tiền phải trả được xác nhận khi thanh toán.',
    'Chọn để lấy báo giá',
    'Số dư nạp: {{credit}}. Thanh toán thực tế: {{payment}}.',
    'Số dư nạp: {{credit}}. Chọn để lấy báo giá thanh toán hiện tại.',
  ],
}
export const paymentPricingCopy = Object.fromEntries(
  Object.entries(values).map(([locale, translations]) => [
    locale,
    Object.fromEntries(keys.map((key, index) => [key, translations[index]])),
  ])
)
