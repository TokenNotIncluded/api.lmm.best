#!/usr/bin/env node
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
import {
  EVIDENCE_SCHEMA_VERSION,
  readAcceptanceBaselineFile,
  readCredentialsFromEnvironment,
  runProductionAcceptance,
  SecretRedactor,
  serializeAcceptanceEvidence,
} from './production-acceptance-lib.mjs'

const redactor = new SecretRedactor()

async function main() {
  const args = process.argv.slice(2)
  const mode = args.shift()
  if (mode !== 'baseline' && mode !== 'verify') {
    throw new Error(
      'usage: production-acceptance.mjs baseline|verify [options]'
    )
  }
  const values = new Map()
  const allowed = new Set([
    '--deployment-id',
    '--backend-revision',
    '--frontend-release',
    '--frontend-digest',
    '--deadline-epoch',
    '--cleanup-deadline-epoch',
    '--baseline-file',
  ])
  while (args.length > 0) {
    const key = args.shift()
    if (!allowed.has(key) || values.has(key) || args.length === 0) {
      throw new Error('invalid, duplicate, or incomplete command option')
    }
    values.set(key, args.shift())
  }
  for (const key of [
    '--deployment-id',
    '--backend-revision',
    '--frontend-release',
    '--frontend-digest',
    '--deadline-epoch',
    '--cleanup-deadline-epoch',
  ]) {
    if (!values.has(key)) throw new Error(`required option is missing: ${key}`)
  }
  if (mode === 'baseline' && values.has('--baseline-file')) {
    throw new Error('--baseline-file is valid only in verify mode')
  }
  if (mode === 'verify' && !values.has('--baseline-file')) {
    throw new Error('verify mode requires --baseline-file')
  }
  const deadlineEpoch = Number(values.get('--deadline-epoch'))
  const cleanupDeadlineEpoch = Number(values.get('--cleanup-deadline-epoch'))
  if (
    !Number.isSafeInteger(deadlineEpoch) ||
    !Number.isSafeInteger(cleanupDeadlineEpoch)
  ) {
    throw new Error('deadline epochs must be integer seconds')
  }
  let baseline
  if (mode === 'verify') {
    baseline = await readAcceptanceBaselineFile(values.get('--baseline-file'))
  }
  const credentials = await readCredentialsFromEnvironment()
  redactor.add(credentials.username)
  redactor.add(credentials.password)
  redactor.add(credentials.totp_code)
  return runProductionAcceptance({
    credentials,
    mode,
    baseline,
    bindings: {
      deployment_id: values.get('--deployment-id'),
      backend_revision: values.get('--backend-revision'),
      frontend_release: values.get('--frontend-release'),
      frontend_digest: values.get('--frontend-digest'),
      deadline_epoch: deadlineEpoch,
      cleanup_deadline_epoch: cleanupDeadlineEpoch,
    },
    deadlineEpochMs: deadlineEpoch * 1000,
    cleanupDeadlineEpochMs: cleanupDeadlineEpoch * 1000,
  })
}

try {
  const summary = await main()
  process.stdout.write(serializeAcceptanceEvidence(summary))
  if (!summary.success) process.exitCode = 1
} catch (error) {
  const summary = {
    schema_version: EVIDENCE_SCHEMA_VERSION,
    mode: process.argv[2] ?? null,
    target: 'https://api.lmm.best',
    bindings: null,
    success: false,
    checks: {},
    channels: [],
    failures: [
      {
        stage: 'startup',
        code: 'STARTUP_FAILURE',
        detail: redactor.text(error?.message ?? 'startup failed'),
      },
    ],
    cleanup: {
      attempts: {
        token_delete: false,
        test_user_logout: false,
        user_delete: false,
        root_logout: false,
      },
      token_deleted: false,
      user_deleted: false,
      retained_test_identity: null,
      retained_token: null,
    },
  }
  process.stdout.write(serializeAcceptanceEvidence(summary))
  process.exitCode = 1
}
