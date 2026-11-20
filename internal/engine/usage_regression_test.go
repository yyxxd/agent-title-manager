package engine

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPartialTokensAndConnectionTestReconcile(t *testing.T) {
	e := engineAt(t, t.TempDir(), nil)
	var calls atomic.Int32
	httpProvider(t, e, func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		body := usageReply
		switch n {
		case 1:
			body = strings.Replace(body, `,"prompt_tokens_details":{"cached_tokens":12}`, "", 1)
		case 2:
			body = strings.Replace(body, `,"usage":{"prompt_tokens":50,"completion_tokens":8,"prompt_tokens_details":{"cached_tokens":12}}`, "", 1)
		default:
			body = strings.Replace(body, `"cached_tokens":12`, `"cached_tokens":0`, 1)
		}
		_, _ = w.Write([]byte(body))
	})
	call[accepted](t, e, "event.submit", event("partial", ""))
	eventually(t, func() bool { return latest(t, e).Status == "candidate" })
	call[testResult](t, e, "providers.test", providerTest{Provider: "api"})
	stats := call[dailyStats](t, e, "stats.daily", dateRequest{})
	if stats.Attempts != 2 || stats.TestAttempts != 1 || stats.UnknownUsage != 2 || stats.InputKnown != 1 ||
		stats.OutputKnown != 1 || stats.CachedKnown != 0 || !hasCount(stats.InputTokens, 50) ||
		!hasCount(stats.OutputTokens, 8) || stats.CachedTokens != nil || len(stats.ByModel) != 1 ||
		stats.ByModel[0].Provider != "api" || stats.ByModel[0].Model != "test-model" || stats.ByModel[0].TestAttempts != 1 {
		t.Fatalf("stats: %+v", stats)
	}
	records := call[recordList](t, e, "records.list", listRequest{})
	raw, err := json.Marshal(records)
	if err != nil || !strings.Contains(string(raw), `"inputTokens":50,"outputTokens":8,"cachedTokens":null`) ||
		!strings.Contains(string(raw), `"inputTokens":null,"outputTokens":null,"cachedTokens":null`) || len(records.TestAttempts) != 1 {
		t.Fatalf("%s %v", raw, err)
	}
	model := "another-model"
	call[testResult](t, e, "providers.test", providerTest{Provider: "api", APIModel: &model})
	stats = call[dailyStats](t, e, "stats.daily", dateRequest{})
	if len(stats.ByModel) != 2 || stats.ByModel[0].Model != model || stats.ByModel[0].CachedKnown != 1 ||
		!hasCount(stats.ByModel[0].CachedTokens, 0) || !hasCount(stats.CachedTokens, 0) {
		t.Fatalf("known zero: %+v", stats)
	}
	naming := stats.NamingUsage
	if stats.Attempts != 3 || stats.TestAttempts != 2 || !hasCount(stats.InputTokens, 100) ||
		naming.Attempts != 1 || naming.TestAttempts != 0 || !hasCount(naming.InputTokens, 50) ||
		!hasCount(naming.OutputTokens, 8) || naming.CachedTokens != nil {
		t.Fatalf("naming usage mixed with connection tests: %+v", stats)
	}
}
