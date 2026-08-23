package herosms

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/shopspring/decimal"
)

const (
	DefaultBaseURL   = "https://hero-sms.com/api/v1"
	defaultTimeout   = 15 * time.Second
	defaultBodyLimit = int64(256 << 10)
	defaultPageSize  = 100
)

var (
	ErrUnauthorized       = errors.New("hero sms unauthorized")
	ErrNotFound           = errors.New("hero sms not found")
	ErrInvalidRequest     = errors.New("hero sms invalid request")
	ErrRateLimited        = errors.New("hero sms rate limited")
	ErrUpstreamBusy       = errors.New("hero sms upstream busy")
	ErrBadResponse        = errors.New("hero sms bad response")
	ErrBatchCountMismatch = errors.New("hero sms batch count mismatch")
	ErrUpstreamTimeout    = errors.New("hero sms timeout")
)

type Domain struct {
	Name    string          `json:"name"`
	Count   int             `json:"count"`
	CostUSD decimal.Decimal `json:"-"`
}

type ListDomainsResponse struct {
	Data []Domain `json:"data"`
}

type EmailListItem struct {
	ID     string `json:"id"`
	Email  string `json:"email"`
	Site   string `json:"site"`
	Status string `json:"status"`
	Date   string `json:"date"`
}

type ListEmailsResponse struct {
	Data  []EmailListItem `json:"data"`
	Page  int             `json:"page"`
	Size  int             `json:"size"`
	Total int             `json:"total"`
}

type EmailRecord struct {
	ID           string          `json:"id"`
	Email        string          `json:"email"`
	Code         string          `json:"value"`
	Message      string          `json:"message"`
	Status       string          `json:"status"`
	Site         string          `json:"site"`
	Domain       string          `json:"domain"`
	Date         string          `json:"date"`
	CostUSD      decimal.Decimal `json:"-"`
	CurrencyCode int             `json:"currency"`
}

type BatchPurchaseResult struct {
	Items []EmailRecord `json:"items"`
	Count int           `json:"count"`
}

type Client interface {
	ListDomains(ctx context.Context, site string) (*ListDomainsResponse, error)
	ListEmails(ctx context.Context, page int, size int) (*ListEmailsResponse, error)
	CreateEmail(ctx context.Context, site string, domain string) (*EmailRecord, error)
	CreateEmailBatch(ctx context.Context, site string, domain string, count int) (*BatchPurchaseResult, error)
	GetEmail(ctx context.Context, id string) (*EmailRecord, error)
	DeleteEmail(ctx context.Context, id string) error
	ReorderEmail(ctx context.Context, id string) (*EmailRecord, error)
}

type HTTPClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	bodyLimit  int64
	timeout    time.Duration
}

func NewClient(baseURL string, apiKey string) *HTTPClient {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultBaseURL
	}
	transport := &http.Transport{
		Proxy: nil,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}
	return &HTTPClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  strings.TrimSpace(apiKey),
		httpClient: &http.Client{
			Transport: transport,
			CheckRedirect: func(request *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		bodyLimit: defaultBodyLimit,
		timeout:   defaultTimeout,
	}
}

func (c *HTTPClient) TimeoutForTest(timeout time.Duration) {
	if c != nil && timeout > 0 {
		c.timeout = timeout
	}
}

func (c *HTTPClient) ListDomains(ctx context.Context, site string) (*ListDomainsResponse, error) {
	query := url.Values{}
	if strings.TrimSpace(site) != "" {
		query.Set("site", strings.TrimSpace(site))
	}
	body, err := c.doJSON(ctx, http.MethodGet, "/emails/domains", query, nil)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Data []map[string]any `json:"data"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("%w: decode domains response", ErrBadResponse)
	}
	response := &ListDomainsResponse{Data: make([]Domain, 0, len(raw.Data))}
	for _, item := range raw.Data {
		domain, err := decodeDomain(item)
		if err != nil {
			return nil, err
		}
		response.Data = append(response.Data, domain)
	}
	return response, nil
}

func (c *HTTPClient) ListEmails(ctx context.Context, page int, size int) (*ListEmailsResponse, error) {
	query := url.Values{}
	query.Set("page", strconv.Itoa(page))
	query.Set("size", strconv.Itoa(size))
	query.Set("sort[id]", "desc")
	body, err := c.doJSON(ctx, http.MethodGet, "/emails", query, nil)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Data  []map[string]any `json:"data"`
		Page  int              `json:"page"`
		Size  int              `json:"size"`
		Total int              `json:"total"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("%w: decode email list response", ErrBadResponse)
	}
	response := &ListEmailsResponse{Page: raw.Page, Size: raw.Size, Total: raw.Total, Data: make([]EmailListItem, 0, len(raw.Data))}
	if response.Page <= 0 {
		response.Page = page
	}
	if response.Size <= 0 {
		response.Size = size
	}
	for _, item := range raw.Data {
		record, err := decodeEmailRecordMap(item)
		if err != nil {
			return nil, err
		}
		response.Data = append(response.Data, EmailListItem{ID: record.ID, Email: record.Email, Site: record.Site, Status: record.Status, Date: record.Date})
	}
	if response.Total <= 0 {
		response.Total = len(response.Data)
	}
	return response, nil
}

func (c *HTTPClient) SearchEmails(ctx context.Context, search string, size int) (*ListEmailsResponse, error) {
	query := url.Values{}
	query.Set("page", "1")
	query.Set("size", strconv.Itoa(size))
	query.Set("sort[id]", "desc")
	query.Set("search", strings.TrimSpace(search))
	body, err := c.doJSON(ctx, http.MethodGet, "/emails", query, nil)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Data []map[string]any `json:"data"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("%w: decode email search response", ErrBadResponse)
	}
	response := &ListEmailsResponse{Page: 1, Size: size, Total: len(raw.Data), Data: make([]EmailListItem, 0, len(raw.Data))}
	for _, item := range raw.Data {
		record, err := decodeEmailRecordMap(item)
		if err != nil {
			return nil, fmt.Errorf("decode searched HeroSMS email: %w", err)
		}
		response.Data = append(response.Data, EmailListItem{ID: record.ID, Email: record.Email, Site: record.Site, Status: record.Status, Date: record.Date})
	}
	return response, nil
}

func (c *HTTPClient) CreateEmail(ctx context.Context, site string, domain string) (*EmailRecord, error) {
	body, err := c.doJSON(ctx, http.MethodPost, "/emails", nil, map[string]any{"site": site, "domain": domain})
	if err != nil {
		return nil, err
	}
	return decodeEmailRecord(body)
}

func (c *HTTPClient) CreateEmailBatch(ctx context.Context, site string, domain string, count int) (*BatchPurchaseResult, error) {
	body, err := c.doJSON(ctx, http.MethodPost, "/emails/batch", nil, map[string]any{"site": site, "domain": domain, "count": count})
	if err != nil {
		return nil, err
	}
	var raw struct {
		Items []map[string]any `json:"data"`
		Meta  struct {
			Count int `json:"count"`
		} `json:"meta"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("%w: decode batch response", ErrBadResponse)
	}
	result := &BatchPurchaseResult{Items: make([]EmailRecord, 0, len(raw.Items)), Count: raw.Meta.Count}
	for _, item := range raw.Items {
		record, err := decodeEmailRecordMap(item)
		if err != nil {
			return nil, err
		}
		result.Items = append(result.Items, record)
	}
	if len(result.Items) != count || result.Count != count {
		return result, fmt.Errorf("%w: requested=%d returned=%d meta=%d", ErrBatchCountMismatch, count, len(result.Items), result.Count)
	}
	return result, nil
}

func (c *HTTPClient) GetEmail(ctx context.Context, id string) (*EmailRecord, error) {
	body, err := c.doJSON(ctx, http.MethodGet, "/emails/"+url.PathEscape(id), nil, nil)
	if err != nil {
		return nil, err
	}
	return decodeEmailRecord(body)
}

func (c *HTTPClient) DeleteEmail(ctx context.Context, id string) error {
	body, err := c.doJSON(ctx, http.MethodDelete, "/emails/"+url.PathEscape(id), nil, nil)
	if err != nil {
		return fmt.Errorf("delete HeroSMS email: %w", err)
	}
	if len(body) != 0 {
		return fmt.Errorf("%w: cancellation returned an unexpected body", ErrBadResponse)
	}
	return nil
}

func (c *HTTPClient) ReorderEmail(ctx context.Context, id string) (*EmailRecord, error) {
	body, err := c.doJSON(ctx, http.MethodPost, "/emails/"+url.PathEscape(id)+"/reorder", nil, nil)
	if err != nil {
		return nil, err
	}
	return decodeEmailRecord(body)
}

func (c *HTTPClient) doJSON(ctx context.Context, method string, path string, query url.Values, payload any) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: request context is required", ErrInvalidRequest)
	}
	requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	endpoint := c.baseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("encode HeroSMS request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(requestCtx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", c.apiKey)
	// Keep the legacy header during the provider's documented auth migration.
	request.Header.Set("ApiKey", c.apiKey)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		if isTimeoutError(err) || errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
			return nil, ErrUpstreamTimeout
		}
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return nil, ErrBadResponse
	}
	if err := common.LimitResponseBody(response, c.bodyLimit); err != nil {
		return nil, ErrBadResponse
	}
	data, err := common.ReadAllLimit(response.Body, c.bodyLimit)
	if err != nil {
		if errors.Is(err, common.ErrLimitExceeded) {
			return nil, ErrBadResponse
		}
		return nil, err
	}
	if response.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return data, nil
	}
	switch response.StatusCode {
	case http.StatusUnauthorized:
		return nil, ErrUnauthorized
	case http.StatusNotFound:
		return nil, ErrNotFound
	case http.StatusUnprocessableEntity, http.StatusBadRequest:
		return nil, ErrInvalidRequest
	case http.StatusTooManyRequests:
		return nil, ErrRateLimited
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return nil, ErrUpstreamBusy
	default:
		return nil, ErrBadResponse
	}
}

func decodeDomain(item map[string]any) (Domain, error) {
	cost, err := decimalFromAny(item["cost"])
	if err != nil || cost.IsNegative() {
		return Domain{}, fmt.Errorf("%w: invalid domain cost", ErrBadResponse)
	}
	count, err := intFromAny(item["count"])
	if err != nil || count < 0 {
		return Domain{}, fmt.Errorf("%w: invalid domain count", ErrBadResponse)
	}
	domain := Domain{Name: stringFromAny(item["name"]), Count: count, CostUSD: cost}
	if domain.Name == "" {
		return Domain{}, fmt.Errorf("%w: missing domain name", ErrBadResponse)
	}
	return domain, nil
}

func decodeEmailRecord(body []byte) (*EmailRecord, error) {
	envelope := make(map[string]any)
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&envelope); err != nil {
		return nil, fmt.Errorf("%w: decode email record", ErrBadResponse)
	}
	item, ok := envelope["data"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: missing email data", ErrBadResponse)
	}
	record, err := decodeEmailRecordMap(item)
	if err != nil {
		return nil, fmt.Errorf("decode HeroSMS email data: %w", err)
	}
	return &record, nil
}

func decodeEmailRecordMap(item map[string]any) (EmailRecord, error) {
	record := EmailRecord{
		ID:      stringFromAny(item["id"]),
		Email:   stringFromAny(item["email"]),
		Code:    stringFromAny(item["value"]),
		Message: stringFromAny(item["message"]),
		Status:  stringFromAny(item["status"]),
		Site:    stringFromAny(item["site"]),
		Domain:  stringFromAny(item["domain"]),
		Date:    stringFromAny(item["date"]),
	}
	if rawCost, ok := item["cost"]; ok && rawCost != nil {
		cost, err := decimalFromAny(rawCost)
		if err != nil || cost.IsNegative() {
			return EmailRecord{}, fmt.Errorf("%w: invalid email cost", ErrBadResponse)
		}
		record.CostUSD = cost
	}
	if rawCurrency, ok := item["currency"]; ok && rawCurrency != nil {
		currencyCode, err := intFromAny(rawCurrency)
		if err != nil {
			return EmailRecord{}, fmt.Errorf("%w: invalid email currency", ErrBadResponse)
		}
		record.CurrencyCode = currencyCode
	}
	if record.Email == "" || len(record.Email) > 320 {
		return EmailRecord{}, fmt.Errorf("%w: invalid email field", ErrBadResponse)
	}
	if len(record.Code) > 4096 || len(record.Message) > 64*1024 {
		return EmailRecord{}, fmt.Errorf("%w: activation content exceeds limits", ErrBadResponse)
	}
	return record, nil
}

func decimalFromAny(value any) (decimal.Decimal, error) {
	switch typed := value.(type) {
	case string:
		return decimal.NewFromString(strings.TrimSpace(typed))
	case float64:
		return decimal.NewFromString(strconv.FormatFloat(typed, 'f', -1, 64))
	case json.Number:
		return decimal.NewFromString(string(typed))
	case int:
		return decimal.NewFromInt(int64(typed)), nil
	case int64:
		return decimal.NewFromInt(typed), nil
	case nil:
		return decimal.Zero, nil
	default:
		return decimal.Zero, fmt.Errorf("unsupported decimal type %T", value)
	}
}

func intFromAny(value any) (int, error) {
	switch typed := value.(type) {
	case float64:
		return int(typed), nil
	case int:
		return typed, nil
	case int64:
		return int(typed), nil
	case json.Number:
		parsed, err := typed.Int64()
		return int(parsed), err
	case string:
		return strconv.Atoi(strings.TrimSpace(typed))
	default:
		return 0, fmt.Errorf("unsupported int type %T", value)
	}
}

func stringFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func isTimeoutError(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// pi-lens-ignore: go-bare-error
func FindEmailByExactAddress(ctx context.Context, client Client, address string) (*EmailListItem, error) {
	trimmed := strings.TrimSpace(address)
	if trimmed == "" {
		return nil, nil
	}
	if searcher, ok := client.(interface {
		SearchEmails(context.Context, string, int) (*ListEmailsResponse, error)
	}); ok {
		list, err := searcher.SearchEmails(ctx, trimmed, defaultPageSize)
		if err != nil {
			return nil, fmt.Errorf("search HeroSMS emails during reconciliation: %w", err)
		}
		for _, item := range list.Data {
			if strings.EqualFold(strings.TrimSpace(item.Email), trimmed) {
				copied := item
				return &copied, nil
			}
		}
		return nil, nil
	}
	for page := 1; page <= 10; page++ {
		list, err := client.ListEmails(ctx, page, defaultPageSize)
		if err != nil {
			return nil, fmt.Errorf("list HeroSMS emails during reconciliation: %w", err)
		}
		for _, item := range list.Data {
			if strings.EqualFold(strings.TrimSpace(item.Email), trimmed) {
				copied := item
				return &copied, nil
			}
		}
		if len(list.Data) == 0 || len(list.Data) < defaultPageSize || (list.Total > len(list.Data) && page*list.Size >= list.Total) {
			return nil, nil
		}
	}
	return nil, fmt.Errorf("%w: email lookup page limit exceeded", ErrBadResponse)
}
