package deploycli

import (
	"encoding/json"
	"errors"
	"path/filepath"
)

const merchantStoreSocketDirectory = "/run/lmm-api-merchant-fences"

// Owner receipts stay in their persistent capsule/workspace. Only the Unix
// transport uses /run: losing it on reboot never removes or releases ACTIVE.
// Hash the full canonical owner (authority, host, nonce and actual generations)
// plus its complete persistent root, rather than truncating an identity/path.
func (runtime *productionRuntime) merchantStoreSocketPath(root string, owner productionMerchantStoreFenceOwner, create bool) (string, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == "/" {
		return "", errors.New("merchant socket authority root is not canonical")
	}
	directory := runtime.merchantStoreSocketDirectory
	if directory == "" {
		directory = merchantStoreSocketDirectory
	}
	body, err := canonicalMerchantStoreFenceOwner(owner)
	if err != nil {
		return "", err
	}
	authority, err := json.Marshal(struct {
		Root  string
		Owner json.RawMessage
	}{root, body})
	if err != nil {
		return "", err
	}
	path := filepath.Join(directory, startupContentSHA256(authority)+".sock")
	// Linux sockaddr_un has 108 bytes including the trailing NUL.
	if len(path) >= 108 {
		return "", errors.New("merchant holder Unix socket path is too long")
	}
	if err := verifyMerchantStoreSocketDirectory(directory, runtime.requiredOwnerUID, create); err != nil {
		return "", err
	}
	return path, nil
}
