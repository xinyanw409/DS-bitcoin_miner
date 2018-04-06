package main

import (
	// "container/list"
	"encoding/json"
	"fmt"
	"github.com/cmu440/bitcoin"
	"github.com/cmu440/lsp"
	// "log"
	"os"
	"strconv"
)

// var LOGF *log.Logger

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

	const numArgs = 4
	if len(os.Args) != numArgs {
		fmt.Printf("Usage: ./%s <hostport> <message> <maxNonce>", os.Args[0])
		return
	}
	hostport := os.Args[1]
	message := os.Args[2]
	maxNonce, err := strconv.ParseUint(os.Args[3], 10, 64)
	if err != nil {
		fmt.Printf("%s is not a number.\n", os.Args[3])
		return
	}

	client, err := lsp.NewClient(hostport, lsp.NewParams())
	if err != nil {
		fmt.Println("Failed to connect to server:", err)
		return
	}
	// LOGF.Println("new client buildup")
	// LOGF.Println("defer close")
	defer client.Close()
	// LOGF.Println("ckilode")
	// defer LOGF.Println("client close1")

	// _ = message    // Keep compiler happy. Please remove!
	// _ = maxNonce   // Keep compiler happy. Please remove!
	// TODO: implement this!
	req := bitcoin.NewRequest(message, 0, maxNonce)
	marshaledReq, err := json.Marshal(req)
	if err != nil {
		fmt.Println("request marshal error")
		return
	}
	err = client.Write(marshaledReq)
	if err != nil {
		fmt.Println("client error write")
		return
	}
	marshaledResp, err := client.Read()
	if err != nil {
		// LOGF.Println("client error read")
		fmt.Println("client error read")
		printDisconnected()
		return
	}
	var result bitcoin.Message
	err = json.Unmarshal(marshaledResp, &result)
	if err != nil {
		fmt.Println("client error unmarshal")
		return
	}
	// hash := strconv.FormatInt(int64(result.Hash), 10)
	// nonce := strconv.FormatInt(int64(result.Nonce), 10)
	printResult(result.Hash, result.Nonce)
	// client.Close()
	// printResult(0, 0)
}

// printResult prints the final result to stdout.
func printResult(hash, nonce uint64) {
	fmt.Printf("Result %v %v", hash, nonce)
}

// printDisconnected prints a disconnected message to stdout.
func printDisconnected() {
	fmt.Println("Disconnected")
}
