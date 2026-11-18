package ipc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"sync"
	"time"

	"github.com/Microsoft/go-winio"
)

const timeout = 900 * time.Millisecond

func IsRunning(scope string) (bool, error) {
	name, _, err := identity(scope)
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	connection, err := winio.DialPipeContext(ctx, name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, connection.Close()
}

func Listen(scope string) (net.Listener, error) {
	name, acl, err := identity(scope)
	if err != nil {
		return nil, err
	}
	return winio.ListenPipe(name, &winio.PipeConfig{SecurityDescriptor: acl})
}

func Serve(ctx context.Context, listener net.Listener, handler Handler) error {
	const maxClients = 32
	active := make(chan struct{}, maxClients)
	var clients sync.WaitGroup
	defer clients.Wait()
	for {
		connection, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return nil
			}
			return err
		}
		select {
		case active <- struct{}{}:
			clients.Add(1)
			go func() {
				defer clients.Done()
				defer func() { <-active }()
				serveConnection(ctx, connection, handler)
			}()
		default:
			connection.Close()
		}
	}
}

func serveConnection(parent context.Context, connection net.Conn, handler Handler) {
	defer connection.Close()
	connection.SetDeadline(time.Now().Add(timeout))
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	reader := bufio.NewReaderSize(connection, maxRequest+1)
	line, err := reader.ReadSlice('\n')
	if err != nil {
		return
	}
	request, err := Parse(bytes.NewReader(line[:len(line)-1]))
	if err != nil {
		_ = json.NewEncoder(connection).Encode(Failure(err))
		return
	}
	_ = json.NewEncoder(connection).Encode(dispatch(ctx, request, handler))
}

func Call(ctx context.Context, scope string, request Request) (Response, error) {
	var response Response
	name, _, err := identity(scope)
	if err != nil {
		return response, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	connection, err := winio.DialPipeContext(ctx, name)
	if err != nil {
		return response, errors.New("桌面服务未运行或未响应")
	}
	defer connection.Close()
	deadline, _ := ctx.Deadline()
	connection.SetDeadline(deadline)
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return response, err
	}
	err = json.NewDecoder(io.LimitReader(connection, maxRequest)).Decode(&response)
	return response, err
}
