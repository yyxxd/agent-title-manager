package orcasync

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
)

const maxPiTail = 768 << 10

// ReadPiTitle mirrors Pi's session manager: the latest session_info entry in
// file order supplies the name, including an explicit empty name.
func ReadPiTitle(ctx context.Context, id, handle string) (string, error) {
	file, err := openPiSession(ctx, id, handle)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	header, err := readPiHeader(file)
	if err != nil || header.Version != 3 || header.ID != id || header.Type != "session" {
		return "", ErrNativePending
	}
	start := max(0, info.Size()-maxPiTail)
	wire := make([]byte, info.Size()-start)
	if _, err := file.ReadAt(wire, start); err != nil {
		return "", err
	}
	after, err := file.Stat()
	if err != nil || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) || ctx.Err() != nil {
		return "", ErrNativePending
	}
	// Only accept complete lines and discard the first fragment of a bounded tail.
	if !bytes.HasSuffix(wire, []byte{'\n'}) {
		return "", ErrNativePending
	}
	if start > 0 {
		_, wire, _ = bytes.Cut(wire, []byte{'\n'})
	}
	return readPiName(wire, start > 0)
}

func readPiName(wire []byte, truncated bool) (string, error) {
	lines := bytes.Split(wire, []byte{'\n'})
	for i := len(lines) - 1; i >= 0; i-- {
		if len(bytes.TrimSpace(lines[i])) == 0 {
			continue
		}
		var entry struct {
			Type string  `json:"type"`
			Name *string `json:"name"`
		}
		if json.Unmarshal(lines[i], &entry) != nil || entry.Type == "" {
			return "", ErrNativePending
		}
		if entry.Type == "session_info" {
			if entry.Name == nil {
				return "", ErrNativePending
			}
			return strings.TrimSpace(*entry.Name), nil
		}
	}
	if truncated {
		return "", ErrNativePending
	}
	return "", nil
}

func readPiHeader(file *os.File) (piHeader, error) {
	var header piHeader
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return header, err
	}
	wire, err := readFirstLine(file)
	if err != nil {
		return header, err
	}
	return header, json.Unmarshal(wire, &header)
}

type piHeader struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Version int    `json:"version"`
}
