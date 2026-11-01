package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/access"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/usage"
)

// Decisions is OpenAI's own API, not System One: keep its question array
// and answer distributions intact and route the provider-prefixed model.
func TestOpenAIDecisionsPassThrough(t *testing.T) {
	fresh(t)
	var sent []map[string]json.RawMessage
	const answer = `{"model":"gpt-6-luna","answers":[{"type":"predicate","name":"code","probability":0.99}],"usage":{"input_tokens":12,"output_tokens":3,"total_tokens":15}}`
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/decisions" || r.Header.Get("Authorization") != "Bearer key" {
			t.Errorf("upstream path or auth: %s", r.URL.Path)
			w.WriteHeader(400)
			return
		}
		var got map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		sent = append(sent, got)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, answer)
	}))
	t.Cleanup(up.Close)
	if err := provider.Save(provider.Provider{ID: "openai", Name: "OpenAI", Chat: up.URL + "/v1", Responses: up.URL + "/v1", Key: "key", Models: []string{"gpt-6-luna"}}); err != nil {
		t.Fatal(err)
	}
	s := New()
	const body = `{"model":"openai/gpt-6-luna","input":"Fix the login crash.","questions":[{"type":"predicate","name":"code","instructions":"Is this a coding request?"}],"prompt_cache_key":"decision-test"}`
	for _, path := range []string{"/v1/decisions", "/decisions"} {
		code, raw := postRetrieval(t, s, path, body)
		if code != 200 || raw != answer {
			t.Fatalf("%s: %d %s", path, code, raw)
		}
	}
	var want map[string]json.RawMessage
	json.Unmarshal([]byte(body), &want)
	want["model"] = json.RawMessage(`"gpt-6-luna"`)
	for _, got := range sent {
		for k, v := range want {
			if string(got[k]) != string(v) {
				t.Errorf("upstream %s: %s, want %s", k, got[k], v)
			}
		}
	}
	if len(sent) != 2 {
		t.Fatalf("upstream requests: %d", len(sent))
	}
	var records []usage.Record
	for i := 0; i < 50; i++ {
		records = usage.Load(time.Time{})
		if len(records) >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(records) < 2 || records[0].Operation != "decisions" || records[0].Input != 12 || records[0].Output != 3 {
		t.Fatalf("decision usage: %+v", records)
	}
	keys, secrets := newCaller(t, "Held", "Free")
	h := lanGuard(s.Handler())
	unauthorized := httptest.NewRecorder()
	h.ServeHTTP(unauthorized, httptest.NewRequest("POST", "/v1/decisions", strings.NewReader(body)))
	if unauthorized.Code != 401 || len(sent) != 2 {
		t.Fatal("an unauthenticated decision reached upstream")
	}
	if _, err := access.Update("models-key", access.Change{Key: keys[0].ID, Models: []string{"other/*"}}); err != nil {
		t.Fatal(err)
	}
	for i, status := range []int{403, 200} {
		r := httptest.NewRequest("POST", "/v1/decisions", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+secrets[i])
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("calling key %d: %d, want %d", i, w.Code, status)
		}
	}
	if len(sent) != 3 {
		t.Fatal("a calling key's model restriction was not enforced")
	}
	// A switched-off model never reaches the paid API.
	if err := provider.DisableModels("openai/gpt-6-luna"); err != nil {
		t.Fatal(err)
	}
	if code, raw := postRetrieval(t, s, "/v1/decisions", body); code != 403 || !strings.Contains(raw, "error") {
		t.Fatalf("disabled model: %d %s", code, raw)
	}
	if len(sent) != 3 {
		t.Fatal("disabled decision reached upstream")
	}
}
