//go:build !windows

package netstack

import (
    "fmt"
    "ttmesh/pkg/transport"
)

func newWinPipeTransport() (transport.Transport, error) { return nil, fmt.Errorf("winpipe transport is not supported on this platform") }

