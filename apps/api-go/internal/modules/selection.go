package modules

import (
	"fmt"
	"strings"
)

// Select resolves an explicit module list. Empty configuration uses only the
// supplied defaults; adding a module does not silently enable it for everyone.
func Select(config string, defaults, available []string) ([]string, error) {
	allowed := make(map[string]bool, len(available))
	for _, name := range available {
		if !validName.MatchString(name) || name == "modules" || allowed[name] {
			return nil, fmt.Errorf("invalid available module: %q", name)
		}
		allowed[name] = true
	}
	config = strings.TrimSpace(config)
	selected := append([]string{}, defaults...)
	if config == "none" {
		return []string{}, nil
	}
	if config != "" {
		selected = strings.Split(config, ",")
	}
	seen := make(map[string]bool, len(selected))
	for i, name := range selected {
		name = strings.TrimSpace(name)
		if !allowed[name] || seen[name] {
			return nil, fmt.Errorf("unknown or duplicate extension module: %q", name)
		}
		selected[i] = name
		seen[name] = true
	}
	return selected, nil
}
