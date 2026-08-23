/*
Copyright (C) 2025 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/

import {
  JsonCodeEditor,
  type JsonCodeEditorProps,
} from '@/components/json-code-editor'
import {
  getSystemJsonConfiguration,
  type SystemJsonConfigurationKey,
} from '@/features/system-settings/components/system-json-configurations'

type SystemJsonCodeEditorProps = Omit<
  JsonCodeEditorProps,
  'example' | 'specification'
> & {
  configurationKey: SystemJsonConfigurationKey
}

/**
 * Binds a settings JSON editor to the reviewed example and field contract.
 * Requiring a registry key prevents settings forms from shipping an
 * undocumented JSON input or drifting away from the raw JSON editor.
 */
export function SystemJsonCodeEditor({
  configurationKey,
  ...props
}: SystemJsonCodeEditorProps) {
  const configuration = getSystemJsonConfiguration(configurationKey)

  return (
    <JsonCodeEditor
      {...props}
      example={configuration.example}
      specification={configuration.specification}
    />
  )
}
