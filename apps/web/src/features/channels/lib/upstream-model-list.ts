/*
Copyright (C) 2026 LIghtJUNction

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
*/
import { CHANNEL_TYPE_TYPESAFE, TYPESAFE_MODELS } from '../constants'
import { normalizeModelName } from './model-mapping-validation'

const typeSafeVersionModels = new Set(
  TYPESAFE_MODELS.filter((model) => /^jev-\d+\.\d+\.\d+$/.test(model))
)

function normalizeModelNames(models: readonly string[]): string[] {
  return [...new Set(models.map(normalizeModelName).filter(Boolean))]
}

export function getModelsMissingFromUpstream({
  selectedModels,
  fetchedModels,
  redirectSourceModels = [],
  channelType,
}: {
  selectedModels: readonly string[]
  fetchedModels: readonly string[]
  redirectSourceModels?: readonly string[]
  channelType?: number
}): string[] {
  const fetched = new Set(normalizeModelNames(fetchedModels))
  const redirectSources = new Set(normalizeModelNames(redirectSourceModels))

  return normalizeModelNames(selectedModels).filter((model) => {
    if (fetched.has(model) || redirectSources.has(model)) return false
    // TypeSafe lists aliases only. A documented version can remain valid even
    // when it is absent from GET /v1/models; do not exempt unknown versions.
    if (
      channelType === CHANNEL_TYPE_TYPESAFE &&
      typeSafeVersionModels.has(model)
    ) {
      return false
    }
    return true
  })
}
