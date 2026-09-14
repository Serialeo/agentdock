//go:build !windows && (!darwin || !cgo || !agentdock_computer_native)

package computer

import "os"

func platformConfigDir() (string, error) { return os.UserConfigDir() }
