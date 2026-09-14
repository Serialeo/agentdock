//go:build !agentdock_computer_native || (!windows && (!darwin || !cgo))

package computer

import protocol "github.com/Serialeo/agentdock-protocol"

func NewNativeBackend() (Backend, error) {
	return nil, failure(protocol.ErrorComputerUnsupported, "this helper was built without a native desktop backend")
}
