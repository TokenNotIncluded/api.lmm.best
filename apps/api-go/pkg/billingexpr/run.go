package billingexpr

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"github.com/tidwall/gjson"
)

// RunExpr compiles (with cache) and executes an expression string.
// The environment exposes:
//   - p, c             — prompt / completion tokens (auto-excluding separately-priced sub-categories)
//   - len              — total input context length for tier conditions (never reduced by sub-category exclusion)
//   - cr, cc, cc1h     — cache read / creation / creation-1h tokens
//   - cr_text, cr_img, cr_audio — optional measured cache-read modality tokens
//   - audio_s          — optional measured audio/session seconds; a $/minute
//     price uses coefficient price * 1,000,000 / 60 in the existing v1 units
//   - tier(name, value) — trace callback that records which tier matched
//   - max, min, abs, ceil, floor — standard math helpers
//
// Returns the resulting float64 quota (before group ratio) and a TraceResult
// with side-channel info captured by tier() during execution.
func RunExpr(exprStr string, params TokenParams) (float64, TraceResult, error) {
	return RunExprWithRequest(exprStr, params, RequestInput{})
}

func RunExprWithRequest(exprStr string, params TokenParams, request RequestInput) (float64, TraceResult, error) {
	entry, err := compileEntryFromCacheByHash(exprStr, ExprHashString(exprStr))
	if err != nil {
		return 0, TraceResult{}, err
	}
	return runProgram(entry.prog, entry.requestRules, entry.usedVars, params, request)
}

// RunExprByHash is like RunExpr but accepts a pre-computed hash for the cache
// lookup, avoiding a redundant SHA-256 computation when the caller already
// holds BillingSnapshot.ExprHash.
func RunExprByHash(exprStr, hash string, params TokenParams) (float64, TraceResult, error) {
	return RunExprByHashWithRequest(exprStr, hash, params, RequestInput{})
}

func RunExprByHashWithRequest(exprStr, hash string, params TokenParams, request RequestInput) (float64, TraceResult, error) {
	entry, err := compileEntryFromCacheByHash(exprStr, hash)
	if err != nil {
		return 0, TraceResult{}, err
	}
	return runProgram(entry.prog, entry.requestRules, entry.usedVars, params, request)
}

func runProgram(prog *vm.Program, requestRules []RequestRuleTrace, usedVars map[string]bool, params TokenParams, request RequestInput) (float64, TraceResult, error) {
	return runProgramAt(prog, requestRules, usedVars, params, request, time.Now())
}

func runProgramAt(prog *vm.Program, requestRules []RequestRuleTrace, usedVars map[string]bool, params TokenParams, request RequestInput, at time.Time) (float64, TraceResult, error) {
	trace := TraceResult{
		RequestRules: append([]RequestRuleTrace(nil), requestRules...),
	}
	if params.MeasurementError != "" {
		return 0, trace, fmt.Errorf("expr usage error: %s", params.MeasurementError)
	}
	measured := map[string]*float64{
		"cr_text": params.CRText, "cr_img": params.CRImg, "cr_audio": params.CRAudio,
		"audio_s": params.AudioSeconds,
	}
	for variable, value := range measured {
		if !usedVars[variable] {
			continue
		}
		if value == nil {
			return 0, trace, fmt.Errorf("expr usage error: measured dimension %s is unavailable", variable)
		}
		if math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 {
			return 0, trace, fmt.Errorf("expr usage error: measured dimension %s must be finite and nonnegative", variable)
		}
	}
	headers := normalizeHeaders(request.Headers)

	env := map[string]interface{}{
		"p":        params.P,
		"c":        params.C,
		"len":      params.Len,
		"cr":       params.CR,
		"cr_text":  measuredDimensionValue(params.CRText),
		"cr_img":   measuredDimensionValue(params.CRImg),
		"cr_audio": measuredDimensionValue(params.CRAudio),
		"cc":       params.CC,
		"cc1h":     params.CC1h,
		"img":      params.Img,
		"img_o":    params.ImgO,
		"ai":       params.AI,
		"ao":       params.AO,
		"audio_s":  measuredDimensionValue(params.AudioSeconds),
		"tier": func(name string, value float64) float64 {
			trace.MatchedTier = name
			trace.Cost = value
			return value
		},
		requestRuleTraceFunction: func(ruleIndex int, matched bool, multiplier float64) float64 {
			if matched && ruleIndex >= 0 && ruleIndex < len(trace.RequestRules) {
				trace.RequestRules[ruleIndex].Matched = true
			}
			if matched {
				return multiplier
			}
			return 1
		},
		requestRuleTraceIntFunction: func(ruleIndex int, matched bool, multiplier int) int {
			if matched && ruleIndex >= 0 && ruleIndex < len(trace.RequestRules) {
				trace.RequestRules[ruleIndex].Matched = true
			}
			if matched {
				return multiplier
			}
			return 1
		},
		"header": func(key string) string {
			return headers[strings.ToLower(strings.TrimSpace(key))]
		},
		"param": func(path string) interface{} {
			path = strings.TrimSpace(path)
			if path == "" || len(request.Body) == 0 {
				return nil
			}
			result := gjson.GetBytes(request.Body, path)
			if !result.Exists() {
				return nil
			}
			return result.Value()
		},
		"has": func(source interface{}, substr string) bool {
			if source == nil || substr == "" {
				return false
			}
			return strings.Contains(fmt.Sprint(source), substr)
		},
		"hour":    func(tz string) int { return timeInZoneAt(at, tz).Hour() },
		"minute":  func(tz string) int { return timeInZoneAt(at, tz).Minute() },
		"weekday": func(tz string) int { return int(timeInZoneAt(at, tz).Weekday()) },
		"month":   func(tz string) int { return int(timeInZoneAt(at, tz).Month()) },
		"day":     func(tz string) int { return timeInZoneAt(at, tz).Day() },
		"date":    func(tz string) int { return calendarDate(timeInZoneAt(at, tz)) },
		"max":     math.Max,
		"min":     math.Min,
		"abs":     math.Abs,
		"ceil":    math.Ceil,
		"floor":   math.Floor,
	}

	out, err := expr.Run(prog, env)
	if err != nil {
		return 0, trace, fmt.Errorf("expr run error: %w", err)
	}
	f, ok := out.(float64)
	if !ok {
		return 0, trace, fmt.Errorf("expr result is %T, want float64", out)
	}
	return f, trace, nil
}

func calendarDate(at time.Time) int {
	return at.Year()*10000 + int(at.Month())*100 + at.Day()
}

func measuredDimensionValue(value *float64) float64 {
	if value == nil {
		return 0 // Only unused dimensions may reach evaluation without a value.
	}
	return *value
}

func timeInZone(tz string) time.Time {
	return timeInZoneAt(time.Now(), tz)
}

func timeInZoneAt(at time.Time, tz string) time.Time {
	tz = strings.TrimSpace(tz)
	if tz == "" {
		return at.UTC()
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return at.UTC()
	}
	return at.In(loc)
}

func normalizeHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return map[string]string{}
	}
	normalized := make(map[string]string, len(headers))
	for key, value := range headers {
		k := strings.ToLower(strings.TrimSpace(key))
		v := strings.TrimSpace(value)
		if k == "" || v == "" {
			continue
		}
		normalized[k] = v
	}
	return normalized
}
