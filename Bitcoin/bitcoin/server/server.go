// schedule policy:
// break job to little chunk and add job chunks to chunklist, return chunk number
// We use fixed chunk size which is 1000 to breakup the request from cleint and store
// them in a list. Each time a new miner join in or an old miner is available again,
// assign the job chunks to miner.

package main

import (
	"container/list"
	"encoding/json"
	"fmt"
	"github.com/cmu440/bitcoin"
	"github.com/cmu440/lsp"
	"log"
	"os"
	"strconv"
)

const JOBSIZE = 500

type server struct {
	lspServer  lsp.Server
	minerMap   map[int]*miner
	availMiner map[int]*miner
	cliMap     map[int]*client
	jobList    *list.List
	// wrapMsgChan chan *wrapMessage //put in wrapServer
	// handleErr   chan int
}

type wrapMessage struct {
	conn int
	msg  *bitcoin.Message
}

type job struct {
	connId int //requested client
	msg    *bitcoin.Message
}

type miner struct {
	connId int
	job    *job
}

type client struct {
	minHash    uint64
	nonce      uint64 // result return to client
	jobCnt     int    // return by schedule
	resvJobCnt int
}

func startServer(port int) (*server, error) {
	// TODO: implement this!
	params := lsp.NewParams()
	s, err := lsp.NewServer(port, params)
	if err != nil {
		fmt.Println("Server start failed.")
		return nil, err
	}
	new_server := &server{
		lspServer:  s,
		minerMap:   make(map[int]*miner),
		availMiner: make(map[int]*miner),
		cliMap:     make(map[int]*client),
		jobList:    list.New(),
		// wrapMsgChan: make(chan *wrapMessage, 1), //put in wrapServer
		// handleErr:   make(chan int, 1),
	}
	return new_server, err
}

var LOGF *log.Logger

func main() {
	// You may need a logger for debug purpose
	const (
		name = "log.txt"
		flag = os.O_RDWR | os.O_CREATE
		perm = os.FileMode(0666)
	)

	file, err := os.OpenFile(name, flag, perm)
	if err != nil {
		return
	}
	defer file.Close()

	LOGF = log.New(file, "", log.Lshortfile|log.Lmicroseconds)
	// Usage: LOGF.Println() or LOGF.Printf()

	const numArgs = 2
	if len(os.Args) != numArgs {
		fmt.Printf("Usage: ./%s <port>", os.Args[0])
		return
	}

	port, err := strconv.Atoi(os.Args[1])
	if err != nil {
		fmt.Println("Port must be a number:", err)
		return
	}

	srv, err := startServer(port)
	if err != nil {
		fmt.Println(err.Error())
		return
	}
	// LOGF.Println("server start")
	fmt.Println("Server listening on port", port)

	defer srv.lspServer.Close()
	// LOGF.Println("server close")

	for {
		cliConn, marshaledRecv, err := srv.lspServer.Read()
		if err != nil {
			// LOGF.Println("cliConn %v lost", cliConn)
			// client or miner crash
			if _, ok := srv.cliMap[cliConn]; ok {
				// LOGF.Printf("Client %v lost", cliConn)
				delete(srv.cliMap, cliConn)
				// srv.lspServer.CloseConn(cliConn)
			} else if _, ok := srv.minerMap[cliConn]; ok {
				// LOGF.Printf("Miner %v lost", cliConn)
				miner := srv.minerMap[cliConn]
				if miner.job != nil {
					job_toRedo := miner.job
					if _, ok := srv.cliMap[job_toRedo.connId]; ok {
						srv.jobList.PushFront(job_toRedo)
					}
				}

				delete(srv.minerMap, cliConn)
				delete(srv.availMiner, cliConn)
				// LOGF.Println("closed")
				// srv.lspServer.CloseConn(cliConn)
			}
		} else {
			var unmarshaledrecvMsg bitcoin.Message
			err = json.Unmarshal(marshaledRecv, &unmarshaledrecvMsg)
			if err != nil {
				fmt.Println("server error unmarshal")
			}
			recvMsg := &unmarshaledrecvMsg
			if recvMsg.Type == bitcoin.Join {
				// fmt.Printf("recv join %v", cliConn)
				// LOGF.Printf("recv join %v", cliConn)
				miner := &miner{
					connId: cliConn,
					job:    nil,
				}
				srv.minerMap[cliConn] = miner
				srv.availMiner[cliConn] = miner
				srv.sendMessageHandler()
			} else if recvMsg.Type == bitcoin.Request {
				// fmt.Printf("recv request %v \n", cliConn)
				// LOGF.Printf("recv request %v", cliConn)
				jobCnt := srv.scheduleJob(recvMsg, cliConn)
				srv.cliMap[cliConn] = &client{
					minHash:    ^uint64(0),
					nonce:      0,
					jobCnt:     jobCnt,
					resvJobCnt: 0,
				}
				srv.sendMessageHandler()
			} else if recvMsg.Type == bitcoin.Result {
				// fmt.Printf("receive %v\n", cliConn)

				// LOGF.Println("recv result", cliConn)
				miner, ok := srv.minerMap[cliConn]
				if ok {
					thisJob := miner.job
					miner.job = nil
					srv.availMiner[cliConn] = miner
					srv.sendMessageHandler()
					cli, ok := srv.cliMap[thisJob.connId]
					if ok {
						cli.resvJobCnt++

						if cli.minHash > recvMsg.Hash {
							cli.minHash = recvMsg.Hash
							cli.nonce = recvMsg.Nonce
						}
						if cli.resvJobCnt == cli.jobCnt {
							res := bitcoin.NewResult(cli.minHash, cli.nonce)
							srv.sendMessage(res, thisJob.connId)
							delete(srv.cliMap, thisJob.connId)
							// srv.lspServer.CloseConn(thisJob.connId)

							// LOGF.Printf("finish job, server close client %v", thisJob.connId)
						}
					} // if not ok, client lost, ignore the result
				}
			}
		}
	}
}

func (s *server) sendMessageHandler() {
	if s.jobList.Len() > 0 && len(s.availMiner) > 0 {
		for connId, miner := range s.availMiner {
			if e := s.jobList.Front(); e != nil {
				job := e.Value.(*job)
				miner.job = job
				s.sendMessage(job.msg, miner.connId)
				s.jobList.Remove(e)
				delete(s.availMiner, connId)
			} else {
				break
			}
		}
	}
}

func (s *server) sendMessage(msg *bitcoin.Message, connID int) error {
	marshaledMsg, err := json.Marshal(msg)
	if err != nil {
		// fmt.Println("server marshal error")
		return err
	}
	err = s.lspServer.Write(connID, marshaledMsg)
	if err != nil {
		// fmt.Println("server write error")
		//implement ............
		return err
	}
	return nil
}

// break job to little chunk and add job chunks to chunklist, return chunk number
// // schedule policy:
// break job to little chunk and add job chunks to chunklist, return chunk number
// We use fixed chunk size which is 1000 to breakup the request from cleint and store
// them in a list. Each time a new miner join in or an old miner is available again,
// assign the job chunks to miner.
func (s *server) scheduleJob(jobmsg *bitcoin.Message, connID int) int {
	if jobmsg.Type != bitcoin.Request {
		// LOGF.Println("error error error")
		return 0
	} else {
		size := jobmsg.Upper - jobmsg.Lower + 1
		// LOGF.Printf("job size is %v", size)
		// fmt.Printf("job size is %v\n", size)
		count := uint64(0)
		if size%JOBSIZE == 0 {
			count = size / JOBSIZE
		} else {
			count = size/JOBSIZE + 1
		}
		// LOGF.Println("count is %v", count)
		// fmt.Printf("count is %v\n", count)
		// fmt.Println("jobList len before is %v", s.jobList.Len())
		for ui := uint64(0); ui < count; ui++ {
			if ui == count-1 {
				newmsg := bitcoin.NewRequest(jobmsg.Data, ui*JOBSIZE, jobmsg.Upper)
				s.jobList.PushBack(&job{
					connId: connID,
					msg:    newmsg,
				})
			} else {
				newmsg := bitcoin.NewRequest(jobmsg.Data, ui*JOBSIZE, (ui+1)*JOBSIZE-1)
				s.jobList.PushBack(&job{
					connId: connID,
					msg:    newmsg,
				})
			}

		}
		// fmt.Println("jobList len %v", s.jobList.Len())
		return int(count)
	}
}
