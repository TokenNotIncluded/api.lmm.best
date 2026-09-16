"""One-shot source repair for PR 335; remove before merging the PR."""
from pathlib import Path
import subprocess

EXPECTED_BLOBS = {
    "apps/api-go/relay/common/relay_info.go": "c8b4a8a71b9043c202c817be3862d00cf176396f",
    "apps/api-go/relay/channel/openai/relay_responses.go": "484a12aacc01711f53e22e9191227e56ecaaa4a5",
    "apps/api-go/service/text_quota.go": "68a1b6ab2e09ccf527b9959ced4975ce56e6bed5",
    "apps/api-go/controller/relay_responses_settlement_test.go": "1a1303d5665f7f5dcca95fe92e3cf7707dce1278",
}
for filename, expected in EXPECTED_BLOBS.items():
    actual = subprocess.check_output(["git", "hash-object", filename], text=True).strip()
    if actual != expected:
        raise SystemExit(f"Refusing to edit changed source: {filename}: {actual}")


def replace_once(filename: str, old: str, new: str) -> None:
    path = Path(filename)
    source = path.read_text()
    if source.count(old) != 1:
        raise SystemExit(f"Expected one exact source match in {filename}")
    path.write_text(source.replace(old, new, 1))


replace_once(
    "apps/api-go/relay/common/relay_info.go",
    "type ResponsesUsageInfo struct {\n\tBuiltInTools map[string]*BuildInToolInfo\n}",
    "type ResponsesUsageInfo struct {\n\tBuiltInTools map[string]*BuildInToolInfo\n"
    "\t// UnsuccessfulStream excludes unmeasured failed/interrupted streams from\n"
    "\t// successful-request missing-usage estimates. Observed usage still settles.\n"
    "\tUnsuccessfulStream bool\n}",
)
replace_once(
    "apps/api-go/relay/channel/openai/relay_responses.go",
    "\tvar usage = &dto.Usage{}\n\tvar responseTextBuilder strings.Builder",
    "\tif info.ResponsesUsageInfo == nil {\n"
    "\t\tinfo.ResponsesUsageInfo = &relaycommon.ResponsesUsageInfo{}\n\t}\n"
    "\t// Acceptance alone is not a successful response. Only a successful\n"
    "\t// terminal event permits missing-usage fallback billing.\n"
    "\tinfo.ResponsesUsageInfo.UnsuccessfulStream = true\n\n"
    "\tvar usage = &dto.Usage{}\n\tvar responseTextBuilder strings.Builder",
)
replace_once(
    "apps/api-go/relay/channel/openai/relay_responses.go",
    '\t\tcase "response.completed", "response.done":\n\t\t\tterminal = true\n',
    '\t\tcase "response.completed", "response.done":\n\t\t\tterminal = true\n'
    "\t\t\tinfo.ResponsesUsageInfo.UnsuccessfulStream = streamResponse.Response != nil &&\n"
    "\t\t\t\trelaycommon.IsNonBillableResponsesStatus(streamResponse.Response.Status)\n",
)
replace_once(
    "apps/api-go/service/text_quota.go",
    "\tif !summary.hasBillableUsage() {\n\t\testimated, samples, estimateErr := model.EstimateRecentModelQuota",
    "\tif !summary.hasBillableUsage() && relayInfo.ResponsesUsageInfo != nil && relayInfo.ResponsesUsageInfo.UnsuccessfulStream {\n"
    "\t\t// Do not charge an interrupted/failed Responses stream merely because\n"
    "\t\t// response.created was forwarded. Measured usage and tool surcharges\n"
    "\t\t// still take the normal settlement path below.\n"
    "\t\tsummary.Quota = 0\n"
    '\t\textraContent = append(extraContent, "Responses 流未成功完成且没有可计费用量，不应用成功请求的缺失用量估算")\n'
    "\t} else if !summary.hasBillableUsage() {\n\t\testimated, samples, estimateErr := model.EstimateRecentModelQuota",
)

path = "apps/api-go/controller/relay_responses_settlement_test.go"
replace_once(
    path,
    '\t\t{"created_unknown", "", false, 0},\n',
    '\t\t{"created_unknown", "", false, 0},\n'
    '\t\t{"created_read_error", "", false, 0},\n'
    '\t\t{"failed_unknown", "data: {\\\"type\\\":\\\"response.failed\\\",\\\"response\\\":{\\\"status\\\":\\\"failed\\\"}}\\n\\n", false, 0},\n'
    '\t\t{"incomplete_unknown", "data: {\\\"type\\\":\\\"response.incomplete\\\",\\\"response\\\":{\\\"status\\\":\\\"incomplete\\\"}}\\n\\n", false, 0},\n'
    '\t\t{"cancelled_unknown", "data: {\\\"type\\\":\\\"response.cancelled\\\",\\\"response\\\":{\\\"status\\\":\\\"cancelled\\\"}}\\n\\n", false, 0},\n'
    '\t\t{"completed_unknown", "data: {\\\"type\\\":\\\"response.completed\\\",\\\"response\\\":{\\\"status\\\":\\\"completed\\\"}}\\n\\n", true, 0},\n'
    '\t\t{"failed_input_usage", "data: {\\\"type\\\":\\\"response.failed\\\",\\\"response\\\":{\\\"status\\\":\\\"failed\\\",\\\"usage\\\":{\\\"input_tokens\\\":100,\\\"output_tokens\\\":0,\\\"total_tokens\\\":100}}}\\n\\n", true, 100},\n',
)
replace_once(
    path,
    '\t\t\t\tif tc.name == "text_read_error" {',
    '\t\t\t\tif tc.name == "text_read_error" || tc.name == "created_read_error" {',
)
replace_once(
    path,
    '\t\t\tif tc.name == "incomplete_usage" {\n\t\t\t\tterminal = "response.incomplete"\n\t\t\t}',
    '\t\t\tswitch tc.name {\n'
    '\t\t\tcase "incomplete_usage", "incomplete_unknown":\n\t\t\t\tterminal = "response.incomplete"\n'
    '\t\t\tcase "cancelled_unknown":\n\t\t\t\tterminal = "response.cancelled"\n'
    '\t\t\tcase "completed_unknown":\n\t\t\t\tterminal = "response.completed"\n\t\t\t}',
)
replace_once(
    path,
    '\t\t\trequire.Equal(t, charge, logs[0].Quota)\n',
    '\t\t\trequire.Equal(t, charge, logs[0].Quota)\n'
    '\t\t\tvar other map[string]any\n'
    '\t\t\trequire.NoError(t, json.Unmarshal([]byte(logs[0].Other), &other))\n'
    '\t\t\tif tc.name == "completed_unknown" {\n'
    '\t\t\t\t// Preserve successful missing-usage billing from #310.\n'
    '\t\t\t\trequire.Equal(t, true, other["usage_estimated"])\n'
    '\t\t\t\trequire.Equal(t, "preconsumed_fallback_no_history", other["usage_estimate_basis"])\n'
    '\t\t\t\trequire.EqualValues(t, charge, other["usage_estimate_cap"])\n'
    '\t\t\t} else if !tc.charged {\n'
    '\t\t\t\trequire.NotContains(t, other, "usage_estimated")\n\t\t\t}\n',
)
subprocess.run(["gofmt", "-w", *EXPECTED_BLOBS], check=True)
print("Applied the scoped unsuccessful-stream billing fix and six regression cases.")
