//go:build windows

package credittransition

import "errors"

func readSealedFile(string) ([]byte, error) {
	return nil, errors.New("sealed credit preparation is supported only on Unix")
}
