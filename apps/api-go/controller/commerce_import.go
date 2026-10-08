package controller

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/internal/commerceimport"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

func commerceImportRespond(c *gin.Context, value any, err error) {
	service.OAuthNoStore(c.Writer)
	if err == nil {
		merchantStoreRespond(c, value, nil)
		return
	}
	status, code := http.StatusConflict, "COMMERCE_IMPORT_UNAVAILABLE"
	var protocol *commerceimport.ProtocolError
	switch {
	case errors.As(err, &protocol):
		code = "COMMERCE_IMPORT_" + commerceimport.ErrorCode(err)
		status = http.StatusUnprocessableEntity
		if protocol.StatusCode >= 400 && protocol.StatusCode <= 599 {
			status = protocol.StatusCode
		}
	case errors.Is(err, service.ErrCommerceImportConfiguration):
		status, code = http.StatusServiceUnavailable, "COMMERCE_IMPORT_CONFIGURATION_REQUIRED"
	case errors.Is(err, service.ErrCommerceImportReauthorize), errors.Is(err, model.ErrCommerceImportReauthorize):
		code = "COMMERCE_IMPORT_REAUTHORIZE"
	case errors.Is(err, model.ErrCommerceImportLease):
		code = "COMMERCE_IMPORT_CONNECTION_BUSY"
	case errors.Is(err, service.ErrCommerceImportManualRecovery):
		code = "COMMERCE_IMPORT_MANUAL_RECOVERY"
	default:
		merchantStoreRespond(c, nil, err)
		return
	}
	response := gin.H{"success": false, "code": code, "message": "The external shop request could not be completed. Review the connection and saved request before continuing."}
	if protocol != nil {
		response["error"] = commerceimport.ErrorCode(err)
	}
	if errors.Is(err, service.ErrCommerceImportManualRecovery) {
		response["error"] = "manual_recovery"
	}
	if requestID := c.GetString("commerce_import_request_id"); requestID != "" {
		response["request_id"] = requestID
	}
	c.AbortWithStatusJSON(status, response)
}

// Body decoding is strict and bounded by each route. Issuer, connection and
// merchant IDs are always resolved from authentication rather than JSON fields.
func commerceImportBind(c *gin.Context, destination any) bool {
	if c.Request.URL.RawQuery != "" || c.Request.URL.ForceQuery {
		commerceImportRespond(c, nil, model.ErrMerchantStoreInput)
		return false
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, (64<<10)+1))
	if err != nil || len(body) > 64<<10 {
		commerceImportRespond(c, nil, model.ErrMerchantStoreInput)
		return false
	}
	guard := json.NewDecoder(bytes.NewReader(body))
	guard.UseNumber()
	value, valid := commerceImportJSONValue(guard, 0)
	_, object := value.(map[string]any)
	if !valid || !object || guard.Decode(new(any)) != io.EOF || !commerceImportJSONShape(value, reflect.TypeOf(destination)) {
		commerceImportRespond(c, nil, model.ErrMerchantStoreInput)
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		commerceImportRespond(c, nil, model.ErrMerchantStoreInput)
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		commerceImportRespond(c, nil, model.ErrMerchantStoreInput)
		return false
	}
	return true
}

// Reject duplicate names at every object depth before struct decoding. Go's
// normal decoder accepts duplicate fields and case-insensitive aliases, which
// must not turn a reviewed price, permission or quantity into another value.
func commerceImportJSONValue(decoder *json.Decoder, depth int) (any, bool) {
	if depth > 32 {
		return nil, false
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, false
	}
	delim, container := token.(json.Delim)
	if !container {
		return token, true
	}
	switch delim {
	case '{':
		object := map[string]any{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			key, ok := keyToken.(string)
			if err != nil || !ok {
				return nil, false
			}
			if _, exists := object[key]; exists {
				return nil, false
			}
			value, ok := commerceImportJSONValue(decoder, depth+1)
			if !ok {
				return nil, false
			}
			object[key] = value
		}
		closing, err := decoder.Token()
		return object, err == nil && closing == json.Delim('}')
	case '[':
		array := []any{}
		for decoder.More() {
			value, ok := commerceImportJSONValue(decoder, depth+1)
			if !ok {
				return nil, false
			}
			array = append(array, value)
		}
		closing, err := decoder.Token()
		return array, err == nil && closing == json.Delim(']')
	default:
		return nil, false
	}
}

func commerceImportJSONShape(value any, kind reflect.Type) bool {
	if value == nil {
		return kind.Kind() == reflect.Pointer
	}
	for kind.Kind() == reflect.Pointer {
		kind = kind.Elem()
	}
	if object, ok := value.(map[string]any); ok {
		if kind.Kind() != reflect.Struct {
			return false
		}
		fields := map[string]reflect.Type{}
		for index := 0; index < kind.NumField(); index++ {
			field := kind.Field(index)
			key := strings.Split(field.Tag.Get("json"), ",")[0]
			if key != "" && key != "-" {
				fields[key] = field.Type
			}
		}
		for key, child := range object {
			field, present := fields[key]
			if !present || !commerceImportJSONShape(child, field) {
				return false
			}
		}
	}
	if array, ok := value.([]any); ok {
		if kind.Kind() != reflect.Slice && kind.Kind() != reflect.Array {
			return false
		}
		for _, child := range array {
			if !commerceImportJSONShape(child, kind.Elem()) {
				return false
			}
		}
	}
	return true
}

func GetCommerceImportConfiguration(c *gin.Context) {
	commerceImportRespond(c, service.GetCommerceImportConfiguration(), nil)
}

func ListCommerceImportConnections(c *gin.Context) {
	connections, err := model.ListCommerceImportConnections(c.GetInt("id"))
	if connections == nil {
		connections = []model.MerchantStoreCommerceConnection{}
	}
	commerceImportRespond(c, connections, err)
}

func CreateCommerceImportConnection(c *gin.Context) {
	var input struct {
		Origin   string `json:"origin"`
		ClientID string `json:"client_id"`
	}
	if !commerceImportBind(c, &input) {
		return
	}
	connection, err := service.CreateCommerceImportConnection(c.Request.Context(), c.GetInt("id"), input.Origin, input.ClientID)
	commerceImportRespond(c, connection, err)
}

func AuthorizeCommerceImportConnection(c *gin.Context) {
	var input struct {
		CardsIssue bool `json:"cards_issue"`
	}
	if !commerceImportBind(c, &input) {
		return
	}
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok {
		commerceImportRespond(c, nil, model.ErrMerchantStoreDenied)
		return
	}
	target, err := service.AuthorizeCommerceImport(c.Request.Context(), identity.UserID, c.Param("connection_id"), identity.SessionID, input.CardsIssue)
	commerceImportRespond(c, gin.H{"authorization_url": target}, err)
}

func GetCommerceImportCatalog(c *gin.Context) {
	catalog, err := service.FetchCommerceImportCatalog(c.Request.Context(), c.GetInt("id"), c.Param("connection_id"))
	commerceImportRespond(c, catalog, err)
}

func ImportCommerceImportProduct(c *gin.Context) {
	var input service.CommerceImportDraftInput
	if !commerceImportBind(c, &input) {
		return
	}
	mapping, err := service.ImportCommerceImportDraft(c.Request.Context(), c.GetInt("id"), c.Param("connection_id"), input)
	if err != nil {
		commerceImportRespond(c, nil, err)
		return
	}
	commerceImportRespond(c, gin.H{"product_id": mapping.LocalProductID}, nil)
}

func RestockCommerceImportProduct(c *gin.Context) {
	var input service.CommerceImportRestockInput
	if !commerceImportBind(c, &input) {
		return
	}
	request, err := service.RestockCommerceImport(c.Request.Context(), c.GetInt("id"), c.Param("connection_id"), input)
	if request != nil && request.ID != "" {
		c.Set("commerce_import_request_id", request.ID)
	}
	commerceImportRespond(c, request, err)
}

func ListCommerceImportRestockRequests(c *gin.Context) {
	requests, err := model.ListCommerceImportRestockRequests(c.GetInt("id"), c.Param("connection_id"))
	if requests == nil {
		requests = []model.MerchantStoreCommerceRestockRequest{}
	}
	commerceImportRespond(c, requests, err)
}

func RecoverCommerceImportRestock(c *gin.Context) {
	if !commerceImportBind(c, &struct{}{}) {
		return
	}
	request, err := service.RecoverCommerceImportRestock(c.Request.Context(), c.GetInt("id"), c.Param("connection_id"), c.Param("request_id"))
	if request != nil && request.ID != "" {
		c.Set("commerce_import_request_id", request.ID)
	}
	commerceImportRespond(c, request, err)
}

func DisconnectCommerceImportConnection(c *gin.Context) {
	commerceImportRespond(c, nil, service.DisconnectCommerceImport(c.Request.Context(), c.GetInt("id"), c.Param("connection_id")))
}

// This is a standalone page with no app shell, analytics, images or third-party
// resources. The first cross-site navigation cannot use the Strict login cookie;
// after replacing the URL, a same-origin fetch can validate the ordinary login.
func CommerceImportCallback(c *gin.Context) {
	service.OAuthNoStore(c.Writer)
	actual, err := service.CommerceImportRequestURL(c.Request.Host, c.Request.URL.EscapedPath(), c.Request.URL.RawQuery)
	if err != nil || len(actual) > 8192 {
		commerceImportRespond(c, nil, commerceimport.ErrInvalidCallback)
		return
	}
	var random [24]byte
	if _, err = rand.Read(random[:]); err != nil {
		commerceImportRespond(c, nil, model.ErrMerchantStoreUnavailable)
		return
	}
	nonce := base64.RawURLEncoding.EncodeToString(random[:])
	c.Header("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+nonce+"'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	payload, _ := json.Marshal(gin.H{"callback_url": actual})
	html := `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="referrer" content="no-referrer"><title>Connect external shop</title><p>Completing the shop connection…</p><script nonce="` + nonce + `">history.replaceState(null,"",` + `"` + service.CommerceImportCallbackPath + `"` + `);fetch("` + service.CommerceImportCompletePath + `",{method:"POST",credentials:"same-origin",headers:{"Content-Type":"application/json"},body:JSON.stringify(` + string(payload) + `)}).then(r=>r.json()).then(r=>{const outcome=r.success&&r.data&&r.data.outcome;location.replace("/store/manage?commerce_import="+(outcome==="connected"?"connected":outcome==="denied"?"denied":"failed"))}).catch(()=>location.replace("/store/manage?commerce_import=failed"));</script><noscript>Enable JavaScript to finish this connection, then return to product management.</noscript></html>`
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
}

func CompleteCommerceImportCallback(c *gin.Context) {
	service.OAuthNoStore(c.Writer)
	actor, sessionID, err := service.CommerceImportCallbackIdentity(c.Request)
	if err != nil {
		commerceImportRespond(c, nil, err)
		return
	}
	var input struct {
		CallbackURL string `json:"callback_url"`
	}
	if !commerceImportBind(c, &input) {
		return
	}
	outcome, err := service.CompleteCommerceImportCallback(c.Request.Context(), actor, sessionID, input.CallbackURL)
	commerceImportRespond(c, gin.H{"outcome": outcome}, err)
}
