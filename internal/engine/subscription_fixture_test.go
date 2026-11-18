package engine

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	name := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
	if name == "codex" || name == "claude" {
		os.Exit(runSubscriptionFixture(name, os.Args[1:]))
	}
	os.Exit(m.Run())
}

func installSubscriptionFixtures(t *testing.T) func(string) {
	t.Helper()
	root := t.TempDir()
	original, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"codex", "claude"} {
		path := filepath.Join(root, name)
		if runtime.GOOS == "windows" {
			path += ".exe"
		}
		if err := os.Link(original, path); err != nil {
			data, err := os.ReadFile(original)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0700); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ANTHROPIC_API_KEY", "fixture-not-forwarded")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "fixture-not-forwarded")
	t.Setenv("OPENAI_API_KEY", "fixture-not-forwarded")
	return func(mode string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, "fixture-mode"), []byte(mode), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func runSubscriptionFixture(provider string, args []string) int {
	modeRaw, _ := os.ReadFile(filepath.Join(filepath.Dir(os.Args[0]), "fixture-mode"))
	mode := string(modeRaw)
	if provider == "codex" {
		if os.Getenv("OPENAI_API_KEY") != "" {
			return 91
		}
		return fixtureCodex(args, mode)
	}
	if os.Getenv("ANTHROPIC_API_KEY") != "" || os.Getenv("ANTHROPIC_AUTH_TOKEN") != "" {
		return 91
	}
	return fixtureClaude(args, mode)
}

func fixtureCodex(args []string, mode string) int {
	switch args[0] {
	case "login":
		if mode == "noauth" {
			fmt.Println("Not logged in")
			return 1
		}
		fmt.Println("Logged in using ChatGPT")
	case "app-server":
		if !slices.Contains(args, nativeCodexProvider) || slices.Contains(args, "--ignore-user-config") {
			return 92
		}
		return fixtureCodexStream(mode)
	case "exec":
		if !slices.Contains(args, `model_reasoning_effort="high"`) || !slices.Contains(args, `service_tier="priority"`) ||
			!slices.Contains(args, "fixture-codex") {
			return 93
		}
		fmt.Println(`{"type":"item.completed","item":{"type":"agent_message","text":"{\"action\":\"keep\"}"}}`)
		fmt.Println(`{"type":"turn.completed","usage":{"input_tokens":5,"output_tokens":2}}`)
	}
	return 0
}

func fixtureCodexStream(mode string) int {
	pages := 0
	decoder := json.NewDecoder(os.Stdin)
	for {
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params struct {
				Hidden *bool  `json:"includeHidden"`
				Cursor string `json:"cursor"`
			} `json:"params"`
		}
		if decoder.Decode(&request) != nil {
			return 0
		}
		switch request.Method {
		case "initialize":
			fmt.Printf("{\"id\":%d,\"result\":{}}\n", request.ID)
		case "initialized":
		case "config/read":
			path := ""
			if mode == "custom-catalog" {
				path = "fixture-catalog.json"
			}
			fmt.Printf("{\"id\":%d,\"result\":{\"config\":{\"model_provider\":\"openai\",\"model_catalog_json\":%q}}}\n", request.ID, path)
		case "model/list":
			pages++
			if request.Params.Hidden == nil || *request.Params.Hidden {
				return 94
			}
			if mode == "old" {
				fmt.Printf("{\"id\":%d,\"error\":{\"code\":-32601}}\n", request.ID)
			} else if pages == 1 && mode != "empty" {
				fmt.Printf("{\"id\":%d,\"result\":{\"data\":[{\"id\":\"display-id\",\"model\":\"fixture-codex\",\"displayName\":\"Fixture Codex\",\"supportedReasoningEfforts\":[{\"reasoningEffort\":\"high\"}],\"defaultReasoningEffort\":\"high\"}],\"nextCursor\":\"next-page\"}}\n", request.ID)
			} else if mode == "empty" || request.Params.Cursor == "next-page" {
				data := "[]"
				if mode != "empty" {
					data = `[{"model":"hidden","hidden":true},{"model":"fixture-no-effort"}]`
				}
				fmt.Printf("{\"id\":%d,\"result\":{\"data\":%s,\"nextCursor\":null}}\n", request.ID, data)
			} else {
				return 95
			}
		}
	}
}

func fixtureClaude(args []string, mode string) int {
	if !slices.Contains(args, "--safe-mode") || slices.Contains(args, "--bare") || os.Getenv("ATM_CLAUDE_TITLE_WORKER") != "1" {
		return 96
	}
	source := slices.Index(args, "--setting-sources")
	if source < 0 || source+1 >= len(args) || args[source+1] != "" {
		return 96
	}
	if slices.Contains(args, "auth") {
		if mode == "noauth" {
			fmt.Println(`{"loggedIn":false,"authMethod":"none","apiProvider":"firstParty"}`)
			return 1
		}
		if mode == "injected-oauth" {
			fmt.Println(`{"loggedIn":true,"authMethod":"oauth_token","apiProvider":"firstParty"}`)
			return 0
		}
		fmt.Println(`{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty"}`)
		return 0
	}
	if slices.Contains(args, "--input-format") {
		return fixtureClaudeStream(mode)
	}
	if !slices.Contains(args, "fixture-claude") || !slices.Contains(args, "--effort") ||
		!slices.Contains(args, "max") || !slices.Contains(args, decisionSchema) || !slices.Contains(args, "--no-session-persistence") {
		return 97
	}
	if mode == "ambiguous" {
		return 1
	}
	if mode == "timeout" {
		time.Sleep(30 * time.Second)
		return 1
	}
	if mode == "refused" {
		fmt.Println(`{"type":"assistant","error":"rate_limit"}`)
		fmt.Println(`{"type":"result","subtype":"error_during_execution","is_error":true}`)
		return 1
	}
	input, _ := io.ReadAll(os.Stdin)
	title := goodTitle
	action := "rename"
	if strings.Contains(string(input), "connectivity_test") {
		title, action = "", "keep"
	}
	fmt.Printf(`{"type":"result","subtype":"success","is_error":false,"structured_output":{"action":%q,"title":%q},"usage":{"input_tokens":7,"output_tokens":3,"cache_creation_input_tokens":5,"cache_read_input_tokens":2},"modelUsage":{"fixture-resolved":{}}}`+"\n", action, title)
	return 0
}

func fixtureClaudeStream(mode string) int {
	scan := bufio.NewScanner(os.Stdin)
	if !scan.Scan() || !strings.Contains(scan.Text(), `"subtype":"initialize"`) ||
		!strings.Contains(scan.Text(), `"type":"control_request"`) {
		return 98
	}
	if mode == "timeout" {
		time.Sleep(30 * time.Second)
		return 1
	}
	fmt.Println(`{"type":"control_response","response":{"subtype":"success","request_id":"other"}}`)
	data := `{"models":[{"value":"fixture-claude","displayName":"Fixture Claude","resolvedModel":"fixture-resolved","supportsEffort":true,"supportedEffortLevels":["max"]},{"value":"legacy"}]}`
	if mode == "old" {
		data = "{}"
	}
	fmt.Printf(`{"type":"control_response","response":{"subtype":"success","request_id":"atm-catalog","response":%s}}`+"\n", data)
	if scan.Scan() {
		return 99
	}
	return 0
}
