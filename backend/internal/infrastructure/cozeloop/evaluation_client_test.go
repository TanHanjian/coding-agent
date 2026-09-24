package cozeloop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"interview-memory-agent/backend/internal/eval"
	"interview-memory-agent/backend/internal/infrastructure/config"
)

func TestOpenAPIEvaluationClientMapsDatasetEndpoints(t *testing.T) {
	var createdDataset map[string]any
	var createdItems map[string]any
	var createdVersion map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" || r.Header.Get("cozeloop-workspace-id") != "workspace-123" {
			t.Errorf("auth/workspace headers = %q / %q", r.Header.Get("Authorization"), r.Header.Get("cozeloop-workspace-id"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/loop/evaluation/evaluation_sets":
			if r.URL.Query().Get("workspace_id") != "workspace-123" || r.URL.Query().Get("dataset_keys") != eval.EvaluationDatasetKey {
				t.Errorf("dataset query = %v", r.URL.Query())
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"sets":[]}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/loop/evaluation/evaluation_sets":
			if err := json.NewDecoder(r.Body).Decode(&createdDataset); err != nil {
				t.Errorf("decode create dataset: %v", err)
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"evaluation_set_id":"ds-1"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/loop/evaluation/evaluation_sets/ds-1/versions":
			_, _ = w.Write([]byte(`{"code":0,"data":{"versions":[{"id":"ver-1","version":"0.0.0+run.run-one"}],"next_page_token":""}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/loop/evaluation/evaluation_sets/ds-1/items":
			_, _ = w.Write([]byte(`{"code":0,"data":{"items":[{"item_key":"key-1","turns":[{"field_datas":[{"name":"result_identity","content":{"content_type":"text","text":"run-one:case-1:1:1"}},{"name":"input_query","content":{"content_type":"text","text":"query"}}]}]}],"next_page_token":""}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/loop/evaluation/evaluation_sets/ds-1/items":
			if err := json.NewDecoder(r.Body).Decode(&createdItems); err != nil {
				t.Errorf("decode item request: %v", err)
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"itemOutputs":[{"item_key":"key-2","item_id":"item-2","item_version":"1","is_new_item":true}],"errors":[]}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/loop/evaluation/evaluation_sets/ds-1/versions":
			if err := json.NewDecoder(r.Body).Decode(&createdVersion); err != nil {
				t.Errorf("decode create version: %v", err)
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"version_id":"ver-2"}}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := newOpenAPIEvaluationClient(server.URL, "workspace-123", "test-token", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	set, err := client.EnsureDataset(ctx, eval.DefaultEvaluationDatasetSpec())
	if err != nil {
		t.Fatal(err)
	}
	if set.ID != "ds-1" || set.Key != eval.EvaluationDatasetKey {
		t.Fatalf("dataset = %+v", set)
	}
	if createdDataset["dataset_key"] != eval.EvaluationDatasetKey || createdDataset["workspace_id"] != "workspace-123" {
		t.Fatalf("dataset create request = %#v", createdDataset)
	}

	versions, err := client.ListDatasetVersions(ctx, set.ID)
	if err != nil || len(versions) != 1 || versions[0].ID != "ver-1" {
		t.Fatalf("versions = %+v, error = %v", versions, err)
	}
	items, err := client.ListDatasetItems(ctx, set.ID, "")
	if err != nil || len(items) != 1 || items[0].Fields["input_query"] != "query" || items[0].Identity != "run-one:case-1:1:1" {
		t.Fatalf("items = %+v, error = %v", items, err)
	}
	writeResults, err := client.BatchCreateDatasetItems(ctx, set.ID, []eval.DatasetItem{{
		ItemKey: "key-2",
		Fields:  map[string]string{"result_identity": "run-one:case-2:1:1", "input_query": "question"},
	}})
	if err != nil || len(writeResults) != 1 || writeResults[0].ItemID != "item-2" || !writeResults[0].IsNewItem {
		t.Fatalf("write results = %+v, error = %v", writeResults, err)
	}
	if createdItems["workspace_id"] != "workspace-123" || createdItems["is_skip_invalid_items"] != false || createdItems["is_allow_partial_add"] != false {
		t.Fatalf("item create request = %#v", createdItems)
	}
	version, err := client.CreateDatasetVersion(ctx, set.ID, eval.DatasetVersionSpec{Version: "0.0.0+run.run-one"})
	if err != nil || version.ID != "ver-2" || createdVersion["version"] != "0.0.0+run.run-one" {
		t.Fatalf("created version = %+v, request = %#v, error = %v", version, createdVersion, err)
	}
}

func TestOpenAPIEvaluationClientValidatesExistingDatasetSchema(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/loop/evaluation/evaluation_sets":
			_, _ = w.Write([]byte(`{"code":0,"data":{"sets":[{"id":"ds-1","dataset_key":"interview-agent-evaluation"}]}}`))
		case "/v1/loop/evaluation/evaluation_sets/ds-1":
			fields := make([]map[string]any, 0)
			for _, field := range eval.DefaultEvaluationDatasetSpec().Fields {
				fields = append(fields, map[string]any{"name": field.Name, "is_required": field.Required, "schema_key": "string", "content_type": "text"})
			}
			encoded, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]any{
				"evaluation_set": map[string]any{"current_version": map[string]any{"evaluation_set_schema": map[string]any{"field_schemas": fields}}},
			}})
			_, _ = w.Write(encoded)
		default:
			t.Errorf("unexpected request path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := newOpenAPIEvaluationClient(server.URL, "workspace", "token", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.EnsureDataset(context.Background(), eval.DefaultEvaluationDatasetSpec()); err != nil {
		t.Fatalf("EnsureDataset() error = %v", err)
	}
}

func TestOpenAPIEvaluationClientDoesNotLeakResponseBodyOrToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`sensitive response containing test-token`))
	}))
	defer server.Close()
	client, err := newOpenAPIEvaluationClient(server.URL, "workspace", "test-token", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	err = client.request(context.Background(), http.MethodGet, "/resource", nil, nil, nil)
	if err == nil || strings.Contains(err.Error(), "test-token") || strings.Contains(err.Error(), "sensitive response") {
		t.Fatalf("error = %v, expected sanitized response error", err)
	}
}

func TestEvaluationClientRechecksConsentBeforeEveryRequest(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"code":0,"data":{"sets":[]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":0,"data":{"evaluation_set_id":"ds-1"}}`))
	}))
	defer server.Close()
	consentPath := t.TempDir() + "/consent.json"
	if err := GrantContentConsent(consentPath, "workspace", server.URL, []string{ConsentScopeEvaluationContent}, time.Now()); err != nil {
		t.Fatal(err)
	}
	client, err := NewEvaluationClient(config.CozeLoopConfig{
		Enabled:                        true,
		EvaluationEnabled:              true,
		EvaluationContentUploadEnabled: true,
		APIBaseURL:                     server.URL,
		ConsentPath:                    consentPath,
		WorkspaceID:                    "workspace",
		APIToken:                       "secret-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.EnsureDataset(context.Background(), eval.DefaultEvaluationDatasetSpec()); err != nil {
		t.Fatal(err)
	}
	if requestCount != 2 {
		t.Fatalf("request count = %d, want 2", requestCount)
	}
	if err := RevokeContentConsent(consentPath, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.EnsureDataset(context.Background(), eval.DefaultEvaluationDatasetSpec()); err == nil {
		t.Fatal("request after consent revocation succeeded")
	}
	if requestCount != 2 {
		t.Fatalf("request count after revocation = %d, want 2", requestCount)
	}
}

func TestOpenAPIEvaluationClientRejectsUnsafeBaseURLs(t *testing.T) {
	for _, baseURL := range []string{
		"https://user:pass@example.com",
		"http://example.com",
		"https://example.com?token=secret",
	} {
		if _, err := newOpenAPIEvaluationClient(baseURL, "workspace", "token", nil); err == nil {
			t.Errorf("newOpenAPIEvaluationClient(%q) error = nil", baseURL)
		}
	}
}

func TestOpenAPIEvaluationClientCrossHostRedirectDoesNotForwardAuthorization(t *testing.T) {
	var secondaryReceived bool
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondaryReceived = true
		if r.Header.Get("Authorization") != "" {
			t.Errorf("authorization was forwarded across host: %q", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer secondary.Close()
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, secondary.URL+r.URL.Path, http.StatusFound)
	}))
	defer primary.Close()
	client, err := newOpenAPIEvaluationClient(primary.URL, "workspace", "secret-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	err = client.request(context.Background(), http.MethodGet, "/resource", nil, nil, nil)
	if err == nil || secondaryReceived || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("error = %v; secondary received = %v", err, secondaryReceived)
	}
}
