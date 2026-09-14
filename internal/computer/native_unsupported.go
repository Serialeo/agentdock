//go:build !windows && (!darwin || !cgo)

package computer

import "os"
import protocol "github.com/Serialeo/agentdock-protocol"

func NewNativeBackend() (Backend, error) {
	return nil, failure(protocol.ErrorComputerUnsupported, "this helper was built without a native desktop backend")
}

func platformConfigDir() (string, error) { return os.UserConfigDir() }
