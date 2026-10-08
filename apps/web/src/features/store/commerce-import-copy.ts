/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
export const STORE_COMMERCE_IMPORT_COPY = {
  title: 'Import from an external shop',
  description:
    'Connect a trusted shop, review its product fields, and save a local draft.',
  connectTitle: 'Connect a shop',
  origin: 'Trusted shop origin',
  clientId: 'Registered client ID',
  clientHelp:
    'Register this callback URL in the external shop, then enter its public client ID. No client secret is needed.',
  callback: 'Callback URL',
  connect: 'Connect and authorize product reading',
  connections: 'Shop connections',
  noConnections: 'No connected shops yet',
  selectConnection: 'Choose a shop connection',
  readAuthorize: 'Authorize product reading',
  issueAuthorize: 'Authorize generation of new cards',
  issueHelp:
    'This separately requests permission to generate new cards. Approve the products, variants, cumulative limits and expiry in the external shop.',
  readOnly: 'Product reading only',
  issueScope: 'Card generation authorized',
  pendingConnection: 'Awaiting authorization',
  activeConnection: 'Connected',
  reauthorizeConnection: 'Authorization required',
  disconnectedConnection: 'Disconnected',
  disconnect: 'Disconnect shop',
  disconnectTitle: 'Disconnect this shop?',
  disconnectHelp:
    'This stops product reading and card generation. Cards already issued remain valid, and existing local products and inventory are kept.',
  disconnectRetentionHelp:
    'Disconnecting clears connection credentials and recoverable responses. Unresolved batches still require manual reconciliation. Existing products, inventory, orders and financial records are kept.',
  cancel: 'Cancel',
  connected:
    'Shop connected. Review products before importing; no product or inventory has been imported yet.',
  denied:
    'Authorization was declined. You can authorize product reading when ready.',
  failed:
    'The connection could not be authorized. Start a new authorization request.',
  refresh: 'Refresh products and batch status',
  catalog: 'Authorized products',
  noProducts: 'No authorized products available',
  productsHelp:
    'Only product information is imported. Existing cards, redemption counts and external inventory are not imported.',
  preview: 'Review field mapping',
  name: 'Product title',
  descriptionField: 'Product description',
  externalProduct: 'External product ID',
  externalVariant: 'External variant ID',
  redemption: 'Buyer redemption URL',
  revision: 'Product revision',
  mappingTitle: 'Fields requiring separate setup',
  mappingHelp:
    'These fields are retained as source information but do not become local checkout fields, delivery content, progress or media. Review them and add suitable buyer instructions to the draft.',
  noUnsupported: 'No additional fields require separate setup.',
  stockHelp:
    'This product uses external text-card stock. Its information can be imported, but this protocol cannot generate cards for it.',
  variantName: 'Variant name',
  reference: 'External reference price',
  unknownPrice: 'Unknown reference price',
  referenceHelp:
    'Reference prices are informational. CNY and other currencies are never converted automatically. Enter and confirm the actual local selling price for every variant.',
  priceUnit: 'Actual selling price unit',
  usd: 'USD',
  integerQuota: 'Integer price_quota',
  price: 'Actual selling price',
  priceHelp:
    '1 USD = 500,000 price_quota, fixed. The saved price_quota must be a safe nonnegative integer; fractional units are rejected.',
  priceError:
    'Enter a valid USD decimal or integer price_quota without fractional units.',
  localEnabled: 'Enable this local variant',
  externalDisabled: 'Disabled by the external shop',
  visibility: 'Local product visibility',
  chooseVisibility: 'Choose local visibility',
  publicVisibility: 'Public',
  registeredVisibility: 'Signed-in users',
  privateVisibility: 'Private preview',
  visibilityHelp:
    'Choose local visibility explicitly. External public visibility does not set local visibility. Saving creates a draft and does not publish it.',
  confirm:
    'I have reviewed the mapping, actual selling prices, variant settings and local visibility.',
  saveDraft: 'Save as a local draft',
  saved:
    'Local draft saved. Review it in the seller center before submitting it for publication.',
  linked: 'Linked local product: {{id}}',
  changed:
    'External product fields changed since the last import. Review the new fields before saving or creating a new batch.',
  restock: 'Generate new cards for local inventory',
  restockHelp:
    'This generates new cards for the selected external variant and checks its approved cumulative issuance limit. Issuance limits and batch quota snapshots are not local sales inventory.',
  restockVariant: 'Linked external variant to restock',
  chooseVariant: 'Choose a linked variant',
  count: 'Number of new cards',
  countHelp: 'Enter a whole number from 1 to {{maximum}}.',
  label: 'Batch label',
  restockConfirm:
    'I confirm this will generate new cards for this linked variant.',
  generate: 'Confirm and generate new cards',
  batchHistory: 'Card batch requests',
  noRequests: 'No card batch requests yet',
  pendingRequest: 'Result pending confirmation',
  receivedRequest: 'Received; awaiting local inventory import',
  importedRequest: 'Imported into local inventory',
  manualRequest: 'Manual reconciliation required',
  recover: 'Recover the original request',
  recoveryHelp:
    'Recovery uses the stored original request and original key. It does not create a new batch. If a response expired or issuance cannot be reconciled, check the external shop manually.',
  requestUnknown:
    'The result is not confirmed. Refresh batch status and recover the original request; do not create another batch to retry.',
  requestFailed:
    'The commerce import request failed. Review the connection and refresh its status.',
  catalogChanged:
    'The external product changed. Refresh and review the current fields before creating a new batch.',
  unavailable:
    'The product or variant is unavailable. Refresh the authorized products and stop restocking this variant.',
  quotaExceeded:
    'The approved cumulative issuance limit was reached. Request a new limit from the merchant.',
  insufficientScope:
    'This action requires a new authorization with the requested permission.',
  invalidGrant:
    'The authorization is no longer usable. Start a new authorization request.',
  manualError:
    'The original issuance needs manual reconciliation. Do not generate a replacement batch automatically.',
  invalidCatalog: 'The external shop returned an unsupported product catalog.',
  invalidAuthorization: 'The shop returned an invalid authorization address.',
  refundExternalUnknown:
    'The external redemption status is unknown and needs manual verification with the merchant. A local refund does not revoke external cards.',
} as const

// Draft only. Locale files are maintained by the separately assigned translator.
export const STORE_COMMERCE_IMPORT_ZH_CN: Record<
  keyof typeof STORE_COMMERCE_IMPORT_COPY,
  string
> = {
  title: '从外部商城导入',
  description: '连接可信商城，预览商品字段，保存为本地草稿。',
  connectTitle: '连接商城',
  origin: '可信商城地址',
  clientId: '已登记的客户端 ID',
  clientHelp:
    '先在外部商城登记这个回调地址，再填写其公开客户端 ID。无需客户端密钥。',
  callback: '回调地址',
  connect: '连接并授权读取商品',
  connections: '商城连接',
  noConnections: '还没有连接商城',
  selectConnection: '选择商城连接',
  readAuthorize: '授权读取商品',
  issueAuthorize: '授权生成新卡密',
  issueHelp:
    '此操作单独申请生成新卡密的权限。请在外部商城批准商品、规格、累计额度和有效期。',
  readOnly: '仅可读取商品',
  issueScope: '已授权生成卡密',
  pendingConnection: '等待授权',
  activeConnection: '已连接',
  reauthorizeConnection: '需要重新授权',
  disconnectedConnection: '已断开',
  disconnect: '断开商城',
  disconnectTitle: '断开这个商城？',
  disconnectHelp:
    '断开后停止读取商品和生成卡密。已发行卡密仍有效，本地商品和库存会保留。',
  disconnectRetentionHelp:
    '断开会清除连接凭据和可恢复回执。结果未确认的批次仍需人工核对。已有商品、库存、订单和财务记录会保留。',
  cancel: '取消',
  connected: '商城已连接。请预览后再导入；目前尚未导入商品或库存。',
  denied: '授权已拒绝。准备好后可重新授权读取商品。',
  failed: '商城授权失败。请重新发起授权。',
  refresh: '刷新商品和批次状态',
  catalog: '已授权商品',
  noProducts: '没有可读取的已授权商品',
  productsHelp: '仅导入商品资料，不导入已有卡密、兑换数量或外部库存。',
  preview: '预览字段映射',
  name: '商品标题',
  descriptionField: '商品介绍',
  externalProduct: '外部商品 ID',
  externalVariant: '外部规格 ID',
  redemption: '买家兑换地址',
  revision: '商品资料版本',
  mappingTitle: '需要单独设置的字段',
  mappingHelp:
    '这些字段保留为来源资料，不会直接成为本地结算字段、交付内容、进度或媒体。请检查并在草稿中补充合适的购买说明。',
  noUnsupported: '没有需要单独设置的附加字段。',
  stockHelp:
    '该商品使用外部文本卡库存。可以导入资料，但不能通过此协议生成卡密。',
  variantName: '规格名称',
  reference: '外部参考价',
  unknownPrice: '参考价未知',
  referenceHelp:
    '参考价仅供说明。CNY 等币种不会自动换汇。请逐项填写并确认本地实际售价。',
  priceUnit: '实际售价单位',
  usd: 'USD',
  integerQuota: '整数 price_quota',
  price: '实际售价',
  priceHelp:
    '固定 1 USD = 500,000 price_quota。保存值必须是安全范围内的非负整数，不接受不足一个单位的小数。',
  priceError:
    '请填写合法 USD 十进制或整数 price_quota，不可含不足一个单位的小数。',
  localEnabled: '启用这个本地规格',
  externalDisabled: '外部商城已禁用',
  visibility: '本地商品可见范围',
  chooseVisibility: '选择本地可见范围',
  publicVisibility: '公开',
  registeredVisibility: '仅登录用户',
  privateVisibility: '私密预览',
  visibilityHelp:
    '请明确选择本地可见范围。外部公开状态不会决定本地范围。保存只创建草稿，不会发布。',
  confirm: '我已检查字段映射、实际售价、规格设置和本地可见范围。',
  saveDraft: '保存为本地草稿',
  saved: '本地草稿已保存。请在卖家中心检查后再提交发布。',
  linked: '已关联本地商品：{{id}}',
  changed: '外部商品资料与上次导入不同。请检查新资料后再保存或生成新批次。',
  restock: '生成新卡密补充本地库存',
  restockHelp:
    '为选定外部规格生成新卡密，并校验已批准的累计发行额度。发行额度和批次额度快照都不是本地销售库存。',
  restockVariant: '选择已关联的外部补货规格',
  chooseVariant: '选择已关联规格',
  count: '新卡密数量',
  countHelp: '请填写 1 至 {{maximum}} 的整数。',
  label: '批次名称',
  restockConfirm: '我确认将为这个已关联规格生成新卡密。',
  generate: '确认并生成新卡密',
  batchHistory: '卡密批次请求',
  noRequests: '还没有卡密批次请求',
  pendingRequest: '结果待确认',
  receivedRequest: '已收到，等待导入本地库存',
  importedRequest: '已导入本地库存',
  manualRequest: '需要人工核对',
  recover: '恢复原请求',
  recoveryHelp:
    '恢复使用已保存的原请求和原键，不创建新批次。响应过期或发行结果无法核对时，请到外部商城人工核查。',
  requestUnknown:
    '结果尚未确认。请刷新批次状态并恢复原请求，不要另建批次重试。',
  requestFailed: '商城导入请求失败。请检查连接并刷新状态。',
  catalogChanged: '外部商品已变更。请刷新并检查当前资料后再创建新批次。',
  unavailable: '商品或规格不可用。请刷新已授权商品并停止为该规格补货。',
  quotaExceeded: '已达到批准的累计发行额度。请向商家申请新额度。',
  insufficientScope: '此操作需要重新授权相应权限。',
  invalidGrant: '授权已不可用。请重新发起授权。',
  manualError: '原批次发行需要人工核对。不要自动生成替代批次。',
  invalidCatalog: '外部商城返回了不支持的商品目录。',
  invalidAuthorization: '商城返回的授权地址无效。',
  refundExternalUnknown:
    '外部兑换状态未知，需要向商家人工核对。本地退款不会撤销外部卡密。',
}
