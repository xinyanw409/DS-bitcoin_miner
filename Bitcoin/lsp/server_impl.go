// Contains the implementation of a LSP server. Some core functions
// are in common.go. In this file we mainly implement the newclient function,
// close function and read/write function.

package lsp

import (
	"encoding/json"
	"errors"
	// "fmt"
	"github.com/cmu440/lspnet"
	"strconv"
)

//wraped data for read
type readMsg struct {
	message *Message
	udpaddr *lspnet.UDPAddr
}

//wraped data for clientMap
type mapAccess struct {
	cli *client
	ok  bool
}

//wraped data for Write function
type writeData struct {
	connID  int
	payload []byte
}

// server struct
type server struct {
	// basic
	params       *Params
	clientMap    map[int]*client //id to client
	clientsIdMap map[string]int  //what to clientID
	newClientId  int
	conn         *lspnet.UDPConn
	// receive
	recvMsgChan chan *Message //read by Read
	// close
	tocloseClient chan int  // signal to close a exact client
	tocloseAll    chan bool // signal to close all clients
	isClose       bool      // signal to help close
	isCloseDown   chan bool // helper channel to indicate all prepare for close done
	losterror     chan int  // helper channel to indicate Read Lost Error
	closeDown     chan bool // helper channel to close

	//helper chan
	readHelpChan           chan *readMsg   // helper channel to read
	closeCliMapChan        chan bool       //send signal to get access to clientMap to delete all clients
	finishMapAccessChan    chan bool       //send signal when finish delete all clients in clientMap
	closeConnCliMapChan    chan int        //send signal to delete single client in clientMap
	closeConnMapAccessChan chan *mapAccess //sned signal back when finsih delete single client in clientMap
	// write helper channel
	writeDataChan  chan *writeData //write data chan
	writeErrorChan chan bool       //if write error send signal
}

// NewServer creates, initiates, and returns a new server. This function should
// NOT block. Instead, it should spawn one or more goroutines (to handle things
// like accepting incoming client connections, triggering epoch events at
// fixed intervals, synchronizing events using a for-select loop like you saw in
// project 0, etc.) and immediately return. It should return a non-nil error if
// there was an error resolving or listening on the specified port number.
func NewServer(port int, params *Params) (Server, error) {
	addr, err1 := lspnet.ResolveUDPAddr("udp", lspnet.JoinHostPort("localhost", strconv.Itoa(port)))
	if err1 != nil {
		return nil, err1
	}
	conn, err2 := lspnet.ListenUDP("udp", addr)
	if err2 != nil {
		return nil, err2
	}
	s := &server{
		params:       params,
		clientMap:    make(map[int]*client), //id to client
		clientsIdMap: make(map[string]int),  //addr to clientID
		newClientId:  1,
		conn:         conn,
		// receive
		recvMsgChan:  make(chan *Message, 1), //read by Read
		readHelpChan: make(chan *readMsg, 1),
		// close
		tocloseClient: make(chan int, 1),
		tocloseAll:    make(chan bool, 1),
		closeDown:     make(chan bool, 1),
		losterror:     make(chan int, 1),
		isCloseDown:   make(chan bool, 1),
		isClose:       false,
		//help chan
		closeCliMapChan:        make(chan bool, 1),
		finishMapAccessChan:    make(chan bool, 1),
		closeConnCliMapChan:    make(chan int, 1),
		closeConnMapAccessChan: make(chan *mapAccess, 1),
		// write
		writeDataChan:  make(chan *writeData, 1),
		writeErrorChan: make(chan bool, 1),
	}
	go s.readRoutine()
	return s, nil
}

// User read, if called, pending until a msg prepared to be read
func (s *server) Read() (int, []byte, error) {
	select {
	case id := <-s.losterror:
		return id, nil, errors.New("some client has been lost")
	case message := <-s.recvMsgChan:
		return message.ConnID, message.Payload, nil
	}
}

// User use this function write message to client
func (s *server) Write(connID int, payload []byte) error {
	s.writeDataChan <- &writeData{
		connID:  connID,
		payload: payload,
	}
	select {
	case <-s.writeErrorChan:
		return errors.New("fail write , the conn has been closed")
	default:
		return nil
	}
}

// Function to close a exact client
func (s *server) CloseConn(connID int) error {
	s.closeConnCliMapChan <- connID
	cliInfo := <-s.closeConnMapAccessChan
	ok := cliInfo.ok
	if !ok {
		return errors.New("close a closed client")
	}
	c := cliInfo.cli

	c.toClose <- true
	return nil
}

// Function to close all clients
func (s *server) Close() error {
	s.closeCliMapChan <- true
	<-s.finishMapAccessChan

	s.tocloseAll <- true
	<-s.closeDown
	s.conn.Close()
	return nil
}

// Helper routine to read routine
func (s *server) keepRead() {
	for {
		select {
		case <-s.isCloseDown:
			return
		default:
			recvByte := make([]byte, MAX)
			message, udpAddr, err := s.serverResvMsg(recvByte)

			if err != nil {
				continue
			}
			s.readHelpChan <- &readMsg{
				message: message,
				udpaddr: udpAddr,
			}
		}
	}
}

// Read routine to handle cases (mainroutine in server)
func (s *server) readRoutine() {
	//go a help read function to keep reading from client
	go s.keepRead()
	for {
		select {
		// some case for close
		case connID := <-s.closeConnCliMapChan:
			c, ok := s.clientMap[connID]
			mapInfo := &mapAccess{
				cli: c,
				ok:  ok,
			}
			s.closeConnMapAccessChan <- mapInfo

		case <-s.closeCliMapChan:
			s.isClose = true
			for _, c := range s.clientMap {
				c.toClose <- true
			}
			s.finishMapAccessChan <- true

		case connID := <-s.tocloseClient:
			addr := s.clientMap[connID].addr
			flag := s.clientMap[connID].isLost
			if flag {
				s.losterror <- connID
			}
			delete(s.clientsIdMap, addr.String())
			delete(s.clientMap, connID)
			if s.isClose && len(s.clientMap) == 0 {
				s.closeDown <- true
				s.isCloseDown <- true
				return
			}
		case <-s.tocloseAll:
			if len(s.clientMap) == 0 {
				s.closeDown <- true
				s.isCloseDown <- true
				return
			}
			// else {
			// 	 fmt.Println("close all fail")
			// }

		//case Write, avoid race condition of clientMap and seqNum
		case writedata := <-s.writeDataChan:
			c, ok := s.clientMap[writedata.connID]
			if !ok {
				s.writeErrorChan <- true
				// fmt.Println("error write")
				continue
			}
			payload := writedata.payload
			c.seqNum++
			message := NewData(c.connID, c.seqNum, len(payload), payload)
			c.pendingMsgQue <- message

		//read messages
		case readmsg := <-s.readHelpChan:
			message := readmsg.message
			udpAddr := readmsg.udpaddr
			if message.Type == MsgConnect {
				if s.isClose {
					continue
				}
				clientId, ok := s.clientsIdMap[udpAddr.String()]
				if !ok {
					clientId = s.newClientId
					s.clientsIdMap[udpAddr.String()] = clientId
					//common go lack parameter
					c := initClient(s.conn, udpAddr, s.params)
					c.connID = clientId
					s.clientMap[clientId] = c
					s.newClientId += 1
					//lack parameter  s.recvmegchan s.serversendmsg s.tocloseclient
					go c.mainRoutine(s.recvMsgChan, s.tocloseClient, serverSendMsg)
					newAck := NewAck(clientId, 0)
					serverSendMsg(c, newAck)
				} else {
					//receive duplicate connect and drop it
					c := s.clientMap[clientId]
					newAck := NewAck(clientId, 0)
					serverSendMsg(c, newAck)
				}

			} else {
				c, ok := s.clientMap[message.ConnID]
				// just drop the unknow connid
				if !ok {
					continue
				}
				if message.Type == MsgData {
					newAck := NewAck(message.ConnID, message.SeqNum)
					err := serverSendMsg(c, newAck)
					if err != nil {
						// fmt.Println("server send error")
					}
					// deal with message
					c.sendRecvChan <- message
				} else if message.Type == MsgAck {
					c.sendRecvChan <- message
				}
			}
		}

	}
}

// Helper function to send message to client, doing marshal
func serverSendMsg(c *client, message *Message) error {
	b, err1 := json.Marshal(message)
	if err1 != nil {
		return errors.New("fail marshalling")
	}
	_, err2 := c.conn.WriteToUDP(b, c.addr)
	if err2 != nil {
		return errors.New("fail to send back to client")
	}
	return nil
}

// Helper function to receive message from client, doing unmarshal
func (s *server) serverResvMsg(b []byte) (*Message, *lspnet.UDPAddr, error) {
	size, udpaddr, err1 := s.conn.ReadFromUDP(b)
	if err1 != nil {
		return nil, nil, err1
	}
	var message Message
	err2 := json.Unmarshal(b[:size], &message)
	if err2 != nil {
		return nil, nil, err2
	}
	if len(message.Payload) < message.Size {
		return nil, nil, errors.New("size too small")
	}
	if message.Size < len(message.Payload) {
		message.Payload = message.Payload[:message.Size]
	}
	return &message, udpaddr, nil
}
