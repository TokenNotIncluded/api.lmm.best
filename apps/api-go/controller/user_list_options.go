package controller

import (
	"errors"
	"math"
	"strconv"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

func parseUserListOptions(c *gin.Context) (model.UserSortOptions, error) {
	options := model.NewUserSortOptions(c.Query("sort_by"), c.Query("sort_order"))
	for name, target := range map[string]**float64{"risk_min": &options.Filters.RiskMin, "risk_max": &options.Filters.RiskMax} {
		if value := c.Query(name); value != "" {
			parsed, err := strconv.ParseFloat(value, 64)
			if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed < 0 || parsed > 1 {
				return options, errors.New("invalid risk range")
			}
			*target = &parsed
		}
	}
	if options.Filters.RiskMin != nil && options.Filters.RiskMax != nil && *options.Filters.RiskMin > *options.Filters.RiskMax {
		return options, errors.New("invalid risk range")
	}
	for _, filter := range []struct {
		name    string
		target  *string
		allowed []string
	}{
		{"transfers", &options.Filters.Transfers, []string{"sent", "received", "none"}},
		{"usage", &options.Filters.Usage, []string{"zero", "consumed"}},
		{"funding", &options.Filters.Funding, []string{"paid", "unpaid"}},
		{"checkin", &options.Filters.Checkin, []string{"yes", "no"}},
	} {
		value := c.Query(filter.name)
		if value == "" {
			continue
		}
		valid := false
		for _, allowed := range filter.allowed {
			if value == allowed {
				valid = true
				break
			}
		}
		if !valid {
			return options, errors.New("invalid user activity filter")
		}
		*filter.target = value
	}
	return options, nil
}
