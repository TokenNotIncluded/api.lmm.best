package model

import "errors"

// Existing type IDs remain readable; retired providers cannot be created or
// silently repurposed as a different transport.
var ErrRetiredChannelType = errors.New("OpenHuman channel type has been removed")
