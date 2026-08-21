package appcli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

const productionPackagedFrontendIndex = "usr/share/lmm-api-go/frontend-dist/index.html"

func (runtime *productionRuntime) candidateFrontendIndexSHA256(ctx context.Context, packagePath string) (string, error) {
	contents, err := runtime.runner.Run(ctx, productionCommand{Name: commandBsdtar, Args: []string{"-xOf", packagePath, productionPackagedFrontendIndex},})
	if err != nil {
		return "", fmt.Errorf("read candidate frontend index: %w", err)
	}
	if len(contents) == 0 {
		return "", errors.New("candidate frontend index is empty")
	}
	digest := sha256.Sum256(contents)
	return hex.EncodeToString(digest[:]), nil
}
