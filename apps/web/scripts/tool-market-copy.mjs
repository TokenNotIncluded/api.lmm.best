/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
// Source copy belongs to the tool-market UI. All writes use add-missing-keys.mjs.
const rows = `
Could not load tool access. Retry before changing permissions.|无法读取工具授权状态，请重试后再修改权限。|無法讀取工具授權狀態，請重試後再修改權限。|Impossible de lire les autorisations. Réessayez avant de les modifier.|ツールの権限を読み込めません。再試行してから権限を変更してください。|Не удалось загрузить права инструмента. Повторите попытку перед их изменением.|Không thể tải quyền công cụ. Hãy thử lại trước khi thay đổi quyền.
Remaining spending limit|剩余可消费额度|剩餘可消費額度|Budget restant|残りの支出枠|Остаток лимита расходов|Hạn mức chi tiêu còn lại
Remaining successful calls|剩余成功调用次数|剩餘成功呼叫次數|Appels réussis restants|成功する呼び出しの残り回数|Осталось успешных вызовов|Số lần gọi thành công còn lại
Account|账户|帳戶|Compte|アカウント|Аккаунт|Tài khoản
Add this as a Bearer token in your client. Do not paste it into an Agent conversation.|在客户端中将其设为 Bearer 令牌，不要粘贴到 Agent 对话中。|在用戶端中將其設為 Bearer 權杖，請勿貼到 Agent 對話中。|Ajoutez-le comme jeton Bearer dans votre client. Ne le collez pas dans une conversation avec un agent.|クライアントに Bearer トークンとして設定してください。Agent との会話には貼り付けないでください。|Укажите его как токен Bearer в клиенте. Не вставляйте его в диалог с агентом.|Thêm làm token Bearer trong ứng dụng. Không dán vào cuộc trò chuyện với Agent.
Approve and publish|批准并发布|核准並發布|Approuver et publier|承認して公開|Одобрить и опубликовать|Duyệt và công bố
Arguments (JSON)|参数（JSON）|參數（JSON）|Paramètres (JSON)|引数（JSON）|Параметры (JSON)|Tham số (JSON)
Authorization failed. Check the limits and refresh the tool version.|授权失败，请检查限额并刷新工具版本。|授權失敗，請檢查限額並重新整理工具版本。|Échec de l’autorisation. Vérifiez les limites et actualisez la version de l’outil.|許可に失敗しました。上限を確認し、ツールのバージョンを更新してください。|Не удалось выдать разрешение. Проверьте лимиты и обновите версию инструмента.|Cấp quyền thất bại. Kiểm tra hạn mức và tải lại phiên bản công cụ.
Authorize tool calls|授权工具调用|授權工具呼叫|Autoriser les appels|ツールの呼び出しを許可|Разрешить вызовы инструмента|Cho phép gọi công cụ
Billing status|计费状态|計費狀態|État de facturation|課金状況|Статус оплаты|Trạng thái tính phí
Budget scope|预算范围|預算範圍|Portée du budget|予算の適用範囲|Область бюджета|Phạm vi ngân sách
Budgets include reserved and spent credits. These are cumulative limits; changing them does not reset usage.|预算包含冻结和已消费的额度。这是累计上限，修改上限不会清空用量。|預算包含凍結與已消費的額度。這是累計上限，修改上限不會清空用量。|Les budgets incluent les crédits réservés et dépensés. Modifier ces plafonds cumulés ne remet pas l’usage à zéro.|予算には確保済みと消費済みのクレジットを含みます。累計上限を変更しても使用量はリセットされません。|Бюджет учитывает зарезервированные и потраченные кредиты. Изменение общего лимита не сбрасывает расход.|Ngân sách gồm tín dụng đã giữ và đã dùng. Đổi hạn mức tích lũy không đặt lại mức sử dụng.
Call records|调用记录|呼叫紀錄|Historique des appels|呼び出し履歴|История вызовов|Lịch sử gọi
Checking…|检查中…|檢查中…|Vérification…|確認中…|Проверка…|Đang kiểm tra…
Client|客户端|用戶端|Client|クライアント|Клиент|Ứng dụng khách
Confirm authorization|确认授权|確認授權|Confirmer l’autorisation|許可を確定|Подтвердить разрешение|Xác nhận cấp quyền
Confirm market settings|确认市场设置|確認市集設定|Confirmer les réglages|マーケット設定を確定|Подтвердить настройки рынка|Xác nhận cài đặt chợ
Connect MCP|连接 MCP|連接 MCP|Connecter MCP|MCP に接続|Подключить MCP|Kết nối MCP
Connect a public HTTPS MCP service. Services requiring credentials are not supported yet.|接入公开的 HTTPS MCP 服务。目前不支持需要凭证的服务。|接入公開的 HTTPS MCP 服務。目前不支援需要憑證的服務。|Connectez un service MCP public en HTTPS. Les services nécessitant des identifiants ne sont pas encore pris en charge.|公開 HTTPS MCP サービスを接続します。認証情報が必要なサービスは現在未対応です。|Подключите публичный MCP-сервис по HTTPS. Сервисы с учётными данными пока не поддерживаются.|Kết nối dịch vụ MCP HTTPS công khai. Chưa hỗ trợ dịch vụ cần thông tin xác thực.
Connect your MCP client|连接你的 MCP 客户端|連接你的 MCP 用戶端|Connecter votre client MCP|MCP クライアントを接続|Подключите свой MCP-клиент|Kết nối ứng dụng MCP
Connection and definitions checked. This is not a guarantee of tool safety.|已检查连接和工具定义，不代表保证工具安全。|已檢查連線與工具定義，不代表保證工具安全。|Connexion et définitions vérifiées. Cela ne garantit pas la sécurité de l’outil.|接続と定義は確認済みです。ツールの安全性を保証するものではありません。|Соединение и определения проверены. Это не гарантирует безопасность инструмента.|Đã kiểm tra kết nối và định nghĩa. Điều này không bảo đảm công cụ an toàn.
Connection token — shown once|连接令牌，仅显示一次|連線權杖，僅顯示一次|Jeton de connexion — affiché une seule fois|接続トークン（一度だけ表示）|Токен подключения — показывается один раз|Token kết nối — chỉ hiển thị một lần
Connections and limits|连接与限额|連線與限額|Connexions et limites|接続と上限|Подключения и лимиты|Kết nối và hạn mức
Could not load market settings|无法加载市场设置|無法載入市集設定|Impossible de charger les réglages|マーケット設定を読み込めません|Не удалось загрузить настройки рынка|Không thể tải cài đặt chợ
Could not load records.|无法加载记录。|無法載入紀錄。|Impossible de charger l’historique.|履歴を読み込めません。|Не удалось загрузить записи.|Không thể tải lịch sử.
Could not load tools.|无法加载工具。|無法載入工具。|Impossible de charger les outils.|ツールを読み込めません。|Не удалось загрузить инструменты.|Không thể tải công cụ.
Could not refresh. Try again.|刷新失败，请重试。|重新整理失敗，請重試。|Échec de l’actualisation. Réessayez.|更新に失敗しました。再試行してください。|Не удалось обновить. Повторите попытку.|Không thể tải lại. Hãy thử lại.
Create a seven-day connection token. It can discover tools and manage this client’s tool set. Calls still require a separate tool authorization.|创建有效期七天的连接令牌，用于发现工具和管理此客户端的工具集。调用仍需单独授权。|建立有效期七天的連線權杖，用於探索工具與管理此用戶端的工具集。呼叫仍需個別授權。|Créez un jeton valable sept jours pour découvrir les outils et gérer ceux de ce client. Les appels nécessitent une autorisation distincte.|7 日間有効な接続トークンでツールの検索とクライアントのツール管理ができます。呼び出しには別途許可が必要です。|Создайте токен на семь дней для поиска инструментов и управления набором клиента. Для вызовов нужно отдельное разрешение.|Tạo token có hiệu lực bảy ngày để tìm và quản lý bộ công cụ của ứng dụng. Việc gọi vẫn cần cấp quyền riêng.
Create connection token|创建连接令牌|建立連線權杖|Créer un jeton|接続トークンを作成|Создать токен подключения|Tạo token kết nối
Data recipient|数据接收方|資料接收方|Destinataire des données|データの送信先|Получатель данных|Bên nhận dữ liệu
Declared permissions|声明的权限|宣告的權限|Autorisations déclarées|申告された権限|Заявленные разрешения|Quyền được khai báo
Discover|发现|探索|Découvrir|探す|Обзор|Khám phá
Discover MCP tools, choose what each client can use, and pay only for successful calls.|发现 MCP 工具，为各客户端选择可用工具，仅为成功调用付费。|探索 MCP 工具，為各用戶端選擇可用工具，僅為成功呼叫付費。|Découvrez les outils MCP, choisissez ceux de chaque client et ne payez que les appels réussis.|MCP ツールを探し、各クライアントで使うものを選択。成功した呼び出しにのみ課金されます。|Находите MCP-инструменты, выбирайте доступные каждому клиенту и платите только за успешные вызовы.|Khám phá công cụ MCP, chọn công cụ cho từng ứng dụng và chỉ trả phí cho lần gọi thành công.
Do not include API keys or tokens in the URL.|不要在 URL 中填写 API Key 或令牌。|請勿在 URL 中填入 API Key 或權杖。|N’incluez aucune clé API ni aucun jeton dans l’URL.|URL に API キーやトークンを含めないでください。|Не указывайте API-ключи или токены в URL.|Không đưa API key hoặc token vào URL.
Edit draft|编辑草稿|編輯草稿|Modifier le brouillon|下書きを編集|Изменить черновик|Sửa bản nháp
Execution status|执行状态|執行狀態|État d’exécution|実行状況|Статус выполнения|Trạng thái thực thi
External account|外部账户|外部帳戶|Compte externe|外部アカウント|Внешний аккаунт|Tài khoản bên ngoài
Favorite|收藏|收藏|Ajouter aux favoris|お気に入りに追加|В избранное|Yêu thích
Files|文件|檔案|Fichiers|ファイル|Файлы|Tệp
Hide token|隐藏令牌|隱藏權杖|Masquer le jeton|トークンを隠す|Скрыть токен|Ẩn token
Income transfers|收益划转|收益劃轉|Transferts de revenus|収益の振替|Переводы дохода|Chuyển thu nhập
Load|加载|載入|Charger|読み込む|Загрузить|Nạp
Load a tool from the market, then authorize its client and spending limits.|从市场加载工具，再为客户端设置授权和消费限额。|從市集載入工具，再為用戶端設定授權與消費限額。|Chargez un outil, puis autorisez son client et fixez ses limites de dépenses.|マーケットからツールを追加し、クライアントの権限と支出上限を設定してください。|Загрузите инструмент из рынка, затем настройте разрешения клиента и лимиты расходов.|Nạp công cụ từ chợ, rồi cấp quyền cho ứng dụng và đặt hạn mức chi tiêu.
Loaded tools and authorizations|已加载工具与授权|已載入工具與授權|Outils chargés et autorisations|読み込み済みツールと許可|Загруженные инструменты и разрешения|Công cụ đã nạp và quyền
Loading…|加载中…|載入中…|Chargement…|読み込み中…|Загрузка…|Đang tải…
Market settings|市场设置|市集設定|Réglages du marché|マーケット設定|Настройки рынка|Cài đặt chợ
Maximum successful calls|最多成功调用次数|最多成功呼叫次數|Nombre maximal d’appels réussis|成功する呼び出しの上限回数|Максимум успешных вызовов|Số lần gọi thành công tối đa
My publications|我发布的服务|我發布的服務|Mes publications|公開したサービス|Мои публикации|Dịch vụ tôi đăng
Network|网络|網路|Réseau|ネットワーク|Сеть|Mạng
New authorization|新增授权|新增授權|Nouvelle autorisation|新しい許可|Новое разрешение|Cấp quyền mới
New calls|新调用|新呼叫|Nouveaux appels|新規呼び出し|Новые вызовы|Lần gọi mới
New tool calls are paused|新的工具调用已暂停|新的工具呼叫已暫停|Les nouveaux appels sont suspendus|新しいツール呼び出しは停止中です|Новые вызовы инструментов приостановлены|Đã tạm dừng lần gọi công cụ mới
No calls yet|暂无调用|尚無呼叫|Aucun appel pour l’instant|呼び出しはまだありません|Вызовов пока нет|Chưa có lần gọi
No income transfers yet|暂无收益划转|尚無收益劃轉|Aucun transfert de revenus|収益の振替はまだありません|Переводов дохода пока нет|Chưa có chuyển thu nhập
No pending reviews|暂无待审核项目|尚無待審核項目|Aucun examen en attente|審査待ちはありません|Нет заявок на проверку|Không có mục chờ duyệt
No published tools found|没有找到已发布的工具|找不到已發布的工具|Aucun outil publié trouvé|公開済みのツールが見つかりません|Опубликованные инструменты не найдены|Không tìm thấy công cụ đã công bố
OAuth connections use the client ID oauth:lmm-pi or oauth:lmm-dsh and require newly approved market scopes.|OAuth 连接使用客户端 ID oauth:lmm-pi 或 oauth:lmm-dsh，需要重新批准市场权限。|OAuth 連線使用用戶端 ID oauth:lmm-pi 或 oauth:lmm-dsh，需要重新核准市集權限。|Les connexions OAuth utilisent oauth:lmm-pi ou oauth:lmm-dsh et nécessitent une nouvelle autorisation pour le marché.|OAuth 接続のクライアント ID は oauth:lmm-pi または oauth:lmm-dsh です。マーケット権限の新規承認が必要です。|OAuth-подключения используют ID oauth:lmm-pi или oauth:lmm-dsh и требуют нового согласия на доступ к рынку.|Kết nối OAuth dùng ID oauth:lmm-pi hoặc oauth:lmm-dsh và cần duyệt mới các quyền của chợ.
Only me|仅自己|僅自己|Moi uniquement|自分のみ|Только я|Chỉ mình tôi
Only the arguments below are sent to this provider. Review them before running the tool.|仅将下方参数发送给此服务提供方，运行前请确认。|僅將下方參數傳送給此服務提供方，執行前請確認。|Seuls les paramètres ci-dessous sont envoyés à ce fournisseur. Vérifiez-les avant l’exécution.|以下の引数だけが提供者へ送信されます。実行前に確認してください。|Провайдеру отправляются только параметры ниже. Проверьте их перед запуском.|Chỉ các tham số bên dưới được gửi cho nhà cung cấp. Hãy kiểm tra trước khi chạy.
Open draft|打开草稿|開啟草稿|Ouvrir le brouillon|下書きを開く|Открыть черновик|Mở bản nháp
Parameter schema|参数定义|參數定義|Schéma des paramètres|引数の定義|Схема параметров|Lược đồ tham số
Platform fee (%)|平台手续费（%）|平台手續費（%）|Commission de la plateforme (%)|プラットフォーム手数料（%）|Комиссия платформы (%)|Phí nền tảng (%)
Price per successful call|每次成功调用的价格|每次成功呼叫的價格|Prix par appel réussi|成功した呼び出し 1 回の料金|Цена успешного вызова|Giá mỗi lần gọi thành công
Processing…|处理中…|處理中…|Traitement…|処理中…|Обработка…|Đang xử lý…
Provider account {{id}}|提供方账户 {{id}}|提供方帳戶 {{id}}|Compte fournisseur {{id}}|提供者アカウント {{id}}|Аккаунт провайдера {{id}}|Tài khoản nhà cung cấp {{id}}
Public|公开|公開|Public|公開|Публичный|Công khai
Publish a tool|发布工具|發布工具|Publier un outil|ツールを公開|Опубликовать инструмент|Đăng công cụ
Publish a tool service|发布工具服务|發布工具服務|Publier un service d’outils|ツールサービスを公開|Опубликовать сервис инструментов|Đăng dịch vụ công cụ
Read|读取|讀取|Lire|読み取り|Чтение|Đọc
Read tool definitions|读取工具定义|讀取工具定義|Lire les définitions|ツール定義を取得|Получить определения|Đọc định nghĩa công cụ
Recent calls|最近调用|最近呼叫|Appels récents|最近の呼び出し|Недавние вызовы|Lần gọi gần đây
Refresh result|刷新结果|重新整理結果|Actualiser le résultat|結果を更新|Обновить результат|Tải lại kết quả
Remote MCP endpoint|Remote MCP 地址|Remote MCP 位址|Adresse Remote MCP|Remote MCP のアドレス|Адрес Remote MCP|Địa chỉ Remote MCP
Remove favorite|取消收藏|取消收藏|Retirer des favoris|お気に入りを解除|Убрать из избранного|Bỏ yêu thích
Reserved|已冻结|已凍結|Réservé|確保済み|Зарезервировано|Đã giữ
Retry same request|重试同一请求|重試同一請求|Réessayer la même demande|同じリクエストを再試行|Повторить тот же запрос|Thử lại cùng yêu cầu
Review queue|审核队列|審核佇列|File d’examen|審査待ち一覧|Очередь проверки|Hàng đợi duyệt
Review reason|审核理由|審核理由|Motif de la décision|審査理由|Причина решения|Lý do duyệt
Revoke authorization|撤销授权|撤銷授權|Révoquer l’autorisation|許可を取り消す|Отозвать разрешение|Thu hồi quyền
Revoked|已撤销|已撤銷|Révoqué|取り消し済み|Отозвано|Đã thu hồi
Run for up to {{amount}} credits|运行，最多消费 {{amount}} 额度|執行，最多消費 {{amount}} 額度|Exécuter pour au plus {{amount}} crédits|最大 {{amount}} クレジットで実行|Запустить с лимитом {{amount}} кредитов|Chạy với tối đa {{amount}} tín dụng
Run tool|运行工具|執行工具|Exécuter l’outil|ツールを実行|Запустить инструмент|Chạy công cụ
Running…|运行中…|執行中…|Exécution…|実行中…|Выполнение…|Đang chạy…
Save budget|保存预算|儲存預算|Enregistrer le budget|予算を保存|Сохранить бюджет|Lưu ngân sách
Saving…|保存中…|儲存中…|Enregistrement…|保存中…|Сохранение…|Đang lưu…
Search tools|搜索工具|搜尋工具|Rechercher des outils|ツールを検索|Поиск инструментов|Tìm công cụ
Select the tools to publish and review their permissions. Prices are per successful call in platform credits.|选择要发布的工具并核对权限。价格以平台额度计，按成功调用次数收费。|選擇要發布的工具並核對權限。價格以平台額度計，按成功呼叫次數收費。|Sélectionnez les outils et vérifiez leurs autorisations. Les prix sont en crédits de plateforme par appel réussi.|公開するツールを選び、権限を確認してください。料金は成功した呼び出しごとのプラットフォームクレジットです。|Выберите инструменты и проверьте разрешения. Цена указана в кредитах платформы за успешный вызов.|Chọn công cụ cần đăng và kiểm tra quyền. Giá tính bằng tín dụng nền tảng cho mỗi lần gọi thành công.
Settings could not be saved. Check the fee and recipient account.|无法保存设置，请检查费率和收款账户。|無法儲存設定，請檢查費率與收款帳戶。|Impossible d’enregistrer. Vérifiez la commission et le compte bénéficiaire.|設定を保存できません。手数料と受取アカウントを確認してください。|Не удалось сохранить настройки. Проверьте комиссию и аккаунт получателя.|Không thể lưu cài đặt. Kiểm tra phí và tài khoản nhận.
Settled|已结算|已結算|Réglé|精算済み|Оплачено|Đã quyết toán
Single-call limit|单次消费上限|單次消費上限|Plafond par appel|1 回の支出上限|Лимит одного вызова|Hạn mức mỗi lần gọi
Specific users|指定用户|指定使用者|Utilisateurs désignés|指定ユーザー|Выбранные пользователи|Người dùng chỉ định
Spending budgets|消费预算|消費預算|Budgets de dépenses|支出予算|Бюджеты расходов|Ngân sách chi tiêu
Succeeded|已成功|已成功|Réussi|成功|Успешно|Thành công
Successful calls are charged once. Repeated delivery of this request does not charge again.|成功调用只收费一次，重复提交本请求不会再次收费。|成功呼叫只收費一次，重複提交本請求不會再次收費。|Un appel réussi est facturé une seule fois. Répéter cette demande n’entraîne pas de nouvelle facturation.|成功した呼び出しは一度だけ課金されます。同じリクエストを再送しても追加課金はありません。|Успешный вызов оплачивается один раз. Повторная отправка этого запроса не списывает средства снова.|Lần gọi thành công chỉ tính phí một lần. Gửi lại cùng yêu cầu không bị tính phí thêm.
Successful calls transfer the fee to this super administrator account and the remainder directly to the author.|调用成功后，手续费划入此超级管理员账户，其余收入直接划给作者。|呼叫成功後，手續費劃入此超級管理員帳戶，其餘收入直接劃給作者。|Après un appel réussi, la commission est versée à ce compte super administrateur et le reste directement à l’auteur.|呼び出し成功後、手数料はこのスーパー管理者アカウントへ、残額は作者へ直接振り替えられます。|После успешного вызова комиссия переводится этому суперадминистратору, а остаток — напрямую автору.|Khi gọi thành công, phí được chuyển cho tài khoản quản trị viên cấp cao này, phần còn lại chuyển thẳng cho tác giả.
Super administrator account ID|超级管理员账户 ID|超級管理員帳戶 ID|ID du compte super administrateur|スーパー管理者のアカウント ID|ID аккаунта суперадминистратора|ID tài khoản quản trị viên cấp cao
The call could not be completed. Check your grant, budget, balance and arguments. A retry uses the same request ID.|调用未能完成，请检查授权、预算、余额和参数。重试会使用同一请求 ID。|呼叫未能完成，請檢查授權、預算、餘額與參數。重試會使用同一請求 ID。|L’appel n’a pas abouti. Vérifiez l’autorisation, le budget, le solde et les paramètres. La nouvelle tentative conserve le même ID.|呼び出しを完了できませんでした。権限、予算、残高、引数を確認してください。再試行では同じリクエスト ID を使います。|Вызов не завершён. Проверьте разрешение, бюджет, баланс и параметры. Повтор использует тот же ID запроса.|Không thể hoàn tất lần gọi. Kiểm tra quyền, ngân sách, số dư và tham số. Khi thử lại sẽ dùng cùng ID yêu cầu.
The operation failed. Check the current status, permissions and configuration, then retry.|操作失败，请检查当前状态、权限和配置后重试。|操作失敗，請檢查目前狀態、權限與設定後重試。|Échec de l’opération. Vérifiez l’état, les autorisations et la configuration, puis réessayez.|操作に失敗しました。現在の状態、権限、設定を確認して再試行してください。|Операция не выполнена. Проверьте статус, права и настройки и повторите попытку.|Thao tác thất bại. Kiểm tra trạng thái, quyền và cấu hình rồi thử lại.
The operation failed. Check the fields, endpoint and supported tool definitions, then retry.|操作失败，请检查字段、服务地址及工具定义是否受支持后重试。|操作失敗，請檢查欄位、服務位址及工具定義是否受支援後重試。|Échec de l’opération. Vérifiez les champs, l’adresse et les définitions prises en charge, puis réessayez.|操作に失敗しました。入力欄、接続先、対応するツール定義を確認して再試行してください。|Операция не выполнена. Проверьте поля, адрес и поддержку определений инструментов.|Thao tác thất bại. Kiểm tra các trường, địa chỉ và định nghĩa công cụ được hỗ trợ rồi thử lại.
The operation failed. Check the limits and connection settings, then retry.|操作失败，请检查限额和连接设置后重试。|操作失敗，請檢查限額與連線設定後重試。|Échec de l’opération. Vérifiez les limites et les réglages de connexion, puis réessayez.|操作に失敗しました。上限と接続設定を確認して再試行してください。|Операция не выполнена. Проверьте лимиты и настройки подключения.|Thao tác thất bại. Kiểm tra hạn mức và cài đặt kết nối rồi thử lại.
The result is not settled. {{amount}} credits remain reserved until {{time}}. Check this request instead of starting it again.|结果尚未结算，{{amount}} 额度冻结至 {{time}}。请查询本请求，不要重新发起。|結果尚未結算，{{amount}} 額度凍結至 {{time}}。請查詢本請求，不要重新發起。|Le résultat n’est pas réglé. {{amount}} crédits sont réservés jusqu’au {{time}}. Consultez cette demande sans la relancer.|結果は未精算です。{{amount}} クレジットを {{time}} まで確保しています。再実行せず、このリクエストを確認してください。|Расчёт ещё не завершён. До {{time}} зарезервировано {{amount}} кредитов. Проверяйте этот запрос, не запускайте новый.|Kết quả chưa được quyết toán. {{amount}} tín dụng được giữ đến {{time}}. Hãy kiểm tra yêu cầu này thay vì khởi chạy lại.
The saved result has expired. The call and transfer records are retained.|保存的结果已过期，调用和划转记录仍保留。|儲存的結果已過期，呼叫與劃轉紀錄仍保留。|Le résultat enregistré a expiré. Les historiques d’appel et de transfert sont conservés.|保存結果の期限が切れました。呼び出しと振替の記録は保持されます。|Срок хранения результата истёк. Записи вызова и переводов сохранены.|Kết quả lưu đã hết hạn. Lịch sử gọi và chuyển tiền vẫn được giữ.
This grants the selected client permission to use this exact tool version within these limits.|这将允许所选客户端在以下限额内使用此工具的当前版本。|這將允許所選用戶端在以下限額內使用此工具的目前版本。|Le client choisi pourra utiliser cette version précise de l’outil dans ces limites.|選択したクライアントに、このツールの指定バージョンを以下の上限内で使用する権限を与えます。|Выбранный клиент получит доступ к этой конкретной версии инструмента в указанных пределах.|Cho phép ứng dụng đã chọn dùng đúng phiên bản công cụ này trong các hạn mức sau.
This service is unavailable or you do not have access.|服务不可用或你没有访问权限。|服務無法使用或你沒有存取權限。|Ce service est indisponible ou vous n’y avez pas accès.|サービスが利用できないか、アクセス権がありません。|Сервис недоступен или у вас нет прав доступа.|Dịch vụ không khả dụng hoặc bạn không có quyền truy cập.
Tool|工具|工具|Outil|ツール|Инструмент|Công cụ
Tool ID|工具 ID|工具 ID|ID de l’outil|ツール ID|ID инструмента|ID công cụ
Tool market|工具市场|工具市集|Marché des outils|ツールマーケット|Рынок инструментов|Chợ công cụ
Tools and prices|工具与价格|工具與價格|Outils et tarifs|ツールと料金|Инструменты и цены|Công cụ và giá
Total spending limit|累计消费上限|累計消費上限|Plafond total de dépenses|支出の累計上限|Общий лимит расходов|Tổng hạn mức chi tiêu
Try another search, or publish a Remote MCP service for review.|换个关键词搜索，或发布 Remote MCP 服务并提交审核。|換個關鍵字搜尋，或發布 Remote MCP 服務並提交審核。|Essayez une autre recherche ou soumettez un service Remote MCP à l’examen.|別の語句で検索するか、Remote MCP サービスを審査に提出してください。|Измените запрос или отправьте Remote MCP-сервис на проверку.|Thử từ khóa khác hoặc gửi dịch vụ Remote MCP để xét duyệt.
Unload|卸载|卸載|Retirer|読み込みを解除|Выгрузить|Gỡ
Use this same client ID when loading and authorizing tools.|加载和授权工具时使用同一个客户端 ID。|載入與授權工具時請使用同一個用戶端 ID。|Utilisez ce même ID client pour charger et autoriser les outils.|ツールの読み込みと許可には同じクライアント ID を使ってください。|Используйте тот же ID клиента при загрузке и выдаче разрешений.|Dùng cùng ID ứng dụng này khi nạp và cấp quyền cho công cụ.
Use web-market for this browser, or the client ID from your MCP connection.|在本浏览器中使用 web-market，其他客户端填写 MCP 连接中的客户端 ID。|在本瀏覽器中使用 web-market，其他用戶端填入 MCP 連線中的用戶端 ID。|Utilisez web-market pour ce navigateur, ou l’ID client de votre connexion MCP.|このブラウザーには web-market、MCP 接続にはそのクライアント ID を使います。|Для этого браузера используйте web-market, для MCP — ID своего клиента.|Dùng web-market cho trình duyệt này hoặc ID ứng dụng từ kết nối MCP.
User IDs, separated by commas|用户 ID，以逗号分隔|使用者 ID，以逗號分隔|ID utilisateurs séparés par des virgules|ユーザー ID（カンマ区切り）|ID пользователей через запятую|ID người dùng, phân cách bằng dấu phẩy
Valid for hours|有效小时数|有效小時數|Durée en heures|有効時間（時間）|Срок действия в часах|Số giờ có hiệu lực
Validate connection|校验连接|驗證連線|Valider la connexion|接続を検証|Проверить подключение|Xác thực kết nối
Validate the connection and definitions before review.|提交审核前请校验连接和工具定义。|提交審核前請驗證連線與工具定義。|Validez la connexion et les définitions avant l’examen.|審査の前に接続とツール定義を検証してください。|Перед проверкой подтвердите соединение и определения инструментов.|Xác thực kết nối và định nghĩa trước khi duyệt.
View result|查看结果|檢視結果|Voir le résultat|結果を表示|Посмотреть результат|Xem kết quả
Write|写入|寫入|Écrire|書き込み|Запись|Ghi
You can still browse tools and manage drafts, connections and existing records.|仍可浏览工具，管理草稿、连接和已有记录。|仍可瀏覽工具，管理草稿、連線與現有紀錄。|Vous pouvez toujours parcourir les outils et gérer les brouillons, connexions et historiques.|ツールの閲覧と、下書き・接続・既存記録の管理は引き続き利用できます。|Просмотр инструментов, управление черновиками, подключениями и записями остаются доступны.|Bạn vẫn có thể xem công cụ và quản lý bản nháp, kết nối và lịch sử.
You have not published any services yet.|你还没有发布服务。|你尚未發布服務。|Vous n’avez encore publié aucun service.|まだサービスを公開していません。|Вы ещё не публиковали сервисы.|Bạn chưa đăng dịch vụ nào.
{{amount}} credits|{{amount}} 额度|{{amount}} 額度|{{amount}} crédits|{{amount}} クレジット|{{amount}} кредитов|{{amount}} tín dụng
{{amount}} credits per successful call|每次成功调用 {{amount}} 额度|每次成功呼叫 {{amount}} 額度|{{amount}} crédits par appel réussi|成功した呼び出し 1 回につき {{amount}} クレジット|{{amount}} кредитов за успешный вызов|{{amount}} tín dụng mỗi lần gọi thành công
`
  .trim()
  .split('\n')
  .map((row) => row.split('|'))
const locales = ['en', 'zh', 'zh-TW', 'fr', 'ja', 'ru', 'vi']
for (const row of rows) {
  if (row.length !== locales.length) {
    throw new Error(`Invalid tool market translation: ${row[0]}`)
  }
}
export const toolMarketCopy = Object.fromEntries(
  locales.map((locale, index) => [
    locale,
    Object.fromEntries(rows.map((row) => [row[0], row[index]])),
  ])
)
