//go:build !windows

package transports

import (
    "fmt"
    "ttmesh/pkg/transport"
)

func newWinPipeTransport() (transport.Transport, error) { return nil, fmt.Errorf("winpipe transport is not supported on this platform") }

