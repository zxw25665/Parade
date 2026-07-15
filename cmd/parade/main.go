package main

import (
	"fmt"
	"log"
	"os"

	"parade/cmd/parade/daemon"
)

const AppVersion = "v0.2.0-libp2p"

func main() {
	log.SetFlags(0)
	log.SetPrefix("[parade] ")

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "daemon":
		daemon.Run(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println("parade", AppVersion)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Print(`Parade — P2P file sharing & messaging

Usage:
  parade daemon [flags]    Start the Parade daemon (backend engine)
  parade version           Print version and exit
  parade help              Print this help

Daemon Flags:
  --uds <path>             Unix domain socket path (default: /tmp/parade.sock)
  --data-dir <dir>         Data directory for DB, identity, logs (default: $PWD)
  --port <n>               P2P listen port (default: 4327)
  --listen <addr>          P2P listen address (default: 127.0.0.1)
  --headless               Run without UDS listener (for automation/testing)
  --debug                  Debug mode: allow multi-instance, custom listen interface
  --production             Production mode: enforce loopback-only, single-instance lock
  --mdns                   Enable mDNS peer discovery (default: enabled)
  --no-mdns                Disable mDNS peer discovery

Mode Precedence:
  --production  >  --debug  >  (normal)
  --headless is orthogonal and can be combined with any mode.

Examples:
  parade daemon                              # Normal mode, UDS at /tmp/parade.sock
  parade daemon --headless                   # Headless, no UDS, for automated tests
  parade daemon --debug --listen 0.0.0.0     # Debug: listen on all interfaces
  parade daemon --production                 # Production: enforced security
  parade daemon --no-mdns                     # Disable mDNS discovery
  parade daemon --data-dir /tmp/parade-1 --port 4328 --debug   # Multi-instance debug
`)
}
