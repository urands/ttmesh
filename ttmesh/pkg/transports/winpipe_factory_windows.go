//go:build windows

package transports

import (
    "ttmesh/pkg/transport"
    "ttmesh/pkg/transport/winpipe"
)

func newWinPipeTransport() (transport.Transport, error) { return winpipe.New(), nil }

