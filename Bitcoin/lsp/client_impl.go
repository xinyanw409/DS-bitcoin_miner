// This file contains the implementation of a LSP client. Some core functions
// are in common.go. In this file we mainly implement the newclient function,
// close function and read/write function.

package lsp

import (
	// "fmt"
	"encoding/json"
	"errors"
	"github.com/cmu440/lspnet"
	"time"
)

// Create a new client and send a connect request to server
// begin a main routine and a read routine here
// have not consider connect fail
func NewClient(hostport string, params *Params) (Client, error) {
	addr, err := lspnet.ResolveUDPAddr("udp", hostport)
	if err != nil {
		return nil, err
	}
	conn, err := lspnet.DialUDP("udp", nil, addr)
	if err != nil {
		return nil, err
	}
	cli := initClient(conn, addr, params)
	// connection request
	connMsg := NewConnect()
	clientSendMsg(cli, connMsg)
	cli.epochTimer.Reset(time.Millisecond * time.Duration(params.EpochMillis))
	go cli.ConnectHelper(params, connMsg)
	for {
		readBytes := make([]byte, MAX)
		msg, _, err := cli.clientRecvMsg(readBytes)
		if err != nil {
			continue
		}
		// a connection is established
		if msg.Type == MsgAck && msg.SeqNum == 0 {
			// reset the global cnt after connection build
			cli.globalCntReset <- true // helper channel to reset global cnt
			<-cli.globalCntGo
			cli.connID = msg.ConnID
			cli.newclihelper <- true
			go cli.mainRoutine(cli.recvChan, cli.stopConn, clientSendMsg)
			go cli.readRoutine()
			return cli, nil
		}
	}
}

// Return clientId
func (c *client) ConnID() int {
	return c.connID
}

// User read from client, if called, pending until a msg prepared to
// be read
func (c *client) Read() ([]byte, error) {
	select {
	case msg := <-c.recvChan:
		return msg.Payload, nil
	case <-c.stopReadConn:
		return nil, errors.New("Read falied, server lost.")
		// case msg, ok := <-c.recvChan:
		// 	if !ok {
		// 		return nil, errors.New("Read falied, server lost.")
		// 	}
		// 	return msg.Payload, nil
	}
}

// User write from client to server
func (c *client) Write(payload []byte) error {
	if c.isLost {
		return errors.New("Server lost.")
	}
	c.seqNum++
	message := NewData(c.connID, c.seqNum, len(payload), payload)
	c.pendingMsgQue <- message // add to queue pending to be processed
	return nil
}

// Close client from client itself, return after processing all pending
// message already
func (c *client) Close() error {
	if !c.isLost {
		c.toClose <- true
		<-c.closeDone // all pending message process done
	}
	err := c.conn.Close()
	return err
}

//keep connecting to server until epoch limit or connected to server
func (cli *client) ConnectHelper(params *Params, connMsg *Message) {
	for {
		select {
		case <-cli.newclihelper:
			return
		case <-cli.epochTimer.C:
			cli.globalCnt++
			// if not build connect during one epoch, resend the conn request
			if cli.globalCnt < params.EpochLimit {
				clientSendMsg(cli, connMsg)
				cli.epochTimer.Reset(time.Millisecond * time.Duration(params.EpochMillis))
			} else {
				return
			}
		case <-cli.globalCntReset:
			cli.globalCnt = 0
			cli.globalCntGo <- true
		}
	}
}

//keep reading from server
func (c *client) keepRead() {
	for {
		select {
		case <-c.isstopConn: // kill this goroutine when close
			return
		default: // read from UDP
			readBytes := make([]byte, MAX)
			msg, _, err := c.clientRecvMsg(readBytes)
			if err != nil {
				continue
			}
			c.readHelper <- msg
		}
	}
}

// Listen for packages, handle ACKs and data
func (c *client) readRoutine() {
	go c.keepRead()
	for {
		select {
		case <-c.stopConn:
			c.isstopConn <- true
			c.stopReadConn <- true
			if !c.isLost {
				c.closeDone <- true
			}
			return
		case msg := <-c.readHelper: // handle different type message
			if msg.Type == MsgData {
				newMsg := NewAck(msg.ConnID, msg.SeqNum)
				err := clientSendMsg(c, newMsg) // send ack back to server
				if err != nil {
					// fmt.Println("error send Msg")
					continue
				}
				c.sendRecvChan <- msg
			}
			if msg.Type == MsgAck {
				c.sendRecvChan <- msg
			}
		}
	}
}

// Helper function to marshal and send to server
func clientSendMsg(cli *client, msg *Message) error {
	sendBytes, err := json.Marshal(msg)
	if err != nil {
		// fmt.Println("Marshal error!")
		return err
	}
	_, err = cli.conn.Write(sendBytes)
	if err != nil {
		// fmt.Println("Write to server error!")
		return err
	}
	return nil
}

// Helper function to unmarshal and read from server
func (c *client) clientRecvMsg(readBytes []byte) (*Message, *lspnet.UDPAddr, error) {
	readSize, addr, err := c.conn.ReadFromUDP(readBytes)
	if err != nil {
		// fmt.Println("Read from server error!")
		return nil, nil, err
	}
	var msg Message
	err = json.Unmarshal(readBytes[:readSize], &msg)
	if err != nil {
		// fmt.Println("Unmarshal error!")
		return nil, nil, err
	}
	// if payload read is smaller than message size, drop it
	if len(msg.Payload) < msg.Size {
		return nil, nil, errors.New("size too small")
	}
	// if payload read is larger than message size, chrunk it
	if msg.Size < len(msg.Payload) {
		msg.Payload = msg.Payload[:msg.Size]
	}
	return &msg, addr, err
}
