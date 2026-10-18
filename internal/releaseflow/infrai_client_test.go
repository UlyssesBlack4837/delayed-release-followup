package releaseflow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientRequestBodies(t *testing.T) {
	tests := []struct {
		name string
		call func(*Client) error
		want map[string]any
	}{
		{
			name: "delete cron sends object",
			call: func(client *Client) error {
				return client.DeleteCron(context.Background(), "job_42", "delete-key")
			},
			want: map[string]any{},
		},
		{
			name: "publish includes queue and payload",
			call: func(client *Client) error {
				return client.Publish(context.Background(), "release-operations", map[string]any{"operation": "release"}, "publish-key")
			},
			want: map[string]any{
				"queue":   "release-operations",
				"payload": map[string]any{"operation": "release"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Errorf("decode request: %v", err)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"ok":true,"data":{}}`))
			}))
			defer server.Close()

			client := NewClient("test-key")
			client.baseURL = server.URL
			if err := tt.call(client); err != nil {
				t.Fatal(err)
			}
			if !mapsEqual(got, tt.want) {
				t.Fatalf("body = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func mapsEqual(got, want map[string]any) bool {
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	return string(gotJSON) == string(wantJSON)
}
