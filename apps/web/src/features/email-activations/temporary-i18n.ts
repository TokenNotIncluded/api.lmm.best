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
/*
Copyright (C) 2026 LIghtJUNction
*/
import i18next, { type i18n as I18nInstance } from 'i18next'

export const temporaryActivationTranslations = {
  en: {
    'Temporary activations': 'Temporary activations',
    'Temporary activation type': 'Temporary activation type',
    'Phone number': 'Phone number',
    'Email address': 'Email address',
    'Enable temporary activations': 'Enable temporary activations',
    'Allow authenticated users to purchase temporary phone-number and email activations.':
      'Allow authenticated users to purchase temporary phone-number and email activations.',
    'Enable phone-number activations': 'Enable phone-number activations',
    'Allow users to purchase temporary phone numbers and receive SMS verification codes.':
      'Allow users to purchase temporary phone numbers and receive SMS verification codes.',
    'Enable email activations': 'Enable email activations',
    'Allow users to purchase temporary email addresses and receive verification messages.':
      'Allow users to purchase temporary email addresses and receive verification messages.',
    'Charging rule': 'Charging rule',
    'HeroSMS ¥1 → platform ${{price}} balance':
      'HeroSMS ¥1 → platform ${{price}} balance',
    'The multiplier is x: each HeroSMS ¥1 of upstream cost charges $x from the user balance. Platform balance and RMB recharge are treated as approximately 1:1 for this simplified calculation.':
      'The multiplier is x: each HeroSMS ¥1 of upstream cost charges $x from the user balance. Platform balance and RMB recharge are treated as approximately 1:1 for this simplified calculation.',
    'HeroSMS temporary activations': 'HeroSMS temporary activations',
    'Phone number purchased': 'Phone number purchased',
    'Phone activation cancelled and refunded':
      'Phone activation cancelled and refunded',
    'Unable to load phone activation catalog':
      'Unable to load phone activation catalog',
    'Purchase temporary phone number': 'Purchase temporary phone number',
    'Select a country': 'Select a country',
    'Select a service': 'Select a service',
    Operator: 'Operator',
    'Optional; leave blank for any operator':
      'Optional; leave blank for any operator',
    'HeroSMS upstream price': 'HeroSMS upstream price',
    'Platform balance charge': 'Platform balance charge',
    'Buy phone activation': 'Buy phone activation',
    'Current phone activation': 'Current phone activation',
    'Unable to load current phone activation':
      'Unable to load current phone activation',
    'Cancel and refund': 'Cancel and refund',
    'Verification code': 'Verification code',
    'SMS message': 'SMS message',
    'No active phone activation': 'No active phone activation',
    'Phone activation history': 'Phone activation history',
    'Unable to load phone activation history':
      'Unable to load phone activation history',
    'No phone activation history': 'No phone activation history',
    'Confirm phone activation purchase': 'Confirm phone activation purchase',
    'Purchase {{service}} in {{country}} for {{price}} of platform balance?':
      'Purchase {{service}} in {{country}} for {{price}} of platform balance?',
    pending_provider: 'Preparing provider purchase',
    purchase_unknown: 'Reconciling provider purchase',
    active: 'Waiting for SMS',
    completed: 'Completed',
    cancelled: 'Cancelled',
    failed: 'Failed',
  },
  zhCN: {
    'Temporary activations': '临时接码',
    'Temporary activation type': '接码类型',
    'Phone number': '手机号',
    'Email address': '邮箱地址',
    'Enable temporary activations': '启用临时接码',
    'Allow authenticated users to purchase temporary phone-number and email activations.':
      '允许已登录用户购买临时手机号和临时邮箱接码。',
    'Enable phone-number activations': '启用手机号接码',
    'Allow users to purchase temporary phone numbers and receive SMS verification codes.':
      '允许用户购买临时手机号并接收短信验证码。',
    'Enable email activations': '启用邮箱接码',
    'Allow users to purchase temporary email addresses and receive verification messages.':
      '允许用户购买临时邮箱并接收验证邮件。',
    'Charging rule': '收费规则',
    'HeroSMS ¥1 → platform ${{price}} balance':
      'HeroSMS ¥1 → 平台 ${{price}} 余额',
    'The multiplier is x: each HeroSMS ¥1 of upstream cost charges $x from the user balance. Platform balance and RMB recharge are treated as approximately 1:1 for this simplified calculation.':
      '倍率为 x：HeroSMS 上游每 ¥1，用户支付 $x 平台余额。为简化计算，平台美元余额与人民币充值按约 1:1 处理。',
    'HeroSMS temporary activations': 'HeroSMS 临时接码',
    'Phone number purchased': '手机号购买成功',
    'Phone activation cancelled and refunded': '手机号接码已取消并退款',
    'Unable to load phone activation catalog': '无法加载手机号接码目录',
    'Purchase temporary phone number': '购买临时手机号',
    'Select a country': '选择国家或地区',
    'Select a service': '选择服务',
    Operator: '运营商',
    'Optional; leave blank for any operator': '可选；留空表示任意运营商',
    'HeroSMS upstream price': 'HeroSMS 上游价格',
    'Platform balance charge': '平台余额扣费',
    'Buy phone activation': '购买手机号接码',
    'Current phone activation': '当前手机号接码',
    'Unable to load current phone activation': '无法加载当前手机号接码',
    'Cancel and refund': '取消并退款',
    'Verification code': '验证码',
    'SMS message': '短信内容',
    'No active phone activation': '暂无进行中的手机号接码',
    'Phone activation history': '手机号接码记录',
    'Unable to load phone activation history': '无法加载手机号接码记录',
    'No phone activation history': '暂无手机号接码记录',
    'Confirm phone activation purchase': '确认购买手机号接码',
    'Purchase {{service}} in {{country}} for {{price}} of platform balance?':
      '使用 {{price}} 平台余额购买 {{country}} 的 {{service}} 接码吗？',
    pending_provider: '正在准备购买',
    purchase_unknown: '正在核对购买结果',
    active: '等待短信',
    completed: '已完成',
    cancelled: '已取消',
    failed: '失败',
  },
  zhTW: {
    'Temporary activations': '臨時接碼',
    'Temporary activation type': '接碼類型',
    'Phone number': '手機號碼',
    'Email address': '電子郵件地址',
    'Enable temporary activations': '啟用臨時接碼',
    'Allow authenticated users to purchase temporary phone-number and email activations.':
      '允許已登入使用者購買臨時手機號碼與臨時信箱接碼。',
    'Enable phone-number activations': '啟用手機號碼接碼',
    'Allow users to purchase temporary phone numbers and receive SMS verification codes.':
      '允許使用者購買臨時手機號碼並接收簡訊驗證碼。',
    'Enable email activations': '啟用信箱接碼',
    'Allow users to purchase temporary email addresses and receive verification messages.':
      '允許使用者購買臨時信箱並接收驗證郵件。',
    'Charging rule': '收費規則',
    'HeroSMS ¥1 → platform ${{price}} balance':
      'HeroSMS ¥1 → 平台 ${{price}} 餘額',
    'The multiplier is x: each HeroSMS ¥1 of upstream cost charges $x from the user balance. Platform balance and RMB recharge are treated as approximately 1:1 for this simplified calculation.':
      '倍率為 x：HeroSMS 上游每 ¥1，使用者支付 $x 平台餘額。為簡化計算，平台美元餘額與人民幣儲值按約 1:1 處理。',
    'HeroSMS temporary activations': 'HeroSMS 臨時接碼',
    'Phone number purchased': '手機號碼購買成功',
    'Phone activation cancelled and refunded': '手機號碼接碼已取消並退款',
    'Unable to load phone activation catalog': '無法載入手機號碼接碼目錄',
    'Purchase temporary phone number': '購買臨時手機號碼',
    'Select a country': '選擇國家或地區',
    'Select a service': '選擇服務',
    Operator: '電信商',
    'Optional; leave blank for any operator': '選填；留空表示任意電信商',
    'HeroSMS upstream price': 'HeroSMS 上游價格',
    'Platform balance charge': '平台餘額扣款',
    'Buy phone activation': '購買手機號碼接碼',
    'Current phone activation': '目前手機號碼接碼',
    'Unable to load current phone activation': '無法載入目前手機號碼接碼',
    'Cancel and refund': '取消並退款',
    'Verification code': '驗證碼',
    'SMS message': '簡訊內容',
    'No active phone activation': '目前沒有進行中的手機號碼接碼',
    'Phone activation history': '手機號碼接碼記錄',
    'Unable to load phone activation history': '無法載入手機號碼接碼記錄',
    'No phone activation history': '尚無手機號碼接碼記錄',
    'Confirm phone activation purchase': '確認購買手機號碼接碼',
    'Purchase {{service}} in {{country}} for {{price}} of platform balance?':
      '使用 {{price}} 平台餘額購買 {{country}} 的 {{service}} 接碼嗎？',
    pending_provider: '正在準備購買',
    purchase_unknown: '正在核對購買結果',
    active: '等待簡訊',
    completed: '已完成',
    cancelled: '已取消',
    failed: '失敗',
  },
  fr: {
    'Temporary activations': 'Activations temporaires',
    'Temporary activation type': 'Type d’activation temporaire',
    'Phone number': 'Numéro de téléphone',
    'Email address': 'Adresse e-mail',
    'Enable temporary activations': 'Activer les activations temporaires',
    'Allow authenticated users to purchase temporary phone-number and email activations.':
      'Autoriser les utilisateurs connectés à acheter des numéros de téléphone et des adresses e-mail temporaires.',
    'Enable phone-number activations': 'Activer les numéros temporaires',
    'Allow users to purchase temporary phone numbers and receive SMS verification codes.':
      'Autoriser l’achat de numéros temporaires et la réception de codes SMS.',
    'Enable email activations': 'Activer les e-mails temporaires',
    'Allow users to purchase temporary email addresses and receive verification messages.':
      'Autoriser l’achat d’adresses e-mail temporaires et la réception de messages de vérification.',
    'Charging rule': 'Règle de facturation',
    'HeroSMS ¥1 → platform ${{price}} balance':
      'HeroSMS ¥1 → solde plateforme ${{price}}',
    'The multiplier is x: each HeroSMS ¥1 of upstream cost charges $x from the user balance. Platform balance and RMB recharge are treated as approximately 1:1 for this simplified calculation.':
      'Le multiplicateur est x : chaque ¥1 facturé par HeroSMS débite $x du solde utilisateur. Pour simplifier, le solde en dollars et la recharge en RMB sont traités approximativement à 1:1.',
    'HeroSMS temporary activations': 'Activations temporaires HeroSMS',
    'Phone number purchased': 'Numéro de téléphone acheté',
    'Phone activation cancelled and refunded':
      'Activation téléphonique annulée et remboursée',
    'Unable to load phone activation catalog':
      'Impossible de charger le catalogue téléphonique',
    'Purchase temporary phone number': 'Acheter un numéro temporaire',
    'Select a country': 'Sélectionner un pays',
    'Select a service': 'Sélectionner un service',
    Operator: 'Opérateur',
    'Optional; leave blank for any operator':
      'Facultatif ; laissez vide pour tout opérateur',
    'HeroSMS upstream price': 'Prix fournisseur HeroSMS',
    'Platform balance charge': 'Débit du solde plateforme',
    'Buy phone activation': 'Acheter l’activation téléphonique',
    'Current phone activation': 'Activation téléphonique actuelle',
    'Unable to load current phone activation':
      'Impossible de charger l’activation actuelle',
    'Cancel and refund': 'Annuler et rembourser',
    'Verification code': 'Code de vérification',
    'SMS message': 'Message SMS',
    'No active phone activation': 'Aucune activation téléphonique active',
    'Phone activation history': 'Historique des activations téléphoniques',
    'Unable to load phone activation history':
      'Impossible de charger l’historique téléphonique',
    'No phone activation history': 'Aucun historique téléphonique',
    'Confirm phone activation purchase': 'Confirmer l’achat du numéro',
    'Purchase {{service}} in {{country}} for {{price}} of platform balance?':
      'Acheter {{service}} en {{country}} pour {{price}} de solde plateforme ?',
    pending_provider: 'Préparation de l’achat',
    purchase_unknown: 'Vérification de l’achat',
    active: 'En attente du SMS',
    completed: 'Terminé',
    cancelled: 'Annulé',
    failed: 'Échec',
  },
  ja: {
    'Temporary activations': '一時認証',
    'Temporary activation type': '一時認証の種類',
    'Phone number': '電話番号',
    'Email address': 'メールアドレス',
    'Enable temporary activations': '一時認証を有効化',
    'Allow authenticated users to purchase temporary phone-number and email activations.':
      'ログイン済みユーザーが一時電話番号と一時メールを購入できるようにします。',
    'Enable phone-number activations': '電話番号認証を有効化',
    'Allow users to purchase temporary phone numbers and receive SMS verification codes.':
      '一時電話番号を購入して SMS 認証コードを受信できるようにします。',
    'Enable email activations': 'メール認証を有効化',
    'Allow users to purchase temporary email addresses and receive verification messages.':
      '一時メールアドレスを購入して認証メッセージを受信できるようにします。',
    'Charging rule': '課金ルール',
    'HeroSMS ¥1 → platform ${{price}} balance':
      'HeroSMS ¥1 → プラットフォーム残高 ${{price}}',
    'The multiplier is x: each HeroSMS ¥1 of upstream cost charges $x from the user balance. Platform balance and RMB recharge are treated as approximately 1:1 for this simplified calculation.':
      '倍率を x とすると、HeroSMS の上流コスト ¥1 ごとにユーザー残高から $x を請求します。簡略化のため、ドル残高と人民元チャージはおよそ 1:1 として扱います。',
    'HeroSMS temporary activations': 'HeroSMS 一時認証',
    'Phone number purchased': '電話番号を購入しました',
    'Phone activation cancelled and refunded':
      '電話認証をキャンセルして返金しました',
    'Unable to load phone activation catalog':
      '電話認証カタログを読み込めません',
    'Purchase temporary phone number': '一時電話番号を購入',
    'Select a country': '国を選択',
    'Select a service': 'サービスを選択',
    Operator: '通信事業者',
    'Optional; leave blank for any operator': '任意。空欄ならすべての事業者',
    'HeroSMS upstream price': 'HeroSMS 上流価格',
    'Platform balance charge': 'プラットフォーム残高の請求',
    'Buy phone activation': '電話認証を購入',
    'Current phone activation': '現在の電話認証',
    'Unable to load current phone activation': '現在の電話認証を読み込めません',
    'Cancel and refund': 'キャンセルして返金',
    'Verification code': '認証コード',
    'SMS message': 'SMS メッセージ',
    'No active phone activation': '有効な電話認証はありません',
    'Phone activation history': '電話認証履歴',
    'Unable to load phone activation history': '電話認証履歴を読み込めません',
    'No phone activation history': '電話認証履歴はありません',
    'Confirm phone activation purchase': '電話認証の購入を確認',
    'Purchase {{service}} in {{country}} for {{price}} of platform balance?':
      '{{country}} の {{service}} をプラットフォーム残高 {{price}} で購入しますか？',
    pending_provider: '購入を準備中',
    purchase_unknown: '購入結果を確認中',
    active: 'SMS を待機中',
    completed: '完了',
    cancelled: 'キャンセル済み',
    failed: '失敗',
  },
  ru: {
    'Temporary activations': 'Временные активации',
    'Temporary activation type': 'Тип временной активации',
    'Phone number': 'Номер телефона',
    'Email address': 'Адрес электронной почты',
    'Enable temporary activations': 'Включить временные активации',
    'Allow authenticated users to purchase temporary phone-number and email activations.':
      'Разрешить авторизованным пользователям покупать временные номера и адреса электронной почты.',
    'Enable phone-number activations': 'Включить активации по номеру',
    'Allow users to purchase temporary phone numbers and receive SMS verification codes.':
      'Разрешить покупку временных номеров и получение кодов по SMS.',
    'Enable email activations': 'Включить почтовые активации',
    'Allow users to purchase temporary email addresses and receive verification messages.':
      'Разрешить покупку временных адресов и получение проверочных сообщений.',
    'Charging rule': 'Правило оплаты',
    'HeroSMS ¥1 → platform ${{price}} balance':
      'HeroSMS ¥1 → ${{price}} баланса платформы',
    'The multiplier is x: each HeroSMS ¥1 of upstream cost charges $x from the user balance. Platform balance and RMB recharge are treated as approximately 1:1 for this simplified calculation.':
      'Множитель x означает, что за каждый ¥1 цены HeroSMS списывается $x баланса пользователя. Для упрощения долларовый баланс и пополнение в RMB считаются примерно 1:1.',
    'HeroSMS temporary activations': 'Временные активации HeroSMS',
    'Phone number purchased': 'Номер телефона приобретён',
    'Phone activation cancelled and refunded':
      'Активация отменена, средства возвращены',
    'Unable to load phone activation catalog':
      'Не удалось загрузить каталог номеров',
    'Purchase temporary phone number': 'Купить временный номер',
    'Select a country': 'Выберите страну',
    'Select a service': 'Выберите сервис',
    Operator: 'Оператор',
    'Optional; leave blank for any operator':
      'Необязательно; оставьте пустым для любого оператора',
    'HeroSMS upstream price': 'Цена HeroSMS',
    'Platform balance charge': 'Списание баланса платформы',
    'Buy phone activation': 'Купить активацию номера',
    'Current phone activation': 'Текущая активация номера',
    'Unable to load current phone activation':
      'Не удалось загрузить текущую активацию',
    'Cancel and refund': 'Отменить и вернуть средства',
    'Verification code': 'Код подтверждения',
    'SMS message': 'SMS-сообщение',
    'No active phone activation': 'Нет активной активации номера',
    'Phone activation history': 'История активаций номера',
    'Unable to load phone activation history':
      'Не удалось загрузить историю активаций',
    'No phone activation history': 'История активаций пуста',
    'Confirm phone activation purchase': 'Подтвердить покупку номера',
    'Purchase {{service}} in {{country}} for {{price}} of platform balance?':
      'Купить {{service}} в {{country}} за {{price}} баланса платформы?',
    pending_provider: 'Подготовка покупки',
    purchase_unknown: 'Проверка результата покупки',
    active: 'Ожидание SMS',
    completed: 'Завершено',
    cancelled: 'Отменено',
    failed: 'Ошибка',
  },
  vi: {
    'Temporary activations': 'Kích hoạt tạm thời',
    'Temporary activation type': 'Loại kích hoạt tạm thời',
    'Phone number': 'Số điện thoại',
    'Email address': 'Địa chỉ email',
    'Enable temporary activations': 'Bật kích hoạt tạm thời',
    'Allow authenticated users to purchase temporary phone-number and email activations.':
      'Cho phép người dùng đã đăng nhập mua số điện thoại và email tạm thời.',
    'Enable phone-number activations': 'Bật kích hoạt số điện thoại',
    'Allow users to purchase temporary phone numbers and receive SMS verification codes.':
      'Cho phép mua số điện thoại tạm thời và nhận mã xác minh SMS.',
    'Enable email activations': 'Bật kích hoạt email',
    'Allow users to purchase temporary email addresses and receive verification messages.':
      'Cho phép mua địa chỉ email tạm thời và nhận thư xác minh.',
    'Charging rule': 'Quy tắc tính phí',
    'HeroSMS ¥1 → platform ${{price}} balance':
      'HeroSMS ¥1 → ${{price}} số dư nền tảng',
    'The multiplier is x: each HeroSMS ¥1 of upstream cost charges $x from the user balance. Platform balance and RMB recharge are treated as approximately 1:1 for this simplified calculation.':
      'Hệ số x nghĩa là mỗi ¥1 chi phí HeroSMS sẽ trừ $x khỏi số dư người dùng. Để đơn giản, số dư USD và nạp RMB được tính xấp xỉ 1:1.',
    'HeroSMS temporary activations': 'Kích hoạt tạm thời HeroSMS',
    'Phone number purchased': 'Đã mua số điện thoại',
    'Phone activation cancelled and refunded':
      'Đã hủy và hoàn tiền kích hoạt điện thoại',
    'Unable to load phone activation catalog':
      'Không thể tải danh mục số điện thoại',
    'Purchase temporary phone number': 'Mua số điện thoại tạm thời',
    'Select a country': 'Chọn quốc gia',
    'Select a service': 'Chọn dịch vụ',
    Operator: 'Nhà mạng',
    'Optional; leave blank for any operator':
      'Không bắt buộc; để trống cho mọi nhà mạng',
    'HeroSMS upstream price': 'Giá HeroSMS',
    'Platform balance charge': 'Phí trừ số dư nền tảng',
    'Buy phone activation': 'Mua kích hoạt điện thoại',
    'Current phone activation': 'Kích hoạt điện thoại hiện tại',
    'Unable to load current phone activation':
      'Không thể tải kích hoạt hiện tại',
    'Cancel and refund': 'Hủy và hoàn tiền',
    'Verification code': 'Mã xác minh',
    'SMS message': 'Tin nhắn SMS',
    'No active phone activation':
      'Không có kích hoạt điện thoại đang hoạt động',
    'Phone activation history': 'Lịch sử kích hoạt điện thoại',
    'Unable to load phone activation history':
      'Không thể tải lịch sử kích hoạt',
    'No phone activation history': 'Chưa có lịch sử kích hoạt điện thoại',
    'Confirm phone activation purchase': 'Xác nhận mua kích hoạt điện thoại',
    'Purchase {{service}} in {{country}} for {{price}} of platform balance?':
      'Mua {{service}} tại {{country}} với {{price}} số dư nền tảng?',
    pending_provider: 'Đang chuẩn bị mua',
    purchase_unknown: 'Đang đối soát giao dịch',
    active: 'Đang chờ SMS',
    completed: 'Đã hoàn tất',
    cancelled: 'Đã hủy',
    failed: 'Thất bại',
  },
} as const

let registered = false

export function registerTemporaryActivationTranslations(
  instance: I18nInstance = i18next
) {
  if (registered) return
  for (const [language, resource] of Object.entries(
    temporaryActivationTranslations
  )) {
    instance.addResourceBundle(language, 'translation', resource, true, true)
  }
  registered = true
}
