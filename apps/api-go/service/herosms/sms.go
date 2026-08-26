package herosms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/shopspring/decimal"
)

var (
	ErrNoSMSNumbersAvailable       = errors.New("hero sms has no matching phone numbers")
	ErrProviderBalanceInsufficient = errors.New("hero sms provider balance is insufficient")
)

const (
	maxSMSOperators       = 256
	maxSMSOfferPriceTiers = 256
)

type SMSCountry struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	EnglishName string `json:"english_name"`
	ChineseName string `json:"chinese_name"`
	Visible     bool   `json:"visible"`
}

type SMSService struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type SMSPriceTier struct {
	Count      int             `json:"count"`
	Price      decimal.Decimal `json:"-"`
	PriceValue string          `json:"price"`
}

func parseSMSPriceValue(value string) (decimal.Decimal, bool) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 || strings.ContainsAny(value, "eE+-") {
		return decimal.Zero, false
	}
	digits := 0
	decimalPoints := 0
	for _, character := range value {
		if character == '.' {
			decimalPoints++
			if decimalPoints > 1 {
				return decimal.Zero, false
			}
			continue
		}
		if character < '0' || character > '9' {
			return decimal.Zero, false
		}
		digits++
	}
	if digits == 0 {
		return decimal.Zero, false
	}
	price, err := decimal.NewFromString(value)
	if err != nil || price.LessThanOrEqual(decimal.Zero) || price.GreaterThan(decimal.NewFromInt(1_000_000)) {
		return decimal.Zero, false
	}
	return price, true
}

type SMSOffer struct {
	CountryID  int             `json:"country_id"`
	Service    string          `json:"service"`
	Count      int             `json:"count"`
	Price      decimal.Decimal `json:"-"`
	PriceValue string          `json:"price"`
	Tiers      []SMSPriceTier  `json:"tiers"`
}

type SMSPurchaseRequest struct {
	CountryID    int
	Service      string
	Operator     string
	MaxPrice     decimal.Decimal
	CurrencyCode int
}

type SMSActivation struct {
	ID                 string          `json:"activation_id"`
	PhoneNumber        string          `json:"phone_number"`
	ActivationCost     decimal.Decimal `json:"-"`
	CostValue          string          `json:"activation_cost"`
	CurrencyCode       int             `json:"currency_code"`
	CountryCode        int             `json:"country_code"`
	CanGetAnother      bool            `json:"can_get_another_sms"`
	ActivationTime     string          `json:"activation_time"`
	ActivationEndTime  string          `json:"activation_end_time"`
	ActivationOperator string          `json:"activation_operator"`
}

type SMSStatus struct {
	Code string `json:"code"`
	Text string `json:"text"`
}

type SMSActiveActivation struct {
	ID             string          `json:"activation_id"`
	Service        string          `json:"service"`
	PhoneNumber    string          `json:"phone_number"`
	ActivationCost decimal.Decimal `json:"-"`
	CostValue      string          `json:"activation_cost"`
	CurrencyCode   int             `json:"currency_code"`
	Status         int             `json:"status"`
	SMSCode        string          `json:"sms_code"`
	SMSText        string          `json:"sms_text"`
	ActivationTime string          `json:"activation_time"`
	CountryCode    int             `json:"country_code"`
}

type SMSClient interface {
	ListSMSCountries(ctx context.Context) ([]SMSCountry, error)
	ListSMSServices(ctx context.Context) ([]SMSService, error)
	ListSMSOperators(ctx context.Context, countryID int) ([]string, error)
	GetSMSOffer(ctx context.Context, countryID int, service string) (*SMSOffer, error)
	PurchaseSMSActivation(ctx context.Context, request SMSPurchaseRequest) (*SMSActivation, error)
	GetSMSActivationStatus(ctx context.Context, activationID string) (*SMSStatus, error)
	SetSMSActivationStatus(ctx context.Context, activationID string, status int) error
	ListActiveSMSActivations(ctx context.Context) ([]SMSActiveActivation, error)
}

func (c *HTTPClient) ListSMSCountries(ctx context.Context) ([]SMSCountry, error) {
	body, err := c.doSMSActivate(ctx, "getCountries", nil)
	if err != nil {
		return nil, err
	}
	payload := make(map[string]struct {
		Russian string `json:"rus"`
		English string `json:"eng"`
		Chinese string `json:"chn"`
		Visible int    `json:"visible"`
	})
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, ErrBadResponse
	}
	countries := make([]SMSCountry, 0, len(payload))
	for rawID, item := range payload {
		id, err := strconv.Atoi(rawID)
		if err != nil {
			continue
		}
		name := strings.TrimSpace(item.Chinese)
		if name == "" {
			name = strings.TrimSpace(item.English)
		}
		countries = append(countries, SMSCountry{
			ID:          id,
			Name:        name,
			EnglishName: strings.TrimSpace(item.English),
			ChineseName: strings.TrimSpace(item.Chinese),
			Visible:     item.Visible != 0,
		})
	}
	return countries, nil
}

func (c *HTTPClient) ListSMSServices(ctx context.Context) ([]SMSService, error) {
	body, err := c.doSMSActivate(ctx, "getServicesList", nil)
	if err != nil {
		return nil, err
	}

	// HeroSMS currently wraps the catalog in
	// {"status":"success","services":[{"code":"tg","name":"Telegram"}]},
	// while older SMS-Activate-compatible deployments return {"tg":"Telegram"}.
	// Accept both documented shapes so a provider-side schema migration does not
	// make the entire phone-number activation panel unavailable.
	var envelope struct {
		Status   string       `json:"status"`
		Services []SMSService `json:"services"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Services != nil {
		if !strings.EqualFold(strings.TrimSpace(envelope.Status), "success") {
			return nil, ErrBadResponse
		}
		return normalizeSMSServices(envelope.Services), nil
	}

	payload := make(map[string]string)
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, ErrBadResponse
	}
	services := make([]SMSService, 0, len(payload))
	for code, name := range payload {
		services = append(services, SMSService{Code: code, Name: name})
	}
	return normalizeSMSServices(services), nil
}

func normalizeSMSServices(services []SMSService) []SMSService {
	normalized := make([]SMSService, 0, len(services))
	for _, service := range services {
		service.Code = strings.TrimSpace(service.Code)
		if service.Code == "" {
			continue
		}
		service.Name = strings.TrimSpace(service.Name)
		normalized = append(normalized, service)
	}
	return normalized
}

func (c *HTTPClient) ListSMSOperators(ctx context.Context, countryID int) ([]string, error) {
	if countryID < 0 {
		return nil, ErrInvalidRequest
	}
	body, err := c.doSMSActivate(ctx, "getOperators", url.Values{
		"country": {strconv.Itoa(countryID)},
	})
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(body)) == "OPERATORS_NOT_FOUND" {
		return []string{}, nil
	}
	var payload struct {
		Status           string              `json:"status"`
		CountryOperators map[string][]string `json:"countryOperators"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || !strings.EqualFold(strings.TrimSpace(payload.Status), "success") {
		return nil, ErrBadResponse
	}
	operators := payload.CountryOperators[strconv.Itoa(countryID)]
	normalized := make([]string, 0, len(operators))
	seen := make(map[string]struct{}, len(operators))
	for _, operator := range operators {
		operator = strings.TrimSpace(operator)
		if operator == "" || strings.EqualFold(operator, "any") || len(operator) > 64 || strings.Contains(operator, ",") {
			continue
		}
		key := strings.ToLower(operator)
		if _, exists := seen[key]; exists {
			continue
		}
		if len(normalized) >= maxSMSOperators {
			return nil, ErrBadResponse
		}
		seen[key] = struct{}{}
		normalized = append(normalized, operator)
	}
	sort.Slice(normalized, func(i int, j int) bool {
		return strings.ToLower(normalized[i]) < strings.ToLower(normalized[j])
	})
	return normalized, nil
}

func (c *HTTPClient) GetSMSOffer(ctx context.Context, countryID int, service string) (*SMSOffer, error) {
	service = strings.TrimSpace(service)
	if countryID < 0 || service == "" || len(service) > 64 {
		return nil, ErrInvalidRequest
	}
	body, err := c.doSMSAPIv1(ctx, "/api/v1/activations/offers/sms", url.Values{
		"countries": {strconv.Itoa(countryID)},
		"services":  {service},
	})
	if err != nil {
		return nil, err
	}
	var payload struct {
		Data map[string]map[string]struct {
			Counts struct {
				Total        int `json:"total"`
				DefaultPrice int `json:"defaultPrice"`
			} `json:"counts"`
			Prices struct {
				Default json.Number `json:"default"`
				Retail  json.Number `json:"retail"`
				Min     json.Number `json:"min"`
			} `json:"prices"`
			Map map[string]int `json:"map"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, ErrBadResponse
	}
	serviceOffers, ok := payload.Data[service]
	if !ok {
		return nil, ErrNoSMSNumbersAvailable
	}
	item, ok := serviceOffers[strconv.Itoa(countryID)]
	if !ok || item.Counts.Total <= 0 {
		return nil, ErrNoSMSNumbersAvailable
	}

	tierCounts := make(map[string]int, len(item.Map))
	tierPrices := make(map[string]decimal.Decimal, len(item.Map))
	for rawPrice, count := range item.Map {
		price, valid := parseSMSPriceValue(rawPrice)
		if !valid || count <= 0 {
			continue
		}
		key := price.String()
		if count > tierCounts[key] {
			_, exists := tierCounts[key]
			if !exists && len(tierCounts) >= maxSMSOfferPriceTiers {
				return nil, ErrBadResponse
			}
			tierCounts[key] = count
			tierPrices[key] = price
		}
	}
	tiers := make([]SMSPriceTier, 0, len(tierCounts)+1)
	for key, count := range tierCounts {
		tiers = append(tiers, SMSPriceTier{
			Count:      count,
			Price:      tierPrices[key],
			PriceValue: key,
		})
	}
	if len(tiers) == 0 {
		price, valid := parseSMSPriceValue(item.Prices.Default.String())
		if !valid {
			return nil, ErrBadResponse
		}
		count := item.Counts.DefaultPrice
		if count <= 0 {
			return nil, ErrBadResponse
		}
		tiers = append(tiers, SMSPriceTier{
			Count:      count,
			Price:      price,
			PriceValue: price.String(),
		})
	}
	sort.Slice(tiers, func(i int, j int) bool {
		return tiers[i].Price.LessThan(tiers[j].Price)
	})
	previousCount := 0
	for _, tier := range tiers {
		if tier.Count < previousCount {
			return nil, ErrBadResponse
		}
		previousCount = tier.Count
	}
	return &SMSOffer{
		CountryID:  countryID,
		Service:    service,
		Count:      tiers[0].Count,
		Price:      tiers[0].Price,
		PriceValue: tiers[0].PriceValue,
		Tiers:      tiers,
	}, nil
}

func (c *HTTPClient) PurchaseSMSActivation(ctx context.Context, request SMSPurchaseRequest) (*SMSActivation, error) {
	request.Service = strings.TrimSpace(request.Service)
	request.Operator = strings.TrimSpace(request.Operator)
	if request.CountryID < 0 || request.Service == "" || request.MaxPrice.LessThanOrEqual(decimal.Zero) || request.CurrencyCode <= 0 {
		return nil, ErrInvalidRequest
	}
	query := url.Values{
		"country":  {strconv.Itoa(request.CountryID)},
		"service":  {request.Service},
		"maxPrice": {request.MaxPrice.String()},
		"currency": {strconv.Itoa(request.CurrencyCode)},
	}
	if request.Operator != "" {
		query.Set("operator", request.Operator)
	}
	body, err := c.doSMSActivate(ctx, "getNumberV2", query)
	if err != nil {
		return nil, err
	}
	var payload struct {
		ActivationID       json.Number `json:"activationId"`
		PhoneNumber        string      `json:"phoneNumber"`
		ActivationCost     json.Number `json:"activationCost"`
		CurrencyCode       int         `json:"currencyCode"`
		Currency           int         `json:"currency"`
		CountryCode        int         `json:"countryCode"`
		CanGetAnotherSMS   bool        `json:"canGetAnotherSms"`
		ActivationTime     string      `json:"activationTime"`
		ActivationEndTime  string      `json:"activationEndTime"`
		ActivationOperator string      `json:"activationOperator"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, ErrBadResponse
	}
	cost, err := decimal.NewFromString(payload.ActivationCost.String())
	if err != nil || payload.ActivationID.String() == "" || strings.TrimSpace(payload.PhoneNumber) == "" || cost.LessThanOrEqual(decimal.Zero) {
		return nil, ErrBadResponse
	}
	currencyCode := payload.CurrencyCode
	if currencyCode == 0 {
		currencyCode = payload.Currency
	}
	return &SMSActivation{
		ID:                 payload.ActivationID.String(),
		PhoneNumber:        strings.TrimSpace(payload.PhoneNumber),
		ActivationCost:     cost,
		CostValue:          cost.String(),
		CurrencyCode:       currencyCode,
		CountryCode:        payload.CountryCode,
		CanGetAnother:      payload.CanGetAnotherSMS,
		ActivationTime:     payload.ActivationTime,
		ActivationEndTime:  payload.ActivationEndTime,
		ActivationOperator: payload.ActivationOperator,
	}, nil
}

func (c *HTTPClient) GetSMSActivationStatus(ctx context.Context, activationID string) (*SMSStatus, error) {
	activationID = strings.TrimSpace(activationID)
	if activationID == "" {
		return nil, ErrInvalidRequest
	}
	body, err := c.doSMSActivate(ctx, "getStatusV2", url.Values{"id": {activationID}})
	if err != nil {
		return nil, err
	}
	var payload struct {
		SMS *SMSStatus `json:"sms"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, ErrBadResponse
	}
	if payload.SMS == nil {
		return &SMSStatus{}, nil
	}
	payload.SMS.Code = strings.TrimSpace(payload.SMS.Code)
	payload.SMS.Text = strings.TrimSpace(payload.SMS.Text)
	return payload.SMS, nil
}

func (c *HTTPClient) ListActiveSMSActivations(ctx context.Context) ([]SMSActiveActivation, error) {
	body, err := c.doSMSActivate(ctx, "getActiveActivations", nil)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Data []struct {
			ActivationID   json.Number `json:"activationId"`
			ServiceCode    string      `json:"serviceCode"`
			PhoneNumber    string      `json:"phoneNumber"`
			ActivationCost json.Number `json:"activationCost"`
			Currency       int         `json:"currency"`
			Status         int         `json:"activationStatus"`
			SMSCode        string      `json:"smsCode"`
			SMSText        string      `json:"smsText"`
			ActivationTime string      `json:"activationTime"`
			CountryCode    int         `json:"countryCode"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, ErrBadResponse
	}
	activations := make([]SMSActiveActivation, 0, len(payload.Data))
	for _, item := range payload.Data {
		cost, err := decimal.NewFromString(item.ActivationCost.String())
		if err != nil || item.ActivationID.String() == "" {
			continue
		}
		activations = append(activations, SMSActiveActivation{
			ID:             item.ActivationID.String(),
			Service:        strings.TrimSpace(item.ServiceCode),
			PhoneNumber:    strings.TrimSpace(item.PhoneNumber),
			ActivationCost: cost,
			CostValue:      cost.String(),
			CurrencyCode:   item.Currency,
			Status:         item.Status,
			SMSCode:        strings.TrimSpace(item.SMSCode),
			SMSText:        strings.TrimSpace(item.SMSText),
			ActivationTime: item.ActivationTime,
			CountryCode:    item.CountryCode,
		})
	}
	return activations, nil
}

func (c *HTTPClient) SetSMSActivationStatus(ctx context.Context, activationID string, status int) error {
	activationID = strings.TrimSpace(activationID)
	if activationID == "" || (status != 3 && status != 6 && status != 8) {
		return ErrInvalidRequest
	}
	body, err := c.doSMSActivate(ctx, "setStatus", url.Values{
		"id":     {activationID},
		"status": {strconv.Itoa(status)},
	})
	if err != nil {
		return err
	}
	expectedResponse := map[int]string{
		3: "ACCESS_RETRY_GET",
		6: "ACCESS_ACTIVATION",
		8: "ACCESS_CANCEL",
	}[status]
	if strings.TrimSpace(string(body)) != expectedResponse {
		return ErrBadResponse
	}
	return nil
}

func (c *HTTPClient) doSMSAPIv1(ctx context.Context, path string, query url.Values) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: request context is required", ErrInvalidRequest)
	}
	if !strings.HasPrefix(path, "/api/v1/") || strings.TrimSpace(c.apiKey) == "" {
		return nil, ErrInvalidRequest
	}
	base, err := url.Parse(c.baseURL)
	if err != nil || base.Scheme != "https" && base.Scheme != "http" || base.Host == "" {
		return nil, ErrInvalidRequest
	}
	base.Path = path
	base.RawQuery = query.Encode()
	base.Fragment = ""

	requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, base.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "ApiKey "+c.apiKey)
	response, err := c.httpClient.Do(request)
	if err != nil {
		if isTimeoutError(err) || errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
			return nil, ErrUpstreamTimeout
		}
		return nil, ErrUpstreamBusy
	}
	defer response.Body.Close()
	if err := common.LimitResponseBody(response, c.bodyLimit); err != nil {
		return nil, ErrBadResponse
	}
	body, err := common.ReadAllLimit(response.Body, c.bodyLimit)
	if err != nil {
		return nil, ErrBadResponse
	}
	switch response.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, ErrUnauthorized
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return nil, ErrInvalidRequest
	case http.StatusOK:
		return body, nil
	default:
		return nil, ErrUpstreamBusy
	}
}

func (c *HTTPClient) doSMSActivate(ctx context.Context, action string, query url.Values) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: request context is required", ErrInvalidRequest)
	}
	base, err := url.Parse(c.baseURL)
	if err != nil || base.Scheme != "https" && base.Scheme != "http" || base.Host == "" {
		return nil, ErrInvalidRequest
	}
	if query == nil {
		query = make(url.Values)
	}
	query.Set("action", action)
	query.Set("api_key", c.apiKey)
	base.Path = "/stubs/handler_api.php"
	base.RawQuery = query.Encode()
	base.Fragment = ""

	requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, base.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json, text/plain")
	response, err := c.httpClient.Do(request)
	if err != nil {
		if isTimeoutError(err) || errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
			return nil, ErrUpstreamTimeout
		}
		return nil, ErrUpstreamBusy
	}
	defer response.Body.Close()
	if err := common.LimitResponseBody(response, c.bodyLimit); err != nil {
		return nil, ErrBadResponse
	}
	body, err := common.ReadAllLimit(response.Body, c.bodyLimit)
	if err != nil {
		return nil, ErrBadResponse
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, ErrUpstreamBusy
	}
	trimmed := strings.TrimSpace(string(body))
	switch {
	case trimmed == "BAD_KEY":
		return nil, ErrUnauthorized
	case trimmed == "NO_NUMBERS":
		return nil, ErrNoSMSNumbersAvailable
	case trimmed == "NO_BALANCE":
		return nil, ErrProviderBalanceInsufficient
	case strings.HasPrefix(trimmed, "BAD_") || strings.HasPrefix(trimmed, "WRONG_"):
		return nil, ErrInvalidRequest
	case trimmed == "ERROR_SQL":
		return nil, ErrUpstreamBusy
	}
	return body, nil
}
