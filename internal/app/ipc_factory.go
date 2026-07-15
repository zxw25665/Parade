//go:build !windows

package app

func NewIPCServer(path string) IPCServer {
	return NewUDSServer(path)
}

func GetDefaultPipePath() string {
	return "/tmp/parade.sock"
}
