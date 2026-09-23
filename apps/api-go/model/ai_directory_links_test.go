/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateAIDirectoryLinksRejectsUnsafeAndDuplicateDestinations(t *testing.T) {
	valid := `[{"id":"chatgpt","name":"ChatGPT","url":"https://chatgpt.com","category":"chat","summary":"AI assistant","description":"Details","enabled":true}]`
	require.NoError(t, ValidateAIDirectoryLinks(valid))
	require.NoError(t, ValidateAIDirectoryLinks(`[]`))
	assert.Error(t, ValidateAIDirectoryLinks(`null`))
	assert.Error(t, ValidateAIDirectoryLinks(`{"links":[]}`))
	assert.Error(t, ValidateAIDirectoryLinks(`[{"id":"bad","name":"Bad","url":"javascript:alert(1)","category":"other","enabled":true}]`))
	assert.Error(t, ValidateAIDirectoryLinks(`[{"id":"bad","name":"Bad","url":"https://user:pass@example.com","category":"other","enabled":true}]`))
	assert.Error(t, ValidateAIDirectoryLinks(`[{"id":"bad","name":"Bad","url":"https://example.com","category":"other","summary":"","description":""}]`))
	assert.Error(t, ValidateAIDirectoryLinks(`[`+valid[1:len(valid)-1]+`,`+valid[1:len(valid)-1]+`]`))
}
