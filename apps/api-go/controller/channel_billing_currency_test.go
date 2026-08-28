/*
Copyright (C) 2023-2026 QuantumNous

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

For commercial licensing, please contact support@quantumnous.com
*/
package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConvertCNYBalanceToUSDUsesSynchronizedRate(t *testing.T) {
	usd, err := convertCNYBalanceToUSD(6.8, 6.8)
	require.NoError(t, err)
	require.InDelta(t, 1, usd, 1e-12)
}

func TestConvertCNYBalanceToUSDRejectsInvalidRate(t *testing.T) {
	_, err := convertCNYBalanceToUSD(6.8, 0)
	require.Error(t, err)
}
