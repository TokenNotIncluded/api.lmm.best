/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
export const COMMERCE_IMPORT_SETTINGS_COPY = {
  title: 'External shop imports',
  description:
    'Choose the external shop servers that sellers may connect to. Sellers register their client ID and approve product permissions directly in each trusted shop.',
  enabled: 'Enable external shop imports',
  enabledHelp:
    'Allow signed-in sellers to connect to the configured trusted origins. Disabled by default.',
  origins: 'Trusted external shop origins',
  originsHelp:
    'Enter one public HTTPS origin per line, up to 50. Use only the scheme and hostname with an optional port, without a trailing slash, path, credentials, query or fragment. These addresses are network trust targets, not product or buyer links.',
  invalidOrigins:
    'Enter up to 50 unique public HTTPS origins without paths, credentials or internal addresses.',
  enabledWithoutOrigins:
    'Add at least one trusted origin before enabling external shop imports.',
  save: 'Save external shop import settings',
  environmentOverride:
    'The server environment currently overrides these stored settings. Saving here persists the configuration, but the override must be removed on the server before the stored switch and origins become effective.',
  effectiveOrigins: 'Currently effective trusted origins',
  saved: 'External shop import settings saved.',
} as const

export const COMMERCE_IMPORT_SETTINGS_ZH_CN: Record<
  keyof typeof COMMERCE_IMPORT_SETTINGS_COPY,
  string
> = {
  title: '外部商城导入',
  description:
    '设置卖家可以连接的外部商城服务器。卖家自行登记客户端 ID，并在对应可信商城批准商品权限。',
  enabled: '启用外部商城导入',
  enabledHelp: '允许已登录卖家连接配置的可信商城地址。默认关闭。',
  origins: '可信外部商城地址',
  originsHelp:
    '每行填写一个公开 HTTPS 地址，最多 50 个。只包含协议、域名和可选端口，不含末尾斜杠、路径、凭据、查询或片段。这些地址是服务器网络信任目标，不是商品或买家链接。',
  invalidOrigins:
    '请填写至多 50 个不重复的公开 HTTPS 地址，不含路径、凭据或内网地址。',
  enabledWithoutOrigins: '请先添加至少一个可信商城地址，再启用外部商城导入。',
  save: '保存外部商城导入设置',
  environmentOverride:
    '服务器环境当前覆盖了这些保存设置。这里可以持久保存配置，但需要在服务器移除覆盖设置后，已保存的开关和地址才会生效。',
  effectiveOrigins: '当前实际使用的可信地址',
  saved: '外部商城导入设置已保存。',
}
