package ipc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

const maxRequest = 256 * 1024

type Handler func(context.Context, string, json.RawMessage) (any, error)

type Request struct {
	V      int             `json:"v"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type Response struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

func Failure(err error) Response { return Response{Error: err.Error()} }

func Parse(reader io.Reader) (Request, error) {
	var request Request
	raw, err := io.ReadAll(io.LimitReader(reader, maxRequest+1))
	if err != nil {
		return request, err
	}
	if len(raw) > maxRequest || !utf8.Valid(raw) {
		return request, errors.New("请求超过大小限制或 UTF-8 无效")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return request, errors.New("请求 JSON 无效")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return request, errors.New("每次只能发送一个请求")
	}
	if request.V != 1 {
		return request, errors.New("不支持的协议版本")
	}
	switch request.Method {
	case "event.submit", "title.claim", "title.ack", "hook.observed":
	default:
		return request, errors.New("bridge 不允许调用此方法")
	}
	if len(request.Params) == 0 || request.Params[0] != '{' {
		return request, errors.New("params 必须是 JSON 对象")
	}
	return request, nil
}

func dispatch(ctx context.Context, request Request, handler Handler) Response {
	value, err := handler(ctx, request.Method, request.Params)
	if err != nil {
		return Failure(err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return Failure(err)
	}
	return Response{OK: true, Result: raw}
}
