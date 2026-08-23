package minimax

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsRemoteAudioURL(t *testing.T) {
	assert.True(t, isRemoteAudioURL("https://cdn.example.com/audio.mp3"))
	assert.True(t, isRemoteAudioURL("http://cdn.example.com/audio.mp3"))
	assert.False(t, isRemoteAudioURL("httpfoo"))
	assert.False(t, isRemoteAudioURL("javascript:alert(1)"))
	assert.False(t, isRemoteAudioURL("deadbeefcafebabe"))
	assert.False(t, isRemoteAudioURL(""))
}
