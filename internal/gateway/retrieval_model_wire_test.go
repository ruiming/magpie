package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/settings"
)

func TestRetrievalModelWireNames(t *testing.T) {
	for _, mapping := range []string{"unchanged", "wire", "wildcard", "local-id"} {
		t.Run(mapping, func(t *testing.T) {
			fresh(t)
			want := "local"
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var got map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Error(err)
					http.Error(w, "invalid JSON", http.StatusBadRequest)
					return
				}
				var model string
				json.Unmarshal(got["model"], &model)
				if model != want {
					t.Errorf("upstream model %q, want %q", model, want)
				}
				if string(got["input"]) != `"probe"` {
					t.Errorf("input changed: %s", got["input"])
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"usage":{"input_tokens":1,"output_tokens":1}}`))
			}))
			defer up.Close()
			if err := provider.Save(provider.Provider{ID: "relay", Chat: up.URL + "/v1", Key: "key", Models: []string{"local"}}); err != nil {
				t.Fatal(err)
			}
			saved := settings.Load()
			switch mapping {
			case "wire":
				saved.ModelWires = map[string]string{"relay/local": "openai/vendor"}
				want = "openai/vendor"
			case "wildcard":
				saved.ModelWires = map[string]string{"relay/*": "openai/*"}
				want = "openai/local"
			case "local-id":
				saved.ModelIDs = map[string]string{"relay/local": "vendor"}
				want = "vendor"
			}
			if err := settings.Save(saved); err != nil {
				t.Fatal(err)
			}
			s := New()
			for _, path := range []string{"/v1/decisions", "/v1/embeddings", "/v1/rerank"} {
				if code, _ := postRetrieval(t, s, path, `{"model":"relay/local","input":"probe"}`); code != 200 {
					t.Fatalf("%s: status %d", path, code)
				}
			}
		})
	}
}
