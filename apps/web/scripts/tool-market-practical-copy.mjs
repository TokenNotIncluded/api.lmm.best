/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
// Source copy belongs to the practical market workflows. Locale writes use add-missing-keys.mjs.
const rows = `
Image result|图片结果|圖片結果|Résultat image|画像の結果|Результат-изображение|Kết quả hình ảnh
Audio result|音频结果|音訊結果|Résultat audio|音声の結果|Аудиорезультат|Kết quả âm thanh
This request is waiting for confirmation. Open the original tool call to continue.|此请求正在等待确认，请打开原工具调用以继续。|此請求正在等待確認，請開啟原工具呼叫以繼續。|Cette demande attend une confirmation. Ouvrez l’appel d’outil d’origine pour continuer.|このリクエストは確認待ちです。元のツール呼び出しを開いて続行してください。|Запрос ожидает подтверждения. Откройте исходный вызов инструмента, чтобы продолжить.|Yêu cầu đang chờ xác nhận. Mở lần gọi công cụ gốc để tiếp tục.
Add and authorize tool|添加并授权工具|新增並授權工具|Ajouter et autoriser l’outil|ツールを追加して許可|Добавить и разрешить инструмент|Thêm và cấp quyền công cụ
Add this tool to the selected client to make it available.|将此工具添加到所选客户端后即可使用。|將此工具新增至所選用戶端後即可使用。|Ajoutez cet outil au client sélectionné pour le rendre disponible.|このツールを選択中のクライアントに追加すると利用できます。|Добавьте инструмент в выбранный клиент, чтобы он стал доступен.|Thêm công cụ vào ứng dụng đã chọn để sử dụng.
Advanced JSON|高级 JSON|進階 JSON|JSON avancé|高度な JSON|Расширенный JSON|JSON nâng cao
All tools|全部工具|所有工具|Tous les outils|すべてのツール|Все инструменты|Tất cả công cụ
Arguments must be a JSON object.|参数必须是 JSON 对象。|參數必須是 JSON 物件。|Les paramètres doivent être un objet JSON.|引数は JSON オブジェクトである必要があります。|Параметры должны быть объектом JSON.|Tham số phải là đối tượng JSON.
Authorization was saved, but loading failed. Refresh access and load this tool.|授权已保存，但工具加载失败。请刷新授权状态并加载此工具。|授權已儲存，但工具載入失敗。請重新整理授權狀態並載入此工具。|L’autorisation est enregistrée, mais le chargement a échoué. Actualisez l’accès et chargez cet outil.|許可は保存されましたが、追加に失敗しました。アクセス情報を更新し、このツールを追加してください。|Разрешение сохранено, но загрузка не удалась. Обновите доступ и загрузите инструмент.|Đã lưu quyền nhưng nạp công cụ thất bại. Làm mới quyền truy cập và nạp công cụ này.
Authorize this version with a remaining call and spending allowance.|请授权此版本，并确保剩余调用次数和消费额度足够。|請授權此版本，並確保剩餘呼叫次數及消費額度足夠。|Autorisez cette version avec un nombre d’appels et un budget restants.|このバージョンを許可し、呼び出し回数と支出枠を確保してください。|Разрешите эту версию с доступными лимитами вызовов и расходов.|Cấp quyền cho phiên bản này với số lần gọi và hạn mức chi tiêu còn lại.
Awaiting confirmation|等待确认|等待確認|En attente de confirmation|確認待ち|Ожидает подтверждения|Đang chờ xác nhận
Cancel action|取消操作|取消操作|Annuler l’action|操作をキャンセル|Отменить действие|Hủy thao tác
Check parameter {{name}}.|请检查参数 {{name}}。|請檢查參數 {{name}}。|Vérifiez le paramètre {{name}}.|パラメーター {{name}} を確認してください。|Проверьте параметр {{name}}.|Kiểm tra tham số {{name}}.
Check the tool parameters and try again.|请检查工具参数后重试。|請檢查工具參數後重試。|Vérifiez les paramètres de l’outil et réessayez.|ツールの引数を確認して再試行してください。|Проверьте параметры инструмента и повторите попытку.|Kiểm tra tham số công cụ rồi thử lại.
Choose this browser to try a tool, or a connected client to give it access.|选择当前浏览器试用工具，或选择已连接的客户端为其授权。|選擇目前瀏覽器試用工具，或選擇已連接的用戶端為其授權。|Choisissez ce navigateur pour essayer un outil, ou un client connecté pour lui donner accès.|ツールを試すにはこのブラウザーを、アクセスを許可するには接続済みクライアントを選択してください。|Выберите этот браузер для проверки инструмента или подключённый клиент для выдачи доступа.|Chọn trình duyệt này để thử công cụ hoặc ứng dụng đã kết nối để cấp quyền.
Configure market|配置工具市场|設定工具市集|Configurer le marché|マーケットを設定|Настроить рынок|Cấu hình chợ công cụ
Confirm and continue|确认并继续|確認並繼續|Confirmer et continuer|確認して続行|Подтвердить и продолжить|Xác nhận và tiếp tục
Confirm tool action|确认工具操作|確認工具操作|Confirmer l’action de l’outil|ツールの操作を確認|Подтвердить действие инструмента|Xác nhận thao tác công cụ
Connect another client|连接其他客户端|連接其他用戶端|Connecter un autre client|別のクライアントを接続|Подключить другой клиент|Kết nối ứng dụng khác
Edit in advanced JSON|使用高级 JSON 编辑|使用進階 JSON 編輯|Modifier en JSON avancé|高度な JSON で編集|Изменить в расширенном JSON|Chỉnh sửa bằng JSON nâng cao
Enter valid JSON.|请输入有效的 JSON。|請輸入有效的 JSON。|Saisissez un JSON valide.|有効な JSON を入力してください。|Введите корректный JSON.|Nhập JSON hợp lệ.
Free tool calls|免费工具调用|免費工具呼叫|Appels d’outils gratuits|無料のツール呼び出し|Бесплатные вызовы инструментов|Gọi công cụ miễn phí
I reviewed this action and its charges.|我已检查此操作及其费用。|我已檢查此操作及其費用。|J’ai vérifié cette action et ses frais.|この操作と料金を確認しました。|Я проверил действие и его стоимость.|Tôi đã xem lại thao tác và chi phí.
Parameter form|参数表单|參數表單|Formulaire de paramètres|引数フォーム|Форма параметров|Biểu mẫu tham số
Platform builtin|平台内置|平台內建|Intégré à la plateforme|プラットフォーム内蔵|Встроенный в платформу|Tích hợp trên nền tảng
Platform tools do not charge a tool fee. Model generation and transfers still use their normal pricing and require confirmation.|平台工具不收工具调用费。模型生成和转账仍按各自规则计费，并需要确认。|平台工具不收工具呼叫費。模型生成及轉帳仍依各自規則計費，並需要確認。|Les outils de la plateforme n’ont pas de frais d’appel. Les générations et transferts conservent leurs tarifs et nécessitent une confirmation.|プラットフォームのツールには呼び出し料金がかかりません。モデル生成と振替には通常の料金と確認が必要です。|За инструменты платформы плата не взимается. Генерация моделей и переводы оплачиваются по обычным тарифам и требуют подтверждения.|Công cụ nền tảng không thu phí gọi. Tạo nội dung bằng mô hình và chuyển tiền vẫn áp dụng giá riêng và cần xác nhận.
Ready in this client. Refresh its tool list.|此客户端已可使用。请刷新客户端工具列表。|此用戶端已可使用。請重新整理用戶端工具清單。|Disponible dans ce client. Actualisez sa liste d’outils.|このクライアントで利用可能です。ツール一覧を更新してください。|Готово в этом клиенте. Обновите список инструментов.|Đã sẵn sàng trong ứng dụng này. Làm mới danh sách công cụ.
Ready to run in this browser.|已可在当前浏览器中运行。|已可在目前瀏覽器中執行。|Prêt à exécuter dans ce navigateur.|このブラウザーで実行できます。|Готово к запуску в этом браузере.|Đã sẵn sàng chạy trong trình duyệt này.
Remote MCP|远程 MCP|遠端 MCP|MCP distant|リモート MCP|Удалённый MCP|MCP từ xa
Remote tool calls are paused.|远程工具调用已暂停。|遠端工具呼叫已暫停。|Les appels d’outils distants sont suspendus.|リモートツールの呼び出しは停止中です。|Вызовы удалённых инструментов приостановлены.|Đã tạm dừng gọi công cụ từ xa.
Remote tool calls are paused. Free platform tools remain available; model generation and transfers keep their own charges.|远程工具调用已暂停。免费平台工具仍可使用；模型生成和转账仍按各自规则计费。|遠端工具呼叫已暫停。免費平台工具仍可使用；模型生成及轉帳仍依各自規則計費。|Les appels distants sont suspendus. Les outils gratuits de la plateforme restent disponibles ; les générations et transferts conservent leurs frais.|リモートツールの呼び出しは停止中です。無料のプラットフォームツールは利用できます。モデル生成と振替には各料金が適用されます。|Удалённые вызовы приостановлены. Бесплатные инструменты платформы доступны; генерация и переводы оплачиваются по своим тарифам.|Đã tạm dừng gọi công cụ từ xa. Công cụ nền tảng miễn phí vẫn dùng được; tạo nội dung và chuyển tiền vẫn có phí riêng.
Run free tool|运行免费工具|執行免費工具|Exécuter l’outil gratuit|無料ツールを実行|Запустить бесплатный инструмент|Chạy công cụ miễn phí
Runs on this platform|在本平台运行|在本平台執行|Exécuté sur cette plateforme|このプラットフォームで実行|Работает на этой платформе|Chạy trên nền tảng này
Service credential storage is unavailable. Contact an administrator.|服务凭证存储不可用，请联系管理员。|服務憑證儲存無法使用，請聯絡管理員。|Le stockage des identifiants du service est indisponible. Contactez un administrateur.|サービスの認証情報を保存できません。管理者にお問い合わせください。|Хранилище учётных данных сервиса недоступно. Обратитесь к администратору.|Kho thông tin xác thực dịch vụ không khả dụng. Liên hệ quản trị viên.
Start another call|发起新的调用|發起新的呼叫|Lancer un autre appel|別の呼び出しを開始|Начать новый вызов|Bắt đầu lần gọi mới
The call could not be confirmed. Retry the same request to check its status.|暂时无法确认调用结果。请重试同一请求以查询状态。|暫時無法確認呼叫結果。請重試同一請求以查詢狀態。|L’appel n’a pas pu être confirmé. Réessayez la même demande pour vérifier son état.|呼び出しの結果を確認できませんでした。同じリクエストを再試行して状態を確認してください。|Не удалось подтвердить вызов. Повторите тот же запрос, чтобы проверить состояние.|Không thể xác nhận lần gọi. Thử lại cùng yêu cầu để kiểm tra trạng thái.
The provider changed this tool. Refresh its definitions before calling.|提供方已修改此工具。请刷新工具定义后再调用。|提供方已修改此工具。請重新整理工具定義後再呼叫。|Le fournisseur a modifié cet outil. Actualisez ses définitions avant de l’appeler.|提供者がこのツールを変更しました。定義を更新してから呼び出してください。|Провайдер изменил инструмент. Перед вызовом обновите его определения.|Nhà cung cấp đã thay đổi công cụ. Làm mới định nghĩa trước khi gọi.
The provider could not be connected. Check its endpoint and credentials.|无法连接提供方，请检查服务地址和凭证。|無法連接提供方，請檢查服務網址及憑證。|Impossible de joindre le fournisseur. Vérifiez son adresse et ses identifiants.|提供者に接続できませんでした。エンドポイントと認証情報を確認してください。|Не удалось подключиться к провайдеру. Проверьте адрес и учётные данные.|Không thể kết nối nhà cung cấp. Kiểm tra địa chỉ và thông tin xác thực.
The provider rejected its credentials. Update the service authentication.|提供方拒绝了当前凭证，请更新服务认证设置。|提供方拒絕了目前憑證，請更新服務驗證設定。|Le fournisseur a refusé ses identifiants. Mettez à jour l’authentification du service.|提供者が認証情報を拒否しました。サービスの認証設定を更新してください。|Провайдер отклонил учётные данные. Обновите настройки аутентификации сервиса.|Nhà cung cấp từ chối thông tin xác thực. Cập nhật xác thực dịch vụ.
The request or version changed. Refresh the original call before trying again.|请求或版本已变更，请刷新原调用状态后重试。|請求或版本已變更，請重新整理原呼叫狀態後重試。|La demande ou la version a changé. Actualisez l’appel d’origine avant de réessayer.|リクエストまたはバージョンが変更されました。元の呼び出しを更新してから再試行してください。|Запрос или версия изменились. Обновите исходный вызов перед повтором.|Yêu cầu hoặc phiên bản đã thay đổi. Làm mới lần gọi gốc trước khi thử lại.
The result is unknown. Check this call instead of starting it again.|结果尚不确定，请查询本次调用，不要重新发起。|結果尚不確定，請查詢本次呼叫，不要重新發起。|Le résultat est inconnu. Vérifiez cet appel au lieu de le relancer.|結果は未確定です。再実行せず、この呼び出しの状態を確認してください。|Результат неизвестен. Проверьте этот вызов, не запускайте его заново.|Chưa rõ kết quả. Kiểm tra lần gọi này thay vì gọi lại.
The result was received. Settlement is still being confirmed.|已收到结果，计费结算仍在确认中。|已收到結果，計費結算仍在確認中。|Le résultat est reçu. Le règlement est encore en cours de confirmation.|結果を受信しました。精算は確認中です。|Результат получен. Расчёт ещё подтверждается.|Đã nhận kết quả. Việc quyết toán vẫn đang được xác nhận.
The tool is busy. Wait briefly and retry.|工具繁忙，请稍后重试。|工具忙碌，請稍後重試。|L’outil est occupé. Patientez puis réessayez.|ツールが混み合っています。少し待って再試行してください。|Инструмент занят. Немного подождите и повторите попытку.|Công cụ đang bận. Chờ một chút rồi thử lại.
The tool returned an invalid or unsuccessful result.|工具返回了无效结果或执行未成功。|工具傳回無效結果或執行未成功。|L’outil a renvoyé un résultat invalide ou un échec.|ツールの結果が無効か、実行に失敗しました。|Инструмент вернул недействительный результат или ошибку.|Công cụ trả về kết quả không hợp lệ hoặc không thành công.
This authorization or budget has no remaining allowance.|此授权或预算的剩余额度已用完。|此授權或預算的剩餘額度已用完。|Cette autorisation ou ce budget n’a plus de marge disponible.|この許可または予算の利用枠がありません。|Лимит этого разрешения или бюджета исчерпан.|Quyền hoặc ngân sách này đã hết hạn mức còn lại.
This browser|当前浏览器|目前瀏覽器|Ce navigateur|このブラウザー|Этот браузер|Trình duyệt này
This loads the tool for the selected client and authorizes this exact version within the limits below.|这会为所选客户端加载工具，并在下方限额内授权当前确切版本。|這會為所選用戶端載入工具，並在下方限額內授權目前確切版本。|Cette action charge l’outil pour le client sélectionné et autorise cette version exacte dans les limites ci-dessous.|選択中のクライアントにツールを追加し、以下の上限内でこのバージョンを許可します。|Инструмент загружается для выбранного клиента, а эта версия получает разрешение в пределах лимитов ниже.|Thao tác nạp công cụ cho ứng dụng đã chọn và cấp quyền đúng phiên bản này trong giới hạn bên dưới.
This tool or authorization is no longer available. Refresh access.|此工具或授权已不可用，请刷新授权状态。|此工具或授權已無法使用，請重新整理授權狀態。|Cet outil ou cette autorisation n’est plus disponible. Actualisez l’accès.|このツールまたは許可は利用できません。アクセス情報を更新してください。|Инструмент или разрешение больше не доступны. Обновите доступ.|Công cụ hoặc quyền không còn khả dụng. Làm mới quyền truy cập.
Validate and enable only for me|验证并仅为自己启用|驗證並僅為自己啟用|Valider et activer pour moi|検証して自分用に有効化|Проверить и включить только для меня|Xác minh và bật chỉ cho tôi
Your available balance is too low for this call.|可用余额不足以支付此次调用。|可用餘額不足以支付此次呼叫。|Votre solde disponible est insuffisant pour cet appel.|この呼び出しに必要な残高が不足しています。|Доступного баланса недостаточно для этого вызова.|Số dư khả dụng không đủ cho lần gọi này.
{{count}} tools|{{count}} 个工具|{{count}} 個工具|{{count}} outils|{{count}} 個のツール|{{count}} инструментов|{{count}} công cụ
`
  .trim()
  .split('\n')
  .map((row) => row.split('|'))

const locales = ['en', 'zh', 'zh-TW', 'fr', 'ja', 'ru', 'vi']
for (const row of rows) {
  if (row.length !== locales.length) {
    throw new Error(`Invalid practical tool market translation: ${row[0]}`)
  }
}
export const toolMarketPracticalCopy = Object.fromEntries(
  locales.map((locale, index) => [
    locale,
    Object.fromEntries(rows.map((row) => [row[0], row[index]])),
  ])
)
