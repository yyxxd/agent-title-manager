package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"github.com/yyxxd/agent-title-manager/internal/ipc"
)

func main() {
	if err := json.NewEncoder(os.Stdout).Encode(run()); err != nil {
		os.Exit(1)
	}
}

func run() ipc.Response {
	if len(os.Args) != 2 || os.Args[1] != "rpc" {
		return ipc.Failure(errors.New("用法: atm-bridge.exe rpc"))
	}
	request, err := ipc.Parse(os.Stdin)
	if err != nil {
		return ipc.Failure(err)
	}
	_, scope, err := ipc.Profile()
	if err != nil {
		return ipc.Failure(err)
	}
	response, err := ipc.Call(context.Background(), scope, request)
	if err != nil {
		return ipc.Failure(err)
	}
	return response
}
