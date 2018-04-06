// This common.go file contains all the functions which serevr and
// client share

package lsp

import (
	// "fmt"
	"container/list"
	"github.com/cmu440/lspnet"
	"time"
)

const MAX = 1024

//client struct
type client struct {
	conn           *lspnet.UDPConn
	addr           *lspnet.UDPAddr
	connID         int
	seqNum         int
	params         *Params
	msg_toSend     *list.List       // pending message to send
	msg_toReSend   *list.List       // used to track unack, handle resend
	msg_toReceive  *list.List       // order message from server --> recvChan
	sendRecvChan   chan *Message    // helper channel to send data and ack
	recvChan       chan *Message    // ordered msg waiting to be read
	pendingMsgQue  chan *Message    // raw message to deal with, from write function
	readHelper     chan *Message    // helper channel in client's read routine
	newclihelper   chan bool        // helper channel to kill goroutine after connection build
	toClose        chan bool        // when close called, set true for this channel
	closeDone      chan bool        // receive signal indicate that all pending msg handle done
	globalCntReset chan bool        // helper channel to reset global count
	globalCntGo    chan bool        // helper channel to reset global count
	isstopConn     chan bool        // helper channel to kill readroutine
	stopConn       chan int         // close readroutine
	unackMsg       map[int]int      // tracking packages waiting for ACK
	recvMap        map[int]*Message // map to store all msg received, help to sort in order
	sWtop          int              // first unack
	header         int              // first msg in read list
	globalCnt      int              // total number of epoch with no msg happens, compared with epochlimit
	noMsgRecvEpoch bool             // flag to check whether recieving msg during one epoch
	isLost         bool             // flag to check if connection lost
	isClose        bool             // signal to indicate that close function has been called
	epochTimer     *time.Timer      // global timer to handle epoch event
	stopReadConn   chan bool
}

//the wrapped msg to be resend
type wrapped_msg struct {
	message        *Message
	currentBackoff int // compared with maxbackoffinterval
	expectEpoch    int // expected epoch number to resend
}

// create a client
func initClient(conn *lspnet.UDPConn, addr *lspnet.UDPAddr, params *Params) *client {
	cli := &client{
		conn:           conn,
		connID:         0,
		seqNum:         0,
		header:         1,
		sWtop:          1,
		addr:           addr,
		params:         params,
		msg_toSend:     list.New(),
		msg_toReSend:   list.New(),
		msg_toReceive:  list.New(),
		sendRecvChan:   make(chan *Message), //raw message from server
		pendingMsgQue:  make(chan *Message), //message to send to server
		recvChan:       make(chan *Message), //for Read
		readHelper:     make(chan *Message),
		toClose:        make(chan bool), // signal to prepare for close
		closeDone:      make(chan bool), // signal to indicate all pending packages and ack sent
		newclihelper:   make(chan bool),
		globalCntReset: make(chan bool, 1),
		globalCntGo:    make(chan bool, 1),
		isstopConn:     make(chan bool, 1),
		stopConn:       make(chan int),
		unackMsg:       make(map[int]int),
		recvMap:        make(map[int]*Message),
		globalCnt:      0,
		noMsgRecvEpoch: true,
		isLost:         false,
		isClose:        false,
		epochTimer:     time.NewTimer(0),
		stopReadConn:   make(chan bool, 1),
	}
	return cli
}

//to create a wrapped_msg instance
func NewWrap(msg *Message) *wrapped_msg {
	wrappedmsg := &wrapped_msg{
		message:        msg,
		currentBackoff: 0,
		expectEpoch:    0,
	}
	return wrappedmsg
}

// both client and server may use this main Routine to deal with message(read and write)
func (c *client) mainRoutine(recvChan chan *Message, stopConn chan int,
	sendMessage func(*client, *Message) error) {
	// begin a global epoch timer
	c.epochTimer.Reset(time.Millisecond * time.Duration(c.params.EpochMillis))
	for {
		if c.msg_toReceive.Len() != 0 { // has pending message waiting in list to be read
			msg := c.msg_toReceive.Front().Value.(*Message)
			if c.isLost {
				// fmt.Println("is lost 128")
				select {
				case recvChan <- msg:
					c.msg_toReceive.Remove(c.msg_toReceive.Front())
				}
			} else {
				select {
				case recvChan <- msg:
					c.msg_toReceive.Remove(c.msg_toReceive.Front())
				case <-c.toClose:
					// fmt.Println("is lost 144")
					// LOGF.Println("call close 144")
					c.isClose = true
					if c.CheckState() {
						stopConn <- c.connID
						return
					}
				case <-c.epochTimer.C:
					c.handleEpochevent(sendMessage)
				case msg := <-c.sendRecvChan:
					if msg == nil {
					}
					if c.handleRecvMsg(msg, sendMessage) {
						stopConn <- c.connID
						return
					}
				case msg := <-c.pendingMsgQue:
					if msg == nil {
					}
					c.msg_toSend.PushBack(msg)
					c.handlePendingSendMsg(sendMessage)
				}
			}
		} else {
			// fmt.Println("list len == 0")
			if c.isLost { // if lost, break the connection
				// fmt.Println("is lost 173")
				stopConn <- c.connID
				// fmt.Println("return 175")
				return
			} else {
				select {
				case <-c.toClose:
					// fmt.Println("is lost 180")
					c.isClose = true
					if c.CheckState() {
						stopConn <- c.connID
						return
					}
				case <-c.epochTimer.C:
					c.handleEpochevent(sendMessage)
				case msg := <-c.sendRecvChan:
					if c.handleRecvMsg(msg, sendMessage) {
						stopConn <- c.connID
						return
					}
				case msg := <-c.pendingMsgQue:
					c.msg_toSend.PushBack(msg)
					c.handlePendingSendMsg(sendMessage)
				}
			}
		}
	}
}

// function to handle epoch event: 1. exceed the epochlimit. 2. send ack0 3. resend
// message that not has ack, follow the exponential principle
func (c *client) handleEpochevent(sendMessage func(*client, *Message) error) {
	// total cnt exceed limit, disconnect
	if c.noMsgRecvEpoch {
		c.globalCnt++
		ack0 := NewAck(c.connID, 0)
		sendMessage(c, ack0)
	}
	if c.globalCnt >= c.params.EpochLimit {
		// fmt.Println("exceed epoch limit")
		// LOGF.Println("exceed epoch limit")
		// declare connection lose
		c.isLost = true
		return
	}
	// resend unack msg
	for e := c.msg_toReSend.Front(); e != nil; e = e.Next() {
		wrapmsg := e.Value.(*wrapped_msg)
		if wrapmsg.expectEpoch == 0 {
			sendMessage(c, wrapmsg.message)
			if wrapmsg.currentBackoff == 0 {
				wrapmsg.currentBackoff = 1
			} else {
				wrapmsg.currentBackoff *= 2
			}
			if wrapmsg.currentBackoff >= c.params.MaxBackOffInterval {
				wrapmsg.currentBackoff = c.params.MaxBackOffInterval
			}
			wrapmsg.expectEpoch = wrapmsg.currentBackoff
		} else {
			wrapmsg.expectEpoch--
		}
	}
	c.epochTimer.Reset(time.Millisecond * time.Duration(c.params.EpochMillis))
	c.noMsgRecvEpoch = true
}

// handle received message
// if ack move sliding window to send more data
// if data, sort it to read in order
func (c *client) handleRecvMsg(msg *Message, sendMessage func(*client, *Message) error) bool {
	c.globalCnt = 0
	// c.noMsgRecvEpoch = false
	if msg.Type == MsgAck {
		_, ok := c.unackMsg[msg.SeqNum]
		if ok {
			for e := c.msg_toReSend.Front(); e != nil; e = e.Next() {
				wrappedmsg := e.Value.(*wrapped_msg)
				if wrappedmsg.message.SeqNum == msg.SeqNum {
					c.msg_toReSend.Remove(e)
					break
				}
			}
			c.unackMsg[msg.SeqNum] = -1
			if msg.SeqNum == c.sWtop {
				item := c.unackMsg[c.sWtop]
				for item == -1 {
					c.sWtop++
					item = c.unackMsg[c.sWtop]
				}
			}
			c.handlePendingSendMsg(sendMessage)
		}
	}
	if msg.Type == MsgData {
		c.noMsgRecvEpoch = false
		_, ok := c.recvMap[msg.SeqNum]
		if !ok && msg.SeqNum >= c.header {
			c.recvMap[msg.SeqNum] = msg
			for i := c.header; ; i++ {
				if _, ok := c.recvMap[i]; !ok {
					break
				}
				c.header++
				c.msg_toReceive.PushBack(c.recvMap[i])
				delete(c.recvMap, i)
			}
		}
	}
	if c.isClose {
		if c.CheckState() {
			return true
		}
	}
	return false
}

//check if all pending message are sent
func (c *client) CheckState() bool {
	if c.msg_toReSend.Len() == 0 && c.msg_toSend.Len() == 0 && c.msg_toReceive.Len() == 0 && c.CheckMap() {
		return true
	}
	return false
}

// checkthe unackMsgMap
func (c *client) CheckMap() bool {
	cnt := 0
	for _, flag := range c.unackMsg {
		if flag != -1 {
			cnt++
		}
	}
	if cnt == 0 {
		return true
	} else {
		return false
	}
}

//to send data within slide window size
func (c *client) handlePendingSendMsg(sendMessage func(*client, *Message) error) {
	e := c.msg_toSend.Front()
	for e != nil {
		msg := e.Value.(*Message)
		if msg.SeqNum < c.sWtop+c.params.WindowSize {
			sendMessage(c, msg)
			wrappmsg := NewWrap(msg)
			c.msg_toReSend.PushBack(wrappmsg)
			c.msg_toSend.Remove(e)
			e = c.msg_toSend.Front()
			c.unackMsg[msg.SeqNum] = 1
		} else {
			e = e.Next()
			continue
		}
	}
}
