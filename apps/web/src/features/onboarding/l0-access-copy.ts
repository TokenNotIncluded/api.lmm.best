/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
// Feature-local copy follows the existing interface language, without registering
// a second global i18next instance or changing translations on other pages.
const en = {
  title: 'Top up. Skip the review.',
  description: 'Qualifying credit unlocks L1 as soon as the payment is credited.',
  topup: 'Top up and unlock L1',
  apply: 'Apply for access instead',
  check: 'Already paid? Check status',
  remaining: 'Eligible API credit still needed',
  minimum: 'Any successful eligible top-up',
  eligibility: 'External paid top-ups only. LinuxDO Credit is excluded. Checkout shows the amount you pay.',
  review: 'Apply for developer access',
  reviewNote: 'This account is not eligible for automatic paid activation. Use the application below.',
  unknown: 'Access conditions are not available yet. Refresh before paying for an upgrade.',
  sync: 'No additional top-up needed',
  syncNote: 'The credit requirement is met. Refresh to confirm developer access with the server.',
  checking: 'Checking account access…',
  waiting: 'Not activated yet. Checking for credited payment; do not pay again.',
  error: 'Could not refresh account access. Retrying; do not pay again.',
  timeout: 'Automatic checks have stopped. Check the order or contact support before paying again.',
  help: 'Need help choosing?',
}

type Copy = typeof en
const zhCN: Copy = {
  title: '充值到账，免审核开通。',
  description: '达到开通额度后自动升级 L1，无需填写用途或等待审核。',
  topup: '充值并开通 L1',
  apply: '暂不充值，申请开通',
  check: '我已充值，检查状态',
  remaining: '还需到账的有效 API 额度',
  minimum: '完成一笔有效充值即可',
  eligibility: '仅外部付费充值计入，不含 LinuxDO Credit。实际支付金额以收银台为准。',
  review: '申请开发者权限',
  reviewNote: '当前账户不适用充值自动开通，请通过下方申请。',
  unknown: '暂未读取到开通条件。请先刷新状态，不要为升级重复付款。',
  sync: '无需继续充值',
  syncNote: '已达到额度要求，刷新后由服务器确认开通状态。',
  checking: '正在检查开通状态…',
  waiting: '暂未开通，正在等待到账确认，请勿重复付款。',
  error: '状态刷新失败，正在重试，请勿重复付款。',
  timeout: '自动检查已停止。请核对订单或联系客服，不要重复付款。',
  help: '不确定怎么选？',
}
const zhTW: Copy = {
  title: '儲值到帳，免審核開通。',
  description: '達到開通額度後自動升級 L1，無需填寫用途或等待審核。',
  topup: '儲值並開通 L1',
  apply: '暫不儲值，申請開通',
  check: '我已儲值，檢查狀態',
  remaining: '仍需到帳的有效 API 額度',
  minimum: '完成一筆有效儲值即可',
  eligibility: '僅外部付費儲值計入，不含 LinuxDO Credit。實際支付金額以結帳頁為準。',
  review: '申請開發者權限',
  reviewNote: '目前帳戶不適用儲值自動開通，請透過下方申請。',
  unknown: '暫未讀取到開通條件。請先重新整理狀態，不要為升級重複付款。',
  sync: '無需繼續儲值',
  syncNote: '已達到額度要求，重新整理後由伺服器確認開通狀態。',
  checking: '正在檢查開通狀態…',
  waiting: '尚未開通，正在等待到帳確認，請勿重複付款。',
  error: '狀態更新失敗，正在重試，請勿重複付款。',
  timeout: '自動檢查已停止。請核對訂單或聯絡客服，不要重複付款。',
  help: '不確定怎麼選？',
}
export function getL0AccessCopy(language: string): Copy {
  const locale = language.toLowerCase().replaceAll('-', '')
  if (['zhtw', 'zhhk', 'zhhant'].includes(locale)) return zhTW
  if (locale.startsWith('zh')) return zhCN
  return en
}
