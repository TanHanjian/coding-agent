package cozeloop

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"interview-memory-agent/backend/internal/eval"
	"interview-memory-agent/backend/internal/infrastructure/config"
)

const (
	defaultEvaluationAPIBaseURL = "https://api.coze.cn"
	maxEvaluationResponseBytes  = 4 << 20
	maxEvaluationPageCount      = 100
	maxEvaluationBatchSize      = 100
	evaluatorRequestTimeout     = 5 * time.Minute
)

type OpenAPIEvaluationClient struct {
	baseURL              *url.URL
	workspaceID          string
	token                string
	httpClient           *http.Client
	consentPath          string
	requireConsent       bool
	contentUploadEnabled bool
}

func NewEvaluatorClient(cfg config.CozeLoopConfig) (eval.EvaluatorPlatform, error) {
	if !cfg.EvaluationEnabled {
		return nil, errors.New("CozeLoop evaluation is disabled; set COZELOOP_EVALUATION_ENABLED=true")
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate CozeLoop evaluator configuration: %w", err)
	}
	if !cfg.Enabled {
		return nil, errors.New("CozeLoop evaluator management requires COZELOOP_ENABLED=true")
	}
	baseURL := strings.TrimSpace(cfg.APIBaseURL)
	if baseURL == "" {
		baseURL = defaultEvaluationAPIBaseURL
	}
	client, err := newOpenAPIEvaluationClient(baseURL, cfg.WorkspaceID, cfg.APIToken, nil)
	if err != nil {
		return nil, err
	}
	client.consentPath = cfg.ConsentPath
	client.requireConsent = true
	client.contentUploadEnabled = cfg.EvaluationContentUploadEnabled
	client.httpClient.Timeout = evaluatorRequestTimeout
	return client, nil
}

func NewEvaluationClient(cfg config.CozeLoopConfig) (*OpenAPIEvaluationClient, error) {
	if !cfg.EvaluationEnabled {
		return nil, errors.New("CozeLoop evaluation is disabled; set COZELOOP_EVALUATION_ENABLED=true")
	}
	if !cfg.EvaluationContentUploadEnabled {
		return nil, errors.New("evaluation content upload is disabled; set COZELOOP_EVALUATION_CONTENT_UPLOAD_ENABLED=true")
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate CozeLoop evaluation configuration: %w", err)
	}
	if !cfg.Enabled {
		return nil, errors.New("CozeLoop evaluation requires COZELOOP_ENABLED=true")
	}
	baseURL := strings.TrimSpace(cfg.APIBaseURL)
	if baseURL == "" {
		baseURL = defaultEvaluationAPIBaseURL
	}
	if err := RequireContentConsent(cfg.ConsentPath, cfg.WorkspaceID, baseURL, ConsentScopeEvaluationContent); err != nil {
		return nil, errors.New("active CozeLoop evaluation content authorization is required")
	}
	client, err := newOpenAPIEvaluationClient(baseURL, cfg.WorkspaceID, cfg.APIToken, nil)
	if err != nil {
		return nil, err
	}
	client.consentPath = cfg.ConsentPath
	client.requireConsent = true
	client.contentUploadEnabled = true
	return client, nil
}

func newOpenAPIEvaluationClient(baseURL, workspaceID, token string, httpClient *http.Client) (*OpenAPIEvaluationClient, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("CozeLoop evaluation API base URL is invalid")
	}
	if parsed.Scheme != "https" && parsed.Hostname() != "localhost" && parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "::1" {
		return nil, errors.New("CozeLoop evaluation API requires HTTPS except for loopback hosts")
	}
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(token) == "" {
		return nil, errors.New("CozeLoop evaluation workspace and API token are required")
	}
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) > 0 && !sameOrigin(req.URL, via[0].URL) {
					return http.ErrUseLastResponse
				}
				return nil
			},
		}
	}
	return &OpenAPIEvaluationClient{
		baseURL:     parsed,
		workspaceID: strings.TrimSpace(workspaceID),
		token:       strings.TrimSpace(token),
		httpClient:  httpClient,
	}, nil
}

func (c *OpenAPIEvaluationClient) EnsureDataset(ctx context.Context, spec eval.DatasetSpec) (eval.Dataset, error) {
	if strings.TrimSpace(spec.Key) == "" || strings.TrimSpace(spec.Name) == "" || len(spec.Fields) == 0 {
		return eval.Dataset{}, errors.New("evaluation dataset key, name, and fields are required")
	}
	sets, err := c.findDatasets(ctx, spec.Key)
	if err != nil {
		return eval.Dataset{}, err
	}
	if len(sets) > 1 {
		return eval.Dataset{}, errors.New("evaluation dataset key is not unique in the workspace")
	}
	if len(sets) == 1 {
		if err := c.validateDatasetSchema(ctx, sets[0].ID, spec); err != nil {
			return eval.Dataset{}, err
		}
		return sets[0], nil
	}

	fieldSchemas := make([]map[string]any, 0, len(spec.Fields))
	for _, field := range spec.Fields {
		fieldSchemas = append(fieldSchemas, map[string]any{
			"name":                   field.Name,
			"description":            field.Description,
			"content_type":           "text",
			"default_display_format": "plain_text",
			"is_required":            field.Required,
			"schema_key":             "string",
		})
	}
	requestBody := map[string]any{
		"workspace_id": c.workspaceID,
		"name":         spec.Name,
		"description":  spec.Description,
		"dataset_key":  spec.Key,
		"evaluation_set_schema": map[string]any{
			"field_schemas": fieldSchemas,
		},
	}
	var response struct {
		EvaluationSetID json.RawMessage `json:"evaluation_set_id"`
	}
	if err := c.request(ctx, http.MethodPost, "/v1/loop/evaluation/evaluation_sets", nil, requestBody, &response); err != nil {
		// Resolve a create race by re-reading the immutable dataset key.
		sets, listErr := c.findDatasets(ctx, spec.Key)
		if listErr == nil && len(sets) == 1 {
			if schemaErr := c.validateDatasetSchema(ctx, sets[0].ID, spec); schemaErr == nil {
				return sets[0], nil
			}
		}
		return eval.Dataset{}, err
	}
	id, err := parsePlatformID(response.EvaluationSetID)
	if err != nil {
		return eval.Dataset{}, errors.New("CozeLoop evaluation API returned an invalid evaluation set ID")
	}
	return eval.Dataset{ID: id, Key: spec.Key}, nil
}

func (c *OpenAPIEvaluationClient) findDatasets(ctx context.Context, datasetKey string) ([]eval.Dataset, error) {
	query := url.Values{}
	query.Set("workspace_id", c.workspaceID)
	query.Add("dataset_keys", datasetKey)
	query.Set("page_size", "100")
	var response struct {
		Sets []struct {
			ID         json.RawMessage `json:"id"`
			DatasetKey string          `json:"dataset_key"`
		} `json:"sets"`
	}
	if err := c.request(ctx, http.MethodGet, "/v1/loop/evaluation/evaluation_sets", query, nil, &response); err != nil {
		return nil, err
	}
	result := make([]eval.Dataset, 0, len(response.Sets))
	for _, item := range response.Sets {
		if item.DatasetKey != datasetKey {
			continue
		}
		id, err := parsePlatformID(item.ID)
		if err != nil {
			return nil, errors.New("CozeLoop evaluation API returned an invalid evaluation set ID")
		}
		result = append(result, eval.Dataset{ID: id, Key: item.DatasetKey})
	}
	return result, nil
}

func (c *OpenAPIEvaluationClient) validateDatasetSchema(ctx context.Context, datasetID string, spec eval.DatasetSpec) error {
	query := url.Values{}
	query.Set("workspace_id", c.workspaceID)
	var response struct {
		EvaluationSet struct {
			CurrentVersion struct {
				EvaluationSetSchema struct {
					FieldSchemas []struct {
						Name        string `json:"name"`
						SchemaKey   string `json:"schema_key"`
						ContentType string `json:"content_type"`
						Required    bool   `json:"is_required"`
					} `json:"field_schemas"`
				} `json:"evaluation_set_schema"`
			} `json:"current_version"`
		} `json:"evaluation_set"`
	}
	endpoint := path.Join("/v1/loop/evaluation/evaluation_sets", url.PathEscape(datasetID))
	if err := c.request(ctx, http.MethodGet, endpoint, query, nil, &response); err != nil {
		return err
	}
	type remoteField struct {
		required    bool
		schemaKey   string
		contentType string
	}
	actual := make(map[string]remoteField, len(response.EvaluationSet.CurrentVersion.EvaluationSetSchema.FieldSchemas))
	for _, field := range response.EvaluationSet.CurrentVersion.EvaluationSetSchema.FieldSchemas {
		actual[field.Name] = remoteField{required: field.Required, schemaKey: field.SchemaKey, contentType: field.ContentType}
	}
	if len(actual) == 0 {
		return errors.New("cannot verify existing CozeLoop evaluation dataset schema")
	}
	if len(actual) != len(spec.Fields) {
		return errors.New("existing CozeLoop evaluation dataset schema does not match the local schema")
	}
	for _, expected := range spec.Fields {
		field, exists := actual[expected.Name]
		if !exists || field.required != expected.Required || field.schemaKey != "string" || field.contentType != "text" {
			return errors.New("existing CozeLoop evaluation dataset schema does not match the local schema")
		}
	}
	return nil
}

func (c *OpenAPIEvaluationClient) ListDatasetVersions(ctx context.Context, datasetID string) ([]eval.DatasetVersionRecord, error) {
	versions := make([]eval.DatasetVersionRecord, 0)
	pageToken := ""
	for page := 0; page < maxEvaluationPageCount; page++ {
		query := url.Values{}
		query.Set("workspace_id", c.workspaceID)
		query.Set("page_size", "200")
		if pageToken != "" {
			query.Set("page_token", pageToken)
		}
		var response struct {
			Versions []struct {
				ID      json.RawMessage `json:"id"`
				Version string          `json:"version"`
			} `json:"versions"`
			NextPageToken string `json:"next_page_token"`
		}
		endpoint := path.Join("/v1/loop/evaluation/evaluation_sets", url.PathEscape(datasetID), "versions")
		if err := c.request(ctx, http.MethodGet, endpoint, query, nil, &response); err != nil {
			return nil, err
		}
		for _, item := range response.Versions {
			id, err := parsePlatformID(item.ID)
			if err != nil {
				return nil, errors.New("CozeLoop evaluation API returned an invalid dataset version ID")
			}
			versions = append(versions, eval.DatasetVersionRecord{ID: id, Version: item.Version})
		}
		if response.NextPageToken == "" {
			return versions, nil
		}
		if response.NextPageToken == pageToken {
			return nil, errors.New("CozeLoop evaluation API returned a repeated page token")
		}
		pageToken = response.NextPageToken
	}
	return nil, errors.New("CozeLoop evaluation API exceeded the dataset version page limit")
}

func (c *OpenAPIEvaluationClient) ListDatasetItems(ctx context.Context, datasetID, versionID string) ([]eval.DatasetItem, error) {
	items := make([]eval.DatasetItem, 0)
	pageToken := ""
	for page := 0; page < maxEvaluationPageCount; page++ {
		query := url.Values{}
		query.Set("workspace_id", c.workspaceID)
		query.Set("page_size", "200")
		if versionID != "" {
			query.Set("version_id", versionID)
		}
		if pageToken != "" {
			query.Set("page_token", pageToken)
		}
		var response struct {
			Items []struct {
				ItemKey string `json:"item_key"`
				Turns   []struct {
					FieldDatas []struct {
						Name    string `json:"name"`
						Content struct {
							Text           string `json:"text"`
							ContentOmitted bool   `json:"content_omitted"`
						} `json:"content"`
					} `json:"field_datas"`
				} `json:"turns"`
			} `json:"items"`
			NextPageToken string `json:"next_page_token"`
		}
		endpoint := path.Join("/v1/loop/evaluation/evaluation_sets", url.PathEscape(datasetID), "items")
		if err := c.request(ctx, http.MethodGet, endpoint, query, nil, &response); err != nil {
			return nil, err
		}
		for _, remote := range response.Items {
			if len(remote.Turns) > 1 {
				return nil, errors.New("CozeLoop evaluation dataset contains unsupported multi-turn result items")
			}
			fields := make(map[string]string)
			contentOmitted := false
			if len(remote.Turns) == 1 {
				for _, field := range remote.Turns[0].FieldDatas {
					fields[field.Name] = field.Content.Text
					contentOmitted = contentOmitted || field.Content.ContentOmitted
				}
			}
			identity := fields["result_identity"]
			contentHash := strings.TrimSpace(fields["sync_content_hash"])
			if contentHash == "" {
				encoded, err := json.Marshal(fields)
				if err != nil {
					return nil, errors.New("cannot hash CozeLoop evaluation dataset item")
				}
				hash := sha256.Sum256(encoded)
				contentHash = hex.EncodeToString(hash[:])
			} else {
				decoded, err := hex.DecodeString(contentHash)
				if err != nil || len(decoded) != sha256.Size {
					return nil, errors.New("CozeLoop evaluation item contains an invalid synchronization hash")
				}
				if !contentOmitted {
					delete(fields, "sync_content_hash")
					encoded, err := json.Marshal(fields)
					if err != nil {
						return nil, errors.New("cannot verify CozeLoop evaluation dataset item hash")
					}
					hash := sha256.Sum256(encoded)
					if hex.EncodeToString(hash[:]) != contentHash {
						return nil, errors.New("CozeLoop evaluation item content does not match its synchronization hash")
					}
					fields["sync_content_hash"] = contentHash
				}
			}
			items = append(items, eval.DatasetItem{
				ItemKey:     remote.ItemKey,
				Identity:    identity,
				ContentHash: contentHash,
				Fields:      fields,
			})
		}
		if response.NextPageToken == "" {
			return items, nil
		}
		if response.NextPageToken == pageToken {
			return nil, errors.New("CozeLoop evaluation API returned a repeated page token")
		}
		pageToken = response.NextPageToken
	}
	return nil, errors.New("CozeLoop evaluation API exceeded the dataset item page limit")
}

func (c *OpenAPIEvaluationClient) BatchCreateDatasetItems(ctx context.Context, datasetID string, items []eval.DatasetItem) ([]eval.ItemWriteResult, error) {
	if len(items) == 0 {
		return nil, nil
	}
	results := make([]eval.ItemWriteResult, 0, len(items))
	for start := 0; start < len(items); start += maxEvaluationBatchSize {
		end := start + maxEvaluationBatchSize
		if end > len(items) {
			end = len(items)
		}
		chunk := items[start:end]
		remoteItems := make([]map[string]any, 0, len(chunk))
		for _, item := range chunk {
			fieldNames := make([]string, 0, len(item.Fields))
			for name := range item.Fields {
				fieldNames = append(fieldNames, name)
			}
			sort.Strings(fieldNames)
			fieldDatas := make([]map[string]any, 0, len(fieldNames))
			for _, name := range fieldNames {
				fieldDatas = append(fieldDatas, map[string]any{
					"name": name,
					"content": map[string]any{
						"content_type": "text",
						"text":         item.Fields[name],
					},
				})
			}
			remoteItems = append(remoteItems, map[string]any{
				"item_key": item.ItemKey,
				"turns":    []any{map[string]any{"field_datas": fieldDatas}},
			})
		}
		requestBody := map[string]any{
			"workspace_id":          c.workspaceID,
			"items":                 remoteItems,
			"is_skip_invalid_items": false,
			"is_allow_partial_add":  false,
		}
		type itemOutput struct {
			ItemKey       string          `json:"item_key"`
			ItemID        json.RawMessage `json:"item_id"`
			ItemVersion   string          `json:"item_version"`
			IsNewItem     bool            `json:"is_new_item"`
			ItemVersionID json.RawMessage `json:"item_version_id"`
		}
		var response struct {
			ItemOutputs      []itemOutput      `json:"itemOutputs"`
			ItemOutputsSnake []itemOutput      `json:"item_outputs"`
			Errors           []json.RawMessage `json:"errors"`
		}
		endpoint := path.Join("/v1/loop/evaluation/evaluation_sets", url.PathEscape(datasetID), "items")
		if err := c.request(ctx, http.MethodPost, endpoint, nil, requestBody, &response); err != nil {
			return nil, err
		}
		if len(response.Errors) > 0 {
			return nil, fmt.Errorf("CozeLoop rejected %d evaluation dataset item(s)", len(response.Errors))
		}
		outputs := response.ItemOutputs
		if len(outputs) == 0 {
			outputs = response.ItemOutputsSnake
		}
		if len(outputs) != len(chunk) {
			return nil, errors.New("CozeLoop evaluation API returned an incomplete item batch response")
		}
		requestedKeys := make(map[string]struct{}, len(chunk))
		for _, item := range chunk {
			requestedKeys[item.ItemKey] = struct{}{}
		}
		seenKeys := make(map[string]struct{}, len(outputs))
		for _, output := range outputs {
			if output.ItemKey != "" {
				if _, requested := requestedKeys[output.ItemKey]; !requested {
					return nil, errors.New("CozeLoop evaluation API returned an unexpected item key")
				}
				if _, duplicate := seenKeys[output.ItemKey]; duplicate {
					return nil, errors.New("CozeLoop evaluation API returned duplicate item keys")
				}
				seenKeys[output.ItemKey] = struct{}{}
			}
			itemID := ""
			if len(output.ItemID) > 0 && string(output.ItemID) != "null" {
				var err error
				itemID, err = parsePlatformID(output.ItemID)
				if err != nil {
					return nil, errors.New("CozeLoop evaluation API returned an invalid item ID")
				}
			}
			results = append(results, eval.ItemWriteResult{
				ItemKey:     output.ItemKey,
				ItemID:      itemID,
				ItemVersion: output.ItemVersion,
				IsNewItem:   output.IsNewItem,
			})
		}
	}
	return results, nil
}

func (c *OpenAPIEvaluationClient) CreateDatasetVersion(ctx context.Context, datasetID string, spec eval.DatasetVersionSpec) (eval.DatasetVersionRecord, error) {
	if strings.TrimSpace(spec.Version) == "" {
		return eval.DatasetVersionRecord{}, errors.New("evaluation dataset version is required")
	}
	requestBody := map[string]any{
		"workspace_id": c.workspaceID,
		"version":      spec.Version,
		"description":  spec.Description,
	}
	var response struct {
		VersionID json.RawMessage `json:"version_id"`
	}
	endpoint := path.Join("/v1/loop/evaluation/evaluation_sets", url.PathEscape(datasetID), "versions")
	if err := c.request(ctx, http.MethodPost, endpoint, nil, requestBody, &response); err != nil {
		return eval.DatasetVersionRecord{}, err
	}
	id, err := parsePlatformID(response.VersionID)
	if err != nil {
		return eval.DatasetVersionRecord{}, errors.New("CozeLoop evaluation API returned an invalid dataset version ID")
	}
	return eval.DatasetVersionRecord{ID: id, Version: spec.Version}, nil
}

func (c *OpenAPIEvaluationClient) request(ctx context.Context, method, endpoint string, query url.Values, body any, output any) error {
	if c.requireConsent {
		if !c.contentUploadEnabled {
			return errors.New("CozeLoop evaluation content upload is disabled")
		}
		if err := RequireContentConsent(c.consentPath, c.workspaceID, c.baseURL.String(), ConsentScopeEvaluationContent); err != nil {
			return errors.New("CozeLoop evaluation content authorization is no longer active")
		}
	}
	return c.sendRequest(ctx, method, endpoint, query, body, output)
}

// requestMetadata is reserved for evaluator definition/version management,
// whose payload contains no evaluation case or candidate output content.
func (c *OpenAPIEvaluationClient) requestMetadata(ctx context.Context, method, endpoint string, query url.Values, body any, output any) error {
	return c.sendRequest(ctx, method, endpoint, query, body, output)
}

// requestContent gates every request that carries complete evaluation inputs or
// outputs. It intentionally rechecks both the feature switch and current grant
// for every Validate/Debug call.
func (c *OpenAPIEvaluationClient) requestContent(ctx context.Context, method, endpoint string, query url.Values, body any, output any) error {
	if !c.contentUploadEnabled {
		return errors.New("CozeLoop evaluation content upload is disabled")
	}
	if err := RequireContentConsent(c.consentPath, c.workspaceID, c.baseURL.String(), ConsentScopeEvaluationContent); err != nil {
		return errors.New("CozeLoop evaluation content authorization is no longer active")
	}
	return c.sendRequest(ctx, method, endpoint, query, body, output)
}

func (c *OpenAPIEvaluationClient) sendRequest(ctx context.Context, method, endpoint string, query url.Values, body any, output any) error {
	target := *c.baseURL
	target.Path = path.Join(target.Path, endpoint)
	target.RawPath = ""
	target.RawQuery = query.Encode()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return errors.New("cannot encode CozeLoop evaluation request")
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, target.String(), reader)
	if err != nil {
		return errors.New("cannot create CozeLoop evaluation request")
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("cozeloop-workspace-id", c.workspaceID)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("CozeLoop evaluation request failed: %s", classifyEvaluationRequestError(err))
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxEvaluationResponseBytes+1))
	if err != nil {
		return errors.New("cannot read CozeLoop evaluation response")
	}
	if len(data) > maxEvaluationResponseBytes {
		return errors.New("CozeLoop evaluation response exceeded the size limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		switch response.StatusCode {
		case http.StatusUnauthorized:
			return errors.New("CozeLoop evaluation authentication failed (HTTP 401)")
		case http.StatusForbidden:
			return errors.New("CozeLoop evaluation permission denied (HTTP 403)")
		default:
			return fmt.Errorf("CozeLoop evaluation API returned HTTP %d", response.StatusCode)
		}
	}
	var envelope struct {
		Code *int            `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return errors.New("CozeLoop evaluation API returned invalid JSON")
	}
	if envelope.Code != nil && *envelope.Code != 0 {
		return fmt.Errorf("CozeLoop evaluation API rejected the request (code %d)", *envelope.Code)
	}
	if output == nil || len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(envelope.Data, output); err != nil {
		return errors.New("CozeLoop evaluation API returned an invalid response payload")
	}
	return nil
}

func parsePlatformID(value json.RawMessage) (string, error) {
	if len(value) == 0 || string(value) == "null" {
		return "", errors.New("id is missing")
	}
	var asString string
	if err := json.Unmarshal(value, &asString); err == nil {
		if strings.TrimSpace(asString) == "" {
			return "", errors.New("id is empty")
		}
		return asString, nil
	}
	var number json.Number
	if err := json.Unmarshal(value, &number); err != nil {
		return "", err
	}
	if _, err := strconv.ParseInt(number.String(), 10, 64); err != nil {
		return "", err
	}
	return number.String(), nil
}

func sameOrigin(left, right *url.URL) bool {
	return left != nil && right != nil && strings.EqualFold(left.Scheme, right.Scheme) && strings.EqualFold(left.Host, right.Host)
}

func classifyEvaluationRequestError(err error) string {
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return "transport_error"
}

var _ eval.EvaluationPlatform = (*OpenAPIEvaluationClient)(nil)
