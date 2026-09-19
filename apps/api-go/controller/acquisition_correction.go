package controller

import (
	"bytes"
	"encoding/csv"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

func acquisitionCorrectionTarget(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.AbortWithStatus(http.StatusBadRequest)
		return 0, false
	}
	var target model.User
	if err = model.DB.WithContext(c.Request.Context()).Select("id", "role").First(&target, id).Error; err != nil {
		c.AbortWithStatus(http.StatusNotFound)
		return 0, false
	}
	if target.Role >= common.RoleAdminUser || !canManageTargetRole(c.GetInt("role"), target.Role) {
		c.AbortWithStatus(http.StatusForbidden)
		return 0, false
	}
	return id, true
}
func GetAcquisitionCorrections(c *gin.Context) {
	id, ok := acquisitionCorrectionTarget(c)
	if !ok {
		return
	}
	result, err := model.ReadAcquisitionCorrections(c.Request.Context(), id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
func SaveAcquisitionCorrection(c *gin.Context) {
	id, ok := acquisitionCorrectionTarget(c)
	if !ok {
		return
	}
	var input struct {
		Source           string `json:"source"`
		Reason           string `json:"reason"`
		ExpectedRevision int64  `json:"expected_revision"`
	}
	if c.ShouldBindJSON(&input) != nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	result, err := model.SaveAcquisitionCorrection(c.Request.Context(), id, c.GetInt("id"), input.ExpectedRevision, input.Source, input.Reason)
	if errors.Is(err, model.ErrAcquisitionCorrectionConflict) {
		c.JSON(http.StatusConflict, gin.H{"success": false, "message": "Source correction changed; reload before saving"})
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
func acquisitionCSVCell(value string) string {
	trimmed := strings.TrimLeftFunc(value, func(r rune) bool { return unicode.IsSpace(r) || r == '\uFEFF' || r == '\u200B' })
	if len(trimmed) > 0 && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + value
	}
	return value
}
func acquisitionAccountCSV(rows []model.AcquisitionExportRow, source string, from, to int64) ([]byte, error) {
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	_ = writer.Write([]string{"user_id", "registered_at_unix", "observed_source", "evidence", "manual_corrected_source", "correction_revision", "first_observed_success_at_unix", "filter_source", "registration_from_inclusive", "registration_to_exclusive"})
	for _, row := range rows {
		fields := []string{strconv.Itoa(row.UserID), strconv.FormatInt(row.RegisteredAt, 10), row.ObservedSource, row.Evidence, row.CorrectedSource, strconv.FormatInt(row.CorrectionRevision, 10), strconv.FormatInt(row.FirstSuccessAt, 10), source, strconv.FormatInt(from, 10), strconv.FormatInt(to, 10)}
		for index := range fields {
			fields[index] = acquisitionCSVCell(fields[index])
		}
		if err := writer.Write(fields); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	return buffer.Bytes(), writer.Error()
}
func ExportAcquisitionUsers(c *gin.Context) {
	from, _ := strconv.ParseInt(c.Query("from"), 10, 64)
	to, _ := strconv.ParseInt(c.Query("to"), 10, 64)
	source := c.Query("source")
	rows, err := model.ExportAcquisitionUsers(c.Request.Context(), source, from, to)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Unable to export; narrow the registration period (maximum 10000 accounts)"})
		return
	}
	data, err := acquisitionAccountCSV(rows, source, from, to)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	c.Header("Content-Disposition", `attachment; filename="acquisition-accounts.csv"`)
	c.Data(http.StatusOK, "text/csv; charset=utf-8", data)
}
