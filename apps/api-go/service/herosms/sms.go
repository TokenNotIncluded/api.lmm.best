package herosms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/shopspring/decimal"
)

var (
	ErrNoSMSNumbersAvailable       = errors.New("hero sms has no matching phone numbers")
	ErrProviderBalanceInsufficient = errors.New("hero sms provider balance is insufficient")
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

type SMSOffer struct {
	CountryID  int             `json:"country_id"`
	Service    string          `json:"service"`
	Count      int             `json:"count"`
	Price      decimal.Decimal `json:"-"`
	PriceValue string          `json:"price"`
}

type SMSPurchaseRequest struct {
	CountryID int
	Service   string
	Operator  string
	MaxPrice  decimal.Decimal
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

func (c *HTTPClient) GetSMSOffer(ctx context.Context, countryID int, service string) (*SMSOffer, error) {
	service = strings.TrimSpace(service)
	if countryID < 0 || service == "" {
		return nil, ErrInvalidRequest
	}
	query := url.Values{
		"country": {strconv.Itoa(countryID)},
		"service": {service},
	}
	body, err := c.doSMSActivate(ctx, "getPrices", query)
	if err != nil {
		return nil, err
	}
	payload := make(map[string]map[string]struct {
		Cost  json.Number `json:"cost"`
		Count int         `json:"count"`
	})
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, ErrBadResponse
	}
	countryOffers, ok := payload[strconv.Itoa(countryID)]
	if !ok {
		return nil, ErrNoSMSNumbersAvailable
	}
	item, ok := countryOffers[service]
	if !ok || item.Count <= 0 {
		return nil, ErrNoSMSNumbersAvailable
	}
	price, err := decimal.NewFromString(item.Cost.String())
	if err != nil || price.LessThanOrEqual(decimal.Zero) {
		return nil, ErrBadResponse
	}
	return &SMSOffer{
		CountryID:  countryID,
		Service:    service,
		Count:      item.Count,
		Price:      price,
		PriceValue: price.String(),
	}, nil
}

func (c *HTTPClient) PurchaseSMSActivation(ctx context.Context, request SMSPurchaseRequest) (*SMSActivation, error) {
	request.Service = strings.TrimSpace(request.Service)
	request.Operator = strings.TrimSpace(request.Operator)
	if request.CountryID < 0 || request.Service == "" || request.MaxPrice.LessThanOrEqual(decimal.Zero) {
		return nil, ErrInvalidRequest
	}
	query := url.Values{
		"country":    {strconv.Itoa(request.CountryID)},
		"service":    {request.Service},
		"maxPrice":   {request.MaxPrice.String()},
		"fixedPrice": {"true"},
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
	return &SMSActivation{
		ID:                 payload.ActivationID.String(),
		PhoneNumber:        strings.TrimSpace(payload.PhoneNumber),
		ActivationCost:     cost,
		CostValue:          cost.String(),
		CurrencyCode:       payload.CurrencyCode,
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
	if !strings.HasPrefix(strings.TrimSpace(string(body)), "ACCESS_") {
		return ErrBadResponse
	}
	return nil
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
