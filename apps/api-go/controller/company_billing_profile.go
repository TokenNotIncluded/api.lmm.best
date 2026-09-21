package controller

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

type companyBillingProfileRequest struct {
	Country        *string `json:"country"`
	IsBusiness     *bool   `json:"isBusiness"`
	Postcode       *string `json:"postcode"`
	State          *string `json:"state"`
	BusinessName   *string `json:"businessName"`
	TaxID          *string `json:"taxId"`
	UseForInvoices *bool   `json:"useForInvoices"`
}

func companyBillingProfileFailure(c *gin.Context, status int, message string, fieldErrors map[string]string) {
	response := gin.H{"success": false, "message": message}
	if len(fieldErrors) > 0 {
		response["errors"] = fieldErrors
	}
	c.JSON(status, response)
}

func decodeCompanyBillingProfileRequest(c *gin.Context) (companyBillingProfileRequest, error) {
	var request companyBillingProfileRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return companyBillingProfileRequest{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return companyBillingProfileRequest{}, errors.New("request must contain one JSON object")
	}
	return request, nil
}

func companyBillingOptionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func GetCompanyBillingProfile(c *gin.Context) {
	profile, err := model.GetCompanyBillingProfile(c.GetInt("id"))
	if errors.Is(err, model.ErrCompanyBillingProfileNotFound) {
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": nil})
		return
	}
	if err != nil {
		companyBillingProfileFailure(c, http.StatusInternalServerError, "Unable to load company billing profile", nil)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": profile})
}

func PutCompanyBillingProfile(c *gin.Context) {
	request, err := decodeCompanyBillingProfileRequest(c)
	if err != nil {
		companyBillingProfileFailure(c, http.StatusBadRequest, "Invalid company billing profile request", nil)
		return
	}
	fieldErrors := make(map[string]string, 3)
	if request.Country == nil {
		fieldErrors["country"] = "required"
	}
	if request.IsBusiness == nil {
		fieldErrors["isBusiness"] = "required"
	}
	if request.UseForInvoices == nil {
		fieldErrors["useForInvoices"] = "required"
	}
	if len(fieldErrors) > 0 {
		companyBillingProfileFailure(c, http.StatusUnprocessableEntity, "Invalid company billing profile", fieldErrors)
		return
	}

	profile, err := model.SaveCompanyBillingProfile(c.GetInt("id"), model.CompanyBillingProfileInput{
		Country:        *request.Country,
		IsBusiness:     *request.IsBusiness,
		Postcode:       companyBillingOptionalString(request.Postcode),
		State:          companyBillingOptionalString(request.State),
		BusinessName:   companyBillingOptionalString(request.BusinessName),
		TaxID:          companyBillingOptionalString(request.TaxID),
		UseForInvoices: *request.UseForInvoices,
	})
	if err != nil {
		var fieldError *model.CompanyBillingProfileFieldError
		if errors.As(err, &fieldError) {
			companyBillingProfileFailure(c, http.StatusUnprocessableEntity, "Invalid company billing profile", map[string]string{
				fieldError.Field: fieldError.Code,
			})
			return
		}
		companyBillingProfileFailure(c, http.StatusInternalServerError, "Unable to save company billing profile", nil)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": profile})
}
