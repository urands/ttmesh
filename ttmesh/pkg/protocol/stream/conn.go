package stream

import (
    "bufio"
    "io"
    "net"
    "ttmesh/pkg/protocol"
)

// Conn wraps an io.ReadWriter to send/receive protocol.Envelope frames.
type Conn struct {
    rw   io.ReadWriter
    br   *bufio.Reader
    bw   *bufio.Writer
}

func New(rw io.ReadWriter) *Conn {
    return &Conn{rw: rw, br: bufio.NewReader(rw), bw: bufio.NewWriter(rw)}
}

func NewNetConn(c net.Conn) *Conn { return New(c) }

func (c *Conn) Send(e *protocol.Envelope) error {
    _, err := e.WriteTo(c.bw)
    if err != nil { return err }
    return c.bw.Flush()
}

func (c *Conn) Recv(e *protocol.Envelope) error {
    _, err := e.ReadFrom(c.br)
    return err
}

