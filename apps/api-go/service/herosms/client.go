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
	ErrUnauthorized    = errors.New("hero sms unauthorized")
	ErrNotFound        = errors.New("hero sms not found")
	ErrInvalidRequest  = errors.New("hero sms invalid request")
	ErrRateLimited     = errors.New("hero sms rate limited")
	ErrUpstreamBusy    = errors.New("hero sms upstream busy")
	ErrBadResponse     = errors.New("hero sms bad response")
	ErrUpstreamTimeout = errors.New("hero sms timeout")
)

type Domain struct {
	ID           string          `json:"id"`
	Site         string          `json:"site"`
	Domain       string          `json:"domain"`
	Stock        int             `json:"stock"`
	CostUSD      decimal.Decimal `json:"-"`
	Currency     string          `json:"currency"`
	CurrencyCode int             `json:"currency_code"`
}

type ListDomainsResponse struct {
	Data  []Domain `json:"data"`
	Page  int      `json:"page"`
	Size  int      `json:"size"`
	Total int      `json:"total"`
}

type EmailListItem struct {
	ID    string `json:"id"`
	Email string `json:"email"`
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
	Code         string          `json:"code"`
	Message      string          `json:"message"`
	Status       string          `json:"status"`
	DomainID     string          `json:"domain_id"`
	CostUSD      decimal.Decimal `json:"-"`
	Currency     string          `json:"currency"`
	CurrencyCode int             `json:"currency_code"`
}

type BatchPurchaseResult struct {
	Items []EmailRecord `json:"items"`
}

type Client interface {
	ListDomains(ctx context.Context, page int, size int, site string) (*ListDomainsResponse, error)
	ListEmails(ctx context.Context, page int, size int) (*ListEmailsResponse, error)
	CreateEmail(ctx context.Context, domainID string) (*EmailRecord, error)
	CreateEmailBatch(ctx context.Context, domainID string, amount int) (*BatchPurchaseResult, error)
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

func (c *HTTPClient) ListDomains(ctx context.Context, page int, size int, site string) (*ListDomainsResponse, error) {
	query := url.Values{}
	query.Set("page", strconv.Itoa(page))
	query.Set("size", strconv.Itoa(size))
	if strings.TrimSpace(site) != "" {
		query.Set("site", strings.TrimSpace(site))
	}
	body, err := c.doJSON(ctx, http.MethodGet, "/emails/domains", query, nil)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Data  []map[string]any `json:"data"`
		Page  int              `json:"page"`
		Size  int              `json:"size"`
		Total int              `json:"total"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("%w: decode domains response", ErrBadResponse)
	}
	response := &ListDomainsResponse{Page: raw.Page, Size: raw.Size, Total: raw.Total, Data: make([]Domain, 0, len(raw.Data))}
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
	body, err := c.doJSON(ctx, http.MethodGet, "/emails", query, nil)
	if err != nil {
		return nil, err
	}
	var response ListEmailsResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("%w: decode email list response", ErrBadResponse)
	}
	return &response, nil
}

func (c *HTTPClient) CreateEmail(ctx context.Context, domainID string) (*EmailRecord, error) {
	body, err := c.doJSON(ctx, http.MethodPost, "/emails", nil, map[string]any{"id": domainID})
	if err != nil {
		return nil, err
	}
	return decodeEmailRecord(body)
}

func (c *HTTPClient) CreateEmailBatch(ctx context.Context, domainID string, amount int) (*BatchPurchaseResult, error) {
	body, err := c.doJSON(ctx, http.MethodPost, "/emails/batch", nil, map[string]any{"id": domainID, "amount": amount})
	if err != nil {
		return nil, err
	}
	var raw struct {
		Items []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("%w: decode batch response", ErrBadResponse)
	}
	result := &BatchPurchaseResult{Items: make([]EmailRecord, 0, len(raw.Items))}
	for _, item := range raw.Items {
		record, err := decodeEmailRecordMap(item)
		if err != nil {
			return nil, err
		}
		result.Items = append(result.Items, record)
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
	_, err := c.doJSON(ctx, http.MethodDelete, "/emails/"+url.PathEscape(id), nil, nil)
	return err
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
		ctx = context.Background()
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
			return nil, err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(requestCtx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
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
	if err != nil {
		return Domain{}, fmt.Errorf("%w: invalid domain cost", ErrBadResponse)
	}
	currencyCode, err := intFromAny(item["currency_code"])
	if err != nil {
		return Domain{}, fmt.Errorf("%w: invalid currency code", ErrBadResponse)
	}
	stock, err := intFromAny(item["stock"])
	if err != nil {
		return Domain{}, fmt.Errorf("%w: invalid stock", ErrBadResponse)
	}
	domain := Domain{
		ID:           stringFromAny(item["id"]),
		Site:         stringFromAny(item["site"]),
		Domain:       stringFromAny(item["domain"]),
		Stock:        stock,
		CostUSD:      cost,
		Currency:     stringFromAny(item["currency"]),
		CurrencyCode: currencyCode,
	}
	if domain.ID == "" || domain.Domain == "" {
		return Domain{}, fmt.Errorf("%w: missing domain fields", ErrBadResponse)
	}
	return domain, nil
}

func decodeEmailRecord(body []byte) (*EmailRecord, error) {
	var item map[string]any
	if err := json.Unmarshal(body, &item); err != nil {
		return nil, fmt.Errorf("%w: decode email record", ErrBadResponse)
	}
	record, err := decodeEmailRecordMap(item)
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func decodeEmailRecordMap(item map[string]any) (EmailRecord, error) {
	record := EmailRecord{
		ID:       stringFromAny(item["id"]),
		Email:    stringFromAny(item["email"]),
		Code:     stringFromAny(item["code"]),
		Message:  stringFromAny(item["message"]),
		Status:   stringFromAny(item["status"]),
		DomainID: stringFromAny(item["domain_id"]),
		Currency: stringFromAny(item["currency"]),
	}
	if rawCost, ok := item["cost"]; ok && rawCost != nil {
		cost, err := decimalFromAny(rawCost)
		if err != nil {
			return EmailRecord{}, fmt.Errorf("%w: invalid email cost", ErrBadResponse)
		}
		record.CostUSD = cost
	}
	if rawCode, ok := item["currency_code"]; ok && rawCode != nil {
		currencyCode, err := intFromAny(rawCode)
		if err != nil {
			return EmailRecord{}, fmt.Errorf("%w: invalid email currency code", ErrBadResponse)
		}
		record.CurrencyCode = currencyCode
	}
	if record.Email == "" {
		return EmailRecord{}, fmt.Errorf("%w: missing email field", ErrBadResponse)
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

func FindEmailByExactAddress(ctx context.Context, client Client, address string) (*EmailListItem, error) {
	trimmed := strings.TrimSpace(address)
	if trimmed == "" {
		return nil, nil
	}
	page := 1
	for {
		list, err := client.ListEmails(ctx, page, defaultPageSize)
		if err != nil {
			return nil, err
		}
		for _, item := range list.Data {
			if strings.EqualFold(strings.TrimSpace(item.Email), trimmed) {
				copied := item
				return &copied, nil
			}
		}
		if len(list.Data) == 0 || (list.Total > 0 && page*list.Size >= list.Total) {
			return nil, nil
		}
		page++
	}
}
