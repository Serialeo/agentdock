//go:build !agentdock_computer_native || !darwin || !cgo

package computer

func RequestNativePermissions() {}

func RunMain(run func() error) error { return run() }
