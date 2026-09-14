package computer

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
)

// HMAC prevents dictionary attacks against short typed secrets in the journal.
func operationKey(home string) ([]byte, error) {
	path := filepath.Join(home, "operation.key")
	key, err := os.ReadFile(path)
	if err == nil {
		if len(key) != 32 {
			return nil, fmt.Errorf("invalid computer operation key")
		}
		return key, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	key = make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	if _, err = f.Write(key); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if err = syncDirectory(home); err != nil {
		return nil, err
	}
	return key, nil
}
