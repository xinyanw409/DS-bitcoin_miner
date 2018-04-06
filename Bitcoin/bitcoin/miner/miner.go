package main

import (
	// "container/list"
	"encoding/json"
	"fmt"
	"github.com/cmu440/bitcoin"
	"github.com/cmu440/lsp"
	// "log"
	"os"
	// "strconv"
)

// var LOGF *log.Logger

// Attempt to connect miner as a client to the server.
func joinWithServer(hostport string) (lsp.Client, error) {
	// TODO: implement this!
	client, err := lsp.NewClient(hostport, lsp.NewParams())
	if err != nil {
		fmt.Println("miner error connect")
		return nil, err
	}
	req := bitcoin.NewJoin()
	marshaledReq, err := json.Marshal(req)
	if err != nil {
		fmt.Println("join marshal error")
		return nil, err
	}
	err = client.Write(marshaledReq)
	if err != nil {
		fmt.Println("miner error write")
		return nil, err
	}
	return client, nil
}

func main() {
	// const (
	// 	name = "log.txt"
	// 	flag = os.O_RDWR | os.O_CREATE
	// 	perm = os.FileMode(0666)
	// )

	// file, err := os.OpenFile(name, flag, perm)
	// if err != nil {
	// 	return
	// }
	// defer file.Close()

	// LOGF = log.New(file, "", log.Lshortfile|log.Lmicroseconds)
	const numArgs = 2
	if len(os.Args) != numArgs {
		fmt.Printf("Usage: ./%s <hostport>", os.Args[0])
		return
	}
	// fmt.Println("miner start")
	hostport := os.Args[1]
	miner, err := joinWithServer(hostport)
	if err != nil {
		fmt.Println("Failed to join with server:", err)
		return
	}
	// LOGF.Println("miner defer close")
	defer miner.Close()
	// defer LOGF.Println("miner close22")
	// TODO: implement this!
	for {
		marshaledJob, err := miner.Read()
		if err != nil {
			// LOGF.Println("miner error read")
			return
		}
		var unmarshaledjob bitcoin.Message
		// LOGF.Println("unmarshal")
		err = json.Unmarshal(marshaledJob, &unmarshaledjob)
		job := &unmarshaledjob
		if job.Type == bitcoin.Request {
			// LOGF.Println("request from server")
			hash, nonce := computeHash(job)
			result := bitcoin.NewResult(hash, nonce)
			marshaledres, err := json.Marshal(result)
			if err != nil {
				// LOGF.Println("marshal error miner")
				return
			}
			err = miner.Write(marshaledres)
			if err != nil {
				// LOGF.Println("write error miner")
				return
			}
		}
	}
}

func computeHash(job *bitcoin.Message) (uint64, uint64) {
	data := job.Data
	lower := job.Lower
	upper := job.Upper
	nonce := lower
	hash := bitcoin.Hash(data, nonce)
	for i := lower + 1; i <= upper; i++ {
		temp := bitcoin.Hash(data, i)
		if temp < hash {
			hash = temp
			nonce = i
		}
	}
	return hash, nonce
}
