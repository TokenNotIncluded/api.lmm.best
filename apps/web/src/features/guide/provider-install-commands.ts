/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published
by the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
/*
Copyright (C) 2026 LIghtJUNction
*/
export const PI_INSTALL_LATEST_COMMAND =
  'npm exec --yes --prefer-online --package=@tokennotincluded/pi-lmm-provider@latest -- lmm-pi-provider npm:@tokennotincluded/pi-lmm-provider@latest'

export const DSH_PACKAGE_LATEST = '@tokennotincluded/dsh-lmm-provider@latest'

export const DSH_WEB_INSTALL_PORTABLE_COMMAND =
  `dsh plugin --profile web add ${DSH_PACKAGE_LATEST}`

export const DSH_WEB_INSTALL_LATEST_COMMAND =
  'dsh plugin --profile web add "$(npm view @tokennotincluded/dsh-lmm-provider@latest dist.tarball --prefer-online)" --ignore-scripts'

export const DSH_WEB_INSTALL_LATEST_WINDOWS_COMMAND =
  'dsh.cmd plugin --profile web add "$(npm.cmd view @tokennotincluded/dsh-lmm-provider@latest dist.tarball --prefer-online)" --ignore-scripts'

export const DSH_DOWNLOAD_LATEST_COMMAND = `npm pack ${DSH_PACKAGE_LATEST}`

export const CODEWHALE_INSTALL_COMMAND =
  'git clone https://github.com/TokenNotIncluded/codewhale-lmm-provider.git && cd codewhale-lmm-provider && npm install --global .'
