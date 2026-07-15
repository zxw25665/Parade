//go:build windows

package app

func NewIPCServer(path string) IPCServer {
	return NewNPipeServer(path)
}

func GetDefaultPipePath() string {
	return "parade"
}
