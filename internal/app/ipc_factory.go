package app

func NewIPCServer() IPCServer {
	return NewStdioServer()
}
