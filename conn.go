package gotcp

import (
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
	"path/filepath"
	"runtime/debug"
	"svr-frame/libs/tool"
)

// Error type
var (
	ErrConnClosing   = errors.New("use of closed network connection")
	ErrWriteBlocking = errors.New("write packet was blocking")
	ErrReadBlocking  = errors.New("read packet was blocking")
)

// Conn exposes a set of callbacks for the various events that occur on a connection
type Conn struct {
	srv               *Server
	conn              net.Conn  // the raw connection
	extraData         interface{}   // to save extra data
	closeOnce         sync.Once     // close the conn, once, per instance
	closeFlag         int32         // close flag
	closeChan         chan struct{} // close chanel
	packetSendChan    chan Packet   // packet send chanel
	packetReceiveChan chan Packet   // packeet receive chanel
}

// ConnCallback is an interface of methods that are used as callbacks on a connection
type ConnCallback interface {
	// OnConnect is called when the connection was accepted,
	// If the return value of false is closed
	OnConnect(*Conn) bool

	// OnMessage is called when the connection receives a packet,
	// If the return value of false is closed
	OnMessage(*Conn, Packet) bool

	// OnClose is called when the connection closed
	OnClose(*Conn)
}

// newConn returns a wrapper of raw conn
func newConn(conn net.Conn, srv *Server) *Conn {
	return &Conn{
		srv:               srv,
		conn:              conn,
		closeChan:         make(chan struct{}),
		packetSendChan:    make(chan Packet, srv.config.PacketSendChanLimit),
		packetReceiveChan: make(chan Packet, srv.config.PacketReceiveChanLimit),
	}
}

// GetExtraData gets the extra data from the Conn
func (c *Conn) GetExtraData() interface{} {
	return c.extraData
}

// PutExtraData puts the extra data with the Conn
func (c *Conn) PutExtraData(data interface{}) {
	c.extraData = data
}

// GetRawConn returns the raw net.TCPConn from the Conn
func (c *Conn) GetRawConn() net.Conn {
	return c.conn
}

// Close closes the connection
func (c *Conn) Close() {
	c.closeOnce.Do(func() {
		atomic.StoreInt32(&c.closeFlag, 1)
		close(c.closeChan)
		close(c.packetSendChan)
		close(c.packetReceiveChan)
		c.conn.Close()
		c.srv.callback.OnClose(c)
	})
}

// IsClosed indicates whether or not the connection is closed
func (c *Conn) IsClosed() bool {
	return atomic.LoadInt32(&c.closeFlag) == 1
}

// AsyncWritePacket async writes a packet, this method will never block
func (c *Conn) AsyncWritePacket(p Packet, timeout time.Duration) (err error) {
	if c.IsClosed() {
		return ErrConnClosing
	}

	defer func() {
		if e := recover(); e != nil {
			writeLog(fmt.Sprintf("defer AsyncWritePacket, err:%v, connid:%v", e, c.GetExtraData()))
			err = ErrConnClosing
		}
	}()

	if timeout == 0 {
		select {
		case c.packetSendChan <- p:
			return nil

		default:
			writeLog(fmt.Sprintf("defer AsyncWritePacket, err:%v, connid:%v", ErrWriteBlocking, c.GetExtraData()))
			return ErrWriteBlocking
		}

	} else {
		select {
		case c.packetSendChan <- p:
			return nil

		case <-c.closeChan:
			return ErrConnClosing

		case <-time.After(timeout):
			return ErrWriteBlocking
		}
	}
}

// Do it
func (c *Conn) Do() {
	if !c.srv.callback.OnConnect(c) {
		return
	}

	asyncDo(c.handleLoop, c.srv.waitGroup)
	asyncDo(c.readLoop, c.srv.waitGroup)
	asyncDo(c.writeLoop, c.srv.waitGroup)
}

func (c *Conn) readLoop() {
	addr := c.conn.RemoteAddr().String()
	defer func() {
		//recover()
		if r := recover(); r != nil {
			writeLog(fmt.Sprintf("defer readLoop, panic:%v, connid:%v, addr:%s, stack:%s", r, c.GetExtraData(), addr, string(debug.Stack())))
		} else {
			writeLog(fmt.Sprintf("defer readLoop, connid:%v, addr:%s", c.GetExtraData(), addr))
		}
		c.Close()
	}()

	for {
		select {
		case <-c.srv.exitChan:
			return

		case <-c.closeChan:
			return

		default:
		}

		p, err := c.srv.protocol.ReadPacket(c.conn)
		if err != nil {
			writeLog(fmt.Sprintf("defer readLoop ReadPacket, err:%v, connid:%v, addr:%s", err, c.GetExtraData(), addr))
			return
		}

		c.packetReceiveChan <- p
	}
}

func (c *Conn) writeLoop() {
	addr := c.conn.RemoteAddr().String()
	defer func() {
		//recover()
		if r := recover(); r != nil {
			writeLog(fmt.Sprintf("defer writeLoop, err:%v, connid:%v, addr:%s, stack:%s", r, c.GetExtraData(), addr, string(debug.Stack())))
		} else {
			writeLog(fmt.Sprintf("defer writeLoop, connid:%v, addr:%s", c.GetExtraData(), addr))
		}
		c.Close()
	}()

	for {
		select {
		case <-c.srv.exitChan:
			return

		case <-c.closeChan:
			return

		case p := <-c.packetSendChan:
			if c.IsClosed() {
				return
			}
			if _, err := c.conn.Write(p.Serialize()); err != nil {
				writeLog(fmt.Sprintf("defer writeLoop Write, err:%v, connid:%v, addr:%s", err, c.GetExtraData(), addr))
				return
			}
		}
	}
}

func (c *Conn) handleLoop() {
	addr := c.conn.RemoteAddr().String()
	defer func() {
		//recover()
		if r := recover(); r != nil {
			writeLog(fmt.Sprintf("defer handleLoop, err:%v, connid:%v, addr:%s, stack:%s", r, c.GetExtraData(), addr, string(debug.Stack())))
		} else {
			writeLog(fmt.Sprintf("defer handleLoop, connid:%v, addr:%s", c.GetExtraData(), addr))
		}
		c.Close()
	}()

	for {
		select {
		case <-c.srv.exitChan:
			return

		case <-c.closeChan:
			return

		case p := <-c.packetReceiveChan:
			if c.IsClosed() {
				return
			}
			if !c.srv.callback.OnMessage(c, p) {
				return
			}
		}
	}
}

func asyncDo(fn func(), wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		fn()
		wg.Done()
	}()
}



var logDir = "./"
/*
* 只能设置执行文件所在目录下的一级文件夹
*/
func SetLogDir(dir string) {
	logDir = filepath.Join(filepath.Dir(os.Args[0]), dir)

	if _, err := os.Stat(logDir); os.IsNotExist(err) {
		os.MkdirAll(logDir, os.ModePerm)
		fmt.Println("Dir created: ", logDir)
	}
}

func init() {
	logDir = filepath.Dir(os.Args[0])
}

func writeLog(line string) {
	path := filepath.Join(logDir, "err.txt")
	tool.WriteLog(path, line)
}
