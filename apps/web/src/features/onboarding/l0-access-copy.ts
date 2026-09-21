/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
// Feature-local copy follows the existing interface language.
const en = {
  greeting: 'What will you make?',
  prompt: 'Ask a question. Start an idea.',
  models: 'Choose a model',
  tools: 'Connect a tool',
  privacy: 'Keep passwords and API keys out of this conversation.',
  title: 'Unlock developer access',
  description: 'Qualifying credit unlocks L1. No review.',
  topup: 'Top up to L1',
  apply: 'Apply instead',
  check: 'Check payment',
  remaining: 'Eligible API credit needed',
  minimum: 'One eligible paid top-up',
  rules: 'Top-up eligibility',
  eligibility:
    'External paid top-ups only. LinuxDO Credit is excluded. Checkout shows the amount you pay.',
  review: 'Apply for developer access',
  reviewNote: 'Paid activation is unavailable for this account. Apply below.',
  unknown: 'Access conditions are unavailable. Refresh before paying.',
  sync: 'No further top-up needed',
  syncNote: 'Credit requirement met. Refresh to confirm access.',
  checking: 'Checking access…',
  waiting: 'Awaiting payment confirmation. Do not pay again.',
  error: 'Refresh failed. Retrying; do not pay again.',
  timeout: 'Checks stopped. Review your order or contact support before paying again.',
  help: 'Need help choosing?',
  plans: 'Plans & top-ups',
  support: 'Contact support',
  application: 'Apply for access',
  oauth: 'Connect with Pi',
}

type Copy = typeof en
const zhCN: Copy = {
  greeting: '想做点什么？',
  prompt: '问个问题，或说说你的想法',
  models: '选个模型',
  tools: '连接工具',
  privacy: '请勿发送密码、API 密钥等敏感信息。',
  title: '开通开发者权限',
  description: '充值达标，到账即开通，无需审核。',
  topup: '充值开通 L1',
  apply: '申请开通',
  check: '检查到账',
  remaining: '还需有效 API 额度',
  minimum: '完成一笔有效充值即可',
  rules: '充值开通规则',
  eligibility:
    '仅外部付费充值计入，不含 LinuxDO Credit。实际支付金额以收银台为准。',
  review: '申请开发者权限',
  reviewNote: '此账户暂不支持充值开通，请在下方申请。',
  unknown: '暂未读取到开通条件，请先刷新，不要重复付款。',
  sync: '无需继续充值',
  syncNote: '已达到额度要求，刷新确认开通状态。',
  checking: '正在检查开通状态…',
  waiting: '正在等待到账确认，请勿重复付款。',
  error: '刷新失败，正在重试，请勿重复付款。',
  timeout: '检查已停止，请核对订单或联系客服，勿重复付款。',
  help: '不确定怎么选？',
  plans: '套餐与充值',
  support: '联系支持',
  application: '申请开通',
  oauth: '连接 Pi',
}
const zhTW: Copy = {
  greeting: '想做點什麼？',
  prompt: '問個問題，或說說你的想法',
  models: '選個模型',
  tools: '連接工具',
  privacy: '請勿傳送密碼、API 金鑰等敏感資訊。',
  title: '開通開發者權限',
  description: '儲值達標，到帳即開通，無需審核。',
  topup: '儲值開通 L1',
  apply: '申請開通',
  check: '檢查到帳',
  remaining: '仍需有效 API 額度',
  minimum: '完成一筆有效儲值即可',
  rules: '儲值開通規則',
  eligibility:
    '僅外部付費儲值計入，不含 LinuxDO Credit。實際支付金額以結帳頁為準。',
  review: '申請開發者權限',
  reviewNote: '此帳戶暫不支援儲值開通，請在下方申請。',
  unknown: '暫未讀取到開通條件，請先重新整理，不要重複付款。',
  sync: '無需繼續儲值',
  syncNote: '已達到額度要求，重新整理以確認開通狀態。',
  checking: '正在檢查開通狀態…',
  waiting: '正在等待到帳確認，請勿重複付款。',
  error: '更新失敗，正在重試，請勿重複付款。',
  timeout: '檢查已停止，請核對訂單或聯絡客服，勿重複付款。',
  help: '不確定怎麼選？',
  plans: '方案與儲值',
  support: '聯絡支援',
  application: '申請開通',
  oauth: '連接 Pi',
}

export function getL0AccessCopy(language: string): Copy {
  const locale = language.toLowerCase().replaceAll('-', '')
  if (['zhtw', 'zhhk', 'zhhant'].includes(locale)) return zhTW
  if (locale.startsWith('zh')) return zhCN
  return en
}
