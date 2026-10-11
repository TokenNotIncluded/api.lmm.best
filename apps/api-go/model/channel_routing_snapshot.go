package model

// cloneRoutingMapWithoutID removes one ID and reuses unchanged channel lists. Published
// lists must remain read-only; callers still hold channelSyncLock when publishing.
// Both map levels and every changed list are independent of the source. The
// original eager copier remains available through cloneRoutingMapWithout.
func cloneRoutingMapWithoutID(source map[string]map[string][]int, removedID int) map[string]map[string][]int {
	cloned := make(map[string]map[string][]int, len(source))
	for group, models := range source {
		clonedModels := make(map[string][]int, len(models))
		for model, channels := range models {
			first := -1
			for i, channelID := range channels {
				if channelID == removedID {
					first = i
					break
				}
			}
			if first < 0 {
				if channels == nil {
					clonedModels[model] = []int{} // Preserve the eager copy's nil handling.
				} else {
					// Appending must not overwrite an unused part of the source array.
					clonedModels[model] = channels[:len(channels):len(channels)]
				}
				continue
			}
			next := first + 1
			if first == 0 {
				// Skip a removed prefix before allocating. An entirely removed
				// list needs an empty result, not an unused backing array.
				for next < len(channels) {
					if channels[next] != removedID {
						break
					}
					next++
				}
			}
			filtered := make([]int, first, first+len(channels)-next)
			copy(filtered, channels[:first])
			for _, channelID := range channels[next:] {
				if channelID != removedID {
					filtered = append(filtered, channelID)
				}
			}
			clonedModels[model] = filtered
		}
		cloned[group] = clonedModels
	}
	return cloned
}
