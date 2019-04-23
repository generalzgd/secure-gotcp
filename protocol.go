package securegotcp

import (
	"net"
)

type Packet interface {
	Serialize() []byte
}

type Protocol interface {
	ReadPacket(conn net.Conn) (Packet, error)
}
