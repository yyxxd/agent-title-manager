package pluginmgr

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yyxxd/agent-title-manager/internal/engine"
)

func (m *Manager) ReadNamingSnapshot(ctx context.Context, agent, sessionID, handle, scope string) (engine.NamingSnapshot, error) {
	if enabled, err := m.Enabled(ctx, agent); err != nil || !enabled {
		return engine.NamingSnapshot{}, ErrNativePending
	}
	if agent == "pi" {
		if handle == sessionID {
			handle = locatePiNamingSession(sessionID)
		}
		return piNamingSnapshot(ctx, sessionID, handle)
	}
	node, adapter, home, err := m.nativeReader(agent, sessionID, handle)
	var snapshot engine.NamingSnapshot
	if err != nil {
		return snapshot, err
	}
	wire, err := adapterOutput(ctx, node, agent, sessionID, adapter, home, scope, "suggest")
	if err != nil || len(wire) > 128<<10 || json.Unmarshal(wire, &snapshot) != nil || snapshot.SessionID != sessionID {
		return engine.NamingSnapshot{}, ErrNativePending
	}
	return snapshot, nil
}

func locatePiNamingSession(id string) string {
	if !piSessionID.MatchString(id) {
		return ""
	}
	host, err := resolveHost("pi")
	if err != nil {
		return ""
	}
	dir := filepath.Join(host.dir, "sessions")
	root, err := os.OpenRoot(dir)
	if err != nil {
		return ""
	}
	defer root.Close()
	groups, err := fs.ReadDir(root.FS(), ".")
	if err != nil || len(groups) > 4096 {
		return ""
	}
	var found string
	for _, group := range groups {
		if !group.IsDir() {
			continue
		}
		files, err := fs.ReadDir(root.FS(), group.Name())
		if err != nil {
			return ""
		}
		for _, file := range files {
			if !file.Type().IsRegular() || !strings.HasSuffix(file.Name(), "_"+id+".jsonl") {
				continue
			}
			if found != "" {
				return ""
			}
			found = filepath.Join(dir, group.Name(), file.Name())
		}
	}
	return found
}

type piNamingEntry struct {
	Type, ID string
	ParentID string
	Name     *string
	Message  struct {
		Role, StopReason string
		Content          json.RawMessage
	}
}

func piNamingSnapshot(ctx context.Context, id, handle string) (engine.NamingSnapshot, error) {
	file, err := openPiSession(ctx, id, handle)
	if err != nil {
		return engine.NamingSnapshot{}, err
	}
	defer file.Close()
	const limit = 8 << 20
	before, err := file.Stat()
	if err != nil || before.Size() > limit {
		return engine.NamingSnapshot{}, ErrNativePending
	}
	wire, err := io.ReadAll(io.NewSectionReader(file, 0, limit+1))
	after, statErr := file.Stat()
	if err != nil || statErr != nil || ctx.Err() != nil || int64(len(wire)) != before.Size() ||
		after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return engine.NamingSnapshot{}, ErrNativePending
	}
	return parsePiNamingSnapshot(id, wire)
}

func parsePiNamingSnapshot(id string, wire []byte) (engine.NamingSnapshot, error) {
	header, body, complete := bytes.Cut(wire, []byte{'\n'})
	var h struct {
		piHeader
		Cwd string
	}
	if !complete || !bytes.HasSuffix(wire, []byte{'\n'}) || json.Unmarshal(header, &h) != nil ||
		h.Version != 3 || h.Type != "session" || h.ID != id {
		return engine.NamingSnapshot{}, ErrNativePending
	}
	snapshot := engine.NamingSnapshot{SessionID: id, Project: h.Cwd, TitleOrigin: "auto"}
	entries := make(map[string]piNamingEntry)
	var leaf string
	for line := range bytes.SplitSeq(body, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var entry piNamingEntry
		if json.Unmarshal(line, &entry) != nil || entry.ID == "" || entries[entry.ID].ID != "" ||
			entry.ParentID != "" && entries[entry.ParentID].ID == "" {
			return engine.NamingSnapshot{}, ErrNativePending
		}
		if entry.Type == "session_info" && entry.Name != nil {
			snapshot.CurrentTitle = strings.TrimSpace(*entry.Name)
		}
		entries[entry.ID], leaf = entry, entry.ID
	}
	if snapshot.CurrentTitle != "" {
		snapshot.TitleOrigin = "unknown"
	}
	for cursor := leaf; cursor != ""; cursor = entries[cursor].ParentID {
		entry := entries[cursor]
		if entry.Type == "message" && (entry.Message.Role == "user" || entry.Message.Role == "assistant") {
			if snapshot.Revision == "" {
				if entry.Message.Role != "assistant" || entry.Message.StopReason != "stop" && entry.Message.StopReason != "length" {
					return snapshot, nil
				}
				digest := sha256.Sum256(wire)
				snapshot.Revision = hex.EncodeToString(digest[:])
			}
			text := piNamingText(entry.Message.Content)
			if entry.Message.Role == "user" && text != "" {
				snapshot.OriginalGoal = text
			}
			if text != "" && len(snapshot.Turns) < 8 {
				snapshot.Turns = append(snapshot.Turns, engine.NamingTurn{Role: entry.Message.Role, Text: text})
			}
		}
	}
	slices.Reverse(snapshot.Turns)
	return snapshot, nil
}

func piNamingText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) != nil {
		var parts []struct{ Type, Text string }
		if json.Unmarshal(raw, &parts) != nil {
			return ""
		}
		var blocks []string
		for _, part := range parts {
			if part.Type == "text" {
				blocks = append(blocks, part.Text)
			}
		}
		text = strings.Join(blocks, "\n")
	}
	text = strings.TrimSpace(text)
	count := 0
	for offset := range text {
		if count == 1500 {
			return text[:offset]
		}
		count++
	}
	return text
}
