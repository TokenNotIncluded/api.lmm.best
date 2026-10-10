# One SVG for multiple AI accounts

Open [Profile → Public SVG badge](https://api.lmm.best/profile/share), choose
**Linked accounts**, and add your public profile URLs. Save the accounts and
enable aggregate sharing, then copy the **GitHub README** embed. The image uses
the existing revocable profile-share URL with `layout=aggregate`.

The LMM account you are signed into is included automatically. You can add up
to five external accounts, including several accounts from the same provider.
The model-usage layout and its separate sharing permission remain available.
Aggregate sharing publishes account links and the usage you import; disable it
to stop serving the aggregate image. Revoking the whole public badge invalidates
its token; enabling it again creates a new URL.

| Source | Usage displayed | Refresh method |
| --- | --- | --- |
| LMM Best | Native token and API request totals for the selected LMM period | Read from this account's stored usage |
| Cursor | Tokens in the public profile's reported date window | Read anonymously from a public `https://cursor.com/@handle` profile |
| ChatGPT | Optional owner-entered tokens, requests, or messages | Import an observed snapshot; the profile URL alone cannot provide anonymous metrics |
| Other provider | Optional owner-entered tokens, requests, or messages | Save a public HTTPS profile link and import a snapshot |

[Cursor documents public profile visibility and activity](https://cursor.com/help/account-and-billing/profiles).
Its current public page embeds structured usage in its HTML, rather than a
documented metrics API. If visibility changes, the page cannot be read, or its
structure changes, the source becomes unavailable. Other accounts still render.

[OpenAI requires sign-in to view ChatGPT profiles](https://help.openai.com/en/articles/20001539-shareable-profiles-in-chatgpt).
Chat message counts and Work/Codex token usage are separate metrics, and usage
belongs to the workspace being viewed. A snapshot source note should identify
that scope, such as **Codex lifetime tokens**. No provider cookies or API keys
are needed or accepted by this feature.

When importing a snapshot, leave unknown metrics blank. Enter the time when
you observed the values, choose the original period, and mark rounded numbers
as approximate. For example, a profile display of `117B` can be entered as
`117000000000` with **approximate** enabled; this preserves the displayed scale
without claiming an exact count. Snapshots retain their observation time and
do not update automatically.

Each row keeps its own period and source. Changing the LMM period does not
change Cursor's reported window or an imported snapshot. The image does not
add different periods into a grand total, and missing values are never reported
as zero. GitHub may cache an embedded image even though the SVG endpoint serves
it with `Cache-Control: no-store`.

## Existing API extension

Authenticated `GET /api/user/self/profile-share` returns the existing badge
state together with `aggregate_usage_enabled`, `linked_profiles`, and resolved
`aggregate_sources`. `POST` to the same endpoint accepts optional settings:

```json
{
  "aggregate_usage_enabled": true,
  "linked_profiles": [
    {
      "provider": "cursor",
      "url": "https://cursor.com/@your-handle"
    }
  ]
}
```

An optional `snapshot` object accepts nullable `tokens`, `requests`, and
`messages`, plus `period`, `observed_at`, `approximate`, an optional `source`
note, and optional `period_start` / `period_end` dates. At least one known
metric is required. Supported periods are `all`, `7d`, `30d`, `365d`, and
`custom`; custom periods require both dates. Observation timestamps use
RFC 3339 and date boundaries use `YYYY-MM-DD`. Limits and canonical URL
validation are enforced before any settings are saved.

Only fields present in the request are changed. Aggregate sharing does not
change `model_usage_enabled`, and aggregate rows do not publish model names or
platform spend. `DELETE /api/user/self/profile-share` continues to revoke the
whole public token. This existing profile-share surface is implemented by the
Go backend.
